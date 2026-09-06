import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import type { Environment } from '../api/contracts/environments'
import type { Project } from '../api/contracts/projects'
import { aggregateProjectStatus } from '../features/projects/projectPresentation'
import { StatusMark } from './Status'
import { useOverlayDismiss } from './overlays/useOverlayDismiss'

type SwitcherOption = { id: string } & (
  | { kind: 'environment'; environment: Environment }
  | { kind: 'project'; project: Project }
)

export function ProjectSwitcher({ projects, recentProjects, environments, project, activeEnvironment, onClose, onManage, onSwitch, onOpenEnvironment }: {
  projects: Project[]
  recentProjects: Project[]
  environments: Environment[]
  project?: Project
  activeEnvironment?: Environment
  onClose: (restoreFocus?: boolean) => void
  onManage: () => void
  onSwitch: (project: Project) => void
  onOpenEnvironment: (environment: Environment) => void
}) {
  const [query, setQuery] = useState('')
  const [highlightedID, setHighlightedID] = useState('')
  const container = useRef<HTMLDivElement>(null)
  const search = useRef<HTMLInputElement>(null)
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([])
  useOverlayDismiss({ containerRef: container, initialFocusRef: search, dismissBlocked: false, onDismiss: () => onClose(true) })
  const { running, recent } = useMemo(() => {
    const searchText = query.trim().toLowerCase()
    const projectNames = new Set(projects.map((item) => item.name))
    const running: SwitcherOption[] = environments
      .filter((item) => projectNames.has(item.project) && item.status !== 'stopped' && `${item.project}/${item.name}`.toLowerCase().includes(searchText))
      .sort((left, right) => left.project.localeCompare(right.project) || left.name.localeCompare(right.name))
      .map((environment) => ({ kind: 'environment', id: `environment:${environment.project}/${environment.name}`, environment }))
    const recent: SwitcherOption[] = recentProjects
      .filter((item) => item.name.toLowerCase().includes(searchText))
      .map((project) => ({ kind: 'project', id: `project:${project.name}`, project }))
    return { running, recent }
  }, [environments, projects, query, recentProjects])
  const options = [...running, ...recent]
  const highlightedIndex = Math.max(0, options.findIndex((item) => item.id === highlightedID))

  useEffect(() => {
    const outside = (event: MouseEvent) => { if (!container.current?.contains(event.target as Node)) onClose() }
    document.addEventListener('mousedown', outside)
    return () => document.removeEventListener('mousedown', outside)
  }, [onClose])

  useEffect(() => {
    optionRefs.current[highlightedIndex]?.scrollIntoView({ block: 'nearest' })
  }, [highlightedIndex, highlightedID, query])

  const select = (option: SwitcherOption) => {
    if (option.kind === 'environment') onOpenEnvironment(option.environment)
    else onSwitch(option.project)
  }

  const keydown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!options.length) return
    if (event.key === 'ArrowDown') { event.preventDefault(); setHighlightedID(options[(highlightedIndex + 1) % options.length].id) }
    if (event.key === 'ArrowUp') { event.preventDefault(); setHighlightedID(options[(highlightedIndex - 1 + options.length) % options.length].id) }
    if (event.key === 'Enter') { event.preventDefault(); select(options[highlightedIndex]) }
  }

  const renderOption = (option: SwitcherOption, index: number) => {
    const owned = option.kind === 'project' ? environments.filter((environment) => environment.project === option.project.name) : []
    const status = option.kind === 'environment' ? option.environment.status : aggregateProjectStatus(owned)
    const current = option.kind === 'environment'
      ? option.environment.project === activeEnvironment?.project && option.environment.name === activeEnvironment.name
      : option.project.name === project?.name
    const name = option.kind === 'environment' ? `${option.environment.project}/${option.environment.name}` : option.project.name
    const count = option.kind === 'environment' ? option.environment.services.length : owned.length
    const noun = option.kind === 'environment' ? 'service' : 'environment'
    return <button ref={(element) => { optionRefs.current[index] = element }} id={`project-switcher-option-${index}`} className={index === highlightedIndex ? 'project-switcher__option is-selected' : 'project-switcher__option'} type="button" role="option" aria-selected={index === highlightedIndex} data-current={current} data-kind={option.kind} key={option.id} onMouseEnter={() => setHighlightedID(option.id)} onFocus={() => setHighlightedID(option.id)} onClick={() => select(option)}>
      <StatusMark status={status} label={false} />
      <span className="project-switcher__project"><span className="project-switcher__name"><strong title={name}>{name}</strong>{current && <span className="project-switcher__current">Current</span>}</span><small>{count} {noun}{count !== 1 && 's'}</small></span>
      <em>{status.toUpperCase()}</em>
    </button>
  }

  return <div ref={container} className="project-switcher" role="dialog" aria-modal="true" aria-label="Switch project">
    <div className="project-switcher__heading"><span>Switch project</span><button type="button" aria-label="Close project switcher" onClick={() => onClose(true)}>×</button></div>
    <label className="project-switcher__search"><span className="sr-only">Search projects and environments</span><SearchIcon /><input ref={search} role="combobox" value={query} placeholder="Search" autoComplete="off" aria-autocomplete="list" aria-expanded="true" aria-controls="project-switcher-results" aria-activedescendant={options.length ? `project-switcher-option-${highlightedIndex}` : undefined} onChange={(event) => { setQuery(event.target.value); setHighlightedID('') }} onKeyDown={keydown} /></label>
    <div id="project-switcher-results" className="project-switcher__results" role="listbox" aria-label="Projects and environments">
      {(running.length > 0 || !query.trim()) && <div className="project-switcher__group" role="group" aria-labelledby="project-switcher-running">
        <div id="project-switcher-running" className="project-switcher__group-heading">Running environments</div>
        {running.map(renderOption)}
        {running.length === 0 && <div className="project-switcher__empty">No running environments.</div>}
      </div>}
      {(recent.length > 0 || !query.trim()) && <div className="project-switcher__group" role="group" aria-labelledby="project-switcher-recent">
        <div id="project-switcher-recent" className="project-switcher__group-heading">Recent projects</div>
        {recent.map((option, index) => renderOption(option, running.length + index))}
        {recent.length === 0 && <div className="project-switcher__empty">No recent projects.</div>}
      </div>}
      {options.length === 0 && query.trim() && <div className="project-switcher__empty">No projects or environments match this search.</div>}
    </div>
    <button className="project-switcher__manage" type="button" onClick={onManage}>Manage projects <span>→</span></button>
  </div>
}

function SearchIcon() {
  return <svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5" /><path d="m12.2 12.2 4 4" /></svg>
}
