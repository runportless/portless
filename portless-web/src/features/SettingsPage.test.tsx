import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { RuntimeStatus } from '../types'
import { SettingsPage } from './SettingsPage'

const runtime: RuntimeStatus = {
  preference: 'auto',
  selected: 'docker',
  state: 'ready',
  version: '29.4.0',
  candidates: [
    { name: 'podman', state: 'missing', reason: 'Podman is not installed.' },
    { name: 'docker', state: 'ready', version: '29.4.0' },
  ],
}

describe('settings page', () => {
  it('offers system, light, and dark browser themes', () => {
    const markup = renderSettings()

    expect(markup).toContain('<h1>Settings</h1>')
    expect(markup).toContain('role="radiogroup" aria-label="Theme"')
    expect(markup).toContain('role="radio" aria-checked="true" class="theme-choice is-selected"')
    expect(markup).toContain('theme-preview--system')
    expect(markup).toContain('theme-preview--light')
    expect(markup).toContain('theme-preview--dark')
    expect(markup).toContain('light theme active')
  })

  it('contains the container runtime configuration', () => {
    const markup = renderSettings()

    expect(markup).toContain('CONTAINER RUNTIME')
    expect(markup).toContain('preference: auto')
    expect(markup).toContain('USE DOCKER')
    expect(markup).toContain('USE PODMAN')
    expect(markup).toContain('USE AUTOMATIC SELECTION')
  })
})

function renderSettings() {
  return renderToStaticMarkup(<SettingsPage preference="light" resolvedTheme="light" runtime={runtime} onPreferenceChange={() => undefined} onRuntimeChange={async () => undefined} onRuntimeStart={async () => undefined} />)
}
