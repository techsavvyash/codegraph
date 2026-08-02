import { describe, it, expect } from 'vitest'
import cytoscape from 'cytoscape'
import { overviewStyle, nodeSize, edgeWidth } from './style'

describe('nodeSize', () => {
  it('clamps to [20, 44]', () => {
    expect(nodeSize(0)).toBe(20)
    expect(nodeSize(-5)).toBe(20)
    expect(nodeSize(100000)).toBe(44)
  })
  it('grows monotonically with symbol count', () => {
    expect(nodeSize(4)).toBeGreaterThan(nodeSize(1))
    expect(nodeSize(16)).toBeGreaterThan(nodeSize(4))
  })
  it('tolerates NaN', () => {
    expect(nodeSize(NaN)).toBe(20)
  })
})

describe('edgeWidth', () => {
  it('clamps to [1, 5]', () => {
    expect(edgeWidth(0)).toBe(1)
    expect(edgeWidth(1_000_000)).toBe(5)
  })
  it('grows with weight', () => {
    expect(edgeWidth(8)).toBeGreaterThan(edgeWidth(1))
  })
})

/**
 * Declutter grammar guards: edges must carry no label at rest — the call
 * weight is revealed only on `.focus` edges (incident to the selection).
 */
describe('edge label declutter grammar', () => {
  const styleFor = (selector: string) => overviewStyle.find((s) => s.selector === selector)?.style

  it('base edges have an empty label', () => {
    expect((styleFor('edge') as Record<string, unknown>).label).toBe('')
  })
  it('.focus edges reveal the weight label', () => {
    expect((styleFor('edge.focus') as Record<string, unknown>).label).toBe('data(wlabel)')
  })
  it('a .dimmed style exists for out-of-neighborhood elements', () => {
    expect(styleFor('.dimmed')).toBeDefined()
  })
})

/**
 * Lens decoration grammar guards: the new lens classes must exist, and the
 * synthetic flow-segment edge must be non-hit-testable with an empty label
 * (like ghost edges) so reconciliation can skip it and it never intercepts taps.
 */
describe('lens decoration grammar', () => {
  const styleFor = (selector: string) => overviewStyle.find((s) => s.selector === selector)?.style

  it('defines flow, usage, heat, and dead classes', () => {
    expect(styleFor('node.hl-path')).toBeDefined()
    expect(styleFor('edge.hl-path')).toBeDefined()
    for (const d of [0, 1, 2, 3]) expect(styleFor(`node.usage-d${d}`)).toBeDefined()
    for (const h of [0, 1, 2, 3, 4]) expect(styleFor(`node.heat-${h}`)).toBeDefined()
    for (const d of [1, 2, 3]) expect(styleFor(`node.dead-${d}`)).toBeDefined()
  })

  it('flowseg edges are non-hit-testable with an empty label', () => {
    const seg = styleFor('edge[kind = "flowseg"]') as Record<string, unknown>
    expect(seg.events).toBe('no')
    expect(seg.label).toBe('')
    expect(seg['line-style']).toBe('dashed')
  })

  it('base edge label is still empty (declutter grammar intact)', () => {
    expect((styleFor('edge') as Record<string, unknown>).label).toBe('')
  })

  it('heat ramp climbs from pale yellow to deep orange', () => {
    expect((styleFor('node.heat-0') as Record<string, unknown>)['background-color']).toBe('#FFF3BF')
    expect((styleFor('node.heat-4') as Record<string, unknown>)['background-color']).toBe('#D9480F')
  })

  it('dead ramp climbs from pale to deep red', () => {
    expect((styleFor('node.dead-1') as Record<string, unknown>)['background-color']).toBe('#FFA8A8')
    expect((styleFor('node.dead-3') as Record<string, unknown>)['background-color']).toBe('#C92A2A')
  })
})

/**
 * A flowseg edge with styleEnabled must still be a valid, rendered element (the
 * events:'no' + dashed styling must not abort the stylesheet like width:'label').
 */
describe('flowseg edge renders under styleEnabled', () => {
  it('adds and renders a flowseg edge alongside a base edge', () => {
    const cy = cytoscape({ headless: true, styleEnabled: true, style: overviewStyle as never })
    cy.add([
      { group: 'nodes', data: { id: 'na', kind: 'dir', label: 'a', symbolCount: 4, fg: '#7048E8', bg: '#F3F0FF' } },
      { group: 'nodes', data: { id: 'nb', kind: 'dir', label: 'b', symbolCount: 4, fg: '#7048E8', bg: '#F3F0FF' } },
      { group: 'edges', data: { id: 'flowseg:na->nb', source: 'na', target: 'nb', weight: 0, wlabel: '', kind: 'flowseg' } }
    ])
    cy.getElementById('na').addClass('hl-path')
    cy.getElementById('flowseg:na->nb').addClass('hl-path')
    expect(cy.nodes().filter((n) => n.visible()).length).toBe(2)
    // flowseg edge exists; visibility is fine (opacity/style applied, not aborted)
    expect(cy.getElementById('flowseg:na->nb').length).toBe(1)
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
      { group: 'edges', data: { id: 'e2', source: 's1', target: 'f1', weight: 1, wlabel: '1', kind: 'symbol' } }
    ])
    expect(cy.nodes().length).toBe(3)
    expect(cy.nodes().filter((n) => n.visible()).length).toBe(3)
    expect(cy.edges().filter((e) => e.visible()).length).toBe(2)
    // f1 is a compound parent (symbol child), so it reports as :parent
    expect(cy.getElementById('f1').isParent()).toBe(true)
    // focus/dimmed classes must not break visibility
    cy.edges().forEach((e) => {
      e.addClass('focus')
    })
    cy.getElementById('dir:internal').addClass('dimmed')
    expect(cy.nodes().filter((n) => n.visible()).length).toBe(3)
    expect(cy.edges().filter((e) => e.visible()).length).toBe(2)
  })
})
