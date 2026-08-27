import type { ReactNode } from 'react'
import { duration } from '../../../components/Status'
import type { TrafficExchange } from '../../../types'
import { captureSummary, CopyIcon } from './TrafficFormatting'
import type { TrafficDetailView, TrafficDirection } from './trafficDetailTypes'

export type ProtocolField = { name: string; value: string }

export type ProtocolMessagePresentation = {
  label: 'COMMAND' | 'RESULT'
  title: string
  showTitle: boolean
  type: string
  content: string
  contentType: string
  binary: boolean
  fields: ProtocolField[]
  truncated: boolean
  contentBytes: number
  capturedBytes: number
  meta?: string
  emptyText: string
}

export function ProtocolMessageCard({ direction, presentation, content, table = false, copy }: {
  direction: TrafficDirection
  presentation: ProtocolMessagePresentation
  content?: ReactNode
  table?: boolean
  copy?: { label: string; action: () => void }
}) {
  const meta = presentation.meta || presentation.contentType || presentation.type.toUpperCase() || 'not captured'
  return <section className={`traffic-semantic-card traffic-semantic-card--${direction}`} aria-label={`${presentation.label.toLowerCase()} summary`}>
    <div className="traffic-semantic-card__header"><span>{presentation.label}</span><div><small>{meta}</small>{copy && <button className="traffic-copy-button" type="button" onClick={copy.action} aria-label={copy.label} title={copy.label}><CopyIcon />COPY</button>}</div></div>
    <div className={`traffic-semantic-card__body${table ? ' traffic-semantic-card__body--table' : ''}`}>
      {presentation.showTitle && <strong>{presentation.title}</strong>}
      {content}
      {presentation.fields.length > 0 && <dl>{presentation.fields.map((field, index) => <div key={`${field.name}:${index}`}><dt>{field.name}</dt><dd>{field.value}</dd></div>)}</dl>}
      {presentation.binary && <p>Binary payload captured.</p>}
      {!content && presentation.fields.length === 0 && !presentation.binary && <p>{presentation.emptyText}</p>}
    </div>
    {presentation.truncated && <footer><span>CAPTURE TRUNCATED</span><span>{captureSummary(presentation.contentBytes, presentation.capturedBytes)}</span></footer>}
  </section>
}

export function CommandResultLayout({ exchanges, view, renderMessage }: {
  exchanges: TrafficExchange[]
  view: TrafficDetailView
  renderMessage: (exchange: TrafficExchange, direction: TrafficDirection) => ReactNode
}) {
  const label = view === 'request' ? 'Command' : view === 'response' ? 'Result' : 'Command and result'
  return <section className={`traffic-command-results${view === 'compare' ? '' : ' traffic-command-results--single'}`} aria-label={label}>
    <ol>
      {exchanges.map((exchange, index) => <li className="traffic-command-result" key={exchange.sequence}>
        <header><span>{String(index + 1).padStart(2, '0')}</span><strong>{exchange.tcp?.operation || 'COMMAND'}</strong><small>{duration(exchange.durationMs)}</small></header>
        <div className="traffic-semantic-grid">
          {view !== 'response' && renderMessage(exchange, 'request')}
          {view !== 'request' && renderMessage(exchange, 'response')}
        </div>
      </li>)}
    </ol>
  </section>
}
