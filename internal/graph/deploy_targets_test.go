package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent"
)

func TestDeployGatewayPublicURL(t *testing.T) {
	publicURL := "http://litellm.internal:4000"

	tests := []struct {
		name            string
		gateway         *ent.GatewayConnection
		controlPlaneURL string
		want            string
	}{
		{
			name:            "uses explicit agent access URL first",
			gateway:         &ent.GatewayConnection{Endpoint: "http://localhost:4000", PublicURL: &publicURL},
			controlPlaneURL: "http://192.168.15.128:8080",
			want:            "http://litellm.internal:4000",
		},
		{
			name:            "uses routable endpoint directly",
			gateway:         &ent.GatewayConnection{Endpoint: "http://10.121.160.250:4000"},
			controlPlaneURL: "http://192.168.15.128:8080",
			want:            "http://10.121.160.250:4000",
		},
		{
			name:            "uses localhost endpoint when it is the configured LiteLLM endpoint",
			gateway:         &ent.GatewayConnection{Endpoint: "http://localhost:4000"},
			controlPlaneURL: "http://192.168.15.128:8080",
			want:            "http://localhost:4000",
		},
		{
			name:            "uses loopback endpoint when it is the configured LiteLLM endpoint",
			gateway:         &ent.GatewayConnection{Endpoint: "http://127.0.0.1:4000"},
			controlPlaneURL: "http://10.121.160.250",
			want:            "http://127.0.0.1:4000",
		},
		{
			name:            "does not invent temporary jump host proxy",
			gateway:         &ent.GatewayConnection{Endpoint: ""},
			controlPlaneURL: "",
			want:            "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deployGatewayPublicURL(tt.gateway, tt.controlPlaneURL)
			if got != tt.want {
				t.Fatalf("deployGatewayPublicURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
