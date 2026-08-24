import { proxyJSON, requiredURL } from '../../../lib/upstream.js'

export function GET(request) {
  const query = new URL(request.url).searchParams
  return proxyJSON(request, `${requiredURL('API_URL')}/estimates?${query.toString()}`)
}
