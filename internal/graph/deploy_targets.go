package graph

// deploy_targets.go holds the read-only validation/resolution phase of
// DeployAgent. It is deliberately NOT a *.resolvers.go file so `go run gqlgen`
// (which rewrites *.resolvers.go) never mangles it.

import (
	"context"
	"fmt"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// deployTargets is everything DeployAgent's VM/key/row lifecycle consumes after
// the read-only validation/resolution phase. Resolving it performs NO side
// effects (no DB writes, no VM, no key, no enrollment) — it only parses input,
// reads catalog/pool rows and resolves credentials. The first side effect
// (r.Ent.Agent.Create()) stays in DeployAgent.
type deployTargets struct {
	deptID       *uuid.UUID
	deployTeamID string
	gw           gateway.Client
	gwURL        string
	ownerID      uuid.UUID
	familyID     uuid.UUID
	versionID    uuid.UUID
	poolID       uuid.UUID
	fam          *ent.OvaTemplateFamily
	version      *ent.OvaTemplateVersion
	pool         *ent.ResourcePool
	cred         secrets.Credential
	tenantID     *uuid.UUID
}

// resolveDeployTargets runs DeployAgent's read-only validation/resolution prefix
// and returns the resolved values, or the SAME first error for the same bad
// input. It writes nothing. The check order is preserved verbatim so the same
// error fires first for the same input.
func (r *mutationResolver) resolveDeployTargets(ctx context.Context, input model.DeployAgentInput) (*deployTargets, error) {
	if r.Secrets == nil || r.VCenterConnect == nil {
		return nil, gqlerror.Errorf("deploy is not configured (secrets/vcenter required)")
	}
	if input.Name == "" {
		return nil, gqlerror.Errorf("name is required")
	}
	// Resolve the gateway that issues this agent's key + whose public URL the VM
	// will call (LLD-13 §3.3): the chosen department's gateway, or the default.
	var deptID *uuid.UUID
	if input.DepartmentID != nil {
		did, err := uuid.Parse(*input.DepartmentID)
		if err != nil {
			return nil, gqlerror.Errorf("invalid departmentId")
		}
		deptID = &did
	}
	// The key's team == its litellm team == the department (LLD-13 §3.3, where
	// CreateDepartment sets teamID = deptID.String()). Persist it on the key and
	// pass it to GenerateKey so the key (a) is grouped under the department's
	// litellm team for budgeting and (b) RecycleAgent can route the revoke back to
	// the department's gateway via deptIDFromTeam(vk.TeamID). Empty (no department)
	// → default gateway, no team.
	var deployTeamID string
	if deptID != nil {
		deployTeamID = deptID.String()
	}
	gw, gwURL := r.deployGateway(ctx, deptID)
	if gw == nil {
		return nil, gqlerror.Errorf("deploy is not configured (gateway required)")
	}
	cu := auth.FromContext(ctx)
	ownerID, err := uuid.Parse(cu.ID)
	if err != nil {
		return nil, gqlerror.Errorf("invalid current user")
	}

	// 1) Resolve the catalog family (its `type` = the agent kind) + the chosen
	//    version (its ova_identifier = the source template) and validate the
	//    version belongs to the family.
	familyID, err := uuid.Parse(input.TemplateFamilyID)
	if err != nil {
		return nil, gqlerror.Errorf("invalid templateFamilyId")
	}
	versionID, err := uuid.Parse(input.TemplateVersionID)
	if err != nil {
		return nil, gqlerror.Errorf("invalid templateVersionId")
	}
	fam, err := r.Ent.OvaTemplateFamily.Get(ctx, familyID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, gqlerror.Errorf("template family not found")
		}
		return nil, err
	}
	version, err := r.Ent.OvaTemplateVersion.Get(ctx, versionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, gqlerror.Errorf("template version not found")
		}
		return nil, err
	}
	verFamily, err := version.QueryFamily().Only(ctx)
	if err != nil || verFamily.ID != familyID {
		return nil, gqlerror.Errorf("template version does not belong to the family")
	}

	// 2) Resolve the target pool + its vCenter credentials.
	poolID, err := uuid.Parse(input.ResourcePoolID)
	if err != nil {
		return nil, gqlerror.Errorf("invalid resourcePoolId")
	}
	pool, err := r.Ent.ResourcePool.Get(ctx, poolID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, gqlerror.Errorf("resource pool not found")
		}
		return nil, err
	}
	if pool.SecretRef == "" {
		return nil, gqlerror.Errorf("resource pool has no secret_ref")
	}
	cred, err := r.Secrets.Resolve(ctx, pool.SecretRef)
	if err != nil {
		return nil, fmt.Errorf("resolve pool credentials: %w", err)
	}

	// 3) tenant from the caller's write scope. The agent row itself is created by
	//    DeployAgent (the first side effect); this only resolves the scope.
	tenantID, err := writeTenant(ctx)
	if err != nil {
		return nil, err
	}

	return &deployTargets{
		deptID:       deptID,
		deployTeamID: deployTeamID,
		gw:           gw,
		gwURL:        gwURL,
		ownerID:      ownerID,
		familyID:     familyID,
		versionID:    versionID,
		poolID:       poolID,
		fam:          fam,
		version:      version,
		pool:         pool,
		cred:         cred,
		tenantID:     tenantID,
	}, nil
}
