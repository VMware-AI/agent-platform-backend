#!/usr/bin/env bash
# One-command launcher for the local LiteLLM gateway (data plane) + its postgres.
# The control-plane backend pushes models/keys via litellm's admin API, so this
# stays minimal. Idempotent: re-running reuses the existing .env + volumes.
#
#   ./start.sh                       # bring it up (generates .env on first run)
#   ./start.sh down                  # stop + remove containers (keeps the pg volume)
#   ./start.sh clean                  # stop + remove containers AND the pg volume
#
# After it's up, point the backend at it (the script prints the exact line):
#   LITELLM_BASE_URL=http://localhost:4000 LITELLM_MASTER_KEY=<printed> make run
set -euo pipefail
cd "$(dirname "$0")"

compose() { docker compose "$@"; }

case "${1:-up}" in
  down) compose down; echo "litellm stopped (pg volume kept)."; exit 0 ;;
  clean) compose down -v; echo "litellm stopped + pg volume removed."; exit 0 ;;
  up) ;;
  *) echo "usage: $0 [up|down|clean]"; exit 2 ;;
esac

# 1) Ensure .env exists with a master key (auto-generated) + salt. Never committed.
if [[ ! -f .env ]]; then
  echo "creating .env (master key auto-generated)…"
  {
    echo "LITELLM_MASTER_KEY=sk-local-$(openssl rand -hex 12)"
    echo "LITELLM_SALT_KEY=local-salt-$(openssl rand -hex 12)"
  } > .env
fi

# 2) Bring up the stack.
echo "starting litellm + postgres + redis + prometheus…"
compose up -d

# 3) Wait for litellm to report alive.
echo -n "waiting for litellm on :4000 "
for _ in $(seq 1 60); do
  if curl -fsS http://localhost:4000/health/liveliness >/dev/null 2>&1; then
    echo " ✓"
    break
  fi
  echo -n "."
  sleep 2
done

MASTER_KEY="$(grep '^LITELLM_MASTER_KEY=' .env | cut -d= -f2-)"
cat <<EOF

────────────────────────────────────────────────────────────
 LiteLLM gateway is up:  http://localhost:4000   (postgres :5433)
 Master key:             ${MASTER_KEY}

 Point the backend at it:
   LITELLM_ADMIN_URL=http://localhost:4000/ui
   LITELLM_BASE_URL=http://localhost:4000 \\
   LITELLM_MASTER_KEY=${MASTER_KEY} \\
   ALLOWED_ORIGINS=http://localhost:5173 \\
   ADMIN_BOOTSTRAP_PASSWORD=<your-≥12-char-pw> \\
   make run

 Then add a minimax upstream from the UI (or GraphQL):
   model=openai/MiniMax-Text-01  api_base=https://api.minimaxi.com/v1  apiKey=<your minimax key>
────────────────────────────────────────────────────────────
EOF
