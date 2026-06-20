// Command server runs the Agent Platform GraphQL control-plane backend.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/redis/go-redis/v9"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/config"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph"
	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/VMware-AI/agent-platform-backend/internal/ratelimit"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx := context.Background()

	client, err := store.Open(ctx, cfg.DatabaseURL, cfg.DBAutoMigrate)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer client.Close()

	if err := seedAdmin(ctx, client); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	var sessions session.Store
	ttl := time.Duration(cfg.SessionTTL) * time.Second
	if cfg.RedisURL != "" {
		opt, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			log.Fatalf("redis url: %v", err)
		}
		sessions = session.NewRedisStore(redis.NewClient(opt), ttl)
		log.Printf("session store: redis")
	} else {
		sessions = session.NewMemoryStore()
		log.Printf("session store: in-memory (dev)")
	}

	var gw gateway.Client
	var gwModels gateway.ModelManager
	if base := os.Getenv("LITELLM_BASE_URL"); base != "" {
		hc := gateway.NewHTTPClient(base, os.Getenv("LITELLM_MASTER_KEY"))
		gw, gwModels = hc, hc
		log.Printf("model gateway: %s", base)
	} else {
		log.Printf("model gateway: not configured (set LITELLM_BASE_URL)")
	}

	var sec secrets.Resolver
	if vw := os.Getenv("VAULTWARDEN_URL"); vw != "" {
		sec = secrets.NewVaultwardenResolver(vw)
		log.Printf("secrets: vaultwarden (%s)", vw)
	} else {
		sec = secrets.EnvResolver{}
		log.Printf("secrets: env:// resolver (dev)")
	}
	vcConnect := func(ctx context.Context, endpoint, user, pass string, insecure bool) (graph.VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}

	resolver := &graph.Resolver{
		Ent:             client,
		Sessions:        sessions,
		SessionTTL:      ttl,
		SecureCookies:   cfg.Env == "prod",
		Gateway:         gw,
		GatewayModels:   gwModels,
		Secrets:         sec,
		GatewayURL:      os.Getenv("GATEWAY_PUBLIC_URL"),
		VCenterConnect:  vcConnect,
		VCenterInsecure: cfg.VCenterInsecure,
		LoginLimiter:    ratelimit.NewMemory(10, 15*time.Minute),
	}
	resolver.EnablePermissionCache(60 * time.Second)

	es := graph.NewExecutableSchema(graph.Config{
		Resolvers: resolver,
		Directives: graph.DirectiveRoot{
			HasRole:       graph.HasRole,
			HasPermission: resolver.HasPermission,
		},
	})
	srv := handler.New(es)
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})
	srv.Use(extension.FixedComplexityLimit(200)) // guard against deep/expensive queries

	mux := http.NewServeMux()
	mux.Handle("/query", httpx.CSRF(cfg.AllowedOrigins)(auth.SessionMiddleware(sessions)(srv)))
	mux.Handle("/", playground.Handler("Agent Platform", "/query"))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpx.RequestLogger(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	shutCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Printf("agent-platform-backend listening on %s (env=%s)", cfg.HTTPAddr, cfg.Env)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-shutCtx.Done()
	log.Printf("shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
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
