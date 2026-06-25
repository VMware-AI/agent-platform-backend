#!/usr/bin/env bash
# One-command launcher for the local agent-platform-backend (control plane) once
# the docker-compose postgres + redis are already up.
#
# You only need to provide your admin bootstrap password (or accept the dev
# default + forced first-login change).
#
#   ./start_backend_local.sh                         # sane defaults
#   ADMIN_BOOTSTRAP_PASSWORD='MyStrong!Pass1' \
#     ./start_backend_local.sh                      # explicit admin pw, no first-login nag
#   ./start_backend_local.sh --help                 # show this help
#
# After it boots:
#   admin user:    admin (password = $ADMIN_BOOTSTRAP_PASSWORD, or dev default if blank)
#   GraphQL:       http://localhost:8080/query
#   Playground:    http://localhost:8080/
set -euo pipefail

usage() {
  # Print the comment header as the help text — single source of truth.
  sed -n '2,16p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' | tr -d '\r'
}

case "${1:-}" in
  -h|--help|help)
    usage
    exit 0
    ;;
esac

# Resolve paths so the script works no matter where it's invoked from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ---------- sane dev defaults (override via env) ----------
export APP_ENV="${APP_ENV:-dev}"
export HTTP_ADDR="${HTTP_ADDR:-:8080}"

# Postgres + redis are published on the host by deploy/docker-compose.yml.
# 5433 avoids clashing with a native/homebrew pg on 5432.
export DATABASE_URL="${DATABASE_URL:-postgres://agentplatform_user:agentplatform_passwd@127.0.0.1:5433/agentplatform?sslmode=disable}"
export REDIS_URL="${REDIS_URL:-redis://127.0.0.1:6379/0}"

# CORS: same-origin is always allowed; comma-separated extras go here. The
# Vite/Next dev console is the canonical extra in dev.
export ALLOWED_ORIGINS="${ALLOWED_ORIGINS:-http://localhost:5173}"

# Dev auto-migrates the schema; prod does not. APP_ENV=dev already implies this,
# but be explicit so an operator running prod locally understands the tradeoff.
export DB_AUTO_MIGRATE="${DB_AUTO_MIGRATE:-true}"

# Session TTL (8h). Integer, must be > 0 (config.Load validates).
export SESSION_TTL_SECONDS="${SESSION_TTL_SECONDS:-28800}"

# ---------- admin bootstrap password ----------
# Empty value = use the dev default "ChangeMe123!" + force change on first login.
# Set this to a strong password to skip the forced change.
if [[ -z "${ADMIN_BOOTSTRAP_PASSWORD:-}" ]]; then
  echo "WARNING: ADMIN_BOOTSTRAP_PASSWORD not set; using dev default 'ChangeMe123!'"
  echo "         (first login will force a password change)"
fi

# ---------- preflight: docker compose stack is up ----------
if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker not found in PATH" >&2
  exit 1
fi

if ! (cd "${SCRIPT_DIR}" && docker compose ps --status running --services 2>/dev/null) | grep -qx 'postgres'; then
  echo "warning: 'postgres' service does not appear to be running."
  echo "         start it with:  cd deploy && docker compose up -d postgres redis"
fi

if (cd "${SCRIPT_DIR}" && docker compose ps --status running --services 2>/dev/null) | grep -qx 'postgres'; then
  echo -n "waiting for postgres to accept connections "
  for _ in $(seq 1 30); do
    if (cd "${SCRIPT_DIR}" && docker compose exec -T postgres pg_isready -U agentplatform_user >/dev/null 2>&1); then
      echo " ✓"
      break
    fi
    echo -n "."
    sleep 1
  done
fi

# ---------- run ----------
echo
echo "────────────────────────────────────────────────────────────"
echo " agent-platform-backend"
echo "   APP_ENV=${APP_ENV}    HTTP_ADDR=${HTTP_ADDR}"
echo "   DATABASE_URL=${DATABASE_URL}"
echo "   REDIS_URL=${REDIS_URL}"
echo "   ALLOWED_ORIGINS=${ALLOWED_ORIGINS}"
echo "────────────────────────────────────────────────────────────"
echo

cd "${REPO_ROOT}"
exec make run