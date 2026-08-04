/**
 * Pure layout for the Flows spine (RFC-012 R4): turns a flat FlowStep[] into
 * a DFS-ordered tree of rows, then into pixel positions/connectors for the
 * stage. No Svelte, no DOM — unit-testable in isolation, mirrored visually
 * by design/studio/screens/flows.html (.stage / .step / .dguide / svg paths).
 */
import type { FlowStep } from '$lib/types/flows'

export interface SpineRow {
  step: FlowStep
  /** DFS row index — drives the y position */
  row: number
  depth: number
  /** true when parentKey is set but no step in the flow has that nodeKey */
  orphan: boolean
}

export interface ChipPosition {
  step: FlowStep
  row: number
  depth: number
  orphan: boolean
  x: number
  y: number
  /** chip content box, approximated for connector anchoring */
  width: number
  height: number
}

export interface ConnectorSegment {
  parentKey: string
  childKey: string
  /** SVG path `d` for an elbow: down from parent bottom, then right to child left */
  path: string
  /** midpoint of the elbow's horizontal run, for the CALLS pill label */
  labelX: number
  labelY: number
}

export interface DepthGuide {
  depth: number
  x: number
}

export interface SpineLayout {
  rows: SpineRow[]
  chips: ChipPosition[]
  connectors: ConnectorSegment[]
  depthGuides: DepthGuide[]
  width: number
  height: number
}

export interface LayoutOptions {
  /** left offset of depth 0 */
  originX?: number
  /** top offset of row 0 */
  originY?: number
  /** horizontal pixels per depth level */
  colWidth?: number
  /** vertical pixels per row */
  rowHeight?: number
  /** approximate chip box, used for connector anchoring + stage sizing */
  chipWidth?: number
  chipHeight?: number
  /** bottom/right padding added to the computed stage size */
  padding?: number
}

const DEFAULTS: Required<LayoutOptions> = {
  originX: 48,
  originY: 56,
  colWidth: 168,
  rowHeight: 84,
  chipWidth: 140,
  chipHeight: 26,
  padding: 40
}

/**
 * Builds a DFS-ordered row list from a flat FlowStep[]. The depth-0 step is
 * the root (there should be exactly one, but we don't assume it — any step
 * without a resolvable parentKey is treated as a root and walked in order).
 * Children of a node are visited in ascending `order`, and appear
 * immediately after their parent (and after any of the parent's own
 * earlier-visited descendants) in the returned array.
 *
 * Steps whose parentKey doesn't match any step's nodeKey are flagged
 * `orphan: true` and appended (in `order`) after all reachable steps —
 * they still get a row/depth so the caller can render them, just without a
 * connector back to a (missing) parent.
 */
export function buildSpineTree(steps: FlowStep[]): SpineRow[] {
  const byKey = new Map<string, FlowStep>()
  for (const s of steps) byKey.set(s.nodeKey, s)

  const childrenOf = new Map<string, FlowStep[]>()
  const roots: FlowStep[] = []
  const orphans: FlowStep[] = []

  for (const s of steps) {
    if (!s.parentKey) {
      roots.push(s)
    } else if (byKey.has(s.parentKey)) {
      const list = childrenOf.get(s.parentKey) ?? []
      list.push(s)
      childrenOf.set(s.parentKey, list)
    } else {
      orphans.push(s)
    }
  }

  for (const list of childrenOf.values()) list.sort((a, b) => a.order - b.order)
  roots.sort((a, b) => a.order - b.order)
  orphans.sort((a, b) => a.order - b.order)

  const rows: SpineRow[] = []
  let rowCounter = 0

  function visit(step: FlowStep, depth: number, orphan: boolean) {
    rows.push({ step, row: rowCounter++, depth, orphan })
    const kids = childrenOf.get(step.nodeKey) ?? []
    for (const kid of kids) visit(kid, depth + 1, false)
  }

  for (const root of roots) visit(root, 0, false)
  // orphans render as their own rows, at their own recorded depth, with no parent connector
  for (const orphan of orphans) {
    rows.push({ step: orphan, row: rowCounter++, depth: orphan.depth, orphan: true })
  }

  return rows
}

/**
 * Computes pixel positions for chips, depth guides, and parent→child
 * connectors from DFS-ordered rows. Column x = originX + depth*colWidth,
 * row y = originY + rowIndex*rowHeight.
 */
export function layoutSpine(rows: SpineRow[], opts: LayoutOptions = {}): SpineLayout {
  const o = { ...DEFAULTS, ...opts }

  const chips: ChipPosition[] = rows.map((r) => ({
    step: r.step,
    row: r.row,
    depth: r.depth,
    orphan: r.orphan,
    x: o.originX + r.depth * o.colWidth,
    y: o.originY + r.row * o.rowHeight,
    width: o.chipWidth,
    height: o.chipHeight
  }))

  const byKey = new Map<string, ChipPosition>()
  for (const c of chips) byKey.set(c.step.nodeKey, c)

  const connectors: ConnectorSegment[] = []
  for (const c of chips) {
    if (c.orphan) continue
    const parentKey = c.step.parentKey
    if (!parentKey) continue
    const parent = byKey.get(parentKey)
    if (!parent) continue

    const startX = parent.x + o.chipWidth * 0.15
    const startY = parent.y + o.chipHeight
    const endX = c.x
    const endY = c.y + o.chipHeight / 2

    connectors.push({
      parentKey,
      childKey: c.step.nodeKey,
      path: `M${startX} ${startY} V ${endY} H ${endX}`,
      labelX: (startX + endX) / 2,
      labelY: endY
    })
  }

  const depthSet = new Set(rows.map((r) => r.depth))
  const depthGuides: DepthGuide[] = [...depthSet]
    .sort((a, b) => a - b)
    .map((depth) => ({ depth, x: o.originX + depth * o.colWidth }))

  const maxDepth = rows.length ? Math.max(...rows.map((r) => r.depth)) : 0
  const maxRow = rows.length ? Math.max(...rows.map((r) => r.row)) : 0
  const width = o.originX + maxDepth * o.colWidth + o.chipWidth + o.padding
  const height = o.originY + maxRow * o.rowHeight + o.chipHeight + o.padding

  return { rows, chips, connectors, depthGuides, width, height }
}
