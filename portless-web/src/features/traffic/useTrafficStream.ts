import { useCallback, useEffect, useRef, useState } from 'react'
import type { Environment } from '../../api/contracts/environments'
import type { TrafficTrace } from '../../api/contracts/traffic'
import { createTrafficSession, initialTrafficState } from './trafficSession'

export function useTrafficStream(environment: Environment, edgeFilter: string, expandedTrace: number | null) {
  const [state, setState] = useState(initialTrafficState)
  const session = useRef<ReturnType<typeof createTrafficSession> | null>(null)
  const project = environment.project
  const name = environment.name

  useEffect(() => {
    setState(initialTrafficState())
    const current = createTrafficSession({ project, name }, edgeFilter, setState)
    session.current = current
    return () => {
      current.dispose()
      if (session.current === current) session.current = null
    }
  }, [project, name, edgeFilter])

  useEffect(() => { session.current?.setExpanded(expandedTrace) }, [expandedTrace, project, name, edgeFilter])

  const togglePaused = useCallback(() => session.current?.togglePaused(), [])
  const clearTraffic = useCallback(() => session.current?.clearTraffic(), [])
  const mergeTrace = useCallback((trace: TrafficTrace) => session.current?.mergeTrace(trace), [])
  const dismissError = useCallback(() => session.current?.dismissError(), [])
  const reportError = useCallback((title: string, value: unknown) => session.current?.reportError(title, value), [])

  return { ...state, togglePaused, clearTraffic, mergeTrace, dismissError, reportError }
}
