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

func buildInstallScript(sk *ent.Skill) (string, error) {
	switch sk.InstallMethod {
	case "pip":
		if sk.Name == "fabric" {
			return fmt.Sprintf(
				"mkdir -p /tmp/skills_%s && curl -sL --max-time 300 %s | tar xz -C /tmp/skills_%s && break_flag=$(pip3 install --help 2>/dev/null | grep -q -- --break-system-packages && echo --break-system-packages || true) && wrapt_src=$(find /tmp/skills_%s -maxdepth 1 -name 'wrapt-*.tar.gz' | head -n1) && pip3 install ${break_flag} --user --no-index --find-links /tmp/skills_%s --ignore-requires-python \"$wrapt_src\" && pip3 install ${break_flag} --user --no-index --find-links /tmp/skills_%s --ignore-requires-python %s",
				sk.Name, sk.PackageURL, sk.Name, sk.Name, sk.Name, sk.Name, sk.Name,
			), nil
		}
		return fmt.Sprintf(
			"mkdir -p /tmp/skills_%s && curl -sL --max-time 300 %s | tar xz -C /tmp/skills_%s && break_flag=$(pip3 install --help 2>/dev/null | grep -q -- --break-system-packages && echo --break-system-packages || true) && pip3 install ${break_flag} --user --no-index --find-links /tmp/skills_%s --ignore-requires-python %s",
			sk.Name, sk.PackageURL, sk.Name, sk.Name, sk.Name,
		), nil
	case "pip-requirements":
		return fmt.Sprintf(
			"mkdir -p /opt/skills/%s && curl -sL --max-time 300 %s | tar xz -C /opt/skills/%s && if [ -f /opt/skills/%s/requirements.txt ]; then pip3 install --user -r /opt/skills/%s/requirements.txt; fi",
			sk.Name, sk.PackageURL, sk.Name, sk.Name, sk.Name,
		), nil
	case "npm":
		return fmt.Sprintf(
			"tmpdir=$(mktemp -d /tmp/skills_%s.XXXXXX) && curl -sL --max-time 300 %s | tar xz -C \"$tmpdir\" && npm install -g \"$tmpdir\" && rm -rf \"$tmpdir\"",
			sk.Name, sk.PackageURL,
		), nil
	case "binary":
		return fmt.Sprintf(
			"skill_dir=\"$HOME/.local/share/skills/%s\" && mkdir -p \"$skill_dir\" && curl -sL --max-time 300 %s | tar xz -C \"$skill_dir\" && if [ -x \"$skill_dir/install.sh\" ]; then (cd \"$skill_dir\" && ./install.sh); fi",
			sk.Name, sk.PackageURL,
		), nil
	default:
		return "", fmt.Errorf("unsupported install method %q", sk.InstallMethod)
	}
}

func buildVerifyScript(sk *ent.Skill) (string, error) {
	switch sk.InstallMethod {
	case "pip", "pip-requirements":
		return fmt.Sprintf("pip3 list 2>/dev/null | grep -i %s", sk.Name), nil
	case "npm":
		return fmt.Sprintf("npm list -g --depth=0 --json 2>/dev/null | grep -i %s", sk.Name), nil
	case "binary":
		return fmt.Sprintf("[ -e \"$HOME/.local/share/skills/%s\" ] && echo installed", sk.Name), nil
	default:
		return "", fmt.Errorf("unsupported install method %q", sk.InstallMethod)
	}
}

func skillInstalled(sk *ent.Skill, pipNames, binaryNames map[string]struct{}) bool {
	switch sk.InstallMethod {
	case "binary":
		_, ok := binaryNames[sk.Name]
		return ok
	case "pip", "pip-requirements", "npm":
		_, ok := pipNames[strings.ToLower(sk.Name)]
		return ok
	default:
		_, ok := pipNames[strings.ToLower(sk.Name)]
		if ok {
			return true
		}
		_, ok = binaryNames[sk.Name]
		return ok
	}
}

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
		downloadArgs := []string{
			"download",
			"-i", "https://pypi.tuna.tsinghua.edu.cn/simple",
			"--trusted-host", "pypi.tuna.tsinghua.edu.cn",
			"--only-binary=:all:",
			"-d", tmpDir, "--python-version", "3.10",
			"--platform", "manylinux2014_x86_64", sk.Name, "exceptiongroup",
		}
		if sk.Name == "fabric" {
			downloadArgs = append(downloadArgs, "wrapt")
		}
		cmd := exec.CommandContext(ctx, "pip3", downloadArgs...)
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
	installScript, err := buildInstallScript(sk)
	if err != nil {
		return err
	}
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
	verifyCmd, err := buildVerifyScript(sk)
	if err != nil {
		return err
	}
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

// InstalledOnAgent returns the installed status of every offline-packaged skill
// by comparing skill names with the agent's pip package list.
func (s *Service) InstalledOnAgent(ctx context.Context, agentIP string) (map[string]bool, error) {
	sks, err := s.Ent.Skill.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	listCmd := "python3 -m pip list --format=json 2>/dev/null || pip3 list --format=json"
	var agentSSH string
	if s.JumpHost != "" {
		agentSSH = fmt.Sprintf("sshpass -p %s ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 %s@%s '%s'",
			s.AgentPass, s.AgentUser, agentIP, listCmd)
	} else {
		agentSSH = listCmd
	}
	c := s.sshCmd(agentSSH)
	c = exec.CommandContext(ctx, c.Args[0], c.Args[1:]...)
	out, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pip list failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	jsonOut := strings.TrimSpace(string(out))
	start := strings.Index(jsonOut, "[")
	end := strings.LastIndex(jsonOut, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("pip list returned no JSON array: %s", jsonOut)
	}
	var pkgs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(jsonOut[start:end+1]), &pkgs); err != nil {
		return nil, fmt.Errorf("parse pip list: %w", err)
	}
	installedNames := make(map[string]struct{}, len(pkgs))
	for _, pkg := range pkgs {
		installedNames[strings.ToLower(pkg.Name)] = struct{}{}
	}

	binaryCmd := "find \"$HOME/.local/share/skills\" -mindepth 1 -maxdepth 1 -type d -exec basename {} \\; 2>/dev/null || true"
	var binarySSH string
	if s.JumpHost != "" {
		binarySSH = fmt.Sprintf("sshpass -p %s ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 %s@%s '%s'",
			s.AgentPass, s.AgentUser, agentIP, binaryCmd)
	} else {
		binarySSH = binaryCmd
	}
	bc := s.sshCmd(binarySSH)
	bc = exec.CommandContext(ctx, bc.Args[0], bc.Args[1:]...)
	bout, berr := bc.CombinedOutput()
	if berr != nil {
		return nil, fmt.Errorf("binary skill probe failed: %w (output: %s)", berr, strings.TrimSpace(string(bout)))
	}
	binaryNames := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(bout)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			binaryNames[line] = struct{}{}
		}
	}

	result := make(map[string]bool, len(sks))
	for _, sk := range sks {
		if sk.PackageURL == "" {
			continue
		}
		result[sk.ID.String()] = skillInstalled(sk, installedNames, binaryNames)
	}
	return result, nil
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
	mux.HandleFunc("GET /v1/skills/installed/{agentIp}", s.handleInstalled)
	// Stripped paths (when outer mux uses subtree pattern /v1/skills/)
	mux.HandleFunc("POST /sync/{skillId}", s.handleSync)
	mux.HandleFunc("POST /upload/{skillId}", s.handleUpload)
	mux.HandleFunc("POST /install/{agentIp}/{skillId}", s.handleInstall)
	mux.HandleFunc("GET /installed/{agentIp}", s.handleInstalled)
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

func (s *Service) handleInstalled(w http.ResponseWriter, r *http.Request) {
	agentIP := r.PathValue("agentIp")
	if agentIP == "" {
		writeJSON(w, 400, map[string]string{"error": "missing agentIp"})
		return
	}
	installed, err := s.InstalledOnAgent(r.Context(), agentIP)
	if err != nil {
		log.Printf("[skills] installed check error: %v", err)
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "installed": installed})
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
