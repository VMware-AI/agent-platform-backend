.PHONY: generate schema-dump apidocs docs docs-check build test lint run tidy migrate-diff migrate-apply migrate-status release-images postman

# Image build/push settings.
IMAGE     ?= agent-platform-backend
REGISTRY  ?= quay.io/vmware-ai
PLATFORMS ?= linux/amd64,linux/arm64
BUILDER   ?= agent-platform-builder
VERSION   := $(shell cat VERSION)
TAG       ?= $(VERSION)-$(shell date -u +%Y%m%d)

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

# Versioned migrations are disabled during the dev (iteration) phase. The
# dev environment relies on `ent.Client.Schema.Create()` for fresh-DB
# bootstrap; versioned SQL migrations + the CI drift gate are restored when
# the maintainer declares a release phase (CLAUDE.md §2). The migrate-*
# targets intentionally fail loud so a stray call surfaces the suspension.
migrate-diff migrate-apply migrate-status:
	@echo "migrations are disabled during the dev phase (see CLAUDE.md §2)" >&2
	@exit 1

# Regenerate postman/agent-platform-backend.postman_collection.json from
# internal/graph/testdata/client_operations/*.graphql.
postman:
	python3 tools/postmangen/main.py

# Multi-arch build + push to $(REGISTRY). Tags :$(TAG) (versioned) + :latest.
# Always pushes — pushes are not undo-able; keep prod tags deliberate.
release-images:
	docker buildx create --name $(BUILDER) --use --driver docker-container 2>/dev/null || true
	docker buildx build \
		--builder $(BUILDER) \
		--platform $(PLATFORMS) \
		--tag $(REGISTRY)/$(IMAGE):$(TAG) \
		--tag $(REGISTRY)/$(IMAGE):latest \
		--push \
		.
