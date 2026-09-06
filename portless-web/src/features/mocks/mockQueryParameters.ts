import type { MockQueryMatcher } from '../../api/contracts/mocks'
import type { MockNameValueDraft } from './MockNameValueEditor'

export function mockQueryParameterDrafts(query?: Record<string, MockQueryMatcher>): MockNameValueDraft[] {
  return Object.entries(query || {}).map(([name, matcher], index) => ({ id: index + 1, name, match: matcher.match, value: matcher.match === 'exists' ? '' : matcher.value }))
}

export function mockRequiredQueryParameters(rows: MockNameValueDraft[]): Record<string, MockQueryMatcher> {
  const query: Record<string, MockQueryMatcher> = Object.create(null)
  for (const row of rows) {
    const name = queryParameterName(row)
    if (name === null) continue
    if (Object.hasOwn(query, name)) throw new Error('Required query parameter ' + name + ' is duplicated.')
    if (row.match === 'equals' && row.value === '') throw new Error('Enter a value for query parameter ' + name + ', or choose Exists to match any value.')
    const match = row.match || (row.value === '' ? 'exists' : 'equals')
    if (match === 'regex' && !row.value) throw new Error('Enter a regex for query parameter ' + name + '.')
    if (match === 'regex' && new TextEncoder().encode(row.value).length > 4096) throw new Error('Regex for query parameter ' + name + ' exceeds 4096 bytes.')
    query[name] = match === 'exists' ? { match } : { match, value: row.value }
  }
  return query
}

export function mockPreviewQueryParameters(rows: MockNameValueDraft[]): Record<string, string[]> {
  const query: Record<string, string[]> = Object.create(null)
  for (const row of rows) {
    const name = queryParameterName(row)
    if (name === null) continue
    ;(query[name] ||= []).push(row.value)
  }
  return query
}

function queryParameterName(row: MockNameValueDraft): string | null {
  const name = row.name.trim()
  if (!name && !row.value.trim()) return null
  if (!name) throw new Error('Query parameter name is required when a value is present.')
  return name
}
