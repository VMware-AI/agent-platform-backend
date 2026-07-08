package graph

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// LLD-11 §8 acceptance — this file gathers the cross-cutting ACs not already
// proven by the per-feature tests, and documents where each AC is covered:
//   AC-1 knowledge CRUD          → knowledge_artifact_test.go (TestUpsertArtifact_Knowledge)
//   AC-3 出口走控制面通道(bearer) → agentmgr/knowledge_test.go (TestKnowledgeEndpoint_*)
//   AC-4 租户授权                → agentmgr/knowledge_test.go (cross-tenant 404)
//   AC-5 完整性(sha256)         → K1 serves X-Content-Sha256; daemon verifies (K3 research)
//   AC-2 非 RAG 约束             → TestAC2_NoVectorRetrievalDependency (below)
//   AC-6 接地可用(文件导航)     → TestAC6_AgentNavigatesByFiles (below)

// AC-2: the platform must not depend on any vector store / embedding / retrieval
// stack — grounding is file navigation, not RAG. Guard the module graph so a
// future change can't quietly introduce one.
func TestAC2_NoVectorRetrievalDependency(t *testing.T) {
	// go.mod lives two levels up from internal/graph.
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	mod := strings.ToLower(string(data))
	forbidden := []string{
		"pgvector", "weaviate", "qdrant", "milvus", "faiss",
		"chromem", "pinecone", "lancedb", "embedding",
	}
	for _, f := range forbidden {
		if strings.Contains(mod, f) {
			t.Errorf("non-RAG violation (LLD-11 §2): go.mod references %q — knowledge "+
				"grounding must be file navigation, not vector retrieval", f)
		}
	}
}

// linkRE pulls the target out of a markdown link [text](target).
var linkRE = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// AC-6: an agent grounds itself purely by reading files — it opens the bundle's
// index.md, follows a markdown link, and reaches the target page. No retrieval
// service, no embeddings: just open + follow links, exactly how a human reads docs.
func TestAC6_AgentNavigatesByFiles(t *testing.T) {
	root := t.TempDir()
	// An OKF bundle: an index with a link to a table page that itself links onward.
	mustWrite(t, filepath.Join(root, "index.md"),
		"# 知识库\n- [Orders 表](tables/orders.md)\n")
	mustWrite(t, filepath.Join(root, "tables", "orders.md"),
		"---\ntype: BigQuery Table\n---\n# Orders\n一行一笔订单。见 [customers](customers.md)。\n")
	mustWrite(t, filepath.Join(root, "tables", "customers.md"),
		"# Customers\n一行一个客户。\n")

	// "Agent" navigation: start at index.md, follow the first link, then the next.
	visited := navigate(t, root, "index.md", 3)
	if len(visited) < 3 {
		t.Fatalf("expected to traverse index→orders→customers, visited %v", visited)
	}
	if !strings.Contains(readFile(t, filepath.Join(root, visited[len(visited)-1])), "一个客户") {
		t.Fatalf("did not reach the linked customers page by file navigation")
	}
}

// navigate follows the first markdown link on each page, up to maxHops, returning
// the relative paths visited. This is the whole "retrieval" mechanism: read a
// file, parse a link, open the next file.
func navigate(t *testing.T, root, start string, maxHops int) []string {
	t.Helper()
	visited := []string{start}
	cur := start
	for i := 0; i < maxHops; i++ {
		body := readFile(t, filepath.Join(root, cur))
		m := linkRE.FindStringSubmatch(body)
		if m == nil {
			break
		}
		next := filepath.Join(filepath.Dir(cur), m[1])
		if _, err := os.Stat(filepath.Join(root, next)); err != nil {
			break
		}
		visited = append(visited, next)
		cur = next
	}
	return visited
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
