export class Orders {
  constructor(repository, cache) {
    this.repository = repository
    this.cache = cache
  }

  async create(input) {
    const order = await this.repository.create(input)
    await this.cache.remove(order.id)
    return order
  }

  async find(id) {
    const cached = await this.cache.read(id)
    if (cached.status === 'hit') return cached

    const order = await this.repository.find(id)
    if (!order) return { status: cached.status, order: null }

    const stored = await this.cache.write(order)
    return {
      status: cached.status === 'unavailable' || !stored ? 'unavailable' : 'miss',
      order,
    }
  }
}
