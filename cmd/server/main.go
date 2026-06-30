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
	"github.com/VMware-AI/agent-platform-backend/internal/agentmgr"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/catalog"
	"github.com/VMware-AI/agent-platform-backend/internal/config"
	"github.com/VMware-AI/agent-platform-backend/internal/graph"
	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
	"github.com/VMware-AI/agent-platform-backend/internal/leader"
	"github.com/VMware-AI/agent-platform-backend/internal/ratelimit"
	"github.com/VMware-AI/agent-platform-backend/internal/reconcile"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// reconcileLeaseKey is the Postgres advisory-lock key that single-flights the
// gateway-key reconciler across replicas. Arbitrary but fixed and unique to this
// job (ASCII "rcncl-gw").
const reconcileLeaseKey int64 = 0x72636e636c2d6777

// HTTP server timeouts.
const (
	readHeaderTimeout   = 10 * time.Second
	readTimeout         = 30 * time.Second
	writeTimeout        = 60 * time.Second
	idleTimeout         = 120 * time.Second
	serverShutdownGrace = 15 * time.Second
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx := context.Background()

	client, db, err := store.OpenWithPool(ctx, cfg.DatabaseURL, cfg.DBAutoMigrate, store.PoolConfig{
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.DBConnMaxLifetimeMinutes) * time.Minute,
	})
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer client.Close()

	if err := seedAdmin(ctx, client); err != nil {
		log.Fatalf("seed admin: %v", err)
	}
	if err := catalog.Seed(ctx, client); err != nil {
		log.Fatalf("seed catalog: %v", err)
	}

	// Login brute-force throttle: 10 failures per 15 minutes per key.
	const loginThreshold = 10
	const loginWindow = 15 * time.Minute

	var sessions session.Store
	var loginLimiter ratelimit.Limiter
	ttl := time.Duration(cfg.SessionTTL) * time.Second
	if cfg.RedisURL != "" {
		opt, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			log.Fatalf("redis url: %v", err)
		}
		// One client shared by the session store and the limiter so both counters
		// are GLOBAL across replicas (a per-replica limiter would let a load
		// balancer multiply the brute-force threshold by the replica count).
		rdb := redis.NewClient(opt)
		sessions = session.NewRedisStore(rdb, ttl)
		loginLimiter = ratelimit.NewRedis(rdb, loginThreshold, loginWindow)
		log.Printf("session store: redis; login limiter: redis (shared across replicas)")
	} else {
		sessions = session.NewMemoryStore()
		loginLimiter = ratelimit.NewMemory(loginThreshold, loginWindow)
		log.Printf("session store: in-memory (dev); login limiter: in-memory")
	}

	// Model gateways are configured in the console (模型网关接入) and stored in the
	// DB; resolvers resolve the right gateway per department / the platform default
	// at request time (LLD-13 §3.3). No process-wide gateway env anymore.
	log.Printf("model gateway: resolved per-department from DB (console 模型网关接入)")

	// Single secrets backend for ALL environments (no dev/prod split): credentials
	// are stored in the platform_secrets table, encrypted at rest under one
	// symmetric key (SECRETS_ENCRYPTION_KEY). This persists across restarts and
	// gives one code path. The key is required — fail fast rather than start a
	// server that cannot read or write any credential.
	sec, err := secrets.NewDBStore(client, cfg.SecretsEncryptionKey)
	if err != nil {
		log.Fatalf("secrets: %v", err)
	}
	log.Printf("secrets: encrypted db-backed store (platform_secrets, AES-GCM)")
	vcConnect := func(ctx context.Context, endpoint, user, pass string, insecure bool) (graph.VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}

	// Static placeholder values for catalog install_command rendering. Only
	// AGENT_PKG_BASE_URL (and then only when set, so an unconfigured mirror leaves
	// the placeholder visible). AGENT_USER is NOT here — it is a DB platform
	// setting resolved per-request (LLD-13), not a startup env.
	installVars := map[string]string{}
	if cfg.AgentPkgBaseURL != "" {
		installVars["AGENT_PKG_BASE_URL"] = cfg.AgentPkgBaseURL
	}

	// agent-manager backend (LLD-08): VM enrollment + heartbeat + rotation. Its
	// secret store needs write access; the encrypted DBStore is a secrets.Store, so
	// rotation completions persist (encrypted) across restarts.
	agentMgr := &agentmgr.Service{Ent: client, Secrets: sec}

	resolver := &graph.Resolver{
		Ent:             client,
		Sessions:        sessions,
		SessionTTL:      ttl,
		SecureCookies:   cfg.Env == "prod",
		Secrets:         sec,
		InstallVars:     installVars,
		VCenterConnect:  vcConnect,
		LoginLimiter:    loginLimiter,
		AgentMgr:        agentMgr,
		ControlPlaneURL: os.Getenv("CONTROL_PLANE_URL"),
		EnvScopeEnabled: cfg.EnvScopeEnabled,
	}
	// In-process @hasPermission cache (process-local; see permcache.go). Disabled by
	// default — enable it (single-replica only) via PERM_CACHE_TTL_SECONDS>0, so a
	// multi-replica deployment never serves stale permissions after a revocation.
	if cfg.PermCacheTTLSeconds > 0 {
		resolver.EnablePermissionCache(time.Duration(cfg.PermCacheTTLSeconds) * time.Second)
	}

	// Periodically reconcile gateway keys against governance rows (detect/heal
	// ungoverned orphans + stale rows). Disabled unless an interval is set AND a
	// gateway is configured. Report-only unless RECONCILE_PRUNE=true.
	reconcileCtx, stopReconcile := context.WithCancel(context.Background())
	defer stopReconcile()
	if cfg.ReconcileInterval > 0 {
		rec := &reconcile.Reconciler{
			Ent: client,
			// Reconcile the platform default gateway, resolved from DB each cycle
			// (LLD-13 §3.3); cycles are skipped until a default gateway exists.
			GatewayFunc: func(ctx context.Context) reconcile.Gateway { return resolver.ReconcileGateway(ctx) },
			Prune:       cfg.ReconcilePrune,
		}
		// Single-flight across replicas via a Postgres advisory lock so the prune
		// runs on exactly one replica (postgres only — the dev sqlite path has a
		// single in-memory replica, so it's always leader).
		if cfg.DatabaseURL != "" {
			lease := leader.NewPGLease(db, reconcileLeaseKey)
			defer lease.Release(context.Background())
			rec.IsLeader = lease.IsLeader
		}
		interval := time.Duration(cfg.ReconcileInterval) * time.Second
		log.Printf("gateway-key reconciler: every %s (prune=%v), default gateway", interval, cfg.ReconcilePrune)
		go rec.Run(reconcileCtx, interval)
	}

	// Periodically re-sync resource pools that have stored credentials.
	// Disabled unless POOL_SYNC_INTERVAL_SECONDS > 0.
	if cfg.PoolSyncIntervalSeconds > 0 {
		poolSyncCtx, stopPoolSync := context.WithCancel(context.Background())
		defer stopPoolSync()
		go resolver.StartAutoSync(poolSyncCtx, time.Duration(cfg.PoolSyncIntervalSeconds)*time.Second)
	}

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
	// Per-response batch loaders (dataloader pattern): coalesce the Agent
	// owner/apiKey/credentials field lookups into one IN(...) query each,
	// eliminating the agents-list N+1.
	resolver.InstallLoaders(srv)
	srv.Use(extension.Introspection{})
	srv.Use(extension.FixedComplexityLimit(200)) // guard against deep/expensive queries
	// LLD-01 §6: block all mutations except changePassword/logout while the
	// caller still has must_change_password set (centralized, fail-closed).
	srv.AroundFields(graph.RequirePasswordChange())
	// Mask internal errors/panics behind a generic message + logged correlation id
	// so resolver/infra detail never reaches the client.
	srv.SetErrorPresenter(graph.ErrorPresenter)
	srv.SetRecoverFunc(graph.RecoverFunc)

	mux := http.NewServeMux()
	// CORS (outermost) so the split-origin dev console (:5173 → :8080) can call with
	// cookies; preflight is handled before CSRF/session. Allowlist = ALLOWED_ORIGINS.
	mux.Handle("/query", httpx.CORS(cfg.AllowedOrigins)(httpx.CSRF(cfg.AllowedOrigins)(auth.SessionMiddleware(sessions)(httpx.Environment(srv)))))
	// Daemon-facing REST (LLD-08): bearer-authenticated, mounted OUTSIDE the CSRF +
	// session middleware (machine client, no cookies/Origin). Still inside the
	// RequestLogger wrap below.
	mux.Handle("/v1/agents/", agentmgr.Handler(agentMgr))
	mux.Handle("/", playground.Handler("Agent Platform", "/query"))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpx.RequestLogger(mux),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

// seedAdmin creates the initial super-admin (platform-global `admin` role) on an
// empty database; all other users are created later via the UI. The bootstrap
// password comes from ADMIN_BOOTSTRAP_PASSWORD.
//
// Forced first-login change applies ONLY to the insecure dev default: an operator
// who set ADMIN_BOOTSTRAP_PASSWORD explicitly has already chosen a credential, so
// the admin is usable immediately (must_change_password=false).
func seedAdmin(ctx context.Context, client *ent.Client) error {
	n, err := client.User.Query().Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	pw := os.Getenv("ADMIN_BOOTSTRAP_PASSWORD")
	mustChange := false
	if pw == "" {
		pw = "ChangeMe123!" // dev default; prod must set ADMIN_BOOTSTRAP_PASSWORD
		mustChange = true   // insecure default → force a change on first login
		log.Printf("WARNING: ADMIN_BOOTSTRAP_PASSWORD not set; using dev default (forced change on first login)")
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
		SetMustChangePassword(mustChange).
		Save(ctx)
	return err
}
