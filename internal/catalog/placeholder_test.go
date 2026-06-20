package catalog

import "testing"

func TestResolvePlaceholders(t *testing.T) {
	vars := map[string]string{
		"AGENT_PKG_BASE_URL": "http://mirror.internal/agents",
		"AGENT_USER":         "agent",
	}
	in := "curl {{AGENT_PKG_BASE_URL}}/goose.tar.gz && su {{AGENT_USER}} -c run"
	want := "curl http://mirror.internal/agents/goose.tar.gz && su agent -c run"
	if got := ResolvePlaceholders(in, vars); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Unknown placeholders are left intact rather than silently blanked — a blanked
// install command would be a broken command that looks valid.
func TestResolvePlaceholders_LeavesUnknownIntact(t *testing.T) {
	got := ResolvePlaceholders("a {{UNKNOWN}} b", map[string]string{"X": "1"})
	if got != "a {{UNKNOWN}} b" {
		t.Errorf("unknown placeholder must be left intact, got %q", got)
	}
}

func TestResolvePlaceholders_EmptyInputsSafe(t *testing.T) {
	if got := ResolvePlaceholders("", nil); got != "" {
		t.Errorf("empty input: got %q", got)
	}
	if got := ResolvePlaceholders("no tokens here", nil); got != "no tokens here" {
		t.Errorf("nil vars: got %q", got)
	}
}
