/**
 * Pure query parsing + result grouping for the Omnibox (RFC-012 R1).
 * No Svelte, no DOM — Omnibox.svelte wires these into the UI so the
 * parsing/grouping/decision logic is unit-testable in isolation.
 */
import type { ApiError, FoundNode } from '$lib/types/graph'

/** Canonical node labels the palette knows how to group/filter by. */
export type NodeLabel =
  | 'Function'
  | 'Method'
  | 'Class'
  | 'Interface'
  | 'File'
  | 'Symbol'
  | 'Variable'
  | 'Document'
  | 'DocumentChunk'
  | 'Service'

const CANONICAL_LABELS: NodeLabel[] = [
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
]

const LABEL_BY_LOWER: Record<string, NodeLabel> = Object.fromEntries(
  CANONICAL_LABELS.map((l) => [l.toLowerCase(), l])
)

export interface ParsedQuery {
  /** free-text remainder after filter tokens are stripped, trimmed and collapsed. */
  text: string
  /** canonical label, only present if the raw query had a recognized `label:` token. */
  label?: NodeLabel
  /** service name, only present if the raw query had a `svc:` token. */
  service?: string
}

/**
 * Parses free text plus inline filters `label:<Label>` and `svc:<name>` (keys
 * case-insensitive). An unrecognized label value is left in place as plain
 * text (the token is not stripped) since we can't disambiguate it from a
 * search term. `svc:` values are taken verbatim (case preserved) since
 * service names are not normalized elsewhere in the app.
 */
export function parseQuery(raw: string): ParsedQuery {
  const tokens = raw.split(/\s+/).filter((t) => t.length > 0)
  const textTokens: string[] = []
  let label: NodeLabel | undefined
  let service: string | undefined

  for (const token of tokens) {
    const colonIdx = token.indexOf(':')
    if (colonIdx <= 0 || colonIdx === token.length - 1) {
      textTokens.push(token)
      continue
    }
    const key = token.slice(0, colonIdx).toLowerCase()
    const value = token.slice(colonIdx + 1)

    if (key === 'label') {
      const canonical = LABEL_BY_LOWER[value.toLowerCase()]
      if (canonical) {
        label = canonical
        continue
      }
      // Unknown label value: not a recognized filter, keep as free text.
      textTokens.push(token)
      continue
    }

    if (key === 'svc') {
      service = value
      continue
    }

    textTokens.push(token)
  }

  return { text: textTokens.join(' ').trim(), label, service }
}

/** Group order for result rows; labels outside this list are appended alphabetically. */
const GROUP_ORDER: NodeLabel[] = [
  'Function',
  'Method',
  'Class',
  'Interface',
  'File',
  'Symbol',
  'Variable',
  'Document',
  'DocumentChunk'
]

export interface ResultGroup {
  label: string
  nodes: FoundNode[]
}

/**
 * Groups results by label, in GROUP_ORDER, with any labels not in that list
 * appended afterward in alphabetical order. Within a group, input order
 * (the API's relevance ranking) is preserved.
 */
export function groupResults(nodes: FoundNode[]): ResultGroup[] {
  const byLabel = new Map<string, FoundNode[]>()
  for (const node of nodes) {
    const existing = byLabel.get(node.label)
    if (existing) existing.push(node)
    else byLabel.set(node.label, [node])
  }

  const orderedLabels = GROUP_ORDER.filter((l) => byLabel.has(l))
  const knownSet = new Set<string>(GROUP_ORDER)
  const extraLabels = [...byLabel.keys()].filter((l) => !knownSet.has(l)).sort((a, b) => a.localeCompare(b))

  return [...orderedLabels, ...extraLabels].map((label) => ({ label, nodes: byLabel.get(label)! }))
}

/**
 * Decides whether a failed semantic-search request should permanently
 * disable the semantic toggle for the session, per RFC: degraded features
 * must surface *why*. Only tool-error responses whose message mentions the
 * embedding provider qualify — network/validation/internal errors do not
 * (they may be transient and unrelated to embedding availability).
 * Returns the server's verbatim message to show in the tooltip, or null.
 */
export function semanticDisableReason(err: ApiError): string | null {
  if (err.kind !== 'tool-error') return null
  if (/embedding provider/i.test(err.error)) return err.error
  return null
}
