import { describe, it, expect } from 'vitest'
import {
  insertAtCursor,
  scopeFilterSnippet,
  encodeQueryParam,
  decodeQueryParam,
  encodeConsoleState,
  decodeConsoleState,
  exampleQueries,
  SERVICE_PROPERTY
} from './query'

describe('insertAtCursor', () => {
  it('inserts at a collapsed caret', () => {
    const r = insertAtCursor('MATCH n', 5, 5, 'XX')
    expect(r.text).toBe('MATCHXX n')
    expect(r.cursor).toBe(7)
  })

  it('replaces a selection', () => {
    const r = insertAtCursor('abcdef', 1, 4, 'Z')
    expect(r.text).toBe('aZef')
    expect(r.cursor).toBe(2)
  })

  it('normalizes an inverted selection', () => {
    const r = insertAtCursor('abcdef', 4, 1, 'Z')
    expect(r.text).toBe('aZef')
    expect(r.cursor).toBe(2)
  })

  it('clamps out-of-range positions to the end', () => {
    const r = insertAtCursor('abc', 99, 99, 'X')
    expect(r.text).toBe('abcX')
    expect(r.cursor).toBe(4)
  })

  it('handles NaN caret by appending', () => {
    const r = insertAtCursor('abc', NaN, NaN, 'X')
    expect(r.text).toBe('abcX')
  })
})

describe('scopeFilterSnippet', () => {
  it('builds a serviceName predicate for a concrete service', () => {
    expect(scopeFilterSnippet('f', 'codegraph')).toBe(`f.${SERVICE_PROPERTY} = 'codegraph'`)
  })
  it('uses a placeholder when no service is active', () => {
    expect(scopeFilterSnippet('n', null)).toBe(`n.${SERVICE_PROPERTY} = '<service>'`)
  })
  it('defaults a blank variable to n', () => {
    expect(scopeFilterSnippet('   ', 'x')).toBe(`n.${SERVICE_PROPERTY} = 'x'`)
  })
})

describe('encode/decode query param', () => {
  it('round-trips multi-line queries with quotes and newlines', () => {
    const q = "MATCH (f:Function)\nWHERE f.name = 'x'\nRETURN f"
    const enc = encodeQueryParam(q)
    expect(enc).not.toContain('\n')
    expect(enc).not.toMatch(/[+/=]/) // base64url, no padding
    expect(decodeQueryParam(enc)).toBe(q)
  })

  it('round-trips unicode', () => {
    const q = 'MATCH (n) WHERE n.name = "café→λ" RETURN n'
    expect(decodeQueryParam(encodeQueryParam(q))).toBe(q)
  })

  it('returns null for null / empty / malformed input', () => {
    expect(decodeQueryParam(null)).toBeNull()
    expect(decodeQueryParam('')).toBeNull()
    expect(decodeQueryParam('!!!not base64!!!')).toBeNull()
  })
})

describe('exampleQueries', () => {
  it('scopes examples to a concrete service', () => {
    const ex = exampleQueries('codegraph')
    const dead = ex.find((e) => e.label.startsWith('Dead functions'))
    expect(dead).toBeDefined()
    expect(dead!.label).toContain('(codegraph)')
    expect(dead!.query).toContain(`f.${SERVICE_PROPERTY} = 'codegraph'`)
    const hubs = ex.find((e) => e.label.startsWith('Top CALLS hubs'))!
    expect(hubs.query).toContain(`WHERE f.${SERVICE_PROPERTY} = 'codegraph'`)
  })

  it('leaves examples unscoped for "All services"', () => {
    const ex = exampleQueries(null)
    const dead = ex.find((e) => e.label.startsWith('Dead functions'))!
    expect(dead.label).not.toContain('(')
    expect(dead.query).not.toContain(SERVICE_PROPERTY)
    const labels = ex.find((e) => e.label.startsWith('Label counts'))!
    expect(labels.query).not.toContain(SERVICE_PROPERTY)
  })

  it('always includes a Services example', () => {
    expect(exampleQueries(null).some((e) => e.label === 'Services')).toBe(true)
    expect(exampleQueries('x').some((e) => e.label === 'Services')).toBe(true)
  })
})

describe('encode/decode console state (query + params)', () => {
  it('params-free state emits the legacy shape (identical to encodeQueryParam)', () => {
    const q = 'MATCH (n:Service) RETURN n.name'
    expect(encodeConsoleState({ query: q })).toBe(encodeQueryParam(q))
    expect(encodeConsoleState({ query: q, paramsText: '   ' })).toBe(encodeQueryParam(q))
  })

  it('round-trips query + params through the JSON envelope', () => {
    const state = {
      query: 'MATCH (s:Service) WHERE s.name = $name RETURN s.name',
      paramsText: '{"name": "codegraph"}'
    }
    expect(decodeConsoleState(encodeConsoleState(state))).toEqual(state)
  })

  it('decodes a legacy params-free link (backward compatibility)', () => {
    const q = 'MATCH (f:Function) RETURN count(f)'
    expect(decodeConsoleState(encodeQueryParam(q))).toEqual({ query: q })
  })

  it('treats a query that literally starts with { as a raw query, not an envelope', () => {
    const q = '{ not: "an envelope" } // user typed this'
    expect(decodeConsoleState(encodeQueryParam(q))).toEqual({ query: q })
  })

  it('returns null for malformed or empty input', () => {
    expect(decodeConsoleState(null)).toBeNull()
    expect(decodeConsoleState('')).toBeNull()
    expect(decodeConsoleState('!!!not-base64url!!!')).toBeNull()
  })

  it('drops a blank paramsText on decode (stays params-free)', () => {
    const encoded = encodeConsoleState({ query: 'RETURN 1', paramsText: '{"a":1}' })
    const decoded = decodeConsoleState(encoded)!
    expect(decoded.paramsText).toBe('{"a":1}')
    const legacyish = decodeConsoleState(encodeConsoleState({ query: 'RETURN 1' }))!
    expect(legacyish.paramsText).toBeUndefined()
  })
})
