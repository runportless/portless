import { useEffect, useState } from 'react'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import type { Service } from '../../api/contracts/topology'
import { MocksPanel } from '../mocks'
import { TrafficPanel } from '../traffic'
import { BindingsPanel } from './bindings/BindingsPanel'
import { EnvironmentOverviewHeading } from './EnvironmentHeader'
import { EnvironmentNotices } from './EnvironmentNotices'
import { FaultsPanel } from './faults/FaultsPanel'
import { environmentUIPath, type EnvironmentNavigationOptions, type EnvironmentView } from './navigation'
import { OverviewPanel } from './OverviewPanel'
import { RecordingsPanel } from './recordings/RecordingsPanel'
import { ServiceDrawer } from './service/ServiceDrawer'
import { TimelinePanel } from './timeline/TimelinePanel'
import { TopologyPanel } from './topology/TopologyPanel'
import type { EnvironmentActivity } from './useEnvironmentActivity'
import type { EnvironmentActions } from './useEnvironmentActions'

export function EnvironmentPage({ environment, project, view, activity, actions, mockScenario, mockCreateRoute, mockRoute, onNavigate, onChanged }: {
  environment: Environment
  project?: Project
  view: EnvironmentView
  activity: EnvironmentActivity
  actions: EnvironmentActions
  mockScenario?: string
  mockCreateRoute?: boolean
  mockRoute?: string
  onNavigate: (path: string) => void
  onChanged: () => void
}) {
  const [selectedService, setSelectedService] = useState<Service | null>(null)

  useEffect(() => {
    if (!selectedService) return
    const updated = environment.services.find((service) => service.name === selectedService.name)
    if (updated) setSelectedService(updated)
  }, [environment.services]) // eslint-disable-line react-hooks/exhaustive-deps

  const navigateView = (next: EnvironmentView, options?: EnvironmentNavigationOptions) => onNavigate(environmentUIPath(environment, next, options))
  const activeRecording = activity.recordings.find((recording) => recording.status === 'active')
  const activeFaults = activity.faults.filter((fault) => fault.enabled)
  const ready = environment.services.filter((service) => service.status === 'ready').length
  const trafficCount = environment.services.reduce((sum, service) => sum + (service.recentRequests || 0), 0)
  const workspace = view === 'topology' || (view === 'mocks' && mockScenario)

  return <div className={`page project-page environment-page${workspace ? ' environment-page--workspace' : ''}${view === 'mocks' && mockScenario ? ' environment-page--mock-workspace' : ''}`}>
    {view === 'overview' && <EnvironmentOverviewHeading environment={environment} />}
    <EnvironmentNotices environment={environment} actions={actions} activity={activity} />
    {view === 'overview' && <OverviewPanel environment={environment} actions={actions} timeline={activity.timeline} ready={ready} faults={activeFaults} activeRecording={activeRecording} trafficCount={trafficCount} onService={setSelectedService} onNavigate={navigateView} onChanged={onChanged} />}
    {view === 'topology' && <TopologyPanel environment={environment} faults={activeFaults} onService={setSelectedService} onEdge={(edge) => navigateView('traffic', { edge: `${edge.source}:${edge.target}`, protocol: edge.protocol === 'http' ? 'http' : 'tcp' })} />}
    {view === 'bindings' && <BindingsPanel environment={environment} project={project} onNavigate={onNavigate} onChanged={onChanged} />}
    {view === 'traffic' && <TrafficPanel environment={environment} />}
    {view === 'mocks' && <MocksPanel environment={environment} selectedScenario={mockScenario} creatingRoute={mockCreateRoute} selectedRoute={mockRoute} onSelectRoute={(scenario, route) => navigateView('mocks', { scenario, route })} onCreateRoute={(scenario) => navigateView('mocks', { scenario, createRoute: true })} onSelectScenario={(scenario) => navigateView('mocks', { scenario })} onChanged={onChanged} />}
    {view === 'recordings' && <RecordingsPanel environment={environment} recordings={activity.recordings} refresh={activity.refresh} />}
    {view === 'faults' && <FaultsPanel environment={environment} faults={activity.faults} refresh={activity.refresh} />}
    {view === 'timeline' && <TimelinePanel key={`${environment.project}/${environment.name}`} timeline={activity.timeline} />}
    {selectedService && <ServiceDrawer environment={environment} service={selectedService} onClose={() => setSelectedService(null)} onChanged={onChanged} onNavigate={(path) => { setSelectedService(null); onNavigate(path) }} />}
  </div>
}
