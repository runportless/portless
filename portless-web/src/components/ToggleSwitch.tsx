export function ToggleSwitch({ label, checked, disabled = false, pending = false, title, onChange }: {
  label: string
  checked: boolean
  disabled?: boolean
  pending?: boolean
  title?: string
  onChange: (checked: boolean) => void
}) {
  const state = pending ? 'Updating' : checked ? 'On' : 'Off'
  return <label className={`toggle-switch${pending ? ' is-pending' : disabled ? ' is-disabled' : ''}`} title={title || `${label}: ${state.toLowerCase()}`} onClick={(event) => event.stopPropagation()}>
    <input type="checkbox" role="switch" checked={checked} disabled={disabled || pending} aria-label={label} aria-busy={pending} onChange={(event) => onChange(event.target.checked)} />
    <span className="toggle-switch__track" aria-hidden="true"><span className="toggle-switch__thumb" /></span>
    <span className="sr-only">{state}</span>
  </label>
}
