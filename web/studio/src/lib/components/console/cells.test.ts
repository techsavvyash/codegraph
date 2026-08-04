import { describe, it, expect } from 'vitest'
import {
  classifyCell,
  cellSummary,
  expandedJson,
  isElementId,
  isNodeIdColumnName,
  collectNodeIds,
  graphLinkForIds
} from './cells'
import type { CypherNode, CypherRelationship, CypherValue } from '$lib/types/console'

const node: CypherNode = {
  _type: 'node',
  _id: '4:abc-uuid:79268',
  _labels: ['Service'],
  props: { name: 'codegraph', language: 'Go' }
}
const rel: CypherRelationship = {
  _type: 'relationship',
  _id: '5:abc-uuid:12',
  _rtype: 'CALLS',
  _start: '4:abc-uuid:1',
  _end: '4:abc-uuid:2',
  props: {}
}

describe('classifyCell', () => {
  it('classifies scalars, null, arrays, objects, nodes, rels', () => {
    expect(classifyCell('x')).toBe('scalar')
    expect(classifyCell(42)).toBe('scalar')
    expect(classifyCell(true)).toBe('scalar')
    expect(classifyCell(null)).toBe('null')
    expect(classifyCell([1, 2])).toBe('array')
    expect(classifyCell({ a: 1 })).toBe('object')
    expect(classifyCell(node)).toBe('node')
    expect(classifyCell(rel)).toBe('relationship')
  })
})

describe('cellSummary', () => {
  it('summarizes a node with its label and name', () => {
    expect(cellSummary(node)).toBe('Service: codegraph')
  })
  it('falls back to label when a node has no name-like prop', () => {
    expect(cellSummary({ ...node, props: { language: 'Go' } })).toBe('Service')
  })
  it('summarizes a relationship by type', () => {
    expect(cellSummary(rel)).toBe('[:CALLS]')
  })
  it('summarizes arrays and objects by size', () => {
    expect(cellSummary([1, 2, 3])).toBe('[3]')
    expect(cellSummary({ a: 1, b: 2 })).toBe('{2}')
  })
  it('renders scalars and null plainly', () => {
    expect(cellSummary(7)).toBe('7')
    expect(cellSummary(null)).toBe('null')
  })
})

describe('expandedJson', () => {
  it('pretty-prints a node', () => {
    expect(expandedJson(node)).toContain('"name": "codegraph"')
    expect(expandedJson(node)).toContain('\n')
  })
})

describe('isElementId', () => {
  it('accepts real neo4j element ids', () => {
    expect(isElementId('4:902a108f-35df-4be3-ad95-3eb1957b8e8d:79268')).toBe(true)
  })
  it('rejects bare numbers and arbitrary strings', () => {
    expect(isElementId('42')).toBe(false)
    expect(isElementId('codegraph')).toBe(false)
    expect(isElementId('4:79268')).toBe(false)
    expect(isElementId(42)).toBe(false)
    expect(isElementId(null)).toBe(false)
  })
})

describe('isNodeIdColumnName', () => {
  it('matches id / node_id / nodeId', () => {
    expect(isNodeIdColumnName('id')).toBe(true)
    expect(isNodeIdColumnName('node_id')).toBe(true)
    expect(isNodeIdColumnName('nodeId')).toBe(true)
    expect(isNodeIdColumnName('nodeid')).toBe(true)
  })
  it('does not match unrelated columns', () => {
    expect(isNodeIdColumnName('name')).toBe(false)
    expect(isNodeIdColumnName('identifier')).toBe(false)
    expect(isNodeIdColumnName('idx')).toBe(false)
  })
})

describe('collectNodeIds', () => {
  const eid = (n: number) => `4:902a108f-35df-4be3-ad95-3eb1957b8e8d:${n}`

  it('collects ids from a node-id column of genuine element ids', () => {
    const rows: Array<Record<string, CypherValue>> = [
      { nodeId: eid(1), name: 'a' },
      { nodeId: eid(2), name: 'b' }
    ]
    expect(collectNodeIds(['nodeId', 'name'], rows, 50)).toEqual([eid(1), eid(2)])
  })

  it('returns [] when the id column holds non-element-id values', () => {
    const rows = [{ id: 'not-an-id' }, { id: 'nope' }]
    expect(collectNodeIds(['id'], rows, 50)).toEqual([])
  })

  it('returns [] when no column is named like an id', () => {
    const rows = [{ name: eid(1) }]
    expect(collectNodeIds(['name'], rows, 50)).toEqual([])
  })

  it('dedupes and caps at the limit', () => {
    const rows = [{ id: eid(1) }, { id: eid(1) }, { id: eid(2) }, { id: eid(3) }]
    expect(collectNodeIds(['id'], rows, 2)).toEqual([eid(1), eid(2)])
  })

  it('returns [] for empty rows', () => {
    expect(collectNodeIds(['id'], [], 50)).toEqual([])
  })
})

describe('graphLinkForIds', () => {
  it('builds a comma-joined encoded /graph link', () => {
    expect(graphLinkForIds(['4:u:1', '4:u:2'])).toBe('/graph?nodes=4%3Au%3A1,4%3Au%3A2')
  })
})
