package agentmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agentenrollment"
	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
)

// enrolledTenant enrolls a VM bound to a specific tenant and returns its bearer.
func enrolledTenant(t *testing.T, svc *Service, tenantID *uuid.UUID) (vmID, vmToken string) {
	t.Helper()
	ctx := context.Background()
	agentID := uuid.New()
	vmID = "vm-" + agentID.String()[:8]
	enrollTok, err := svc.IssueEnrollment(ctx, agentID, vmID, tenantID)
	if err != nil {
		t.Fatalf("IssueEnrollment: %v", err)
	}
	vmToken, err = svc.Enroll(ctx, vmID, enrollTok)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return vmID, vmToken
}

// Redeploy must refresh the enrollment's tenant scope: knowledgeScope keys
// authorization entirely off enr.TenantID, so a stale tenant_id on re-issue would
// be a cross-tenant isolation gap. Re-tenant → updated; re-issue untenanted → cleared.
func TestIssueEnrollment_RefreshesTenantOnRedeploy(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()
	ctx := context.Background()

	agentID := uuid.New()
	tenantA := uuid.New()
	tenantB := uuid.New()

	if _, err := svc.IssueEnrollment(ctx, agentID, "vm-x", &tenantA); err != nil {
		t.Fatalf("issue A: %v", err)
	}
	enr := svc.Ent.AgentEnrollment.Query().Where(agentenrollment.AgentID(agentID)).OnlyX(ctx)
	if enr.TenantID == nil || *enr.TenantID != tenantA {
		t.Fatalf("initial tenant = %v, want A", enr.TenantID)
	}

	// Redeploy re-tenanted to B → tenant_id must update.
	if _, err := svc.IssueEnrollment(ctx, agentID, "vm-x", &tenantB); err != nil {
		t.Fatalf("issue B: %v", err)
	}
	enr = svc.Ent.AgentEnrollment.Query().Where(agentenrollment.AgentID(agentID)).OnlyX(ctx)
	if enr.TenantID == nil || *enr.TenantID != tenantB {
		t.Fatalf("after re-tenant: tenant = %v, want B", enr.TenantID)
	}

	// Redeploy as a platform (untenanted) agent → tenant_id must clear.
	if _, err := svc.IssueEnrollment(ctx, agentID, "vm-x", nil); err != nil {
		t.Fatalf("issue nil: %v", err)
	}
	enr = svc.Ent.AgentEnrollment.Query().Where(agentenrollment.AgentID(agentID)).OnlyX(ctx)
	if enr.TenantID != nil {
		t.Fatalf("after untenanted redeploy: tenant = %v, want nil", enr.TenantID)
	}
}

func mkKnowledge(t *testing.T, client *ent.Client, name, body string, tenant *uuid.UUID) *ent.Artifact {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	return client.Artifact.Create().
		SetName(name).SetKind(artifact.KindKnowledge).SetVersion("1.0.0").
		SetURI("inline://" + name).SetContent(body).
		SetSha256(hex.EncodeToString(sum[:])).
		SetNillableTenantID(tenant).SaveX(context.Background())
}

// LLD-11 K1 / AC-3 + AC-4: the daemon-facing knowledge endpoint authenticates a
// VM bearer (no bearer → 401) and serves only knowledge the VM's tenant may see:
// its own tenant + platform-public (NULL); another tenant's private pack → 404
// (no existence oracle). Reuses the LLD-08 enrollment/bearer machinery.
func TestKnowledgeEndpoint_BearerAndTenant(t *testing.T) {
	svc, _, done := newTestService(t)
	defer done()

	tenantA := uuid.New()
	tenantB := uuid.New()
	vmA, tokA := enrolledTenant(t, svc, &tenantA)

	platformPack := mkKnowledge(t, svc.Ent, "platform-kb", "# Platform\nshared", nil)
	tenantAPack := mkKnowledge(t, svc.Ent, "a-kb", "# A\nprivate to A", &tenantA)
	tenantBPack := mkKnowledge(t, svc.Ent, "b-kb", "# B\nprivate to B", &tenantB)

	h := Handler(svc)
	get := func(path, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	base := "/v1/agents/" + vmA + "/knowledge/"

	// AC-3: no bearer → 401
	if rec := get(base+platformPack.ID.String(), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: code = %d, want 401", rec.Code)
	}
	// AC-3: bad bearer → 401
	if rec := get(base+platformPack.ID.String(), "garbage"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer: code = %d, want 401", rec.Code)
	}

	// platform-public pack → 200 + body + sha256 header (AC-3, AC-5 prep)
	rec := get(base+platformPack.ID.String(), tokA)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform pack: code = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "# Platform\nshared" {
		t.Fatalf("platform pack body = %q", rec.Body.String())
	}
	if rec.Header().Get("X-Content-Sha256") != platformPack.Sha256 {
		t.Fatalf("missing/incorrect sha256 header: %q", rec.Header().Get("X-Content-Sha256"))
	}

	// own-tenant private pack → 200 (AC-4)
	if rec := get(base+tenantAPack.ID.String(), tokA); rec.Code != http.StatusOK {
		t.Fatalf("own-tenant pack: code = %d, want 200", rec.Code)
	}
	// another tenant's private pack → 404, no oracle (AC-4)
	if rec := get(base+tenantBPack.ID.String(), tokA); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant pack: code = %d, want 404", rec.Code)
	}

	// list endpoint: returns platform + own-tenant, not tenant B
	listRec := get("/v1/agents/"+vmA+"/knowledge", tokA)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: code = %d, want 200", listRec.Code)
	}
	var packs []struct {
		ID, Name, Sha256 string
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &packs); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	names := map[string]bool{}
	for _, p := range packs {
		names[p.Name] = true
	}
	if !names["platform-kb"] || !names["a-kb"] {
		t.Fatalf("list missing authorized packs: %v", names)
	}
	if names["b-kb"] {
		t.Fatal("list leaked another tenant's private pack")
	}
}
