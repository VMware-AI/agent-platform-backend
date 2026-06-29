# Agent Platform Backend — Postman Collection

Postman v2.1 collection for the GraphQL API at `agent-platform-backend`.

## Files

- `agent-platform-backend.postman_collection.json` — 17 grouped folders, 114 requests covering every GraphQL query/mutation in the schema.

## Quick start

1. Import the collection into Postman (`File → Import`).
2. Make sure the backend is running locally (`make dev` or the docker launcher). Default endpoint: `http://localhost:8080/query`.
3. Open **Auth → Login** and send it. The collection-level auth + the `session_token` collection variable are auto-populated by the request's `Tests` script (look for `pm.collectionVariables.set('session_token', ...)`).
4. All other requests in the collection automatically carry the bearer token via the collection-level `Authorization: Bearer {{session_token}}`.

> If your session token expires or the server restarts, just re-run **Auth → Login** to re-capture it.

## Why an `Origin` header?

The backend's CSRF middleware (`internal/httpx/csrf.go`) requires **either** an `Origin` **or** a `Referer` header on non-safe requests (POST/PUT/DELETE). A raw Postman request without one returns `403 Forbidden`.

Every request in this collection includes `Origin: http://localhost:8080` (configurable via the `{{origin}}` variable). The CORS allowlist must permit that origin; default config permits `http://localhost:5173` and `http://localhost:8080`.

If you need to point at a different host, update both:
- The `{{origin}}` collection variable
- The `{{baseUrl}}` collection variable

…and ensure the host is in `ALLOWED_ORIGINS` for the server.

## Authentication

- **Cookie (`ap_session`)** — set automatically by the browser when posting from a same-origin page. The login mutation issues it as `Path=/`, `HttpOnly`, `SameSite=Lax`, `Secure` (in prod).
- **Bearer token (`Authorization: Bearer <token>`)** — returned from the `login` mutation as `AuthPayload.token`. **Preferred for Postman** — easier to manage than cookies, and the auth middleware reads it before falling back to the cookie.

Either one works; the collection uses the Bearer path.

## Collection structure

| Group | Count | Notes |
|---|---:|---|
| Auth | 4 | Login (auto-captures token), Me, ChangePassword, Logout |
| Dashboard | 1 | DashboardOverview (admin / observability) |
| Users & Roles | 11 | Users list, CreateUser (AUTO/CUSTOM), ResetPassword, Toggle, … |
| RBAC | 9 | Custom roles, permission catalog, role-permission matrix |
| Departments & Memberships | 6 | Add/RemoveMembership delegated to dept-admins |
| Platform Settings | 2 | `agentUser` (LLD-13) |
| Resource Pools | 8 | vCenter pools, sync, pre-save probe, vsphere placement |
| Model Gateways (LITELLM) | 7 | Page + sync summary + test connection (id-based + dry-run pre-create) |
| Gateway Connections (raw) | 4 | Lower-level connection ops |
| Model Routes | 6 | Create/Update/Delete + legacy upsertModelRoute |
| Upstreams & Router Tiers | 5 | Provider/model routing + difficulty router |
| Virtual Keys | 5 | Issue/Revoke/Regenerate/SetEnabled — `secret` returned ONCE |
| Rate Limit Policies | 4 | rpm/tpm, enabled, delete (refused while keys reference it) |
| Agents (Deployed) | 17 | List, lifecycle, snapshots, agent configs, rotation, enrollment revoke |
| Deploy (Marketplace OVA) | 6 | OVA families + versions + DeployAgent + vmTemplates |
| Content (Artifacts / Skills / Harbor) | 11 | OKF knowledge packs, scripts, images |
| Observability (Logs / Metrics / Audit) | 8 | RequestLogs/Metrics, AuditLogs, Metering, ingest |

## Conventions

- Variables prefixed with `{{` are collection-level (e.g. `{{baseUrl}}`, `{{session_token}}`, `{{origin}}`).
- Body placeholders like `"REPLACE_WITH_USER_ID"` must be replaced with real IDs from a list/lookup response before sending.
- Mutations that **return secrets once** (e.g. `login.token`, `issueVirtualKey.secret`, `resetUserPassword.generatedPassword`, `deployAgent.virtualKeySecret`, `regenerateVirtualKey.secret`): copy them out of the response immediately — they cannot be re-fetched.

## Default admin (dev)

Username `admin@platform.local`, password `ChangeMe123!`. First login sets `mustChangePassword=true`, which blocks every mutation except `changePassword` and `logout`. To skip the forced change in dev, set the `ADMIN_BOOTSTRAP_PASSWORD` env var before first startup.

## Regenerating

This collection was generated from `internal/graph/testdata/client_operations/*.graphql` and the union of `schema/*.graphql`. Re-run the generator (or update by hand) when you add or rename an operation.

## Common pitfalls

| Symptom | Cause |
|---|---|
| `403 Forbidden` on every POST | Missing `Origin` header — add one matching an allowlisted origin. |
| `unauthenticated` | Bearer token missing/expired — re-run **Login**. |
| `password change required: …` | Logged-in user still has `mustChangePassword=true`. Call **ChangePassword** first. |
| `permission denied` / `forbidden role` | Logged-in user lacks the role/permission for that field. Switch users. |
| `directive @hasRole` / `@hasPermission` errors | You're sending a `mutation` from a query token (or vice versa) — they're scoped per type. |