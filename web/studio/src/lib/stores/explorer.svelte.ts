/**
 * Explorer working-set store (RFC-012 R2): owns the canvas state and every
 * API interaction. The canvas/omnibox/inspector are presentational; this is
 * the brain. Svelte 5 runes class — instantiate once per /graph page.
 */
import { untrack } from 'svelte'
import { SvelteMap } from 'svelte/reactivity'
import type {
  ApiEnvelope,
  ApiError,
  ExpandResponse,
  FoundNode,
  GraphEdge,
  GraphNode,
  PathResponse,
  SourceResponse
} from '$lib/types/graph'
import { edgeKey } from '$lib/types/workingset'

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export class ExplorerApiError extends Error {
  constructor(
    message: string,
    readonly kind: string,
    readonly status: number
  ) {
    super(message)
    this.name = 'ExplorerApiError'
  }
}

async function unwrap<T>(res: Response): Promise<ApiEnvelope<T>> {
  const body = (await res.json()) as ApiEnvelope<T> | ApiError
  if (!res.ok || 'error' in body) {
    const err = body as ApiError
    throw new ExplorerApiError(err.error ?? `HTTP ${res.status}`, err.kind ?? 'internal', res.status)
  }
  return body as ApiEnvelope<T>
}

export function foundToGraphNode(f: FoundNode): GraphNode {
  return {
    node_id: f.node_id,
    label: f.label,
    name: f.name,
    file_path: f.file_path || undefined,
    start_line: f.start_line,
    end_line: f.end_line,
    signature: f.signature || undefined,
    service: f.service || undefined
  }
}

export class ExplorerStore {
  nodes = new SvelteMap<string, GraphNode>()
  edges = new SvelteMap<string, GraphEdge>()
  selected = $state<string | null>(null)
  pinned = $state<string[]>([])
  /** most recent non-fatal notices (guardrail warnings, truncations) */
  warnings = $state<string[]>([])
  /** in-flight request count — drives busy indicators */
  pending = $state(0)
  /** last fatal error surfaced to the user (dismissable) */
  error = $state<string | null>(null)

  private fetchFn: FetchLike

  constructor(fetchFn: FetchLike = (...args) => fetch(...args)) {
    this.fetchFn = fetchFn
  }

  get nodeList(): GraphNode[] {
    return [...this.nodes.values()]
  }
  get edgeList(): GraphEdge[] {
    return [...this.edges.values()]
  }

  // ── working-set mutations ────────────────────────────────

  addNode(n: GraphNode): void {
    const existing = this.nodes.get(n.node_id)
    if (!existing) {
      this.nodes.set(n.node_id, n)
      return
    }
    // keep the richer record; preserve object identity when nothing changes
    // (child $effects track the node object — churning identity re-triggers
    // source fetches and resets inspector UI state)
    const merged = { ...n, ...pruneUndefined(existing) }
    if (!shallowEqual(merged, existing)) this.nodes.set(n.node_id, merged)
  }

  addFound(f: FoundNode): void {
    this.addNode(foundToGraphNode(f))
  }

  private addEdge(e: GraphEdge): void {
    // only keep edges whose endpoints are both loaded — the canvas is a
    // working set, not a window onto the whole graph
    if (this.nodes.has(e.from) && this.nodes.has(e.to)) {
      this.edges.set(edgeKey(e), e)
    }
  }

  mergeExpand(resp: ExpandResponse): void {
    for (const n of resp.nodes) this.addNode(n)
    for (const e of resp.edges) this.addEdge(e)
  }

  mergePath(resp: PathResponse): void {
    for (const p of resp.paths) {
      for (const n of p.nodes) this.addNode({ node_id: n.node_id, label: n.label, name: n.name })
      for (const e of p.edges) this.addEdge(e)
    }
  }

  removeNode(id: string): void {
    this.nodes.delete(id)
    for (const [k, e] of this.edges) {
      if (e.from === id || e.to === id) this.edges.delete(k)
    }
    if (this.selected === id) this.selected = null
    this.pinned = this.pinned.filter((p) => p !== id)
  }

  clear(): void {
    this.nodes.clear()
    this.edges.clear()
    this.selected = null
    this.pinned = []
    this.warnings = []
    this.error = null
  }

  select(id: string | null): void {
    this.selected = id
  }

  /** Max two pins, FIFO eviction; pinning an existing pin unpins it. */
  togglePin(id: string): void {
    if (this.pinned.includes(id)) {
      this.pinned = this.pinned.filter((p) => p !== id)
    } else {
      this.pinned = [...this.pinned, id].slice(-2)
    }
  }

  dismissError(): void {
    this.error = null
  }

  // ── API actions ──────────────────────────────────────────

  private async run<T>(fn: () => Promise<ApiEnvelope<T>>): Promise<T | null> {
    // untrack: `pending += 1` is a read-modify-write; when a store action is
    // invoked synchronously from a component $effect (e.g. the inspector's
    // source loader), the read would register `pending` as a dependency of
    // that effect and the write would re-trigger it — an infinite loop.
    untrack(() => (this.pending += 1))
    try {
      const env = await fn()
      if (env.warnings.length) {
        // keep the last few distinct warnings
        this.warnings = [...new Set([...this.warnings, ...env.warnings])].slice(-5)
      }
      return env.data
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e)
      return null
    } finally {
      untrack(() => (this.pending -= 1))
    }
  }

  async expand(
    nodeId: string,
    relTypes: string[],
    direction: 'in' | 'out' | 'both' = 'out',
    depth = 1,
    opts: { maxNodes?: number; quiet?: boolean } = {}
  ): Promise<boolean> {
    const body: Record<string, unknown> = { node_id: nodeId, rel_types: relTypes, direction, depth }
    if (opts.maxNodes !== undefined) body.max_nodes = opts.maxNodes
    const data = await this.run<ExpandResponse>(async () =>
      unwrap(
        await this.fetchFn('/api/expand', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify(body)
        })
      )
    )
    if (!data) return false
    this.mergeExpand(data)
    if (data.truncated && !opts.quiet) {
      this.warnings = [...new Set([...this.warnings, 'expansion truncated — raise limits or narrow rel types'])].slice(-5)
    }
    return true
  }

  /**
   * Deep-link restore: loads bare nodes by id. Uses expand with max_nodes 1
   * (the tool has no fetch-by-id primitive; the start node always comes back
   * first) — quiet so the inevitable truncation flag doesn't spam warnings.
   */
  async hydrate(ids: string[]): Promise<void> {
    const results = await Promise.all(
      ids.map((id) => this.expand(id, ['CALLS'], 'out', 1, { maxNodes: 1, quiet: true }))
    )
    const failed = results.filter((ok) => !ok).length
    if (failed > 0) {
      // stale link ≠ fatal: clear the per-call error, surface an honest notice
      this.error = null
      this.warnings = [
        ...new Set([...this.warnings, `${failed} node(s) from this link no longer exist in the graph`])
      ].slice(-5)
    }
  }

  async path(
    fromId: string,
    toId: string,
    relTypes: string[],
    opts: { shortest?: boolean; maxHops?: number; direction?: 'in' | 'out' | 'both' } = {}
  ): Promise<number | null> {
    const data = await this.run<PathResponse>(async () =>
      unwrap(
        await this.fetchFn('/api/path', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({
            from_id: fromId,
            to_id: toId,
            rel_types: relTypes,
            shortest: opts.shortest ?? true,
            max_hops: opts.maxHops ?? 6,
            direction: opts.direction ?? 'out'
          })
        })
      )
    )
    if (!data) return null
    this.mergePath(data)
    return data.path_count
  }

  async source(nodeId: string): Promise<SourceResponse | null> {
    return this.run<SourceResponse>(async () =>
      unwrap(await this.fetchFn(`/api/source?node_id=${encodeURIComponent(nodeId)}`))
    )
  }

  // ── URL (deep-link) round-trip ───────────────────────────

  /** Serializes the working set into URLSearchParams (node ids + selection + pins). */
  toParams(): URLSearchParams {
    const p = new URLSearchParams()
    if (this.nodes.size) p.set('nodes', [...this.nodes.keys()].join(','))
    if (this.selected) p.set('sel', this.selected)
    if (this.pinned.length) p.set('pin', this.pinned.join(','))
    return p
  }
}

function shallowEqual<T extends object>(a: T, b: T): boolean {
  const ra = a as Record<string, unknown>
  const rb = b as Record<string, unknown>
  const ka = Object.keys(ra)
  if (ka.length !== Object.keys(rb).length) return false
  return ka.every((k) => ra[k] === rb[k])
}

function pruneUndefined<T extends object>(obj: T): Partial<T> {
  const out: Partial<T> = {}
  for (const [k, v] of Object.entries(obj)) {
    if (v !== undefined) (out as Record<string, unknown>)[k] = v
  }
  return out
}
