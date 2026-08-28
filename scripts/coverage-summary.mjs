import { readFile, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'

const coverageDirectory = resolve(process.argv[2] || 'coverage')

function percentage(covered, total) {
  return total === 0 ? 'n/a' : `${(covered / total * 100).toFixed(2)}%`
}

function metric(covered, total) {
  return total === 0 ? '—' : `${percentage(covered, total)} (${covered}/${total})`
}

function parseGoProfile(profile) {
  let covered = 0
  let total = 0
  for (const line of profile.trim().split('\n').slice(1)) {
    const match = line.match(/\s(\d+)\s+(\d+)$/)
    if (!match) continue
    const statements = Number(match[1])
    total += statements
    if (Number(match[2]) > 0) covered += statements
  }
  return { covered, total }
}

function parseLCOV(profile) {
  const totals = { branches: 0, branchesCovered: 0, functions: 0, functionsCovered: 0, lines: 0, linesCovered: 0 }
  for (const line of profile.split('\n')) {
    const [name, rawValue] = line.split(':', 2)
    const value = Number(rawValue)
    if (!Number.isFinite(value)) continue
    if (name === 'BRF') totals.branches += value
    if (name === 'BRH') totals.branchesCovered += value
    if (name === 'FNF') totals.functions += value
    if (name === 'FNH') totals.functionsCovered += value
    if (name === 'LF') totals.lines += value
    if (name === 'LH') totals.linesCovered += value
  }
  return totals
}

const [goProfile, webSummaryText, siteProfile] = await Promise.all([
  readFile(resolve(coverageDirectory, 'go/coverage.out'), 'utf8'),
  readFile(resolve(coverageDirectory, 'web/coverage-summary.json'), 'utf8'),
  readFile(resolve(coverageDirectory, 'site/lcov.info'), 'utf8'),
])

const go = parseGoProfile(goProfile)
const web = JSON.parse(webSummaryText).total
const site = parseLCOV(siteProfile)
const summary = `# Code coverage

| Area | Statements | Branches | Functions | Lines |
| --- | ---: | ---: | ---: | ---: |
| Go | ${metric(go.covered, go.total)} | — | — | — |
| Web control plane | ${metric(web.statements.covered, web.statements.total)} | ${metric(web.branches.covered, web.branches.total)} | ${metric(web.functions.covered, web.functions.total)} | ${metric(web.lines.covered, web.lines.total)} |
| Marketing site TypeScript | — | ${metric(site.branchesCovered, site.branches)} | ${metric(site.functionsCovered, site.functions)} | ${metric(site.linesCovered, site.lines)} |

Detailed Go, LCOV, JSON, and HTML reports are available in the coverage artifact.
`

await writeFile(resolve(coverageDirectory, 'summary.md'), summary)
process.stdout.write(summary)
