import { useId, useRef } from 'react'
import { MockNameValueEditor, type MockNameValueDraft } from './MockNameValueEditor'

export function MockNameValueSection({ rows, label, open, disabled, onOpenChange, onChange }: {
  rows: MockNameValueDraft[]
  label: 'Query parameters' | 'Preview query parameters' | 'Response headers'
  open: boolean
  disabled: boolean
  onOpenChange: (open: boolean) => void
  onChange: (rows: MockNameValueDraft[]) => void
}) {
  const id = useId()
  const content = useRef<HTMLDivElement>(null)
  const headers = label === 'Response headers'
  const matching = label === 'Query parameters'
  const count = rows.filter(({ name, value }) => name.trim() || value.trim()).length
  const add = () => {
    onOpenChange(true)
    window.requestAnimationFrame(() => {
      const inputs = content.current?.querySelectorAll<HTMLInputElement>('input[data-entry-name]')
      inputs?.[inputs.length - 1]?.focus()
    })
  }

  return <section className="mock-name-value-section">
    <div className="mock-name-value-section__heading">
      <button className="mock-section-toggle" type="button" aria-label={label} aria-expanded={open} aria-controls={id} aria-describedby={`${id}-count`} onClick={() => onOpenChange(!open)}>
        <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 4 4 4-4 4" /></svg><span>{headers ? 'RESPONSE HEADERS' : 'QUERY PARAMETERS'}</span><span id={`${id}-count`} className="mock-name-value-section__count">{count}</span>
      </button>
      <button className="mock-name-value-section__add" type="button" aria-label={headers ? 'Add response header' : matching ? 'Add query parameter' : 'Add preview query parameter'} title={headers ? 'Add header' : 'Add parameter'} disabled={disabled} onClick={add}>+</button>
    </div>
    <div ref={content} id={id} hidden={!open}>
      <MockNameValueEditor rows={rows} label={label} rowLabel={headers ? 'Header' : 'Query parameter'} hideCaption matching={matching} disabled={disabled} onChange={onChange} />
    </div>
  </section>
}
