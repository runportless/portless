const defaultTTLSeconds = 60

export function orderCacheKey(id) {
  return `store:order:${id}`
}

export class OrderCache {
  constructor(client, ttlSeconds = defaultTTLSeconds) {
    this.client = client
    this.ttlSeconds = ttlSeconds
  }

  async read(id) {
    try {
      const encoded = await this.client.get(orderCacheKey(id))
      if (encoded === null) return { status: 'miss', order: null }
      try {
        return { status: 'hit', order: JSON.parse(encoded) }
      } catch {
        await this.client.del(orderCacheKey(id)).catch(() => {})
        return { status: 'miss', order: null }
      }
    } catch {
      return { status: 'unavailable', order: null }
    }
  }

  async write(order) {
    try {
      await this.client.set(orderCacheKey(order.id), JSON.stringify(order), { EX: this.ttlSeconds })
      return true
    } catch {
      return false
    }
  }

  async remove(id) {
    try {
      await this.client.del(orderCacheKey(id))
      return true
    } catch {
      return false
    }
  }
}
