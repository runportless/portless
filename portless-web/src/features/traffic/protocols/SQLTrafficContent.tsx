import type { ReactNode } from 'react'

const sqlKeywords = new Set([
  'ADD', 'ALTER', 'AND', 'AS', 'ASC', 'BEGIN', 'BETWEEN', 'BY', 'CASE', 'COMMIT', 'CONFLICT', 'CREATE',
  'CROSS', 'DELETE', 'DESC', 'DISTINCT', 'DO', 'DROP', 'ELSE', 'END', 'EXISTS', 'FETCH', 'FILTER', 'FOR',
  'FOREIGN', 'FROM', 'FULL', 'GROUP', 'HAVING', 'ILIKE', 'IN', 'INDEX', 'INNER', 'INSERT', 'INTERSECT',
  'INTO', 'IS', 'JOIN', 'KEY', 'LEFT', 'LIKE', 'LIMIT', 'LOCKED', 'NATURAL', 'NOT', 'NOTHING', 'NOWAIT',
  'OFFSET', 'ON', 'OR', 'ORDER', 'OUTER', 'OVER', 'PARTITION', 'PRIMARY', 'RANGE', 'RECURSIVE', 'REFERENCES',
  'RETURNING', 'RIGHT', 'ROLLBACK', 'ROWS', 'SELECT', 'SET', 'SHARE', 'SKIP', 'TABLE', 'THEN', 'UNION',
  'UPDATE', 'USING', 'VALUES', 'VIEW', 'WHEN', 'WHERE', 'WINDOW', 'WITH',
])

const sqlTokenPattern = /(--[^\n]*)|(\/\*[\s\S]*?\*\/)|('(?:''|\\.|[^'])*')|("(?:""|[^"])*")|(\$\d+\b)|(-?(?:\d+(?:\.\d+)?|\.\d+)\b)|(\b[A-Za-z_][A-Za-z0-9_$]*\b)/g

function highlightedSQL(value: string) {
  const nodes: ReactNode[] = []
  let cursor = 0
  let key = 0
  for (const match of value.matchAll(sqlTokenPattern)) {
    const start = match.index
    if (start > cursor) nodes.push(value.slice(cursor, start))
    const word = match[7]?.toUpperCase()
    const kind = match[1] || match[2] ? 'comment'
      : match[3] ? 'string'
        : match[4] ? 'identifier'
          : match[5] ? 'parameter'
            : match[6] ? 'number'
              : word === 'NULL' ? 'null'
                : word === 'TRUE' || word === 'FALSE' ? 'boolean'
                  : word && sqlKeywords.has(word) ? 'keyword'
                    : value.slice(start + match[0].length).trimStart().startsWith('(') ? 'function'
                      : ''
    if (kind) nodes.push(<span className={`traffic-sql__${kind}`} key={key++}>{match[0]}</span>)
    else nodes.push(match[0])
    cursor = start + match[0].length
  }
  if (cursor < value.length) nodes.push(value.slice(cursor))
  return nodes
}

export function SQLTrafficContent({ content }: { content: string }) {
  return <pre className="traffic-sql">{highlightedSQL(content)}</pre>
}
