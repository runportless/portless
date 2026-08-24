import { proxyJSON, requiredURL } from '../../../lib/upstream.js'

export function GET(request) {
  return proxyJSON(request, `${requiredURL('NOTIFIER_URL')}/events?limit=20`)
}
