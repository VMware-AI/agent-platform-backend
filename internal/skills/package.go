// Package skills handles offline Skill package distribution to the jump-host
// file server at 10.121.166.205:/data/skill/ so agent VMs on VLAN 805 can
// fetch them during cloud-init.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/google/uuid"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/ent"
)

// Service manages offline skill package distribution to a repo host.
type Service struct {
	Ent       *ent.Client
	SCPHost   string // e.g. "root@10.121.166.205"
	DataDir   string // e.g. "/data/skill"
	HTTPBase  string // e.g. "http://172.16.85.230:8081"
	JumpHost  string // SSH bastion (empty = direct)
	JHPass    string // jump host password
	AgentUser string // default "vmware"
	AgentPass string // Agent VM SSH password
}


func (s *Service) sshCmd(remoteCmd string) *exec.Cmd {
	if s.JumpHost != "" {
		return exec.Command("sshpass", "-p", s.JHPass,
			"ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-tt",
			s.JumpHost, remoteCmd)
	}
	// Direct SSH to agent VM
	args := []string{"-p", s.AgentPass, "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-tt", s.AgentUser + "@"}
	return exec.Command("sshpass", append(args, remoteCmd)...)
}

func (s *Service) scpDest(file string) string {
	return fmt.Sprintf("%s:%s/%s", s.SCPHost, s.DataDir, file)
}
func (s *Service) httpURL(file string) string { return fmt.Sprintf("%s/%s", s.HTTPBase, file) }

// SyncPackage downloads the skill's package_url and SCPs it to the jump host.
func (s *Service) SyncPackage(ctx context.Context, skillID string, sourceURL string) error {
	sk, err := s.Ent.Skill.Get(ctx, uuid.MustParse(skillID))
	if err != nil {
		return fmt.Errorf("skill not found: %w", err)
	}
	src := sourceURL
	if src == "" {
		src = sk.PackageURL
	}
	if src == "" && sk.InstallMethod != "pip" {
		return fmt.Errorf("skill %s has no source URL", sk.Name)
	}
	// Skip if already synced to jump host
	if strings.HasPrefix(src, s.HTTPBase) {
		log.Printf("[skills] %s already synced at %s", sk.Name, src)
		return nil
	}

	target := fmt.Sprintf("%s-%s.tar.gz", sk.Name, sk.Version)
	tmp := filepath.Join(os.TempDir(), target)
	var cmd *exec.Cmd

	switch sk.InstallMethod {
	case "pip":
		tmpDir, err := os.MkdirTemp("", "skill-pip-*")
		if err != nil {
			return fmt.Errorf("create temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)
		log.Printf("[skills] pip download %s + deps for Python 3.10", sk.Name)
		cmd := exec.CommandContext(ctx, "pip3", "download", sk.Name, "exceptiongroup",
			"-d", tmpDir, "--python-version", "3.10",
			"--platform", "manylinux2014_x86_64", "--only-binary=:all:")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("pip download %s failed: %w (%s)", sk.Name, err, strings.TrimSpace(string(out)))
		}
		log.Printf("[skills] packaged %d whl(s) for %s", listWhl(tmpDir), sk.Name)
		cmd = exec.CommandContext(ctx, "tar", "czf", tmp, "-C", tmpDir, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tar failed: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	default:
		log.Printf("[skills] downloading %s from %s", sk.Name, src)
		cmd := exec.CommandContext(ctx, "curl", "-sL", "--connect-timeout", "30", "-o", tmp, src)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("download %s failed: %w (%s)", src, err, strings.TrimSpace(string(out)))
		}
	}
	defer os.Remove(tmp)

	dest := s.scpDest(target)
	log.Printf("[skills] scp %s -> %s", tmp, dest)
	cmd = exec.CommandContext(ctx, "sshpass", "-p", "root",
		"scp", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		tmp, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	httpURL := fmt.Sprintf("%s/%s", s.HTTPBase, target)
	if _, err := sk.Update().SetPackageURL(httpURL).Save(ctx); err != nil {
		return fmt.Errorf("update package_url: %w", err)
	}
	log.Printf("[skills] %s ready at %s", sk.Name, httpURL)
	return nil
}

func listWhl(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".whl") {
			n++
		}
	}
	return n
}

func (s *Service) uploadFile(ctx context.Context, skillID string, r io.Reader) error {
	sk, err := s.Ent.Skill.Get(ctx, uuid.MustParse(skillID))
	if err != nil {
		return fmt.Errorf("skill not found: %w", err)
	}

	target := fmt.Sprintf("%s-%s.tar.gz", sk.Name, sk.Version)
	tmp := filepath.Join(os.TempDir(), target)

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	f.Close()
	defer os.Remove(tmp)

	dest := s.scpDest(target)
	cmd := exec.CommandContext(ctx, "sshpass", "-p", "root",
		"scp", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		tmp, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	httpURL := fmt.Sprintf("%s/%s", s.HTTPBase, target)
	if _, err := sk.Update().SetPackageURL(httpURL).Save(ctx); err != nil {
		return fmt.Errorf("update package_url: %w", err)
	}
	log.Printf("[skills] %s uploaded, ready at %s", sk.Name, httpURL)
	return nil
}

// InstallOnAgent SSHes to an agent VM and installs a skill from the offline repo.
func (s *Service) InstallOnAgent(ctx context.Context, agentIP, skillID string) error {
	sk, err := s.Ent.Skill.Get(ctx, uuid.MustParse(skillID))
	if err != nil {
		return fmt.Errorf("skill not found: %w", err)
	}
	if sk.PackageURL == "" {
		return fmt.Errorf("skill %s has no offline package", sk.Name)
	}
	installScript := fmt.Sprintf(
		"mkdir -p /tmp/skills_%s && curl -sL --max-time 300 %s | tar xz -C /tmp/skills_%s && pip3 install --break-system-packages --no-index --find-links /tmp/skills_%s --ignore-requires-python %s",
		sk.Name, sk.PackageURL, sk.Name, sk.Name, sk.Name,
	)
	var agentSSH string
	if s.JumpHost != "" {
		agentSSH = fmt.Sprintf("sshpass -p %s ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 %s@%s '%s'",
			s.AgentPass, s.AgentUser, agentIP, installScript)
	} else {
		agentSSH = installScript
	}
	log.Printf("[skills] installing %s on %s", sk.Name, agentIP)
	c := s.sshCmd(agentSSH)
	c = exec.CommandContext(ctx, c.Args[0], c.Args[1:]...)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	// Verify
	verifyCmd := fmt.Sprintf("pip3 list 2>/dev/null | grep -i %s", sk.Name)
	var verifySSH string
	if s.JumpHost != "" {
		verifySSH = fmt.Sprintf("sshpass -p %s ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 %s@%s '%s'",
			s.AgentPass, s.AgentUser, agentIP, verifyCmd)
	} else {
		verifySSH = verifyCmd
	}
	vc := s.sshCmd(verifySSH)
	vc = exec.CommandContext(ctx, vc.Args[0], vc.Args[1:]...)
	vout, verr := vc.CombinedOutput()
	if verr == nil && strings.TrimSpace(string(vout)) != "" {
		log.Printf("[skills] %s verified on %s: %s", sk.Name, agentIP, strings.TrimSpace(string(vout)))
	} else {
		log.Printf("[skills] %s install on %s could not be verified: %v", sk.Name, agentIP, verr)
	}
	return nil
}

// Handler returns HTTP routes for skill package management.
// Routes are registered both with and without the /v1/skills/ prefix because
// the outer mux may strip it when matching the subtree pattern.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	// Full paths (when matched by method+path patterns in outer mux)
	mux.HandleFunc("POST /v1/skills/sync/{skillId}", s.handleSync)
	mux.HandleFunc("POST /v1/skills/upload/{skillId}", s.handleUpload)
	mux.HandleFunc("POST /v1/skills/install/{agentIp}/{skillId}", s.handleInstall)
	// Stripped paths (when outer mux uses subtree pattern /v1/skills/)
	mux.HandleFunc("POST /sync/{skillId}", s.handleSync)
	mux.HandleFunc("POST /upload/{skillId}", s.handleUpload)
	mux.HandleFunc("POST /install/{agentIp}/{skillId}", s.handleInstall)
	// Fallback: match any POST under /v1/skills/
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "not found", "path": r.URL.Path})
	})
	return mux
}

func (s *Service) handleSync(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("skillId")
	if id == "" {
		writeJSON(w, 400, map[string]string{"error": "missing skillId"})
		return
	}
	var body struct {
		SourceURL string `json:"sourceUrl"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := s.SyncPackage(r.Context(), id, body.SourceURL); err != nil {
		log.Printf("[skills] sync error: %v", err)
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Service) handleInstall(w http.ResponseWriter, r *http.Request) {
	agentIP := r.PathValue("agentIp")
	skillID := r.PathValue("skillId")
	if agentIP == "" || skillID == "" {
		writeJSON(w, 400, map[string]string{"error": "missing agentIp or skillId"})
		return
	}
	if err := s.InstallOnAgent(r.Context(), agentIP, skillID); err != nil {
		log.Printf("[skills] install error: %v", err)
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("skillId")
	if id == "" {
		writeJSON(w, 400, map[string]string{"error": "missing skillId"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		writeJSON(w, 400, map[string]string{"error": "file too large"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "missing file"})
		return
	}
	defer file.Close()
	if err := s.uploadFile(r.Context(), id, file); err != nil {
		log.Printf("[skills] upload error: %v", err)
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
