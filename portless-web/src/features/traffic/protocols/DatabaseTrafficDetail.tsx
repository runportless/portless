import type { TrafficExchange } from '../../../types'
import { CommandResultLayout, ProtocolMessageCard, type ProtocolMessagePresentation } from '../detail/CommandResultLayout'
import { databaseResultCSV, databaseResultRows, databaseResultSummary, DatabaseResultTable } from '../detail/DatabaseResultTable'
import { TrafficTextContent } from '../detail/TrafficFormatting'
import type { TrafficDetailView, TrafficDirection } from '../detail/trafficDetailTypes'
import { SQLTrafficContent } from './SQLTrafficContent'

function DatabaseMessage({ exchange, direction, present }: {
  exchange: TrafficExchange
  direction: TrafficDirection
  present: (exchange: TrafficExchange, direction: TrafficDirection) => ProtocolMessagePresentation
}) {
  const presentation = present(exchange, direction)
  const result = direction === 'response' ? databaseResultRows(exchange) : null
  if (result) {
    const tablePresentation: ProtocolMessagePresentation = {
      ...presentation,
      title: '',
      showTitle: false,
      fields: [],
      binary: false,
      truncated: result.truncated || presentation.truncated,
      contentBytes: result.contentBytes,
      capturedBytes: result.capturedBytes,
      meta: databaseResultSummary(result),
    }
    const copyResult = () => navigator.clipboard.writeText(databaseResultCSV(result)).catch(() => undefined)
    return <ProtocolMessageCard
      direction={direction}
      presentation={tablePresentation}
      content={<DatabaseResultTable result={result} />}
      table
      copy={{ label: 'Copy database results as CSV', action: copyResult }}
    />
  }

  const sql = presentation.contentType.toLowerCase().startsWith('text/x-sql')
  const content = presentation.content
    ? sql ? <SQLTrafficContent content={presentation.content} /> : <TrafficTextContent content={presentation.content} contentType={presentation.contentType} />
    : undefined
  return <ProtocolMessageCard direction={direction} presentation={presentation} content={content} />
}

export function DatabaseTrafficDetail({ exchanges, view, present }: {
  exchanges: TrafficExchange[]
  view: TrafficDetailView
  present: (exchange: TrafficExchange, direction: TrafficDirection) => ProtocolMessagePresentation
}) {
  return <CommandResultLayout exchanges={exchanges} view={view} renderMessage={(exchange, direction) => <DatabaseMessage exchange={exchange} direction={direction} present={present} />} />
}
