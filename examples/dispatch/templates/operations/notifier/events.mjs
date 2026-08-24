export class EventBuffer {
  constructor(limit = 50) {
    if (!Number.isInteger(limit) || limit < 1) throw new Error('event buffer limit must be positive')
    this.limit = limit
    this.events = []
  }

  add(event) {
    this.events.unshift(event)
    if (this.events.length > this.limit) this.events.length = this.limit
  }

  list(limit = this.limit) {
    const bounded = Number.isInteger(limit) ? Math.max(1, Math.min(limit, this.limit)) : this.limit
    return this.events.slice(0, bounded)
  }
}

