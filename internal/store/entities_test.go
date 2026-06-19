package store_test

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func TestNewEntities_CRUD(t *testing.T) {
	ctx := context.Background()
	client, err := store.Open(ctx, "") // in-memory sqlite, migrated
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	// Artifact (Content lib)
	a, err := client.Artifact.Create().
		SetName("goose-installer").
		SetKind(artifact.KindPackage).
		SetVersion("1.0.0").
		SetURI("https://repo.internal/goose-1.0.0.tar.gz").
		SetSha256("abc123").
		Save(ctx)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	if a.Kind != artifact.KindPackage {
		t.Fatalf("artifact kind = %v", a.Kind)
	}

	// Skill (Skill hub)
	if _, err := client.Skill.Create().
		SetName("vmware-ops").SetVersion("1.3.0").
		SetURI("https://repo.internal/skills/vmware-ops-1.3.0").Save(ctx); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	// Image (Harbor)
	img, err := client.Image.Create().
		SetRepository("agent-platform/agent-vm").SetTag("1.4.0").
		SetDigest("sha256:deadbeef").SetSigned(true).Save(ctx)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if !img.Signed {
		t.Fatal("image should be signed")
	}

	// ResourcePool (vCenter) — credentials are a reference, never plaintext
	pool, err := client.ResourcePool.Create().
		SetName("vCenter_OC1").
		SetEndpoint("https://vcenter.internal").
		SetSecretRef("vault://resource-pools/oc1").
		Save(ctx)
	if err != nil {
		t.Fatalf("create resource pool: %v", err)
	}
	if pool.Status.String() != "disconnected" {
		t.Fatalf("default pool status = %v, want disconnected", pool.Status)
	}

	// counts
	if n, _ := client.Artifact.Query().Count(ctx); n != 1 {
		t.Fatalf("artifact count = %d", n)
	}
}

func TestArtifact_UniqueNameVersion(t *testing.T) {
	ctx := context.Background()
	client, _ := store.Open(ctx, "")
	defer client.Close()
	mk := func() error {
		_, err := client.Artifact.Create().
			SetName("dup").SetKind(artifact.KindConfig).
			SetVersion("1.0.0").SetURI("x").Save(ctx)
		return err
	}
	if err := mk(); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := mk(); err == nil {
		t.Fatal("duplicate name+version should violate unique index")
	}
}
