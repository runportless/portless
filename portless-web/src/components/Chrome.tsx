import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { ControlPlaneHealth, DaemonDiagnostics, DaemonHandoffStatus, DaemonRestart, DaemonStatus, Environment, Project, RelayStatus, RuntimeStatus } from '../types'
import { DaemonDrawer } from './DaemonDrawer'
import { StatusMark } from './Status'

export interface Command { label: string; detail?: string; group: string; run: () => void }
export type EnvironmentView = 'overview' | 'topology' | 'traffic' | 'mocks' | 'recordings' | 'faults' | 'bindings' | 'timeline'
export type SettingsView = 'appearance' | 'runtime' | 'mcp'

const expandedProjectsKey = 'portless.expanded-projects'

export function AppChrome({ projects, environments, activeProject, activeEnvironment, activeView, settingsActive = false, settingsView = 'appearance', runtime, daemon, diagnostics, controlPlaneHealth, relay, children, onNavigate, commands, live = true, onDaemonRefresh, onDaemonDiagnosticsRefresh, onDaemonHandoffVerify, onDaemonRestart, onDaemonReconnected }: {
  projects: Project[]
  environments: Environment[]
  activeProject?: Project
  activeEnvironment?: Environment
  activeView: EnvironmentView
  settingsActive?: boolean
  settingsView?: SettingsView
  runtime?: RuntimeStatus | null
  daemon: DaemonStatus | null
  diagnostics: DaemonDiagnostics | null
  controlPlaneHealth: ControlPlaneHealth
  relay?: RelayStatus | null
  children: ReactNode
  onNavigate: (path: string) => void
  commands: Command[]
  live?: boolean
  onDaemonRefresh: () => Promise<DaemonStatus>
  onDaemonDiagnosticsRefresh: (includeStorage?: boolean) => Promise<DaemonDiagnostics>
  onDaemonHandoffVerify: () => Promise<DaemonHandoffStatus>
  onDaemonRestart: (instanceId: string) => Promise<DaemonRestart>
  onDaemonReconnected: () => Promise<void>
}) {
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [daemonOpen, setDaemonOpen] = useState(false)
  const [expandedProjects, setExpandedProjects] = useState<Set<string>>(readExpandedProjects)
  const scopedProject = activeEnvironment?.project ?? activeProject?.name

  useEffect(() => {
    if (!scopedProject) return
    setExpandedProjects((current) => {
      if (current.has(scopedProject)) return current
      return new Set([...current, scopedProject])
    })
  }, [scopedProject])

  useEffect(() => {
    try { window.sessionStorage.setItem(expandedProjectsKey, JSON.stringify([...expandedProjects])) }
    catch { /* Disclosure state can remain in memory when storage is unavailable. */ }
  }, [expandedProjects])

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault(); setPaletteOpen((value) => !value)
      }
      if (event.key === 'Escape') setPaletteOpen(false)
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [])
  const allCommands = useMemo<Command[]>(() => [
    ...projects.map((project) => ({ group: 'Projects', label: project.name, detail: `${project.environments?.length || 0} environments`, run: () => onNavigate(`/projects/${encodeURIComponent(project.name)}`) })),
    ...environments.map((environment) => ({ group: 'Environments', label: `${environment.project}/${environment.name}`, detail: environment.status, run: () => onNavigate(environmentRoute(environment)) })),
    ...commands,
    { group: 'System', label: 'Configure MCP', detail: activeEnvironment ? `${activeEnvironment.project}/${activeEnvironment.name}` : 'AI client access', run: () => onNavigate(mcpSettingsRoute(activeEnvironment)) },
    { group: 'System', label: 'Settings', detail: 'Appearance, runtime, and MCP', run: () => onNavigate('/settings') },
  ], [activeEnvironment, commands, environments, onNavigate, projects])

  const expandProject = (name: string) => {
    setExpandedProjects((current) => current.has(name) ? current : new Set([...current, name]))
  }

  const toggleProject = (name: string) => {
    setExpandedProjects((current) => {
      const next = new Set(current)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const inspectDaemon = () => {
    setPaletteOpen(false)
    setDaemonOpen(true)
    void onDaemonRefresh().catch(() => undefined)
    void onDaemonDiagnosticsRefresh(false).catch(() => undefined)
  }

  const daemonStateLabel = live ? daemon?.state ?? 'connected' : 'reconnecting'
  const daemonLabel = `daemon ${daemonStateLabel}`

  return <div className="shell">
    <aside className="sidebar">
      <button className="brand" onClick={() => onNavigate('/projects')} aria-label="Portless projects"><span className="brand__signal"><i /><i /><i /></span><span>portless</span></button>
      <div className="sidebar__body">
        <div className="sidebar__section-label">Projects</div>
        <nav className="project-nav" aria-label="Projects">
          {projects.map((project) => {
            const expanded = expandedProjects.has(project.name)
            const selected = activeProject?.name === project.name && !activeEnvironment
            const projectEnvironments = environments.filter((item) => item.project === project.name)
            return <div className="project-nav__branch" key={project.name}>
              <div className={selected ? 'project-nav__row is-active' : 'project-nav__row'}>
                <button className="project-nav__disclosure" type="button" aria-label={`${expanded ? 'Collapse' : 'Expand'} ${project.name}`} aria-expanded={expanded} onClick={() => toggleProject(project.name)}><ChevronIcon expanded={expanded} /></button>
                <button className="project-nav__project-link" type="button" aria-current={selected ? 'page' : undefined} onClick={() => { expandProject(project.name); onNavigate(`/projects/${encodeURIComponent(project.name)}`) }}><span>{project.name}</span><small>{projectEnvironments.length} env</small></button>
              </div>
              {expanded && <div className="project-nav__children">{projectEnvironments.map((environment) => {
                const environmentSelected = activeEnvironment?.project === environment.project && activeEnvironment.name === environment.name
                return <button key={environment.name} className={environmentSelected ? 'project-nav__item project-nav__item--child is-active' : 'project-nav__item project-nav__item--child'} aria-current={environmentSelected ? 'page' : undefined} onClick={() => onNavigate(environmentRoute(environment))}>
                  <StatusMark status={environment.status} label={false} /><span>{environment.name}</span><small>{environment.status}</small>
                </button>
              })}</div>}
            </div>
          })}
          {projects.length === 0 && <div className="sidebar__empty">Run <code>portless up</code> or create a multi-source project.</div>}
        </nav>
        {activeEnvironment && <>
          <div className="sidebar__section-label sidebar__section-label--context"><span>Environment</span><small title={`${activeEnvironment.project}/${activeEnvironment.name}`}>{activeEnvironment.project}/{activeEnvironment.name}</small></div>
          <nav className="view-nav" aria-label={`${activeEnvironment.project}/${activeEnvironment.name} views`}>
            <ViewButton label="Overview" view="overview" activeView={activeView} environment={activeEnvironment} icon={<GridIcon />} onNavigate={onNavigate} />
            <ViewButton label="Topology" view="topology" activeView={activeView} environment={activeEnvironment} icon={<TopologyIcon />} onNavigate={onNavigate} />
            <ViewButton label="Traffic" view="traffic" activeView={activeView} environment={activeEnvironment} icon={<PulseIcon />} onNavigate={onNavigate} />
            <ViewButton label="Mocks" view="mocks" activeView={activeView} environment={activeEnvironment} icon={<MockIcon />} onNavigate={onNavigate} />
            <ViewButton label="Recordings" view="recordings" activeView={activeView} environment={activeEnvironment} icon={<RecordIcon />} onNavigate={onNavigate} />
            <ViewButton label="Faults" view="faults" activeView={activeView} environment={activeEnvironment} icon={<FaultIcon />} onNavigate={onNavigate} />
            <ViewButton label="Bindings" view="bindings" activeView={activeView} environment={activeEnvironment} icon={<LinkIcon />} onNavigate={onNavigate} />
            <ViewButton label="Timeline" view="timeline" activeView={activeView} environment={activeEnvironment} icon={<TimelineIcon />} onNavigate={onNavigate} />
          </nav>
        </>}
      </div>
      <nav className="sidebar__utility" aria-label="Application">
        <button type="button" className={settingsActive ? 'is-active' : ''} aria-current={settingsActive ? 'page' : undefined} onClick={() => onNavigate('/settings')}><SettingsIcon /><span>Settings</span></button>
      </nav>
      <button className="sidebar__footer" type="button" aria-expanded={daemonOpen} onClick={inspectDaemon}><span className={live ? 'live-dot' : 'live-dot live-dot--off'} /><span className={live ? undefined : 'daemon-state--reconnecting'}>{daemonStateLabel}</span><small>DETAILS ›</small></button>
    </aside>
    <div className="stage">
      <header className="topbar"><TopbarBreadcrumbs settingsActive={settingsActive} settingsView={settingsView} activeProject={activeProject} activeEnvironment={activeEnvironment} onNavigate={onNavigate} /><div className="topbar__tools"><button className="topbar__daemon" type="button" aria-label={daemonLabel} onClick={inspectDaemon}><span className={live ? 'live-dot' : 'live-dot live-dot--off'} /></button><button className="key-button" onClick={() => setPaletteOpen(true)}><span>⌘</span><span>K</span><em>jump or run</em></button></div></header>
      <main>{children}</main>
    </div>
    {paletteOpen && <CommandPalette commands={allCommands} onClose={() => setPaletteOpen(false)} />}
    {daemonOpen && <DaemonDrawer status={daemon} diagnostics={diagnostics} controlPlaneHealth={controlPlaneHealth} runtime={runtime ?? null} relay={relay ?? null} live={live} onClose={() => setDaemonOpen(false)} onRefresh={onDaemonRefresh} onRefreshDiagnostics={onDaemonDiagnosticsRefresh} onVerifyHandoff={onDaemonHandoffVerify} onRestart={onDaemonRestart} onReconnected={onDaemonReconnected} />}
  </div>
}

function TopbarBreadcrumbs({ settingsActive, settingsView, activeProject, activeEnvironment, onNavigate }: {
  settingsActive: boolean
  settingsView: SettingsView
  activeProject?: Project
  activeEnvironment?: Environment
  onNavigate: (path: string) => void
}) {
  return <nav className="crumbs" aria-label="Breadcrumb">
    {settingsActive ? <><BreadcrumbLink path="/projects" onNavigate={onNavigate}>projects</BreadcrumbLink><b>/</b><BreadcrumbLink path="/settings" onNavigate={onNavigate}>settings</BreadcrumbLink><b>/</b><strong aria-current="page">{settingsView}</strong></>
      : activeEnvironment ? <><BreadcrumbLink path="/projects" onNavigate={onNavigate}>projects</BreadcrumbLink><b>/</b><BreadcrumbLink path={`/projects/${encodeURIComponent(activeEnvironment.project)}`} onNavigate={onNavigate}>{activeEnvironment.project}</BreadcrumbLink><b>/</b><strong aria-current="page">{activeEnvironment.name}</strong></>
        : activeProject ? <><BreadcrumbLink path="/projects" onNavigate={onNavigate}>projects</BreadcrumbLink><b>/</b><strong aria-current="page">{activeProject.name}</strong></>
          : <strong aria-current="page">projects</strong>}
  </nav>
}

function BreadcrumbLink({ path, onNavigate, children }: { path: string; onNavigate: (path: string) => void; children: ReactNode }) {
  return <a href={path} onClick={(event) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    onNavigate(path)
  }}>{children}</a>
}

function ViewButton({ label, view, activeView, environment, icon, onNavigate }: {
  label: string
  view: EnvironmentView
  activeView: EnvironmentView
  environment: Environment
  icon: ReactNode
  onNavigate: (path: string) => void
}) {
  const active = activeView === view
  return <button className={active ? 'is-active' : ''} aria-current={active ? 'page' : undefined} onClick={() => onNavigate(environmentViewRoute(environment, view))}>{icon}<span>{label}</span></button>
}

function CommandPalette({ commands, onClose }: { commands: Command[]; onClose: () => void }) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const selectedCommand = useRef<HTMLButtonElement>(null)
  const filtered = commands.filter((command) => `${command.label} ${command.detail ?? ''} ${command.group}`.toLowerCase().includes(query.toLowerCase()))
  useEffect(() => input.current?.focus(), [])
  useEffect(() => setSelected(0), [query])
  useEffect(() => scrollCommandIntoView(selectedCommand.current), [query, selected])
  const execute = (command?: Command) => { if (!command) return; onClose(); command.run() }
  return <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
    <section className="command-palette" role="dialog" aria-modal="true" aria-label="Command palette" onMouseDown={(event) => event.stopPropagation()}>
      <div className="command-palette__input"><span>›</span><input ref={input} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="jump to a project or environment" onKeyDown={(event) => {
        if (event.key === 'ArrowDown') { event.preventDefault(); setSelected((value) => Math.min(value + 1, filtered.length - 1)) }
        if (event.key === 'ArrowUp') { event.preventDefault(); setSelected((value) => Math.max(value - 1, 0)) }
        if (event.key === 'Enter') execute(filtered[selected])
      }} /><small>{filtered.length} results</small></div>
      <div className="command-palette__results">{filtered.map((command, index) => { const previous = filtered[index - 1]; return <div key={`${command.group}:${command.label}:${index}`}>{(!previous || previous.group !== command.group) && <div className="command-group">{command.group}</div>}<button ref={index === selected ? selectedCommand : undefined} className={index === selected ? 'command is-selected' : 'command'} onMouseEnter={() => setSelected(index)} onClick={() => execute(command)}><span>{command.label}</span><small>{command.detail}</small></button></div> })}{filtered.length === 0 && <div className="command-empty">No matching project, environment, or action.</div>}</div>
      <footer><span><kbd>↑</kbd><kbd>↓</kbd> navigate</span><span><kbd>↵</kbd> open</span><span><kbd>esc</kbd> dismiss</span></footer>
    </section>
  </div>
}

export function scrollCommandIntoView(command: Pick<Element, 'scrollIntoView'> | null) {
  command?.scrollIntoView({ block: 'nearest' })
}

function environmentRoute(environment: Pick<Environment, 'project' | 'name'>) { return `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}` }
function environmentViewRoute(environment: Pick<Environment, 'project' | 'name'>, view: EnvironmentView) { const base = environmentRoute(environment); return view === 'overview' ? base : `${base}?tab=${view}` }
function mcpSettingsRoute(environment?: Pick<Environment, 'project' | 'name'>) { return environment ? `/settings?tab=mcp&env=${encodeURIComponent(`${environment.project}/${environment.name}`)}` : '/settings?tab=mcp' }
function readExpandedProjects() {
  try {
    const stored = JSON.parse(window.sessionStorage.getItem(expandedProjectsKey) ?? '[]')
    return new Set<string>(Array.isArray(stored) ? stored.filter((value): value is string => typeof value === 'string') : [])
  } catch { return new Set<string>() }
}
function ChevronIcon({ expanded }: { expanded: boolean }) { return <svg className={expanded ? 'is-expanded' : ''} viewBox="0 0 12 12" aria-hidden="true"><path d="m4 2.5 3.5 3.5L4 9.5" /></svg> }
function GridIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="5" height="5" /><rect x="9" y="2" width="5" height="5" /><rect x="2" y="9" width="5" height="5" /><rect x="9" y="9" width="5" height="5" /></svg> }
function TopologyIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.5" /><circle cx="10.5" cy="3.5" r="1.5" /><circle cx="13" cy="11.5" r="1.5" /><path d="m4.5 7.1 4.6-2.7M4.5 8.7l7 2.3M11.2 5l1.2 5" /></svg> }
function LinkIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6.5 5.5 5 4a3 3 0 0 0-4 4l2 2a3 3 0 0 0 4 0l1-1"/><path d="m9.5 10.5 1.5 1.5a3 3 0 0 0 4-4l-2-2a3 3 0 0 0-4 0L8 7"/></svg> }
function PulseIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M1 8h3l2-5 3.5 10L12 8h3" /></svg> }
function MockIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2h4M7 2v4l-4 7h10L9 6V2" /><path d="M5 10h6" /></svg> }
function RecordIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5" /><circle cx="8" cy="8" r="2" className="fill" /></svg> }
function FaultIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.5 14.5 14h-13L8 1.5Z" /><path d="M8 5v4M8 11.5v.5" /></svg> }
function TimelineIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 2v12M3 4h8M3 8h6M3 12h10" /><circle cx="3" cy="4" r="1" /><circle cx="3" cy="8" r="1" /><circle cx="3" cy="12" r="1" /></svg> }
function SettingsIcon() { return <svg className="settings-gear" viewBox="0 0 24 24" aria-hidden="true"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.38a2 2 0 0 0-.73-2.73l-.15-.09a2 2 0 0 1-1-1.74v-.51a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2Z" /><circle cx="12" cy="12" r="3" /></svg> }
