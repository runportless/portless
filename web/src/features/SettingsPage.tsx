import type { ResolvedTheme, ThemePreference } from '../theme'

const choices: Array<{ value: ThemePreference; label: string; detail: string }> = [
  { value: 'system', label: 'System', detail: 'Follow this device' },
  { value: 'light', label: 'Light', detail: 'Cool, high-contrast surfaces' },
  { value: 'dark', label: 'Dark', detail: 'Low-light control plane' },
]

export function SettingsPage({ preference, resolvedTheme, onPreferenceChange }: {
  preference: ThemePreference
  resolvedTheme: ResolvedTheme
  onPreferenceChange: (preference: ThemePreference) => void
}) {
  return <div className="page settings-page">
    <header className="settings-heading"><div><div className="eyebrow">PORTLESS</div><h1>Settings</h1></div></header>
    <section className="panel settings-panel">
      <div className="panel-title"><span>APPEARANCE</span><small>{resolvedTheme} theme active</small></div>
      <div className="settings-section">
        <div className="settings-copy"><div className="eyebrow">THEME</div><p>Choose how Portless looks in this browser.</p></div>
        <div className="theme-choices" role="radiogroup" aria-label="Theme">
          {choices.map((choice) => <button type="button" role="radio" aria-checked={preference === choice.value} className={preference === choice.value ? 'theme-choice is-selected' : 'theme-choice'} key={choice.value} onClick={() => onPreferenceChange(choice.value)}>
            <ThemePreview theme={choice.value} />
            <span className="theme-choice__copy"><strong>{choice.label}</strong><small>{choice.detail}</small></span>
            <span className="theme-choice__mark" aria-hidden="true">{preference === choice.value ? '●' : ''}</span>
          </button>)}
        </div>
      </div>
    </section>
  </div>
}

function ThemePreview({ theme }: { theme: ThemePreference }) {
  return <span className={`theme-preview theme-preview--${theme}`} aria-hidden="true"><i /><b><i /><i /><i /></b></span>
}
