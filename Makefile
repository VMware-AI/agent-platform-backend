.PHONY: generate schema-dump build test lint run tidy migrate-diff migrate-apply migrate-status

# Regenerate Ent + gqlgen code.
generate:
	go generate ./ent/...
	go run github.com/99designs/gqlgen generate

# Merge every schema/*.graphql module into one reference SDL at docs/schema.graphql.
schema-dump:
	go run ./tools/schemadump

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
