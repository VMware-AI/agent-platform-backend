.PHONY: generate build test lint run tidy

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
