import type { MockNameValueDraft } from './MockNameValueEditor'

const headerNamePattern = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/
const managedHeaders = new Set(['connection', 'content-length', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer', 'transfer-encoding', 'upgrade'])

export function mockResponseHeaderDrafts(headers?: Record<string, string>): MockNameValueDraft[] {
  return Object.entries(headers || {}).map(([name, value], index) => ({ id: index + 1, name, value }))
}

export function mockResponseHeaders(rows: MockNameValueDraft[]): Record<string, string> {
  const result: Record<string, string> = Object.create(null)
  const names = new Set<string>()
  for (const row of rows) {
    if (/[\r\n]/.test(row.name) || /[\r\n]/.test(row.value)) throw new Error('Response header names and values cannot contain line breaks.')
    const name = row.name.trim()
    if (!name && !row.value.trim()) continue
    if (!name) throw new Error('Response header name is required when a value is present.')
    if (!headerNamePattern.test(name)) throw new Error('Response header names must be valid HTTP header names.')
    const canonical = name.toLowerCase()
    if (names.has(canonical)) throw new Error('Response header ' + name + ' is duplicated.')
    if (managedHeaders.has(canonical)) throw new Error('Response header ' + name + ' is managed by the HTTP transport.')
    names.add(canonical)
    result[name] = row.value
  }
  return result
}
