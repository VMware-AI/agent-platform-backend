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
        name_type = part.split(":", 1)
        if len(name_type) != 2:
            continue
        name, ty = name_type[0].strip(), name_type[1].strip()
        out[name] = _stub_for(ty)
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
    m = OP_RE.search(text)
    if not m:
        return None
    kind, name, var_block = m.group(1), m.group(2), (m.group(3) or "").strip()
    # Body is the whole text after the opening `{` until the matching `}`.
    # For multi-line fixtures we use the raw text minus the op header.
    body = text.strip()
    return kind, name, var_block, body


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