import { duration } from '../../../components/Status'
import type { ComponentBinding } from '../../../api/contracts/topology'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import { formatTrafficBytes } from './TrafficFormatting'

function OverviewDetail({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>
}

export function trafficTargetBinding(exchange: TrafficExchange, binding?: ComponentBinding) {
  const provider = exchange.targetProvider || binding?.provider
  const matchingBinding = binding && (!exchange.targetProvider || binding.provider === exchange.targetProvider) ? binding : undefined
  let configuration = exchange.target
  if (provider === 'local') configuration = matchingBinding?.source || exchange.target
  if (provider === 'container') configuration = 'Portless managed'
  if (provider === 'remote') configuration = matchingBinding?.remote?.url || (exchange.remoteClassification ? `${exchange.remoteClassification} target` : exchange.target)
	if (provider === 'mock') configuration = matchingBinding?.mock?.scenario || exchange.mockScenario || exchange.target
  return [configuration, provider].filter(Boolean).join(' · ') || 'not reported'
}

export function trafficStartedTime(timestamp: string) {
  return new Date(timestamp).toLocaleTimeString([], {
    hour: 'numeric', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3,
  })
}

export function TrafficOverview({ exchange, targetBinding }: { exchange: TrafficExchange; targetBinding?: ComponentBinding }) {
  const tcp = exchange.tcp
  const showTCPNotice = exchange.protocol !== 'http' && (!tcp || tcp.kind === 'session' || (tcp.inspection !== 'decoded' && tcp.inspection !== 'limited'))
  return <section className="traffic-overview" aria-label="Exchange overview">
    <div className="traffic-overview__panel">
      <div className="traffic-overview__heading">OVERVIEW</div>
      <div className="traffic-overview__context">
        <OverviewDetail label="ENVIRONMENT" value={exchange.environment} />
        <OverviewDetail label="TARGET BINDING" value={trafficTargetBinding(exchange, targetBinding)} />
        <OverviewDetail label="STARTED" value={trafficStartedTime(exchange.startedAt)} />
        <OverviewDetail label="COMPLETED" value={duration(exchange.durationMs)} />
      </div>
    </div>
    {exchange.error && <div className="traffic-detail__error"><span>{exchange.protocol === 'http' ? 'REQUEST ERROR' : 'OPERATION ERROR'}</span><strong>{exchange.error}</strong></div>}
    {showTCPNotice && <section className="traffic-tcp-summary"><span>{tcp?.inspection?.toUpperCase() || 'TCP SESSION'}</span><strong>{tcp?.inspectionReason || 'Application protocol details are not available for this connection.'}</strong><small>{formatTrafficBytes(Math.max(0, exchange.requestBytes))} sent · {formatTrafficBytes(Math.max(0, exchange.responseBytes))} received</small></section>}
  </section>
}

export function TrafficInterventionBadges({ exchange }: { exchange: TrafficExchange }) {
	const mock = [exchange.mockScenario, exchange.mockRoute].filter(Boolean).join(' / ')
  if (!exchange.fault && !exchange.recording && !mock) return null
  return <div className="traffic-intervention-badges" role="list" aria-label="Exchange interventions">
    {exchange.fault && <span className="traffic-intervention-badge traffic-intervention-badge--fault" role="listitem" aria-label={`FAULT ${exchange.fault}`}><b>FAULT</b><span>{exchange.fault}</span></span>}
    {exchange.recording && <span className="traffic-intervention-badge traffic-intervention-badge--recording" role="listitem" aria-label={`RECORDING ${exchange.recording}`}><b>RECORDING</b><span>{exchange.recording}</span></span>}
    {mock && <span className="traffic-intervention-badge traffic-intervention-badge--mock" role="listitem" aria-label={`MOCK ${mock}`}><b>MOCK</b><span>{mock}</span></span>}
  </div>
}
