import { describe, it, expect } from 'vitest'
import { fmtInt, fmtConfidence, pctSplit, relTime } from './format'

describe('fmtInt', () => {
  it('adds thousands separators', () => {
    expect(fmtInt(51940)).toBe('51,940')
  })

  it('formats numbers under 1000 with no separator', () => {
    expect(fmtInt(412)).toBe('412')
  })

  it('formats zero', () => {
    expect(fmtInt(0)).toBe('0')
  })

  it('truncates fractional input', () => {
    expect(fmtInt(1234.9)).toBe('1,234')
  })

  it('formats large multi-comma numbers', () => {
    expect(fmtInt(20925000)).toBe('20,925,000')
  })
})

describe('fmtConfidence', () => {
  it('pads to 2 decimals', () => {
    expect(fmtConfidence(0.9)).toBe('0.90')
  })

  it('rounds to 2 decimals', () => {
    expect(fmtConfidence(0.526)).toBe('0.53')
  })

  it('formats exact 2-decimal input unchanged', () => {
    expect(fmtConfidence(0.92)).toBe('0.92')
  })

  it('formats zero confidence', () => {
    expect(fmtConfidence(0)).toBe('0.00')
  })

  it('formats 1.0 confidence', () => {
    expect(fmtConfidence(1)).toBe('1.00')
  })
})

describe('pctSplit', () => {
  it('splits an even 50/50', () => {
    expect(pctSplit(50, 50)).toEqual({ a: 50, b: 50 })
  })

  it('matches the design mock 42/58 split', () => {
    // 461 docmine, 638 semlink -> a=docmine% b=semlink%
    expect(pctSplit(461, 638)).toEqual({ a: 42, b: 58 })
  })

  it('guards divide-by-zero when both are 0', () => {
    expect(pctSplit(0, 0)).toEqual({ a: 0, b: 0 })
  })

  it('handles all-a', () => {
    expect(pctSplit(10, 0)).toEqual({ a: 100, b: 0 })
  })

  it('handles all-b', () => {
    expect(pctSplit(0, 10)).toEqual({ a: 0, b: 100 })
  })

  it('always sums to 100 when total is non-zero', () => {
    const { a, b } = pctSplit(1, 2)
    expect(a + b).toBe(100)
  })
})

describe('relTime', () => {
  const now = Date.parse('2026-07-23T12:00:00Z')

  it('returns null for null input', () => {
    expect(relTime(null, now)).toBeNull()
  })

  it('returns null for an unparseable ISO string', () => {
    expect(relTime('not-a-date', now)).toBeNull()
  })

  it('returns "just now" for under a minute', () => {
    expect(relTime('2026-07-23T11:59:30Z', now)).toBe('just now')
  })

  it('returns minutes ago for under an hour', () => {
    expect(relTime('2026-07-23T11:45:00Z', now)).toBe('15m ago')
  })

  it('returns hours ago for under a day', () => {
    expect(relTime('2026-07-23T09:00:00Z', now)).toBe('3h ago')
  })

  it('returns days ago for under 30 days', () => {
    expect(relTime('2026-07-20T12:00:00Z', now)).toBe('3d ago')
  })

  it('falls back to ISO date for 30+ days', () => {
    expect(relTime('2026-05-01T12:00:00Z', now)).toBe('2026-05-01')
  })

  it('falls back to ISO date for future timestamps', () => {
    expect(relTime('2026-08-01T12:00:00Z', now)).toBe('2026-08-01')
  })
})
