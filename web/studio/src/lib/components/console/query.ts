/**
 * Query text helpers for the console (RFC-012 R8/R9): scope-filter snippet
 * insertion at the cursor, shareable-URL encode/decode, and the scope-aware
 * example templates. Pure — unit-tested without a DOM.
 *
 * IMPORTANT (R9): raw Cypher is NOT auto-scoped. The scope selector only
 * *offers* to insert a `WHERE n.serviceName = '<svc>'` snippet; the user's
 * query text is otherwise sent to the tool untouched. The page surfaces this
 * in a hint line.
 */

/** The graph property that carries the owning service on code nodes. */
export const SERVICE_PROPERTY = 'serviceName'

export interface SnippetInsertion {
  /** The full text after inserting `snippet` at [start,end). */
  text: string
  /** The new caret position (end of the inserted snippet). */
  cursor: number
}

/**
 * Inserts `snippet` into `text`, replacing the selection [selStart,selEnd).
 * Returns the new text and where the caret should land. Out-of-range or
 * inverted selections are clamped so this never throws.
 */
export function insertAtCursor(
  text: string,
  selStart: number,
  selEnd: number,
  snippet: string
): SnippetInsertion {
  const len = text.length
  let start = Number.isFinite(selStart) ? Math.max(0, Math.min(selStart, len)) : len
  let end = Number.isFinite(selEnd) ? Math.max(0, Math.min(selEnd, len)) : len
  if (start > end) [start, end] = [end, start]
  const next = text.slice(0, start) + snippet + text.slice(end)
  return { text: next, cursor: start + snippet.length }
}

/**
 * The scope-filter snippet to insert for a given variable + service. When no
 * service is active ("All services") we still produce a well-formed snippet
 * against a placeholder so the user sees the shape and fills it in.
 */
export function scopeFilterSnippet(variable: string, service: string | null): string {
  const svc = service ?? '<service>'
  const v = variable.trim() || 'n'
  return `${v}.${SERVICE_PROPERTY} = '${svc}'`
}

// ── shareable URL encode/decode ────────────────────────────────────────────
// The query lives in the URL as a base64url-encoded param so a console query
// is copy-pasteable. base64url (not raw JSON in the query string) keeps
// newlines/quotes from mangling the link and avoids double-encoding surprises.

function toBase64Url(bytes: Uint8Array): string {
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  const b64 = typeof btoa === 'function' ? btoa(bin) : Buffer.from(bin, 'binary').toString('base64')
  return b64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function fromBase64Url(s: string): Uint8Array {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/')
  const bin =
    typeof atob === 'function' ? atob(b64) : Buffer.from(b64, 'base64').toString('binary')
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes
}

export function encodeQueryParam(query: string): string {
  return toBase64Url(new TextEncoder().encode(query))
}

/** Decodes a base64url query param back to its raw text. Null for malformed. */
export function decodeQueryParam(param: string | null): string | null {
  if (!param) return null
  try {
    const text = new TextDecoder().decode(fromBase64Url(param))
    return text.length > 0 ? text : null
  } catch {
    return null
  }
}

/**
 * The console's shareable state: the query plus the raw params JSON text.
 * paramsText is omitted when empty.
 */
export interface ConsoleState {
  query: string
  paramsText?: string
}

/**
 * Encodes the full console state (query + params) into a single base64url
 * ?q= value. When there are no params it emits the legacy shape — the raw
 * query text, base64url-encoded — so links stay identical to before this
 * change and short. With params it emits a JSON envelope {q, p}.
 */
export function encodeConsoleState(state: ConsoleState): string {
  const paramsText = state.paramsText?.trim()
  if (!paramsText) return encodeQueryParam(state.query)
  return toBase64Url(
    new TextEncoder().encode(JSON.stringify({ q: state.query, p: paramsText }))
  )
}

/**
 * Decodes a ?q= value back into console state. Backward compatible: a legacy
 * link (base64url of the raw query text, no envelope) decodes to a params-free
 * state. A JSON envelope {q, p} restores both. Returns null for malformed or
 * empty input.
 */
export function decodeConsoleState(param: string | null): ConsoleState | null {
  const decoded = decodeQueryParam(param)
  if (decoded === null) return null
  // A JSON envelope must be a leading '{' AND parse to {q: string}; anything
  // else is treated as a legacy raw-query string (which may itself start with
  // '{' only if the user literally typed that as a query — still valid).
  if (decoded.startsWith('{')) {
    try {
      const parsed = JSON.parse(decoded) as unknown
      if (
        typeof parsed === 'object' &&
        parsed !== null &&
        typeof (parsed as Record<string, unknown>).q === 'string'
      ) {
        const obj = parsed as { q: string; p?: unknown }
        const state: ConsoleState = { query: obj.q }
        if (typeof obj.p === 'string' && obj.p.trim().length > 0) state.paramsText = obj.p
        return state
      }
    } catch {
      // fall through: treat as legacy raw query text
    }
  }
  return { query: decoded }
}

// ── example templates ──────────────────────────────────────────────────────

export interface ExampleQuery {
  label: string
  query: string
}

/**
 * Prefilled example queries for the console. When a concrete service is
 * active they are scoped to it; with "All services" they stay unscoped (and
 * the label says so, matching R9's honesty stance).
 */
export function exampleQueries(service: string | null): ExampleQuery[] {
  const scoped = service !== null
  const svc = service ?? ''
  const where = (v: string) => (scoped ? `\n  AND ${v}.${SERVICE_PROPERTY} = '${svc}'` : '')
  const whereFirst = (v: string) =>
    scoped ? `\nWHERE ${v}.${SERVICE_PROPERTY} = '${svc}'` : ''
  const suffix = scoped ? ` (${svc})` : ''

  return [
    {
      label: `Dead functions${suffix}`,
      query:
        `MATCH (f:Function)\nWHERE f.reachability = 'dead'` +
        where('f') +
        `\nRETURN f.name AS name, f.filePath AS file\nORDER BY file, name\nLIMIT 100`
    },
    {
      label: `Top CALLS hubs${suffix}`,
      query:
        `MATCH (f:Function)-[c:CALLS]->()` +
        whereFirst('f') +
        `\nRETURN f.name AS name, count(c) AS calls\nORDER BY calls DESC\nLIMIT 25`
    },
    {
      label: `Label counts${suffix}`,
      query: scoped
        ? `MATCH (n)\nWHERE n.${SERVICE_PROPERTY} = '${svc}'\nRETURN labels(n)[0] AS label, count(*) AS n\nORDER BY n DESC`
        : `MATCH (n)\nRETURN labels(n)[0] AS label, count(*) AS n\nORDER BY n DESC`
    },
    {
      label: `Services`,
      query: `MATCH (s:Service)\nRETURN s.name AS name, s.language AS language\nORDER BY name`
    }
  ]
}
