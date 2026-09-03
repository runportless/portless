import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { Environment } from '../api/contracts/environments'
import type { Project } from '../api/contracts/projects'
import type { ControlPlaneHealth, DaemonDiagnostics, DaemonHandoffStatus, DaemonRestart, DaemonStatus, RelayStatus, RuntimeStatus } from '../api/contracts/system'
import type { ProjectNavigationPreferences } from '../features/projects/projectNavigation'
import { DaemonDrawer } from './DaemonDrawer'
import { FaultIcon, MockIcon, RecordIcon } from './ExperimentIcons'
import { ProjectContextNav } from './ProjectContextNav'
import { environmentUIPath, environmentViews, environmentViewLabel, type EnvironmentView } from '../features/environment/navigation'
import { useOverlayDismiss } from './overlays/useOverlayDismiss'

export interface Command { label: string; detail?: string; group: string; run: () => void }
export type SettingsView = 'appearance' | 'runtime' | 'mcp'

const sidebarCollapsedKey = 'portless.sidebar-collapsed'
const focusModeKey = 'portless.focus-mode'
const focusModeShortcut = '⌘⇧F / Ctrl+Shift+F'
const focusNavigationCloseDelay = 320

export function AppChrome({ projects, environments, activeProject, sidebarProject, activeEnvironment, activeView, settingsActive = false, settingsView = 'appearance', navigation, runtime, daemon, diagnostics, controlPlaneHealth, relay, children, headerContext, headerActions, viewCounts, onNavigate, onSwitchProject, onEnvironmentChanged, onSettingsToggle, commands, live = true, onDaemonRefresh, onDaemonDiagnosticsRefresh, onDaemonHandoffVerify, onDaemonRestart, onDaemonReconnected }: {
  projects: Project[]
  environments: Environment[]
  activeProject?: Project
  sidebarProject?: Project
  activeEnvironment?: Environment
  activeView: EnvironmentView
  settingsActive?: boolean
  settingsView?: SettingsView
  navigation: ProjectNavigationPreferences
  runtime?: RuntimeStatus | null
  daemon: DaemonStatus | null
  diagnostics: DaemonDiagnostics | null
  controlPlaneHealth: ControlPlaneHealth
  relay?: RelayStatus | null
  children: ReactNode
  headerContext?: ReactNode
  headerActions?: ReactNode
  viewCounts?: Partial<Record<EnvironmentView, number>>
  onNavigate: (path: string) => void
  onSwitchProject: (project: Project) => void
  onEnvironmentChanged: () => Promise<void>
  onSettingsToggle: () => void
  commands: Command[]
  live?: boolean
  onDaemonRefresh: () => Promise<DaemonStatus>
  onDaemonDiagnosticsRefresh: (includeStorage?: boolean) => Promise<DaemonDiagnostics>
  onDaemonHandoffVerify: () => Promise<DaemonHandoffStatus>
  onDaemonRestart: (instanceId: string, force?: boolean) => Promise<DaemonRestart>
  onDaemonReconnected: () => Promise<void>
}) {
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [daemonOpen, setDaemonOpen] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(readSidebarCollapsed)
  const [focusMode, setFocusMode] = useState(readFocusMode)
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 760px)').matches)
  const [navigationMode, setNavigationMode] = useState<'closed' | 'preview' | 'explicit'>('closed')
  const sidebar = useRef<HTMLElement>(null)
  const shell = useRef<HTMLDivElement>(null)
  const header = useRef<HTMLElement>(null)
  const navigationTrigger = useRef<HTMLButtonElement>(null)
  const focusNavigationEdge = useRef<HTMLButtonElement>(null)
  const navigationCloseTimer = useRef<number | null>(null)
  const overlaySidebar = focusMode || narrow
  const navigationOpen = overlaySidebar && navigationMode !== 'closed'
  const modalNavigation = overlaySidebar && navigationMode === 'explicit'

  const cancelNavigationClose = useCallback(() => {
    if (navigationCloseTimer.current !== null) window.clearTimeout(navigationCloseTimer.current)
    navigationCloseTimer.current = null
  }, [])
  const closeNavigation = useCallback(() => {
    cancelNavigationClose()
    setNavigationMode('closed')
  }, [cancelNavigationClose])
  const openNavigation = useCallback(() => {
    cancelNavigationClose()
    setNavigationMode('explicit')
  }, [cancelNavigationClose])
  const previewNavigation = () => {
    cancelNavigationClose()
    setNavigationMode((current) => current === 'explicit' ? current : 'preview')
  }
  const scheduleNavigationClose = () => {
    if (navigationMode !== 'preview') return
    cancelNavigationClose()
    navigationCloseTimer.current = window.setTimeout(() => {
      navigationCloseTimer.current = null
      if (sidebar.current?.contains(document.activeElement)) return
      setNavigationMode((current) => current === 'preview' ? 'closed' : current)
    }, focusNavigationCloseDelay)
  }

  const { onBackdropMouseDown } = useOverlayDismiss({
    containerRef: sidebar, restoreFocusRef: focusMode ? focusNavigationEdge : navigationTrigger, dismissBlocked: false,
    enabled: modalNavigation, onDismiss: closeNavigation,
  })

  useEffect(() => {
    const media = window.matchMedia('(max-width: 760px)')
    const synchronize = () => { setNarrow(media.matches); closeNavigation() }
    media.addEventListener('change', synchronize)
    return () => media.removeEventListener('change', synchronize)
  }, [closeNavigation])

  useEffect(() => {
    const element = header.current
    if (!element || !shell.current) return
    const measure = () => shell.current?.style.setProperty('--app-header-height', `${Math.ceil(element.getBoundingClientRect().height)}px`)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    try { window.localStorage.setItem(sidebarCollapsedKey, sidebarCollapsed ? 'true' : 'false') }
    catch { /* Navigation still works when storage is unavailable. */ }
  }, [sidebarCollapsed])
  useEffect(() => {
    try { window.localStorage.setItem(focusModeKey, focusMode ? 'true' : 'false') }
    catch { /* Focus mode still works when storage is unavailable. */ }
  }, [focusMode])
  useEffect(() => () => cancelNavigationClose(), [cancelNavigationClose])

  const toggleFocusMode = useCallback(() => {
    closeNavigation()
    setFocusMode((value) => !value)
  }, [closeNavigation])

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        closeNavigation()
        setPaletteOpen((value) => !value)
      }
      if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.key.toLowerCase() === 'f') {
        event.preventDefault()
        setPaletteOpen(false)
        toggleFocusMode()
      }
      if (event.key === 'Escape') {
        setPaletteOpen(false)
        closeNavigation()
      }
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [closeNavigation, toggleFocusMode])

  useEffect(() => {
    if (!overlaySidebar || navigationMode !== 'preview') return
    const pointerdown = (event: PointerEvent) => {
      const target = event.target as Node
      if (sidebar.current?.contains(target) || focusNavigationEdge.current?.contains(target) || navigationTrigger.current?.contains(target)) return
      closeNavigation()
    }
    window.addEventListener('pointerdown', pointerdown)
    return () => window.removeEventListener('pointerdown', pointerdown)
  }, [closeNavigation, navigationMode, overlaySidebar])

  const focusDestination = () => {
    if (!overlaySidebar) return
    window.requestAnimationFrame(() => window.requestAnimationFrame(() => {
      document.querySelector<HTMLElement>('#environment-view-title, main h1, main')?.focus()
    }))
  }
  const navigateFromSidebar = (path: string) => { closeNavigation(); onNavigate(path); focusDestination() }
  const switchFromSidebar = (project: Project) => { closeNavigation(); onSwitchProject(project); focusDestination() }
  const settingsFromSidebar = () => { closeNavigation(); onSettingsToggle(); focusDestination() }

  const allCommands = useMemo<Command[]>(() => [
    ...projects.map((project) => ({ group: 'Switch project', label: project.name, detail: `${project.environments?.length || 0} environments`, run: () => onSwitchProject(project) })),
    ...environments.map((environment) => ({ group: 'Environments', label: `${environment.project}/${environment.name}`, detail: environment.status, run: () => onNavigate(environmentUIPath(environment)) })),
    ...commands,
    { group: 'View', label: focusMode ? 'Exit focus mode' : 'Enter focus mode', detail: focusModeShortcut, run: toggleFocusMode },
    { group: 'System', label: 'Configure MCP', detail: activeEnvironment ? `${activeEnvironment.project}/${activeEnvironment.name}` : 'AI client access', run: () => onNavigate(mcpSettingsRoute(activeEnvironment)) },
    { group: 'System', label: 'Settings', detail: 'Appearance, runtime, and MCP', run: onSettingsToggle },
  ], [activeEnvironment, commands, environments, focusMode, onNavigate, onSettingsToggle, onSwitchProject, projects, toggleFocusMode])

  const inspectDaemon = () => {
    closeNavigation()
    setPaletteOpen(false)
    setDaemonOpen(true)
    void onDaemonRefresh().catch(() => undefined)
    void onDaemonDiagnosticsRefresh(false).catch(() => undefined)
  }

  const daemonStateLabel = live ? daemon?.state ?? 'connected' : 'reconnecting'
  const daemonLabel = `daemon ${daemonStateLabel}`
  const compactSidebar = !overlaySidebar && sidebarCollapsed
  const shellClassName = ['shell', focusMode && 'shell--focus-mode', compactSidebar && 'shell--sidebar-collapsed', overlaySidebar && 'shell--overlay-navigation', navigationOpen && 'shell--navigation-open'].filter(Boolean).join(' ')

  return <div ref={shell} className={shellClassName}>
    {modalNavigation && <div className="navigation-backdrop" onMouseDown={onBackdropMouseDown} aria-hidden="true" />}
    <aside ref={sidebar} id="sidebar-navigation" className="sidebar" role={modalNavigation ? 'dialog' : undefined} aria-modal={modalNavigation || undefined} aria-label={modalNavigation ? 'Navigation' : undefined} tabIndex={modalNavigation ? -1 : undefined} inert={overlaySidebar && !navigationOpen} onMouseEnter={overlaySidebar ? previewNavigation : undefined} onMouseLeave={overlaySidebar ? scheduleNavigationClose : undefined} onFocus={overlaySidebar ? cancelNavigationClose : undefined} onBlur={overlaySidebar ? scheduleNavigationClose : undefined}>
      <div className="sidebar__header">
        <button className="brand" type="button" onClick={() => navigateFromSidebar('/projects')} aria-label="Portless projects" title={compactSidebar ? 'Projects' : undefined}><span className="brand__signal"><i /><i /><i /></span><span className="brand__wordmark">portless</span></button>
        {overlaySidebar
          ? <button className="sidebar__collapse" type="button" aria-label="Close navigation overlay" title="Close navigation overlay" onClick={closeNavigation}><SidebarCollapseIcon collapsed={false} /></button>
          : <button className="sidebar__collapse" type="button" aria-label={`${sidebarCollapsed ? 'Expand' : 'Collapse'} navigation`} aria-expanded={!sidebarCollapsed} title={`${sidebarCollapsed ? 'Expand' : 'Collapse'} navigation`} onClick={() => setSidebarCollapsed((value) => !value)}><SidebarCollapseIcon collapsed={sidebarCollapsed} /></button>}
      </div>
      <div className="sidebar__body">
        <ProjectContextNav projects={projects} environments={environments} project={sidebarProject} activeEnvironment={activeEnvironment} navigation={navigation} collapsed={compactSidebar} onNavigate={navigateFromSidebar} onSwitchProject={switchFromSidebar} onEnvironmentChanged={onEnvironmentChanged} />
        {activeEnvironment && <>
          <div className="sidebar__section-label sidebar__section-label--context"><span>Environment</span><small title={`${activeEnvironment.project}/${activeEnvironment.name}`}>{activeEnvironment.project}/{activeEnvironment.name}</small></div>
          <nav className="view-nav" aria-label={`${activeEnvironment.project}/${activeEnvironment.name} views`}>
            {environmentViews.map((view) => <ViewButton key={view.name} label={view.label} view={view.name} activeView={activeView} environment={activeEnvironment} icon={viewIcons[view.name]} compact={compactSidebar} count={viewCounts?.[view.name]} onNavigate={navigateFromSidebar} />)}
          </nav>
        </>}
      </div>
      <nav className="sidebar__utility" aria-label="Application">
        <button type="button" className={settingsActive ? 'is-active' : ''} aria-label="Settings" aria-current={settingsActive ? 'page' : undefined} title={compactSidebar ? 'Settings' : undefined} onClick={settingsFromSidebar}><SettingsIcon /><span>Settings</span></button>
      </nav>
      <button className="sidebar__footer" type="button" aria-label={`Daemon ${daemonStateLabel}`} aria-expanded={daemonOpen} title={compactSidebar ? `Daemon ${daemonStateLabel}` : undefined} onClick={inspectDaemon}><span className={live ? 'live-dot' : 'live-dot live-dot--off'} /><span className={live ? undefined : 'daemon-state--reconnecting'}>{daemonStateLabel}</span><small>DETAILS ›</small></button>
    </aside>
    {focusMode && <button ref={focusNavigationEdge} className="focus-navigation-edge" type="button" aria-label="Reveal navigation" aria-controls="sidebar-navigation" aria-expanded={navigationOpen} title="Open navigation" onMouseEnter={previewNavigation} onMouseLeave={scheduleNavigationClose} onClick={openNavigation}><span /></button>}
    <div className="stage" inert={modalNavigation}>
      <header ref={header} className={`topbar${activeEnvironment ? ' topbar--environment' : ''}`} aria-label={activeEnvironment ? `${activeEnvironment.project}/${activeEnvironment.name} environment` : 'Application header'}>
        <div className="topbar__context">
          {narrow && !focusMode && <button ref={navigationTrigger} className="topbar__navigation" type="button" aria-label="Open navigation" aria-controls="sidebar-navigation" aria-expanded={navigationOpen} onClick={openNavigation}><NavigationIcon /></button>}
          <TopbarBreadcrumbs settingsActive={settingsActive} settingsView={settingsView} activeProject={activeProject} activeEnvironment={activeEnvironment} activeView={activeView} onNavigate={onNavigate} />
          {headerContext}
        </div>
        <div className="topbar__actions">
          {headerActions}
          <div className="topbar__tools"><button className="topbar__daemon" type="button" aria-label={daemonLabel} onClick={inspectDaemon}><span className={live ? 'live-dot' : 'live-dot live-dot--off'} /></button><button className="key-button" aria-label="Search" title="Search (⌘K / Ctrl+K)" onClick={() => setPaletteOpen(true)}><span>⌘</span><span>K</span><em>Search</em></button></div>
        </div>
      </header>
      <main aria-labelledby={activeEnvironment ? 'environment-view-title' : undefined} tabIndex={-1}>{children}</main>
    </div>
    {paletteOpen && <CommandPalette commands={allCommands} onClose={() => setPaletteOpen(false)} />}
    {daemonOpen && <DaemonDrawer status={daemon} diagnostics={diagnostics} controlPlaneHealth={controlPlaneHealth} runtime={runtime ?? null} relay={relay ?? null} live={live} onClose={() => setDaemonOpen(false)} onRefresh={onDaemonRefresh} onRefreshDiagnostics={onDaemonDiagnosticsRefresh} onVerifyHandoff={onDaemonHandoffVerify} onRestart={onDaemonRestart} onReconnected={onDaemonReconnected} />}
  </div>
}

function TopbarBreadcrumbs({ settingsActive, settingsView, activeProject, activeEnvironment, activeView, onNavigate }: {
  settingsActive: boolean
  settingsView: SettingsView
  activeProject?: Project
  activeEnvironment?: Environment
  activeView: EnvironmentView
  onNavigate: (path: string) => void
}) {
  return <nav className={`crumbs${activeEnvironment ? ' crumbs--environment' : ''}`} aria-label="Breadcrumb">
    {settingsActive ? <><BreadcrumbLink path="/projects" onNavigate={onNavigate}>projects</BreadcrumbLink><b>/</b><BreadcrumbLink path="/settings" onNavigate={onNavigate}>settings</BreadcrumbLink><b>/</b><strong aria-current="page">{settingsView}</strong></>
      : activeEnvironment ? <><BreadcrumbLink path={`/projects/${encodeURIComponent(activeEnvironment.project)}`} onNavigate={onNavigate}>{activeEnvironment.project}</BreadcrumbLink><b>/</b><BreadcrumbLink path={environmentUIPath(activeEnvironment)} onNavigate={onNavigate}>{activeEnvironment.name}</BreadcrumbLink><b>/</b><h1 id="environment-view-title" tabIndex={-1} aria-current="page">{environmentViewLabel(activeView)}</h1></>
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

function ViewButton({ label, view, activeView, environment, icon, compact, count, onNavigate }: {
  label: string
  view: EnvironmentView
  activeView: EnvironmentView
  environment: Environment
  icon: ReactNode
  compact: boolean
  count?: number
  onNavigate: (path: string) => void
}) {
  const active = activeView === view
  const countLabel = view === 'faults' ? 'active fault' : view === 'mocks' ? 'active mock' : 'active recording'
  const description = count ? `${count} ${countLabel}${count === 1 ? '' : 's'}` : undefined
  return <button className={active ? 'is-active' : ''} aria-label={label} aria-describedby={description ? `view-count-${view}` : undefined} aria-current={active ? 'page' : undefined} title={compact ? label : undefined} onClick={() => onNavigate(environmentUIPath(environment, view))}>{icon}<span>{label}</span>{description && <><small className="view-nav__count" aria-hidden="true">{count}</small><span className="sr-only" id={`view-count-${view}`}>{description}</span></>}</button>
}

function CommandPalette({ commands, onClose }: { commands: Command[]; onClose: () => void }) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const container = useRef<HTMLElement>(null)
  const selectedCommand = useRef<HTMLButtonElement>(null)
  const { onBackdropMouseDown } = useOverlayDismiss({ containerRef: container, initialFocusRef: input, dismissBlocked: false, onDismiss: onClose })
  const filtered = commands.filter((command) => `${command.label} ${command.detail ?? ''} ${command.group}`.toLowerCase().includes(query.toLowerCase()))
  useEffect(() => setSelected(0), [query])
  useEffect(() => scrollCommandIntoView(selectedCommand.current), [query, selected])
  const execute = (command?: Command) => { if (!command) return; onClose(); command.run() }
  return <div className="modal-backdrop" role="presentation" onMouseDown={onBackdropMouseDown}>
    <section ref={container} className="command-palette" role="dialog" aria-modal="true" aria-label="Command palette" onMouseDown={(event) => event.stopPropagation()}>
      <div className="command-palette__input"><span>›</span><input ref={input} value={query} onChange={(event) => setQuery(event.target.value)} aria-label="Search" placeholder="Search" onKeyDown={(event) => {
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

function mcpSettingsRoute(environment?: Pick<Environment, 'project' | 'name'>) { return environment ? `/settings?tab=mcp&env=${encodeURIComponent(`${environment.project}/${environment.name}`)}` : '/settings?tab=mcp' }
function readSidebarCollapsed() {
  try { return window.localStorage.getItem(sidebarCollapsedKey) === 'true' }
  catch { return false }
}
function readFocusMode() {
  try { return window.localStorage.getItem(focusModeKey) === 'true' }
  catch { return false }
}
const viewIcons: Record<EnvironmentView, ReactNode> = { overview: <GridIcon />, topology: <TopologyIcon />, traffic: <PulseIcon />, mocks: <MockIcon />, recordings: <RecordIcon />, faults: <FaultIcon />, bindings: <LinkIcon />, timeline: <TimelineIcon /> }
function NavigationIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 4h12M2 8h12M2 12h12" /></svg> }
function SidebarCollapseIcon({ collapsed }: { collapsed: boolean }) { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d={collapsed ? 'm6 3 5 5-5 5' : 'm10 3-5 5 5 5'} /></svg> }
function GridIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="5" height="5" /><rect x="9" y="2" width="5" height="5" /><rect x="2" y="9" width="5" height="5" /><rect x="9" y="9" width="5" height="5" /></svg> }
function TopologyIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.5" /><circle cx="10.5" cy="3.5" r="1.5" /><circle cx="13" cy="11.5" r="1.5" /><path d="m4.5 7.1 4.6-2.7M4.5 8.7l7 2.3M11.2 5l1.2 5" /></svg> }
function LinkIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6.5 5.5 5 4a3 3 0 0 0-4 4l2 2a3 3 0 0 0 4 0l1-1"/><path d="m9.5 10.5 1.5 1.5a3 3 0 0 0 4-4l-2-2a3 3 0 0 0-4 0L8 7"/></svg> }
function PulseIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M1 8h3l2-5 3.5 10L12 8h3" /></svg> }
function TimelineIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 2v12M3 4h8M3 8h6M3 12h10" /><circle cx="3" cy="4" r="1" /><circle cx="3" cy="8" r="1" /><circle cx="3" cy="12" r="1" /></svg> }
function SettingsIcon() { return <svg className="settings-gear" viewBox="0 0 24 24" aria-hidden="true"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.38a2 2 0 0 0-.73-2.73l-.15-.09a2 2 0 0 1-1-1.74v-.51a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2Z" /><circle cx="12" cy="12" r="3" /></svg> }
