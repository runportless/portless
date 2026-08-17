export type ThemePreference = 'system' | 'light' | 'dark'
export type ResolvedTheme = Exclude<ThemePreference, 'system'>

export const themeStorageKey = 'portless.theme'

export function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'system' || value === 'light' || value === 'dark'
}

export function readThemePreference(storage?: Pick<Storage, 'getItem'>): ThemePreference {
  try {
    const value = (storage ?? window.localStorage).getItem(themeStorageKey)
    return isThemePreference(value) ? value : 'system'
  } catch {
    return 'system'
  }
}

export function writeThemePreference(preference: ThemePreference, storage?: Pick<Storage, 'setItem'>) {
  try { (storage ?? window.localStorage).setItem(themeStorageKey, preference) }
  catch { /* The selected theme still applies for this page when storage is unavailable. */ }
}

export function resolveTheme(preference: ThemePreference, prefersDark?: boolean): ResolvedTheme {
  if (preference !== 'system') return preference
  const dark = prefersDark ?? window.matchMedia('(prefers-color-scheme: dark)').matches
  return dark ? 'dark' : 'light'
}

export function applyTheme(theme: ResolvedTheme) {
  document.documentElement.dataset.theme = theme
  document.documentElement.style.colorScheme = theme
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'light' ? '#f4f7f7' : '#071012')
}
