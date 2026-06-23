# Local LiteLLM gateway — end-to-end test with the control plane

LiteLLM is the **data plane** (agents send LLM requests to it; it applies the
virtual key + rate limits and routes to an upstream like minimax). Our Go backend
is the **control plane** — it pushes the model list and mints virtual keys via
litellm's admin API. This stack lets you test that integration locally **without a
VM** (just Docker).

## 1. Start litellm

**Quick start (recommended):**
```bash
cd deploy/litellm
./start.sh                          # auto-generates .env (master key) + brings it up
# or seed your minimax key at the same time:
MINIMAX_API_KEY=sk-... ./start.sh
# ./start.sh down   (stop, keep pg)   |   ./start.sh nuke   (stop + wipe pg)
```
It prints the master key and the exact `make run` line for the backend.

**Manual:**
```bash
cp .env.example .env        # set LITELLM_MASTER_KEY (sk-...), LITELLM_SALT_KEY, MINIMAX_API_KEY
docker compose up -d
curl -s http://localhost:4000/health/liveliness   # → "I'm alive!"
```

> Verified end-to-end (2026-06-24): backend `upsertUpstream`→`/model/new`,
> `issueVirtualKey`→`/key/generate`, then a virtual key → litellm → **a real
> minimax completion** (`openai/MiniMax-Text-01` @ `api.minimaxi.com/v1`).
litellm = `:4000`, its own postgres = `:5433` (won't clash with native pg on 5432).

## 2. Point the backend at it
```bash
LITELLM_BASE_URL=http://localhost:4000 \
LITELLM_MASTER_KEY=<same sk- as above> \
ALLOWED_ORIGINS=http://localhost:5173 \
ADMIN_BOOTSTRAP_PASSWORD=AdminLocal123! \
make run
```
Now the backend's gateway client is live: `upsertUpstream` → litellm `/model/new`,
`issueVirtualKey` → litellm `/key/generate`.

## 3. End-to-end (via the console UI or GraphQL)
1. **模型路由 → 加上游**: name `minimax-chat`, model `openai/abab6.5s-chat`,
   apiBase `https://api.minimaxi.com/v1`, apiKey `<your minimax key>`.
   → backend pushes it to litellm (`/model/new`). Adjust model/apiBase to your minimax account.
2. **虚拟密钥 → 发放**: bind a user/agent → backend calls litellm `/key/generate`,
   returns the `sk-...` secret **once**.
3. **Call the gateway with that key** (this is what an agent does):
   ```bash
   curl -s http://localhost:4000/v1/chat/completions \
     -H "Authorization: Bearer <virtual-key-sk-...>" \
     -H "Content-Type: application/json" \
     -d '{"model":"minimax-chat","messages":[{"role":"user","content":"你好"}]}'
   ```
   A real minimax completion = the full path (control plane → litellm → minimax) works.

## Notes
- minimax is OpenAI-compatible → `openai/<model>` prefix. Confirm your exact model
  id + base URL from your minimax console.
- ⚠️ For shared/prod use, pin the litellm image **digest** and exclude the poisoned
  1.82.7 / 1.82.8 releases (design `research/litellm-proxy-deployment-and-api.md` §1.4).
- Single local instance needs no redis (redis only syncs rate-limit state across replicas).
