// Command backfill-vk-gateway is a one-shot, idempotent job that fills
// VirtualKey.gateway_connection_id for legacy rows minted before LLD-14 T1 (NULL),
// deriving each key's gateway from its department's current binding, else the
// platform default (LLD-14 §3.3/§3.6).
//
// Run it once after deploying T1–T3 to shrink the NULL fallback set; it is safe to
// re-run (only NULL, non-revoked rows are touched). It reads the same DATABASE_URL
// as the server and never runs schema migrations.
package main

import (
	"context"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/backfill"
	"github.com/VMware-AI/agent-platform-backend/internal/config"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx := context.Background()

	// autoMigrate=false: the gateway_connection_id column already exists (T1
	// migration). A data-backfill job must never alter the schema.
	client, _, err := store.OpenWithPool(ctx, cfg.DatabaseURL, false, store.PoolConfig{
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.DBConnMaxLifetimeMinutes) * time.Minute,
	})
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer client.Close()

	res, err := backfill.VKGateway(ctx, client)
	log.Printf("backfill vk gateway_connection_id: scanned=%d filled=%d skipped_no_gateway=%d failed=%d",
		res.Scanned, res.Filled, res.SkippedNoGateway, res.Failed)
	if err != nil {
		log.Fatalf("backfill: %v", err)
	}
}
