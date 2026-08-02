/**
 * Pure helpers for GraphCanvas: converting WorkingSet-shaped props (GraphNode /
 * GraphEdge) into cytoscape element definitions, computing incremental diffs
 * against a live cytoscape core, the categorical/provenance style function,
 * and a plain-TS controller that drives the sync (no Svelte, no DOM —
 * GraphCanvas.svelte only wires lifecycle + the HTML overlay around it).
 */
import cytoscape from 'cytoscape'
import type { Core, ElementDefinition, EventObject, LayoutOptions, NodeSingular, StylesheetStyle } from 'cytoscape'
import fcose from 'cytoscape-fcose'
import type { GraphEdge, GraphNode } from '$lib/types/graph'
import { edgeKey } from '$lib/types/workingset'
import { fmtConfidence } from '$lib/format'

// Registering an already-registered extension throws, so guard it: this module
// is imported both by GraphCanvas.svelte and by canvas.test.ts (which drives a
// cytoscape() instance directly), and either could load first.
const registeredExtensions = (cytoscape as unknown as { __fcoseRegistered?: boolean }).__fcoseRegistered
if (!registeredExtensions) {
  cytoscape.use(fcose)
  ;(cytoscape as unknown as { __fcoseRegistered?: boolean }).__fcoseRegistered = true
}

/** Known node labels, in the order the design defines the hue map. */
export type NodeLabel =
  | 'Service'
  | 'File'
  | 'Function'
  | 'Method'
  | 'Class'
  | 'Interface'
  | 'Variable'
  | 'Symbol'
  | 'Document'
  | 'DocumentChunk'

const NODE_COLORS: Record<NodeLabel, { fg: string; bg: string }> = {
  Service: { fg: '#7048E8', bg: '#F3F0FF' },
  File: { fg: '#495057', bg: '#F1F3F5' },
  Function: { fg: '#1C7ED6', bg: '#E7F5FF' },
  Method: { fg: '#0B7285', bg: '#E3FAFC' },
  Class: { fg: '#E8590C', bg: '#FFF4E6' },
  Interface: { fg: '#9C36B5', bg: '#F8F0FC' },
  Variable: { fg: '#868E96', bg: '#F8F9FA' },
  Symbol: { fg: '#087F5B', bg: '#E6FCF5' },
  Document: { fg: '#C2255C', bg: '#FFF0F6' },
  DocumentChunk: { fg: '#E64980', bg: '#FFF0F6' }
}

/** Unknown labels fall back to the Variable palette. */
export function nodeColors(label: string): { fg: string; bg: string } {
  return NODE_COLORS[label as NodeLabel] ?? NODE_COLORS.Variable
}

export type EdgeProvenance = 'structural' | 'docmine' | 'semlink'

/** structural (no strategy) | docmine/* | semlink/* — anything else falls back to structural. */
export function edgeProvenance(edge: Pick<GraphEdge, 'strategy'>): EdgeProvenance {
  if (!edge.strategy) return 'structural'
  if (edge.strategy.startsWith('docmine')) return 'docmine'
  if (edge.strategy.startsWith('semlink')) return 'semlink'
  return 'structural'
}

/** "docmine 0.90" / "semlink 0.57" — empty for structural edges (no label drawn). */
export function edgeLabelText(edge: Pick<GraphEdge, 'strategy' | 'confidence'>): string {
  const kind = edgeProvenance(edge)
  if (kind === 'structural') return ''
  const conf = edge.confidence ?? 0
  return `${kind} ${fmtConfidence(conf)}`
}

export interface NodeConvertOptions {
  selected?: boolean
  pinned?: boolean
}

/** GraphNode -> cytoscape node element. Classes carry label + selection/pin state for the stylesheet. */
export function toNodeElement(node: GraphNode, opts: NodeConvertOptions = {}): ElementDefinition {
  const classes = ['node', `label-${node.label}`]
  if (opts.selected) classes.push('is-selected')
  if (opts.pinned) classes.push('is-pinned')
  return {
    group: 'nodes',
    data: {
      id: node.node_id,
      label: node.label,
      name: node.name
    },
    classes
  }
}

/** GraphEdge -> cytoscape edge element, keyed via edgeKey (from|type|to) for stable ids. */
export function toEdgeElement(edge: GraphEdge): ElementDefinition {
  const kind = edgeProvenance(edge)
  const classes = ['edge', `prov-${kind}`]
  return {
    group: 'edges',
    data: {
      id: edgeKey(edge),
      source: edge.from,
      target: edge.to,
      type: edge.type,
      label: edgeLabelText(edge)
    },
    classes
  }
}

export interface ElementDiff {
  addedNodeIds: string[]
  removedNodeIds: string[]
  addedEdgeIds: string[]
  removedEdgeIds: string[]
}

/**
 * Set-difference between the ids currently in the cy instance and the ids the
 * next props describe. Pure (no cytoscape dependency) so it's trivial to unit
 * test in isolation from the controller.
 */
export function diffIds(current: Set<string>, next: Set<string>): { added: string[]; removed: string[] } {
  const added: string[] = []
  const removed: string[] = []
  for (const id of next) if (!current.has(id)) added.push(id)
  for (const id of current) if (!next.has(id)) removed.push(id)
  return { added, removed }
}

/** Full diff of both node and edge id sets, given the desired next-state elements. */
export function computeDiff(
  currentNodeIds: Set<string>,
  currentEdgeIds: Set<string>,
  nextNodes: GraphNode[],
  nextEdges: GraphEdge[]
): ElementDiff {
  const nextNodeIds = new Set(nextNodes.map((n) => n.node_id))
  const nextEdgeIds = new Set(nextEdges.map((e) => edgeKey(e)))
  const nodeDiff = diffIds(currentNodeIds, nextNodeIds)
  const edgeDiff = diffIds(currentEdgeIds, nextEdgeIds)
  return {
    addedNodeIds: nodeDiff.added,
    removedNodeIds: nodeDiff.removed,
    addedEdgeIds: edgeDiff.added,
    removedEdgeIds: edgeDiff.removed
  }
}

/**
 * The design's graph-node grammar (design/studio/screens/graph.html .gnode):
 * a solid categorical dot with a white ring and a mono label chip BELOW the
 * node — the same dot+label language the flows screen chips use. Shared by the
 * workbench and overview stylesheets so /graph reads as one visual system.
 */
export const gnodeLabelStyle = {
  'font-family': 'IBM Plex Mono, monospace',
  'font-size': 10,
  color: '#495057',
  'text-valign': 'bottom',
  'text-halign': 'center',
  'text-margin-y': 6,
  'text-wrap': 'ellipsis',
  'text-max-width': '150px',
  'text-background-color': '#FFFFFF',
  'text-background-opacity': 0.92,
  'text-background-shape': 'roundrectangle',
  'text-background-padding': '2px'
} as const

/** The cytoscape stylesheet implementing the design's gnode dot + edge provenance grammar. */
export const canvasStyle: StylesheetStyle[] = [
  {
    selector: 'node',
    style: {
      // NOT width:'label' — that deprecated value doesn't just warn in
      // cytoscape 3.34, it aborts style application entirely and every node
      // stays visible()=false (never rendered).
      shape: 'ellipse',
      width: 26,
      height: 26,
      'background-color': (ele: NodeSingular) => nodeColors(String(ele.data('label'))).fg,
      'border-width': 2.5,
      'border-color': '#FFFFFF',
      label: (ele: NodeSingular) => {
        const pinned = ele.hasClass('is-pinned')
        const name = String(ele.data('name') ?? '')
        return pinned ? `⌖ ${name}` : name
      },
      ...gnodeLabelStyle
    }
  },
  {
    selector: 'node.is-selected',
    style: {
      color: '#364FC7',
      'font-weight': 500,
      'overlay-color': '#3B5BDB',
      'overlay-opacity': 0.14,
      'overlay-padding': 5
    }
  },
  {
    selector: 'node.is-pinned',
    style: {
      color: '#364FC7'
    }
  },
  {
    selector: 'edge',
    style: {
      width: 1.6,
      'line-color': '#ADB5BD',
      'target-arrow-color': '#ADB5BD',
      'target-arrow-shape': 'triangle',
      'arrow-scale': 0.8,
      'curve-style': 'bezier',
      label: '',
      'font-family': 'IBM Plex Mono, monospace',
      'font-size': 9,
      'text-rotation': 'none',
      'text-background-opacity': 1,
      'text-background-padding': '3px',
      'text-background-shape': 'roundrectangle'
    }
  },
  {
    selector: 'edge.prov-docmine',
    style: {
      'line-color': '#E67700',
      'target-arrow-color': '#E67700',
      label: 'data(label)',
      color: '#E67700',
      'text-background-color': '#FFF9DB'
    }
  },
  {
    selector: 'edge.prov-semlink',
    style: {
      'line-color': '#6741D9',
      'target-arrow-color': '#6741D9',
      'line-style': 'dashed',
      'line-dash-pattern': [5, 4],
      label: 'data(label)',
      color: '#6741D9',
      'text-background-color': '#F3F0FF'
    }
  }
]

export interface CanvasController {
  /** Incrementally sync the cy instance to the given nodes/edges/selection/pins. Runs layout only if elements were added/removed. */
  sync(nodes: GraphNode[], edges: GraphEdge[], selected: string | null, pinned: string[]): void
  /** Release all bound cytoscape event handlers. Does not destroy the cy instance (caller owns it). */
  destroy(): void
}

export interface CanvasControllerCallbacks {
  onSelect: (nodeId: string | null) => void
  onTogglePin: (nodeId: string) => void
  onExpandRequest: (nodeId: string) => void
}

const FCOSE_LAYOUT_BASE = {
  name: 'fcose',
  animationDuration: 300,
  randomize: false,
  fit: false
} as const

/**
 * Drives incremental sync of a cytoscape core from WorkingSet-shaped props.
 * Pure TS controller (no Svelte) so it can be unit tested against a headless
 * cy instance directly; GraphCanvas.svelte wires this to $effect + DOM events.
 */
export function createCanvasController(cy: Core, callbacks: CanvasControllerCallbacks): CanvasController {
  const handleTap = (evt: EventObject) => {
    if (evt.target === cy) {
      callbacks.onSelect(null)
      return
    }
  }
  const handleNodeTap = (evt: EventObject) => {
    const node = evt.target as NodeSingular
    const orig = evt.originalEvent as MouseEvent | undefined
    if (orig && (orig.altKey || orig.metaKey)) {
      callbacks.onTogglePin(node.id())
      return
    }
    callbacks.onSelect(node.id())
  }
  const handleNodeDblTap = (evt: EventObject) => {
    const node = evt.target as NodeSingular
    callbacks.onExpandRequest(node.id())
  }

  cy.on('tap', handleTap)
  cy.on('tap', 'node', handleNodeTap)
  cy.on('dbltap', 'node', handleNodeDblTap)

  function sync(nodes: GraphNode[], edges: GraphEdge[], selected: string | null, pinned: string[]) {
    const currentNodeIds = new Set(cy.nodes().map((n) => n.id()))
    const currentEdgeIds = new Set(cy.edges().map((e) => e.id()))
    const diff = computeDiff(currentNodeIds, currentEdgeIds, nodes, edges)

    const pinnedSet = new Set(pinned)
    const addedNodeIdSet = new Set(diff.addedNodeIds)

    // Seed positions for new nodes: fcose with randomize:false starts from
    // current coordinates, and a pile of nodes all at the origin is a
    // degenerate start it cannot untangle. Scatter each new node near an
    // already-placed neighbor (or the center of the existing graph).
    const anchorFor = (nodeId: string): { x: number; y: number } => {
      for (const e of edges) {
        const other = e.from === nodeId ? e.to : e.to === nodeId ? e.from : null
        if (other && currentNodeIds.has(other)) {
          const pos = cy.getElementById(other).position()
          return { x: pos.x, y: pos.y }
        }
      }
      if (cy.nodes().length > 0) {
        const bb = cy.nodes().boundingBox()
        return { x: (bb.x1 + bb.x2) / 2, y: (bb.y1 + bb.y2) / 2 }
      }
      return { x: 0, y: 0 }
    }
    let seedIndex = 0
    const jitter = (i: number): { dx: number; dy: number } => {
      // deterministic spiral scatter — stable across runs, no Math.random
      const angle = i * 2.39996 // golden angle
      const radius = 90 + 24 * Math.sqrt(i)
      return { dx: Math.cos(angle) * radius, dy: Math.sin(angle) * radius }
    }

    const toAdd: ElementDefinition[] = []
    for (const node of nodes) {
      if (addedNodeIdSet.has(node.node_id)) {
        const el = toNodeElement(node, { selected: node.node_id === selected, pinned: pinnedSet.has(node.node_id) })
        const anchor = anchorFor(node.node_id)
        const { dx, dy } = jitter(seedIndex++)
        el.position = { x: anchor.x + dx, y: anchor.y + dy }
        toAdd.push(el)
      }
    }
    for (const edge of edges) {
      if (diff.addedEdgeIds.includes(edgeKey(edge))) {
        toAdd.push(toEdgeElement(edge))
      }
    }

    const hasStructuralChange = toAdd.length > 0 || diff.removedNodeIds.length > 0 || diff.removedEdgeIds.length > 0

    // NOT wrapped in cy.batch(): elements added inside a batch never get
    // their initial style applied (visible()/takesUpSpace() stay false until
    // some later style touch), leaving new nodes unrendered. The add is a
    // single atomic cy.add(array) anyway, so batching buys nothing.
    // Edges first: removing a node auto-drops its incident edges, but being
    // explicit keeps this correct even if diffIds ever disagrees with cytoscape.
    for (const id of diff.removedEdgeIds) cy.getElementById(id).remove()
    for (const id of diff.removedNodeIds) cy.getElementById(id).remove()
    if (toAdd.length > 0) {
      cy.add(toAdd)
    }

    // Selection/pin classes: reconcile on every sync, not just on add, since
    // selection can change without the working set changing.
    cy.nodes().forEach((n) => {
      n.toggleClass('is-selected', n.id() === selected)
      n.toggleClass('is-pinned', pinnedSet.has(n.id()))
    })

    if (hasStructuralChange && cy.nodes().length > 0) {
      // cy.container() is null in headless mode (e.g. unit tests): cytoscape's
      // animation engine requires a real renderer, so animate only when mounted.
      const animate = cy.container() !== null
      const layout = cy.layout({ ...FCOSE_LAYOUT_BASE, animate } as unknown as LayoutOptions)
      if (animate) {
        // fit after settling — new elements spawn at the origin and would
        // otherwise land outside the viewport; selection-only syncs never
        // reach here, so user pan/zoom survives everything but growth.
        layout.one('layoutstop', () => {
          cy.animate({ fit: { eles: cy.elements(), padding: 60 }, duration: 200 })
        })
      }
      layout.run()
    }
  }

  function destroy() {
    cy.removeListener('tap', handleTap)
    cy.removeListener('tap', 'node', handleNodeTap)
    cy.removeListener('dbltap', 'node', handleNodeDblTap)
  }

  return { sync, destroy }
}
