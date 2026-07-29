import { describe, it, expect } from 'vitest'
import type { GraphEdge, GraphNode } from '$lib/types/graph'
import {
  canvasStyle,
  computeDiff,
  diffIds,
  edgeLabelText,
  edgeProvenance,
  nodeColors,
  toEdgeElement,
  toNodeElement
} from './elements'

function node(id: string, label = 'Function', name = id): GraphNode {
  return { node_id: id, label, name }
}

function edge(from: string, to: string, type = 'CALLS', strategy?: string, confidence?: number): GraphEdge {
  return { from, to, type, strategy, confidence }
}

describe('nodeColors', () => {
  it('maps each documented label to its hue pair', () => {
    expect(nodeColors('Service')).toEqual({ fg: '#7048E8', bg: '#F3F0FF' })
    expect(nodeColors('File')).toEqual({ fg: '#495057', bg: '#F1F3F5' })
    expect(nodeColors('Function')).toEqual({ fg: '#1C7ED6', bg: '#E7F5FF' })
    expect(nodeColors('Method')).toEqual({ fg: '#0B7285', bg: '#E3FAFC' })
    expect(nodeColors('Class')).toEqual({ fg: '#E8590C', bg: '#FFF4E6' })
    expect(nodeColors('Interface')).toEqual({ fg: '#9C36B5', bg: '#F8F0FC' })
    expect(nodeColors('Variable')).toEqual({ fg: '#868E96', bg: '#F8F9FA' })
    expect(nodeColors('Symbol')).toEqual({ fg: '#087F5B', bg: '#E6FCF5' })
    expect(nodeColors('Document')).toEqual({ fg: '#C2255C', bg: '#FFF0F6' })
    expect(nodeColors('DocumentChunk')).toEqual({ fg: '#E64980', bg: '#FFF0F6' })
  })

  it('falls back to Variable colors for unknown labels', () => {
    expect(nodeColors('SomethingNew')).toEqual(nodeColors('Variable'))
  })
})

describe('edgeProvenance', () => {
  it('classifies edges with no strategy as structural', () => {
    expect(edgeProvenance({ strategy: undefined })).toBe('structural')
  })

  it('classifies docmine/* strategies', () => {
    expect(edgeProvenance({ strategy: 'docmine/codespan' })).toBe('docmine')
    expect(edgeProvenance({ strategy: 'docmine' })).toBe('docmine')
  })

  it('classifies semlink/* strategies', () => {
    expect(edgeProvenance({ strategy: 'semlink/gpt-5-nano' })).toBe('semlink')
    expect(edgeProvenance({ strategy: 'semlink' })).toBe('semlink')
  })

  it('falls back to structural for an unrecognized strategy prefix', () => {
    expect(edgeProvenance({ strategy: 'mystery/thing' })).toBe('structural')
  })
})

describe('edgeLabelText', () => {
  it('is empty for structural edges', () => {
    expect(edgeLabelText({ strategy: undefined, confidence: undefined })).toBe('')
  })

  it('formats docmine confidence to 2 decimals', () => {
    expect(edgeLabelText({ strategy: 'docmine/codespan', confidence: 0.9 })).toBe('docmine 0.90')
  })

  it('formats semlink confidence to 2 decimals', () => {
    expect(edgeLabelText({ strategy: 'semlink/gpt-5-nano', confidence: 0.57 })).toBe('semlink 0.57')
  })

  it('rounds confidence to 2 decimals', () => {
    expect(edgeLabelText({ strategy: 'docmine/x', confidence: 0.526 })).toBe('docmine 0.53')
  })

  it('defaults confidence to 0 when missing on a provenance edge', () => {
    expect(edgeLabelText({ strategy: 'semlink/x', confidence: undefined })).toBe('semlink 0.00')
  })
})

describe('toNodeElement', () => {
  it('maps GraphNode fields to cytoscape node data', () => {
    const el = toNodeElement(node('n1', 'Function', 'matchChunks()'))
    expect(el.group).toBe('nodes')
    expect(el.data).toEqual({ id: 'n1', label: 'Function', name: 'matchChunks()' })
  })

  it('has no parent (flat graph, no compound nodes)', () => {
    const el = toNodeElement(node('n1'))
    expect((el.data as Record<string, unknown>).parent).toBeUndefined()
  })

  it('carries a label-<Label> class for stylesheet targeting', () => {
    const el = toNodeElement(node('n1', 'Method'))
    expect(el.classes).toContain('label-Method')
  })

  it('adds is-selected when selected', () => {
    const el = toNodeElement(node('n1'), { selected: true })
    expect(el.classes).toContain('is-selected')
  })

  it('omits is-selected when not selected', () => {
    const el = toNodeElement(node('n1'), { selected: false })
    expect(el.classes).not.toContain('is-selected')
  })

  it('adds is-pinned when pinned', () => {
    const el = toNodeElement(node('n1'), { pinned: true })
    expect(el.classes).toContain('is-pinned')
  })
})

describe('toEdgeElement', () => {
  it('maps source/target/id from edgeKey and keeps the relation type', () => {
    const el = toEdgeElement(edge('a', 'b', 'CALLS'))
    expect(el.group).toBe('edges')
    expect(el.data).toMatchObject({ id: 'a|CALLS|b', source: 'a', target: 'b', type: 'CALLS' })
  })

  it('tags structural edges with prov-structural and an empty label', () => {
    const el = toEdgeElement(edge('a', 'b', 'CONTAINS'))
    expect(el.classes).toContain('prov-structural')
    expect((el.data as Record<string, unknown>).label).toBe('')
  })

  it('tags docmine edges with prov-docmine and a formatted confidence label', () => {
    const el = toEdgeElement(edge('a', 'b', 'MENTIONS', 'docmine/codespan', 0.9))
    expect(el.classes).toContain('prov-docmine')
    expect((el.data as Record<string, unknown>).label).toBe('docmine 0.90')
  })

  it('tags semlink edges with prov-semlink and a formatted confidence label', () => {
    const el = toEdgeElement(edge('a', 'b', 'MENTIONS', 'semlink/gpt-5-nano', 0.57))
    expect(el.classes).toContain('prov-semlink')
    expect((el.data as Record<string, unknown>).label).toBe('semlink 0.57')
  })
})

describe('diffIds', () => {
  it('reports newly added ids', () => {
    const { added, removed } = diffIds(new Set(['a']), new Set(['a', 'b']))
    expect(added).toEqual(['b'])
    expect(removed).toEqual([])
  })

  it('reports removed ids', () => {
    const { added, removed } = diffIds(new Set(['a', 'b']), new Set(['a']))
    expect(added).toEqual([])
    expect(removed).toEqual(['b'])
  })

  it('reports both additions and removals in one diff', () => {
    const { added, removed } = diffIds(new Set(['a', 'b']), new Set(['b', 'c']))
    expect(added).toEqual(['c'])
    expect(removed).toEqual(['a'])
  })

  it('reports nothing when sets are identical', () => {
    const { added, removed } = diffIds(new Set(['a', 'b']), new Set(['a', 'b']))
    expect(added).toEqual([])
    expect(removed).toEqual([])
  })

  it('handles an empty current set (initial load)', () => {
    const { added, removed } = diffIds(new Set(), new Set(['a', 'b']))
    expect(added).toEqual(['a', 'b'])
    expect(removed).toEqual([])
  })

  it('handles an empty next set (clear all)', () => {
    const { added, removed } = diffIds(new Set(['a', 'b']), new Set())
    expect(added).toEqual([])
    expect(removed).toEqual(['a', 'b'])
  })
})

describe('computeDiff', () => {
  it('computes independent node and edge diffs', () => {
    const currentNodeIds = new Set(['a', 'b'])
    const currentEdgeIds = new Set(['a|CALLS|b'])
    const nextNodes = [node('a'), node('c')]
    const nextEdges = [edge('a', 'c', 'CALLS')]

    const diff = computeDiff(currentNodeIds, currentEdgeIds, nextNodes, nextEdges)

    expect(diff.addedNodeIds).toEqual(['c'])
    expect(diff.removedNodeIds).toEqual(['b'])
    expect(diff.addedEdgeIds).toEqual(['a|CALLS|c'])
    expect(diff.removedEdgeIds).toEqual(['a|CALLS|b'])
  })

  it('reports no diff when node/edge sets already match', () => {
    const diff = computeDiff(new Set(['a']), new Set([]), [node('a')], [])
    expect(diff.addedNodeIds).toEqual([])
    expect(diff.removedNodeIds).toEqual([])
    expect(diff.addedEdgeIds).toEqual([])
    expect(diff.removedEdgeIds).toEqual([])
  })
})

describe('canvasStyle', () => {
  it('defines a base node selector and a base edge selector', () => {
    const selectors = canvasStyle.map((s) => s.selector)
    expect(selectors).toContain('node')
    expect(selectors).toContain('edge')
  })

  it('defines selection and pin modifier selectors', () => {
    const selectors = canvasStyle.map((s) => s.selector)
    expect(selectors).toContain('node.is-selected')
    expect(selectors).toContain('node.is-pinned')
  })

  it('defines docmine and semlink edge modifier selectors', () => {
    const selectors = canvasStyle.map((s) => s.selector)
    expect(selectors).toContain('edge.prov-docmine')
    expect(selectors).toContain('edge.prov-semlink')
  })

  it('gives semlink edges a dashed line style distinct from docmine', () => {
    const semlink = canvasStyle.find((s) => s.selector === 'edge.prov-semlink')!
    const docmine = canvasStyle.find((s) => s.selector === 'edge.prov-docmine')!
    expect((semlink.style as Record<string, unknown>)['line-style']).toBe('dashed')
    expect((docmine.style as Record<string, unknown>)['line-style']).toBeUndefined()
  })
})
