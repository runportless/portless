'use client'

import { useMemo, useState } from 'react'
import { formatMoney, nextStatus, statusLabel } from '../lib/format.js'
import RouteMap from './RouteMap.js'

export default function Dashboard({ initialLocations, initialDeliveries, initialEvents, initialProvider, initialError }) {
  const [locations] = useState(initialLocations)
  const [deliveries, setDeliveries] = useState(initialDeliveries)
  const [events, setEvents] = useState(initialEvents)
  const [provider, setProvider] = useState(initialProvider)
  const [pickup, setPickup] = useState(initialLocations[0]?.code || '')
  const [destination, setDestination] = useState(initialLocations[1]?.code || '')
  const [parcelSize, setParcelSize] = useState('medium')
  const [priority, setPriority] = useState('standard')
  const [estimate, setEstimate] = useState(null)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState(initialError)
  const selectedPickup = useMemo(() => locations.find((location) => location.code === pickup), [locations, pickup])
  const selectedDestination = useMemo(() => locations.find((location) => location.code === destination), [locations, destination])

  const estimateRoute = async (event) => {
    event.preventDefault()
    setBusy('estimate'); setError('')
    try {
      setEstimate(await requestJSON(`/dispatch/estimates?${new URLSearchParams({ pickup, destination, size: parcelSize, priority })}`))
    } catch (value) { setError(value.message) }
    finally { setBusy('') }
  }

  const createDelivery = async () => {
    setBusy('create'); setError('')
    try {
      const created = await requestJSON('/dispatch/deliveries', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ pickup, destination, parcelSize, priority }) })
      setDeliveries((current) => [created, ...current])
      window.setTimeout(() => void refreshEvents(), 150)
    } catch (value) { setError(value.message) }
    finally { setBusy('') }
  }

  const advance = async (delivery) => {
    setBusy(delivery.id); setError('')
    try {
      const updated = await requestJSON(`/dispatch/deliveries/${encodeURIComponent(delivery.id)}/advance`, { method: 'POST' })
      setDeliveries((current) => current.map((item) => item.id === updated.id ? updated : item))
      window.setTimeout(() => void refreshEvents(), 150)
    } catch (value) { setError(value.message) }
    finally { setBusy('') }
  }

  const refreshEvents = async () => {
    try { setEvents((await requestJSON('/dispatch/events')).events || []) }
    catch (value) { setError(value.message) }
  }

  const refreshDeliveries = async () => {
    try {
      const value = await requestJSON('/dispatch/deliveries')
      setDeliveries(value.deliveries || []); setProvider(value.provider || 'local')
    } catch (value) { setError(value.message) }
  }

  return <main className="dashboard">
    <header className="masthead">
      <div><span className="eyebrow">PORTLESS EXAMPLE / DISPATCH</span><h1>Courier control</h1><p>Estimate routes, schedule deliveries, and follow operational events across three source checkouts.</p></div>
      <div className="provider-badge"><span>DATA PROVIDER</span><strong>{provider}</strong></div>
    </header>

    {error && <div className="error-notice" role="alert"><strong>Request failed</strong><span>{error}</span><button onClick={() => setError('')}>Dismiss</button></div>}

    <section className="workspace-grid">
      <form className="panel planner" onSubmit={estimateRoute}>
        <div className="panel-title"><span>NEW DELIVERY</span><small>ROUTE PLANNER</small></div>
        <label><span>Pickup</span><select value={pickup} onChange={(event) => { setPickup(event.target.value); setEstimate(null) }}>{locations.map((location) => <option key={location.code} value={location.code}>{location.name}</option>)}</select></label>
        <label><span>Destination</span><select value={destination} onChange={(event) => { setDestination(event.target.value); setEstimate(null) }}>{locations.map((location) => <option key={location.code} value={location.code}>{location.name}</option>)}</select></label>
        <div className="field-pair">
          <label><span>Parcel size</span><select value={parcelSize} onChange={(event) => { setParcelSize(event.target.value); setEstimate(null) }}><option value="small">Small</option><option value="medium">Medium</option><option value="large">Large</option></select></label>
          <label><span>Priority</span><select value={priority} onChange={(event) => { setPriority(event.target.value); setEstimate(null) }}><option value="standard">Standard</option><option value="express">Express</option></select></label>
        </div>
        <div className="planner-actions"><button className="button button--primary" disabled={!!busy || !pickup || !destination}>{busy === 'estimate' ? 'Estimating…' : 'Estimate route'}</button><button className="button" type="button" disabled={!estimate || !!busy} onClick={createDelivery}>{busy === 'create' ? 'Scheduling…' : 'Schedule delivery'}</button></div>
      </form>

      <section className="panel route-panel">
        <div className="panel-title"><span>ROUTE</span><small>{estimate?.strategy || 'AWAITING ESTIMATE'}</small></div>
        <RouteMap pickup={selectedPickup} destination={selectedDestination} />
        <div className="metrics">
          <div><span>Distance</span><strong>{estimate ? `${estimate.distanceKm} km` : '—'}</strong></div>
          <div><span>ETA</span><strong>{estimate ? `${estimate.etaMinutes} min` : '—'}</strong></div>
          <div><span>Price</span><strong>{estimate ? formatMoney(estimate.priceCents) : '—'}</strong></div>
        </div>
      </section>
    </section>

    <section className="lower-grid">
      <section className="panel deliveries-panel">
        <div className="panel-title"><span>ACTIVE DELIVERIES</span><button onClick={refreshDeliveries}>Refresh</button></div>
        <div className="delivery-table">
          <div className="delivery-row delivery-row--head"><span>ID</span><span>ROUTE</span><span>ETA</span><span>PRICE</span><span>STATUS</span><span>ACTION</span></div>
          {deliveries.map((delivery) => <div className="delivery-row" key={delivery.id}>
            <strong>{delivery.id}</strong><span>{locationName(locations, delivery.pickup)} → {locationName(locations, delivery.destination)}</span><span>{delivery.etaMinutes} min</span><span>{formatMoney(delivery.priceCents)}</span><span className={`status status--${delivery.status}`}>{statusLabel(delivery.status)}</span><button disabled={!nextStatus(delivery.status) || !!busy} onClick={() => advance(delivery)}>{busy === delivery.id ? 'Updating…' : nextStatus(delivery.status) ? `Mark ${nextStatus(delivery.status)}` : 'Complete'}</button>
          </div>)}
          {deliveries.length === 0 && <div className="empty">No deliveries have been scheduled in this environment.</div>}
        </div>
      </section>

      <section className="panel activity-panel">
        <div className="panel-title"><span>EVENT FEED</span><button onClick={refreshEvents}>Refresh</button></div>
        <div className="event-list">
          {events.map((event) => <article key={event.eventId}><i /><div><strong>{event.deliveryId}</strong><span>{statusLabel(event.status)}</span><small>{event.type} · {formatTime(event.occurredAt)}</small></div></article>)}
          {events.length === 0 && <div className="empty">Delivery events published through NATS appear here.</div>}
        </div>
      </section>
    </section>
  </main>
}

async function requestJSON(url, init) {
  const response = await fetch(url, init)
  const value = await response.json().catch(() => ({ error: { message: 'The service returned invalid JSON.' } }))
  if (!response.ok) {
    const policy = response.headers.get('X-Portless-Remote-Policy')
    throw new Error(`${value?.error?.message || `HTTP ${response.status}`}${policy ? ` (${policy} remote provider)` : ''}`)
  }
  return value
}

function locationName(locations, code) { return locations.find((location) => location.code === code)?.name || code }
function formatTime(value) { return value ? new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : 'now' }
