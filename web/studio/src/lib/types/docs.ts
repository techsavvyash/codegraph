/**
 * Docs plane contracts (RFC-012 R5 / RFC-011). The server data layer in
 * $lib/server/docs/api.ts derives these from codegraph_cypher JSON rows; the
 * /api/docs routes proxy them verbatim inside an ApiEnvelope.
 *
 * Provenance is first-class here: every doc→code link carries its raw
 * `strategy` string and `confidence`, plus a derived `family`/`band` so the UI
 * can present nothing inferred as ground truth (RFC-011 §MENTIONS provenance).
 */

/** docmine = code-token mined (higher trust); semlink = embedding-similarity. */
export type LinkFamily = 'docmine' | 'semlink'

/** Trust band, mirroring the dashboard's docmine≥0.7 vs semlink derivation. */
export type LinkBand = 'high' | 'medium' | 'low'

/** One document as listed in the left rail. */
export interface DocSummary {
  /** elementId — the address the /graph deep link and source APIs understand */
  nodeId: string
  nodeKey: string
  title: string
  filePath: string | null
  service: string | null
  type: string | null
  chunkCount: number
}

/** Documents grouped by owning service, services sorted, docs sorted by title. */
export interface DocGroup {
  service: string
  documents: DocSummary[]
}

export interface DocListResponse {
  documents: DocSummary[]
}

/** A doc→code link off a single chunk. */
export interface MentionLink {
  /** elementId of the mentioned code node — deep-links to /graph?nodes= */
  nodeId: string
  name: string | null
  label: string | null
  filePath: string | null
  /** raw provenance, always shown — e.g. "docmine/codespan" | "semlink/<model>" */
  strategy: string
  confidence: number
  family: LinkFamily
  band: LinkBand
}

/** One chunk of a document, with its outgoing MENTIONS links. */
export interface DocChunk {
  nodeId: string
  nodeKey: string
  /** heading path breadcrumb, e.g. "Title > Section > Subsection" */
  headingPath: string | null
  chunkIndex: number
  content: string
  mentions: MentionLink[]
}

export interface DocDetail {
  document: DocSummary
  chunks: DocChunk[]
}

/** A single search hit over docs (title/content fulltext or CONTAINS fallback). */
export interface DocSearchHit {
  nodeId: string
  nodeKey: string
  title: string
  filePath: string | null
  service: string | null
  /** which document field matched — 'title' or 'content' */
  matchedIn: 'title' | 'content'
  /** relevance score when a fulltext index served the query; null on CONTAINS fallback */
  score: number | null
}

export interface DocSearchResponse {
  hits: DocSearchHit[]
  /** true when a case-insensitive CONTAINS fallback served the query (no fulltext) */
  fallback: boolean
}

/** Reverse lookup: the chunks (and their docs) that MENTION a given code node. */
export interface ReverseMention {
  documentNodeId: string
  documentTitle: string
  documentService: string | null
  chunkNodeId: string
  headingPath: string | null
  chunkIndex: number
  strategy: string
  confidence: number
  family: LinkFamily
  band: LinkBand
}

export interface ReverseMentionsResponse {
  mentions: ReverseMention[]
}
