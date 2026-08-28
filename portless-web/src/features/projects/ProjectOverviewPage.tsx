import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { ProjectEnvironmentsPanel } from './ProjectEnvironmentsPanel'
import { ProjectSourcesPanel } from './ProjectSourcesPanel'

export function ProjectOverviewPage({ project, environments, onNavigate, onChanged }: {
  project: Project
  environments: Environment[]
  onNavigate: (path: string) => void
  onChanged: () => Promise<void>
}) {
  const projectEnvironments = environments.filter((environment) => environment.project === project.name)
  return <div className="page projects-page">
    <ProjectEnvironmentsPanel project={project} environments={projectEnvironments} onNavigate={onNavigate} onChanged={onChanged} />
    <ProjectSourcesPanel project={project} environments={projectEnvironments} onChanged={onChanged} />
  </div>
}
