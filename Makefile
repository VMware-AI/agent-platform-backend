.PHONY: generate schema-dump apidocs docs docs-check build test lint run tidy migrate-diff migrate-apply migrate-status build-images release-images

# Image build/push settings.
IMAGE     ?= agent-platform-backend
REGISTRY  ?= quay.io/vmware-ai
PLATFORMS ?= linux/amd64,linux/arm64
BUILDER   ?= agent-platform-builder
VERSION   := $(shell cat VERSION)
TAG       := $(VERSION)-$(shell date -u +%Y%m%d)

# Regenerate Ent + gqlgen code.
generate:
	go generate ./ent/...
	go run github.com/99designs/gqlgen generate

# Merge every schema/*.graphql module into one reference SDL at docs/schema.graphql.
schema-dump:
	go run ./tools/schemadump

# Regenerate the GraphQL API reference (docs/api/*.md) from schema/*.graphql.
apidocs:
	python3 tools/apidocs/gen.py

# Regenerate every committed doc derived from the schema: the merged SDL and the
# API reference. Run this after any schema/*.graphql change and commit the result.
docs: schema-dump apidocs

# CI guard: regenerate the derived docs and fail if the committed copies drifted
# from the current schema. Keeps docs/schema.graphql + docs/api/*.md honest.
docs-check: docs
	@if ! git diff --quiet -- docs/; then \
		echo "docs/ is stale vs schema/*.graphql — run 'make docs' and commit:"; \
		git --no-pager diff --stat -- docs/; \
		exit 1; \
	fi

# Snapshot the console's GraphQL operations as contract fixtures (validated by
# TestClientOperationsMatchSchema). Console defaults to ../agent-platform-console.
client-fixtures:
	node tools/genclientfixtures/main.mjs $(CONSOLE)

tidy:
	go mod tidy

build:
	go build ./...

test:
	go test ./... -count=1

run:
	go run ./cmd/server

lint:
	gofmt -l .
	go vet ./...

# Versioned migrations (prod; dev/sqlite still auto-migrate).
# Generate a migration after a schema change. Uses Ent's native Atlas
# integration (./ent/migrate/main.go), not the atlas-provider-ent plugin.
# ATLAS_DEV_URL = a throwaway dev postgres used to compute the diff.
migrate-diff:
	@test -n "$(name)" || { echo "usage: make migrate-diff name=<change>"; exit 1; }
	go run -mod=mod ./ent/migrate/main.go $(name)

# Apply pending migrations to DATABASE_URL:
migrate-apply:
	atlas migrate apply --env ent --url "$(DATABASE_URL)"

# Show applied/pending status + drift:
migrate-status:
	atlas migrate status --env ent --url "$(DATABASE_URL)"

# Multi-arch build (no push). Loads a manifest list into the local docker
# daemon so `docker run --platform …` works for either arch.
build-images:
	docker buildx create --name $(BUILDER) --use --driver docker-container 2>/dev/null || true
	docker buildx build \
		--builder $(BUILDER) \
		--platform $(PLATFORMS) \
		--tag $(REGISTRY)/$(IMAGE):$(TAG) \
		--load \
		.

# Multi-arch build + push to $(REGISTRY). Depends on build-images so a failed
# build never reaches the registry (pushes are not undo-able).
release-images: build-images
	docker buildx build \
		--builder $(BUILDER) \
		--platform $(PLATFORMS) \
		--tag $(REGISTRY)/$(IMAGE):$(TAG) \
		--push \
		.
