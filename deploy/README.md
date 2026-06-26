# Local dev stack — LiteLLM gateway + Go control plane

This directory is a **single `docker compose up` away** from a working end-to-end
LLM gateway stack on your laptop:

- **Data plane** — LiteLLM on `:4000` (agents send LLM requests here; applies the
  virtual key + rate limits and routes upstream).
- **Control plane** — the Go backend in the repo root (`make run`), which pushes
  the model list and mints virtual keys via LiteLLM's admin API.
- **State** — a Postgres for the backend + LiteLLM (`:5433`, isolated from any
  native/homebrew PG on `:5432`) and a Redis for LiteLLM's rate-limit sync
  (`:6379`).
- **Observability** — Prometheus on `:9090` scraping LiteLLM's `/metrics`.

## Files

```
deploy/
├── docker-compose.yml          # litellm + litellm postgres/redis + prometheus
├── config.yaml                 # litellm config (store_model_in_db, prom callbacks)
├── init-db.sh                  # creates the `litellm_db` database on first boot
├── prometheus.yml              # scrape config (litellm:4000 /metrics)
├── start_litellm_and_db.sh     # one-command launcher for the data plane + state
└── start_backend_local.sh      # one-command launcher for the Go control plane
```

## 1. Bring up the data plane + state

```bash
cd deploy
./start_litellm_and_db.sh             # up (default)
./start_litellm_and_db.sh down        # stop containers, keep pg/redis volumes
./start_litellm_and_db.sh clean       # stop + wipe volumes
```

On first run, the script auto-generates `deploy/.env` with a fresh
`LITELLM_MASTER_KEY=sk-...` and `LITELLM_SALT_KEY`. It then waits for LiteLLM
to answer `/health/liveliness`, prints the master key, and prints the exact
`make run` line for step 2.

Ports:
- LiteLLM  → `http://localhost:4000`
- Postgres → `127.0.0.1:5433`  (user `agentplatform_user`, db `agentplatform` + `litellm_db`)
- Redis    → `127.0.0.1:6379`
- Prometheus → `http://localhost:9090`

> Verified end-to-end (2026-06-24): backend `upsertUpstream`→`/model/new`,
> `issueVirtualKey`→`/key/generate`, then a virtual key → litellm → **a real
> minimax completion** (`openai/MiniMax-Text-01` @ `api.minimaxi.com/v1`).

## 2. Bring up the control plane (the Go backend)

```bash
cd deploy
./start_backend_local.sh              # uses sane dev defaults (see below)
# or pin an admin password to skip the first-login change:
ADMIN_BOOTSTRAP_PASSWORD='MyStrong!Pass1' ./start_backend_local.sh
```

What the script does:
- Re-uses the Postgres/Redis brought up in step 1 (`DATABASE_URL` → `:5433`,
  `REDIS_URL` → `:6379`).
- Forces a first-login password change if `ADMIN_BOOTSTRAP_PASSWORD` is empty
  (dev default `ChangeMe123!`).
- Auto-migrates the schema (`DB_AUTO_MIGRATE=true`, dev only).
- Calls `make run` from the repo root.

Endpoints after boot:
- GraphQL  → `http://localhost:8080/query`
- Playground → `http://localhost:8080/`
- Admin user → `admin` (password = `$ADMIN_BOOTSTRAP_PASSWORD` or the dev default)

If you'd rather run it yourself with the same env (or override one):
```bash
LITELLM_BASE_URL=http://localhost:4000 \
LITELLM_MASTER_KEY=$(grep '^LITELLM_MASTER_KEY=' deploy/.env | cut -d= -f2-) \
DATABASE_URL=postgres://agentplatform_user:agentplatform_passwd@127.0.0.1:5433/agentplatform?sslmode=disable \
REDIS_URL=redis://127.0.0.1:6379/0 \
ALLOWED_ORIGINS=http://localhost:5173 \
ADMIN_BOOTSTRAP_PASSWORD=AdminLocal123! \
make run
```

## 3. End-to-end smoke (via the console UI or GraphQL)

1. **模型路由 → 加上游**: name `minimax-chat`, model `openai/MiniMax-Text-01`,
   apiBase `https://api.minimaxi.com/v1`, apiKey `<your minimax key>`.
   → backend pushes it to LiteLLM (`/model/new`). Adjust model/apiBase to your
   minimax account.
2. **虚拟密钥 → 发放**: bind a user/agent → backend calls LiteLLM
   `/key/generate`, returns the `sk-...` secret **once**.
3. **Call the gateway with that key** (this is what an agent does):
   ```bash
   curl -s http://localhost:4000/v1/chat/completions \
     -H "Authorization: Bearer <virtual-key-sk-...>" \
     -H "Content-Type: application/json" \
     -d '{"model":"minimax-chat","messages":[{"role":"user","content":"你好"}]}'
   ```
   A real minimax completion = the full path (control plane → litellm → minimax)
   works.

## 4. Observability

- **LiteLLM Prometheus metrics** are exposed on `:4000/metrics` (success/failure
  callbacks wired in `config.yaml`).
- The bundled Prometheus scrapes it every 15s — open
  `http://localhost:9090/targets?search=litellm` to confirm.

## Notes

- minimax is OpenAI-compatible → use the `openai/<model>` prefix with
  `api_base=https://api.minimaxi.com/v1`. Confirm your exact model id + base URL
  from your minimax console.
- ⚠️ For shared/prod use, pin the litellm image **digest** and exclude the
  poisoned 1.82.7 / 1.82.8 releases (see `research/litellm-proxy-deployment-and-api.md`
  §1.4).
- Redis is part of the stack even for a single local instance — LiteLLM uses it
  for rate-limit state and routing coordination. Drop it only if you've also
  disabled those features in `config.yaml`.