package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildUserdataWritesOpenCodeLiteLLMCompatibleProvider(t *testing.T) {
	userdata := buildUserdata(
		"http://172.16.85.230:4000",
		"sk-test",
		"agent-test",
		"",
		"",
		nil,
		[]string{"qwen-coder"},
		nil,
	)

	cfg := extractCloudInitJSON(t, userdata, "/home/vmware/.config/opencode/opencode.json")
	if _, ok := cfg["model"]; ok {
		t.Fatalf("model field must be omitted when no hardcoded default is desired: %#v", cfg["model"])
	}
	providers, ok := cfg["enabled_providers"].([]any)
	if !ok || len(providers) != 1 || providers[0] != "litellm" {
		t.Fatalf("enabled_providers = %#v, want [litellm]", cfg["enabled_providers"])
	}
	provider := cfg["provider"].(map[string]any)["litellm"].(map[string]any)
	if got := provider["npm"]; got != "@ai-sdk/openai-compatible" {
		t.Fatalf("provider.litellm.npm = %v, want @ai-sdk/openai-compatible", got)
	}
	options := provider["options"].(map[string]any)
	if got := options["baseURL"]; got != "http://172.16.85.230:4000/v1" {
		t.Fatalf("baseURL = %v, want real LiteLLM /v1 URL", got)
	}
	models := provider["models"].(map[string]any)
	if _, ok := models["qwen-coder"]; !ok {
		t.Fatalf("provider.litellm.models missing qwen-coder: %#v", models)
	}

	ocCfg := extractCloudInitJSON(t, userdata, "/home/vmware/.openclaw/openclaw.json")
	if agents, ok := ocCfg["agents"]; ok {
		if m, ok := agents.(map[string]any); ok {
			if _, hasDefault := m["defaults"]; hasDefault {
				t.Fatalf("openclaw agents.defaults must not hardcode a model: %#v", agents)
			}
		}
	}
}

func TestBuildUserdataWritesHermesLiteLLMCustomProvider(t *testing.T) {
	userdata := buildUserdata(
		"http://172.16.85.230:4000",
		"sk-test",
		"agent-test",
		"",
		"",
		nil,
		[]string{"qwen-coder"},
		nil,
	)

	env := extractCloudInitContent(t, userdata, "/home/vmware/.hermes/.env")
	if !strings.Contains(env, "OPENAI_BASE_URL=http://172.16.85.230:4000/v1") {
		t.Fatalf("Hermes .env missing LiteLLM base URL:\n%s", env)
	}

	cfg := extractCloudInitContent(t, userdata, "/home/vmware/.hermes/config.yaml")
	if !strings.Contains(cfg, "provider: custom:litellm") {
		t.Fatalf("Hermes default provider must select custom:litellm, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "base_url: http://172.16.85.230:4000/v1") {
		t.Fatalf("Hermes config missing LiteLLM custom provider URL:\n%s", cfg)
	}
	if strings.Contains(cfg, "default:") {
		t.Fatalf("Hermes config must not hardcode a default model:\n%s", cfg)
	}
}

func TestBuildUserdataDoesNotRequireAgentGroup(t *testing.T) {
	userdata := buildUserdata(
		"http://172.16.85.230:4000",
		"sk-test",
		"agent-test",
		"",
		"",
		nil,
		[]string{"qwen-coder"},
		nil,
	)

	if !strings.Contains(userdata, "  - mkdir -p /etc/agent\n") {
		t.Fatalf("userdata must create /etc/agent before writing gateway env:\n%s", userdata)
	}
	if strings.Contains(userdata, "owner: root:agent") {
		t.Fatalf("userdata must not require a missing agent group:\n%s", userdata)
	}
	if !strings.Contains(userdata, "owner: root:root") {
		t.Fatalf("userdata should write agent env with root ownership:\n%s", userdata)
	}
}

func extractCloudInitJSON(t *testing.T, userdata, path string) map[string]any {
	t.Helper()
	content := extractCloudInitContent(t, userdata, path)
	line := strings.SplitN(content, "\n", 2)[0]
	line = strings.TrimSpace(line)
	var out map[string]any
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		t.Fatalf("decode json %q: %v", line, err)
	}
	return out
}

func extractCloudInitContent(t *testing.T, userdata, path string) string {
	t.Helper()
	marker := "  - path: " + path
	idx := strings.Index(userdata, marker)
	if idx < 0 {
		t.Fatalf("userdata missing %s\n%s", path, userdata)
	}
	rest := userdata[idx:]
	contentIdx := strings.Index(rest, "    content: |\n")
	if contentIdx < 0 {
		t.Fatalf("userdata entry %s missing content", path)
	}
	content := rest[contentIdx+len("    content: |\n"):]
	nextEntry := strings.Index(content, "\n  - path: ")
	if nextEntry >= 0 {
		content = content[:nextEntry]
	}
	return strings.TrimRight(content, "\n")
}
