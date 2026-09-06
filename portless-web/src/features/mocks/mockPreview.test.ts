import { describe, expect, it } from 'vitest'
import { newMockRouteDraft } from './MockRouteEditor'
import { formatMockPreviewBody, mockPreviewRequest, newMockPreviewRequest } from './mockPreview'
import { mockQueryParameterDrafts } from './mockQueryParameters'

describe('mock preview requests', () => {
  it('suggests concrete parameter values and retains required query values', () => {
    const draft = { ...newMockRouteDraft(1, 'inventory'), method: 'POST', path: '/warehouses/{warehouse_id}/inventory/{sku}', query: mockQueryParameterDrafts({ include: { match: 'equals', value: 'stock' }, optional: { match: 'exists' } }) }
    const request = newMockPreviewRequest(draft)
    expect(request).toEqual({ service: 'inventory', method: 'POST', path: '/warehouses/warehouse_id-123/inventory/sku-123', query: [{ id: 1, name: 'include', value: 'stock' }, { id: 2, name: 'optional', value: '' }] })
    expect(request.query).not.toBe(draft.query)
    expect(request.query[0]).not.toBe(draft.query[0])
    request.query[0].value = 'changed sample'
    request.query.push({ id: 3, name: 'sample', value: 'only' })
    expect(draft.query).toEqual(mockQueryParameterDrafts({ include: { match: 'equals', value: 'stock' }, optional: { match: 'exists' } }))
    expect(newMockPreviewRequest(draft).query).toEqual([{ id: 1, name: 'include', value: 'stock' }, { id: 2, name: 'optional', value: '' }])
    expect(draft.path).toBe('/warehouses/{warehouse_id}/inventory/{sku}')
  })

  it('preserves repeated, empty, and literal query values, including special property names', () => {
    const query = [...mockQueryParameterDrafts({ tag: { match: 'equals', value: 'one' }, empty: { match: 'exists' }, expression: { match: 'equals', value: 'a=b&c:d?' }, ['__proto__']: { match: 'equals', value: 'safe' }, constructor: { match: 'equals' as const, value: 'value' } }), { id: 6, name: 'tag', value: 'two' }]
    const request = mockPreviewRequest({ service: 'inventory', method: 'get', path: '/items', query })
    expect(request).toEqual({ service: 'inventory', method: 'GET', path: '/items', query: { tag: ['one', 'two'], empty: [''], expression: ['a=b&c:d?'], ['__proto__']: ['safe'], constructor: ['value'] } })
  })

  it.each(['https://example.com/items', '//example.com/items', '/items?q=a', '/items#fragment', '/items/{id}', '/items with spaces'])('rejects a non-concrete request path %s', (path) => {
    expect(() => mockPreviewRequest({ service: 'inventory', method: 'GET', path, query: [] })).toThrow()
  })

  it('rejects malformed query rows and HTTP methods', () => {
    const draft = { service: 'inventory', method: 'GET', path: '/items', query: [{ id: 1, name: '', value: 'missing' }] }
    expect(() => mockPreviewRequest(draft)).toThrow(/name is required/)
    expect(() => mockPreviewRequest({ ...draft, query: [], method: 'GET\nPOST' })).toThrow('HTTP method')
  })

  it('formats valid JSON and preserves other response text', () => {
    expect(formatMockPreviewBody('{"available":false}')).toBe('{\n  "available": false\n}')
    expect(formatMockPreviewBody('null')).toBe('null')
    expect(formatMockPreviewBody('<script>example()</script>')).toBe('<script>example()</script>')
    expect(formatMockPreviewBody('invalid {')).toBe('invalid {')
  })

  it.each(['exists', 'regex'] as const)('suggests an empty sample for %s and leaves operators out of preview inputs', (match) => {
    const draft = { ...newMockRouteDraft(1, 'inventory'), query: [{ id: 1, name: 'coupon', value: 'previous value', match }] }
    expect(newMockPreviewRequest(draft).query).toEqual([{ id: 1, name: 'coupon', value: '' }])
    expect(draft.query[0].value).toBe('previous value')
  })
})
