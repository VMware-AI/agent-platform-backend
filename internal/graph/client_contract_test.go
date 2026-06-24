package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestClientOperationsMatchSchema validates every agent-platform-console GraphQL
// operation (snapshotted under testdata/client_operations/) against the live
// backend schema. A backend schema change that breaks the frontend contract —
// a renamed field, a dropped query, a changed nullability/arg — fails here in CI,
// before it ever reaches the running console.
//
// The fixtures are a point-in-time snapshot of the console's operations; refresh
// them when the frontend changes (see testdata/client_operations/README.md).
func TestClientOperationsMatchSchema(t *testing.T) {
	schema := loadSDLForTest(t)

	fixtures, err := filepath.Glob("testdata/client_operations/*.graphql")
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no client operation fixtures found under testdata/client_operations/")
	}

	for _, f := range fixtures {
		t.Run(filepath.Base(f), func(t *testing.T) {
			doc, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			// LoadQuery parses AND validates the operation against the schema:
			// unknown fields/args, bad fragment targets, undeclared variables, and
			// type mismatches all surface as errors here.
			if _, errs := gqlparser.LoadQuery(schema, string(doc)); len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("operation does not match backend schema: %s", e.Message)
				}
			}
		})
	}
}

// loadSDLForTest loads the backend SDL from schema/*.graphql (repo root is two
// levels up from this package).
func loadSDLForTest(t *testing.T) *ast.Schema {
	t.Helper()
	files, err := filepath.Glob("../../schema/*.graphql")
	if err != nil {
		t.Fatalf("glob schema: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no schema/*.graphql files found from the graph package dir")
	}
	sources := make([]*ast.Source, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		sources = append(sources, &ast.Source{Name: f, Input: string(b)})
	}
	schema, gerr := gqlparser.LoadSchema(sources...)
	if gerr != nil {
		t.Fatalf("load schema: %v", gerr)
	}
	return schema
}
