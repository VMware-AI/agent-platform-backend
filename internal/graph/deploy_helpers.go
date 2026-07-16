package graph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/internal/deploy"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
	"github.com/google/uuid"
)

const (
	toolsTimeout   = 120 * time.Second
	customTimeout  = 180 * time.Second
	ipWaitTimeout  = 180 * time.Second
	ocReadyTimeout = 120 * time.Second
)

func mapOVFProperties(props []model.OVFPropertyInput) map[string]string {
	if len(props) == 0 {
		return nil
	}
	m := make(map[string]string, len(props))
	for _, p := range props {
		if !strings.HasPrefix(p.Key, "guestinfo.") {
			continue
		}
		m[p.Key] = p.Value
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:4] + "****" + key[len(key)-4:]
}

func secureToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// deployAgentInstant: InstantClone → VMware Tools → CustomizeGuest (static IP)
// → StartGuestNetwork → wait IP → GuestInfo (OpenClaw) → ACK → RUNNING.
func (r *mutationResolver) deployAgentInstant(
	ctx context.Context,
	ag *ent.Agent,
	t *deployTargets,
	conn VCenterClient,
	input model.DeployAgentInput,
	vmName string,
	_ string, // enrollToken (unused in new flow)
	_ string, // defaultConfig (unused)
	_ string, // configPath (unused)
) (*model.DeployedAgent, error) {
	parentVM := derefString(input.InstantCloneParent)
	if parentVM == "" {
		return nil, fmt.Errorf("instantCloneParent required")
	}
	deploymentID := uuid.New().String()
	generation := fmt.Sprintf("%d", time.Now().UnixMilli())

	// 1. Gateway key
	key, err := t.gw.GenerateKey(ctx, gateway.GenerateKeyRequest{
		UserID: ag.OwnerUserID.String(), TeamID: t.deployTeamID, Models: nil,
		MaxBudget: input.MaxBudget,
		Metadata:  map[string]any{"agent": ag.Name, "deployment": deploymentID},
	})
	if err != nil {
		r.deleteAgentRow(ctx, ag)
		return nil, fmt.Errorf("generate key: %w", err)
	}

	rollback := func(phase string, vm string) {
		log.Printf("[instant-clone] ROLLBACK phase=%s deployment=%s vm=%s (keeping VM for diagnosis)", phase, deploymentID, vm)
		_ = t.gw.DeleteKey(context.Background(), key.Key)
		r.deleteAgentRow(context.Background(), ag)
		// Keep VM alive for diagnosis
	}

	// 2. CLONING
	ag, err = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusCloning).Save(ctx)
	if err != nil {
		_ = t.gw.DeleteKey(ctx, key.Key); r.deleteAgentRow(ctx, ag)
		return nil, fmt.Errorf("set cloning: %w", err)
	}

	// 3. InstantClone (no DeviceChange — inherit parent NIC as-is)
	icSpec := vcenter.InstantCloneSpec{
		ParentVM:     parentVM, Name: vmName,
		ResourcePool: derefString(input.TargetResourcePool),
		Network:      derefString(input.TargetNetwork),
	}
	if len(input.OvfProperties) > 0 {
		icSpec.ExtraConfig = make(map[string]string, len(input.OvfProperties))
		for _, p := range input.OvfProperties { icSpec.ExtraConfig[p.Key] = p.Value }
	}
	if _, err := conn.InstantClone(ctx, icSpec); err != nil {
		rollback("clone", ""); return nil, fmt.Errorf("instant clone: %w", err)
	}
	log.Printf("[instant-clone] created %s deployment=%s", vmName, deploymentID)

	// 4. Extract config from ovfProperties
	staticIP, netmask, gateway, dns, hostname := "", "255.255.255.0", "", "", ""
	runAsUser, runAsPass := "", ""
	for _, p := range input.OvfProperties {
		switch p.Key {
		case "guestinfo.static_ip": staticIP = p.Value
		case "guestinfo.netmask": netmask = p.Value
		case "guestinfo.gateway": gateway = p.Value
		case "guestinfo.dns": dns = p.Value
		case "guestinfo.run_as_user": runAsUser = p.Value
		case "guestinfo.password": runAsPass = p.Value
		}
	}
	if runAsUser == "" { runAsUser = "vmware" }
	if runAsPass == "" { return nil, fmt.Errorf("guestinfo.password required for instant clone guest customization") }
	if derefString(input.Hostname) != "" { hostname = derefString(input.Hostname) }

	// 5. CustomizeGuest (may return vCenter timeout but IP is actually set)
	ag, err = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusGuestConfiguring).Save(ctx)
	if err != nil { rollback("set-gc", vmName); return nil, fmt.Errorf("set guest_configuring: %w", err) }

	var dnsList []string
	if dns != "" { dnsList = strings.Split(dns, ",") }
	prefixLen := 24
	if netmask == "255.255.0.0" { prefixLen = 16 }

	log.Printf("[instant-clone] customizing guest: ip=%s gw=%s dns=%v", staticIP, gateway, dnsList)
	custErr := conn.CustomizeInstantCloneGuest(ctx, vmName, vcenter.CustomizeGuestRequest{
		Username: runAsUser, Password: runAsPass,
		Hostname: hostname, IPAddress: staticIP,
		PrefixLen: prefixLen, SubnetMask: netmask,
		Gateway: gateway, DNSServers: dnsList,
	})
	if custErr != nil {
		log.Printf("[instant-clone] CustomizeGuest returned: %v (verifying IP directly...)", custErr)
	}

	// 6. Connect NIC — the reconnect triggers guest kernel to detect new MAC.
	// NIC was disconnected during InstantClone, then GOSC configured static IP.
	// ConnectNIC brings the interface up so the guest sees the new MAC+IP.
	ag, err = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusNetworkConnecting).Save(ctx)
	if err != nil { rollback("set-nc", vmName); return nil, fmt.Errorf("set network_connecting: %w", err) }
	if err := conn.ConnectNIC(ctx, vmName); err != nil {
		ag, _ = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusFailed).Save(context.Background())
		rollback("connect-nic", vmName)
		return nil, fmt.Errorf("connect nic: %w", err)
	}

	// 7. Wait for IP
	if err := conn.WaitForGuestIP(ctx, vmName, staticIP, ipWaitTimeout); err != nil {
		ag, _ = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusFailed).Save(context.Background())
		rollback("wait-ip", vmName)
		return nil, fmt.Errorf("wait IP: %w", err)
	}

	// 7.5 Cold reboot — forces guest kernel to re-read vNIC hardware,
	// giving the clone a unique MAC distinct from the parent.
	log.Printf("[instant-clone] cold rebooting %s for unique MAC", vmName)
	if err := conn.PowerOff(ctx, vmName); err != nil {
		log.Printf("[instant-clone] power off non-fatal: %v", err)
	}
	select {
	case <-ctx.Done(): rollback("reboot-off", vmName); return nil, ctx.Err()
	case <-time.After(5 * time.Second):
	}
	if err := conn.PowerOn(ctx, vmName); err != nil {
		ag, _ = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusFailed).Save(context.Background())
		rollback("reboot-on", vmName)
		return nil, fmt.Errorf("cold reboot power on: %w", err)
	}
	log.Printf("[instant-clone] waiting for IP %s after cold reboot", staticIP)
	if err := conn.WaitForGuestIP(ctx, vmName, staticIP, ipWaitTimeout); err != nil {
		ag, _ = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusFailed).Save(context.Background())
		rollback("wait-ip2", vmName)
		return nil, fmt.Errorf("wait IP after reboot: %w", err)
	}

	// 8. GuestInfo (OpenClaw config, command=start-openclaw)
	ocToken := secureToken()
	gi := &deploy.AgentMgrGuestInfo{
		Role: "clone", DeploymentID: deploymentID, Generation: generation,
		Command: "start-openclaw",
		OpenClawUser: runAsUser, OpenClawHome: "/home/" + runAsUser,
		OpenClawPort: "18789", OpenClawBaseURL: t.gwURL,
		OpenClawModel: "minmax", OpenClawAPIKey: key.Key,
		OpenClawGatewayToken: ocToken,
	}
	giMap := gi.ToGuestInfo()
	if err := conn.SetGuestinfo(ctx, vmName, giMap); err != nil {
		rollback("gi", vmName); return nil, fmt.Errorf("set guestinfo: %w", err)
	}
	if err := conn.SetGuestinfo(ctx, vmName, map[string]string{"agentmgr.commit": generation}); err != nil {
		rollback("commit", vmName); return nil, fmt.Errorf("commit: %w", err)
	}
	log.Printf("[instant-clone] guestinfo oc written gen=%s", generation)

	// 8. Wait for OpenClaw ACK (TCP :18789)
	ag, err = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusServiceStarting).Save(ctx)
	if err != nil { rollback("svc", vmName); return nil, err }

	log.Printf("[instant-clone] waiting openclaw port on %s:18789", staticIP)
	ocAddr := fmt.Sprintf("%s:18789", staticIP)
	dl := time.Now().Add(ocReadyTimeout)
	for {
		if time.Now().After(dl) {
			ag, _ = r.Ent.Agent.UpdateOne(ag).SetStatus(agent.StatusFailed).Save(context.Background())
			rollback("oc-timeout", vmName)
			return nil, fmt.Errorf("openclaw not ready after %v", ocReadyTimeout)
		}
		c, err := net.DialTimeout("tcp", ocAddr, 3*time.Second)
		if err == nil { c.Close(); break }
		select {
		case <-ctx.Done():
			rollback("oc-ctx", vmName); return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}

	// 9. Clear sensitive + persist + RUNNING
	_ = conn.SetGuestinfo(ctx, vmName, map[string]string{
		"agentmgr.openclaw-api-key": "", "agentmgr.openclaw-gateway-token": "",
	})
	vkCreate := r.Ent.VirtualKey.Create().
		SetLitellmKey(key.Key).SetMaskedKey(maskKey(key.Key)).
		SetModelGatewayID(t.gwConn.ID).SetModels(nil).
		SetUserID(t.ownerID.String()).SetName(ag.Name + "-" + deploymentID[:8])
	if key.Token != "" { vkCreate.SetLitellmToken(key.Token) }
	vk, err := vkCreate.Save(ctx)
	if err != nil { rollback("vk", vmName); return nil, err }

	updated, err := r.Ent.Agent.UpdateOne(ag).
		SetStatus(agent.StatusRunning).SetVMRef(vmName).SetVirtualKeyID(vk.ID).Save(ctx)
	if err != nil { r.rollbackDeployCreate(ctx, conn, t.gw, ag, vmName, key.Key); return nil, err }

	log.Printf("[instant-clone] SUCCESS deployment=%s agent=%s vm=%s ip=%s",
		deploymentID, ag.Name, vmName, staticIP)

	return &model.DeployedAgent{
		Agent: toModelAgent(updated), VirtualKeySecret: key.Key,
		TemplateVersion: toModelOvaVersion(t.version, t.familyID.String()),
		ResourcePool:    toModelResourcePool(t.pool),
	}, nil
}

