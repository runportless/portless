import { useRef } from 'react'
export interface MockNameValueDraft {
  id: number
  name: string
  value: string
  match?: 'equals' | 'exists' | 'regex'
}

export function MockNameValueEditor({ rows, label, rowLabel, description, hideCaption = false, matching = false, disabled, onChange }: {
  rows: MockNameValueDraft[]
  label: string
  rowLabel: 'Header' | 'Query parameter'
  description?: string
  hideCaption?: boolean
  matching?: boolean
  disabled: boolean
  onChange: (rows: MockNameValueDraft[]) => void
}) {
  const table = useRef<HTMLTableElement>(null)
  const nextID = rows.reduce((last, row) => Math.max(last, row.id), 0) + 1
  const visibleRows: MockNameValueDraft[] = [...rows, { id: nextID, name: '', value: '', ...(matching ? { match: 'equals' as const } : {}) }]
  const item = rowLabel.toLowerCase()

  const change = (row: MockNameValueDraft, fields: Partial<MockNameValueDraft>) => {
    const updated = { ...row, ...fields }
    onChange(row.id === nextID ? [...rows, updated] : rows.map((entry) => entry.id === row.id ? updated : entry))
  }

  const remove = (index: number) => {
    onChange(rows.filter((_, rowIndex) => rowIndex !== index))
    window.requestAnimationFrame(() => table.current?.querySelectorAll<HTMLInputElement>('input[data-entry-name]')[index]?.focus())
  }

  return <table ref={table} className={`mock-name-value${matching ? ' mock-name-value--matching' : ''}`} aria-label={label}>
    <caption className={hideCaption && !description ? 'sr-only' : undefined}><span className={hideCaption ? 'sr-only' : undefined}>{label}</span>{description && <span className="mock-name-value__description">{description}</span>}</caption>
    <colgroup><col className="mock-name-value__name" />{matching && <col className="mock-name-value__match" />}<col /><col className="mock-name-value__actions" /></colgroup>
    <thead><tr><th scope="col">NAME</th>{matching && <th scope="col">MATCH</th>}<th scope="col">VALUE</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
    <tbody>{visibleRows.map((row, index) => {
      const adding = index === rows.length
      const match = row.match || (row.value === '' ? 'exists' : 'equals')
      const exists = matching && match === 'exists'
      const regex = matching && match === 'regex'
      return <tr key={row.id}>
        <td><input data-entry-name="true" aria-label={adding ? `New ${item} name` : `${rowLabel} name ${index + 1}`} placeholder={adding ? rowLabel === 'Header' ? 'Add header' : 'Add parameter' : 'Name'} value={row.name} disabled={disabled} autoComplete="off" spellCheck="false" onChange={(event) => change(row, { name: event.target.value })} /></td>
        {matching && <td><select aria-label={adding ? `New ${item} match` : `${rowLabel} match ${index + 1}`} value={match} disabled={disabled} onChange={(event) => change(row, { match: event.target.value as MockNameValueDraft['match'] })}><option value="equals">Equals</option><option value="exists">Exists</option><option value="regex">Regex</option></select></td>}
        <td><input aria-label={adding ? `New ${item} value` : `${rowLabel} value ${index + 1}`} placeholder={exists ? 'Any value' : regex ? 'coffee-.*' : adding ? 'Add value' : 'Value'} title={regex ? 'Go regex (RE2). Matches the entire query value.' : undefined} value={exists ? '' : row.value} disabled={disabled || exists} autoComplete="off" spellCheck="false" onChange={(event) => change(row, { value: event.target.value })} /></td>
        <td>{!adding && <button type="button" aria-label={`Remove ${item} ${index + 1}`} title={`Remove ${item}`} disabled={disabled} onClick={() => remove(index)}><svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5.5" /><path d="M5.5 8h5" /></svg></button>}</td>
      </tr>
    })}</tbody>
  </table>
}
