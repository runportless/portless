import { headers } from 'next/headers'
import Dashboard from '../components/Dashboard.js'
import { readJSON, requiredURL } from '../lib/upstream.js'

export const dynamic = 'force-dynamic'

export default async function Page() {
  const incoming = await headers()
  const api = requiredURL('API_URL')
  const notifier = requiredURL('NOTIFIER_URL')
  const results = await Promise.allSettled([
    readJSON(`${api}/locations`, incoming),
    readJSON(`${api}/deliveries`, incoming),
    readJSON(`${notifier}/events?limit=20`, incoming),
  ])

  const locations = resultValue(results[0], 'locations', [])
  const deliveryPayload = resultValue(results[1], null, { deliveries: [], provider: 'unavailable' })
  const events = resultValue(results[2], 'events', [])
  const initialError = results
    .filter((result) => result.status === 'rejected')
    .map((result) => result.reason instanceof Error ? result.reason.message : String(result.reason))
    .join(' · ')

  return <Dashboard
    initialLocations={locations}
    initialDeliveries={deliveryPayload.deliveries || []}
    initialEvents={events}
    initialProvider={deliveryPayload.provider || 'local'}
    initialError={initialError}
  />
}

function resultValue(result, field, fallback) {
  if (result.status !== 'fulfilled') return fallback
  return field ? result.value?.[field] ?? fallback : result.value ?? fallback
}

