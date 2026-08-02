import { describe, it, expect } from 'vitest'
import cytoscape from 'cytoscape'
import { overviewStyle, nodeSize, edgeWidth } from './style'

describe('nodeSize', () => {
  it('clamps to [24, 96]', () => {
    expect(nodeSize(0)).toBe(24)
    expect(nodeSize(-5)).toBe(24)
    expect(nodeSize(100000)).toBe(96)
  })
  it('grows monotonically with symbol count', () => {
    expect(nodeSize(4)).toBeGreaterThan(nodeSize(1))
    expect(nodeSize(16)).toBeGreaterThan(nodeSize(4))
  })
  it('tolerates NaN', () => {
    expect(nodeSize(NaN)).toBe(24)
  })
})

describe('edgeWidth', () => {
  it('clamps to [1, 8]', () => {
    expect(edgeWidth(0)).toBe(1)
    expect(edgeWidth(1_000_000)).toBe(8)
  })
  it('grows with weight', () => {
    expect(edgeWidth(8)).toBeGreaterThan(edgeWidth(1))
  })
})

/**
 * Regression mirror of canvas/visibility.test.ts: with styleEnabled every node
 * must be visible(). Guards against the width:'label' abort and compound-parent
 * misconfiguration leaving nodes unrendered.
 */
describe('overview canvas visibility with styleEnabled', () => {
  it('dir, file (compound), and symbol nodes are all visible', () => {
    const cy = cytoscape({ headless: true, styleEnabled: true, style: overviewStyle as never })
    cy.add([
      { group: 'nodes', data: { id: 'dir:internal', kind: 'dir', label: 'internal', symbolCount: 40, fg: '#7048E8', bg: '#F3F0FF' } },
      { group: 'nodes', data: { id: 'f1', kind: 'file', label: 'client.go', symbolCount: 6, fg: '#495057', bg: '#F1F3F5' } },
      { group: 'nodes', data: { id: 's1', kind: 'symbol', parent: 'f1', label: 'Connect', symbolCount: 0, fg: '#1C7ED6', bg: '#E7F5FF' } },
      { group: 'edges', data: { id: 'e1', source: 'dir:internal', target: 'f1', weight: 5, wlabel: '5', kind: 'aggregate' } },
      { group: 'edges', data: { id: 'e2', source: 's1', target: 'f1', weight: 1, wlabel: '', kind: 'symbol' } }
    ])
    expect(cy.nodes().length).toBe(3)
    expect(cy.nodes().filter((n) => n.visible()).length).toBe(3)
    expect(cy.edges().filter((e) => e.visible()).length).toBe(2)
    // f1 is a compound parent (symbol child), so it reports as :parent
    expect(cy.getElementById('f1').isParent()).toBe(true)
  })
})
