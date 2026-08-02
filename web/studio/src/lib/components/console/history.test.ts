import { describe, it, expect } from 'vitest'
import {
  deserializeHistory,
  serializeHistory,
  pushHistory,
  loadHistory,
  saveHistory,
  MAX_HISTORY,
  HISTORY_STORAGE_KEY,
  type HistoryStorage
} from './history'
import type { HistoryEntry } from '$lib/types/console'

function mem(initial?: string): HistoryStorage & { store: Map<string, string> } {
  const store = new Map<string, string>()
  if (initial !== undefined) store.set(HISTORY_STORAGE_KEY, initial)
  return {
    store,
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => void store.set(k, v)
  }
}

describe('deserializeHistory', () => {
  it('returns [] for null / empty', () => {
    expect(deserializeHistory(null)).toEqual([])
    expect(deserializeHistory('')).toEqual([])
  })

  it('returns [] for non-array / corrupt JSON', () => {
    expect(deserializeHistory('{"query":"x"}')).toEqual([])
    expect(deserializeHistory('not json')).toEqual([])
  })

  it('drops entries missing a non-empty query and defaults missing timestamps', () => {
    const raw = JSON.stringify([
      { query: 'MATCH (n) RETURN n', at: 123 },
      { query: '' },
      { at: 5 },
      { query: 'MATCH (m) RETURN m' }
    ])
    expect(deserializeHistory(raw)).toEqual([
      { query: 'MATCH (n) RETURN n', at: 123 },
      { query: 'MATCH (m) RETURN m', at: 0 }
    ])
  })

  it('caps at MAX_HISTORY', () => {
    const big = Array.from({ length: MAX_HISTORY + 10 }, (_, i) => ({ query: `q${i}`, at: i }))
    expect(deserializeHistory(JSON.stringify(big))).toHaveLength(MAX_HISTORY)
  })
})

describe('pushHistory', () => {
  const base: HistoryEntry[] = [
    { query: 'a', at: 1 },
    { query: 'b', at: 2 }
  ]

  it('promotes a new query to the front', () => {
    expect(pushHistory(base, 'c', 3)).toEqual([
      { query: 'c', at: 3 },
      { query: 'a', at: 1 },
      { query: 'b', at: 2 }
    ])
  })

  it('trims whitespace and ignores blank queries', () => {
    expect(pushHistory(base, '   ', 9)).toEqual(base)
    expect(pushHistory(base, '  c  ', 3)[0]).toEqual({ query: 'c', at: 3 })
  })

  it('dedupes: re-running an existing query bumps it to the front with a fresh time', () => {
    expect(pushHistory(base, 'b', 5)).toEqual([
      { query: 'b', at: 5 },
      { query: 'a', at: 1 }
    ])
  })

  it('collapses a consecutive duplicate of the head (no runaway growth)', () => {
    const after = pushHistory(base, 'a', 7)
    expect(after).toHaveLength(2)
    expect(after[0]).toEqual({ query: 'a', at: 7 })
  })

  it('caps at MAX_HISTORY', () => {
    let list: HistoryEntry[] = []
    for (let i = 0; i < MAX_HISTORY + 5; i++) list = pushHistory(list, `q${i}`, i)
    expect(list).toHaveLength(MAX_HISTORY)
    expect(list[0].query).toBe(`q${MAX_HISTORY + 4}`)
  })

  it('does not mutate the input list', () => {
    const copy = base.slice()
    pushHistory(base, 'z', 1)
    expect(base).toEqual(copy)
  })
})

describe('load/save round-trip', () => {
  it('persists and reloads through storage', () => {
    const s = mem()
    const entries = pushHistory([], 'MATCH (n) RETURN n', 100)
    saveHistory(s, entries)
    expect(s.store.get(HISTORY_STORAGE_KEY)).toBe(serializeHistory(entries))
    expect(loadHistory(s)).toEqual(entries)
  })

  it('loads [] from empty storage', () => {
    expect(loadHistory(mem())).toEqual([])
  })
})

describe('history params persistence', () => {
  it('carries paramsText on an entry and round-trips through storage', () => {
    const s = mem()
    const entries = pushHistory([], 'MATCH (s:Service) WHERE s.name = $name RETURN s', 100, '{"name": "codegraph"}')
    expect(entries[0].paramsText).toBe('{"name": "codegraph"}')
    saveHistory(s, entries)
    expect(loadHistory(s)).toEqual(entries)
  })

  it('drops blank paramsText so back-compat entries stay params-free', () => {
    const entries = pushHistory([], 'RETURN 1', 100, '   ')
    expect(entries[0].paramsText).toBeUndefined()
  })

  it('deserializes legacy entries without paramsText', () => {
    const legacy = JSON.stringify([{ query: 'RETURN 1', at: 5 }])
    expect(deserializeHistory(legacy)).toEqual([{ query: 'RETURN 1', at: 5 }])
  })

  it('re-running a query with different params keeps the newest params', () => {
    let entries = pushHistory([], 'Q', 100, '{"a": 1}')
    entries = pushHistory(entries, 'Q', 200, '{"a": 2}')
    expect(entries).toHaveLength(1)
    expect(entries[0].paramsText).toBe('{"a": 2}')
    entries = pushHistory(entries, 'Q', 300)
    expect(entries[0].paramsText).toBeUndefined()
  })
})
