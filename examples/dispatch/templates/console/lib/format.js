export function formatMoney(cents) {
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(Number(cents || 0) / 100)
}

export function nextStatus(status) {
  return ({ scheduled: 'assigned', assigned: 'picked up', picked_up: 'delivered' })[status] || ''
}

export function statusLabel(status) {
  return String(status || '').replaceAll('_', ' ')
}

