package graph

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/setting"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// Platform setting keys (LLD-13) — a small fixed vocabulary stored in the Setting
// key/value table, editable in the console rather than via startup env.
const (
	settingKeyAgentUser = "agent_user"
	defaultAgentUser    = "agent"

	// Package source (LLD-16 OQ-2): operator-configurable internal mirror, editable in
	// the console. url + user are plain settings; the password is stored encrypted
	// (secrets) and referenced by agent_pkg_pass_ref.
	settingKeyAgentPkgURL     = "agent_pkg_url"
	settingKeyAgentPkgUser    = "agent_pkg_user"
	settingKeyAgentPkgPassRef = "agent_pkg_pass_ref"
)

// getSetting returns the stored value for key, or def when the row is absent or
// its value is empty.
func (r *Resolver) getSetting(ctx context.Context, key, def string) string {
	s, err := r.Ent.Setting.Query().Where(setting.KeyEQ(key)).Only(ctx)
	if err != nil {
		// Absent row → default. A real DB error (not "not found") shouldn't be
		// silent — log it but still fall back so a settings read never hard-fails
		// install rendering.
		if !ent.IsNotFound(err) {
			log.Printf("getSetting %q: %v", key, err)
		}
		return def
	}
	if s.Value == "" {
		return def
	}
	return s.Value
}

// setSetting upserts a key/value (update-or-create; no reliance on a dialect
// UPSERT so it works the same on sqlite-dev and postgres-prod). If two writers
// race on the first write, both see n==0 and try Create; the loser hits the
// unique-key constraint, so it falls back to Update (last-writer-wins) rather
// than surfacing a raw DB error to the admin.
func (r *Resolver) setSetting(ctx context.Context, key, value string) error {
	n, err := r.Ent.Setting.Update().Where(setting.KeyEQ(key)).SetValue(value).Save(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := r.Ent.Setting.Create().SetKey(key).SetValue(value).Save(ctx); err != nil {
		// Only a lost create-race (unique-key violation) is recoverable: the row
		// now exists, so update it (last-writer-wins). Any other error is real.
		if !ent.IsConstraintError(err) {
			return err
		}
		if _, uerr := r.Ent.Setting.Update().Where(setting.KeyEQ(key)).SetValue(value).Save(ctx); uerr != nil {
			return uerr
		}
	}
	return nil
}

// renderInstallVars builds the {{PLACEHOLDER}} substitutions for catalog install
// commands: the static env-derived vars (AGENT_PKG_BASE_URL) PLUS AGENT_USER read
// from the DB platform setting (LLD-13), so AGENT_USER is operator-editable, not a
// startup env. Called once per template-returning resolver (lists fetch once).
func (r *Resolver) renderInstallVars(ctx context.Context) map[string]string {
	vars := make(map[string]string, len(r.InstallVars)+1)
	for k, v := range r.InstallVars {
		vars[k] = v
	}
	vars["AGENT_USER"] = r.getSetting(ctx, settingKeyAgentUser, defaultAgentUser)
	// Package mirror: DB platform settings (console-configurable, OQ-2) take
	// precedence over the startup env; resolveAgentPkgBaseURL falls back to env.
	if base := r.resolveAgentPkgBaseURL(ctx); base != "" {
		vars["AGENT_PKG_BASE_URL"] = base
	}
	return vars
}

// resolveAgentPkgBaseURL builds the internal agent-package mirror base. DB platform
// settings (OQ-2, console-editable) win; when the url setting is unset it falls back
// to the startup env value (r.AgentPkgBaseURL) for backward compat. url + user are
// plain settings; the password is stored encrypted (secrets) under agent_pkg_pass_ref.
// Assembles ftp://user:pass@host/path. NEVER log the result (may embed credentials).
func (r *Resolver) resolveAgentPkgBaseURL(ctx context.Context) string {
	base := r.getSetting(ctx, settingKeyAgentPkgURL, "")
	if base == "" {
		return r.AgentPkgBaseURL // env fallback (may already embed credentials)
	}
	user := r.getSetting(ctx, settingKeyAgentPkgUser, "")
	if user == "" {
		return strings.TrimRight(base, "/") // no credentials configured
	}
	pass := ""
	if ref := r.getSetting(ctx, settingKeyAgentPkgPassRef, ""); ref != "" && r.Secrets != nil {
		if cred, err := r.Secrets.Resolve(ctx, ref); err == nil {
			pass = cred.Password
		}
	}
	return injectCreds(base, user, pass)
}

// injectCreds inserts user[:pass]@ userinfo into a scheme://host/... URL, trimming a
// trailing slash. On a malformed URL it returns the input unchanged (best effort —
// the daemon validates the final URL before use).
func injectCreds(rawURL, user, pass string) string {
	trimmed := strings.TrimRight(rawURL, "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return trimmed
	}
	if pass != "" {
		u.User = url.UserPassword(user, pass)
	} else {
		u.User = url.User(user)
	}
	return u.String()
}

// validatePackageSourceURL guards the mutation boundary (LLD-16 OQ-2): the value is
// later stamped into guestinfo and fetched by the in-VM daemon, so a typo here would
// otherwise only surface as a FetchError inside the VM, several hops from the cause.
// Credentials must come via the separate user/password fields — an embedded userinfo
// would be silently overwritten by injectCreds.
func validatePackageSourceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("packageSourceUrl is not a valid URL: %v", err)
	}
	switch u.Scheme {
	case "ftp", "http", "https":
	default:
		return fmt.Errorf("packageSourceUrl must be ftp/http/https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("packageSourceUrl is missing a host")
	}
	if u.User != nil {
		return fmt.Errorf("packageSourceUrl must not embed credentials — use packageSourceUser/packageSourcePassword")
	}
	return nil
}

// setPackageSourcePassword stores the mirror password encrypted (secrets.Store) and
// records its ref under agent_pkg_pass_ref, deleting any prior secret. An empty
// password clears it. Requires a writable secret store (the prod DBStore is one); a
// read-only Resolver returns an error rather than silently dropping the password.
func (r *Resolver) setPackageSourcePassword(ctx context.Context, pass string) error {
	store, ok := r.Secrets.(secrets.Store)
	if !ok {
		return fmt.Errorf("secret store is read-only; cannot set package-source password")
	}
	oldRef := r.getSetting(ctx, settingKeyAgentPkgPassRef, "")
	newRef := ""
	if strings.TrimSpace(pass) != "" {
		ref, err := store.Put(ctx, "agent-pkg-source", secrets.Credential{Password: pass})
		if err != nil {
			return err
		}
		newRef = ref
	}
	if err := r.setSetting(ctx, settingKeyAgentPkgPassRef, newRef); err != nil {
		return err
	}
	// Best-effort cleanup of the superseded secret; never fail the update over it.
	if oldRef != "" && oldRef != newRef {
		if err := store.Delete(ctx, oldRef); err != nil {
			log.Printf("setPackageSourcePassword: delete old secret failed: %v", err)
		}
	}
	return nil
}
