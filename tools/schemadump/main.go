// Command schemadump merges every schema/*.graphql source into a single,
// deduplicated SDL reference at docs/schema.graphql — the whole GraphQL API
// (auth/login, users & roles, resource pools, model gateway, …) in one file.
//
// The per-module files under schema/ remain the source of truth (gqlgen's
// follow-schema resolver layout keys off their filenames); this generated file
// is a read-only reference for frontend devs and reviewers.
//
// Run from the repo root:  go run ./tools/schemadump   (or: make schema-dump)
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

const outPath = "docs/schema.graphql"

func main() {
	files, err := filepath.Glob("schema/*.graphql")
	if err != nil {
		fatal("glob schema/*.graphql: %v", err)
	}
	if len(files) == 0 {
		fatal("no schema/*.graphql files found — run from the repo root")
	}
	sort.Strings(files)

	sources := make([]*ast.Source, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fatal("read %s: %v", f, err)
		}
		sources = append(sources, &ast.Source{Name: f, Input: string(b)})
	}

	schema, gqlErr := gqlparser.LoadSchema(sources...)
	if gqlErr != nil {
		fatal("load schema: %v", gqlErr)
	}

	out, err := os.Create(outPath)
	if err != nil {
		fatal("create %s: %v", outPath, err)
	}

	fmt.Fprintln(out, "# AUTO-GENERATED — do not edit by hand.")
	fmt.Fprintln(out, "# Complete merged SDL of every schema/*.graphql module (the whole API in one file).")
	fmt.Fprintln(out, "# Regenerate after any schema change:  make schema-dump")
	fmt.Fprintln(out, "# Source of truth = the per-module files under schema/.")
	fmt.Fprintln(out)
	formatter.NewFormatter(out).FormatSchema(schema)

	// Close explicitly (not deferred): a truncated write surfaces only on Close,
	// and a silently-truncated SDL would be worse than a loud failure.
	if err := out.Close(); err != nil {
		fatal("close %s: %v", outPath, err)
	}
	fmt.Printf("wrote %s (%d source files merged)\n", outPath, len(files))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "schemadump: "+format+"\n", args...)
	os.Exit(1)
}
