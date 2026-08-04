/**
 * localStorage-backed query history for the Cypher console (RFC-012 R8).
 *
 * Pure helpers, no Svelte, no direct localStorage access inside the logic
 * (the storage handle is injected) so every branch is unit-testable. The store
 * keeps at most MAX_HISTORY entries, most-recent-first, and dedupes a query
 * against the immediately-preceding entry only (re-running the same query in a
 * row shouldn't fill the list; re-running an older query legitimately bumps it
 * back to the top).
 */
import type { HistoryEntry } from '$lib/types/console'

export const HISTORY_STORAGE_KEY = 'studio:console:history'
export const MAX_HISTORY = 50

/** Minimal storage surface — real localStorage or a test double both satisfy it. */
export interface HistoryStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/**
 * Parses a raw localStorage string into a history list, tolerating any
 * malformed/legacy payload by returning []. Never throws.
 */
export function deserializeHistory(raw: string | null): HistoryEntry[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    const out: HistoryEntry[] = []
    for (const item of parsed) {
      if (typeof item !== 'object' || item === null) continue
      const obj = item as Record<string, unknown>
      if (typeof obj.query !== 'string' || obj.query.length === 0) continue
      const at = typeof obj.at === 'number' && Number.isFinite(obj.at) ? obj.at : 0
      const entry: HistoryEntry = { query: obj.query, at }
      // paramsText is optional and back-compat: older entries lack it.
      if (typeof obj.paramsText === 'string' && obj.paramsText.trim().length > 0) {
        entry.paramsText = obj.paramsText
      }
      out.push(entry)
    }
    return out.slice(0, MAX_HISTORY)
  } catch {
    return []
  }
}

export function serializeHistory(entries: HistoryEntry[]): string {
  return JSON.stringify(entries)
}

/**
 * Returns a new history list with `query` promoted to the front. Trims to a
 * trimmed non-empty string; a blank query is a no-op (returns the input list
 * unchanged). Any earlier occurrence of the same query text is removed so the
 * list stays deduped as things bubble up, and the entry carries the params
 * text (if any) from this most-recent run. A blank/whitespace paramsText is
 * dropped so back-compat entries stay params-free.
 */
export function pushHistory(
  entries: readonly HistoryEntry[],
  query: string,
  at: number,
  paramsText?: string
): HistoryEntry[] {
  const trimmed = query.trim()
  if (trimmed.length === 0) return entries.slice()
  const rest = entries.filter((e) => e.query !== trimmed)
  const entry: HistoryEntry = { query: trimmed, at }
  if (paramsText && paramsText.trim().length > 0) entry.paramsText = paramsText
  return [entry, ...rest].slice(0, MAX_HISTORY)
}

export function loadHistory(storage: HistoryStorage): HistoryEntry[] {
  return deserializeHistory(storage.getItem(HISTORY_STORAGE_KEY))
}

export function saveHistory(storage: HistoryStorage, entries: HistoryEntry[]): void {
  storage.setItem(HISTORY_STORAGE_KEY, serializeHistory(entries))
}
