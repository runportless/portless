import { useEffect, useId, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { api, connectEvents, environmentPath } from '../../../api'
import { StatusMark } from '../../../components/Status'
import type { Environment } from '../../../api/contracts/environments'
import type { FaultRule } from '../../../api/contracts/experiments'
import type { Service } from '../../../api/contracts/topology'
import type { TrafficExchangeList } from '../../../api/contracts/traffic'
import { bindingFor, displayLaunchMode, overviewServiceEndpoint, publicEndpoint } from '../service/servicePresentation'
import {
  buildTopology,
  mergeTopologySignal,
  summarizeTopologyTraffic,
  topologyActiveArrowSize,
  topologyActiveEdgeVisual,
  topologyCenterPosition,
  topologyEdgeKey,
  topologyEdgeLabel,
  topologyEdgeTone,
  topologyEdgeVisualState,
  topologyErrorEdgeVisual,
  topologyInactiveArrowSize,
  topologyInactiveEdgeVisual,
  topologyPanPosition,
  topologyParticleMotion,
  topologyWarningEdgeVisual,
  type TopologyEdge,
  type TopologyEdgeMetric,
  type TopologySignal,
} from './topologyModel'

type TopologyPreviewTarget = { name: string; left: number; top: number; onLeft: boolean }

export function TopologyCanvas({ environment, faults, paused, centerRequest, onService, onEdge }: {
  environment: Environment
  faults: FaultRule[]
  paused: boolean
  centerRequest: number
  onService: (service: Service) => void
  onEdge: (edge: TopologyEdge) => void
}) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const handledCenterRequest = useRef(centerRequest)
  const pan = useRef<{ pointerId: number; clientX: number; clientY: number; scrollLeft: number; scrollTop: number; dragging: boolean } | null>(null)
  const suppressClick = useRef(false)
  const [isPanning, setIsPanning] = useState(false)
  const [hoveredPreview, setHoveredPreview] = useState<TopologyPreviewTarget>()
  const [focusedPreview, setFocusedPreview] = useState<TopologyPreviewTarget>()
  const [edgeMetrics, setEdgeMetrics] = useState<Map<string, TopologyEdgeMetric>>(new Map())
  const [now, setNow] = useState(Date.now())
  const previewID = useId()
  const environmentIdentity = useMemo(() => ({ project: environment.project, name: environment.name }), [environment.project, environment.name])
  const { levels, edges } = buildTopology(environment)
  const rowGap = 48
  const nodeWidth = 164
  const nodeHeight = 72
  const columnGap = 112
  const sidePadding = 54
  const verticalPadding = 40
  const positions = new Map<string, { x: number; y: number }>()
  const widestLevel = Math.max(1, ...levels.map((level) => level.length))
  const width = sidePadding * 2 + levels.length * nodeWidth + Math.max(0, levels.length - 1) * columnGap
  const height = Math.max(280, verticalPadding * 2 + widestLevel * nodeHeight + Math.max(0, widestLevel - 1) * rowGap)
  levels.forEach((level, depth) => {
    const columnHeight = level.length * nodeHeight + Math.max(0, level.length - 1) * rowGap
    const start = (height - columnHeight) / 2
    level.forEach((item, index) => positions.set(item.key, { x: sidePadding + depth * (nodeWidth + columnGap), y: start + index * (nodeHeight + rowGap) }))
  })
  const activeFaultEdges = useMemo(() => new Map(faults.map((fault) => [topologyEdgeKey(fault.source, fault.target), fault.name])), [faults])
  const previewTarget = focusedPreview || hoveredPreview
  const previewServiceName = previewTarget?.name || ''
  const previewService = environment.services.find((service) => service.name === previewServiceName)
  const previewDetails = previewService ? topologyServicePreviewDetails(environment, previewService, edges, faults) : undefined
  const previewRelatedItems = new Set<string>()
  if (previewServiceName) {
    previewRelatedItems.add(previewServiceName)
    for (const edge of edges) {
      if (edge.source === previewServiceName) previewRelatedItems.add(edge.target)
      if (edge.target === previewServiceName) previewRelatedItems.add(edge.source)
    }
  }

  useEffect(() => {
    let active = true
    api<TrafficExchangeList>(environmentPath(environmentIdentity, '/traffic/exchanges?protocol=all&limit=1000')).then((result) => {
      if (active) setEdgeMetrics(summarizeTopologyTraffic(result.exchanges))
    }).catch(() => undefined)
    return () => { active = false }
  }, [environmentIdentity])

  useEffect(() => {
    setHoveredPreview(undefined)
    setFocusedPreview(undefined)
  }, [environment.project, environment.name])

  useEffect(() => {
    if (paused) return
    return connectEvents(environmentIdentity, ['traffic.exchange', 'traffic.tcp.activity'], (type, value) => {
      if (type.startsWith('traffic.')) setEdgeMetrics((metrics) => mergeTopologySignal(metrics, value as TopologySignal))
    })
  }, [environmentIdentity, paused])

  useEffect(() => {
    if (paused) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [paused])

  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport) return
    const frame = requestAnimationFrame(() => {
      const position = topologyCenterPosition(viewport)
      viewport.scrollTo({ left: position.scrollLeft, top: position.scrollTop })
    })
    return () => cancelAnimationFrame(frame)
  }, [environment.project, environment.name])

  useEffect(() => {
    if (handledCenterRequest.current === centerRequest) return
    handledCenterRequest.current = centerRequest
    const viewport = viewportRef.current
    if (!viewport) return
    const position = topologyCenterPosition(viewport)
    const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
    viewport.scrollTo({ left: position.scrollLeft, top: position.scrollTop, behavior: reducedMotion ? 'auto' : 'smooth' })
  }, [centerRequest])

  const startPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement
    if (event.button !== 0 || target.closest('.topology__edge-action')) return
    const viewport = event.currentTarget
    pan.current = {
      pointerId: event.pointerId,
      clientX: event.clientX,
      clientY: event.clientY,
      scrollLeft: viewport.scrollLeft,
      scrollTop: viewport.scrollTop,
      dragging: false,
    }
  }

  const movePan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const origin = pan.current
    if (!origin || origin.pointerId !== event.pointerId) return
    const deltaX = event.clientX - origin.clientX
    const deltaY = event.clientY - origin.clientY
    if (!origin.dragging && Math.hypot(deltaX, deltaY) < 4) return
    if (!origin.dragging) {
      origin.dragging = true
      suppressClick.current = true
      event.currentTarget.setPointerCapture(event.pointerId)
      setIsPanning(true)
    }
    const next = topologyPanPosition(origin, event.clientX, event.clientY)
    event.currentTarget.scrollLeft = next.scrollLeft
    event.currentTarget.scrollTop = next.scrollTop
    event.preventDefault()
  }

  const stopPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const origin = pan.current
    if (origin?.pointerId !== event.pointerId) return
    pan.current = null
    setIsPanning(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
    if (origin.dragging) window.setTimeout(() => { suppressClick.current = false }, 0)
  }

  const selectService = (service: Service) => {
    setHoveredPreview(undefined)
    setFocusedPreview(undefined)
    if (suppressClick.current) {
      suppressClick.current = false
      return
    }
    onService(service)
  }

  const targetServicePreview = (service: Service, node: HTMLElement): TopologyPreviewTarget => {
    const viewport = viewportRef.current
    const canvas = node.closest<HTMLElement>('.topology__canvas')
    if (!viewport || !canvas) return { name: service.name, left: 0, top: 0, onLeft: false }
    return { name: service.name, ...topologyServicePreviewPlacement(node.getBoundingClientRect(), viewport.getBoundingClientRect(), canvas.getBoundingClientRect()) }
  }

  const selectEdge = (edge: TopologyEdge) => {
    if (suppressClick.current) {
      suppressClick.current = false
      return
    }
    onEdge(edge)
  }

  return <div
    ref={viewportRef}
    className={`topology${isPanning ? ' is-panning' : ''}`}
    tabIndex={0}
    aria-label="Topology canvas; drag to pan"
    onPointerDown={startPan}
    onPointerMove={movePan}
    onPointerUp={stopPan}
    onPointerCancel={stopPan}
    onLostPointerCapture={stopPan}
  ><div className="topology__pan-surface"><div className="topology__canvas" style={{ width, height }}>
    <svg className="topology__edges" width={width} height={height} aria-hidden="true">
      <defs>
        <marker className="topology-marker--inactive" id={topologyInactiveEdgeVisual.markerID} viewBox="0 0 8 8" refX="7" refY="4" markerUnits="userSpaceOnUse" markerWidth={topologyInactiveArrowSize} markerHeight={topologyInactiveArrowSize} orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker>
        <marker className="topology-marker--active" id={topologyActiveEdgeVisual.markerID} viewBox="0 0 8 8" refX="7" refY="4" markerUnits="userSpaceOnUse" markerWidth={topologyActiveArrowSize} markerHeight={topologyActiveArrowSize} orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker>
        <marker className="topology-marker--warning" id={topologyWarningEdgeVisual.markerID} viewBox="0 0 8 8" refX="7" refY="4" markerUnits="userSpaceOnUse" markerWidth={topologyActiveArrowSize} markerHeight={topologyActiveArrowSize} orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker>
        <marker className="topology-marker--error" id={topologyErrorEdgeVisual.markerID} viewBox="0 0 8 8" refX="7" refY="4" markerUnits="userSpaceOnUse" markerWidth={topologyActiveArrowSize} markerHeight={topologyActiveArrowSize} orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker>
      </defs>
      {edges.map((edge) => {
        const from = positions.get(edge.source)
        const to = positions.get(edge.target)
        if (!from || !to) return null
        const startX = from.x + nodeWidth
        const startY = from.y + nodeHeight / 2
        const endX = to.x
        const endY = to.y + nodeHeight / 2
        const middleX = (startX + endX) / 2
        const middleY = (startY + endY) / 2
        const edgeKey = topologyEdgeKey(edge.source, edge.target)
        const metric = edgeMetrics.get(edgeKey)
        const activeFault = activeFaultEdges.get(edgeKey)
        const tone = topologyEdgeTone(metric, !!activeFault, now)
        const path = `M ${startX} ${startY} C ${middleX} ${startY}, ${middleX} ${endY}, ${endX} ${endY}`
        const particleMotion = topologyParticleMotion(metric, now)
        const visualState = topologyEdgeVisualState(metric, now, !!activeFault)
        const previewClass = !previewServiceName ? '' : edge.source === previewServiceName || edge.target === previewServiceName ? ' is-preview-connected' : ' is-preview-dimmed'
        return <g key={`${edge.source}:${edge.target}`} data-source={edge.source} data-target={edge.target} className={`topology-edge topology-edge--${tone}${previewClass}`}>
          <path className="topology-edge__line" d={path} style={{ strokeWidth: visualState.strokeWidth, markerEnd: `url(#${visualState.markerID})` }} />
          {!paused && Array.from({ length: particleMotion.count }, (_, index) => <circle key={index} className="topology-edge__pulse" r="3"><animateMotion dur={`${particleMotion.durationSeconds}s`} begin={`${-(index * particleMotion.durationSeconds / particleMotion.count)}s`} repeatCount="indefinite" path={path} /></circle>)}
          <text x={middleX} y={middleY - 10}>{topologyEdgeLabel(edge, metric, now, activeFault)}</text>
        </g>
      })}
    </svg>
    <svg className="topology__edge-actions" width={width} height={height} aria-label="Topology connections">
      {edges.map((edge) => {
        const from = positions.get(edge.source)
        const to = positions.get(edge.target)
        if (!from || !to) return null
        const startX = from.x + nodeWidth
        const startY = from.y + nodeHeight / 2
        const endX = to.x
        const endY = to.y + nodeHeight / 2
        const middleX = (startX + endX) / 2
        const middleY = (startY + endY) / 2
        return <g key={`${edge.source}:${edge.target}`} className="topology__edge-action" role="button" tabIndex={0} aria-label={`Inspect traffic from ${edge.source} to ${edge.target}`} onClick={() => selectEdge(edge)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectEdge(edge) } }}>
          <path className="topology__edge-hit" d={`M ${startX} ${startY} C ${middleX} ${startY}, ${middleX} ${endY}, ${endX} ${endY}`} />
          <rect className="topology__edge-label-hit" x={middleX - 54} y={middleY - 30} width="108" height="28" rx="6" />
        </g>
      })}
    </svg>
    {levels.flat().map((item) => {
      const position = positions.get(item.key)!
      const previewClass = !previewServiceName ? '' : item.key === previewServiceName ? ' is-preview-active' : previewRelatedItems.has(item.key) ? ' is-preview-related' : ' is-preview-dimmed'
      if (item.kind === 'client') return <div key={item.key} className={`topology__external topology__item${previewClass}`} style={{ left: position.x, top: position.y }}><span>INGRESS</span><strong>browser / client</strong><small>localhost</small></div>
      const service = item.service
      return <button
        key={item.key}
        data-service={service.name}
        style={{ left: position.x, top: position.y }}
        className={`topology-node topology__item topology-node--${service.kind}${service.name === environment.primaryService ? ' is-primary' : ''}${previewClass}`}
        aria-describedby={previewServiceName === service.name ? previewID : undefined}
        onMouseEnter={(event) => setHoveredPreview(targetServicePreview(service, event.currentTarget))}
        onMouseLeave={() => setHoveredPreview((current) => current?.name === service.name ? undefined : current)}
        onFocus={(event) => setFocusedPreview(targetServicePreview(service, event.currentTarget))}
        onBlur={() => setFocusedPreview((current) => current?.name === service.name ? undefined : current)}
        onClick={() => selectService(service)}
      >
        <span><StatusMark status={service.status} label={false} />{service.kind === 'resource' ? service.resource?.type : service.framework}</span><strong>{service.name}</strong><small>{publicEndpoint(service)?.url.replace(/^[a-z]+:\/\//, '') || service.status}</small>
      </button>
    })}
    {previewService && previewDetails && previewTarget && <aside
      id={previewID}
      className={`topology-service-preview${previewTarget.onLeft ? ' topology-service-preview--left' : ''}`}
      role="tooltip"
      style={{ left: previewTarget.left, top: previewTarget.top }}
    >
      <div className="topology-service-preview__heading"><strong>{previewService.name}</strong><StatusMark status={previewService.status} /></div>
      <div className="topology-service-preview__mode">{previewDetails.type} · {previewDetails.mode}</div>
      {previewDetails.endpoint && <><span className="topology-service-preview__label">ENDPOINT</span><span className="topology-service-preview__endpoint" title={previewDetails.endpoint}>{previewDetails.endpoint}</span></>}
      <div className="topology-service-preview__metrics"><span>{previewService.recentRequests} recent requests</span><span>{previewService.p95Millis ? `${previewService.p95Millis}ms p95` : '— p95'}</span>{previewService.restartCount > 0 && <span className="warning-text">{previewService.restartCount} restarts</span>}</div>
      <div className="topology-service-preview__connections">{previewDetails.inbound} inbound · {previewDetails.outbound} outbound</div>
      {previewService.reason && <div className="topology-service-preview__notice topology-service-preview__notice--danger">{previewService.reason}</div>}
      {!previewService.reason && previewDetails.fault && <div className="topology-service-preview__notice">▲ {previewDetails.fault}</div>}
      <div className="topology-service-preview__footer">Click node for full details →</div>
    </aside>}
  </div></div></div>
}

export function topologyServicePreviewDetails(environment: Environment, service: Service, edges: TopologyEdge[], faults: FaultRule[]) {
  const provider = bindingFor(environment, service.name)?.provider
  const displayedMode = displayLaunchMode(environment, service)
  const relevantFaults = faults.filter((fault) => fault.enabled && (fault.source === service.name || fault.target === service.name))
  const fault = relevantFaults.length === 1
    ? `${relevantFaults[0]!.name} on ${relevantFaults[0]!.source} → ${relevantFaults[0]!.target}`
    : relevantFaults.length > 1 ? `${relevantFaults.length} active faults` : ''
  return {
    type: service.kind === 'resource' ? service.resource?.type || 'resource' : service.framework || 'process',
    mode: displayedMode === '—' ? provider || service.launchMode || service.kind : displayedMode,
    endpoint: overviewServiceEndpoint(environment, service),
    inbound: edges.filter((edge) => edge.target === service.name).length,
    outbound: edges.filter((edge) => edge.source === service.name).length,
    fault,
  }
}

export function topologyServicePreviewPlacement(
  node: { left: number; right: number; top: number },
  viewport: { left: number; right: number; top: number; bottom: number },
  canvas: { left: number; top: number },
) {
  const width = 286
  const estimatedHeight = 210
  const gap = 14
  const inset = 10
  const rightLimit = viewport.right - inset
  const leftLimit = viewport.left + inset
  let onLeft = node.right + gap + width > rightLimit
  let screenLeft = onLeft ? node.left - gap - width : node.right + gap
  screenLeft = Math.max(leftLimit, Math.min(rightLimit - width, screenLeft))
  onLeft = screenLeft + width <= node.left
  const screenTop = Math.max(viewport.top + inset, Math.min(viewport.bottom - inset - estimatedHeight, node.top - 28))
  return { left: Math.round(screenLeft - canvas.left), top: Math.round(screenTop - canvas.top), onLeft }
}
