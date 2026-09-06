import { environmentUIPath } from '../features/environment/navigation'
import { useMemo, useRef, useState } from 'react'
import type { Environment } from '../api/contracts/environments'
import type { Project } from '../api/contracts/projects'
import { CreateEnvironmentDialog } from '../features/projects/CreateEnvironmentDialog'
import { aggregateProjectStatus } from '../features/projects/projectPresentation'
import { runningProjects } from '../features/projects/projectNavigation'
import { StatusMark } from './Status'
import { ProjectSwitcher } from './ProjectSwitcher'

export function ProjectContextNav({ projects, recentProjects, environments, project, activeEnvironment, collapsed, onNavigate, onSwitchProject, onEnvironmentChanged }: {
  projects: Project[]
  recentProjects: Project[]
  environments: Environment[]
  project?: Project
  activeEnvironment?: Environment
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
      {switcherOpen && <ProjectSwitcher projects={projects} recentProjects={recentProjects} environments={environments} project={project} activeEnvironment={activeEnvironment} onClose={closeSwitcher} onManage={() => { setSwitcherOpen(false); onNavigate('/projects') }} onSwitch={(item) => { setSwitcherOpen(false); onSwitchProject(item) }} onOpenEnvironment={(environment) => { setSwitcherOpen(false); onNavigate(environmentUIPath(environment)) }} />}
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
