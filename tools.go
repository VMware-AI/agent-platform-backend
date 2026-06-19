//go:build tools

// Package tools pins code-generation tool dependencies so `go mod tidy`
// keeps them in go.mod. Not built into the binary.
package tools

import (
	_ "entgo.io/ent/cmd/ent"
	_ "github.com/99designs/gqlgen"
)
