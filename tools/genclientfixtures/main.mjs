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
import { readFileSync, readdirSync, writeFileSync, mkdirSync, rmSync } from 'node:fs'

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
const inline = (body) => body.replace(/\$\{(\w+)\}/g, (_, n) => consts[n] ?? '')

rmSync(OUT, { recursive: true, force: true })
mkdirSync(OUT, { recursive: true })

let count = 0
for (const m of text.matchAll(/gql`([\s\S]*?)`/g)) {
  const body = inline(m[1]).trim()
  const op = body.match(/\b(query|mutation)\s+(\w+)/)
  if (!op) continue // fragment-only block
  writeFileSync(`${OUT}/${op[2]}.graphql`, body + '\n')
  count++
}
console.log(`wrote ${count} operation fixtures to ${OUT}`)
