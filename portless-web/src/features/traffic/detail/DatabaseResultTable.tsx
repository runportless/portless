import { useState } from 'react'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import type { TrafficExchange } from '../../../api/contracts/traffic'

type DatabaseRow = unknown[] | Record<string, unknown>

export type DatabaseResult = {
  columns: string[]
  rows: unknown[][]
  truncated: boolean
  contentBytes: number
  capturedBytes: number
}

export function databaseResultRows(exchange: TrafficExchange): DatabaseResult | null {
  const protocol = exchange.tcp?.applicationProtocol?.toLowerCase()
  if (protocol !== 'postgresql' && protocol !== 'mysql') return null
  const messages = exchange.tcp?.responseMessages || []
  const rowType = protocol === 'postgresql' ? 'data-row' : 'row'
  const rowMessages = messages.filter((message) => message.type.toLowerCase() === rowType)
  const columns = messages.flatMap((message) => {
    const type = message.type.toLowerCase()
    if (protocol === 'postgresql' && type === 'row-description') {
      return (message.fields || []).filter((field) => field.name.toLowerCase() === 'column').map((field) => field.value).filter(Boolean)
    }
    if (protocol === 'mysql' && (type === 'column' || type === 'column-definition')) {
      const column = (message.fields || []).find((field) => ['name', 'column'].includes(field.name.toLowerCase()))?.value || message.summary || ''
      return column ? [column] : []
    }
    return []
  })
  if (rowMessages.length === 0 && columns.length === 0) return null

  const parsedRows: DatabaseRow[] = []
  for (const message of rowMessages) {
    if (!message.content || message.encoding === 'base64') return null
    try {
      const parsed: unknown = JSON.parse(message.content)
      if (Array.isArray(parsed) || (parsed !== null && typeof parsed === 'object')) parsedRows.push(parsed as DatabaseRow)
      else return null
    } catch {
      return null
    }
  }

  const knownColumns = new Set(columns)
  for (const row of parsedRows) {
    if (Array.isArray(row)) {
      while (columns.length < row.length) {
        const column = `column_${columns.length + 1}`
        columns.push(column)
        knownColumns.add(column)
      }
      continue
    }
    for (const column of Object.keys(row)) {
      if (knownColumns.has(column)) continue
      columns.push(column)
      knownColumns.add(column)
    }
  }
  if (columns.length === 0) return null

  return {
    columns,
    rows: parsedRows.map((row) => columns.map((column, index) => Array.isArray(row) ? row[index] : row[column])),
    truncated: Boolean(exchange.tcp?.responseTruncated || rowMessages.some((message) => message.truncated)),
    contentBytes: rowMessages.reduce((total, message) => total + Math.max(0, message.contentBytes || 0), 0),
    capturedBytes: rowMessages.reduce((total, message) => total + Math.max(0, message.capturedBytes || 0), 0),
  }
}

function databaseCellText(value: unknown) {
  if (value === null || value === undefined) return ''
  if (typeof value === 'boolean') return value ? 'TRUE' : 'FALSE'
  if (typeof value === 'object') return JSON.stringify(value) || ''
  return String(value)
}

function csvCell(value: unknown) {
  if (value === null || value === undefined) return ''
  const text = databaseCellText(value)
  return text === '' || /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

export function databaseResultCSV(result: Pick<DatabaseResult, 'columns' | 'rows'>) {
  return [result.columns, ...result.rows].map((row) => row.map(csvCell).join(',')).join('\n')
}

function databaseCell(value: unknown) {
  if (value === null) return <span className="traffic-database-result__null">NULL</span>
  if (value === undefined) return <span className="traffic-database-result__missing">—</span>
  return databaseCellText(value)
}

export function DatabaseResultTable({ result }: { result: DatabaseResult }) {
  const [page, setPage] = useState(0)
  const pagination = paginateItems(result.rows, page, 10)
  return <div className="traffic-database-result">
    <div className="traffic-database-result__scroll">
      <table aria-label="Database result rows">
        <thead><tr>{result.columns.map((column, index) => <th scope="col" key={`${column}:${index}`}>{column}</th>)}</tr></thead>
        <tbody>{result.rows.length > 0
          ? pagination.items.map((row, rowIndex) => <tr key={pagination.start + rowIndex}>{row.map((value, columnIndex) => <td key={columnIndex}>{databaseCell(value)}</td>)}</tr>)
          : <tr><td className="traffic-database-result__empty" colSpan={result.columns.length}>{result.truncated ? 'No rows were retained in this capture.' : 'No rows returned.'}</td></tr>}
        </tbody>
      </table>
    </div>
    <PanelPagination label="database result rows" pagination={pagination} onPage={setPage} />
  </div>
}

export function databaseResultSummary(result: DatabaseResult) {
  const rows = `${result.rows.length} ${result.truncated ? 'captured ' : ''}${result.rows.length === 1 ? 'row' : 'rows'}`
  const columns = `${result.columns.length} ${result.columns.length === 1 ? 'column' : 'columns'}`
  return `${rows} · ${columns}`
}
