import { describe, it, expect } from 'vitest'
import cytoscape from 'cytoscape'
import { canvasStyle, createCanvasController } from './elements'
import type { GraphNode, GraphEdge } from '$lib/types/graph'

function n(id: string): GraphNode {
  return { node_id: id, label: 'Function', name: `fn${id}` }
}

/**
 * Regression: with styleEnabled, every synced node must be visible().
 * Two past causes: width:'label' (deprecated value aborts style application)
 * and elements added inside cy.batch() never receiving initial style.
 */
describe('canvas visibility with styleEnabled', () => {
  it('all nodes visible() across incremental syncs', () => {
    const cy = cytoscape({ headless: true, styleEnabled: true, style: canvasStyle as never })
    const cb = { onSelect: () => {}, onTogglePin: () => {}, onExpandRequest: () => {} }
    const ctl = createCanvasController(cy, cb)

    ctl.sync([n('a')], [], 'a', [])
    expect(cy.nodes().filter((x) => x.visible()).length).toBe(1)

    const more: GraphNode[] = ['a', 'b', 'c', 'd'].map(n)
    const edges: GraphEdge[] = [
      { from: 'a', to: 'b', type: 'CALLS' },
      { from: 'a', to: 'c', type: 'CALLS' },
      { from: 'a', to: 'd', type: 'CALLS', strategy: 'semlink/m', confidence: 0.5 }
    ]
    ctl.sync(more, edges, 'a', ['b'])
    expect(cy.nodes().length).toBe(4)
    expect(cy.nodes().filter((x) => x.visible()).length).toBe(4)
    expect(cy.edges().filter((x) => x.visible()).length).toBe(3)
  })
})
