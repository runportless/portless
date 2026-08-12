import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { Environment, Project, RuntimeStatus } from '../types'
import { StatusMark } from './Status'

export interface Command { label: string; detail?: string; group: string; run: () => void }
export type EnvironmentView = 'overview' | 'bindings' | 'traffic' | 'recordings' | 'faults' | 'timeline'

const expandedProjectsKey = 'portless.expanded-projects'

export function AppChrome({ projects, environments, activeProject, activeEnvironment, activeView, runtime, children, onNavigate, commands, live = true }: {
  projects: Project[]
  environments: Environment[]
  activeProject?: Project
  activeEnvironment?: Environment
  activeView: EnvironmentView
  runtime?: RuntimeStatus | null
  children: ReactNode
  onNavigate: (path: string) => void
  commands: Command[]
  live?: boolean
}) {
  const [paletteOpen, setPaletteOpen] = useState(false)
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
  ], [commands, environments, onNavigate, projects])

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

  return <div className="shell">
    <aside className="sidebar">
      <button className="brand" onClick={() => onNavigate('/projects')} aria-label="Portless projects"><span className="brand__signal"><i /><i /><i /></span><span>portless</span><small>local</small></button>
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
            <ViewButton label="Providers" view="bindings" activeView={activeView} environment={activeEnvironment} icon={<LinkIcon />} onNavigate={onNavigate} />
            <ViewButton label="Traffic" view="traffic" activeView={activeView} environment={activeEnvironment} icon={<PulseIcon />} onNavigate={onNavigate} />
            <ViewButton label="Recordings" view="recordings" activeView={activeView} environment={activeEnvironment} icon={<RecordIcon />} onNavigate={onNavigate} />
            <ViewButton label="Faults" view="faults" activeView={activeView} environment={activeEnvironment} icon={<FaultIcon />} onNavigate={onNavigate} />
            <ViewButton label="Timeline" view="timeline" activeView={activeView} environment={activeEnvironment} icon={<TimelineIcon />} onNavigate={onNavigate} />
          </nav>
        </>}
      </div>
      <div className="sidebar__footer"><span className={live ? 'live-dot' : 'live-dot live-dot--off'} />{live ? 'daemon connected' : 'reconnecting'}{runtime?.selected && <small>{runtime.selected}</small>}</div>
    </aside>
    <div className="stage">
      <header className="topbar"><div className="crumbs"><span className="environment-chip">LOCAL</span>{activeEnvironment ? <><span>{activeEnvironment.project}</span><b>/</b><strong>{activeEnvironment.name}</strong><StatusMark status={activeEnvironment.status} /></> : activeProject ? <><span>projects</span><b>/</b><strong>{activeProject.name}</strong></> : <><span>projects</span><b>/</b><strong>all</strong></>}</div><div className="topbar__tools"><button className="key-button" onClick={() => setPaletteOpen(true)}><span>⌘</span><span>K</span><em>jump or run</em></button></div></header>
      <main>{children}</main>
    </div>
    {paletteOpen && <CommandPalette commands={allCommands} onClose={() => setPaletteOpen(false)} />}
  </div>
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
  const filtered = commands.filter((command) => `${command.label} ${command.detail ?? ''} ${command.group}`.toLowerCase().includes(query.toLowerCase()))
  useEffect(() => input.current?.focus(), [])
  useEffect(() => setSelected(0), [query])
  const execute = (command?: Command) => { if (!command) return; onClose(); command.run() }
  return <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
    <section className="command-palette" role="dialog" aria-modal="true" aria-label="Command palette" onMouseDown={(event) => event.stopPropagation()}>
      <div className="command-palette__input"><span>›</span><input ref={input} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="jump to a project or environment" onKeyDown={(event) => {
        if (event.key === 'ArrowDown') { event.preventDefault(); setSelected((value) => Math.min(value + 1, filtered.length - 1)) }
        if (event.key === 'ArrowUp') { event.preventDefault(); setSelected((value) => Math.max(value - 1, 0)) }
        if (event.key === 'Enter') execute(filtered[selected])
      }} /><small>{filtered.length} results</small></div>
      <div className="command-palette__results">{filtered.map((command, index) => { const previous = filtered[index - 1]; return <div key={`${command.group}:${command.label}:${index}`}>{(!previous || previous.group !== command.group) && <div className="command-group">{command.group}</div>}<button className={index === selected ? 'command is-selected' : 'command'} onMouseEnter={() => setSelected(index)} onClick={() => execute(command)}><span>{command.label}</span><small>{command.detail}</small></button></div> })}{filtered.length === 0 && <div className="command-empty">No matching project, environment, or action.</div>}</div>
      <footer><span><kbd>↑</kbd><kbd>↓</kbd> navigate</span><span><kbd>↵</kbd> open</span><span><kbd>esc</kbd> dismiss</span></footer>
    </section>
  </div>
}

function environmentRoute(environment: Pick<Environment, 'project' | 'name'>) { return `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}` }
function environmentViewRoute(environment: Pick<Environment, 'project' | 'name'>, view: EnvironmentView) { const base = environmentRoute(environment); return view === 'overview' ? base : `${base}?tab=${view}` }
function readExpandedProjects() {
  try {
    const stored = JSON.parse(window.sessionStorage.getItem(expandedProjectsKey) ?? '[]')
    return new Set<string>(Array.isArray(stored) ? stored.filter((value): value is string => typeof value === 'string') : [])
  } catch { return new Set<string>() }
}
function ChevronIcon({ expanded }: { expanded: boolean }) { return <svg className={expanded ? 'is-expanded' : ''} viewBox="0 0 12 12" aria-hidden="true"><path d="m4 2.5 3.5 3.5L4 9.5" /></svg> }
function GridIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="5" height="5" /><rect x="9" y="2" width="5" height="5" /><rect x="2" y="9" width="5" height="5" /><rect x="9" y="9" width="5" height="5" /></svg> }
function LinkIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6.5 5.5 5 4a3 3 0 0 0-4 4l2 2a3 3 0 0 0 4 0l1-1"/><path d="m9.5 10.5 1.5 1.5a3 3 0 0 0 4-4l-2-2a3 3 0 0 0-4 0L8 7"/></svg> }
function PulseIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M1 8h3l2-5 3.5 10L12 8h3" /></svg> }
function RecordIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5" /><circle cx="8" cy="8" r="2" className="fill" /></svg> }
function FaultIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.5 14.5 14h-13L8 1.5Z" /><path d="M8 5v4M8 11.5v.5" /></svg> }
function TimelineIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 2v12M3 4h8M3 8h6M3 12h10" /><circle cx="3" cy="4" r="1" /><circle cx="3" cy="8" r="1" /><circle cx="3" cy="12" r="1" /></svg> }
