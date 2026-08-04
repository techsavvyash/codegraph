/**
 * Dashboard response contract — the interface between the server data layer
 * (src/lib/server/dashboard/) and the dashboard UI. Keep in sync with both.
 */

export type Severity = 'ok' | 'warn' | 'err'

export interface HealthFlag {
  severity: Severity
  /** Stable machine code, e.g. 'zero-flows', 'duplicate-repo', 'embeddings-online' */
  code: string
  /** Human sentence; node/service names referenced inline. */
  text: string
}

export interface DocLinkCounts {
  docmine: number
  semlink: number
}

export interface SemanticState {
  embeddingModel: string | null
  dims: number | null
  semlinkModel: string | null
  semlinkThreshold: number | null
  embeddedChunks: number
}

export interface ReachabilitySummary {
  live: number
  testOnly: number
  dead: number
  /** Dead functions whose callers are all themselves dead. */
  deadCluster: number
  possiblyLive: number
  unknown: number
}

export interface ServiceCard {
  name: string
  scopeId: string
  language: string | null
  version: string | null
  repositoryUrl: string | null
  /** Only labels with non-zero counts are present. */
  nodesByLabel: Record<string, number>
  calls: number
  apiRoutes: number
  flows: number
  docs: number
  chunks: number
  docLinks: DocLinkCounts
  /** null when the service has no embedded chunks at all */
  semantic: SemanticState | null
  /**
   * RFC-014 verdict counts from stamped fn.reachability props; null when
   * the service carries no verdicts yet (not re-indexed since the
   * classifier shipped).
   */
  reachability: ReachabilitySummary | null
}

export interface CallHub {
  name: string
  service: string | null
  label: 'Function' | 'Method'
  inDegree: number
}

export interface RecentDocLink {
  docPath: string
  headingPath: string
  family: 'docmine' | 'semlink'
  strategy: string
  confidence: number
  createdAt: string | null
  targetName: string | null
}

export interface DashboardTotals {
  services: number
  nodesByLabel: Record<string, number>
  edgesByType: Record<string, number>
  docLinks: DocLinkCounts
}

export interface DashboardData {
  /** ISO timestamp of when this snapshot was computed. */
  generatedAt: string
  /** e.g. "bolt://localhost:7687" (from NEO4J_URI or the known default). */
  neo4jTarget: string
  /** Guardrail warnings surfaced by the MCP tools during collection — shown, never dropped. */
  warnings: string[]
  totals: DashboardTotals
  services: ServiceCard[]
  health: HealthFlag[]
  callHubs: CallHub[]
  recentDocLinks: RecentDocLink[]
}

/** Error envelope returned by /api/dashboard on failure (HTTP 503). */
export interface DashboardError {
  error: string
  kind: string
}
