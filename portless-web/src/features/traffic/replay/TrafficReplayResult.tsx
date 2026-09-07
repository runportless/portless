import { useEffect, useId, useState } from 'react'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficComparisonSection, TrafficReplayWorkspace } from '../../../api/contracts/traffic_replay'
import { ActionErrorNotice } from '../../../components/ActionError'
import { CopyIcon, formatTrafficBytes, TrafficTextContent } from '../detail/TrafficFormatting'
import type { TrafficPayloadView } from '../detail/trafficDetailTypes'
import { formattedTrafficHeaders, highlightedTrafficHeaders, rawTrafficMessage } from '../protocols/HttpTrafficDetail'
import { ReplayDifferenceDetails } from './ReplayDifferenceDetails'
import { ReplayTabs } from './ReplayTabs'

export function TrafficReplayResult({ workspace, outdated }: { workspace: TrafficReplayWorkspace; outdated: boolean }) {
  const [view, setView] = useState<'original' | 'replayed' | 'diff'>('original')
  const [representation, setRepresentation] = useState<TrafficPayloadView>('body')
  const [differencesExpanded, setDifferencesExpanded] = useState(true)
  const panelID = useId()
  const result = workspace.result
  const runNumber = result?.runNumber
  useEffect(() => { if (runNumber !== undefined) setView('diff') }, [runNumber])
  const selected = view === 'original' ? workspace.baseline : result?.exchange
  return <section className="replay-result" aria-label="Replay response comparison">
    <div className="replay-section-heading"><h3>RESPONSE</h3>{result && <span role="status">{outdated ? 'From previous request' : `Run ${result.runNumber}`}</span>}</div>
    <ReplayTabs label="Response comparison views" value={view} options={[{ value: 'original', label: 'Original' }, { value: 'replayed', label: 'Replayed' }, { value: 'diff', label: 'Response diff' }]} onChange={setView} panelID={panelID} />
    <div className="replay-result__content" id={panelID} role="tabpanel" aria-label={view === 'diff' ? 'Response diff' : `${view} response`}>
      {view === 'diff' && result && <div className="replay-result__summary"><span>STATUS <strong>{workspace.baseline?.status || '—'} → {result.exchange?.status || '—'}</strong></span><span>DURATION <strong>{workspace.baseline?.durationMs ?? '—'} → {result.exchange?.durationMs ?? '—'} ms</strong></span><span>CHANGE <strong>{result.comparison.durationDeltaMs > 0 ? '+' : ''}{result.comparison.durationDeltaMs} ms</strong></span></div>}
      {workspace.run?.error && <ActionErrorNotice error={{ title: workspace.run.outcome === 'unknown' ? 'Delivery outcome is unknown' : 'Replay request failed', message: workspace.run.error }} />}
      {workspace.run?.outcome === 'unknown' && <p className="replay-notice" role="note">The request may have reached the application. It will not be sent again automatically.</p>}
      {!!result?.limitations?.length && <ul className="replay-limitations">{result.limitations.map((limitation, index) => <li key={`${limitation.code}-${index}`}>{limitation.message}</li>)}</ul>}
      <div className="replay-result__representation">
        {view === 'diff' ? result ? <ReplayDifference section={representation === 'raw' ? undefined : result.comparison[representation]} original={workspace.baseline} replayed={result.exchange} representation={representation} onRepresentation={setRepresentation} expanded={differencesExpanded} onExpandedChange={setDifferencesExpanded} /> : <p className="replay-empty">Send a request to compare the captured response with a new response.</p>
          : selected ? <ReplayResponse exchange={selected} representation={representation} onRepresentation={setRepresentation} label={view === 'original' ? 'Original' : 'Replayed'} /> : <p className="replay-empty">No replay response available.</p>}
      </div>
    </div>
  </section>
}

export function replayResponseText(exchange: TrafficExchange, representation: TrafficPayloadView) {
  // Preserve JSON numbers, duplicate keys, whitespace and original spelling.
  return representation === 'raw' ? rawTrafficMessage(exchange, 'response') : representation === 'headers' ? formattedTrafficHeaders(exchange.responseHeaders) : exchange.responseBody || ''
}

function ReplayResponse({ exchange, representation, onRepresentation, label }: { exchange: TrafficExchange; representation: TrafficPayloadView; onRepresentation: (view: TrafficPayloadView) => void; label: string }) {
  const panelID = useId()
  const text = replayResponseText(exchange, representation)
  const capture = exchange.responseCapture
  const contentType = Object.entries(exchange.responseHeaders || {}).find(([name]) => name.toLowerCase() === 'content-type')?.[1]?.[0]
  return <section className="replay-response" aria-label={`${label} ${representation}`}>
    <div className="traffic-message-workbench__summary replay-response__heading">
      <code>{exchange.status ? `HTTP ${exchange.status}` : 'No HTTP response'}<span>{label} · {exchange.durationMs} ms</span></code>
      <div>{contentType && <span>{contentType}</span>}<span>{formatTrafficBytes(Math.max(0, exchange.responseBytes))}</span><button type="button" className="traffic-copy-button" aria-label={`Copy ${label.toLowerCase()} response ${representation}`} onClick={() => void navigator.clipboard.writeText(text).catch(() => undefined)}><CopyIcon /><span>COPY</span></button></div>
    </div>
    <ReplayTabs className="traffic-payload-tabs" label="Replay response representation" value={representation} options={[{ value: 'body', label: 'Body' }, { value: 'headers', label: 'Headers' }, { value: 'raw', label: 'Raw' }]} onChange={onRepresentation} panelID={panelID} />
    <div className="replay-response__body" id={panelID} role="tabpanel" tabIndex={0} aria-label={`${label} response ${representation}`}>
      {text ? representation === 'body'
        ? <TrafficTextContent content={text} contentType={contentType} />
        : <pre className={representation === 'headers' ? 'traffic-headers' : undefined}>{representation === 'headers' ? highlightedTrafficHeaders(text) : text}</pre>
        : <p className="replay-empty">{capture?.state === 'empty' ? 'The response has no body.' : 'No response content was retained.'}</p>}
    </div>
  </section>
}

function ReplayDifference({ section, original, replayed, representation, onRepresentation, expanded, onExpandedChange }: { section?: TrafficComparisonSection; original?: TrafficExchange; replayed?: TrafficExchange; representation: TrafficPayloadView; onRepresentation: (view: TrafficPayloadView) => void; expanded: boolean; onExpandedChange: (expanded: boolean) => void }) {
  return <div className="replay-diff">
    {section && <ReplayDifferenceDetails section={section} representation={representation} expanded={expanded} onExpandedChange={onExpandedChange} />}
    <div className="replay-diff__panes">{original && <ReplayResponse exchange={original} representation={representation} onRepresentation={onRepresentation} label="Original" />}{replayed && <ReplayResponse exchange={replayed} representation={representation} onRepresentation={onRepresentation} label="Replayed" />}</div>
  </div>
}
