/**
 * Pure URL <-> selection state for the Docs plane (RFC-012 R5 deep-linking).
 * The page keeps its selection in the query string so refresh restores it and
 * the view is shareable. Kept side-effect-free and framework-agnostic so it's
 * unit-testable without a browser or SvelteKit.
 *
 * Query params:
 *   doc   — selected document elementId
 *   chunk — selected chunk elementId (only meaningful with doc)
 *   q     — active search query
 * Service scope lives in the global scope store, not the URL, so switching
 * service via the header selector doesn't fight with a stale ?service= here.
 */

export interface DocsSelection {
  doc: string | null
  chunk: string | null
  query: string
}

export const EMPTY_SELECTION: DocsSelection = { doc: null, chunk: null, query: '' }

/** Reads a DocsSelection out of URLSearchParams, tolerating anything absent. */
export function parseDocsSelection(params: URLSearchParams): DocsSelection {
  const doc = params.get('doc')
  const chunk = params.get('chunk')
  const query = params.get('q') ?? ''
  return {
    doc: doc && doc.length > 0 ? doc : null,
    // a chunk without a doc is meaningless — drop it
    chunk: doc && chunk && chunk.length > 0 ? chunk : null,
    query
  }
}

/**
 * Serializes a selection to a `/docs?...` path (or bare `/docs` when empty).
 * Only non-empty params are written, so the URL stays clean; ordering is
 * stable (doc, chunk, q) so equality checks against the current URL don't
 * churn.
 */
export function serializeDocsSelection(sel: DocsSelection): string {
  const p = new URLSearchParams()
  if (sel.doc) p.set('doc', sel.doc)
  if (sel.doc && sel.chunk) p.set('chunk', sel.chunk)
  if (sel.query) p.set('q', sel.query)
  const qs = p.toString()
  return qs ? `/docs?${qs}` : '/docs'
}
