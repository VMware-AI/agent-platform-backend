package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// #36 coverage: gatewayPublicURL (which URL deployed VMs call) + deployGateway's
// public-URL branch — the newest PR-C routing code, near-zero coverage.

func TestGatewayPublicURL(t *testing.T) {
	cases := []struct{ name, public, endpoint, want string }{
		{"public set wins", "https://pub:4000", "https://internal:4000", "https://pub:4000"},
		{"empty falls back to endpoint", "", "https://internal:4000", "https://internal:4000"},
	}
	for _, c := range cases {
		g := &ent.GatewayConnection{PublicURL: c.public, Endpoint: c.endpoint}
		if got := gatewayPublicURL(g); got != c.want {
			t.Errorf("%s: gatewayPublicURL = %q, want %q", c.name, got, c.want)
		}
	}
}

// No DB gateway → legacy injected r.Gateway + r.GatewayURL.
func TestDeployGateway_LegacyFallback(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	r.GatewayURL = "https://legacy:4000"

	gw, url, _ := r.deployGateway(context.Background(), nil)
	if _, ok := gw.(*fakeGateway); !ok {
		t.Errorf("expected the legacy injected gateway, got %T", gw)
	}
	if url != "https://legacy:4000" {
		t.Errorf("expected legacy URL, got %q", url)
	}
}

// A platform default gateway → its public URL + a client bound to it.
func TestDeployGateway_DefaultGatewayPublicURL(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.GatewayKeyClientFor = func(context.Context, *ent.GatewayConnection) gateway.Client { return fg }
	ctx := context.Background()
	if _, err := r.Ent.GatewayConnection.Create().
		SetName("def").SetEndpoint("https://internal:4000").
		SetPublicURL("https://pub:4000").SetIsDefault(true).Save(ctx); err != nil {
		t.Fatalf("seed gateway: %v", err)
	}
	gw, url, _ := r.deployGateway(ctx, nil)
	if _, ok := gw.(*fakeGateway); !ok {
		t.Errorf("expected the per-connection client, got %T", gw)
	}
	if url != "https://pub:4000" {
		t.Errorf("expected default gateway public URL, got %q", url)
	}
}
