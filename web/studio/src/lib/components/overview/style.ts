/**
 * Cytoscape stylesheet + sizing helpers for the Overview canvas. Kept separate
 * from the Svelte component so the sizing math is unit-testable and so the
 * style array is inspectable (the width:'label' landmine is a data-value bug we
 * guard against by never using it — node width is always a numeric function).
 */
import type { EdgeSingular, NodeSingular, StylesheetStyle } from 'cytoscape'

/** Node diameter from its symbol count: sqrt scale, clamped to [24, 96]px. */
export function nodeSize(symbolCount: number): number {
  const n = Number.isFinite(symbolCount) && symbolCount > 0 ? symbolCount : 0
  return clamp(24 + Math.sqrt(n) * 12, 24, 96)
}

/** Edge width from its call weight: log2 scale, clamped to [1, 8]px. */
export function edgeWidth(weight: number): number {
  const w = Number.isFinite(weight) && weight > 0 ? weight : 0
  return clamp(1 + Math.log2(w + 1) * 1.6, 1, 8)
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v))
}

export const overviewStyle: StylesheetStyle[] = [
  // base node — every node carries fg/bg data from the model's palette pick
  {
    selector: 'node',
    style: {
      // NEVER width:'label' — see the landmine note; size by symbolCount.
      width: (ele: NodeSingular) => nodeSize(Number(ele.data('symbolCount'))),
      height: (ele: NodeSingular) => nodeSize(Number(ele.data('symbolCount'))),
      'background-color': (ele: NodeSingular) => String(ele.data('bg')),
      'border-width': 1.5,
      'border-color': (ele: NodeSingular) => String(ele.data('fg')),
      label: (ele: NodeSingular) => String(ele.data('label') ?? ''),
      color: '#16181D',
      'font-family': 'IBM Plex Sans, sans-serif',
      'font-size': 10,
      'text-valign': 'center',
      'text-halign': 'center',
      'text-wrap': 'ellipsis',
      'text-max-width': '120px'
    }
  },
  // directories — round-rect, larger font, label above the shape so rolled-up
  // dirs read as containers rather than leaves
  {
    selector: 'node[kind = "dir"]',
    style: {
      shape: 'round-rectangle',
      'font-size': 12,
      'font-weight': 600
    }
  },
  // files — rectangle
  {
    selector: 'node[kind = "file"]',
    style: {
      shape: 'rectangle'
    }
  },
  // an expanded file becomes a compound parent: its symbol children sit inside,
  // so draw it as a translucent container with the label at the top
  {
    selector: 'node[kind = "file"]:parent',
    style: {
      shape: 'round-rectangle',
      'background-opacity': 0.14,
      'text-valign': 'top',
      'text-halign': 'center',
      padding: '12px'
    }
  },
  // symbols — small ellipse inside their file compound
  {
    selector: 'node[kind = "symbol"]',
    style: {
      shape: 'ellipse',
      width: 26,
      height: 26,
      'font-size': 9
    }
  },
  {
    selector: 'node.is-selected',
    style: {
      'border-width': 3,
      'border-color': '#3B5BDB',
      'overlay-color': '#3B5BDB',
      'overlay-opacity': 0.12,
      'overlay-padding': 6
    }
  },
  // base edge — width by weight, directed
  {
    selector: 'edge',
    style: {
      width: (ele: EdgeSingular) => edgeWidth(Number(ele.data('weight'))),
      'line-color': '#ADB5BD',
      'target-arrow-color': '#ADB5BD',
      'target-arrow-shape': 'triangle',
      'arrow-scale': 0.9,
      'curve-style': 'bezier',
      label: 'data(wlabel)',
      color: '#495057',
      'font-family': 'IBM Plex Mono, monospace',
      'font-size': 9,
      'text-rotation': 'none',
      'text-background-color': '#F1F3F5',
      'text-background-opacity': 1,
      'text-background-padding': '2px',
      'text-background-shape': 'roundrectangle'
    }
  },
  // symbol-level edges (revealed by drilldown) — accent hue so precise calls
  // stand apart from rolled-up aggregate edges
  {
    selector: 'edge[kind = "symbol"]',
    style: {
      'line-color': '#1C7ED6',
      'target-arrow-color': '#1C7ED6',
      'line-style': 'solid'
    }
  }
]
