package graph

// This file will be automatically regenerated based on the schema, any resolver
// implementations will be copied through when generating.

import (
	"context"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
	"github.com/VMware-AI/agent-platform-backend/ent/image"
	"github.com/VMware-AI/agent-platform-backend/ent/skill"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// --- Artifact (Content lib 制品库) ---

func (r *mutationResolver) UpsertArtifact(ctx context.Context, input model.UpsertArtifactInput) (*model.Artifact, error) {
	existing, err := r.Ent.Artifact.Query().
		Where(artifact.Name(input.Name), artifact.Version(input.Version)).Only(ctx)
	var a *ent.Artifact
	switch {
	case ent.IsNotFound(err):
		c := r.Ent.Artifact.Create().
			SetName(input.Name).SetKind(artifact.Kind(input.Kind)).
			SetVersion(input.Version).SetURI(input.URI)
		if input.Sha256 != nil {
			c.SetSha256(*input.Sha256)
		}
		a, err = c.Save(ctx)
	case err != nil:
		return nil, err
	default:
		u := existing.Update().SetKind(artifact.Kind(input.Kind)).SetURI(input.URI)
		if input.Sha256 != nil {
			u.SetSha256(*input.Sha256)
		}
		a, err = u.Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	r.audit(ctx, "artifact.upsert", "artifact", a.ID.String(), true, actorID(auth.FromContext(ctx)))
	return toModelArtifact(a), nil
}

func (r *mutationResolver) DeleteArtifact(ctx context.Context, id string) (bool, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return false, gqlerror.Errorf("invalid id")
	}
	if err := r.Ent.Artifact.DeleteOneID(uid).Exec(ctx); err != nil {
		return false, err
	}
	r.audit(ctx, "artifact.delete", "artifact", id, true, actorID(auth.FromContext(ctx)))
	return true, nil
}

func (r *queryResolver) Artifacts(ctx context.Context) ([]model.Artifact, error) {
	as, err := r.Ent.Artifact.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Artifact, 0, len(as))
	for _, a := range as {
		out = append(out, *toModelArtifact(a))
	}
	return out, nil
}

// --- Skill (Skill hub) ---

func (r *mutationResolver) UpsertSkill(ctx context.Context, input model.UpsertSkillInput) (*model.Skill, error) {
	existing, err := r.Ent.Skill.Query().
		Where(skill.Name(input.Name), skill.Version(input.Version)).Only(ctx)
	var s *ent.Skill
	switch {
	case ent.IsNotFound(err):
		c := r.Ent.Skill.Create().SetName(input.Name).SetVersion(input.Version).SetURI(input.URI)
		if input.Description != nil {
			c.SetDescription(*input.Description)
		}
		s, err = c.Save(ctx)
	case err != nil:
		return nil, err
	default:
		u := existing.Update().SetURI(input.URI)
		if input.Description != nil {
			u.SetDescription(*input.Description)
		}
		s, err = u.Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	r.audit(ctx, "skill.upsert", "skill", s.ID.String(), true, actorID(auth.FromContext(ctx)))
	return toModelSkill(s), nil
}

func (r *mutationResolver) DeleteSkill(ctx context.Context, id string) (bool, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return false, gqlerror.Errorf("invalid id")
	}
	if err := r.Ent.Skill.DeleteOneID(uid).Exec(ctx); err != nil {
		return false, err
	}
	r.audit(ctx, "skill.delete", "skill", id, true, actorID(auth.FromContext(ctx)))
	return true, nil
}

func (r *queryResolver) Skills(ctx context.Context) ([]model.Skill, error) {
	ss, err := r.Ent.Skill.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Skill, 0, len(ss))
	for _, s := range ss {
		out = append(out, *toModelSkill(s))
	}
	return out, nil
}

// --- Image (Harbor 镜像仓) ---

func (r *mutationResolver) UpsertImage(ctx context.Context, input model.UpsertImageInput) (*model.Image, error) {
	signed := false
	if input.Signed != nil {
		signed = *input.Signed
	}
	existing, err := r.Ent.Image.Query().
		Where(image.Repository(input.Repository), image.Tag(input.Tag)).Only(ctx)
	var img *ent.Image
	switch {
	case ent.IsNotFound(err):
		c := r.Ent.Image.Create().SetRepository(input.Repository).SetTag(input.Tag).SetSigned(signed)
		if input.Digest != nil {
			c.SetDigest(*input.Digest)
		}
		img, err = c.Save(ctx)
	case err != nil:
		return nil, err
	default:
		u := existing.Update().SetSigned(signed)
		if input.Digest != nil {
			u.SetDigest(*input.Digest)
		}
		img, err = u.Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	r.audit(ctx, "image.upsert", "image", img.ID.String(), true, actorID(auth.FromContext(ctx)))
	return toModelImage(img), nil
}

func (r *mutationResolver) DeleteImage(ctx context.Context, id string) (bool, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return false, gqlerror.Errorf("invalid id")
	}
	if err := r.Ent.Image.DeleteOneID(uid).Exec(ctx); err != nil {
		return false, err
	}
	r.audit(ctx, "image.delete", "image", id, true, actorID(auth.FromContext(ctx)))
	return true, nil
}

func (r *queryResolver) Images(ctx context.Context) ([]model.Image, error) {
	is, err := r.Ent.Image.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Image, 0, len(is))
	for _, i := range is {
		out = append(out, *toModelImage(i))
	}
	return out, nil
}
