import { proxyJSON, requiredURL } from '../../../../../lib/upstream.js'

export function POST(request, { params }) {
  return Promise.resolve(params).then(({ id }) => proxyJSON(
    request,
    `${requiredURL('API_URL')}/deliveries/${encodeURIComponent(id)}/advance`,
    { method: 'POST', body: '{}' },
  ))
}
