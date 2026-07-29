import { describe, it, expect } from 'vitest'
import type { ApiError, FoundNode } from '$lib/types/graph'
import { groupResults, parseQuery, semanticDisableReason } from './query'

function node(label: string, name: string): FoundNode {
  return { node_id: `${label}:${name}`, node_key: `${label}:${name}`, label, name, signature: '', file_path: '', service: '' }
}

describe('parseQuery', () => {
  it('parses plain free text with no filters', () => {
    expect(parseQuery('spendBudget')).toEqual({ text: 'spendBudget', label: undefined, service: undefined })
  })

  it('parses plain free text with multiple words', () => {
    expect(parseQuery('spend budget handler')).toEqual({
      text: 'spend budget handler',
      label: undefined,
      service: undefined
    })
  })

  it('extracts a label: filter and normalizes casing to canonical form', () => {
    expect(parseQuery('label:function budget')).toEqual({ text: 'budget', label: 'Function', service: undefined })
    expect(parseQuery('LABEL:FUNCTION budget')).toEqual({ text: 'budget', label: 'Function', service: undefined })
    expect(parseQuery('Label:Method budget')).toEqual({ text: 'budget', label: 'Method', service: undefined })
  })

  it('extracts every canonical label regardless of input casing', () => {
    const labels: Array<[string, string]> = [
      ['function', 'Function'],
      ['method', 'Method'],
      ['class', 'Class'],
      ['interface', 'Interface'],
      ['file', 'File'],
      ['symbol', 'Symbol'],
      ['variable', 'Variable'],
      ['document', 'Document'],
      ['documentchunk', 'DocumentChunk'],
      ['service', 'Service']
    ]
    for (const [input, canonical] of labels) {
      expect(parseQuery(`label:${input}`).label).toBe(canonical)
    }
  })

  it('extracts a svc: filter, preserving its casing verbatim', () => {
    expect(parseQuery('svc:CodeGraph budget')).toEqual({
      text: 'budget',
      label: undefined,
      service: 'CodeGraph'
    })
  })

  it('extracts both label: and svc: filters together, in either order', () => {
    expect(parseQuery('label:Function svc:codegraph budget')).toEqual({
      text: 'budget',
      label: 'Function',
      service: 'codegraph'
    })
    expect(parseQuery('svc:codegraph label:Function budget')).toEqual({
      text: 'budget',
      label: 'Function',
      service: 'codegraph'
    })
  })

  it('leaves an unrecognized label: value in the free text', () => {
    expect(parseQuery('label:Bogus budget')).toEqual({
      text: 'label:Bogus budget',
      label: undefined,
      service: undefined
    })
  })

  it('returns empty text and no filters for an empty string', () => {
    expect(parseQuery('')).toEqual({ text: '', label: undefined, service: undefined })
  })

  it('returns empty text and no filters for a whitespace-only string', () => {
    expect(parseQuery('   ')).toEqual({ text: '', label: undefined, service: undefined })
  })

  it('collapses extra whitespace between remaining free-text tokens', () => {
    expect(parseQuery('spend   label:Function   budget')).toEqual({
      text: 'spend budget',
      label: 'Function',
      service: undefined
    })
  })

  it('treats a trailing colon with no value as plain text, not a filter', () => {
    expect(parseQuery('label: budget')).toEqual({ text: 'label: budget', label: undefined, service: undefined })
  })

  it('treats an unknown key: prefix as plain text', () => {
    expect(parseQuery('kind:Function budget')).toEqual({
      text: 'kind:Function budget',
      label: undefined,
      service: undefined
    })
  })
})

describe('groupResults', () => {
  it('groups results by label in the documented order', () => {
    const nodes = [
      node('Document', 'RFC-011'),
      node('Function', 'spendBudget'),
      node('Method', 'remainingBudget'),
      node('File', 'match.go'),
      node('Class', 'Runner')
    ]
    const groups = groupResults(nodes)
    expect(groups.map((g) => g.label)).toEqual(['Function', 'Method', 'Class', 'File', 'Document'])
  })

  it('preserves the full documented order across all canonical labels', () => {
    const nodes = [
      node('Service', 'svc'),
      node('DocumentChunk', 'chunk'),
      node('Document', 'doc'),
      node('Variable', 'v'),
      node('Symbol', 'sym'),
      node('File', 'f'),
      node('Interface', 'i'),
      node('Class', 'c'),
      node('Method', 'm'),
      node('Function', 'fn')
    ]
    const groups = groupResults(nodes)
    expect(groups.map((g) => g.label)).toEqual([
      'Function',
      'Method',
      'Class',
      'Interface',
      'File',
      'Symbol',
      'Variable',
      'Document',
      'DocumentChunk',
      'Service'
    ])
  })

  it('appends unrecognized labels alphabetically after the known groups', () => {
    const nodes = [node('Zebra', 'z'), node('Function', 'fn'), node('Apple', 'a')]
    const groups = groupResults(nodes)
    expect(groups.map((g) => g.label)).toEqual(['Function', 'Apple', 'Zebra'])
  })

  it('groups nodes of the same label together and preserves their relative order', () => {
    const a = node('Function', 'aaa')
    const b = node('Function', 'bbb')
    const c = node('Function', 'ccc')
    const groups = groupResults([a, b, c])
    expect(groups).toEqual([{ label: 'Function', nodes: [a, b, c] }])
  })

  it('returns an empty array for no results', () => {
    expect(groupResults([])).toEqual([])
  })

  it('omits groups that have no members', () => {
    const groups = groupResults([node('Function', 'fn')])
    expect(groups).toEqual([{ label: 'Function', nodes: [node('Function', 'fn')] }])
  })
})

describe('semanticDisableReason', () => {
  it('returns the verbatim server message for a tool-error mentioning the embedding provider', () => {
    const err: ApiError = { error: 'embedding provider returned 503: connection refused', kind: 'tool-error' }
    expect(semanticDisableReason(err)).toBe('embedding provider returned 503: connection refused')
  })

  it('matches case-insensitively', () => {
    const err: ApiError = { error: 'Embedding Provider misconfigured', kind: 'tool-error' }
    expect(semanticDisableReason(err)).toBe('Embedding Provider misconfigured')
  })

  it('returns null for a tool-error unrelated to the embedding provider', () => {
    const err: ApiError = { error: 'cypher syntax error near WHERE', kind: 'tool-error' }
    expect(semanticDisableReason(err)).toBeNull()
  })

  it('returns null for non-tool-error kinds even if the message mentions the embedding provider', () => {
    const err: ApiError = { error: 'embedding provider timeout', kind: 'timeout' }
    expect(semanticDisableReason(err)).toBeNull()
    const err2: ApiError = { error: 'embedding provider misconfigured', kind: 'validation' }
    expect(semanticDisableReason(err2)).toBeNull()
    const err3: ApiError = { error: 'embedding provider misconfigured', kind: 'internal' }
    expect(semanticDisableReason(err3)).toBeNull()
  })
})
