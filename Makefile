.PHONY: generate build test lint run tidy migrate-diff migrate-apply migrate-status

# Regenerate Ent + gqlgen code.
generate:
	go generate ./ent/...
	go run github.com/99designs/gqlgen generate

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

# Versioned migrations (prod; dev/sqlite still auto-migrate). Needs Atlas CLI.
# Generate a migration after a schema change (ATLAS_DEV_URL = throwaway pg):
migrate-diff:
	atlas migrate diff $(name) --env ent

# Apply pending migrations to DATABASE_URL:
migrate-apply:
	atlas migrate apply --env ent --url "$(DATABASE_URL)"

# Show applied/pending status + drift:
migrate-status:
	atlas migrate status --env ent --url "$(DATABASE_URL)"
