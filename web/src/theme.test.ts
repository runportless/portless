import { describe, expect, it } from 'vitest'
import { isThemePreference, readThemePreference, resolveTheme, themeStorageKey, writeThemePreference } from './theme'

describe('theme preference', () => {
  it('accepts only supported preferences and falls back to system', () => {
    expect(isThemePreference('system')).toBe(true)
    expect(isThemePreference('light')).toBe(true)
    expect(isThemePreference('dark')).toBe(true)
    expect(isThemePreference('neon')).toBe(false)
    expect(readThemePreference({ getItem: () => 'neon' })).toBe('system')
    expect(readThemePreference({ getItem: () => null })).toBe('system')
  })

  it('resolves system while explicit choices remain fixed', () => {
    expect(resolveTheme('system', true)).toBe('dark')
    expect(resolveTheme('system', false)).toBe('light')
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
  })

  it('persists the browser-local selection', () => {
    const saved = new Map<string, string>()
    writeThemePreference('light', { setItem: (key, value) => saved.set(key, value) })
    expect(saved.get(themeStorageKey)).toBe('light')
    expect(readThemePreference({ getItem: (key) => saved.get(key) ?? null })).toBe('light')
  })

  it('continues with system mode when browser storage is unavailable', () => {
    expect(readThemePreference({ getItem: () => { throw new Error('blocked') } })).toBe('system')
    expect(() => writeThemePreference('dark', { setItem: () => { throw new Error('blocked') } })).not.toThrow()
  })
})
