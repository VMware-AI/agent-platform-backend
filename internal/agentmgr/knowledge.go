package agentmgr

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
)

// LLD-11 §6 — daemon-facing knowledge-pack delivery. An OKF bundle is a "large
// file" that must reach an air-gapped VM. Rather than open a new NSX egress
// (VM→mirror), we reuse the LLD-08 control-plane channel the daemon already has:
// the same VM bearer authenticates, and the control plane serves the bundle.
// This keeps gateway-only intact (the VM only ever talks to the control plane).

// knowledgeScope restricts an Artifact query to knowledge packs the enrolled VM
// may see (LLD-11 §7, mirrors LLD-10 contentScopeFor): its own tenant + the
// platform-public (NULL-tenant) namespace. A platform-only VM (no tenant) sees
// only platform-public packs.
func knowledgeScope(q *ent.ArtifactQuery, enr *ent.AgentEnrollment) *ent.ArtifactQuery {
	q = q.Where(artifact.KindEQ(artifact.KindKnowledge))
	if enr.TenantID != nil {
		return q.Where(artifact.Or(
			artifact.TenantIDEQ(*enr.TenantID),
			artifact.TenantIDIsNil(),
		))
	}
	return q.Where(artifact.TenantIDIsNil())
}

// ListKnowledge returns the knowledge packs the VM is authorized to fetch.
func (s *Service) ListKnowledge(ctx context.Context, enr *ent.AgentEnrollment) ([]*ent.Artifact, error) {
	return knowledgeScope(s.Ent.Artifact.Query(), enr).
		Order(ent.Asc(artifact.FieldName, artifact.FieldVersion)).All(ctx)
}

// GetKnowledge fetches one authorized knowledge pack by id. A pack outside the
// VM's tenant scope (or non-knowledge, or missing) returns ent's NotFound — the
// caller maps that to 404 with no existence oracle (AC-4).
func (s *Service) GetKnowledge(ctx context.Context, enr *ent.AgentEnrollment, id uuid.UUID) (*ent.Artifact, error) {
	return knowledgeScope(s.Ent.Artifact.Query(), enr).
		Where(artifact.IDEQ(id)).Only(ctx)
}

// knowledgePack is the list-endpoint shape the daemon consumes to decide what to
// pull and how to verify it.
type knowledgePack struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Sha256  string `json:"sha256"`
	// inline = bundle bytes are served by the get endpoint (M1, OQ-1). uri-only
	// packs await the deferred object backend and are not yet servable.
	Inline bool `json:"inline"`
}

// handleKnowledgeList authenticates the VM bearer and returns its authorized
// knowledge packs (LLD-11 §6/§7).
func (s *Service) handleKnowledgeList(w http.ResponseWriter, r *http.Request) {
	enr := s.authVM(w, r)
	if enr == nil {
		return
	}
	packs, err := s.ListKnowledge(r.Context(), enr)
	if err != nil {
		writeErr(w, http.StatusInternalServerError)
		return
	}
	out := make([]knowledgePack, 0, len(packs))
	for _, a := range packs {
		out = append(out, knowledgePack{
			ID: a.ID.String(), Name: a.Name, Version: a.Version,
			Sha256: a.Sha256, Inline: a.Content != "",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleKnowledgeGet serves one authorized bundle's bytes (LLD-11 §6). The
// daemon verifies the X-Content-Sha256 header against the bytes before use
// (AC-5). M1 serves inline content only; a uri-only pack (object backend,
// deferred per OQ-1) returns 501.
func (s *Service) handleKnowledgeGet(w http.ResponseWriter, r *http.Request) {
	enr := s.authVM(w, r)
	if enr == nil {
		return
	}
	id, err := uuid.Parse(r.PathValue("artifact_id"))
	if err != nil {
		writeErr(w, http.StatusNotFound) // malformed id → no oracle
		return
	}
	a, err := s.GetKnowledge(r.Context(), enr, id)
	if ent.IsNotFound(err) {
		writeErr(w, http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError)
		return
	}
	if a.Content == "" {
		// Object-backed bundle: control-plane object storage is deferred (OQ-1).
		writeErr(w, http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Sha256", a.Sha256)
	w.Header().Set("ETag", `"`+a.Sha256+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(a.Content))
}

// authVM authenticates the VM bearer for daemon-facing knowledge routes. On
// failure it writes an opaque 401 and returns nil (fail-closed).
func (s *Service) authVM(w http.ResponseWriter, r *http.Request) *ent.AgentEnrollment {
	vmID, tok := r.PathValue("vm_id"), bearer(r)
	if vmID == "" || tok == "" {
		writeErr(w, http.StatusUnauthorized)
		return nil
	}
	enr, err := s.Authenticate(r.Context(), vmID, tok)
	if err != nil {
		writeErr(w, http.StatusUnauthorized)
		return nil
	}
	return enr
}
