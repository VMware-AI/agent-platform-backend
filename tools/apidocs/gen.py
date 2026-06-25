#!/usr/bin/env python3
"""Generate GraphQL API reference markdown under docs/api/ from the per-module
schema/*.graphql files (the source of truth).

Accuracy-first: every type, field, argument and directive emitted here is parsed
directly out of the SDL. Descriptions come from the `#` comments in the schema
(leading block comments above a definition/field, or a trailing comment on the
same line). Nothing is invented.

Run from the repo root:  python3 tools/apidocs/gen.py
"""
from __future__ import annotations

import os
import re
import sys
from dataclasses import dataclass, field
from typing import Optional

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SCHEMA_DIR = os.path.join(ROOT, "schema")
OUT_DIR = os.path.join(ROOT, "docs", "api")

# ---------------------------------------------------------------------------
# Domain grouping: which schema module(s) feed which doc page.
# (slug, title, [module basenames])
# ---------------------------------------------------------------------------
DOMAINS = [
    ("agents", "Agents, Templates & Snapshots", ["agent", "deploy"]),
    ("agent-config", "Agent Config, Artifacts, Skills & Images", ["content"]),
    ("model-gateway", "Model Gateway & Routing", ["modelgateway", "gateway-routing"]),
    ("virtual-keys", "Virtual Keys & Rate Limits", ["virtualkey"]),
    ("observability", "Observability (Request Logs, Audit Logs, Metrics)", ["observability"]),
    ("metering", "Metering", ["metering"]),
    ("platform", "Platform (Users, Roles, Permissions, Departments)",
     ["account", "rbac", "department"]),
    ("resource-pools", "Resource Pools & vSphere", ["resourcepool"]),
    ("ova-marketplace", "OVA Marketplace", ["ova"]),
    ("dashboard", "Dashboard", ["dashboard"]),
]

# Core scalars/directives + the auth surface live in schema.graphql; they get
# their own home (README + the cross-cutting "core" handling below).
CORE_MODULE = "schema"

# rate-limit policies + request metrics share observability.graphql, which also
# holds the rate-limit *policy* type used by virtual-keys. We surface
# RateLimitPolicy on the virtual-keys page too via cross-references (it stays
# defined in observability per the file).


# Optional per-page leading notes (cross-references where a related type lives in
# another module's file).
DOMAIN_NOTES = {
    "virtual-keys": (
        "> **Rate-limit policies** (the `RateLimitPolicy` type, the "
        "`rateLimitPolicies` query, and the `upsertRateLimitPolicy` / "
        "`setRateLimitPolicyEnabled` / `deleteRateLimitPolicy` mutations) are "
        "defined in `schema/observability.graphql` and documented on the "
        "[Observability page](./observability.md). A virtual key references a "
        "policy via `IssueVirtualKeyInput.rateLimitPolicyId`."
    ),
    "observability": (
        "> Rate-limit policies are co-located here (same source file) but are "
        "conceptually paired with [virtual keys](./virtual-keys.md), which bind "
        "to them via `rateLimitPolicyId`."
    ),
}


@dataclass
class Field:
    name: str
    type: str
    args: list = field(default_factory=list)  # list[Arg]
    desc: str = ""
    directive: str = ""
    default: Optional[str] = None  # input-field default value, if any


@dataclass
class Arg:
    name: str
    type: str
    default: Optional[str] = None
    desc: str = ""


@dataclass
class TypeDef:
    kind: str  # type | input | enum | scalar | interface | union
    name: str
    desc: str = ""
    fields: list = field(default_factory=list)       # list[Field] (objects/inputs)
    values: list = field(default_factory=list)       # list[(name, desc)] (enums)
    module: str = ""


@dataclass
class Operation:
    name: str
    args: list  # list[Arg]
    return_type: str
    directive: str
    desc: str
    module: str


# ---------------------------------------------------------------------------
# Parsing
# ---------------------------------------------------------------------------

DIRECTIVE_RE = re.compile(r"@hasPermission\(perm:\s*\"([^\"]+)\"\)|@hasRole\(any:\s*\[([^\]]*)\]\)")


def fmt_directive(text: str) -> str:
    """Extract a human-readable auth directive from a field definition line/block."""
    parts = []
    for m in re.finditer(r"@hasPermission\(perm:\s*\"([^\"]+)\"\)", text):
        parts.append(f'@hasPermission(perm: "{m.group(1)}")')
    for m in re.finditer(r"@hasRole\(any:\s*\[([^\]]*)\]\)", text):
        roles = ", ".join(r.strip() for r in m.group(1).split(","))
        parts.append(f"@hasRole(any: [{roles}])")
    return " ".join(parts)


def strip_gofield(text: str) -> str:
    return re.sub(r"@goField\([^)]*\)", "", text)


def clean_comment(lines: list[str]) -> str:
    out = []
    for ln in lines:
        ln = ln.strip()
        if ln.startswith("#"):
            ln = ln[1:].strip()
        out.append(ln)
    return " ".join(p for p in out).strip()


def parse_args(arg_str: str) -> list[Arg]:
    """Parse a GraphQL argument list string into Arg objects (no nested parens)."""
    arg_str = strip_gofield(arg_str).strip()
    if not arg_str:
        return []
    args = []
    # Split on top-level commas (args here never contain commas in their types).
    for raw in split_top_level(arg_str, ","):
        raw = raw.strip()
        if not raw:
            continue
        # name: Type = default
        m = re.match(r"([A-Za-z_]\w*)\s*:\s*(.+)", raw)
        if not m:
            continue
        name = m.group(1)
        rest = m.group(2).strip()
        default = None
        if "=" in rest:
            typ, default = rest.split("=", 1)
            typ = typ.strip()
            default = default.strip()
        else:
            typ = rest
        args.append(Arg(name=name, type=typ.strip(), default=default))
    return args


def split_top_level(s: str, sep: str) -> list[str]:
    out, depth, cur = [], 0, []
    for ch in s:
        if ch in "([{":
            depth += 1
        elif ch in ")]}":
            depth -= 1
        if ch == sep and depth == 0:
            out.append("".join(cur))
            cur = []
        else:
            cur.append(ch)
    out.append("".join(cur))
    return out


def parse_module(path: str, module: str):
    """Parse one schema module. Returns (types, query_ops, mutation_ops)."""
    with open(path, encoding="utf-8") as f:
        raw_lines = f.read().splitlines()

    types: list[TypeDef] = []
    query_ops: list[Operation] = []
    mutation_ops: list[Operation] = []

    i = 0
    pending_comment: list[str] = []
    n = len(raw_lines)

    def flush_comment():
        c = clean_comment(pending_comment)
        pending_comment.clear()
        return c

    while i < n:
        line = raw_lines[i]
        stripped = line.strip()

        if not stripped:
            pending_comment.clear()
            i += 1
            continue

        if stripped.startswith("#"):
            pending_comment.append(stripped)
            i += 1
            continue

        # Directive declarations — skip (documented in README).
        if stripped.startswith("directive "):
            # consume until closing line
            depth = stripped.count("(") - stripped.count(")")
            i += 1
            while depth > 0 and i < n:
                depth += raw_lines[i].count("(") - raw_lines[i].count(")")
                i += 1
            pending_comment.clear()
            continue

        # scalar
        m = re.match(r"scalar\s+(\w+)", stripped)
        if m:
            types.append(TypeDef(kind="scalar", name=m.group(1),
                                 desc=flush_comment(), module=module))
            i += 1
            continue

        # enum
        m = re.match(r"enum\s+(\w+)\s*\{(.*)$", stripped)
        if m:
            name = m.group(1)
            desc = flush_comment()
            td = TypeDef(kind="enum", name=name, desc=desc, module=module)
            inline_tail = m.group(2).strip()
            # enum may be one-liner: enum X { A B C }
            if "}" in inline_tail:
                body = inline_tail[: inline_tail.index("}")]
                for v in body.split():
                    td.values.append((v, ""))
                types.append(td)
                i += 1
                continue
            i += 1
            vcomment: list[str] = []
            while i < n and "}" not in raw_lines[i]:
                vs = raw_lines[i].strip()
                if vs.startswith("#"):
                    vcomment.append(vs)
                elif vs:
                    # value possibly with trailing comment
                    if "#" in vs:
                        val, tc = vs.split("#", 1)
                        vd = clean_comment(vcomment + ["# " + tc])
                    else:
                        val, vd = vs, clean_comment(vcomment)
                    td.values.append((val.strip(), vd))
                    vcomment = []
                i += 1
            types.append(td)
            i += 1
            continue

        # type / input  (also "extend type Query/Mutation")
        m = re.match(r"(?:extend\s+)?(type|input|interface)\s+(\w+)", stripped)
        if m:
            kind = m.group(1)
            name = m.group(2)
            desc = flush_comment()
            # Find opening brace (could be same line)
            # advance to body
            i += 1
            body_lines = []
            # if brace not on the def line, the next non-empty is body
            while i < n and raw_lines[i].strip() != "}":
                body_lines.append(raw_lines[i])
                i += 1
            i += 1  # consume }

            if name in ("Query", "Mutation"):
                ops = parse_operations(body_lines, module)
                if name == "Query":
                    query_ops.extend(ops)
                else:
                    mutation_ops.extend(ops)
            else:
                td = TypeDef(kind=kind, name=name, desc=desc, module=module)
                td.fields = parse_type_fields(body_lines)
                types.append(td)
            continue

        pending_comment.clear()
        i += 1

    return types, query_ops, mutation_ops


def gather_field_blocks(body_lines: list[str]):
    """Yield (comment_lines, field_text) for each field in a type/op body.

    A field may span multiple lines (e.g. a directive on the next line). We treat
    a field as starting at a non-comment line and continuing until brackets are
    balanced and the next line is a comment or another field at the same level.
    """
    blocks = []
    comment: list[str] = []
    i = 0
    n = len(body_lines)
    while i < n:
        s = body_lines[i].strip()
        if not s:
            comment = []
            i += 1
            continue
        if s.startswith("#"):
            comment.append(s)
            i += 1
            continue
        # Start of a field. Accumulate continuation lines while parens unbalanced
        # or while the line ends in a way that needs continuation.
        text = s
        depth = s.count("(") - s.count(")") + s.count("[") - s.count("]")
        # continuation directive on following line(s)
        j = i + 1
        while depth > 0 and j < n:
            cont = body_lines[j].strip()
            text += " " + cont
            depth += cont.count("(") - cont.count(")") + cont.count("[") - cont.count("]")
            j += 1
        # also pull a trailing directive that landed on the next line
        while j < n:
            nxt = body_lines[j].strip()
            if nxt.startswith("@"):
                text += " " + nxt
                j += 1
            else:
                break
        blocks.append((comment, text))
        comment = []
        i = j
    return blocks


def parse_type_fields(body_lines: list[str]) -> list[Field]:
    fields = []
    for comment, text in gather_field_blocks(body_lines):
        # split trailing same-line comment
        trailing = ""
        # Only split on # that is not inside quotes (schema has no # in quotes here)
        if "#" in text:
            text, trailing = text.split("#", 1)
        text = text.strip()
        text_nodir = strip_gofield(text).strip()
        # name(args): Type  OR  name: Type
        m = re.match(r"([A-Za-z_]\w*)\s*(\([^)]*\))?\s*:\s*(.+)", text_nodir)
        if not m:
            continue
        fname = m.group(1)
        args = parse_args(m.group(2)[1:-1]) if m.group(2) else []
        ftype = m.group(3).strip()
        # type may still carry directives; strip them off for the type column
        directive = fmt_directive(text)
        ftype = re.sub(r"@\w+\([^)]*\)", "", ftype).strip()
        # input fields may carry a default: `Int = 50` → type=Int, default=50
        default = None
        if "=" in ftype:
            ftype, default = ftype.split("=", 1)
            ftype, default = ftype.strip(), default.strip()
        desc = clean_comment(comment)
        if trailing.strip():
            desc = (desc + " " + trailing.strip()).strip() if desc else trailing.strip()
        fields.append(Field(name=fname, type=ftype, args=args, desc=desc,
                            directive=directive, default=default))
    return fields


def parse_operations(body_lines: list[str], module: str) -> list[Operation]:
    ops = []
    for comment, text in gather_field_blocks(body_lines):
        trailing = ""
        if "#" in text:
            text, trailing = text.split("#", 1)
        text = strip_gofield(text).strip()
        m = re.match(r"([A-Za-z_]\w*)\s*(\([^)]*\))?\s*:\s*(.+)", text)
        if not m:
            continue
        name = m.group(1)
        args = parse_args(m.group(2)[1:-1]) if m.group(2) else []
        ret = m.group(3).strip()
        directive = fmt_directive(ret)
        ret = re.sub(r"@\w+\([^)]*\)", "", ret).strip()
        desc = clean_comment(comment)
        if trailing.strip():
            desc = (desc + " " + trailing.strip()).strip() if desc else trailing.strip()
        ops.append(Operation(name=name, args=args, return_type=ret,
                             directive=directive, desc=desc, module=module))
    return ops


# ---------------------------------------------------------------------------
# Rendering helpers
# ---------------------------------------------------------------------------

def md_escape(s: str) -> str:
    return s.replace("|", "\\|")


def type_link(t: str) -> str:
    """Render a type ref as inline code (kept simple/accurate; no cross-page links
    to avoid broken anchors)."""
    return f"`{t}`"


def is_required(t: str) -> str:
    return "yes" if t.strip().endswith("!") else "no"


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    # Parse everything
    all_types: dict[str, TypeDef] = {}
    types_by_module: dict[str, list[TypeDef]] = {}
    queries_by_module: dict[str, list[Operation]] = {}
    mutations_by_module: dict[str, list[Operation]] = {}

    for fn in sorted(os.listdir(SCHEMA_DIR)):
        if not fn.endswith(".graphql"):
            continue
        module = fn[: -len(".graphql")]
        path = os.path.join(SCHEMA_DIR, fn)
        types, qops, mops = parse_module(path, module)
        types_by_module[module] = types
        queries_by_module[module] = qops
        mutations_by_module[module] = mops
        for t in types:
            all_types[t.name] = t

    os.makedirs(OUT_DIR, exist_ok=True)

    written = []
    nav = []
    for slug, title, modules in DOMAINS:
        nav.append((slug, title))

    # ---- Per-domain pages ----
    for slug, title, modules in DOMAINS:
        page = render_domain_page(
            slug, title, modules,
            types_by_module, queries_by_module, mutations_by_module,
        )
        out_path = os.path.join(OUT_DIR, f"{slug}.md")
        with open(out_path, "w", encoding="utf-8") as f:
            f.write(page)
        written.append(out_path)

    # ---- README ----
    core_types = types_by_module.get(CORE_MODULE, [])
    core_q = queries_by_module.get(CORE_MODULE, [])
    core_m = mutations_by_module.get(CORE_MODULE, [])
    readme = render_readme(nav, core_types, core_q, core_m)
    out_path = os.path.join(OUT_DIR, "README.md")
    with open(out_path, "w", encoding="utf-8") as f:
        f.write(readme)
    written.append(out_path)

    print("Wrote:")
    for w in written:
        print("  " + os.path.relpath(w, ROOT))


def render_op(op: Operation) -> str:
    out = []
    out.append(f"### `{op.name}`\n")
    if op.desc:
        out.append(op.desc + "\n")
    # signature
    arglist = ", ".join(f"{a.name}: {a.type}" + (f" = {a.default}" if a.default else "")
                        for a in op.args)
    sig = f"{op.name}({arglist}): {op.return_type}" if op.args else f"{op.name}: {op.return_type}"
    out.append("```graphql\n" + sig + "\n```\n")
    out.append(f"- **Returns:** {type_link(op.return_type)}")
    out.append(f"- **Auth:** {('`' + op.directive + '`') if op.directive else 'authenticated (no directive)'}")
    out.append("")
    if op.args:
        out.append("| Argument | Type | Required | Default |")
        out.append("|----------|------|----------|---------|")
        for a in op.args:
            out.append(f"| `{a.name}` | {type_link(a.type)} | {is_required(a.type)} | "
                       f"{('`' + a.default + '`') if a.default else '—'} |")
        out.append("")
    return "\n".join(out)


def render_type(t: TypeDef) -> str:
    out = []
    label = {"type": "Object", "input": "Input", "enum": "Enum",
             "scalar": "Scalar", "interface": "Interface"}.get(t.kind, t.kind)
    out.append(f"### {t.name}\n")
    out.append(f"*{label}*\n")
    if t.desc:
        out.append(t.desc + "\n")
    if t.kind == "scalar":
        out.append("")
        return "\n".join(out)
    if t.kind == "enum":
        out.append("| Value | Description |")
        out.append("|-------|-------------|")
        for v, d in t.values:
            out.append(f"| `{v}` | {md_escape(d) if d else '—'} |")
        out.append("")
        return "\n".join(out)
    # object / input
    has_default = any(fl.default is not None for fl in t.fields)
    if has_default:
        out.append("| Field | Type | Default | Description |")
        out.append("|-------|------|---------|-------------|")
        for fl in t.fields:
            desc = fl.desc
            if fl.directive:
                desc = (desc + f" _Auth: {fl.directive}_").strip()
            dflt = f"`{fl.default}`" if fl.default is not None else "—"
            out.append(f"| `{fl.name}` | {type_link(fl.type)} | {dflt} | "
                       f"{md_escape(desc) if desc else '—'} |")
    else:
        out.append("| Field | Type | Description |")
        out.append("|-------|------|-------------|")
        for fl in t.fields:
            desc = fl.desc
            if fl.directive:
                desc = (desc + f" _Auth: {fl.directive}_").strip()
            out.append(f"| `{fl.name}` | {type_link(fl.type)} | "
                       f"{md_escape(desc) if desc else '—'} |")
    out.append("")
    return "\n".join(out)


def render_domain_page(slug, title, modules,
                       types_by_module, queries_by_module, mutations_by_module) -> str:
    out = []
    out.append(f"# {title}\n")
    out.append(f"[← API Reference index](./README.md)\n")
    src = ", ".join(f"`schema/{m}.graphql`" for m in modules)
    out.append(f"> Source: {src}\n")

    if slug in DOMAIN_NOTES:
        out.append(DOMAIN_NOTES[slug] + "\n")

    queries, mutations, types = [], [], []
    for m in modules:
        queries.extend(queries_by_module.get(m, []))
        mutations.extend(mutations_by_module.get(m, []))
        types.extend(types_by_module.get(m, []))

    if queries:
        out.append("## Queries\n")
        for op in queries:
            out.append(render_op(op))
    if mutations:
        out.append("## Mutations\n")
        for op in mutations:
            out.append(render_op(op))

    if types:
        out.append("## Types\n")
        # objects, then inputs, then enums, then scalars
        order = {"type": 0, "input": 1, "enum": 2, "scalar": 3, "interface": 4}
        for t in sorted(types, key=lambda x: (order.get(x.kind, 9), x.name)):
            out.append(render_type(t))

    return "\n".join(out).rstrip() + "\n"


def render_readme(nav, core_types, core_q, core_m) -> str:
    out = []
    out.append("# Agent Platform — GraphQL API Reference\n")
    out.append(
        "Auto-generated from the source-of-truth schema modules under "
        "[`schema/*.graphql`](../../schema). Regenerate with "
        "`python3 tools/apidocs/gen.py` after any schema change "
        "(and `make schema-dump` to refresh the merged SDL at "
        "[`docs/schema.graphql`](../schema.graphql)).\n"
    )
    out.append("All operations are served from a single endpoint: **`POST /query`**.\n")

    # TOC
    out.append("## Domains\n")
    out.append("| Page | Covers |")
    out.append("|------|--------|")
    covers = {
        "agents": "Agent catalog/templates, agent instances, deploy from OVA, snapshots, lifecycle",
        "agent-config": "Agent configs, content-library artifacts, skills, container images",
        "model-gateway": "Model gateways, gateway connections, upstreams, model routes, difficulty router tiers",
        "virtual-keys": "Per-user LiteLLM virtual keys and rate-limit policies",
        "observability": "Request logs, audit logs, request metrics, rate-limit policies",
        "metering": "Token usage and cost aggregation (metering center)",
        "platform": "Users, built-in roles, custom roles, permissions, departments, memberships",
        "resource-pools": "vCenter resource pools and vSphere placement pools",
        "ova-marketplace": "OVA template families and versions (agent marketplace catalog)",
        "dashboard": "Console overview: stat cards, recent agents, system notices",
    }
    for slug, title in nav:
        out.append(f"| [{title}](./{slug}.md) | {covers.get(slug, '')} |")
    out.append("")

    # Auth & RBAC
    out.append("## Authentication & RBAC\n")
    out.append(AUTH_SECTION)

    # Core / cross-cutting
    out.append("## Core operations\n")
    out.append(
        "These live in `schema/schema.graphql` and are not tied to a single domain. "
        "`login` is the only operation callable while unauthenticated.\n"
    )
    if core_q:
        out.append("### Queries\n")
        for op in core_q:
            out.append(render_op(op))
    if core_m:
        out.append("### Mutations\n")
        for op in core_m:
            out.append(render_op(op))

    # Core types (scalars, AuthPayload, User, audit, paging)
    if core_types:
        out.append("## Core types\n")
        order = {"type": 0, "input": 1, "enum": 2, "scalar": 3}
        for t in sorted(core_types, key=lambda x: (order.get(x.kind, 9), x.name)):
            out.append(render_type(t))

    return "\n".join(out).rstrip() + "\n"


AUTH_SECTION = """\
### Bearer-token auth

1. Call the `login` mutation with `LoginInput { email, password }` (the form
   collects an email; the backend accepts username *or* email).
2. `login` returns an `AuthPayload` whose **`token`** is the session id.
3. Send that token on every subsequent request as
   **`Authorization: Bearer <token>`**. (For same-origin browser use the server
   also accepts the `ap_session` cookie as a fallback.)
4. `me` returns the current `User`; `logout` invalidates the session;
   `changePassword` rotates the caller's password.

`AuthPayload.mustChangePassword` (and `User.mustChangePassword`) signals a
first-login forced password change.

### How the directives gate operations

Two schema directives enforce access control on individual fields:

- **`@hasRole(any: [RoleName!]!)`** — the caller's platform role must be one of
  the listed roles. Unauthenticated callers are rejected (`unauthenticated`);
  authorized-but-wrong-role callers get `forbidden`.
- **`@hasPermission(perm: String!)`** — the caller must hold the named permission
  key. This is checked first against the static role→permission matrix
  (fast path) and then against the union of the caller's **custom-role**
  permissions (`user_roles → role_permissions`), so admin-configured custom
  roles actually grant access.

A field with no directive is available to any authenticated caller. Some
operations additionally enforce **owner scoping** in the resolver (e.g. a regular
user sees only their own agents) — that scoping is *not* expressed by a directive
and is noted in the per-operation descriptions where relevant.

> **Enum vs. storage spelling:** the GraphQL `RoleName` enum uses `tenant_admin`
> (GraphQL enums cannot contain hyphens), while the auth/session layer stores the
> role as `tenant-admin`. The directive layer maps between them
> (`internal/graph/directives.go`).

### Platform roles (`RoleName`)

| Role | Description |
|------|-------------|
| `admin` | Super administrator — platform-wide access. |
| `tenant_admin` | Tenant-wide administrator (progressive multi-tenant rollout). |
| `observability` | Read-only observability specialist (audit + metering). |
| `user` | Regular user — access to their own resources only. |

### Permission matrix (`rolePermissions`)

From `internal/auth/rbac.go`. A ✓ means the role holds that permission
platform-wide. `user` holds no platform-wide permissions; its access to its own
resources is resolved per-resource (owner scoping), not via this table.

| Permission key | `admin` | `tenant_admin` | `observability` | `user` |
|----------------|:------:|:--------------:|:---------------:|:------:|
| `agent:manage` | ✓ | ✓ | | |
| `key:manage` | ✓ | ✓ | | |
| `route:manage` | ✓ | ✓ | | |
| `audit:view` | ✓ | ✓ | ✓ | |
| `metering:view` | ✓ | ✓ | ✓ | |
| `user:manage` | ✓ | ✓ | | |

> Note: `tenant_admin` holds `route:manage`, but the model-gateway/route
> operations are gated with `@hasRole(any: [admin])` (not the permission),
> because routes are platform-global config with no tenant scoping — a permission
> gate would leak cross-tenant config. See `schema/gateway-routing.graphql`.
"""


if __name__ == "__main__":
    sys.exit(main())
