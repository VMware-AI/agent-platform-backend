package skills

import (
	"strings"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent"
)

func TestBuildInstallScriptPipUsesOfflineWheelhouse(t *testing.T) {
	sk := &ent.Skill{Name: "fabric", Version: "1.0.0", PackageURL: "http://mirror/fabric-1.0.0.tar.gz", InstallMethod: "pip"}

	cmd, err := buildInstallScript(sk)
	if err != nil {
		t.Fatalf("buildInstallScript returned error: %v", err)
	}
	if !strings.Contains(cmd, "pip3 install") {
		t.Fatalf("expected pip install command, got %q", cmd)
	}
	if !strings.Contains(cmd, "--find-links /tmp/skills_fabric") {
		t.Fatalf("expected wheelhouse path in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "fabric") {
		t.Fatalf("expected skill name in command, got %q", cmd)
	}
}

func TestBuildInstallScriptBinaryExtractsToSkillDir(t *testing.T) {
	sk := &ent.Skill{Name: "custom-tool", Version: "3.0.0", PackageURL: "http://mirror/custom-tool-3.0.0.tar.gz", InstallMethod: "binary"}

	cmd, err := buildInstallScript(sk)
	if err != nil {
		t.Fatalf("buildInstallScript returned error: %v", err)
	}
	if strings.Contains(cmd, "pip3 install") {
		t.Fatalf("binary skills must not use pip install: %q", cmd)
	}
	if !strings.Contains(cmd, "$HOME/.local/share/skills/custom-tool") {
		t.Fatalf("expected binary install dir, got %q", cmd)
	}
	if !strings.Contains(cmd, "tar xz -C \"$skill_dir\"") {
		t.Fatalf("expected binary extraction path, got %q", cmd)
	}
}

func TestSkillInstalledUsesMethodSpecificProbe(t *testing.T) {
	if !skillInstalled(&ent.Skill{Name: "custom-tool", InstallMethod: "binary"}, nil, map[string]struct{}{"custom-tool": {}}) {
		t.Fatal("expected binary skill to be marked installed from skill dir")
	}
	if skillInstalled(&ent.Skill{Name: "fabric", InstallMethod: "pip"}, map[string]struct{}{"custom-tool": {}}, nil) {
		t.Fatal("expected pip skill to ignore unrelated binary dirs")
	}
	if !skillInstalled(&ent.Skill{Name: "fabric", InstallMethod: "pip"}, map[string]struct{}{"fabric": {}}, nil) {
		t.Fatal("expected pip skill to be marked installed from pip list")
	}
}
