import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { SettingsPage } from './SettingsPage'

describe('settings page', () => {
  it('offers system, light, and dark browser themes', () => {
    const markup = renderToStaticMarkup(<SettingsPage preference="light" resolvedTheme="light" onPreferenceChange={() => undefined} />)

    expect(markup).toContain('<h1>Settings</h1>')
    expect(markup).toContain('role="radiogroup" aria-label="Theme"')
    expect(markup).toContain('role="radio" aria-checked="true" class="theme-choice is-selected"')
    expect(markup).toContain('theme-preview--system')
    expect(markup).toContain('theme-preview--light')
    expect(markup).toContain('theme-preview--dark')
    expect(markup).toContain('light theme active')
  })
})
