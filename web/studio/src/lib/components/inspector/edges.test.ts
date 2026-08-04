import { describe, it, expect } from 'vitest'
import type { GraphEdge } from '$lib/types/graph'
import { displayName, groupIncident } from './edges'

function edge(from: string, to: string, type: string, strategy?: string, confidence?: number): GraphEdge {
  return { from, to, type, strategy, confidence }
}

describe('groupIncident', () => {
  it('partitions edges by (relType, direction) relative to the node', () => {
    const edges = [
      edge('fn:A', 'fn:B', 'CALLS'),
      edge('fn:A', 'fn:C', 'CALLS'),
      edge('fn:D', 'fn:A', 'CALLS'),
      edge('doc:chunk1', 'fn:A', 'MENTIONS')
    ]
    const groups = groupIncident('fn:A', edges)

    expect(groups).toHaveLength(3)
    const callsOut = groups.find((g) => g.relType === 'CALLS' && g.direction === 'out')
    const callsIn = groups.find((g) => g.relType === 'CALLS' && g.direction === 'in')
    const mentionsIn = groups.find((g) => g.relType === 'MENTIONS' && g.direction === 'in')

    expect(callsOut?.edges).toHaveLength(2)
    expect(callsIn?.edges).toHaveLength(1)
    expect(mentionsIn?.edges).toHaveLength(1)
  })

  it('extracts the neighbor id from the "to" side for outgoing edges', () => {
    const groups = groupIncident('fn:A', [edge('fn:A', 'fn:B', 'CALLS')])
    expect(groups[0].edges[0].neighborId).toBe('fn:B')
  })

  it('extracts the neighbor id from the "from" side for incoming edges', () => {
    const groups = groupIncident('fn:A', [edge('fn:D', 'fn:A', 'CALLS')])
    expect(groups[0].edges[0].neighborId).toBe('fn:D')
  })

  it('excludes edges that do not touch the node', () => {
    const edges = [edge('fn:X', 'fn:Y', 'CALLS'), edge('fn:A', 'fn:B', 'CALLS')]
    const groups = groupIncident('fn:A', edges)
    expect(groups).toHaveLength(1)
    expect(groups[0].edges).toHaveLength(1)
    expect(groups[0].edges[0].neighborId).toBe('fn:B')
  })

  it('counts correctly across multiple edges in the same group', () => {
    const edges = [
      edge('fn:A', 'fn:B', 'CALLS'),
      edge('fn:A', 'fn:C', 'CALLS'),
      edge('fn:A', 'fn:D', 'CALLS'),
      edge('fn:A', 'fn:E', 'CALLS')
    ]
    const groups = groupIncident('fn:A', edges)
    expect(groups).toHaveLength(1)
    expect(groups[0].edges).toHaveLength(4)
  })

  it('carries provenance fields (strategy, confidence) through to the grouped edge', () => {
    const edges = [edge('doc:chunk1', 'fn:A', 'MENTIONS', 'docmine/codespan', 0.9)]
    const groups = groupIncident('fn:A', edges)
    expect(groups[0].edges[0].edge.strategy).toBe('docmine/codespan')
    expect(groups[0].edges[0].edge.confidence).toBe(0.9)
  })

  it('treats a self-loop as outgoing only', () => {
    const groups = groupIncident('fn:A', [edge('fn:A', 'fn:A', 'CALLS')])
    expect(groups).toHaveLength(1)
    expect(groups[0].direction).toBe('out')
    expect(groups[0].edges[0].neighborId).toBe('fn:A')
  })

  it('returns an empty array when no edges touch the node', () => {
    const groups = groupIncident('fn:Z', [edge('fn:A', 'fn:B', 'CALLS')])
    expect(groups).toEqual([])
  })

  it('preserves first-encounter order of (relType, direction) pairs', () => {
    const edges = [
      edge('fn:D', 'fn:A', 'CALLS'), // in
      edge('fn:A', 'fn:B', 'CALLS'), // out
      edge('doc:c', 'fn:A', 'MENTIONS') // in
    ]
    const groups = groupIncident('fn:A', edges)
    expect(groups.map((g) => `${g.relType}:${g.direction}`)).toEqual(['CALLS:in', 'CALLS:out', 'MENTIONS:in'])
  })
})

describe('displayName', () => {
  it('renders outgoing relations verbatim', () => {
    expect(displayName('CALLS', 'out')).toBe('CALLS')
    expect(displayName('DEFINES', 'out')).toBe('DEFINES')
    expect(displayName('REFERENCES', 'out')).toBe('REFERENCES')
    expect(displayName('CONTAINS', 'out')).toBe('CONTAINS')
    expect(displayName('MENTIONS', 'out')).toBe('MENTIONS')
    expect(displayName('INHERITS_FROM', 'out')).toBe('INHERITS_FROM')
  })

  it('maps the documented "_BY" suffix types for incoming direction', () => {
    expect(displayName('CALLS', 'in')).toBe('CALLED_BY')
    expect(displayName('DEFINES', 'in')).toBe('DEFINED_BY')
    expect(displayName('REFERENCES', 'in')).toBe('REFERENCED_BY')
    expect(displayName('CONTAINS', 'in')).toBe('CONTAINED_IN')
    expect(displayName('MENTIONS', 'in')).toBe('MENTIONED_BY')
  })

  it('falls back to "← relType" for other incoming relation types', () => {
    expect(displayName('IMPLEMENTS', 'in')).toBe('← IMPLEMENTS')
    expect(displayName('INHERITS_FROM', 'in')).toBe('← INHERITS_FROM')
  })
})
