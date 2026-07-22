import { describe, it, expect } from 'vitest'
import { parseToolPayload, ToolPayloadError } from './payload'

describe('parseToolPayload', () => {
  it('parses a bare JSON object with no warnings', () => {
    const p = parseToolPayload<{ a: number }>('{"a": 1}')
    expect(p.warnings).toEqual([])
    expect(p.data).toEqual({ a: 1 })
  })

  it('parses a JSON array body', () => {
    const p = parseToolPayload<number[]>('[1,2,3]')
    expect(p.data).toEqual([1, 2, 3])
  })

  it('peels leading warning lines exactly as the cypher tool emits them', () => {
    const text =
      'warning: query plan contains AllNodesScan — add a label qualifier to avoid scanning the whole graph\n' +
      '\n' +
      '{\n  "row_count": 7,\n  "rows": []\n}'
    const p = parseToolPayload<{ row_count: number }>(text)
    expect(p.warnings).toEqual([
      'query plan contains AllNodesScan — add a label qualifier to avoid scanning the whole graph'
    ])
    expect(p.data.row_count).toBe(7)
  })

  it('collects multiple warning lines', () => {
    const text = 'warning: one\nwarning: two\n\n{"ok":true}'
    const p = parseToolPayload<{ ok: boolean }>(text)
    expect(p.warnings).toEqual(['one', 'two'])
    expect(p.data.ok).toBe(true)
  })

  it('throws ToolPayloadError on non-JSON payload, preserving the raw text', () => {
    let caught: unknown
    try {
      parseToolPayload('flow spine for main() ...text format...')
    } catch (e) {
      caught = e
    }
    expect(caught).toBeInstanceOf(ToolPayloadError)
    expect((caught as ToolPayloadError).raw).toContain('flow spine')
  })

  it('throws ToolPayloadError on malformed JSON', () => {
    expect(() => parseToolPayload('{"a": ')).toThrow(ToolPayloadError)
  })
})
