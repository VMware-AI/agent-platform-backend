#!/usr/bin/env python3
"""Regenerate postman/agent-platform-backend.postman_collection.json from
internal/graph/testdata/client_operations/*.graphql.

Reads each fixture, parses (kind, name, variables, body), groups operations
into folders by domain prefix, and emits a Postman v2.1 collection. Preserves
collection-level auth + variables + Login request's auto-capture script.

Usage (from the repo root):
    python3 tools/postmangen/main.py

The output file is postman/agent-platform-backend.postman_collection.json.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FIXTURE_DIR = ROOT / "internal" / "graph" / "testdata" / "client_operations"
COLLECTION_PATH = ROOT / "postman" / "agent-platform-backend.postman_collection.json"

# Operation-name → folder mapping. Order in FOLDERS = order in collection.
# Operations whose name matches no prefix fall into a "Other" group at the end.
FOLDERS = [
    ("Auth", ("Login", "Me", "ChangePassword", "Logout")),
    ("Dashboard", ("DashboardOverview",)),
    ("Users & Roles", ("Users", "UserExists", "UsersByRole", "CreateUser", "UpdateUser", "DeleteUser", "ResetUserPassword", "ToggleUserEnabled", "AssignUsersToRole", "Roles", "Role", "RoleUsersMin")),
    ("RBAC (Custom Roles & Permissions)", ("CustomRolesList", "PermissionCatalog", "CreateCustomRoleEntry", "DeleteCustomRoleEntry", "SetCustomRolePermissions", "UpsertPermissionEntry", "UserRoles", "AssignUserRole", "RemoveUserRole")),
    ("Departments & Memberships", ("Departments", "CreateDepartment", "DeleteDepartment", "DepartmentMembers", "AddMembership", "RemoveMembership")),
    ("Resource Pools", ("ResourcePools", "ResourcePool", "CreateResourcePool", "UpdateResourcePool", "DeleteResourcePool", "SyncResourcePool", "TestResourcePoolConnection", "VsphereResourcePools", "VsphereNetworks")),
    ("Model Gateways (LITELLM)", ("ModelGateways", "ModelGatewayById", "GatewayConnectionsSyncSummary", "GatewayHealth", "CreateModelGateway", "UpdateModelGateway", "DeleteModelGateway", "SyncModelGatewayConnection", "TestNewModelGatewayConnection")),
    ("Provider Models", ("ProviderModelInfo", "CreateProviderModel", "UpdateProviderModel", "DeleteProviderModel", "AddProviderModelSpec", "UpdateProviderModelSpec", "DeleteProviderModelSpec", "BlockProviderModelSpec", "TestProviderConnection", "RefreshProviderModelStatus")),
    ("Model Routes", ("ModelRoutes", "CreateModelRoute", "UpdateModelRoute", "SetModelRouteEnabled", "DeleteModelRoute", "SyncRouterSettings")),
    ("Virtual Keys", ("VirtualKeys", "IssueVirtualKey", "RevokeVirtualKey", "RegenerateVirtualKey", "SetVirtualKeyEnabled", "AssociateVirtualKeyAgent", "GatewayAvailableModels")),
    ("Agents (Deployed)", ("Agents", "Agent", "SetAgentStatus", "RecycleAgent", "SetAgentLifecycleStatus", "RecycleAgentLifecycle", "CreateAgentSnapshot", "AgentSnapshotsForLifecycle", "RevertAgentSnapshotState", "AgentConfigs", "SetAgentConfigKnowledge", "AgentTemplatesList", "UpsertAgentTemplateEntry", "CreateAgent", "CreateAgentConfig", "UpdateAgentConfig", "DeleteAgentConfig", "SetDefaultAgentConfig", "RequestRotation", "RevokeAgentEnrollment")),
    ("Deploy (Marketplace OVA)", ("OvaTemplateFamilies", "OvaTemplateVersions", "CreateOvaTemplateFamily", "AddOvaTemplateVersion", "DeployAgent", "VmTemplates", "InstantCloneParents")),
    ("Content (Artifacts / Skills / Harbor)", ("PlatformArtifacts", "KnowledgeArtifacts", "PlatformArtifactVersions", "UpsertPlatformArtifact", "DeletePlatformArtifact", "ContentLibraries", "ContentLibraryItems", "ConsoleSkills", "ConsoleUpsertSkill", "ConsoleDeleteSkill", "HarborImages", "UpsertHarborImage", "DeleteHarborImage")),
    ("Budgets & Spend", ("Budgets", "SpendReport")),
    ("Observability (Logs / Metrics / Audit)", ("RequestLogs", "RequestMetrics", "AuditLogs", "MeteringOverview", "MeteringSummary", "TokenUsage", "RecordTokenUsage", "RecordRequestLog")),
]

OP_RE = re.compile(r"(query|mutation)\s+(\w+)(?:\s*\(([^)]*)\))?\s*\{")
FRAGMENT_RE = re.compile(r"fragment\s+(\w+)\s+on\s+\w+\s*\{", re.MULTILINE)
SPREAD_RE = re.compile(r"\.\.\.(\w+)\b")

# Variables the Login request must prefill to make the captured token useful.
LOGIN_VARIABLES = {
    "input": {
        "email": "admin@platform.local",
        "password": "ChangeMe123!",
        "remember": True,
    },
}

# Operation-name → sample variables for the body preview. Kept intentionally
# minimal: every variable just shows the type-required stub so a developer can
# paste real values. For Login only we prefill the dev admin (script below
# captures the token, so this stays runnable out-of-the-box).
def variables_for(op_name: str, var_block: str) -> dict:
    if op_name == "Login":
        return LOGIN_VARIABLES
    # Parse "name: Type, ..." — emit a minimal stub per variable.
    if not var_block or not var_block.strip():
        return {}
    out = {}
    for part in var_block.split(","):
        part = part.strip()
        if not part:
            continue
        # Strip a default value (`name: Type = default`) — Postman only needs
        # a stub for the typed variable; the default would be ignored anyway.
        if "=" in part:
            part = part.split("=", 1)[0].strip()
        name_type = part.split(":", 1)
        if len(name_type) != 2:
            continue
        name = name_type[0].strip()
        if name.startswith("$"):
            name = name[1:]
        if not name:
            continue
        out[name] = _stub_for(name_type[1].strip())
    return out


def _stub_for(ty: str):
    """Best-effort JS-style placeholder for a GraphQL variable type."""
    ty = ty.strip()
    if ty.startswith("["):
        return []
    if ty.endswith("!"):
        base = ty[:-1]
    else:
        base = ty
    if base == "String" or base == "ID":
        return ""
    if base in ("Int", "Float"):
        return 0
    if base == "Boolean":
        return False
    if "Input" in base or base.startswith("Create") or base.startswith("Update"):
        return {}
    return None


def parse_fixture(path: Path):
    text = path.read_text()
    # Extract any `fragment Name on T { ... }` blocks first so they can be
    # inlined into the operation body below. The fixtures keep fragments
    # outside the operation (a frontend-snapshot convention) but Postman
    # sends the query verbatim, so a fragment defined after the closing
    # `}` is out of scope and `...Name` spreads resolve to nothing.
    fragments = _extract_fragments(text)
    body_without_fragments = _strip_fragments(text)
    m = OP_RE.search(body_without_fragments)
    if not m:
        return None
    kind, name, var_block = m.group(1), m.group(2), (m.group(3) or "").strip()
    op_body = _inline_spreads(body_without_fragments, fragments)
    return kind, name, var_block, op_body


def _extract_fragments(text: str) -> dict:
    """Return {FragmentName: inner body string} for every `fragment X on T { ... }`
    block in the text. Handles top-level balanced braces only — sufficient for
    these snapshots (no fragment spreads inside fragments). The inner body is
    NOT stripped, so its leading whitespace (which `_indent_fragment` needs to
    align subsequent lines) is preserved verbatim."""
    out = {}
    for m in FRAGMENT_RE.finditer(text):
        name = m.group(1)
        start = m.end()  # position right after the opening `{`
        depth = 1
        i = start
        while i < len(text) and depth:
            c = text[i]
            if c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
            i += 1
        if depth != 0:
            continue  # malformed; ignore rather than crash
        inner = text[start : i - 1]
        out[name] = inner
    return out


def _strip_fragments(text: str) -> str:
    """Remove top-level `fragment X on T { ... }` blocks from the text, leaving
    the operation document otherwise untouched. The resulting string still
    parses as a single operation if at least one op is present."""
    out = []
    i = 0
    n = len(text)
    last = 0
    while i < n:
        m = FRAGMENT_RE.search(text, i)
        if not m:
            out.append(text[last:])
            break
        out.append(text[last : m.start()])
        # Skip the matched fragment and its balanced body.
        depth = 1
        j = m.end()
        while j < n and depth:
            c = text[j]
            if c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
            j += 1
        last = j
        i = j
    return "".join(out).strip()


def _inline_spreads(text, fragments, _seen=None):
    """Replace each `...FragmentName` in the op body with that fragment's inner
    field list, preserving the original indentation. Recurses so a fragment
    whose body itself contains a `...Other` spread also gets inlined (the
    current fixtures don't do this, but it's cheap to support)."""
    if _seen is None:
        _seen = set()

    def replace(match: re.Match) -> str:
        name = match.group(1)
        if name not in fragments:
            return match.group(0)  # leave it; the server will error and the dev will notice
        if name in _seen:
            return match.group(0)  # cycle guard
        body = _indent_fragment(fragments[name], match.start(), text)
        # Recurse into the inlined body to resolve nested spreads.
        body = _inline_spreads(body, fragments, _seen | {name})
        return body

    return SPREAD_RE.sub(replace, text)


def _indent_fragment(fragment_body: str, spread_start: int, source: str) -> str:
    """Indent a fragment's field list to match the column where the `...Name`
    spread began, so the inlined text aligns with the surrounding selection
    set.

    The fragment body is the text between the fragment's `{` and matching
    `}` (no header, no closing brace). Inside the fragment, the first
    non-empty line sits at some column (the fragment's "base indent") and
    later lines keep their indentation relative to that base.

    The returned text REPLACES the `...Name` spread in the source. The
    source line already has leading whitespace of its own (e.g. 6 spaces
    before `...VirtualKeyFields`), so the first inlined line should start
    at the SPREAD's column with no extra leading indent — the surrounding
    whitespace does that work. Subsequent lines then need to sit at the same
    column the first line lands on, so multi-line fields stay aligned with
    each other. We achieve that by prefixing each subsequent line with the
    original spread leading-whitespace, then adding the fragment's own
    relative indent (subsequent indent minus first-line indent)."""
    line_start = source.rfind("\n", 0, spread_start) + 1
    leading_ws = source[line_start:spread_start]  # the spread's leading whitespace
    raw_lines = fragment_body.splitlines()
    # Drop blank lines at the edges; the remaining lines all carry their own
    # relative indent within the fragment.
    while raw_lines and not raw_lines[0].strip():
        raw_lines.pop(0)
    while raw_lines and not raw_lines[-1].strip():
        raw_lines.pop()
    if not raw_lines:
        return ""
    # First line: emit the content with no leading indent. The source's
    # `leading_ws` (already in place before the spread) provides the column.
    out = [raw_lines[0].lstrip()]
    # Subsequent lines: shift each by the difference between its current
    # indent and the first line's, so a multi-level fragment keeps its shape
    # relative to the first field, then prefix with the spread's leading_ws
    # so the line lands at the same column the first line did.
    first_indent_len = len(raw_lines[0]) - len(raw_lines[0].lstrip())
    for ln in raw_lines[1:]:
        if not ln.strip():
            out.append("")
            continue
        cur_indent = len(ln) - len(ln.lstrip())
        rel = max(cur_indent - first_indent_len, 0)
        out.append(leading_ws + (" " * rel) + ln.lstrip())
    return "\n".join(out)


def make_request(name: str, kind: str, var_block: str, body: str) -> dict:
    variables = variables_for(name, var_block)
    raw_body = {
        "query": body.strip(),
        "variables": variables,
    }
    req = {
        "name": name,
        "request": {
            "method": "POST",
            "header": [
                {"key": "Content-Type", "value": "application/json"},
                {"key": "Origin", "value": "{{origin}}"},
            ],
            "body": {
                "mode": "raw",
                "raw": json.dumps(raw_body, indent=2),
            },
            "url": {
                "raw": "{{baseUrl}}",
                "host": ["{{baseUrl}}"],
                "path": [],
            },
            "description": _description_for(name, kind),
        },
        "response": [],
    }
    if name == "Login":
        req["event"] = [{
            "listen": "test",
            "script": {
                "type": "text/javascript",
                "exec": [
                    "const json = pm.response.json();",
                    "if (json.data && json.data.login && json.data.login.token) {",
                    "    pm.collectionVariables.set('session_token', json.data.login.token);",
                    "    pm.environment.set('session_token', json.data.login.token);",
                    "    console.log('Captured session token. mustChangePassword=' + json.data.login.mustChangePassword);",
                    "}",
                ],
            },
        }]
        req["auth"] = {"type": "noauth"}
    return req


def _description_for(name: str, kind: str) -> str:
    return f"{kind.capitalize()} {name} — generated from internal/graph/testdata/client_operations/{name}.graphql. Replace the variable stubs with real IDs before sending."


def build_collection(ops_by_name: dict) -> dict:
    items = []
    assigned = set()
    for folder_name, op_names in FOLDERS:
        folder_items = []
        for op_name in op_names:
            if op_name not in ops_by_name:
                continue
            kind, var_block, body = ops_by_name[op_name]
            folder_items.append(make_request(op_name, kind, var_block, body))
            assigned.add(op_name)
        if folder_items:
            items.append({"name": folder_name, "item": folder_items})
    # Anything unmapped → "Other" folder at the end so no op is silently dropped.
    leftovers = sorted(n for n in ops_by_name if n not in assigned)
    if leftovers:
        folder_items = []
        for op_name in leftovers:
            kind, var_block, body = ops_by_name[op_name]
            folder_items.append(make_request(op_name, kind, var_block, body))
        items.append({"name": "Other (unmapped)", "item": folder_items})
    return {
        "info": {
            "_postman_id": "agent-platform-backend-v1",
            "name": "Agent Platform Backend API",
            "description": "GraphQL API collection for the VMware AI Agent Platform backend.\n\n"
                           "Endpoint: `POST {{baseUrl}}` (defaults to `http://localhost:8080/query`).\n\n"
                           "Auth: bearer via `Authorization: Bearer {{session_token}}`. Run **Auth → Login** first; "
                           "its post-response script captures `login.token` into the `session_token` collection variable.\n\n"
                           "Every POST also sends `Origin: {{origin}}` — the CSRF middleware rejects requests without "
                           "an Origin/Referer header that matches an allowlisted origin.\n\n"
                           "Source of truth: `schema/*.graphql` (operations in `internal/graph/testdata/client_operations/*.graphql`).",
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        "auth": {
            "type": "bearer",
            "bearer": [{"key": "token", "type": "string", "value": "{{session_token}}"}],
        },
        "variable": [
            {"key": "baseUrl", "type": "string", "value": "http://localhost:8080/query"},
            {"key": "session_token", "type": "string", "value": ""},
            {"key": "origin", "type": "string", "value": "http://localhost:8080"},
        ],
        "item": items,
    }


def main():
    if not FIXTURE_DIR.is_dir():
        sys.exit(f"fixture dir not found: {FIXTURE_DIR}")
    ops_by_name = {}
    for path in sorted(FIXTURE_DIR.glob("*.graphql")):
        parsed = parse_fixture(path)
        if not parsed:
            print(f"skip: cannot parse {path.name}", file=sys.stderr)
            continue
        kind, name, var_block, body = parsed
        ops_by_name[name] = (kind, var_block, body)
    print(f"parsed {len(ops_by_name)} operations from fixtures")
    collection = build_collection(ops_by_name)
    COLLECTION_PATH.write_text(json.dumps(collection, indent=2) + "\n")
    n_reqs = sum(1 for it in collection["item"] for sub in [it["item"]] for _ in sub)
    n_folders = len(collection["item"])
    print(f"wrote {COLLECTION_PATH.relative_to(ROOT)}: {n_reqs} requests across {n_folders} folders")


if __name__ == "__main__":
    main()