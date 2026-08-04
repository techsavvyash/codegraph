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
  DeadReportResponse,
  EdgeMode,
  FileEdge,
  FileSymbol,
  FileSymbolsResponse,
  LensId,
  OverviewDecorations,
  OverviewFile,
  OverviewResponse,
  RenderNode,
  SymbolCaller,
  SymbolCallersResponse,
  UsageDirection,
  VisibleGraph
} from '$lib/types/overview'
import type { EntryPoint, EntryPointsResponse, FlowResponse, FlowStep } from '$lib/types/flows'
import {
  buildTree,
  visibleGraph,
  makeVisibleOf,
  encodeOverviewState,
  type OverviewTree,
  type OverviewVisibility
} from '$lib/components/overview/model'
import {
  strongEdges,
  projectFlow,
  usageDepths,
  heatBuckets,
  aggregateDead,
  deadBucket
} from '$lib/components/overview/lenses'
import { timedFetch } from '$lib/api/timedFetch'

/** Structure lens: cap on total rendered edges in strong mode. */
// Strong-mode edge budget: low enough to actually prune at the package level
// (~30 aggregate edges on codegraph's internal/), high enough that per-node
// heaviest edges (phase 1 of strongEdges) are never dropped.
const STRONG_EDGE_CAP = 24
/** Usage lens BFS depth cap (depths 0..3 have distinct styling; deeper still lit at d3 tone via clamp). */
const USAGE_MAX_DEPTH = 3
/** Flows lens trace depth. */
const FLOW_MAX_DEPTH = 6

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

  // ── lens state ────────────────────────────────────────────
  /** active lens; default Structure (today's decluttered view) */
  lens = $state<LensId>('structure')
  /** Structure lens: 'strong' (heaviest edges only) or 'all' */
  edgeMode = $state<EdgeMode>('strong')

  // Flows lens
  flowEntries = $state<EntryPoint[]>([])
  flowEntriesStatus = $state<'idle' | 'loading' | 'loaded' | 'error'>('idle')
  flowEntriesError = $state<string | null>(null)
  activeFlowId = $state<string | null>(null)
  activeFlowName = $state<string | null>(null)
  activeFlowSteps = $state<FlowStep[]>([])
  flowTracing = $state(false)
  flowError = $state<string | null>(null)

  // Usage lens
  usageDirection = $state<UsageDirection>('up')
  /** 1-hop caller cache keyed by symbol id (lazy, per selected drilled symbol) */
  symbolCallers = new SvelteMap<string, SymbolCaller[]>()
  symbolCallersLoading = new SvelteSet<string>()

  // Dead lens
  deadReport = $state<DeadReportResponse | null>(null)
  deadStatus = $state<'idle' | 'loading' | 'loaded' | 'error'>('idle')
  deadError = $state<string | null>(null)

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
      // resetExpansion invalidated the per-service lens caches; if a fetching
      // lens is still active, refill it for the new service (otherwise the
      // flow rail / dead legend would sit empty at 'idle' until a lens switch).
      if (this.lens === 'flows') void this.loadFlowEntries()
      if (this.lens === 'dead') void this.loadDeadReport()
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
    // per-service lens caches — invalidated when the service changes
    this.flowEntries = []
    this.flowEntriesStatus = 'idle'
    this.flowEntriesError = null
    this.clearFlow()
    this.symbolCallers.clear()
    this.symbolCallersLoading.clear()
    this.deadReport = null
    this.deadStatus = 'idle'
    this.deadError = null
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

  // ── lens actions ──────────────────────────────────────────

  /**
   * Switches the active lens. Clears lens-specific TRANSIENT state (an active
   * flow trace) but preserves expansion, selection, and per-service caches
   * (loaded entries / dead report / caller cache stay warm). Activating a lens
   * kicks its one-time per-service fetch.
   */
  setLens(id: LensId): void {
    if (id === this.lens) return
    this.lens = id
    // a traced flow is meaningless outside the flows lens — drop it
    if (id !== 'flows') this.clearFlow()
    if (id === 'flows') void this.loadFlowEntries()
    if (id === 'dead') void this.loadDeadReport()
  }

  setEdgeMode(mode: EdgeMode): void {
    this.edgeMode = mode
  }

  toggleEdgeMode(): void {
    this.edgeMode = this.edgeMode === 'strong' ? 'all' : 'strong'
  }

  // ── flows lens ────────────────────────────────────────────

  /**
   * Loads the service's entry points per tier (limit 50/tier) — the same
   * anti-starvation pattern the /flows page uses. Idempotent once loaded for the
   * current service; a service change resets the cache (resetExpansion).
   */
  async loadFlowEntries(): Promise<void> {
    const service = this.service
    if (!service) return
    if (this.flowEntriesStatus === 'loaded' || this.flowEntriesStatus === 'loading') return
    this.flowEntriesStatus = 'loading'
    this.flowEntriesError = null
    untrack(() => (this.pending += 1))
    try {
      const perTier = await Promise.all(
        [1, 2, 3, 4].map(async (tier) =>
          unwrap<EntryPointsResponse>(
            await this.fetchFn(
              `/api/entrypoints?service=${encodeURIComponent(service)}&tier=${tier}&limit=50`
            )
          )
        )
      )
      // stale guard: the user may have switched services during the awaits
      if (this.service !== service) return
      this.flowEntries = perTier.flatMap((d) => d.data.entries)
      this.flowEntriesStatus = 'loaded'
      const warns = perTier.flatMap((d) => [...d.warnings, ...(d.data.tier_errors ?? [])])
      if (warns.length) this.warnings = [...new Set([...this.warnings, ...warns])].slice(-5)
    } catch (e) {
      if (this.service !== service) return
      this.flowEntriesStatus = 'error'
      this.flowEntriesError = e instanceof Error ? e.message : String(e)
    } finally {
      untrack(() => (this.pending -= 1))
    }
  }

  /**
   * Traces one entry point's flow (persist:false server-side) and records its
   * steps for projection onto the visible graph. The stale guard compares BOTH
   * the service AND the requested entry id after the await, so a fast re-click
   * on a different entry can't be clobbered by a slow earlier trace.
   */
  async traceFlow(entry: EntryPoint): Promise<void> {
    const service = this.service
    this.activeFlowId = entry.node_id
    this.activeFlowName = entry.name
    this.flowTracing = true
    this.flowError = null
    untrack(() => (this.pending += 1))
    try {
      const env = await unwrap<FlowResponse>(
        await this.fetchFn('/api/flow', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ node_id: entry.node_id, max_depth: FLOW_MAX_DEPTH })
        })
      )
      // discard if the service changed or a newer trace superseded this one
      if (this.service !== service || this.activeFlowId !== entry.node_id) return
      this.activeFlowSteps = env.data.flows[0]?.steps ?? []
      if (env.warnings.length) this.warnings = [...new Set([...this.warnings, ...env.warnings])].slice(-5)
    } catch (e) {
      if (this.service !== service || this.activeFlowId !== entry.node_id) return
      this.flowError = e instanceof Error ? e.message : String(e)
      this.activeFlowSteps = []
    } finally {
      this.flowTracing = false
      untrack(() => (this.pending -= 1))
    }
  }

  clearFlow(): void {
    this.activeFlowId = null
    this.activeFlowName = null
    this.activeFlowSteps = []
    this.flowTracing = false
    this.flowError = null
  }

  // ── usage lens ────────────────────────────────────────────

  setUsageDirection(dir: UsageDirection): void {
    this.usageDirection = dir
  }

  toggleUsageDirection(): void {
    this.usageDirection = this.usageDirection === 'up' ? 'down' : 'up'
  }

  /**
   * Fetches a drilled symbol's 1-hop callers once, caching by symbol id. Used by
   * the Usage lens to seed the BFS from a symbol's real callers (the file-pair
   * BFS can't see below file granularity). Silent on error — the panel shows the
   * empty list and the lens falls back to file-level projection.
   */
  async loadSymbolCallers(symbolId: string): Promise<void> {
    if (this.symbolCallers.has(symbolId) || this.symbolCallersLoading.has(symbolId)) return
    this.symbolCallersLoading.add(symbolId)
    untrack(() => (this.pending += 1))
    try {
      const env = await unwrap<SymbolCallersResponse>(
        await this.fetchFn(`/api/overview/callers?node_id=${encodeURIComponent(symbolId)}`)
      )
      this.symbolCallers.set(symbolId, env.data.callers)
      if (env.warnings.length) this.warnings = [...new Set([...this.warnings, ...env.warnings])].slice(-5)
    } catch {
      this.symbolCallers.set(symbolId, [])
    } finally {
      this.symbolCallersLoading.delete(symbolId)
      untrack(() => (this.pending -= 1))
    }
  }

  // ── dead lens ─────────────────────────────────────────────

  /** Fetches the service's dead-code report once per service (cached). */
  async loadDeadReport(): Promise<void> {
    const service = this.service
    if (!service) return
    if (this.deadStatus === 'loaded' || this.deadStatus === 'loading') return
    this.deadStatus = 'loading'
    this.deadError = null
    untrack(() => (this.pending += 1))
    try {
      const env = await unwrap<DeadReportResponse>(
        await this.fetchFn(`/api/overview/dead?service=${encodeURIComponent(service)}`)
      )
      if (this.service !== service) return
      this.deadReport = env.data
      this.deadStatus = 'loaded'
      if (env.warnings.length) this.warnings = [...new Set([...this.warnings, ...env.warnings])].slice(-5)
    } catch (e) {
      if (this.service !== service) return
      this.deadStatus = 'error'
      this.deadError = e instanceof Error ? e.message : String(e)
    } finally {
      untrack(() => (this.pending -= 1))
    }
  }

  // ── decorations (the per-lens paint instructions for the canvas) ───────
  // A single $derived.by keyed on (lens, graph, selection, flow steps, usage
  // state, dead report). All non-trivial math lives in lenses.ts — this only
  // shapes the results into class maps. Never mutates $state.

  readonly decorations = $derived.by<OverviewDecorations>(() => {
    const empty: OverviewDecorations = {
      dimUnmatched: false,
      nodeClasses: new Map(),
      edgeClasses: new Map(),
      extraEdges: [],
      visibleEdgeIds: null
    }
    const graph = this.graph
    switch (this.lens) {
      case 'structure':
        return this.structureDecorations(graph)
      case 'flows':
        return this.flowDecorations(graph)
      case 'usage':
        return this.usageDecorations(graph)
      case 'hotspots':
        return this.hotspotDecorations(graph)
      case 'dead':
        return this.deadDecorations(graph)
      default:
        return empty
    }
  })

  /** Structure: strong-mode edge filter (or all); no node/edge classes, no dim. */
  private structureDecorations(graph: VisibleGraph): OverviewDecorations {
    let visibleEdgeIds: Set<string> | null = null
    if (this.edgeMode === 'strong') {
      const kept = strongEdges(graph.edges, STRONG_EDGE_CAP)
      visibleEdgeIds = new Set(kept.map((e) => e.id))
    }
    return {
      dimUnmatched: false,
      nodeClasses: new Map(),
      edgeClasses: new Map(),
      extraEdges: [],
      visibleEdgeIds
    }
  }

  /** Flows: light the projected path, accent its edges, dim everything else. */
  private flowDecorations(graph: VisibleGraph): OverviewDecorations {
    const nodeClasses = new Map<string, string>()
    const edgeClasses = new Map<string, string>()
    const extraEdges: OverviewDecorations['extraEdges'] = []
    if (this.activeFlowSteps.length === 0) {
      // no active trace: show everything, no dim (the rail is the only UI)
      return { dimUnmatched: false, nodeClasses, edgeClasses, extraEdges, visibleEdgeIds: null }
    }
    const visibleOf = makeVisibleOf(this.tree, this.visibilitySnapshot())
    const proj = projectFlow(this.activeFlowSteps, visibleOf, graph.edges)
    for (const id of proj.nodeIds) nodeClasses.set(id, 'hl-path')
    for (const id of proj.onEdgeIds) edgeClasses.set(id, 'hl-path')
    // Drilled precision: when a file on the path is EXPANDED, light the exact
    // symbols the flow touches (steps carry name + filePath; drilldown symbols
    // carry name + nodeId) — expanding a lit file shows which functions the
    // flow runs through, not just the box.
    for (const s of this.activeFlowSteps) {
      if (!s.filePath) continue
      const leaf = this.tree.fileByPath.get(s.filePath)
      if (!leaf || !this.expandedFiles.has(leaf.nodeId)) continue
      for (const sym of this.drilldowns.get(leaf.nodeId) ?? []) {
        if (sym.name === s.name) nodeClasses.set(sym.nodeId, 'hl-path')
      }
    }
    for (const seg of proj.extraSegments) {
      extraEdges.push({ id: `flowseg:${seg.source}->${seg.target}`, source: seg.source, target: seg.target, kind: 'flowseg' })
    }
    return { dimUnmatched: true, nodeClasses, edgeClasses, extraEdges, visibleEdgeIds: null }
  }

  /**
   * Usage: BFS callers/callees from the selection over the loaded file pairs.
   * When the selection is a drilled symbol, seed from its real 1-hop callers'
   * files (fetched lazily); otherwise seed from the selected node's own file
   * path(s). Depth-fades intensity via usage-d0..usage-d3.
   */
  private usageDecorations(graph: VisibleGraph): OverviewDecorations {
    const nodeClasses = new Map<string, string>()
    const seeds = this.usageSeeds()
    if (seeds.size === 0) {
      return { dimUnmatched: false, nodeClasses, edgeClasses: new Map(), extraEdges: [], visibleEdgeIds: null }
    }
    const pairs = this.filePairs()
    const depths = usageDepths(pairs, seeds, this.usageDirection, USAGE_MAX_DEPTH)
    // Map file-path depths onto visible node ids (paths roll up to visible nodes;
    // keep the SHALLOWEST depth when several paths share a visible node).
    const visibleOf = makeVisibleOf(this.tree, this.visibilitySnapshot())
    const byNode = new Map<string, number>()
    for (const [path, depth] of depths) {
      const vid = visibleOf(path)
      if (vid === null) continue
      const prev = byNode.get(vid)
      if (prev === undefined || depth < prev) byNode.set(vid, depth)
    }
    for (const [id, depth] of byNode) {
      nodeClasses.set(id, `usage-d${Math.min(3, depth)}`)
    }
    return { dimUnmatched: true, nodeClasses, edgeClasses: new Map(), extraEdges: [], visibleEdgeIds: null }
  }

  /** Hotspots: heat ramp by incoming aggregate weight; no dim. */
  private hotspotDecorations(graph: VisibleGraph): OverviewDecorations {
    const nodeClasses = new Map<string, string>()
    const buckets = heatBuckets(graph.edges)
    for (const [id, bucket] of buckets) nodeClasses.set(id, `heat-${bucket}`)
    return { dimUnmatched: false, nodeClasses, edgeClasses: new Map(), extraEdges: [], visibleEdgeIds: null }
  }

  /** Dead code: red ramp on nodes containing dead functions; dim the rest. */
  private deadDecorations(graph: VisibleGraph): OverviewDecorations {
    const nodeClasses = new Map<string, string>()
    const report = this.deadReport
    if (!report || report.entries.length === 0) {
      return { dimUnmatched: false, nodeClasses, edgeClasses: new Map(), extraEdges: [], visibleEdgeIds: null }
    }
    const visibleOf = makeVisibleOf(this.tree, this.visibilitySnapshot())
    const agg = aggregateDead(report.entries, visibleOf)
    for (const [id, a] of agg) {
      const bucket = deadBucket(a.dead)
      if (bucket > 0) nodeClasses.set(id, `dead-${bucket}`)
    }
    return { dimUnmatched: true, nodeClasses, edgeClasses: new Map(), extraEdges: [], visibleEdgeIds: null }
  }

  /** Dead names for a selected file/dir node (panel list). */
  deadNamesFor(nodeId: string): string[] {
    const report = this.deadReport
    if (!report) return []
    const visibleOf = makeVisibleOf(this.tree, this.visibilitySnapshot())
    return aggregateDead(report.entries, visibleOf).get(nodeId)?.deadNames ?? []
  }

  // ── lens helpers (pure reads over reactive state) ─────────

  private visibilitySnapshot(): OverviewVisibility {
    return { expandedDirs: new Set(this.expandedDirs), expandedFiles: new Set(this.expandedFiles) }
  }

  /** Raw file→file call pairs (fnWeight + moduleWeight) for the Usage BFS. */
  private filePairs(): Array<{ fromPath: string; toPath: string; weight: number }> {
    return this.edges.map((e) => ({
      fromPath: e.fromPath,
      toPath: e.toPath,
      weight: e.fnWeight + e.moduleWeight
    }))
  }

  /**
   * The seed file paths for the Usage BFS from the current selection:
   *  - a selected symbol → its cached 1-hop callers' file paths (fetched
   *    lazily; empty until loaded), falling back to the containing file's path;
   *  - a selected file → its own path;
   *  - a selected dir → every file path under it (so the whole package is seeded).
   */
  private usageSeeds(): Set<string> {
    const seeds = new Set<string>()
    const sel = this.selected
    if (!sel) return seeds
    const node = this.graph.nodes.find((n) => n.id === sel)
    if (node?.kind === 'symbol') {
      const callers = this.symbolCallers.get(sel)
      if (callers && callers.length > 0) {
        for (const c of callers) if (c.filePath) seeds.add(c.filePath)
        return seeds
      }
      // not yet loaded (or no callers) → seed from the symbol's own file
      const fileId = node.parentId
      const leaf = fileId ? this.tree.fileById.get(fileId) : undefined
      if (leaf) seeds.add(leaf.path)
      return seeds
    }
    if (node?.kind === 'file') {
      if (node.path) seeds.add(node.path)
      return seeds
    }
    // dir (or a file selected but currently collapsed under a dir): seed by the
    // set of file paths whose visible node is the selection.
    const state = this.visibilitySnapshot()
    const visibleOf = makeVisibleOf(this.tree, state)
    for (const [path] of this.tree.fileByPath) {
      if (visibleOf(path) === sel) seeds.add(path)
    }
    return seeds
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

  /** The `lens=` value for the URL — omitted (empty) for the default Structure. */
  lensParam(): string {
    return this.lens === 'structure' ? '' : this.lens
  }

  /**
   * Restores the lens (and, for flows, re-traces a deep-linked entry) after a
   * load. Loads the lens's per-service data, and for flows waits for the entries
   * so the deep-linked entry can be resolved; a missing entry is dropped (no
   * phantom trace). Safe to call with a null/unknown lens id.
   */
  async restoreLens(lensId: string | null | undefined, flowEntryId: string | null | undefined): Promise<void> {
    const valid: LensId[] = ['structure', 'flows', 'usage', 'hotspots', 'dead']
    const id = valid.includes(lensId as LensId) ? (lensId as LensId) : 'structure'
    this.lens = id
    if (id === 'dead') {
      await this.loadDeadReport()
      return
    }
    if (id === 'flows') {
      await this.loadFlowEntries()
      if (flowEntryId) {
        const entry = this.flowEntries.find((e) => e.node_id === flowEntryId)
        if (entry) await this.traceFlow(entry)
      }
    }
  }
}
