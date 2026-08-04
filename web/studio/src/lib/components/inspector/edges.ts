/**
 * Pure edge-grouping helpers for the Inspector (RFC-012 R3): group the
 * working set's full edge list down to the ones incident on the selected
 * node, partitioned by (relType, direction relative to that node), and
 * compute the display name for each group per the design's edge grammar
 * (design/studio/components/inspector.html, design/studio/screens/graph.html).
 */
import type { GraphEdge } from '$lib/types/graph'

export type Direction = 'in' | 'out'

/** One neighbor row within a group: the edge plus which end is the "other" node. */
export interface IncidentEdge {
  edge: GraphEdge
  neighborId: string
}

/** All incident edges for one (relType, direction) pair, in encounter order. */
export interface EdgeGroup {
  relType: string
  direction: Direction
  edges: IncidentEdge[]
}

/**
 * Partitions the full edge list down to edges touching `nodeId`, grouped by
 * (relType, direction). Direction is relative to `nodeId`: 'out' when
 * `nodeId` is the edge's `from`, 'in' when it's the `to`. Self-loops
 * (from === to === nodeId) count as 'out' only, matching the from-side scan.
 * Edges not touching `nodeId` are excluded. Group order follows first
 * encounter of each (relType, direction) pair in the input array.
 */
export function groupIncident(nodeId: string, edges: GraphEdge[]): EdgeGroup[] {
  const groups = new Map<string, EdgeGroup>()

  for (const edge of edges) {
    const isOut = edge.from === nodeId
    const isIn = !isOut && edge.to === nodeId
    if (!isOut && !isIn) continue

    const direction: Direction = isOut ? 'out' : 'in'
    const neighborId = isOut ? edge.to : edge.from
    const key = `${edge.type}|${direction}`

    let group = groups.get(key)
    if (!group) {
      group = { relType: edge.type, direction, edges: [] }
      groups.set(key, group)
    }
    group.edges.push({ edge, neighborId })
  }

  return Array.from(groups.values())
}

/** Documented incoming-direction display names for the common structural/inferred types. */
const INCOMING_DISPLAY_NAME: Record<string, string> = {
  CALLS: 'CALLED_BY',
  DEFINES: 'DEFINED_BY',
  REFERENCES: 'REFERENCED_BY',
  CONTAINS: 'CONTAINED_IN',
  MENTIONS: 'MENTIONED_BY'
}

/**
 * Display name for a relationship group. Outgoing edges show the relType
 * verbatim. Incoming edges use the documented mapping for the common
 * structural/inferred types (CALLS -> CALLED_BY, DEFINES -> DEFINED_BY,
 * REFERENCES -> REFERENCED_BY, CONTAINS -> CONTAINED_IN, MENTIONS ->
 * MENTIONED_BY); any other type falls back to "<- relType".
 */
export function displayName(relType: string, direction: Direction): string {
  if (direction === 'out') return relType
  return INCOMING_DISPLAY_NAME[relType] ?? `← ${relType}`
}
