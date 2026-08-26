import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, APIError, environmentPath, jsonBody, setCSRF } from './api'
import { AppChrome, type Command, type EnvironmentView } from './components/Chrome'
import { EnvironmentPage } from './features/ProjectPage'
import { ProjectsPage } from './features/ProjectsPage'
import { SettingsPage, type SettingsTab } from './features/SettingsPage'
import { applyTheme, readThemePreference, resolveTheme, writeThemePreference, type ResolvedTheme, type ThemePreference } from './theme'
import type { DaemonHandoffStatus, DaemonRestart, DaemonStatus, Environment, Operation, Project, RelayStatus, RuntimeStatus } from './types'

interface Session { actor: string; browser: boolean; csrf: string }
type Tab = EnvironmentView

export function App() {
  const [session, setSession] = useState<Session | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [environments, setEnvironments] = useState<Environment[]>([])
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimeStatus | null>(null)
  const [daemonStatus, setDaemonStatus] = useState<DaemonStatus | null>(null)
  const [relayStatus, setRelayStatus] = useState<RelayStatus | null>(null)
  const [route, setRoute] = useState(() => `${location.pathname}${location.search}`)
  const [loading, setLoading] = useState(true)
  const [authRequired, setAuthRequired] = useState(false)
  const [live, setLive] = useState(true)
  const [themePreference, setThemePreference] = useState<ThemePreference>(readThemePreference)
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() => resolveTheme(readThemePreference()))

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const synchronize = () => {
      const resolved = resolveTheme(themePreference, media.matches)
      setResolvedTheme(resolved)
      applyTheme(resolved)
    }
    synchronize()
    if (themePreference !== 'system') return
    media.addEventListener('change', synchronize)
    return () => media.removeEventListener('change', synchronize)
  }, [themePreference])

  const changeThemePreference = useCallback((preference: ThemePreference) => {
    writeThemePreference(preference)
    setThemePreference(preference)
  }, [])

  const refreshCore = useCallback(async () => {
    const [projectResponse, environmentResponse] = await Promise.all([
      api<{ projects: Project[] }>('/projects'),
      api<{ environments: Environment[] }>('/environments'),
    ])
    setProjects(projectResponse.projects)
    setEnvironments(environmentResponse.environments)
  }, [])

  const refreshDaemon = useCallback(async () => {
    const status = await api<DaemonStatus>('/daemon')
    setDaemonStatus(status)
    return status
  }, [])

  const refreshRuntime = useCallback(async () => {
    setRuntimeStatus(await api<RuntimeStatus>('/runtime').catch(() => null))
  }, [])

  const refresh = useCallback(async () => {
    try {
      await Promise.all([refreshCore(), refreshDaemon()])
      setLive(true)
    } catch (error) {
      if (error instanceof APIError && error.status === 401) setAuthRequired(true)
      setLive(false)
    }
  }, [refreshCore, refreshDaemon])

  const refreshRelay = useCallback(async () => {
    setRelayStatus(await api<RelayStatus>('/relay').catch(() => null))
  }, [])

  useEffect(() => {
    const initialize = async () => {
      try {
        const value = await api<Session>('/session')
        setSession(value); setCSRF(value.csrf)
        await refreshCore()
        setLive(true)
        void refreshDaemon().catch(() => setLive(false))
        void refreshRuntime()
        void refreshRelay()
      } catch (error) {
        if (error instanceof APIError && error.status === 401) setAuthRequired(true)
      } finally { setLoading(false) }
    }
    initialize()
    const popstate = () => setRoute(`${location.pathname}${location.search}`)
    window.addEventListener('popstate', popstate)
    return () => window.removeEventListener('popstate', popstate)
  }, [refreshCore, refreshDaemon, refreshRelay, refreshRuntime])

  useEffect(() => {
    if (!session) return
    const timer = window.setInterval(refresh, 2500)
    return () => window.clearInterval(timer)
  }, [refresh, session])

  useEffect(() => {
    if (!session) return
    const timer = window.setInterval(() => { void refreshRuntime(); void refreshRelay() }, 15000)
    return () => window.clearInterval(timer)
  }, [refreshRelay, refreshRuntime, session])

  const navigate = useCallback((path: string) => {
    if (`${location.pathname}${location.search}` !== path) history.pushState({}, '', path)
    setRoute(path)
    window.scrollTo({ top: 0, left: 0, behavior: 'auto' })
  }, [])

  const parsed = parseRoute(route)
  const activeProject = parsed.project ? projects.find((project) => project.name === parsed.project) : undefined
  const activeEnvironment = parsed.project && parsed.environment
    ? environments.find((environment) => environment.project === parsed.project && environment.name === parsed.environment)
    : undefined
  const mutateEnvironment = useCallback(async (action: 'up' | 'down') => {
    if (!activeEnvironment) return
    await api<Operation>(environmentPath(activeEnvironment, `/${action}`), { method: 'POST', ...(action === 'down' ? jsonBody({ removeVolumes: false }) : {}) })
    await refresh()
  }, [activeEnvironment, refresh])
  const changeRuntime = useCallback(async (preference: RuntimeStatus['preference']) => {
    const status = await api<RuntimeStatus>('/runtime', { method: 'PUT', ...jsonBody({ preference }) })
    setRuntimeStatus(status)
  }, [])
  const startRuntime = useCallback(async () => {
    const status = await api<RuntimeStatus>('/runtime/start', { method: 'POST' })
    setRuntimeStatus(status)
  }, [])
  const restartDaemon = useCallback(async (instanceId: string) => {
    return api<DaemonRestart>('/daemon/restart', { method: 'POST', ...jsonBody({ instanceId }) })
  }, [])
  const verifyDaemonHandoff = useCallback(async () => api<DaemonHandoffStatus>('/daemon/handoff'), [])
  const commands = useMemo<Command[]>(() => activeEnvironment ? [
    ...(activeEnvironment.status === 'recovering' ? [] : [{ group: 'Actions', label: activeEnvironment.status === 'stopped' ? 'Start environment' : 'Stop environment', detail: `${activeEnvironment.project}/${activeEnvironment.name}`, run: () => void mutateEnvironment(activeEnvironment.status === 'stopped' ? 'up' : 'down') } as Command]),
    { group: 'Views', label: 'Open topology', detail: activeEnvironment.name, run: () => navigate(environmentUIPath(activeEnvironment, 'topology')) },
    { group: 'Views', label: 'Configure providers', detail: activeEnvironment.name, run: () => navigate(environmentUIPath(activeEnvironment, 'bindings')) },
    { group: 'Views', label: 'Inspect live traffic', detail: activeEnvironment.name, run: () => navigate(environmentUIPath(activeEnvironment, 'traffic')) },
    { group: 'Views', label: 'Manage mock responses', detail: activeEnvironment.name, run: () => navigate(environmentUIPath(activeEnvironment, 'mocks')) },
    { group: 'Views', label: 'Introduce a fault', detail: activeEnvironment.name, run: () => navigate(environmentUIPath(activeEnvironment, 'faults')) },
    { group: 'Views', label: 'Start a recording', detail: activeEnvironment.name, run: () => navigate(environmentUIPath(activeEnvironment, 'recordings')) },
  ] : [], [activeEnvironment, mutateEnvironment, navigate])

  if (loading) return <LoadingScreen />
  if (authRequired || !session) return <AuthenticationScreen />

  let content
  if (parsed.settings) {
    content = <SettingsPage tab={parsed.settingsTab} preference={themePreference} resolvedTheme={resolvedTheme} runtime={runtimeStatus} environments={environments} initialEnvironment={parsed.settingsEnvironment} onNavigate={navigate} onPreferenceChange={changeThemePreference} onRuntimeChange={changeRuntime} onRuntimeStart={startRuntime} />
  } else if (parsed.environment) {
    content = activeEnvironment
      ? <EnvironmentPage key={environmentSessionKey(activeEnvironment, daemonStatus)} environment={activeEnvironment} project={activeProject} tab={parsed.tab} mockProfile={parsed.mockProfile} onNavigate={navigate} onChanged={refresh} />
      : <NotFound kind="environment" name={`${parsed.project}/${parsed.environment}`} onNavigate={navigate} />
  } else if (parsed.project && !activeProject) {
    content = <NotFound kind="project" name={parsed.project} onNavigate={navigate} />
  } else {
    content = <ProjectsPage projects={projects} environments={environments} selectedProject={activeProject} onNavigate={navigate} onChanged={refresh} />
  }
  return <AppChrome projects={projects} environments={environments} activeProject={activeProject} activeEnvironment={activeEnvironment} activeView={parsed.tab} settingsActive={parsed.settings} settingsView={parsed.settingsTab} runtime={runtimeStatus} daemon={daemonStatus} relay={relayStatus} onNavigate={navigate} commands={commands} live={live} onDaemonRefresh={refreshDaemon} onDaemonHandoffVerify={verifyDaemonHandoff} onDaemonRestart={restartDaemon} onDaemonReconnected={refresh}>{content}</AppChrome>
}

export function environmentSessionKey(environment: Pick<Environment, 'project' | 'name'>, daemon: Pick<DaemonStatus, 'instanceId'> | null) {
  return `${environment.project}/${environment.name}@${daemon?.instanceId || 'daemon-pending'}`
}

export function parseRoute(route: string): { project?: string; environment?: string; settings: boolean; settingsTab: SettingsTab; settingsEnvironment?: string; mockProfile?: string; tab: Tab } {
  const current = new URL(route, 'http://portless.localhost')
  const parts = current.pathname.split('/').filter(Boolean).map(decodeURIComponent)
  let project: string | undefined
  let environment: string | undefined
  if (parts[0] === 'projects' && parts[1]) project = parts[1]
  if (parts[0] === 'environments' && parts[1] && parts[2]) { project = parts[1]; environment = parts[2] }
  const settings = parts[0] === 'settings'
  const requested = current.searchParams.get('tab')
  const tabs: Tab[] = ['overview', 'topology', 'traffic', 'mocks', 'recordings', 'faults', 'bindings', 'timeline']
  const tab = tabs.includes(requested as Tab) ? requested as Tab : 'overview'
  const settingsTabs: SettingsTab[] = ['appearance', 'runtime', 'mcp']
  const settingsTab = settings && settingsTabs.includes(requested as SettingsTab) ? requested as SettingsTab : 'appearance'
  const requestedEnvironment = settings ? current.searchParams.get('env')?.trim() : undefined
  const requestedMockProfile = tab === 'mocks' ? current.searchParams.get('profile')?.trim() : undefined
  return { project, environment, settings, settingsTab, ...(requestedEnvironment ? { settingsEnvironment: requestedEnvironment } : {}), ...(requestedMockProfile ? { mockProfile: requestedMockProfile } : {}), tab }
}

function environmentUIPath(environment: Pick<Environment, 'project' | 'name'>, tab: Tab) {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  return tab === 'overview' ? base : `${base}?tab=${tab}`
}

export function LoadingScreen() {
  return <div className="splash"><div className="splash__content" role="status" aria-live="polite"><div className="brand brand--large"><span className="brand__signal"><i /><i /><i /></span><span>portless</span></div><div className="splash__spinner" aria-hidden="true" /><p>Connecting to the local control plane…</p></div></div>
}

function AuthenticationScreen() {
  return <div className="auth-screen"><div className="auth-card panel"><div className="brand brand--large"><span className="brand__signal"><i /><i /><i /></span><span>portless</span></div><div className="eyebrow warning-text">Browser session required</div><h1>Open this control plane from the CLI.</h1><p>The CLI creates a short-lived, single-use browser claim and exchanges it for a private local session.</p><pre><span>$</span> portless ui</pre><p className="muted">If the session expired, run the command again. Your environments continue running.</p></div></div>
}

function NotFound({ kind, name, onNavigate }: { kind: string; name: string; onNavigate: (path: string) => void }) {
  return <div className="page"><section className="panel not-found"><div className="eyebrow">{kind.toUpperCase()} NOT FOUND</div><h1>{name}</h1><p>No local {kind} has this name.</p><button className="button" onClick={() => onNavigate('/projects')}>ALL PROJECTS</button></section></div>
}
