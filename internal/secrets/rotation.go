package secrets

import (
	"context"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/platformsecret"
)

// RotationScanner is the read+write surface the worker needs from DBStore.
// The worker only ever calls ActiveKeyID and Reencrypt — DBStore satisfies
// this interface implicitly (no separate declaration needed).
type RotationScanner interface {
	ActiveKeyID() string
	Reencrypt(ctx context.Context, ref string) error
}

// RotationBatchSize caps how many rows the worker touches per tick. Each row
// is a separate Reencrypt (one round-trip for the SELECT + one for the
// UPDATE), so an unbounded batch would hold a DB connection for too long.
const RotationBatchSize = 100

// RunRotationWorker scans platform_secrets on a ticker and re-encrypts any
// row whose key_id is no longer the active key. Disabled when interval <= 0.
//
// How it stays correct under rotation:
//   - DBStore holds an AEAD per configured key id; rows sealed under any of
//     them keep decrypting until migrated.
//   - Reencrypt reads the row under the OLD AEAD, writes it back under the
//     ACTIVE AEAD, and stamps key_id = active.
//   - One bad row logs and continues — a single corrupt or missing-key row
//     never blocks the rest of the batch.
//   - A panic on the DB path is recovered so a transient db driver issue
//     (pgx reconnect during failover) doesn't kill the goroutine and leak
//     un-migrated rows.
func RunRotationWorker(ctx context.Context, store RotationScanner, client *ent.Client, interval time.Duration) {
	if interval <= 0 {
		log.Printf("secrets rotation worker: disabled (SECRETS_ROTATION_INTERVAL_SECONDS=0)")
		return
	}
	if store == nil || client == nil {
		log.Printf("secrets rotation worker: skipped (no store or db client)")
		return
	}
	active := store.ActiveKeyID()
	log.Printf("secrets rotation worker: every %s, active key %q", interval, active)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runRotationCycle(ctx, store, client, active)
		}
	}
}

func runRotationCycle(ctx context.Context, store RotationScanner, client *ent.Client, activeID string) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("secrets rotation: panic recovered, skipping cycle: %v", p)
		}
	}()

	// One-shot query: every off-key row (we re-load inside Reencrypt so a
	// concurrent Put doesn't race us). Scanning by full row keeps the
	// Reencrypt path self-contained — the Select(ids) path would require
	// a second SELECT inside Reencrypt to recover the row.
	rows, err := client.PlatformSecret.Query().
		Where(platformsecret.KeyIDNEQ(activeID)).
		Limit(RotationBatchSize).
		All(ctx)
	if err != nil {
		log.Printf("secrets rotation: query off-key rows: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	migrated, failed := 0, 0
	for _, row := range rows {
		if err := store.Reencrypt(ctx, row.Ref); err != nil {
			failed++
			log.Printf("secrets rotation: reencrypt %s: %v", row.Ref, err)
			continue
		}
		migrated++
	}
	log.Printf("secrets rotation: scanned batch=%d migrated=%d failed=%d active=%q",
		len(rows), migrated, failed, activeID)
}
