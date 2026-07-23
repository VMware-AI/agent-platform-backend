package graph

// deploy_targets.go holds the read-only validation/resolution phase of
// DeployAgent. It is deliberately NOT a *.resolvers.go file so `go run gqlgen`
// (which rewrites *.resolvers.go) never mangles it.

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/ent"
	entskill "github.com/VMware-AI/agent-platform-backend/ent/skill"
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
	gwConn       *ent.GatewayConnection
	ownerID      uuid.UUID
	familyID     uuid.UUID
	versionID    uuid.UUID
	poolID       uuid.UUID
	fam          *ent.OvaTemplateFamily
	version      *ent.OvaTemplateVersion
	pool         *ent.ResourcePool
	cred         secrets.Credential
	tenantID     *uuid.UUID
	skills       []*ent.Skill
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
	gw, gwConn := r.deployGateway(ctx, deptID)
	if gw == nil {
		return nil, gqlerror.Errorf("deploy is not configured (gateway required)")
	}
	gwURL := ""
	if gwConn != nil {
		gwURL = deployGatewayPublicURL(gwConn, r.ControlPlaneURL)
		if gwURL == "" {
			return nil, gqlerror.Errorf("deploy is not configured (agent-reachable LiteLLM URL required)")
		}
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
	cred, err := r.resolveSecret(ctx, pool.SecretRef, secretPurposeVCenterConnect)
	if err != nil {
		return nil, fmt.Errorf("resolve pool credentials: %w", err)
	}

	// 3) tenant from the caller's write scope. The agent row itself is created by
	//    DeployAgent (the first side effect); this only resolves the scope.
	tenantID, err := writeTenant(ctx)
	if err != nil {
		return nil, err
	}
	skills, err := r.resolveDeploySkills(ctx, fam.Skills, input.SkillIds)
	if err != nil {
		return nil, err
	}

	return &deployTargets{
		deptID:       deptID,
		deployTeamID: deployTeamID,
		gw:           gw,
		gwURL:        gwURL,
		gwConn:       gwConn,
		ownerID:      ownerID,
		familyID:     familyID,
		versionID:    versionID,
		poolID:       poolID,
		fam:          fam,
		version:      version,
		pool:         pool,
		cred:         cred,
		tenantID:     tenantID,
		skills:       skills,
	}, nil
}

func (r *mutationResolver) resolveTemplateSkills(ctx context.Context, names []string) ([]*ent.Skill, error) {
	if len(names) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(names))
	clean := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		clean = append(clean, name)
	}
	if len(clean) == 0 {
		return nil, nil
	}
	skills, err := r.Ent.Skill.Query().Where(entskill.NameIn(clean...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve template skills: %w", err)
	}
	found := make(map[string]struct{}, len(skills))
	for _, sk := range skills {
		found[sk.Name] = struct{}{}
	}
	for _, name := range clean {
		if _, ok := found[name]; !ok {
			log.Printf("deploy: template skill %q has no synced package row; skipping", name)
		}
	}
	return skills, nil
}

func (r *mutationResolver) resolveDeploySkills(ctx context.Context, templateNames []string, selectedIDs []string) ([]*ent.Skill, error) {
	skills, err := r.resolveTemplateSkills(ctx, templateNames)
	if err != nil {
		return nil, err
	}
	if len(selectedIDs) == 0 {
		return skills, nil
	}
	ids := make([]uuid.UUID, 0, len(selectedIDs))
	for _, raw := range selectedIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, gqlerror.Errorf("invalid skillId")
		}
		ids = append(ids, id)
	}
	selected, err := r.Ent.Skill.Query().Where(entskill.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve selected skills: %w", err)
	}
	if len(selected) != len(ids) {
		return nil, gqlerror.Errorf("selected skill not found")
	}
	byID := make(map[uuid.UUID]*ent.Skill, len(skills)+len(selected))
	for _, sk := range skills {
		byID[sk.ID] = sk
	}
	for _, sk := range selected {
		byID[sk.ID] = sk
	}
	out := make([]*ent.Skill, 0, len(byID))
	for _, sk := range byID {
		out = append(out, sk)
	}
	return out, nil
}

func deployGatewayPublicURL(g *ent.GatewayConnection, controlPlaneURL string) string {
	if g == nil {
		return ""
	}
	if g.PublicURL != nil && strings.TrimSpace(*g.PublicURL) != "" {
		return strings.TrimSpace(*g.PublicURL)
	}
	return strings.TrimSpace(g.Endpoint)
}
