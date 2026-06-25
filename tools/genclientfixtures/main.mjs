// Regenerate the client-contract fixtures: snapshot every agent-platform-console
// GraphQL operation (fragments inlined) into
// internal/graph/testdata/client_operations/<OpName>.graphql.
//
// TestClientOperationsMatchSchema then validates each against the backend schema,
// so frontend↔backend contract drift fails CI.
//
// Usage (from the backend repo root):
//   node tools/genclientfixtures/main.mjs [path-to-console-repo]
// Default console path: ../agent-platform-console (sibling checkout).
import { readFileSync, readdirSync, writeFileSync, mkdirSync, rmSync, existsSync } from 'node:fs'

const consoleRepo = process.argv[2] ?? '../agent-platform-console'
const QDIR = `${consoleRepo}/src/api/graphql/queries`
const OUT = 'internal/graph/testdata/client_operations'

let files
try {
  files = readdirSync(QDIR).filter((f) => f.endsWith('.ts'))
} catch {
  console.error(`genclientfixtures: cannot read ${QDIR} — pass the console repo path as arg 1`)
  process.exit(1)
}
const text = files.map((f) => readFileSync(`${QDIR}/${f}`, 'utf8')).join('\n')

// const-name -> raw GraphQL body, for ${interpolation} resolution (both styles)
const consts = {}
for (const m of text.matchAll(/const\s+(\w+)\s*=\s*(?:\/\*\s*GraphQL\s*\*\/|gql)\s*`([\s\S]*?)`/g)) {
  consts[m[1]] = m[2]
}
const inline = (body) =>
  body.replace(/\$\{(\w+)\}/g, (_, n) => {
    if (!(n in consts)) {
      console.error(`genclientfixtures: unresolved interpolation \${${n}} — fragment defined in an unscanned file?`)
      process.exit(1)
    }
    return consts[n]
  })

// Prune only the generated fixtures (keep README.md and anything else here).
mkdirSync(OUT, { recursive: true })
if (existsSync(OUT)) {
  for (const f of readdirSync(OUT).filter((f) => f.endsWith('.graphql'))) rmSync(`${OUT}/${f}`)
}

// Operations the console ships but the backend hasn't implemented yet. Add an
// entry here only as a temporary measure; remove it the moment its backend op
// lands so drift is caught again.
//
// Block 6 (agent marketplace) is now fully implemented + contract-tested:
//   - Block 6a (OVA-template catalog) landed earlier.
//   - Block 6b (DeployAgent) is implemented as create-from-OVA; the marketplace's
//     separate VirtualKey concept (AvailableVirtualKeys / CreateVirtualKey) was
//     eliminated — deploy issues the key itself and returns its secret once. Those
//     two ops no longer exist in the console, so this set is empty.
const DEFERRED = new Set([])

let count = 0
let skipped = 0
for (const m of text.matchAll(/gql`([\s\S]*?)`/g)) {
  const body = inline(m[1]).trim()
  const op = body.match(/\b(query|mutation)\s+(\w+)/)
  if (!op) continue // fragment-only block
  if (DEFERRED.has(op[2])) {
    skipped++
    continue
  }
  writeFileSync(`${OUT}/${op[2]}.graphql`, body + '\n')
  count++
}
console.log(`wrote ${count} operation fixtures to ${OUT}${skipped ? ` (skipped ${skipped} deferred: Block 6)` : ''}`)
