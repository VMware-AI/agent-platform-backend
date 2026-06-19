// Command server runs the Agent Platform GraphQL control-plane backend.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/config"
	"github.com/VMware-AI/agent-platform-backend/internal/graph"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx := context.Background()

	client, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer client.Close()

	if err := seedAdmin(ctx, client); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	// TODO: swap to a redis-backed store when cfg.RedisURL is set (HLD §5.2).
	sessions := session.NewMemoryStore()

	resolver := &graph.Resolver{
		Ent:           client,
		Sessions:      sessions,
		SessionTTL:    time.Duration(cfg.SessionTTL) * time.Second,
		SecureCookies: cfg.Env == "prod",
	}

	es := graph.NewExecutableSchema(graph.Config{
		Resolvers: resolver,
		Directives: graph.DirectiveRoot{
			HasRole:       graph.HasRole,
			HasPermission: graph.HasPermission,
		},
	})
	srv := handler.New(es)
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})

	mux := http.NewServeMux()
	mux.Handle("/query", auth.SessionMiddleware(sessions)(srv))
	mux.Handle("/", playground.Handler("Agent Platform", "/query"))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("agent-platform-backend listening on %s (env=%s)", cfg.HTTPAddr, cfg.Env)
	if err := http.ListenAndServe(cfg.HTTPAddr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// seedAdmin creates an initial admin user on an empty database. The bootstrap
// password comes from ADMIN_BOOTSTRAP_PASSWORD; the admin must change it on
// first login (must_change_password=true).
func seedAdmin(ctx context.Context, client *ent.Client) error {
	n, err := client.User.Query().Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	pw := os.Getenv("ADMIN_BOOTSTRAP_PASSWORD")
	if pw == "" {
		pw = "ChangeMe123!" // dev default; prod must set ADMIN_BOOTSTRAP_PASSWORD
		log.Printf("WARNING: ADMIN_BOOTSTRAP_PASSWORD not set; using dev default")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	_, err = client.User.Create().
		SetUsername("admin").
		SetEmail("admin@platform.local").
		SetPasswordHash(hash).
		SetRole(user.RoleAdmin).
		SetMustChangePassword(true).
		Save(ctx)
	return err
}
