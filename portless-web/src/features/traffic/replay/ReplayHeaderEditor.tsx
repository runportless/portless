import { useRef, useState } from 'react'
import { sensitiveReplayHeader, type ReplayHeaderRow } from './trafficReplayDraft'

export function ReplayHeaderEditor({ rows, disabled, onChange }: { rows: ReplayHeaderRow[]; disabled: boolean; onChange: (rows: ReplayHeaderRow[]) => void }) {
  const table = useRef<HTMLTableElement>(null)
  const [visible, setVisible] = useState(false)
  const nextID = Math.max(0, ...rows.map((row) => row.id)) + 1
  const display = [...rows, { id: nextID, name: '', value: '', omitted: false, sensitive: false }]
  const change = (row: ReplayHeaderRow, patch: Partial<ReplayHeaderRow>) => {
    const next = { ...row, ...patch }
    next.sensitive = row.sensitive || sensitiveReplayHeader(next.name)
    onChange(row.id === nextID ? [...rows, next] : rows.map((candidate) => candidate.id === row.id ? next : candidate))
  }
  return <div className="replay-headers">
    <div className="replay-headers__actions"><button type="button" className="traffic-copy-button" disabled={disabled} aria-pressed={visible} onClick={() => setVisible(!visible)}>{visible ? 'HIDE VALUES' : 'SHOW VALUES'}</button></div>
    <table ref={table} className="replay-header-table" aria-label="Replay request headers">
      <thead><tr><th scope="col">NAME</th><th scope="col">VALUE</th><th scope="col">OMIT</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
      <tbody>{display.map((row, index) => <tr key={row.id} className={row.omitted ? 'is-omitted' : ''}>
        <td><input data-header-name aria-label={index === rows.length ? 'New header name' : `Header name ${index + 1}`} placeholder="Header name" value={row.name} disabled={disabled} autoComplete="off" spellCheck={false} onChange={(event) => change(row, { name: event.target.value })} /></td>
        <td><input aria-label={index === rows.length ? 'New header value' : `Header value ${index + 1}`} type={row.sensitive && !visible ? 'password' : 'text'} placeholder={row.sensitive ? 'Provide value or omit' : 'Value'} value={row.value} disabled={disabled || row.omitted} autoComplete="off" data-1p-ignore="true" spellCheck={false} onChange={(event) => change(row, { value: event.target.value })} /></td>
        <td>{index < rows.length && <input type="checkbox" aria-label={`Omit header ${index + 1}`} checked={row.omitted} disabled={disabled} onChange={(event) => {
          // Omission resolves the whole header, including its repeated values.
          onChange(rows.map((candidate) => candidate.name.toLowerCase() === row.name.toLowerCase() ? { ...candidate, omitted: event.target.checked, ...(event.target.checked && candidate.sensitive ? { value: '' } : {}) } : candidate))
        }} />}</td>
        <td>{index < rows.length && <button type="button" aria-label={`Remove header ${index + 1}`} disabled={disabled} onClick={() => {
          onChange(rows.filter((candidate) => candidate.id !== row.id))
          requestAnimationFrame(() => table.current?.querySelectorAll<HTMLInputElement>('[data-header-name]')[index]?.focus())
        }}>−</button>}</td>
      </tr>)}</tbody>
    </table>
  </div>
}
