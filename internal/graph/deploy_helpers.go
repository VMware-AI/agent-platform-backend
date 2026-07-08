package graph

// Non-resolver deploy helpers. These live OUTSIDE deploy.resolvers.go on purpose:
// gqlgen rewrites *.resolvers.go on every generate and moves any function that
// doesn't match a schema resolver into a commented "to be deleted" graveyard —
// which silently drops these helpers (and their imports) from the build.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// mapOVFProperties converts deploy-input OVF property pairs to a map for
// the deploy service (guestinfo.* keys become VM ExtraConfig). Only keys
// starting with "guestinfo." are forwarded; anything else is logged and
// dropped to prevent arbitrary ExtraConfig injection. This is a defense-
// in-depth check: the deploy mutation is already admin-gated, but a
// misconfigured client should not be able to inject arbitrary VM config.
func mapOVFProperties(props []model.OVFPropertyInput) map[string]string {
	if len(props) == 0 {
		return nil
	}
	m := make(map[string]string, len(props))
	for _, p := range props {
		if !strings.HasPrefix(p.Key, "guestinfo.") {
			log.Printf("deploy: ignoring ovf property %q — key must start with guestinfo.", p.Key)
			continue
		}
		m[p.Key] = p.Value
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// VMTemplates lists the OVA templates available in a resource pool's vCenter,
// powering the deploy form's template picker.

// deployAgentInstant runs the Instant Clone path: it power-off → remove
// serial ports → power-on → InstantClone a parent VM, then injects guestinfo
// into the already-running child. Brought over from the deploy API branch;
// the VCenterClient interface methods it relies on (PowerOff/On,
// RemoveSerialPorts, InstantClone, SetGuestinfo) are all already part of the
// shared graph.Resolver interface. VirtualKey persistence uses the model-
// routing schema (organization + model_gateway, not user/team).
func (r *mutationResolver) deployAgentInstant(
	ctx context.Context,
	ag *ent.Agent,
	t *deployTargets,
	conn VCenterClient,
	input model.DeployAgentInput,
	vmName string,
	enrollToken string,
	defaultConfig string,
	configPath string,
) (*model.DeployedAgent, error) {
	parentVM := derefString(input.InstantCloneParent)
	if parentVM == "" {
		return nil, fmt.Errorf("instantCloneParent required")
	}

	// 1. Issue gateway key.
	key, err := t.gw.GenerateKey(ctx, gateway.GenerateKeyRequest{
		UserID:         ag.OwnerUserID.String(),
		TeamID:         t.deployTeamID,
		Models:         nil,
		MaxBudget:      input.MaxBudget,
		OrganizationID: ag.TenantID.String(),
		Metadata:       map[string]string{"agent": ag.Name},
	})
	if err != nil {
		r.deleteAgentRow(ctx, ag)
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// 2. Power off the parent so RemoveSerialPorts is safe; tolerate failures
	// (vcsim/standalone may not implement power transitions).
	if err := conn.PowerOff(ctx, parentVM); err != nil {
		log.Printf("[instant-clone] power off non-fatal: %v", err)
	}
	time.Sleep(3 * time.Second)
	if err := conn.RemoveSerialPorts(ctx, parentVM); err != nil {
		_ = t.gw.DeleteKey(ctx, key.Key)
		r.deleteAgentRow(ctx, ag)
		return nil, fmt.Errorf("remove serial ports from %q: %w", parentVM, err)
	}
	if err := conn.PowerOn(ctx, parentVM); err != nil {
		_ = t.gw.DeleteKey(ctx, key.Key)
		r.deleteAgentRow(ctx, ag)
		return nil, fmt.Errorf("power on parent %q: %w", parentVM, err)
	}
	time.Sleep(5 * time.Second)

	// 3. Instant Clone the parent.
	icSpec := vcenter.InstantCloneSpec{
		ParentVM:     parentVM,
		Name:         vmName,
		ResourcePool: derefString(input.TargetResourcePool),
		Network:      derefString(input.TargetNetwork),
	}
	if len(input.OvfProperties) > 0 {
		icSpec.ExtraConfig = make(map[string]string, len(input.OvfProperties))
		for _, p := range input.OvfProperties {
			icSpec.ExtraConfig[p.Key] = p.Value
		}
	}
	if _, err := conn.InstantClone(ctx, icSpec); err != nil {
		_ = t.gw.DeleteKey(ctx, key.Key)
		r.deleteAgentRow(ctx, ag)
		return nil, fmt.Errorf("instant clone: %w", err)
	}

	// 4. Inject guestinfo (the clone is already running).
	gi := map[string]string{}
	if enrollToken != "" {
		gi["agentmgr.enroll_token"] = enrollToken
		gi["agentmgr.vm_id"] = vmName
		gi["agentmgr.control_plane_url"] = r.ControlPlaneURL
	}
	if defaultConfig != "" {
		gi["agent.default_config"] = defaultConfig
	}
	if len(gi) > 0 {
		_ = conn.SetGuestinfo(ctx, vmName, gi)
	}

	// 5. Persist virtual key (model-routing schema: org + gateway, not user/team).
	vkCreate := r.Ent.VirtualKey.Create().
		SetLitellmKey(key.Key).
		SetModelGatewayID(t.gwConn.ID).
		SetModels(nil).
		SetName(ag.Name).
		SetOrganizationID(ag.TenantID.String())
	if key.Token != "" {
		vkCreate.SetLitellmToken(key.Token)
	}
	vk, err := vkCreate.Save(ctx)
	if err != nil {
		r.rollbackDeployCreate(ctx, conn, t.gw, ag, vmName, key.Key)
		return nil, fmt.Errorf("save vk: %w", err)
	}

	// 6. Mark the agent row running and point at the new VM.
	updated, err := r.Ent.Agent.UpdateOne(ag).
		SetStatus(agent.StatusRunning).
		SetVMRef(vmName).
		SetVirtualKeyID(vk.ID).
		Save(ctx)
	if err != nil {
		r.rollbackDeployCreate(ctx, conn, t.gw, ag, vmName, key.Key)
		return nil, fmt.Errorf("update agent: %w", err)
	}

	return &model.DeployedAgent{
		Agent:            toModelAgent(updated),
		VirtualKeySecret: key.Key,
		TemplateVersion:  toModelOvaVersion(t.version, t.familyID.String()),
		ResourcePool:     toModelResourcePool(t.pool),
	}, nil
}
