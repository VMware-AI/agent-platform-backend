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
├── docker-compose.yml          # litellm + litellm postgres/redis + prometheus + grafana
├── config.yaml                 # litellm config (store_model_in_db, prom callbacks)
├── init-db.sh                  # creates the `litellm_db` database on first boot
├── prometheus.yml              # scrape config (litellm:4000 /metrics)
├── grafana/
│   ├── dashboards/
│   │   └── litellm_dashboard.json   # offline'd LiteLLM observability dashboard
│   └── provisioning/
│       ├── datasources/prometheus.yml   # auto-wires Prometheus datasource
│       └── dashboards/litellm.yml      # auto-loads the dashboard above
├── start_litellm_and_db.sh     # one-command launcher for the data plane + state
├── start_backend_local.sh      # one-command launcher for the Go control plane (working tree)
└── start_backend_docker.sh     # one-command launcher for the Go control plane (container image)
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
- Grafana    → `http://localhost:3000` (auto-provisioned Prometheus datasource + LiteLLM dashboard under the "LiteLLM" folder; dev credentials `admin/admin`)

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
DATABASE_URL=postgres://agentplatform_user:agentplatform_passwd@127.0.0.1:5433/agentplatform?sslmode=disable \
REDIS_URL=redis://127.0.0.1:6379/0 \
ALLOWED_ORIGINS=http://localhost:5173 \
ADMIN_BOOTSTRAP_PASSWORD=AdminLocal123! \
SECRETS_ENCRYPTION_KEY=$(openssl rand -hex 32) \
make run
# 模型网关在 console「模型网关接入」页添加：http://localhost:4000 + deploy/.env 里的 master key
```

### 2a. Via the container image

For a "what's on `latest`?" loop without rebuilding locally:

```bash
cd deploy
HOST_IP=host.docker.internal ./start_backend_docker.sh      # up (default)
./start_backend_docker.sh status                            # is the container running?
./start_backend_docker.sh down                              # stop, keep on disk
./start_backend_docker.sh clean                             # stop + remove (next up re-pulls)
HOST_IP=192.168.1.42 ./start_backend_docker.sh              # up with custom host IP

ADMIN_BOOTSTRAP_PASSWORD='MyStrong!Pass1' \
  HOST_IP=host.docker.internal ./start_backend_docker.sh    # skip the first-login nag
HTTP_ADDR=:9090 HOST_IP=192.168.1.42 ./start_backend_docker.sh
                                                            # custom host port + LAN IP
```

Actions (matches `start_litellm_and_db.sh`):

| action | what it does | needs `HOST_IP`? |
|---|---|---|
| `up` *(default)* | pull `latest` and run the container in the foreground | yes |
| `down` | stop the container, keep it on disk (so the next `up` skips the pull) | no |
| `clean` | stop + remove the container (next `up` re-pulls + re-creates) | no |
| `status` | print the container's current state | no |
| `--help` | show usage + the `HOST_IP` explanation | no |

**`HOST_IP` is required for `up`** — there is no safe default. Inside the
container, `127.0.0.1` / `localhost` resolve to the container itself, not to
the host running postgres/redis. The script exits with a reminder if `HOST_IP`
is unset or any loopback value. Pick one of:

- `host.docker.internal` — works out of the box on Docker Desktop and Linux
  Docker ≥ 20.10.
- `<your host LAN IP>` (e.g. `192.168.1.42`) — for older Linux Docker.

`DATABASE_URL`, `REDIS_URL`, and `ALLOWED_ORIGINS` are then derived from
`HOST_IP` automatically (the latter includes `http://${HOST_IP}:5173` so a
console reached via the host IP is allowed by CORS).

**Same env contract as `start_backend_local.sh`** for the variables the
backend actually consumes: `APP_ENV`, `HTTP_ADDR`, `DATABASE_URL`,
`REDIS_URL`, `ALLOWED_ORIGINS`, `DB_AUTO_MIGRATE`, `SESSION_TTL_SECONDS`,
`ADMIN_BOOTSTRAP_PASSWORD`. The only extras at the top of the script are
docker-launcher-specific (`HOST_IP`, `PG_*` / `REDIS_PORT` so you can change
host-side creds without rewriting the whole URL, and `IMAGE_REPO` / `IMAGE_TAG`
/ `CONTAINER_NAME` for pinned versions or air-gapped mirrors). No litellm /
gateway env is read or required by this script.

What it does:
- Re-uses the Postgres/Redis brought up by `start_litellm_and_db.sh`,
  reached via `${HOST_IP}`.
- Pulls `quay.io/vmware-ai/agent-platform-backend:latest` every invocation
  (`docker run --pull=always`) and removes the container on exit (`--rm`).
- Honors `HTTP_ADDR` by mapping the host port to the image's `:8080`.
- Fails fast on a missing/wrong-config prerequisite (HOST_IP unset / loopback,
  docker daemon, postgres/redis not running, host port already taken).

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
- **Grafana** (`http://localhost:3000`) is auto-provisioned with the Prometheus
  datasource and the offline'd LiteLLM dashboard
  ([grafana/dashboards/litellm_dashboard.json](grafana/dashboards/litellm_dashboard.json);
  source `BerriAI/litellm/cookbook/litellm_proxy_server/grafana_dashboard/dashboard_v2/grafana_dashboard.json`).
  Open it after `docker compose up -d` — Prometheus picks up traffic as soon as a
  virtual key issues a completion, so the panels fill in within seconds.
  The dashboard JSON is loaded read-only from the repo, but Grafana's UI can
  still save edits to its own copy (the file-based provider re-scans every 30s
  and re-imports). To re-pin to the upstream dashboard, refresh the JSON file
  from the URL above.
- Anonymous viewer login is enabled for dev convenience (no password prompt);
  disable it via `GF_AUTH_ANONYMOUS_ENABLED=false` in any non-local deployment,
  and override `GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD` in `.env`.

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