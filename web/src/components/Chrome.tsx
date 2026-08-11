import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { Project, RuntimeStatus } from '../types'
import { StatusMark } from './Status'

export interface Command {
  label: string
  detail?: string
  group: string
  run: () => void
}

export function AppChrome({ projects, activeProject, runtime, children, onNavigate, commands, live = true }: {
  projects: Project[]
  activeProject?: Project
  runtime?: RuntimeStatus | null
  children: ReactNode
  onNavigate: (path: string) => void
  commands: Command[]
  live?: boolean
}) {
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [viewProjectName, setViewProjectName] = useState(activeProject?.name)
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPaletteOpen((value) => !value)
      }
      if (event.key === 'Escape') setPaletteOpen(false)
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [])
  useEffect(() => {
    if (activeProject) setViewProjectName(activeProject.name)
  }, [activeProject])
  const viewProject = activeProject ?? projects.find((project) => project.name === viewProjectName) ?? projects[0]
  const allCommands = useMemo<Command[]>(() => [
    ...projects.map((project) => ({ group: 'Projects', label: project.name, detail: project.status.replaceAll('_', ' '), run: () => onNavigate(`/projects/${project.name}`) })),
    ...commands,
    { group: 'Navigation', label: 'All projects', detail: 'global environment list', run: () => onNavigate('/projects') },
  ], [commands, onNavigate, projects])

  return (
    <div className="shell">
      <aside className="sidebar">
        <button className="brand" onClick={() => onNavigate('/projects')} aria-label="Portless projects">
          <span className="brand__signal"><i /><i /><i /></span>
          <span>portless</span>
          <small>local</small>
        </button>
        <div className="sidebar__section-label">Projects</div>
        <nav className="project-nav" aria-label="Projects">
          {projects.map((project) => (
            <button key={project.name} className={project.name === activeProject?.name ? 'project-nav__item is-active' : 'project-nav__item'} onClick={() => onNavigate(`/projects/${project.name}`)}>
              <StatusMark status={project.status} label={false} />
              <span>{project.name}</span>
              <small>{project.status === 'healthy' ? 'live' : project.status.replaceAll('_', ' ')}</small>
            </button>
          ))}
          {projects.length === 0 && <div className="sidebar__empty">Run <code>portless up</code> in a project.</div>}
        </nav>
        <div className="sidebar__section-label">Views</div>
        <nav className="view-nav">
          <button onClick={() => onNavigate('/projects')} className={!activeProject ? 'is-active' : ''}><GridIcon /> Environments</button>
          {viewProject && <>
            <button onClick={() => onNavigate(`/projects/${viewProject.name}?tab=traffic`)}><PulseIcon /> Traffic</button>
            <button onClick={() => onNavigate(`/projects/${viewProject.name}?tab=recordings`)}><RecordIcon /> Recordings</button>
            <button onClick={() => onNavigate(`/projects/${viewProject.name}?tab=faults`)}><FaultIcon /> Faults</button>
          </>}
        </nav>
        <div className="sidebar__footer">
          <span className={live ? 'live-dot' : 'live-dot live-dot--off'} />
          {live ? 'daemon connected' : 'reconnecting'}
          {runtime?.selected && <small>{runtime.selected}</small>}
        </div>
      </aside>
      <div className="stage">
        <header className="topbar">
          <div className="crumbs">
            <span className="environment-chip">LOCAL</span>
            {activeProject ? <><span>projects</span><b>/</b><strong>{activeProject.name}</strong><StatusMark status={activeProject.status} /></> : <><span>projects</span><b>/</b><strong>all environments</strong></>}
          </div>
          <div className="topbar__tools">
            <button className="key-button" onClick={() => setPaletteOpen(true)}><span>⌘</span><span>K</span><em>jump or run</em></button>
          </div>
        </header>
        <main>{children}</main>
      </div>
      {paletteOpen && <CommandPalette commands={allCommands} onClose={() => setPaletteOpen(false)} />}
    </div>
  )
}

function CommandPalette({ commands, onClose }: { commands: Command[]; onClose: () => void }) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const filtered = commands.filter((command) => `${command.label} ${command.detail ?? ''} ${command.group}`.toLowerCase().includes(query.toLowerCase()))
  useEffect(() => input.current?.focus(), [])
  useEffect(() => setSelected(0), [query])
  const execute = (command?: Command) => {
    if (!command) return
    onClose()
    command.run()
  }
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <section className="command-palette" role="dialog" aria-modal="true" aria-label="Command palette" onMouseDown={(event) => event.stopPropagation()}>
        <div className="command-palette__input"><span>›</span><input ref={input} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="jump to a project, or run something" onKeyDown={(event) => {
          if (event.key === 'ArrowDown') { event.preventDefault(); setSelected((value) => Math.min(value + 1, filtered.length - 1)) }
          if (event.key === 'ArrowUp') { event.preventDefault(); setSelected((value) => Math.max(value - 1, 0)) }
          if (event.key === 'Enter') execute(filtered[selected])
        }} /><small>{filtered.length} results</small></div>
        <div className="command-palette__results">
          {filtered.map((command, index) => {
            const previous = filtered[index - 1]
            return <div key={`${command.group}:${command.label}:${index}`}>
              {(!previous || previous.group !== command.group) && <div className="command-group">{command.group}</div>}
              <button className={index === selected ? 'command is-selected' : 'command'} onMouseEnter={() => setSelected(index)} onClick={() => execute(command)}>
                <span>{command.label}</span><small>{command.detail}</small>
              </button>
            </div>
          })}
          {filtered.length === 0 && <div className="command-empty">No matching project or action.</div>}
        </div>
        <footer><span><kbd>↑</kbd><kbd>↓</kbd> navigate</span><span><kbd>↵</kbd> open</span><span><kbd>esc</kbd> dismiss</span></footer>
      </section>
    </div>
  )
}

function GridIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="5" height="5" /><rect x="9" y="2" width="5" height="5" /><rect x="2" y="9" width="5" height="5" /><rect x="9" y="9" width="5" height="5" /></svg> }
function PulseIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M1 8h3l2-5 3.5 10L12 8h3" /></svg> }
function RecordIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5" /><circle cx="8" cy="8" r="2" className="fill" /></svg> }
function FaultIcon() { return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.5 14.5 14h-13L8 1.5Z" /><path d="M8 5v4M8 11.5v.5" /></svg> }
