import { describe, expect, it } from 'vitest'
import type { MockQueryMatcher } from '../../api/contracts/mocks'
import { mockPreviewQueryParameters, mockQueryParameterDrafts, mockRequiredQueryParameters } from './mockQueryParameters'

describe('mock query parameter rows', () => {
  it('creates independent rows with stable initial IDs', () => {
    const query: Record<string, MockQueryMatcher> = { warehouse: { match: 'equals', value: 'central' }, optional: { match: 'exists' } }
    const rows = mockQueryParameterDrafts(query)
    expect(rows).toEqual([{ id: 1, name: 'warehouse', match: 'equals', value: 'central' }, { id: 2, name: 'optional', match: 'exists', value: '' }])
    rows[0].value = 'west'
    expect(query.warehouse).toEqual({ match: 'equals', value: 'central' })
    expect(mockQueryParameterDrafts()).toEqual([])
    expect(mockQueryParameterDrafts({})).toEqual([])
  })

  it('preserves literal and empty values while ignoring blank rows and IDs', () => {
    const query = mockRequiredQueryParameters([
      { id: 3, name: ' expression ', value: 'a=b&c:d?e+f%20g' },
      { id: 8, name: 'empty', value: '' },
      { id: 20, name: 'spaces', value: '  retained  ' },
      { id: 24, name: 'a=b&c:?', value: 'literal name' },
      { id: 40, name: '', value: '' },
      { id: 41, name: ' \t', value: '\t ' },
    ])
    expect(query).toEqual({ expression: { match: 'equals', value: 'a=b&c:d?e+f%20g' }, empty: { match: 'exists' }, spaces: { match: 'equals', value: '  retained  ' }, 'a=b&c:?': { match: 'equals', value: 'literal name' } })
    expect(Object.getPrototypeOf(query)).toBeNull()
    expect(JSON.stringify(query)).not.toContain('"id"')
  })

  it('rejects duplicate required names but keeps query names case-sensitive', () => {
    expect(() => mockRequiredQueryParameters([{ id: 1, name: 'tag', value: 'one' }, { id: 2, name: ' tag ', value: 'two' }])).toThrow(/duplicated/)
    expect(mockRequiredQueryParameters([{ id: 1, name: 'Tag', value: 'one' }, { id: 2, name: 'tag', value: 'two' }])).toEqual({ Tag: { match: 'equals', value: 'one' }, tag: { match: 'equals', value: 'two' } })
  })

  it('applies explicit query operators without persisting a hidden Exists value', () => {
    expect(mockRequiredQueryParameters([{ id: 1, name: 'coupon', value: 'retained draft', match: 'exists' }])).toEqual({ coupon: { match: 'exists' } })
    expect(mockRequiredQueryParameters([{ id: 1, name: 'coupon', value: 'summer', match: 'equals' }])).toEqual({ coupon: { match: 'equals', value: 'summer' } })
    expect(() => mockRequiredQueryParameters([{ id: 1, name: 'coupon', value: '', match: 'equals' }])).toThrow(/choose Exists/)
  })

  it('round trips regex operators and preserves Go syntax for daemon validation', () => {
    const query: Record<string, MockQueryMatcher> = { sku: { match: 'regex', value: String.raw`\Acoffee-\p{L}+\z` } }
    expect(mockRequiredQueryParameters(mockQueryParameterDrafts(query))).toEqual(query)
    expect(() => mockRequiredQueryParameters([{ id: 1, name: 'sku', value: '', match: 'regex' }])).toThrow(/Enter a regex/)
    expect(() => mockRequiredQueryParameters([{ id: 1, name: 'sku', value: 'é'.repeat(2049), match: 'regex' }])).toThrow(/4096 bytes/)
  })

  it('collects repeated sample values in row order', () => {
    const query = mockPreviewQueryParameters([
      { id: 1, name: 'tag', value: 'one' },
      { id: 3, name: 'Tag', value: 'capitalized' },
      { id: 4, name: 'tag', value: '' },
      { id: 8, name: ' tag ', value: 'a=b&c:d?e' },
      { id: 9, name: 'spaces', value: '  retained  ' },
      { id: 10, name: '', value: '' },
    ])
    expect(query).toEqual({ tag: ['one', '', 'a=b&c:d?e'], Tag: ['capitalized'], spaces: ['  retained  '] })
    expect(Object.getPrototypeOf(query)).toBeNull()
  })

  it('preserves special property names in required and sample maps', () => {
    const rows = mockQueryParameterDrafts({ ['__proto__']: { match: 'equals', value: 'safe' }, constructor: { match: 'equals' as const, value: 'value' }, toString: { match: 'equals' as const, value: 'literal' } })
    const required = mockRequiredQueryParameters(rows)
    const sample = mockPreviewQueryParameters([...rows, { id: 4, name: '__proto__', value: 'second' }])
    expect(required).toEqual({ ['__proto__']: { match: 'equals', value: 'safe' }, constructor: { match: 'equals' as const, value: 'value' }, toString: { match: 'equals' as const, value: 'literal' } })
    expect(sample).toEqual({ ['__proto__']: ['safe', 'second'], constructor: ['value'], toString: ['literal'] })
    expect(Object.getPrototypeOf(required)).toBeNull()
    expect(Object.getPrototypeOf(sample)).toBeNull()
  })

  it.each([mockRequiredQueryParameters, mockPreviewQueryParameters])('rejects a value without a parameter name', (convert) => {
    expect(() => convert([{ id: 1, name: ' ', value: 'present' }])).toThrow(/name is required/)
  })
})
