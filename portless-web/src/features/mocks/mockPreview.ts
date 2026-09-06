import type { MockRequest } from '../../api/contracts/mocks'
import type { MockRouteDraft } from './MockRouteEditor'
import type { MockNameValueDraft } from './MockNameValueEditor'
import { mockPreviewQueryParameters } from './mockQueryParameters'

export interface MockPreviewRequestDraft {
  service: string
  method: string
  path: string
  query: MockNameValueDraft[]
}

export function newMockPreviewRequest(draft: MockRouteDraft): MockPreviewRequestDraft {
  return {
    service: draft.service,
    method: draft.method,
    path: draft.path.replace(/\{([A-Za-z_][A-Za-z0-9_]*)\}/g, '$1-123'),
    query: draft.query.map(({ id, name, value, match }) => ({ id, name, value: match === 'exists' || match === 'regex' ? '' : value })),
  }
}

export function mockPreviewRequest(draft: MockPreviewRequestDraft): MockRequest {
  const service = draft.service.trim()
  const method = draft.method.trim().toUpperCase()
  const path = draft.path.trim()
  if (!service) throw new Error('Choose a service to preview.')
  if (!/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(method)) throw new Error('Enter a valid HTTP method.')
  if (!path.startsWith('/') || path.startsWith('//') || /[?#\s]/.test(path)) {
    throw new Error('Enter an absolute path such as /inventory/sku-123. Put query parameters in the query table.')
  }
  if (/[{}]/.test(path)) throw new Error('Replace path parameters with sample values, such as /inventory/sku-123.')
  return { service, method, path, query: mockPreviewQueryParameters(draft.query) }
}

export function formatMockPreviewBody(body: string): string {
  try { return JSON.stringify(JSON.parse(body), null, 2) }
  catch { return body }
}
