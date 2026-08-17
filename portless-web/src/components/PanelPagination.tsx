export type Pagination<T> = {
  items: T[]
  page: number
  pageCount: number
  start: number
  end: number
  total: number
}

export function paginateItems<T>(items: T[], requestedPage: number, pageSize: number): Pagination<T> {
  const pageCount = Math.max(1, Math.ceil(items.length/pageSize))
  const page = Math.min(Math.max(0, requestedPage), pageCount-1)
  const start = page*pageSize
  const end = Math.min(items.length, start+pageSize)
  return { items: items.slice(start, end), page, pageCount, start, end, total: items.length }
}

export function PanelPagination<T>({ label, pagination, onPage }: { label: string; pagination: Pagination<T>; onPage: (page: number) => void }) {
  if (pagination.pageCount <= 1) return null
  return <footer className="panel-pagination" aria-label={`${label} pagination`}>
    <span>{pagination.start+1}–{pagination.end} of {pagination.total}</span>
    <div>
      <button type="button" aria-label={`Previous ${label} page`} disabled={pagination.page === 0} onClick={() => onPage(pagination.page-1)}>← PREV</button>
      <small>{pagination.page+1} / {pagination.pageCount}</small>
      <button type="button" aria-label={`Next ${label} page`} disabled={pagination.page === pagination.pageCount-1} onClick={() => onPage(pagination.page+1)}>NEXT →</button>
    </div>
  </footer>
}
