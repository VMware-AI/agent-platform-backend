package graph_test

import (
	"net/http"
	"testing"

	"github.com/99designs/gqlgen/client"

	"github.com/VMware-AI/agent-platform-backend/ent/user"
)

// These deny-path tests drive the FULL GraphQL executor (executable schema +
// directives + session middleware via setupE2E), so the @hasRole / @hasPermission
// directives actually run on the real execution path — complementing the
// directive-impl unit matrix in rbac_denypath_test.go (which calls the directive
// funcs directly). They cover sensitive ops not exercised by e2e_perms_test.go.
//
// They assert the DENY half end-to-end: an under-privileged caller (and the
// unauthenticated caller) is rejected by the executor before reaching the resolver.
// The allow half for these particular ops is covered at the directive layer, since
// their resolvers reach external systems (vCenter / litellm) that aren't wired in
// the in-memory E2E harness.

func TestE2E_DenyPath_AdminOnlyMutations(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()

	userCookie := e.seedUser(t, "plain", user.RoleUser)
	obsCookie := e.seedUser(t, "obs", user.RoleObservability)
	taCookie := e.seedUser(t, "ta", user.RoleTenantAdmin)

	// modelRoutes query is @hasRole(any:[admin]). A plain user, observability, and
	// tenant-admin are ALL rejected; unauthenticated too.
	const modelRoutesQ = `{ modelRoutes { id } }`
	var mrResp struct {
		ModelRoutes []struct{ ID string }
	}
	denied := []struct {
		name   string
		cookie *http.Cookie
	}{
		{"plain user", userCookie},
		{"observability", obsCookie},
		{"tenant-admin", taCookie},
	}
	for _, d := range denied {
		if err := e.gql.Post(modelRoutesQ, &mrResp, client.AddCookie(d.cookie)); err == nil {
			t.Fatalf("%s must be denied modelRoutes (@hasRole admin)", d.name)
		}
		if mrResp.ModelRoutes != nil {
			t.Fatalf("%s denied modelRoutes must return nil data, got %v", d.name, mrResp.ModelRoutes)
		}
		mrResp.ModelRoutes = nil
	}
	if err := e.gql.Post(modelRoutesQ, &mrResp); err == nil {
		t.Fatal("unauthenticated must be denied modelRoutes")
	}
}

func TestE2E_DenyPath_DeployAgentAdminOnly(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()
	taCookie := e.seedUser(t, "ta", user.RoleTenantAdmin)

	// deployAgent is @hasRole(any:[admin]). A tenant-admin is rejected at the
	// directive, before any vCenter/OVA work in the resolver.
	const deployM = `mutation { deployAgent(input:{templateVersionId:"x", name:"n", resourcePoolId:"x"}){ id } }`
	var resp struct {
		DeployAgent struct{ ID string }
	}
	if err := e.gql.Post(deployM, &resp, client.AddCookie(taCookie)); err == nil {
		t.Fatal("tenant-admin must be denied deployAgent (@hasRole admin)")
	}
	if resp.DeployAgent.ID != "" {
		t.Fatalf("denied deployAgent must not return data, got %+v", resp.DeployAgent)
	}
	if err := e.gql.Post(deployM, &resp); err == nil {
		t.Fatal("unauthenticated must be denied deployAgent")
	}
}

func TestE2E_DenyPath_CreateDepartmentRoleGated(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()
	userCookie := e.seedUser(t, "plain", user.RoleUser)
	obsCookie := e.seedUser(t, "obs", user.RoleObservability)

	// createDepartment is @hasRole(any:[admin, tenant_admin]). A plain user and an
	// observability specialist are both rejected.
	const deptM = `mutation { createDepartment(input:{name:"d"}){ id name } }`
	var resp struct {
		CreateDepartment struct {
			ID   string
			Name string
		}
	}
	if err := e.gql.Post(deptM, &resp, client.AddCookie(userCookie)); err == nil {
		t.Fatal("plain user must be denied createDepartment")
	}
	if err := e.gql.Post(deptM, &resp, client.AddCookie(obsCookie)); err == nil {
		t.Fatal("observability must be denied createDepartment")
	}
	if resp.CreateDepartment.ID != "" {
		t.Fatalf("denied createDepartment must not return data, got %+v", resp.CreateDepartment)
	}
}

func TestE2E_DenyPath_PermissionMismatch(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()

	// issueVirtualKey requires key:manage — observability holds audit:view +
	// metering:view but NOT key:manage, so it is rejected even though it has SOME
	// permissions (proves perm keys don't bleed across each other).
	obsCookie := e.seedUser(t, "obs", user.RoleObservability)
	const issueM = `mutation { issueVirtualKey(input:{userId:"00000000-0000-0000-0000-000000000001"}){ id } }`
	var ivResp struct {
		IssueVirtualKey struct{ ID string }
	}
	if err := e.gql.Post(issueM, &ivResp, client.AddCookie(obsCookie)); err == nil {
		t.Fatal("observability must be denied issueVirtualKey (lacks key:manage)")
	}

	// Conversely, observability DOES hold audit:view, so requestLogs passes the
	// directive (resolver returns an empty list on the in-memory store).
	const logsQ = `{ requestLogs { requestId } }`
	var lResp struct {
		RequestLogs []struct{ RequestID string }
	}
	if err := e.gql.Post(logsQ, &lResp, client.AddCookie(obsCookie)); err != nil {
		t.Fatalf("observability holds audit:view, requestLogs should pass: %v", err)
	}
}
