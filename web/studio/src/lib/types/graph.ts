/**
 * Explorer contracts (RFC-012 R1–R3) — mirrors the JSON payloads of
 * codegraph_find / codegraph_expand / codegraph_path / codegraph_source
 * (probed against the live MCP server; snake_case comes from the Go tools).
 * The /api routes proxy these shapes verbatim inside an ApiEnvelope.
 */

/** A search hit from codegraph_find. */
export interface FoundNode {
  node_id: string
  node_key: string
  label: string
  name: string
  signature: string
  file_path: string
  service: string
  start_line?: number
  end_line?: number
  score?: number
}

export interface FindResponse {
  count: number
  results: FoundNode[]
  next_cursor?: string
}

/** A node as returned by expand (and path when hydrated). */
export interface GraphNode {
  node_id: string
  label: string
  name: string
  file_path?: string
  start_line?: number
  end_line?: number
  signature?: string
  service?: string
  /** hops from the expansion start node (0 = the start itself) */
  distance?: number
}

/**
 * An edge from expand/path. Provenance fields are present only on inferred
 * edges (MENTIONS): strategy e.g. "docmine/codespan" | "semlink/<model>",
 * confidence in [0,1]. Structural edges carry neither — ground truth.
 */
export interface GraphEdge {
  from: string
  to: string
  type: string
  strategy?: string
  confidence?: number
}

export interface ExpandResponse {
  start: GraphNode
  nodes: GraphNode[]
  edges: GraphEdge[]
  node_count: number
  edge_count: number
  truncated: boolean
}

/** Path nodes are slim (node_id/label/name only). */
export interface PathNode {
  node_id: string
  label: string
  name: string
}

export interface PathStep {
  hops: number
  nodes: PathNode[]
  edges: GraphEdge[]
}

export interface PathResponse {
  path_count: number
  paths: PathStep[]
}

/** codegraph_source with format=json. */
export interface SourceResponse {
  kind: 'code' | 'document' | 'chunk'
  name: string
  signature?: string
  service?: string
  file_path?: string
  start_line?: number
  end_line?: number
  /** treesitter | go-ast | scip-declaration | '' — scip-declaration means declaration line only */
  range_source?: string
  /** shiki language id for code; 'markdown' for docs */
  lang: string
  source: string
  /** heading path for chunks */
  heading_path?: string
  title?: string
}

/** Every /api/* route answers with this envelope (HTTP 200) or ApiError. */
export interface ApiEnvelope<T> {
  warnings: string[]
  data: T
}

/** Error envelope (HTTP 4xx/5xx). kind mirrors McpRequestError.kind. */
export interface ApiError {
  error: string
  kind: string
}
