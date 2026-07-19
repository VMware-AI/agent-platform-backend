#!/usr/bin/env bash
# One-command launcher for the agent-platform-backend **container image** against
# the docker-compose postgres + redis stack already up in this directory.
#
# Pulls the image fresh on every invocation (latest tag), so the script doubles
# as "upgrade to current main" — no separate upgrade step.
#
# ─── You MUST set HOST_IP — there is no safe default ─────────────────────
#   HOST_IP: the address the backend container uses to reach the postgres /
#     redis services on this host. Inside the container, 127.0.0.1 / localhost
#     resolve to the container's own loopback — they will not reach your
#     host's postgres/redis. The script exits with a reminder if HOST_IP is
#     unset or any loopback value.
#
#     Pick one of:
#       - <your host LAN IP>     (e.g. 192.168.1.42, for older Linux Docker)
#
#     DATABASE_URL / REDIS_URL / ALLOWED_ORIGINS are then derived from it.
#     (HOST_IP is only required for `up`; `down`/`clean`/`status` don't need it.)
# ───────────────────────────────────────────────────────────────────────────
#
#   HOST_IP=192.168.1.42 ./start_backend_docker.sh           # up (default)
#   ./start_backend_docker.sh up     # bring the container up
#   ./start_backend_docker.sh down   # stop the container, keep it on disk
#   ./start_backend_docker.sh clean  # stop + remove the container
#   ./start_backend_docker.sh status # show current container state
#   ./start_backend_docker.sh --help # show this help
#
#   ADMIN_BOOTSTRAP_PASSWORD='MyStrong!Pass1' \
#     HOST_IP=192.168.1.42 ./start_backend_docker.sh         # skip the first-login nag
#   HTTP_ADDR=:9090 HOST_IP=192.168.1.42 ./start_backend_docker.sh   # custom port + LAN IP
#
# After it boots:
#   admin user:    admin (password = $ADMIN_BOOTSTRAP_PASSWORD, or dev default if blank)
#   GraphQL:       http://localhost:<HTTP_PORT>/query
#   Playground:    http://localhost:<HTTP_PORT>/
#   (HTTP_PORT is derived from HTTP_ADDR; defaults to 8080.)
#   secrets:       encrypted db-backed store; key from
#                  deploy/.secrets_encryption_key (auto-generated first run;
#                  BACK IT UP — losing the key strands every encrypted credential)
set -euo pipefail

# ╔═══════════════════════════════════════════════════════════════════════╗
# ║ USER-FACING SETTINGS                                                   ║
# ║                                                                       ║
# ║ Same env contract as start_backend_local.sh:                          ║
# ║   APP_ENV, HTTP_ADDR, DATABASE_URL, REDIS_URL, ALLOWED_ORIGINS,        ║
# ║   DB_AUTO_MIGRATE, SESSION_TTL_SECONDS, ADMIN_BOOTSTRAP_PASSWORD.     ║
# ║ Plus the secrets key (required in any environment):                   ║
# ║   SECRETS_ENCRYPTION_KEY — single key, simple                          ║
# ║   SECRETS_ENCRYPTION_KEYS — multi-key form (rotation; takes           ║
# ║     precedence over SECRETS_ENCRYPTION_KEY when set)                   ║
# ║ The defaults below mirror start_backend_local.sh, except addresses    ║
# ║ that resolve to the host (DATABASE_URL/REDIS_URL/ALLOWED_ORIGINS) use  ║
# ║ HOST_IP instead of 127.0.0.1, because the backend runs in a container.║
# ║                                                                       ║
# ║ Pass-through backend env vars (no default here; if set in your shell  ║
# ║ they are forwarded via `-e` to the container, otherwise the binary's   ║
# ║ own defaults in internal/config/config.go apply):                     ║
# ║   Reconciler / spend / probe:                                        ║
# ║     LITELLM_RECONCILE_INTERVAL_SECONDS,                                ║
# ║     PROVIDER_PROBE_INTERVAL_SECONDS,                                  ║
# ║     OBS_SPEND_CACHE_TTL_SECONDS, PERM_CACHE_TTL_SECONDS                ║
# ║   Resource pool sync:                                                  ║
# ║     POOL_SYNC_INTERVAL_SECONDS, POOL_SYNC_TIMEOUT_SECONDS,             ║
# ║     POOL_SYNC_MAX_RETRIES, POOL_SYNC_BREAKER_THRESHOLD,                ║
# ║     POOL_SYNC_BREAKER_OPEN_SECONDS                                    ║
# ║   Postgres pool tuning:                                                ║
# ║     DB_MAX_OPEN_CONNS, DB_MAX_IDLE_CONNS,                              ║
# ║     DB_CONN_MAX_LIFETIME_MINUTES                                       ║
# ║   Agent / scope / misc:                                                ║
# ║     AGENT_PKG_BASE_URL, AGENT_KEEP_VERSIONS, AGENT_USER,               ║
# ║     ENV_SCOPE_ENABLED, CONTROL_PLANE_URL                               ║
# ║   Secrets (advanced):                                                 ║
# ║     SECRETS_ROTATION_INTERVAL_SECONDS, SECRETS_AUDIT_ENABLED           ║
# ║                                                                       ║
# ║ Docker-only knobs (no local-script equivalent):                       ║
# ║   HOST_IP       — required: the host address reachable from the       ║
# ║                   container (see banner above).                       ║
# ║   PG_* / REDIS_PORT — split out so you can change host-side creds    ║
# ║                   without rewriting the whole URL.                    ║
# ║   IMAGE_REPO/TAG, CONTAINER_NAME — for air-gapped mirrors / pinned    ║
# ║                   versions / side-by-side runs.                       ║
# ╚═══════════════════════════════════════════════════════════════════════╝

# Address the backend container uses to reach services on the host. REQUIRED
# for `up`; ignored by `down`/`clean`/`status`. Inside the container,
# "127.0.0.1" / "localhost" resolve to the container's own loopback, not this
# host. Set to your host's LAN IP. The check + reminder live in the preflight
# block below.
HOST_IP="${HOST_IP-}"

# Where the backend listens (host side). The container always listens on 8080
# (Dockerfile EXPOSE); this is mapped to your chosen host port.
HTTP_ADDR="${HTTP_ADDR:-:8080}"

# Postgres credentials — must match deploy/docker-compose.yml.
PG_USER="${PG_USER:-agentplatform_user}"
PG_PASSWORD="${PG_PASSWORD:-agentplatform_passwd}"
PG_PORT="${PG_PORT:-5433}"
PG_DB="${PG_DB:-agentplatform}"

# Redis port — must match deploy/docker-compose.yml.
REDIS_PORT="${REDIS_PORT:-6379}"

# Image + container name.
IMAGE_REPO="${IMAGE_REPO:-quay.io/vmware-ai/agent-platform-backend}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
CONTAINER_NAME="${CONTAINER_NAME:-agent-platform-backend}"

# Admin bootstrap password (empty = dev default + forced first-login change).
ADMIN_BOOTSTRAP_PASSWORD="${ADMIN_BOOTSTRAP_PASSWORD:-}"

# ╔═══════════════════════════════════════════════════════════════════════╗
# ║ DERIVED SETTINGS — built from the user-facing block above.            ║
# ║ Override via the matching env var if you need to break out of the     ║
# ║ derived form (e.g. a remote DB).                                       ║
# ╚═══════════════════════════════════════════════════════════════════════╝

usage() {
  # Print the comment header as the help text — single source of truth.
  sed -n '2,38p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' | tr -d '\r'
}

ACTION="${1:-up}"
case "${ACTION}" in
  -h|--help|help)
    usage
    exit 0
    ;;
  up|down|clean|status)
    shift || true   # `up` takes no further args today; reserved for future
    ;;
  *)
    echo "usage: $0 [up|down|clean|status]" >&2
    echo "       (default action is 'up'; '--help' for full options)" >&2
    exit 2
    ;;
esac

# Resolve paths so the script works no matter where it's invoked from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ---------- preflight: docker ----------
# Needed by every action (up/down/clean/status all shell out to docker).
if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker not found in PATH" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon is not reachable (is the docker desktop / service running?)" >&2
  exit 1
fi

# Helper: print whether the named container exists (any state).
container_exists() {
  docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER_NAME}"
}

# ---------- status action ----------
# Show the current container state and where it would publish. Doesn't touch
# anything. Safe to run with HOST_IP unset (we just don't print the URL block).
if [[ "${ACTION}" == "status" ]]; then
  if container_exists; then
    echo "container '${CONTAINER_NAME}':"
    docker ps -a --filter "name=^${CONTAINER_NAME}\$" --format \
      "  {{.Status}}  image={{.Image}}  ports={{.Ports}}"
  else
    echo "container '${CONTAINER_NAME}' is not present."
  fi
  exit 0
fi

# ---------- down / clean ----------
# Both are idempotent: noop with a friendly message if the container is already
# gone. Neither needs HOST_IP (we're not starting anything).
if [[ "${ACTION}" == "down" ]]; then
  if container_exists; then
    echo "stopping '${CONTAINER_NAME}'…"
    docker stop "${CONTAINER_NAME}" >/dev/null
    echo "container stopped (kept on disk; run '$0 up' to start it again)."
  else
    echo "nothing to stop: container '${CONTAINER_NAME}' is not present."
  fi
  exit 0
fi

if [[ "${ACTION}" == "clean" ]]; then
  if container_exists; then
    echo "removing '${CONTAINER_NAME}'…"
    docker rm -f "${CONTAINER_NAME}" >/dev/null
    echo "container removed (next '$0 up' will re-pull the image and create a fresh one)."
  else
    echo "nothing to remove: container '${CONTAINER_NAME}' is not present."
  fi
  exit 0
fi

# ---------- up (the rest of the script) ----------
# Everything below is the `up` path.

IMAGE="${IMAGE_REPO}:${IMAGE_TAG}"

# ---------- secrets encryption key (required since feat/secrets-encrypted-pg) ----------
# Credentials are stored in the platform_secrets table, encrypted at rest under
# a single key (SECRETS_ENCRYPTION_KEY, SHA-256-derived to AES-256-GCM). The
# stored ciphertext has no key-version prefix, so a rotating/changing key strands
# every existing row. The container is recreated on every `up` (--pull=always +
# --rm), so the key MUST live on the host and be injected via -e — generating
# inside the container would produce a different key per run and lock out all
# previously-stored credentials. First run: auto-generate. Explicit env wins.
KEY_FILE="${SCRIPT_DIR}/.secrets_encryption_key"
if [[ -z "${SECRETS_ENCRYPTION_KEY:-}" ]]; then
  if [[ -f "${KEY_FILE}" ]]; then
    export SECRETS_ENCRYPTION_KEY="$(cat "${KEY_FILE}")"
  else
    if ! command -v openssl >/dev/null 2>&1; then
      echo "error: SECRETS_ENCRYPTION_KEY not set and 'openssl' not found to generate one" >&2
      echo "         set SECRETS_ENCRYPTION_KEY=<high-entropy string> and re-run" >&2
      exit 1
    fi
    umask 077
    openssl rand -hex 32 > "${KEY_FILE}"
    chmod 600 "${KEY_FILE}"
    export SECRETS_ENCRYPTION_KEY="$(cat "${KEY_FILE}")"
    echo "generated new SECRETS_ENCRYPTION_KEY at ${KEY_FILE}"
    echo "  (BACK THIS UP — losing the key strands every encrypted credential in platform_secrets)"
  fi
fi

# Postgres + redis are reached via HOST_IP.
# 5433 avoids clashing with a native/homebrew pg on 5432.
export DATABASE_URL="${DATABASE_URL:-postgres://${PG_USER}:${PG_PASSWORD}@${HOST_IP}:${PG_PORT}/${PG_DB}?sslmode=disable}"
export REDIS_URL="${REDIS_URL:-redis://${HOST_IP}:${REDIS_PORT}/0}"

# Dev runtime defaults (override via env if needed).
export APP_ENV="${APP_ENV:-dev}"
export ALLOWED_ORIGINS="${ALLOWED_ORIGINS:-http://localhost:5173,http://${HOST_IP}:5173}"
export DB_AUTO_MIGRATE="${DB_AUTO_MIGRATE:-true}"
export SESSION_TTL_SECONDS="${SESSION_TTL_SECONDS:-28800}"
export ADMIN_BOOTSTRAP_PASSWORD="${ADMIN_BOOTSTRAP_PASSWORD:-}"

# ---------- HTTP_ADDR → host port ----------
# Accepts ":8080", "0.0.0.0:8080", "127.0.0.1:8080", or "8080" (shorthand).
# The container always listens on 8080 (Dockerfile EXPOSE), so we always map
# host_port → 8080.
resolve_port() {
  local addr="$1"
  # Strip leading/trailing whitespace, then peel off an optional host part.
  addr="${addr// /}"
  local port
  if [[ "${addr}" == *:* ]]; then
    port="${addr##*:}"
  else
    port="${addr}"
  fi
  if ! [[ "${port}" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
    echo "error: HTTP_ADDR=${addr} is not a valid host:port" >&2
    exit 1
  fi
  echo "${port}"
}
HTTP_PORT="$(resolve_port "${HTTP_ADDR}")"

# ---------- preflight: HOST_IP ----------
# Required: the backend runs inside a container, so 127.0.0.1/localhost would
# resolve to the container's own loopback and miss the host's postgres/redis.
# Reject unset, empty, or any loopback value.
if [[ -z "${HOST_IP}" ]]; then
  echo "error: HOST_IP is not set." >&2
  echo "         HOST_IP is the address the backend container uses to reach" >&2
  echo "         postgres/redis on this host. It must NOT be 127.0.0.1 or" >&2
  echo "         localhost (those resolve to the container itself)." >&2
  echo "" >&2
  echo "         Pick one of:" >&2
  echo "           - <your host LAN IP>    (e.g. 192.168.1.42)" >&2
  echo "" >&2
  echo "         Then re-run, e.g.:" >&2
  echo "           HOST_IP=192.168.1.42 $0" >&2
  exit 1
fi
case "${HOST_IP}" in
  localhost|127.0.0.1|127.0.0.1/*|::1|0.0.0.0|0:0:0:0:0:0:0:1)
    echo "error: HOST_IP='${HOST_IP}' resolves to a loopback / unspecified address," >&2
    echo "         which inside the container points back to the container itself," >&2
    echo "         not this host. Use your host LAN IP." >&2
    exit 1
    ;;
esac

# ---------- preflight: compose stack (postgres + redis) ----------
if [[ ! -f "${SCRIPT_DIR}/docker-compose.yml" ]]; then
  echo "error: ${SCRIPT_DIR}/docker-compose.yml not found; this script must be run from deploy/" >&2
  exit 1
fi

postgres_running() {
  (cd "${SCRIPT_DIR}" && docker compose ps --status running --services 2>/dev/null) | grep -qx 'postgres'
}
redis_running() {
  (cd "${SCRIPT_DIR}" && docker compose ps --status running --services 2>/dev/null) | grep -qx 'redis'
}

if ! postgres_running; then
  echo "error: 'postgres' service is not running. Start it with:" >&2
  echo "         (cd ${SCRIPT_DIR} && ./start_litellm_and_db.sh)" >&2
  exit 1
fi
if ! redis_running; then
  echo "error: 'redis' service is not running. Start it with:" >&2
  echo "         (cd ${SCRIPT_DIR} && ./start_litellm_and_db.sh)" >&2
  exit 1
fi

# Wait for postgres to actually accept connections (matches start_backend_local.sh).
echo -n "waiting for postgres to accept connections "
for _ in $(seq 1 30); do
  if (cd "${SCRIPT_DIR}" && docker compose exec -T postgres pg_isready -U agentplatform_user >/dev/null 2>&1); then
    echo " ✓"
    break
  fi
  echo -n "."
  sleep 1
done

# ---------- preflight: host port is free ----------
# `lsof` is shipped on macOS; on Linux fall back to `ss` then `netstat`.
if command -v lsof >/dev/null 2>&1; then
  if lsof -nP -iTCP:"${HTTP_PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "error: host port ${HTTP_PORT} is already in use (HTTP_ADDR=${HTTP_ADDR})." >&2
    lsof -nP -iTCP:"${HTTP_PORT}" -sTCP:LISTEN >&2 || true
    exit 1
  fi
elif command -v ss >/dev/null 2>&1; then
  if ss -ltn "sport = :${HTTP_PORT}" 2>/dev/null | tail -n +2 | grep -q .; then
    echo "error: host port ${HTTP_PORT} is already in use (HTTP_ADDR=${HTTP_ADDR})." >&2
    exit 1
  fi
else
  if netstat -ltn 2>/dev/null | awk '{print $4}' | grep -E "[.:]${HTTP_PORT}\$" >/dev/null; then
    echo "error: host port ${HTTP_PORT} is already in use (HTTP_ADDR=${HTTP_ADDR})." >&2
    exit 1
  fi
fi

# ---------- preflight: remove a stale container with our name ----------
# Idempotent: a previous run that was killed (Ctrl-C without --rm catching the
# signal) can leave a stopped container behind; --rm in docker run only fires
# on clean exit.
if container_exists; then
  echo "removing stale container '${CONTAINER_NAME}' from a previous run"
  docker rm -f "${CONTAINER_NAME}" >/dev/null
fi

# ---------- run ----------
echo
echo "────────────────────────────────────────────────────────────"
echo " agent-platform-backend (docker)"
echo "   image:        ${IMAGE}"
echo "   HOST_IP=${HOST_IP}     HTTP_ADDR=${HTTP_ADDR}  → host :${HTTP_PORT}"
echo "   DATABASE_URL=${DATABASE_URL}"
echo "   REDIS_URL=${REDIS_URL}"
echo "   ALLOWED_ORIGINS=${ALLOWED_ORIGINS}"
echo "   SECRETS_ENCRYPTION_KEY=<set>  (key file: ${KEY_FILE})"
if [[ -n "${SECRETS_ENCRYPTION_KEYS:-}" ]]; then
  echo "   SECRETS_ENCRYPTION_KEYS=<set>  (multi-key rotation; overrides single-key)"
fi
echo "   (other backend env vars from your shell are forwarded; see header)"
echo "────────────────────────────────────────────────────────────"
echo
echo "(pulling ${IMAGE}; Ctrl-C to stop and remove the container)"
echo

# --rm         remove container on clean exit
# --pull=always always re-resolve the tag (latest → fresh build on every run)
# -p           honor HTTP_ADDR by mapping host_port → 8080 (image's EXPOSE)
exec docker run \
  --rm \
  -d \
  --pull=always \
  --name "${CONTAINER_NAME}" \
  -p "${HTTP_PORT}:8080" \
  -e APP_ENV \
  -e HTTP_ADDR \
  -e DATABASE_URL \
  -e REDIS_URL \
  -e ALLOWED_ORIGINS \
  -e DB_AUTO_MIGRATE \
  -e SESSION_TTL_SECONDS \
  -e ADMIN_BOOTSTRAP_PASSWORD \
  -e CONTROL_PLANE_URL \
  -e SECRETS_ENCRYPTION_KEY \
  -e SECRETS_ENCRYPTION_KEYS \
  -e SECRETS_ROTATION_INTERVAL_SECONDS \
  -e SECRETS_AUDIT_ENABLED \
  -e LITELLM_RECONCILE_INTERVAL_SECONDS \
  -e POOL_SYNC_INTERVAL_SECONDS \
  -e POOL_SYNC_TIMEOUT_SECONDS \
  -e POOL_SYNC_MAX_RETRIES \
  -e POOL_SYNC_BREAKER_THRESHOLD \
  -e POOL_SYNC_BREAKER_OPEN_SECONDS \
  -e PROVIDER_PROBE_INTERVAL_SECONDS \
  -e OBS_SPEND_CACHE_TTL_SECONDS \
  -e PERM_CACHE_TTL_SECONDS \
  -e DB_MAX_OPEN_CONNS \
  -e DB_MAX_IDLE_CONNS \
  -e DB_CONN_MAX_LIFETIME_MINUTES \
  -e AGENT_PKG_BASE_URL \
  -e AGENT_KEEP_VERSIONS \
  -e AGENT_USER \
  -e ENV_SCOPE_ENABLED \
  "${IMAGE}"