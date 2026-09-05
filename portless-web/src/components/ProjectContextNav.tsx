import { environmentUIPath } from '../features/environment/navigation'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { Environment } from '../api/contracts/environments'
import type { Project } from '../api/contracts/projects'
import { CreateEnvironmentDialog } from '../features/projects/CreateEnvironmentDialog'
import { aggregateProjectStatus } from '../features/projects/projectPresentation'
import { formatProjectLastOpened, projectIsRunning, recentProjects, runningProjects, type ProjectNavigationPreferences } from '../features/projects/projectNavigation'
import { StatusMark } from './Status'
import { useOverlayDismiss } from './overlays/useOverlayDismiss'

export function ProjectContextNav({ projects, environments, project, activeEnvironment, navigation, collapsed, onNavigate, onSwitchProject, onEnvironmentChanged }: {
  projects: Project[]
  environments: Environment[]
  project?: Project
  activeEnvironment?: Environment
  navigation: ProjectNavigationPreferences
  collapsed: boolean
  onNavigate: (path: string) => void
  onSwitchProject: (project: Project) => void
  onEnvironmentChanged: () => Promise<void>
}) {
  const [switcherOpen, setSwitcherOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const trigger = useRef<HTMLButtonElement>(null)
  const ownedEnvironments = useMemo(() => environments.filter((environment) => environment.project === project?.name).sort((left, right) => left.name.localeCompare(right.name)), [environments, project?.name])
  const otherRunning = useMemo(() => runningProjects(projects, environments).filter((item) => item.name !== project?.name), [environments, project?.name, projects])
  const otherStatus = aggregateProjectStatus(environments.filter((environment) => otherRunning.some((item) => item.name === environment.project)))
  const projectLabel = project?.name || (projects.length ? 'Select project' : 'No projects')
  const closeSwitcher = (restoreFocus = false) => {
    setSwitcherOpen(false)
    if (restoreFocus) window.requestAnimationFrame(() => trigger.current?.focus())
  }

  return <>
    <div className="sidebar__section-label project-context-label">Project</div>
    <div className="project-context">
      <button
        ref={trigger}
        className="project-context__trigger"
        type="button"
        aria-label={`${project ? `Current project ${project.name}` : 'Select project'}. Switch project`}
        aria-haspopup="dialog"
        aria-expanded={switcherOpen}
        title={collapsed ? projectLabel : undefined}
        disabled={projects.length === 0}
        onMouseDown={(event) => event.stopPropagation()}
        onClick={() => setSwitcherOpen((value) => !value)}
      >
        <ProjectIcon />
        <span>{projectLabel}</span>
        {project && <small>{ownedEnvironments.length} env</small>}
        <ChevronIcon expanded={switcherOpen} />
      </button>
      {switcherOpen && <ProjectSwitcher projects={projects} environments={environments} project={project} navigation={navigation} onClose={closeSwitcher} onManage={() => { setSwitcherOpen(false); onNavigate('/projects') }} onSwitch={(item) => { setSwitcherOpen(false); onSwitchProject(item) }} />}
    </div>

    {project && <>
      <div className="sidebar__section-label sidebar__section-label--action">
        <span>Environments</span>
        <button
          className="sidebar__section-action"
          type="button"
          aria-label={`Create environment in ${project.name}`}
          aria-haspopup="dialog"
          title={ownedEnvironments.length === 0 ? 'Run portless up to create the first environment' : collapsed ? 'Create environment' : undefined}
          disabled={ownedEnvironments.length === 0}
          onClick={() => setCreateOpen(true)}
        ><span aria-hidden="true">+</span><span className="sidebar__section-action-label">NEW</span></button>
      </div>
      <nav className="project-nav project-environment-nav" aria-label={`${project.name} environments`}>
        {ownedEnvironments.map((environment) => {
          const selected = activeEnvironment?.project === environment.project && activeEnvironment.name === environment.name
          return <button key={environment.name} className={selected ? 'project-nav__item is-active' : 'project-nav__item'} aria-label={`${environment.project}/${environment.name}, ${environment.status}`} aria-current={selected ? 'page' : undefined} title={collapsed ? `${environment.project}/${environment.name}` : undefined} onClick={() => onNavigate(environmentUIPath(environment))}>
            <span className="project-nav__environment-icon"><EnvironmentIcon /><StatusMark status={environment.status} label={false} /></span><span>{environment.name}</span><small data-status={environment.status}>{environment.status}</small>
          </button>
        })}
        {ownedEnvironments.length === 0 && <div className="sidebar__empty">Run <code>portless up</code> to create this project's first environment.</div>}
      </nav>
    </>}

    {otherRunning.length > 0 && <button className="other-running-projects" type="button" aria-label={`${otherRunning.length} other ${otherRunning.length === 1 ? 'project' : 'projects'} running. Switch project`} title={collapsed ? `${otherRunning.length} other running` : undefined} onClick={() => setSwitcherOpen(true)}>
      <span className="other-running-projects__status"><StatusMark status={otherStatus} label={false} /><StackIcon /></span>
      <span>{otherRunning.length} other {otherRunning.length === 1 ? 'project' : 'projects'} running</span>
    </button>}

    {!project && projects.length === 0 && <div className="sidebar__empty">Run <code>portless up</code> or create a multi-source project.</div>}
    {project && createOpen && <CreateEnvironmentDialog project={project} environments={ownedEnvironments} initialCloneFrom={activeEnvironment?.project === project.name ? activeEnvironment.name : undefined} onClose={() => setCreateOpen(false)} onNavigate={onNavigate} onChanged={onEnvironmentChanged} />}
  </>
}

function ProjectSwitcher({ projects, environments, project, navigation, onClose, onManage, onSwitch }: {
  projects: Project[]
  environments: Environment[]
  project?: Project
  navigation: ProjectNavigationPreferences
  onClose: (restoreFocus?: boolean) => void
  onManage: () => void
  onSwitch: (project: Project) => void
}) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const container = useRef<HTMLDivElement>(null)
  const search = useRef<HTMLInputElement>(null)
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([])
  useOverlayDismiss({ containerRef: container, initialFocusRef: search, dismissBlocked: false, onDismiss: () => onClose(true) })
  const active = useMemo(() => runningProjects(projects, environments), [environments, projects])
  const recent = useMemo(() => recentProjects(projects, environments, navigation), [environments, navigation, projects])
  const activeNames = new Set(active.map((item) => item.name))
  const recentNames = new Set(recent.map((item) => item.name))
  const currentOnly = project && !activeNames.has(project.name) && !recentNames.has(project.name) ? [project] : []
  const results = query.trim()
    ? projects.filter((item) => item.name.toLowerCase().includes(query.trim().toLowerCase())).sort((left, right) => left.name.localeCompare(right.name))
    : []
  const groups = query.trim()
    ? [{ label: 'Results', projects: results }]
    : [{ label: 'Running', projects: active }, ...(currentOnly.length ? [{ label: 'Current', projects: currentOnly }] : []), { label: 'Recent', projects: recent }]
  const options = groups.flatMap((group) => group.projects)

  useEffect(() => {
    const outside = (event: MouseEvent) => { if (!container.current?.contains(event.target as Node)) onClose() }
    document.addEventListener('mousedown', outside)
    return () => document.removeEventListener('mousedown', outside)
  }, [onClose])

  useEffect(() => {
    setSelected(0)
    optionRefs.current = []
  }, [query])

  useEffect(() => optionRefs.current[selected]?.scrollIntoView({ block: 'nearest' }), [selected])

  const keydown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (!options.length) return
    if (event.key === 'ArrowDown') { event.preventDefault(); setSelected((value) => (value + 1) % options.length) }
    if (event.key === 'ArrowUp') { event.preventDefault(); setSelected((value) => (value - 1 + options.length) % options.length) }
    if (event.key === 'Enter') { event.preventDefault(); onSwitch(options[selected]) }
  }

  let optionIndex = 0
  return <div ref={container} className="project-switcher" role="dialog" aria-modal="true" aria-label="Switch project">
    <div className="project-switcher__heading"><span>Switch project</span><button type="button" aria-label="Close project switcher" onClick={() => onClose(true)}>×</button></div>
    <label className="project-switcher__search"><span className="sr-only">Search projects</span><SearchIcon /><input ref={search} role="combobox" value={query} placeholder="Search" autoComplete="off" aria-autocomplete="list" aria-expanded="true" aria-controls="project-switcher-results" aria-activedescendant={options.length ? `project-switcher-option-${selected}` : undefined} onChange={(event) => setQuery(event.target.value)} onKeyDown={keydown} /></label>
    <div id="project-switcher-results" className="project-switcher__results" role="listbox" aria-label="Projects">
      {groups.map((group) => group.projects.length > 0 && <section className="project-switcher__group" role="group" key={group.label} aria-label={group.label}>
        <div className="project-switcher__group-label">{group.label}</div>
        {group.projects.map((item) => {
          const index = optionIndex++
          const owned = environments.filter((environment) => environment.project === item.name)
          const status = aggregateProjectStatus(owned)
          const hidden = navigation.hiddenProjects.includes(item.name)
          const selectedProject = item.name === project?.name
          return <button ref={(element) => { optionRefs.current[index] = element }} id={`project-switcher-option-${index}`} className={index === selected ? 'project-switcher__option is-selected' : 'project-switcher__option'} type="button" role="option" aria-selected={selectedProject} key={item.name} onMouseEnter={() => setSelected(index)} onClick={() => onSwitch(item)}>
            <StatusMark status={status} label={false} />
            <span><strong>{item.name}</strong><small>{owned.length} {owned.length === 1 ? 'environment' : 'environments'}{!projectIsRunning(item, environments) && navigation.lastOpenedByProject[item.name] ? ` · ${formatProjectLastOpened(navigation.lastOpenedByProject[item.name])}` : ''}</small></span>
            <em>{selectedProject ? 'ACTIVE' : hidden ? 'HIDDEN' : status.toUpperCase()}</em>
          </button>
        })}
      </section>)}
      {options.length === 0 && <div className="project-switcher__empty">{query.trim() ? 'No projects match this search.' : 'No recent projects.'}</div>}
    </div>
    <button className="project-switcher__manage" type="button" onClick={onManage}>Manage projects <span>→</span></button>
  </div>
}

function ProjectIcon() {
  return <svg className="project-nav__project-icon" viewBox="0 0 20 20" aria-hidden="true"><path d="M2.5 5.5h5l1.4 1.7h8.6v8.3h-15z" /><path d="M2.5 7.2h15" /></svg>
}

function EnvironmentIcon() {
  return <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 2.6 16.2 6v7L10 16.4 3.8 13V6z" /><path d="m3.8 6 6.2 3.5L16.2 6M10 9.5v6.9" /></svg>
}

function ChevronIcon({ expanded }: { expanded: boolean }) {
  return <svg className={expanded ? 'project-context__chevron is-expanded' : 'project-context__chevron'} viewBox="0 0 16 16" aria-hidden="true"><path d="m4 6 4 4 4-4" /></svg>
}

function StackIcon() {
  return <svg className="other-running-projects__icon" viewBox="0 0 20 20" aria-hidden="true"><path d="m10 2.5 7 3.8-7 3.8-7-3.8zM3 10l7 3.8 7-3.8M3 13.7l7 3.8 7-3.8" /></svg>
}

function SearchIcon() {
  return <svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5" /><path d="m12.2 12.2 4 4" /></svg>
}
