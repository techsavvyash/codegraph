/**
 * Working-set model for the graph canvas (RFC-012 R2): the canvas only ever
 * renders explicitly loaded nodes. This is the interface between the store
 * (owns state) and the canvas/omnibox/inspector components (render it).
 */
import type { GraphEdge, GraphNode } from './graph'

export interface WorkingSet {
  /** keyed by node_id — insertion order is display order for lists */
  nodes: Map<string, GraphNode>
  /** keyed by `${from}|${type}|${to}` */
  edges: Map<string, GraphEdge>
}

export function edgeKey(e: GraphEdge): string {
  return `${e.from}|${e.type}|${e.to}`
}

export interface SelectionState {
  /** node under inspection (single) */
  selected: string | null
  /** up to two pins; when two are set the path affordance activates */
  pinned: string[]
}

/** Events the canvas raises toward the page (which owns the store + API calls). */
export interface CanvasEvents {
  onSelect: (nodeId: string | null) => void
  onTogglePin: (nodeId: string) => void
  onExpandRequest: (nodeId: string) => void
  onRemoveNode: (nodeId: string) => void
}
