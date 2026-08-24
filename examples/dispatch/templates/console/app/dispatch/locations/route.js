import { proxyJSON, requiredURL } from '../../../lib/upstream.js'

export function GET(request) {
  const query = new URL(request.url).searchParams.get('query') || ''
  return proxyJSON(request, `${requiredURL('API_URL')}/locations?query=${encodeURIComponent(query)}`)
}
