/**
 * Cell classification + rendering decisions for the console results table
 * (RFC-012 R8). Pure functions so the table's rendering branches are unit-
 * testable without a DOM.
 *
 * The tool serializes Neo4j nodes/relationships into tagged objects
 * ({_type:'node'|'relationship', ...}); plain scalars pass through. We render
 * scalars inline and objects/arrays as compact, expandable JSON.
 */
import type { CypherNode, CypherRelationship, CypherValue } from '$lib/types/console'

export type CellKind = 'scalar' | 'node' | 'relationship' | 'array' | 'object' | 'null'

export function classifyCell(value: CypherValue): CellKind {
  if (value === null || value === undefined) return 'null'
  if (Array.isArray(value)) return 'array'
  if (typeof value === 'object') {
    const t = (value as Record<string, unknown>)._type
    if (t === 'node') return 'node'
    if (t === 'relationship') return 'relationship'
    return 'object'
  }
  return 'scalar'
}

export function isNode(value: CypherValue): value is CypherNode {
  return classifyCell(value) === 'node'
}

export function isRelationship(value: CypherValue): value is CypherRelationship {
  return classifyCell(value) === 'relationship'
}

/**
 * A short inline label for a cell, shown before/instead of expansion. Nodes
 * show their primary label + name; relationships show their type; scalars show
 * their string form; arrays/objects show a size summary.
 */
export function cellSummary(value: CypherValue): string {
  const kind = classifyCell(value)
  switch (kind) {
    case 'null':
      return 'null'
    case 'scalar':
      return String(value)
    case 'array':
      return `[${(value as CypherValue[]).length}]`
    case 'node': {
      const n = value as CypherNode
      const label = n._labels?.[0] ?? 'Node'
      const name = pickName(n.props)
      return name ? `${label}: ${name}` : label
    }
    case 'relationship': {
      const r = value as CypherRelationship
      return `[:${r._rtype}]`
    }
    case 'object': {
      const keys = Object.keys(value as Record<string, unknown>)
      return `{${keys.length}}`
    }
  }
}

function pickName(props: Record<string, CypherValue> | undefined): string | null {
  if (!props) return null
  for (const key of ['name', 'title', 'nodeKey', 'path', 'filePath']) {
    const v = props[key]
    if (typeof v === 'string' && v.length > 0) return v
  }
  return null
}

/** Pretty-printed JSON for the expanded view of an object/array cell. */
export function expandedJson(value: CypherValue): string {
  return JSON.stringify(value, null, 2)
}

/**
 * Neo4j element ids look like "4:<uuid>:<n>" (observed from the live tool,
 * e.g. "4:902a108f-...:79268"). We detect these so the console can offer to
 * open them in /graph. Conservative: must have exactly the three
 * colon-separated parts with a numeric tail — a bare "42" or an arbitrary
 * string is not treated as a node id.
 */
const ELEMENT_ID_RE = /^\d+:[0-9a-fA-F-]{8,}:\d+$/

export function isElementId(value: unknown): value is string {
  return typeof value === 'string' && ELEMENT_ID_RE.test(value)
}

/** Column names that, when their cells are element ids, are node references. */
const NODE_ID_COLUMN_RE = /(?:^|_)(?:node)?id$/i

export function isNodeIdColumnName(column: string): boolean {
  return NODE_ID_COLUMN_RE.test(column)
}

/**
 * Given the result columns and rows, returns the element ids from the first
 * column that (a) is named like a node-id column AND (b) whose values are all
 * genuine element ids. Returns [] when no such column exists — we never guess
 * that arbitrary strings are ids. Order is preserved and duplicates removed;
 * capped at `limit`.
 */
export function collectNodeIds(
  columns: readonly string[],
  rows: ReadonlyArray<Record<string, CypherValue>>,
  limit: number
): string[] {
  for (const col of columns) {
    if (!isNodeIdColumnName(col)) continue
    const ids: string[] = []
    let allIds = rows.length > 0
    for (const row of rows) {
      const v = row[col]
      if (!isElementId(v)) {
        allIds = false
        break
      }
      if (!ids.includes(v)) ids.push(v)
    }
    if (allIds && ids.length > 0) return ids.slice(0, limit)
  }
  return []
}

/** Builds the /graph deep-link for a set of node element ids. */
export function graphLinkForIds(ids: readonly string[]): string {
  const encoded = ids.map(encodeURIComponent).join(',')
  return `/graph?nodes=${encoded}`
}
