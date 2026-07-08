package graph

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// poolProbeTimeout bounds the pre-save reachability dial (testResourcePoolConnection).
const poolProbeTimeout = 5 * time.Second

// defaultVCenterPort is the HTTPS port a vCenter endpoint listens on; used when the
// operator's endpoint omits an explicit port.
const defaultVCenterPort = "443"

// parseEndpointHostPort validates a resource-pool endpoint (a host, host:port, or
// https URL) and returns a "host:port" suitable for net.Dial. It rejects empty or
// malformed input — the boundary validation for the credential-less probe — and
// defaults the port to vCenter HTTPS (443) when none is given.
func parseEndpointHostPort(endpoint string) (string, error) {
	e := strings.TrimSpace(endpoint)
	if e == "" {
		return "", fmt.Errorf("endpoint is required")
	}
	// Accept a full URL (https://host[:port][/path]) or a bare host[:port].
	host := e
	if strings.Contains(e, "://") {
		u, err := url.Parse(e)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("endpoint %q is not a valid URL", endpoint)
		}
		host = u.Host
	}
	// Split host:port; default the port when absent.
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		// No port present (or another error) — treat the whole thing as the host.
		h, p = host, defaultVCenterPort
	}
	if strings.TrimSpace(h) == "" {
		return "", fmt.Errorf("endpoint %q has no host", endpoint)
	}
	return net.JoinHostPort(h, p), nil
}

// dialReachable performs a bounded TCP dial to confirm an endpoint is reachable.
// LIMITATION: this is a transport-level reachability check only — it does NOT
// complete a TLS handshake or authenticate, so it cannot verify the endpoint is
// actually a vCenter or that the supplied credentials work. That full check
// happens later, with credentials, in syncResourcePool.
func dialReachable(ctx context.Context, hostPort string) error {
	d := net.Dialer{Timeout: poolProbeTimeout}
	dctx, cancel := context.WithTimeout(ctx, poolProbeTimeout)
	defer cancel()
	conn, err := d.DialContext(dctx, "tcp", hostPort)
	if err != nil {
		return err
	}
	return conn.Close()
}

// connectPool resolves a resource pool's credentials and dials its vCenter.
func (r *Resolver) connectPool(ctx context.Context, pool *ent.ResourcePool) (VCenterClient, error) {
	if r.Secrets == nil || r.VCenterConnect == nil {
		return nil, fmt.Errorf("resource-pool connect not configured")
	}
	if pool.SecretRef == "" {
		return nil, fmt.Errorf("resource pool has no secret_ref")
	}
	cred, err := r.resolveSecret(ctx, pool.SecretRef, secretPurposeVCenterConnect)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	return r.VCenterConnect(ctx, pool.Endpoint, cred.Username, cred.Password, pool.Insecure)
}

// connectAgentVM resolves an agent the caller owns, dials its resource pool's
// vCenter, and returns the live connection plus the agent's VM ref. The caller
// MUST Logout the returned connection. Errors (404-style via getOwnedAgent) if
// the agent is not the caller's, has no pool, or has no deployed VM.
func (r *Resolver) connectAgentVM(ctx context.Context, cu *auth.CurrentUser, agentID uuid.UUID) (VCenterClient, string, error) {
	ag, err := r.getOwnedAgent(ctx, agentID, cu)
	if err != nil {
		return nil, "", err
	}
	if ag.VMRef == "" {
		return nil, "", gqlerror.Errorf("agent has no VM (not deployed)")
	}
	if ag.ResourcePoolID == nil {
		return nil, "", gqlerror.Errorf("agent has no resource pool; cannot locate its VM")
	}
	pool, err := r.Ent.ResourcePool.Get(ctx, *ag.ResourcePoolID)
	if err != nil {
		return nil, "", err
	}
	conn, err := r.connectPool(ctx, pool)
	if err != nil {
		return nil, "", fmt.Errorf("connect vcenter: %w", err)
	}
	return conn, ag.VMRef, nil
}
