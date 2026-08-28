import { StatusMark } from '../../components/Status'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { projectRoute } from './projectOperations'
import { formatTimestamp, projectOverview, statusCounts } from './projectPresentation'

export function ProjectsIndexPage({ projects, environments, onNavigate }: {
  projects: Project[]
  environments: Environment[]
  onNavigate: (path: string) => void
}) {
  const projectRows = projects.map((project) => projectOverview(project, environments))
  const counts = statusCounts(projectRows)
  return <div className="page projects-page">
    <div className="page-heading projects-heading">
      <header className="projects-heading__title">
        <div className="projects-heading__line"><h1>Projects</h1></div>
      </header>
      <div className="projects-heading__controls">
        <div className="page-heading__summary"><span>{counts.failed ?? 0} failed</span><b>·</b><span>{counts.degraded ?? 0} degraded</span><b>·</b><span>{counts.recovering ?? 0} recovering</span><b>·</b><span>{counts.starting ?? 0} starting</span><b>·</b><span>{counts.healthy ?? 0} healthy</span></div>
      </div>
    </div>
    {projectRows.length > 0 ? <section className="panel projects-table">
      <div className="panel-title"><span>PROJECTS</span></div>
      <div className="table-row table-row--header project-index-row"><span>Status</span><span>Project</span><span>Environments</span><span>Sources</span><span>Services</span><span>Last updated</span></div>
      {projectRows.map((row) => <button className="table-row project-index-row" key={row.project.name} onClick={() => onNavigate(projectRoute(row.project.name))}>
        <span><StatusMark status={row.status} /></span><strong>{row.project.name}</strong><code className="truncate" title={row.environmentNames}>{row.environmentNames || '—'}</code><span>{row.sourceCount || '—'}</span><span>{row.serviceCount || '—'}</span>{row.updatedAt ? <time dateTime={row.updatedAt}>{formatTimestamp(row.updatedAt)}</time> : <span>—</span>}
      </button>)}
    </section> : <EmptyProjects />}
  </div>
}

function EmptyProjects() {
  return <section className="empty-environment panel"><div><div className="eyebrow">No projects yet</div><h2>Start one repository or assemble several.</h2><p>For one repository, run:</p><pre><span>$</span> portless up</pre><p>For several repositories, create one project and name each source:</p><pre><span>$</span> portless project create billing --source checkout=../checkout --source ledger=../ledger</pre></div></section>
}
