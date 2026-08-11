import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, APIError, jsonBody, projectPath, setCSRF } from './api'
import { AppChrome, type Command } from './components/Chrome'
import { ProjectPage } from './features/ProjectPage'
import { ProjectsPage } from './features/ProjectsPage'
import type { Operation, Project, RuntimeStatus } from './types'

interface Session { actor: string; browser: boolean; csrf: string }

export function App() {
  const [session, setSession] = useState<Session | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimeStatus | null>(null)
  const [route, setRoute] = useState(() => `${location.pathname}${location.search}`)
  const [loading, setLoading] = useState(true)
  const [authRequired, setAuthRequired] = useState(false)
  const [live, setLive] = useState(true)

  const refresh = useCallback(async () => {
    try {
      const [response, runtime] = await Promise.all([
        api<{ projects: Project[] }>('/projects'),
        api<RuntimeStatus>('/runtime'),
      ])
      setProjects(response.projects)
      setRuntimeStatus(runtime)
      setLive(true)
    } catch (error) {
      if (error instanceof APIError && error.status === 401) setAuthRequired(true)
      setLive(false)
    }
  }, [])

  useEffect(() => {
    const initialize = async () => {
      try {
        const value = await api<Session>('/session')
        setSession(value); setCSRF(value.csrf)
        await refresh()
      } catch (error) {
        if (error instanceof APIError && error.status === 401) setAuthRequired(true)
      } finally { setLoading(false) }
    }
    initialize()
    const popstate = () => setRoute(`${location.pathname}${location.search}`)
    window.addEventListener('popstate', popstate)
    return () => window.removeEventListener('popstate', popstate)
  }, [refresh])

  useEffect(() => {
    if (!session) return
    const timer = window.setInterval(refresh, 2500)
    return () => window.clearInterval(timer)
  }, [refresh, session])

  const navigate = useCallback((path: string) => {
    if (`${location.pathname}${location.search}` !== path) history.pushState({}, '', path)
    setRoute(path)
    window.scrollTo({ top: 0, left: 0, behavior: 'auto' })
  }, [])

  const parsed = parseRoute(route)
  const activeProject = parsed.project ? projects.find((project) => project.name === parsed.project) : undefined
  const mutateProject = useCallback(async (action: 'up' | 'down') => {
    if (!activeProject) return
    await api<Operation>(projectPath(activeProject.name, `/${action}`), { method: 'POST', ...(action === 'down' ? jsonBody({ removeVolumes: false }) : {}) })
    await refresh()
  }, [activeProject, refresh])
  const changeRuntime = useCallback(async (preference: RuntimeStatus['preference']) => {
    const status = await api<RuntimeStatus>('/runtime', { method: 'PUT', ...jsonBody({ preference }) })
    setRuntimeStatus(status)
  }, [])
  const startRuntime = useCallback(async () => {
    const status = await api<RuntimeStatus>('/runtime/start', { method: 'POST' })
    setRuntimeStatus(status)
  }, [])
  const commands = useMemo<Command[]>(() => activeProject ? [
    { group: 'Actions', label: activeProject.status === 'healthy' ? 'Stop environment' : 'Start environment', detail: activeProject.name, run: () => void mutateProject(activeProject.status === 'healthy' ? 'down' : 'up') },
    { group: 'Views', label: 'Inspect live traffic', detail: activeProject.name, run: () => navigate(`/projects/${activeProject.name}?tab=traffic`) },
    { group: 'Views', label: 'Introduce a fault', detail: activeProject.name, run: () => navigate(`/projects/${activeProject.name}?tab=faults`) },
    { group: 'Views', label: 'Start a recording', detail: activeProject.name, run: () => navigate(`/projects/${activeProject.name}?tab=recordings`) },
  ] : [], [activeProject, mutateProject, navigate])

  if (loading) return <LoadingScreen />
  if (authRequired || !session) return <AuthenticationScreen />

  let content
  if (!parsed.project) {
    content = <ProjectsPage projects={projects} runtime={runtimeStatus} onNavigate={navigate} onRuntimeChange={changeRuntime} onRuntimeStart={startRuntime} />
  } else if (!activeProject) {
    content = <NotFound name={parsed.project} onNavigate={navigate} />
  } else {
    content = <ProjectPage project={activeProject} tab={parsed.tab} onNavigate={navigate} onChanged={refresh} />
  }
  return <AppChrome projects={projects} activeProject={activeProject} runtime={runtimeStatus} onNavigate={navigate} commands={commands} live={live}>{content}</AppChrome>
}

function parseRoute(route: string): { project?: string; tab: 'overview' | 'traffic' | 'recordings' | 'faults' | 'timeline' } {
  const url = new URL(route, location.origin)
  const parts = url.pathname.split('/').filter(Boolean)
  const project = parts[0] === 'projects' && parts[1] ? decodeURIComponent(parts[1]) : undefined
  const requested = url.searchParams.get('tab')
  const tab = ['traffic', 'recordings', 'faults', 'timeline'].includes(requested || '') ? requested as 'traffic' | 'recordings' | 'faults' | 'timeline' : 'overview'
  return { project, tab }
}

function LoadingScreen() {
  return <div className="splash"><div className="brand brand--large"><span className="brand__signal"><i /><i /><i /></span><span>portless</span></div><p>Connecting to the local control plane…</p></div>
}

function AuthenticationScreen() {
  return <div className="auth-screen"><div className="auth-card panel"><div className="brand brand--large"><span className="brand__signal"><i /><i /><i /></span><span>portless</span></div><div className="eyebrow warning-text">Browser session required</div><h1>Open this control plane from the CLI.</h1><p>Portless does not let arbitrary websites call a powerful localhost API. The CLI creates a short-lived, single-use browser claim and exchanges it for a private session.</p><pre><span>$</span> portless ui</pre><p className="muted">If you arrived here after a session expired, run the command again. Your projects continue running.</p></div></div>
}

function NotFound({ name, onNavigate }: { name: string; onNavigate: (path: string) => void }) {
  return <div className="page"><section className="panel not-found"><div className="eyebrow">PROJECT NOT FOUND</div><h1>{name}</h1><p>No local project has this name. Names are assigned per machine and can be changed while an environment is stopped.</p><button className="button" onClick={() => onNavigate('/projects')}>ALL PROJECTS</button></section></div>
}
