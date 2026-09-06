import { useId } from 'react'

export function ReplayTabs<T extends string>({ label, value, options, onChange, panelID, className = 'replay-tabs' }: {
  label: string
  value: T
  options: Array<{ value: T; label: string }>
  onChange: (value: T) => void
  panelID: string
  className?: string
}) {
  const id = useId()
  return <div className={className} role="tablist" aria-label={label}>
    {options.map((option, index) => <button key={option.value} id={`${id}-${option.value}`} className={option.value === value ? 'is-active' : ''} role="tab" type="button" aria-selected={option.value === value} aria-controls={panelID} tabIndex={option.value === value ? 0 : -1} onClick={() => onChange(option.value)} onKeyDown={(event) => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
      event.preventDefault()
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + options.length) % options.length
      onChange(options[next].value)
      document.getElementById(`${id}-${options[next].value}`)?.focus()
    }}>{option.label}</button>)}
  </div>
}
