import { proxyJSON, requiredURL } from '../../../lib/upstream.js'

export function GET(request) {
  return proxyJSON(request, `${requiredURL('API_URL')}/deliveries`)
}

export async function POST(request) {
  const body = await request.text()
  return proxyJSON(request, `${requiredURL('API_URL')}/deliveries`, { method: 'POST', body })
}
