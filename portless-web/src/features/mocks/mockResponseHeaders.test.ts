import { describe, expect, it } from 'vitest'
import { mockResponseHeaderDrafts, mockResponseHeaders } from './mockResponseHeaders'

describe('mock response header rows', () => {
  it('creates independently editable rows with stable initial IDs', () => {
    const headers = { 'Content-Type': 'application/json', 'X-Mode': 'sold-out' }
    const rows = mockResponseHeaderDrafts(headers)
    expect(rows).toEqual([{ id: 1, name: 'Content-Type', value: 'application/json' }, { id: 2, name: 'X-Mode', value: 'sold-out' }])
    rows[0].value = 'text/plain'
    expect(headers['Content-Type']).toBe('application/json')
    expect(mockResponseHeaderDrafts()).toEqual([])
    expect(mockResponseHeaderDrafts({})).toEqual([])
  })

  it('preserves colon values, empty named values, and special property names', () => {
    const rows = [
      { id: 1, name: ' Location ', value: 'https://example.test:8443/items:a' },
      { id: 2, name: 'X-Empty', value: '' },
      { id: 3, name: 'X-Spaces', value: '  retained  ' },
      { id: 4, name: '__proto__', value: 'safe' },
      { id: 5, name: 'constructor', value: 'value' },
    ]
    const headers = mockResponseHeaders(rows)
    expect(headers).toEqual({ Location: 'https://example.test:8443/items:a', 'X-Empty': '', 'X-Spaces': '  retained  ', ['__proto__']: 'safe', constructor: 'value' })
    expect(Object.getPrototypeOf(headers)).toBeNull()
    expect(mockResponseHeaderDrafts(headers).map(({ name, value }) => ({ name, value }))).toEqual(rows.map(({ name, value }) => ({ name: name.trim(), value })))
  })

  it('ignores blank rows without using row IDs in the payload', () => {
    const headers = mockResponseHeaders([
      { id: 7, name: '', value: '' },
      { id: 11, name: '   ', value: '\t ' },
      { id: 99, name: 'X-Mode', value: 'ready' },
    ])
    expect(JSON.stringify(headers)).toBe('{"X-Mode":"ready"}')
  })

  it('rejects values without a header name and case-insensitive duplicate names', () => {
    expect(() => mockResponseHeaders([{ id: 1, name: ' ', value: 'present' }])).toThrow(/name is required/)
    expect(() => mockResponseHeaders([{ id: 1, name: 'X-Mode', value: 'one' }, { id: 2, name: 'x-mode', value: 'two' }])).toThrow(/duplicated/)
  })

  it.each(['Bad Header', 'X:Mode', 'X/Mode', 'X(Mode)'])('rejects invalid HTTP header name %s', (name) => {
    expect(() => mockResponseHeaders([{ id: 1, name, value: 'value' }])).toThrow(/valid HTTP header names/)
  })

  it.each(['Connection', 'Content-Length', 'Keep-Alive', 'Proxy-Authenticate', 'Proxy-Authorization', 'TE', 'Trailer', 'Transfer-Encoding', 'Upgrade'])('rejects transport-managed header %s', (name) => {
    expect(() => mockResponseHeaders([{ id: 1, name, value: '' }])).toThrow(/managed by the HTTP transport/)
  })

  it.each([
    { name: 'X-Mode\r', value: 'ready' },
    { name: 'X-Mode\n', value: 'ready' },
    { name: 'X-Mode', value: 'one\r\ntwo' },
    { name: '', value: '\n' },
  ])('rejects line breaks in header rows', (row) => {
    expect(() => mockResponseHeaders([{ id: 1, ...row }])).toThrow(/line breaks/)
  })
})
