/**
 * Service Overview contracts — the whole-service visualizer on /graph.
 * File/edge shapes mirror the aggregated cypher in $lib/server/overview/api.ts
 * (function-level CALLS rolled up to file pairs); symbol shapes mirror the
 * per-file drilldown. Node ids are Neo4j elementIds (contain ':' — always
 * encodeURIComponent before putting them in a URL). The /api routes proxy these
 * inside an ApiEnvelope (see $lib/types/graph.ts).
 */

/** One source file in the service, with its rolled-up symbol count. */
export interface OverviewFile {
  nodeId: string
  /** service-relative path, e.g. "internal/graph/client.go" */
  path: string
  language: string | null
  lineCount: number
  /** count of contained Function/Method nodes */
  symbolCount: number
}

/**
 * A directed file→file relationship, both weights merged onto one pair.
 * fnWeight = function-level CALLS aggregated to the pair; moduleWeight =
 * module-scope File-CALLS. Either can be 0 (the pair exists via the other).
 */
export interface FileEdge {
  fromPath: string
  toPath: string
  fnWeight: number
  moduleWeight: number
}

export interface OverviewResponse {
  service: string
  files: OverviewFile[]
  edges: FileEdge[]
}

/** An out-call from a symbol to another same-service symbol (drilldown detail). */
export interface SymbolOutCall {
  targetId: string
  targetName: string
  targetPath: string
}

/** One Function/Method inside a file, with its call fan-in/fan-out. */
export interface FileSymbol {
  nodeId: string
  name: string
  label: 'Function' | 'Method'
  startLine: number
  /** distinct incoming CALLS from any source (cheap count) */
  inCalls: number
  /** same-service out-calls, with target identity for drawing symbol→symbol edges */
  outCalls: SymbolOutCall[]
  /** count of out-calls whose target lives in another service */
  externalOutCalls: number
}

export interface FileSymbolsResponse {
  file: { nodeId: string; path: string }
  symbols: FileSymbol[]
}

// ---------------------------------------------------------------------------
// client render model (produced by $lib/components/overview/model.ts)

export type RenderNodeKind = 'dir' | 'file' | 'symbol'

/**
 * A node in the currently-visible graph. `parentId` is set only for symbols
 * (the compound file node they live in). Stats are kind-specific and rolled up:
 * a collapsed dir carries its subtree's fileCount/symbolCount.
 */
export interface RenderNode {
  id: string
  kind: RenderNodeKind
  label: string
  parentId?: string
  /** dir + file: contained symbol count (rolled up for dirs) */
  symbolCount?: number
  /** dir: contained file count (rolled up) */
  fileCount?: number
  /** file: full service-relative path (label is just the basename) */
  path?: string
  /** file: language / lineCount passthrough */
  language?: string | null
  lineCount?: number
  /** symbol: kind + position + fan-in/out */
  symbolLabel?: 'Function' | 'Method'
  startLine?: number
  inCalls?: number
  externalOutCalls?: number
}

/**
 * An edge in the visible graph. `weight` is the merged call count driving edge
 * width/label; `kind` distinguishes an aggregate file/dir edge from a precise
 * symbol→symbol edge revealed by a drilldown.
 */
export interface RenderEdge {
  id: string
  source: string
  target: string
  weight: number
  kind: 'aggregate' | 'symbol'
}

export interface VisibleGraph {
  nodes: RenderNode[]
  edges: RenderEdge[]
}
