export const createOrdersTableSQL = `
CREATE TABLE IF NOT EXISTS store_orders (
  id INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  sku TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK (quantity > 0),
  state TEXT NOT NULL DEFAULT 'created',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

export const insertOrderSQL = `
INSERT INTO store_orders (sku, quantity)
VALUES ($1, $2)
RETURNING id, sku, quantity, state, created_at`

export const selectOrderSQL = `
SELECT id, sku, quantity, state, created_at
FROM store_orders
WHERE id = $1`

function orderFromRow(row) {
  if (!row) return null
  return {
    id: Number(row.id),
    sku: row.sku,
    quantity: Number(row.quantity),
    state: row.state,
    createdAt: new Date(row.created_at).toISOString(),
  }
}

export class OrderRepository {
  constructor(pool) {
    this.pool = pool
  }

  async migrate() {
    await this.pool.query(createOrdersTableSQL)
  }

  async create({ sku, quantity }) {
    const result = await this.pool.query(insertOrderSQL, [sku, quantity])
    return orderFromRow(result.rows[0])
  }

  async find(id) {
    const result = await this.pool.query(selectOrderSQL, [id])
    return orderFromRow(result.rows[0])
  }
}
