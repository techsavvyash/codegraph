/**
 * Cypher console contracts (RFC-012 R8). Mirrors the JSON payload of the
 * codegraph_cypher tool as observed against the live MCP server:
 *
 *   { "columns": string[], "row_count": number,
 *     "rows": Array<Record<string, CypherValue>>, "truncated": boolean }
 *
 * Guardrail warnings (e.g. AllNodesScan) are peeled off the text block by
 * parseToolPayload and arrive in the ApiEnvelope's `warnings`, never inside
 * this body. Write-keyword rejections and EXPLAIN failures arrive as
 * tool-errors (HTTP 422), not as a CypherResult.
 */

/**
 * A single cell value. Scalars pass through as-is; Neo4j nodes and
 * relationships are serialized by the tool's sanitizeCypherValue into these
 * tagged objects (see cmd/codegraph-mcp/handlers_cypher.go).
 */
export type CypherScalar = string | number | boolean | null
export type CypherValue =
  | CypherScalar
  | CypherNode
  | CypherRelationship
  | CypherValue[]
  | { [key: string]: CypherValue }

export interface CypherNode {
  _type: 'node'
  _id: string
  _labels: string[]
  props: Record<string, CypherValue>
}

export interface CypherRelationship {
  _type: 'relationship'
  _id: string
  _rtype: string
  _start: string
  _end: string
  props: Record<string, CypherValue>
}

/** The verbatim JSON body the codegraph_cypher tool returns in `format: json`. */
export interface CypherResult {
  columns: string[]
  row_count: number
  rows: Array<Record<string, CypherValue>> | null
  truncated: boolean
}

/** POST body accepted by /api/cypher. */
export interface CypherRequestBody {
  query: string
  /** Optional named parameters ($name in the query). */
  params?: Record<string, unknown>
  /** Optional row cap (1–1000, tool default 100). */
  row_limit?: number
}

/** A single persisted history entry. */
export interface HistoryEntry {
  query: string
  /** epoch ms when the query was last run */
  at: number
  /** raw params JSON text as typed in the panel; omitted when empty (back-compat) */
  paramsText?: string
}
