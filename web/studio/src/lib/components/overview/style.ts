/**
 * Cytoscape stylesheet + sizing helpers for the Overview canvas, sharing the
 * design's gnode grammar with the workbench (canvas/elements.ts): solid
 * categorical dots with a white ring and a mono label chip below. Kept separate
 * from the Svelte component so the sizing math is unit-testable and so the
 * style array is inspectable (the width:'label' landmine is a data-value bug we
 * guard against by never using it — node width is always a numeric function).
 *
 * Declutter rules: edges carry NO weight label at rest — weights appear only on
 * `.focus` edges (the ones incident to the selected node), and everything
 * outside the selection neighborhood gets `.dimmed` (OverviewCanvas.sync owns
 * both classes).
 */
import type { EdgeSingular, NodeSingular, StylesheetStyle } from 'cytoscape'
import { gnodeLabelStyle } from '$lib/components/canvas/elements'

/** Dot diameter from the node's symbol count: sqrt scale, clamped to [20, 44]px. */
export function nodeSize(symbolCount: number): number {
  const n = Number.isFinite(symbolCount) && symbolCount > 0 ? symbolCount : 0
  return clamp(20 + Math.sqrt(n) * 2.4, 20, 44)
}

/** Edge width from its call weight: log2 scale, clamped to [1, 5]px. */
export function edgeWidth(weight: number): number {
  const w = Number.isFinite(weight) && weight > 0 ? weight : 0
  return clamp(1 + Math.log2(w + 1) * 0.9, 1, 5)
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v))
}

export const overviewStyle: StylesheetStyle[] = [
  // base node — a solid dot in the palette hue (fg data from the model's pick),
  // white ring, mono label chip below. NEVER width:'label' (see landmine note).
  {
    selector: 'node',
    style: {
      shape: 'ellipse',
      width: (ele: NodeSingular) => nodeSize(Number(ele.data('symbolCount'))),
      height: (ele: NodeSingular) => nodeSize(Number(ele.data('symbolCount'))),
      'background-color': (ele: NodeSingular) => String(ele.data('fg')),
      'border-width': 2.5,
      'border-color': '#FFFFFF',
      label: (ele: NodeSingular) => String(ele.data('label') ?? ''),
      ...gnodeLabelStyle
    }
  },
  // directories — rolled-up packages read slightly heavier than leaves
  {
    selector: 'node[kind = "dir"]',
    style: {
      'font-weight': 600
    }
  },
  // an expanded file becomes a compound parent: its symbol children sit inside,
  // so it cannot stay a dot — draw a translucent container with the label chip
  // pinned to the top edge
  {
    selector: 'node[kind = "file"]:parent',
    style: {
      shape: 'round-rectangle',
      'background-color': (ele: NodeSingular) => String(ele.data('bg')),
      'background-opacity': 0.35,
      'border-width': 1.5,
      'border-color': (ele: NodeSingular) => String(ele.data('fg')),
      'border-style': 'dashed',
      'text-valign': 'top',
      'text-margin-y': -4,
      padding: '14px'
    }
  },
  // symbols — small fixed dots inside their file compound
  {
    selector: 'node[kind = "symbol"]',
    style: {
      width: 18,
      height: 18,
      'font-size': 9,
      'text-margin-y': 4
    }
  },
  {
    selector: 'node.is-selected',
    style: {
      color: '#364FC7',
      'font-weight': 600,
      'overlay-color': '#3B5BDB',
      'overlay-opacity': 0.14,
      'overlay-padding': 5
    }
  },
  // base edge — width by weight, directed, NO label at rest (weight chips on
  // every edge were the single biggest source of clutter)
  {
    selector: 'edge',
    style: {
      width: (ele: EdgeSingular) => edgeWidth(Number(ele.data('weight'))),
      'line-color': '#C4CAD1',
      'target-arrow-color': '#C4CAD1',
      'target-arrow-shape': 'triangle',
      'arrow-scale': 0.8,
      'curve-style': 'bezier',
      label: ''
    }
  },
  // symbol-level edges (revealed by drilldown) — accent hue so precise calls
  // stand apart from rolled-up aggregate edges
  {
    selector: 'edge[kind = "symbol"]',
    style: {
      'line-color': '#74C0FC',
      'target-arrow-color': '#74C0FC'
    }
  },
  // edges incident to the selected node: darken and reveal the call weight
  {
    selector: 'edge.focus',
    style: {
      'line-color': '#868E96',
      'target-arrow-color': '#868E96',
      label: 'data(wlabel)',
      color: '#495057',
      'font-family': 'IBM Plex Mono, monospace',
      'font-size': 9,
      'text-rotation': 'none',
      'text-background-color': '#FFFFFF',
      'text-background-opacity': 0.92,
      'text-background-shape': 'roundrectangle',
      'text-background-padding': '2px'
    }
  },
  {
    selector: 'edge[kind = "symbol"].focus',
    style: {
      'line-color': '#1C7ED6',
      'target-arrow-color': '#1C7ED6'
    }
  },
  // everything outside the selection neighborhood recedes
  {
    selector: '.dimmed',
    style: {
      opacity: 0.18
    }
  }
]
