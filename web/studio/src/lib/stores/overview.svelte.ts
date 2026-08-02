/**
 * Overview visualizer store — the whole-service graph mode on /graph. Owns the
 * loaded file/edge data, the expansion state, lazily-fetched file drilldowns,
 * and the selected node; the OverviewCanvas/OverviewPanel are presentational.
 * Svelte 5 runes class — instantiate once per /graph page (like ExplorerStore).
 *
 * Runes hazards observed elsewhere in this codebase and honored here:
 *  - never mutate $state inside a getter (state_unsafe_mutation crashes
 *    hydration) — the derived tree/graph are $derived, computed not mutated;
 *  - read-modify-writes reachable from a component $effect are wrapped in
 *    untrack (the loading counter) so they don't self-trigger the effect;
 *  - the tree + visible graph are derived from data + visibility, so a single
 *    reducer call re-renders the canvas without imperative sync.
 */
import { untrack } from 'svelte'
import { SvelteSet, SvelteMap } from 'svelte/reactivity'
import type {
  ApiEnvelope,
  ApiError,
  GraphNode
} from '$lib/types/graph'
import type {
  FileEdge,
  FileSymbol,
  FileSymbolsResponse,
  OverviewFile,
  OverviewResponse,
  RenderNode,
  VisibleGraph
} from '$lib/types/overview'
import {
  buildTree,
  visibleGraph,
  encodeOverviewState,
  type OverviewTree,
  type OverviewVisibility
} from '$lib/components/overview/model'
import { timedFetch } from '$lib/api/timedFetch'

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export class OverviewApiError extends Error {
  constructor(
    message: string,
    readonly kind: string,
    readonly status: number
  ) {
    super(message)
    this.name = 'OverviewApiError'
  }
}

async function unwrap<T>(res: Response): Promise<ApiEnvelope<T>> {
  const body = (await res.json()) as ApiEnvelope<T> | ApiError
  if (!res.ok || 'error' in body) {
    const err = body as ApiError
    throw new OverviewApiError(err.error ?? `HTTP ${res.status}`, err.kind ?? 'internal', res.status)
  }
  return body as ApiEnvelope<T>
}

export type LoadStatus = 'idle' | 'loading' | 'loaded' | 'error'

export class OverviewStore {
  /** the service currently loaded (null before first load / when scope is null) */
  service = $state<string | null>(null)
  status = $state<LoadStatus>('idle')

  private files = $state<OverviewFile[]>([])
  private edges = $state<FileEdge[]>([])

  // expansion state — SvelteSet so mutation is reactive without new-object churn
  expandedDirs = new SvelteSet<string>()
  expandedFiles = new SvelteSet<string>()

  /** lazily-fetched symbols per expanded file id */
  drilldowns = new SvelteMap<string, FileSymbol[]>()
  /** files whose drilldown fetch is in flight (drives the panel spinner) */
  drillLoading = new SvelteSet<string>()
  /** per-file drilldown error, surfaced in the panel */
  drillErrors = new SvelteMap<string, string>()

  selected = $state<string | null>(null)

  warnings = $state<string[]>([])
  error = $state<string | null>(null)
  /** in-flight request count — drives the top-level busy indicator */
  pending = $state(0)

  private fetchFn: FetchLike

  constructor(fetchFn: FetchLike = (...args) => timedFetch(...args)) {
    this.fetchFn = fetchFn
  }

  // ── derived views ─────────────────────────────────────────
  // $derived.by memoizes: the tree/graph are rebuilt only when their tracked
  // inputs (files / expansion sets / drilldowns) actually change, so reading
  // `graph` several times in one render computes it once. A plain `get` accessor
  // would recompute buildTree/visibleGraph on every access.

  /** The directory tree, rebuilt when the file set changes. */
  readonly tree = $derived.by<OverviewTree>(() => buildTree(this.files))

  /** The render node/edge set for the current expansion — drives the canvas. */
  readonly graph = $derived.by<VisibleGraph>(() =>
    visibleGraph(
      this.tree,
      this.edges,
      { expandedDirs: new Set(this.expandedDirs), expandedFiles: new Set(this.expandedFiles) },
      new Map(this.drilldowns)
    )
  )

  /** Visible-graph tallies for the stats chip. */
  readonly stats = $derived.by<{ dirs: number; files: number; symbols: number }>(() => {
    let dirs = 0
    let files = 0
    let symbols = 0
    for (const n of this.graph.nodes) {
      if (n.kind === 'dir') dirs += 1
      else if (n.kind === 'file') files += 1
      else symbols += 1
    }
    return { dirs, files, symbols }
  })

  /** The currently-selected render node, or null. */
  readonly selectedNode = $derived.by<RenderNode | null>(() => {
    if (!this.selected) return null
    return this.graph.nodes.find((n) => n.id === this.selected) ?? null
  })

  /** True when the service is loaded and has at least one file. */
  get isEmpty(): boolean {
    return this.status === 'loaded' && this.files.length === 0
  }

  // ── loading ───────────────────────────────────────────────

  /**
   * Loads a service's overview, resetting expansion/selection. Idempotent for
   * the already-loaded service unless force is set (scope re-selects reload).
   */
  async load(service: string, scopeId = 'main', opts: { force?: boolean } = {}): Promise<void> {
    if (!opts.force && service === this.service && this.status === 'loaded') return
    this.service = service
    this.status = 'loading'
    this.error = null
    this.warnings = []
    this.resetExpansion()
    untrack(() => (this.pending += 1))
    try {
      const env = await unwrap<OverviewResponse>(
        await this.fetchFn(
          `/api/overview?service=${encodeURIComponent(service)}&scope=${encodeURIComponent(scopeId)}`
        )
      )
      // A late response for a service the user has since switched away from must
      // not clobber the current one.
      if (this.service !== service) return
      this.files = env.data.files
      this.edges = env.data.edges
      if (env.warnings.length) this.warnings = [...new Set(env.warnings)].slice(-5)
      this.status = 'loaded'
    } catch (e) {
      if (this.service !== service) return
      this.status = 'error'
      this.error = e instanceof Error ? e.message : String(e)
      this.files = []
      this.edges = []
    } finally {
      untrack(() => (this.pending -= 1))
    }
  }

  private resetExpansion(): void {
    this.expandedDirs.clear()
    this.expandedFiles.clear()
    this.drilldowns.clear()
    this.drillLoading.clear()
    this.drillErrors.clear()
    this.selected = null
  }

  // ── expansion actions ─────────────────────────────────────

  toggleDir(path: string): void {
    if (this.expandedDirs.has(path)) this.expandedDirs.delete(path)
    else this.expandedDirs.add(path)
  }

  /**
   * Expands a file, fetching its drilldown symbols once and caching them. A
   * failed fetch records a per-file error and leaves the file collapsed so the
   * panel can offer a retry.
   */
  async expandFile(fileId: string): Promise<void> {
    if (this.expandedFiles.has(fileId)) return
    if (!this.drilldowns.has(fileId)) {
      const ok = await this.fetchDrilldown(fileId)
      if (!ok) return
    }
    this.expandedFiles.add(fileId)
  }

  /** (Re)fetches one file's drilldown; returns true on success. */
  async fetchDrilldown(fileId: string): Promise<boolean> {
    this.drillLoading.add(fileId)
    this.drillErrors.delete(fileId)
    untrack(() => (this.pending += 1))
    try {
      const env = await unwrap<FileSymbolsResponse>(
        await this.fetchFn(`/api/overview/file?node_id=${encodeURIComponent(fileId)}`)
      )
      this.drilldowns.set(fileId, env.data.symbols)
      if (env.warnings.length) this.warnings = [...new Set([...this.warnings, ...env.warnings])].slice(-5)
      return true
    } catch (e) {
      this.drillErrors.set(fileId, e instanceof Error ? e.message : String(e))
      return false
    } finally {
      this.drillLoading.delete(fileId)
      untrack(() => (this.pending -= 1))
    }
  }

  collapseFile(fileId: string): void {
    this.expandedFiles.delete(fileId)
  }

  select(id: string | null): void {
    this.selected = id
  }

  dismissError(): void {
    this.error = null
  }

  // ── workbench handoff ─────────────────────────────────────

  /**
   * Builds the GraphNode for the selected node so the page can seed the
   * workbench store with it (Open in workbench). A selected file is resolved
   * from the tree directly (it need not be a currently-visible node — selection
   * survives collapse); a selected symbol comes from the visible render node.
   * Dirs and unknown ids yield null (there's nothing single to open).
   */
  workbenchSeed(): GraphNode | null {
    if (!this.selected) return null
    // File by id: resolve straight from the tree so it works even when the
    // file's dir ancestor is collapsed and the file isn't in the render set.
    const leaf = this.tree.fileById.get(this.selected)
    if (leaf) {
      return {
        node_id: leaf.nodeId,
        label: 'File',
        name: leaf.label,
        file_path: leaf.path,
        service: this.service ?? undefined
      }
    }
    const node = this.selectedNode
    if (node?.kind === 'symbol') {
      return {
        node_id: node.id,
        label: node.symbolLabel ?? 'Function',
        name: node.label,
        start_line: node.startLine,
        service: this.service ?? undefined
      }
    }
    return null
  }

  // ── deep-link ─────────────────────────────────────────────

  /** The `open=` value encoding the current expansion (dirs + expanded files). */
  encodeOpen(): string {
    return encodeOverviewState({
      expandedDirs: new Set(this.expandedDirs),
      expandedFiles: new Set(this.expandedFiles)
    })
  }

  /**
   * Restores expansion from a decoded state after a load: sets the dir set and,
   * for each expanded file, fetches its drilldown then marks it expanded. A
   * file whose drilldown fails is dropped from the restore (no phantom expand).
   */
  async restore(state: OverviewVisibility): Promise<void> {
    for (const d of state.expandedDirs) this.expandedDirs.add(d)
    await Promise.all(
      [...state.expandedFiles].map(async (fileId) => {
        // only restore files that actually exist in this service's tree
        if (!this.tree.fileById.has(fileId)) return
        const ok = this.drilldowns.has(fileId) || (await this.fetchDrilldown(fileId))
        if (ok) this.expandedFiles.add(fileId)
      })
    )
  }
}
