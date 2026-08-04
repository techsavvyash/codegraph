import { describe, it, expect } from 'vitest'
import { validateParams } from './params'

describe('validateParams', () => {
  it('treats empty / whitespace-only text as valid with no params', () => {
    for (const text of ['', '   ', '\n\t']) {
      expect(validateParams(text)).toEqual({ valid: true, params: null, error: null, count: 0 })
    }
  })

  it('accepts a JSON object and reports its key count', () => {
    const v = validateParams('{"name": "codegraph", "limit": 5}')
    expect(v.valid).toBe(true)
    expect(v.params).toEqual({ name: 'codegraph', limit: 5 })
    expect(v.error).toBeNull()
    expect(v.count).toBe(2)
  })

  it('accepts an empty object (valid, zero keys, still sent)', () => {
    const v = validateParams('{}')
    expect(v.valid).toBe(true)
    expect(v.params).toEqual({})
    expect(v.count).toBe(0)
  })

  it('accepts nested values (objects/arrays as parameter values)', () => {
    const v = validateParams('{"ids": ["a", "b"], "opts": {"deep": true}}')
    expect(v.valid).toBe(true)
    expect(v.params).toEqual({ ids: ['a', 'b'], opts: { deep: true } })
    expect(v.count).toBe(2)
  })

  it('rejects malformed JSON with an inline message and no params', () => {
    const v = validateParams('{"name": }')
    expect(v.valid).toBe(false)
    expect(v.params).toBeNull()
    expect(v.error).toMatch(/^invalid JSON: /)
    expect(v.count).toBe(0)
  })

  it('rejects non-object payloads (array, scalar, null literal)', () => {
    for (const text of ['[1,2,3]', '"codegraph"', '42', 'true', 'null']) {
      const v = validateParams(text)
      expect(v.valid).toBe(false)
      expect(v.params).toBeNull()
      expect(v.error).toContain('params must be a JSON object')
    }
  })
})
