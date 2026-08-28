import { useState } from 'react'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../components/ActionError'
import { StatusMark } from '../components/Status'
import type { Environment } from '../api/contracts/environments'
import type { RuntimeStatus } from '../api/contracts/system'
import type { ResolvedTheme, ThemePreference } from '../theme'
import { MCPSettings } from './mcp/MCPSettings'

export type SettingsTab = 'appearance' | 'runtime' | 'mcp'

const choices: Array<{ value: ThemePreference; label: string; detail: string }> = [
  { value: 'system', label: 'System', detail: 'Follow this device' },
  { value: 'light', label: 'Light', detail: 'Cool, high-contrast surfaces' },
  { value: 'dark', label: 'Dark', detail: 'Low-light control plane' },
]

const settingsTabs: Array<{ value: SettingsTab; label: string }> = [
  { value: 'appearance', label: 'APPEARANCE' },
  { value: 'runtime', label: 'RUNTIME' },
  { value: 'mcp', label: 'MCP' },
]

export function SettingsPage({ tab, preference, resolvedTheme, runtime, environments, initialEnvironment, onNavigate, onPreferenceChange, onRuntimeChange, onRuntimeStart }: {
  tab: SettingsTab
  preference: ThemePreference
  resolvedTheme: ResolvedTheme
  runtime: RuntimeStatus | null
  environments: Environment[]
  initialEnvironment?: string
  onNavigate: (path: string) => void
  onPreferenceChange: (preference: ThemePreference) => void
  onRuntimeChange: (preference: RuntimeStatus['preference']) => Promise<void>
  onRuntimeStart: () => Promise<void>
}) {
  return <div className="page settings-page">
    <header className="settings-heading"><div><div className="eyebrow">PORTLESS</div><h1>Settings</h1></div></header>
    <nav className="settings-tabs" role="tablist" aria-label="Settings sections">
      {settingsTabs.map((item) => <button id={`settings-tab-${item.value}`} type="button" role="tab" aria-selected={tab === item.value} aria-controls={`settings-panel-${item.value}`} className={tab === item.value ? 'is-active' : ''} key={item.value} onClick={() => onNavigate(settingsPath(item.value))}>{item.label}</button>)}
    </nav>
    <div id={`settings-panel-${tab}`} role="tabpanel" aria-labelledby={`settings-tab-${tab}`}>
      {tab === 'appearance' && <AppearanceSettings preference={preference} resolvedTheme={resolvedTheme} onPreferenceChange={onPreferenceChange} />}
      {tab === 'runtime' && <RuntimeSettings runtime={runtime} onRuntimeChange={onRuntimeChange} onRuntimeStart={onRuntimeStart} />}
      {tab === 'mcp' && <MCPSettings environments={environments} initialEnvironment={initialEnvironment} />}
    </div>
  </div>
}

export function settingsPath(tab: SettingsTab, environment?: string): string {
  if (tab === 'appearance' && !environment) return '/settings'
  const query = new URLSearchParams({ tab })
  if (environment) query.set('env', environment)
  return `/settings?${query}`
}

function AppearanceSettings({ preference, resolvedTheme, onPreferenceChange }: {
  preference: ThemePreference
  resolvedTheme: ResolvedTheme
  onPreferenceChange: (preference: ThemePreference) => void
}) {
  return <section className="panel settings-panel">
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
}

function RuntimeSettings({ runtime, onRuntimeChange, onRuntimeStart }: {
  runtime: RuntimeStatus | null
  onRuntimeChange: (preference: RuntimeStatus['preference']) => Promise<void>
  onRuntimeStart: () => Promise<void>
}) {
  const [runtimeError, setRuntimeError] = useState<ActionErrorDetails | null>(null)
  const [changingRuntime, setChangingRuntime] = useState(false)

  const changeRuntime = async (nextPreference: RuntimeStatus['preference']) => {
    setChangingRuntime(true)
    setRuntimeError(null)
    try { await onRuntimeChange(nextPreference) }
    catch (error) { setRuntimeError(actionError('Could not change the container runtime', error)) }
    finally { setChangingRuntime(false) }
  }

  const startRuntime = async () => {
    setChangingRuntime(true)
    setRuntimeError(null)
    try { await onRuntimeStart() }
    catch (error) { setRuntimeError(actionError('Could not start the container runtime', error)) }
    finally { setChangingRuntime(false) }
  }

  return <section className="panel settings-panel settings-panel--runtime">
    <div className="panel-title"><span>CONTAINER RUNTIME</span><small>{runtime ? `preference: ${runtime.preference}` : 'status unavailable'}</small></div>
    <div className="settings-section settings-section--runtime">
      <div className="settings-copy"><div className="eyebrow">ENGINE</div><p>Choose the local container engine Portless uses for managed dependencies.</p></div>
      {runtime ? <div className="runtime-settings">
        <div className="runtime-summary"><StatusMark status={runtime.state} /><strong>{runtime.selected ?? 'none selected'}</strong><span>{runtime.version ? `v${runtime.version}` : runtime.reason}</span></div>
        <div className="runtime-candidates">{runtime.candidates.map((candidate) => <div className={candidate.name === runtime.selected ? 'runtime-candidate is-selected' : 'runtime-candidate'} key={candidate.name}>
          <div><StatusMark status={candidate.state} label={false} /><strong>{candidate.name}</strong><small>{candidate.version ? `v${candidate.version}` : candidate.state}</small></div>
          <p>{candidate.reason || (candidate.state === 'ready' ? 'Engine is available.' : 'Engine is unavailable.')}</p>
          <button className="button button--small" disabled={changingRuntime || runtime.preference === candidate.name} onClick={() => void changeRuntime(candidate.name)}>USE {candidate.name.toUpperCase()}</button>
        </div>)}</div>
        <div className="runtime-actions">
          <button className="button button--small" disabled={changingRuntime || runtime.preference === 'auto'} onClick={() => void changeRuntime('auto')}>USE AUTOMATIC SELECTION</button>
          {runtime.state !== 'ready' && <button className="button button--small button--primary" disabled={changingRuntime} onClick={() => void startRuntime()}>START RUNTIME</button>}
        </div>
        {runtimeError && <ActionErrorNotice error={runtimeError} onDismiss={() => setRuntimeError(null)} />}
      </div> : <div className="runtime-unavailable"><StatusMark status="unknown" /><p>Runtime status is unavailable while the daemon reconnects.</p></div>}
    </div>
  </section>
}

function ThemePreview({ theme }: { theme: ThemePreference }) {
  return <span className={`theme-preview theme-preview--${theme}`} aria-hidden="true"><i /><b><i /><i /><i /></b></span>
}
