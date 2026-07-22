/**
 * Formatting helpers for the dashboard UI: thousands separators, fixed-decimal
 * confidence scores, coverage-bar percentage splits, and relative timestamps.
 */

const intFormatter = new Intl.NumberFormat('en-US')

/** Thousands-separated integer, e.g. 51940 -> "51,940". */
export function fmtInt(n: number): string {
  return intFormatter.format(Math.trunc(n))
}

/** Confidence score fixed to 2 decimals, e.g. 0.9 -> "0.90". */
export function fmtConfidence(n: number): string {
  return n.toFixed(2)
}

export interface PctSplit {
  a: number
  b: number
}

/**
 * Percentage widths for a two-segment coverage bar (a, b) that sum to 100.
 * Guards divide-by-zero: when a + b is 0, returns { a: 0, b: 0 }.
 * The remainder of any rounding error is assigned to `a` so the two widths
 * always sum to exactly 100 when the total is non-zero.
 */
export function pctSplit(a: number, b: number): PctSplit {
  const total = a + b
  if (total <= 0) return { a: 0, b: 0 }
  const bPct = Math.round((b / total) * 100)
  const aPct = 100 - bPct
  return { a: aPct, b: bPct }
}

const MINUTE = 60 * 1000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/**
 * Relative time string against `now` (defaults to Date.now()).
 * 'just now' (<1m) / 'Nm ago' (<1h) / 'Nh ago' (<24h) / 'Nd ago' (<30d) /
 * ISO date fallback (YYYY-MM-DD) beyond that. Returns null for null input
 * or an unparseable ISO string.
 */
export function relTime(iso: string | null, now: number = Date.now()): string | null {
  if (iso === null) return null
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return null

  const delta = now - then
  if (delta < 0) return isoDate(then)
  if (delta < MINUTE) return 'just now'
  if (delta < HOUR) return `${Math.floor(delta / MINUTE)}m ago`
  if (delta < DAY) return `${Math.floor(delta / HOUR)}h ago`
  const days = Math.floor(delta / DAY)
  if (days < 30) return `${days}d ago`
  return isoDate(then)
}

function isoDate(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10)
}
