/**
 * Flows screen contracts (RFC-012 R4). Field casing is faithful to the wire:
 * codegraph_entry_points speaks snake_case, codegraph_flows speaks camelCase
 * (its steps serialize internal/query.FlowStep verbatim). Do not normalize —
 * a translation layer here would drift from the Go structs it mirrors.
 */

export interface EntryPoint {
  /** elementId — the address the canvas/inspector/source APIs understand */
  node_id: string
  node_key: string
  name: string
  /** Function | Method — drives dot color and inspector identity */
  label: string
  file_path?: string
  start_line?: number
  tier: 1 | 2 | 3 | 4
  tier_label: string
  /** tier-specific evidence: route source, implemented interface, callee count, centrality */
  detection_source?: string
  service?: string
  out_degree?: number
  in_degree?: number
}

export interface EntryPointsResponse {
  count: number
  entries: EntryPoint[]
  /** per-tier query failures — surfaced, never silently dropped */
  tier_errors?: string[]
}

export interface FlowStep {
  nodeKey: string
  name: string
  label: string
  order: number
  /** BFS distance from the flow's entry point (0 = the entry itself) */
  depth: number
  /** nodeKey of the spanning-tree parent; absent on the entry step */
  parentKey?: string
  /** elementId; absent only if the node vanished between trace and enrich */
  nodeId?: string
  filePath?: string
  startLine?: number
}

export interface Flow {
  flowNodeKey: string
  flowName: string
  flowType: string
  steps: FlowStep[]
}

export interface FlowResponse {
  flow_count: number
  flows: Flow[]
}

export interface ServicesResponse {
  services: string[]
}
