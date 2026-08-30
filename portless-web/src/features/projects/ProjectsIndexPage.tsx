import { useEffect, useMemo, useRef, useState } from 'react'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { FormDialog } from '../../components/overlays/FormDialog'
import { paginateItems, PanelPagination } from '../../components/PanelPagination'
import { SortableGridHeader, type TableSort } from '../../components/SortableTableHeader'
import { StatusMark } from '../../components/Status'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { formatProjectLastOpened, projectIsRunning, recentProjects, type ProjectNavigationPreferences } from './projectNavigation'
import { projectOverview } from './projectPresentation'

type ProjectFilter = 'all' | 'running' | 'recent' | 'hidden'
type ProjectSortField = 'project' | 'runtime' | 'environments' | 'lastOpened'
const defaultProjectSort: TableSort<ProjectSortField> = { key: 'lastOpened', direction: 'desc' }
const projectRegistryPageSize = 10

export function ProjectsIndexPage({ projects, environments, focusedProject, navigation, onOpenProject, onProjectHiddenChange, onForgetProject }: {
  projects: Project[]
  environments: Environment[]
  focusedProject?: string
  navigation: ProjectNavigationPreferences
  onOpenProject: (project: Project) => void
  onProjectHiddenChange: (project: string, hidden: boolean) => void
  onForgetProject: (project: string) => Promise<void>
}) {
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<ProjectFilter>('all')
  const [sort, setSort] = useState<TableSort<ProjectSortField>>(defaultProjectSort)
  const [page, setPage] = useState(0)
  const [menuProject, setMenuProject] = useState('')
  const [forgetProject, setForgetProject] = useState<Project | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const menu = useRef<HTMLDivElement>(null)
  const recent = useMemo(() => recentProjects(projects, environments, navigation), [environments, navigation, projects])
  const recentNames = new Set(recent.map((project) => project.name))
  const hiddenNames = new Set(navigation.hiddenProjects)
  const rows = sortProjectRegistryRows(projects
    .map((project) => ({
      ...projectOverview(project, environments),
      running: projectIsRunning(project, environments),
      openedAt: navigation.lastOpenedByProject[project.name],
      environmentCount: environments.filter((environment) => environment.project === project.name).length,
      focused: focusedProject === project.name,
    }))
    .filter((row) => {
      if (query.trim() && !row.project.name.toLowerCase().includes(query.trim().toLowerCase())) return false
      if (filter === 'running') return row.running
      if (filter === 'recent') return recentNames.has(row.project.name)
      if (filter === 'hidden') return hiddenNames.has(row.project.name)
      return true
    }), sort)
  const pagination = paginateItems(rows, page, projectRegistryPageSize)
  const counts: Record<ProjectFilter, number> = {
    all: projects.length,
    running: projects.filter((project) => projectIsRunning(project, environments)).length,
    recent: recent.length,
    hidden: projects.filter((project) => hiddenNames.has(project.name)).length,
  }

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => { if (event.key === 'Escape') setMenuProject('') }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [])

  useEffect(() => {
    if (!menuProject) return
    const dismissOutside = (event: MouseEvent) => {
      if (!menu.current?.contains(event.target as Node)) setMenuProject('')
    }
    document.addEventListener('mousedown', dismissOutside)
    return () => document.removeEventListener('mousedown', dismissOutside)
  }, [menuProject])

  const forget = async () => {
    if (!forgetProject) return
    setBusy(true); setError(null)
    try {
      await onForgetProject(forgetProject.name)
      setForgetProject(null)
    } catch (reason) {
      setError(actionError("Project couldn't be forgotten", reason))
    } finally { setBusy(false) }
  }

  return <div className="page projects-page project-registry-page">
    <div className="page-heading projects-heading project-registry-heading">
      <header className="projects-heading__title">
        <div className="eyebrow">Workspace</div>
        <div className="projects-heading__line"><h1>Projects</h1></div>
        <p>The sidebar shows one project at a time. Use this page to find, switch, or forget projects.</p>
      </header>
    </div>

    {projects.length > 0 ? <>
      <section className="project-registry-controls" aria-label="Project registry controls">
        <label className="project-registry-search"><SearchIcon /><span className="sr-only">Search projects</span><input value={query} placeholder="Search" autoComplete="off" onChange={(event) => { setQuery(event.target.value); setPage(0); setMenuProject('') }} /></label>
        <div className="project-registry-filters" role="group" aria-label="Filter projects">
          {(['all', 'running', 'recent', 'hidden'] as const).map((item) => <button className={filter === item ? 'is-active' : ''} type="button" aria-pressed={filter === item} key={item} onClick={() => { setFilter(item); setPage(0); setMenuProject('') }}><span>{item}</span><strong>{counts[item]}</strong></button>)}
        </div>
      </section>

      <section className="panel projects-table project-registry-table" aria-label="Projects">
        <div className={`table-row table-row--header project-registry-row sortable-header-row${sort.key === defaultProjectSort.key && sort.direction === defaultProjectSort.direction ? ' is-default-sort' : ''}`} role="row">
          <SortableGridHeader label="Project" sortKey="project" sort={sort} itemCount={rows.length} onSort={(nextSort) => { setSort(nextSort); setPage(0); setMenuProject('') }} />
          <SortableGridHeader label="Runtime" sortKey="runtime" sort={sort} itemCount={rows.length} onSort={(nextSort) => { setSort(nextSort); setPage(0); setMenuProject('') }} />
          <SortableGridHeader label="Environments" sortKey="environments" sort={sort} itemCount={rows.length} onSort={(nextSort) => { setSort(nextSort); setPage(0); setMenuProject('') }} />
          <SortableGridHeader label="Last opened" sortKey="lastOpened" sort={sort} itemCount={rows.length} onSort={(nextSort) => { setSort(nextSort); setPage(0); setMenuProject('') }} />
          <span aria-label="Actions" />
        </div>
        {pagination.items.map((row) => {
          const hidden = hiddenNames.has(row.project.name)
          const focused = row.focused
          const menuOpen = menuProject === row.project.name
          return <div className={focused ? 'table-row project-registry-row project-registry-row--interactive is-focused' : 'table-row project-registry-row project-registry-row--interactive'} role="row" key={row.project.name} onClick={() => onOpenProject(row.project)}>
            <button className="project-registry-row__project" type="button" onClick={(event) => { event.stopPropagation(); onOpenProject(row.project) }}><StatusMark status={row.status} label={false} /><strong>{row.project.name}</strong>{hidden && <small>HIDDEN</small>}</button>
            <span className="project-registry-row__runtime"><StatusMark status={row.status} label={false} /><strong>{focused ? 'active' : row.status}</strong></span>
            <span>{row.environmentCount}</span>
            <time dateTime={row.openedAt}>{formatProjectLastOpened(row.openedAt)}</time>
            <div ref={menuOpen ? menu : undefined} className="project-registry-row__actions">
              <button className="button button--quiet" type="button" onClick={(event) => { event.stopPropagation(); onOpenProject(row.project) }}>OPEN</button>
              <button className="project-registry-row__menu-trigger" type="button" aria-label={`Project actions for ${row.project.name}`} aria-haspopup="menu" aria-expanded={menuOpen} onClick={(event) => { event.stopPropagation(); setMenuProject(menuOpen ? '' : row.project.name) }}>•••</button>
              {menuOpen && <div className="project-registry-row__menu" role="menu" aria-label={`${row.project.name} actions`} onClick={(event) => event.stopPropagation()}>
                <button type="button" role="menuitem" onClick={() => { onProjectHiddenChange(row.project.name, !hidden); setMenuProject('') }}>{hidden ? 'SHOW IN RECENT' : 'HIDE FROM RECENT'}</button>
                <button className="is-danger" type="button" role="menuitem" onClick={() => { setForgetProject(row.project); setMenuProject(''); setError(null) }}>FORGET PROJECT</button>
              </div>}
            </div>
          </div>
        })}
        {rows.length === 0 && <div className="empty-row">No projects match this search and filter.</div>}
        <PanelPagination label="projects" pagination={pagination} onPage={(nextPage) => { setPage(nextPage); setMenuProject('') }} />
      </section>
    </> : <EmptyProjects />}

    {forgetProject && <ForgetProjectDialog project={forgetProject} environments={environments.filter((environment) => environment.project === forgetProject.name)} busy={busy} error={error} onDismissError={() => setError(null)} onClose={() => { if (!busy) { setForgetProject(null); setError(null) } }} onForget={forget} />}
  </div>
}

export function ForgetProjectDialog({ project, environments, busy, error, onDismissError, onClose, onForget }: {
  project: Project
  environments: Environment[]
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onForget: () => Promise<void>
}) {
  const cancelButton = useRef<HTMLButtonElement>(null)
  const activeEnvironments = environments.filter((environment) => environment.status !== 'stopped')
  const blocked = activeEnvironments.length > 0
  const sourceNames = project.sources?.map((source) => source.name) || []
  return <FormDialog
    className="forget-project-modal"
    role="alertdialog"
    titleID="forget-project-title"
    descriptionID="forget-project-description"
    closeLabel="Close forget project"
    closeBlocked={busy}
    initialFocusRef={cancelButton}
    header={<div><div className="eyebrow">PROJECT REGISTRY</div><h2 id="forget-project-title">Forget {project.name}?</h2></div>}
    onClose={onClose}
  >
    <div className="project-forget-content">
      <p id="forget-project-description">This permanently removes the project definition and all retained Portless application state. Source files and checkouts on disk are not deleted.</p>
      <div className="project-forget-impact">
        <div><span className="eyebrow">ENVIRONMENTS</span><strong>{environments.length ? environments.map((environment) => `${environment.name} · ${environment.status}`).join(', ') : 'No environments'}</strong></div>
        <div><span className="eyebrow">SOURCES</span><strong>{sourceNames.length ? sourceNames.join(', ') : 'No sources'}</strong></div>
        <div><span className="eyebrow">RETAINED STATE</span><strong>Timelines, traffic, mocks, recordings, faults, and provider bindings</strong></div>
      </div>
      {blocked && <p className="source-modal-note source-modal-note--danger">Stop every environment first: {activeEnvironments.map((environment) => environment.name).join(', ')}.</p>}
    </div>
    {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
    <footer><button ref={cancelButton} className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--danger" type="button" disabled={busy || blocked} onClick={() => void onForget()}>{busy ? 'FORGETTING…' : 'FORGET PROJECT'}</button></footer>
  </FormDialog>
}

function EmptyProjects() {
  return <section className="empty-environment panel"><div><div className="eyebrow">No projects yet</div><h2>Start one repository or assemble several.</h2><p>For one repository, run:</p><pre><span>$</span> portless up</pre><p>For several repositories, create one project and name each source:</p><pre><span>$</span> portless project create billing --source checkout=../checkout --source ledger=../ledger</pre></div></section>
}

function timestamp(value?: string) {
  const result = value ? new Date(value).getTime() : 0
  return Number.isFinite(result) ? result : 0
}

type ProjectRegistryRow = ReturnType<typeof projectOverview> & {
  running: boolean
  openedAt?: string
  environmentCount: number
  focused: boolean
}

export function sortProjectRegistryRows(rows: ProjectRegistryRow[], sort: TableSort<ProjectSortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...rows].sort((left, right) => {
    const projectOrder = compareProjectRegistryText(left.project.name, right.project.name)
    let order = 0
    switch (sort.key) {
      case 'project':
        order = projectOrder
        break
      case 'runtime':
        order = compareProjectRegistryText(left.focused ? 'active' : left.status, right.focused ? 'active' : right.status)
        break
      case 'environments':
        order = left.environmentCount - right.environmentCount
        break
      case 'lastOpened':
        order = timestamp(left.openedAt) - timestamp(right.openedAt)
        break
    }
    return direction * order || projectOrder
  })
}

function compareProjectRegistryText(left: string, right: string) {
  return left.localeCompare(right, undefined, { sensitivity: 'base', numeric: true })
}

function SearchIcon() {
  return <svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5" /><path d="m12.2 12.2 4 4" /></svg>
}
