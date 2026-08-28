import { useEffect, useState } from 'react'
import type { Environment } from '../../../api/contracts/environments'
import type { FaultRule } from '../../../api/contracts/experiments'
import type { Service } from '../../../api/contracts/topology'
import { TopologyCanvas } from './TopologyCanvas'
import type { TopologyEdge } from './topologyModel'

type TopologyProps = {
  environment: Environment
  faults: FaultRule[]
  onService: (service: Service) => void
  onEdge: (edge: TopologyEdge) => void
}

export function TopologyPanel({ environment, faults, onService, onEdge }: TopologyProps) {
  const [paused, setPaused] = useState(false)
  const [maximized, setMaximized] = useState(false)
  const [centerRequest, setCenterRequest] = useState(0)

  useEffect(() => {
    if (!maximized) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !document.querySelector('.drawer-backdrop')) setMaximized(false)
    }
    window.addEventListener('keydown', keydown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', keydown)
    }
  }, [maximized])

  return <section className={`panel topology-panel topology-panel--page${maximized ? ' topology-panel--maximized' : ''}`} aria-label="Service topology">
    <div className="panel-title topology-toolbar"><span>TOPOLOGY</span><div><TopologyLiveButton paused={paused} onToggle={() => setPaused((value) => !value)} /><TopologyCenterButton onCenter={() => setCenterRequest((value) => value + 1)} /><button className={maximized ? 'icon-button' : 'topology-size-button'} type="button" title={`${maximized ? 'Restore' : 'Maximize'} topology`} aria-label={`${maximized ? 'Restore' : 'Maximize'} topology`} aria-pressed={maximized} onClick={() => setMaximized((value) => !value)}>{maximized ? '×' : <TopologySizeIcon />}</button></div></div>
    <TopologyCanvas environment={environment} faults={faults} paused={paused} centerRequest={centerRequest} onService={onService} onEdge={onEdge} />
  </section>
}

export function TopologyPreview({ environment, faults, onService, onEdge, onOpen }: TopologyProps & { onOpen: () => void }) {
  const [paused, setPaused] = useState(false)
  const [centerRequest, setCenterRequest] = useState(0)
  return <section className="panel topology-panel topology-panel--preview" aria-label="Service topology">
    <div className="panel-title topology-toolbar"><span>TOPOLOGY</span><div><TopologyLiveButton paused={paused} onToggle={() => setPaused((value) => !value)} /><TopologyCenterButton onCenter={() => setCenterRequest((value) => value + 1)} /><button className="topology-size-button" type="button" title="Open topology" aria-label="Open topology" onClick={onOpen}><TopologySizeIcon /></button></div></div>
    <TopologyCanvas environment={environment} faults={faults} paused={paused} centerRequest={centerRequest} onService={onService} onEdge={onEdge} />
  </section>
}

function TopologyLiveButton({ paused, onToggle }: { paused: boolean; onToggle: () => void }) {
  return <button className={`topology-live${paused ? ' is-paused' : ''}`} type="button" title={paused ? 'Resume live topology' : 'Pause live topology'} onClick={onToggle}>{paused ? <svg className="topology-live__pause" viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg> : <i className="topology-live__dot" aria-hidden="true" />}{paused ? 'PAUSED' : 'LIVE'}</button>
}

function TopologyCenterButton({ onCenter }: { onCenter: () => void }) {
  return <button className="topology-center-button" type="button" title="Center topology" aria-label="Center topology" onClick={onCenter}><TopologyCenterIcon /></button>
}

function TopologyCenterIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="3" /><path d="M8 1v3M8 12v3M1 8h3M12 8h3" /></svg>
}

function TopologySizeIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4" /></svg>
}
