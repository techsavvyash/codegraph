<script lang="ts">
  /**
   * Graph screen with two modes:
   *  - overview (DEFAULT): the whole-service visualizer — pick a service, see
   *    its directory/file/symbol graph, drill down/up (OverviewStore + canvas).
   *  - workbench: the RFC-012 omnibox → canvas → inspector working-set explorer
   *    (ExplorerStore), unchanged. Reached via ?view=workbench or a ?nodes= deep
   *    link (flows' "Load onto canvas" handoff, which must keep working).
   *
   * Both stores live for the page's lifetime, so switching modes never destroys
   * the other's in-memory state. Overview follows the global scope store; the
   * workbench keeps its own working set. This page owns keyboard shortcuts, the
   * two deep-link round-trips, and the scope-follow effect.
   *
   * Svelte-5 effect-loop hazards (see the original workbench comments + the
   * flows page): callback props are hoisted consts; read-modify-writes inside
   * store actions are untracked in the store; the scope-follow effect reads
   * scope.service tracked and everything else untracked.
   */
  import { onMount, untrack } from 'svelte'
  import { replaceState } from '$app/navigation'
  import { page } from '$app/state'
  import GraphCanvas from '$lib/components/canvas/GraphCanvas.svelte'
  import Omnibox from '$lib/components/omnibox/Omnibox.svelte'
  import Inspector from '$lib/components/inspector/Inspector.svelte'
  import OverviewCanvas from '$lib/components/overview/OverviewCanvas.svelte'
  import OverviewPanel from '$lib/components/overview/OverviewPanel.svelte'
  import ServicePicker from '$lib/components/overview/ServicePicker.svelte'
  import LensBar from '$lib/components/overview/LensBar.svelte'
  import FlowRail from '$lib/components/overview/FlowRail.svelte'
  import { ExplorerStore } from '$lib/stores/explorer.svelte'
  import { OverviewStore } from '$lib/stores/overview.svelte'
  import { scope } from '$lib/stores/scope.svelte'
  import { decodeOverviewState } from '$lib/components/overview/model'
  import type { FoundNode, GraphNode } from '$lib/types/graph'
  import type { LensId, RenderNode } from '$lib/types/overview'
  import type { EntryPoint } from '$lib/types/flows'

  type Mode = 'overview' | 'workbench'

  const store = new ExplorerStore()
  const overview = new OverviewStore()

  let mode = $state<Mode>('overview')
  let omniboxOpen = $state(false)
  let pathBusy = $state(false)
  let pathNotice = $state<string | null>(null)

  // gate the URL-sync effects until mount has resolved the initial mode/state,
  // so the debounced writer doesn't stomp the incoming deep link
  let bootstrapped = false
  // one-shot: the ?open= expansion + lens/flow to restore once the first
  // overview load lands
  let pendingOpen: string | null = null
  let pendingLens: string | null = null
  let pendingFlow: string | null = null

  // ── mount: resolve mode + restore whichever mode's deep link ─────────
  onMount(() => {
    const p = page.url.searchParams
    const ids = (p.get('nodes') ?? '').split(',').filter(Boolean)
    const view = p.get('view')

    // Mode resolution: explicit workbench view OR a ?nodes= deep link → workbench;
    // otherwise overview (the default whole-service experience).
    if (view === 'workbench' || ids.length > 0) {
      mode = 'workbench'
      const sel = p.get('sel')
      const pins = (p.get('pin') ?? '').split(',').filter(Boolean)
      const stitch = (p.get('stitch') ?? '').split(',').filter(Boolean)
      if (ids.length) {
        void store.hydrate(ids, stitch).then(() => {
          if (sel && store.nodes.has(sel)) store.select(sel)
          for (const pin of pins) if (store.nodes.has(pin)) store.togglePin(pin)
        })
      } else {
        omniboxOpen = true
      }
    } else {
      mode = 'overview'
      pendingOpen = p.get('open')
      pendingLens = p.get('lens')
      pendingFlow = p.get('flow')
      // load the current scope's service now if one is selected; otherwise the
      // picker shows and the scope-follow effect loads on pick
      if (scope.service) void loadOverview(scope.service)
    }
    bootstrapped = true
  })

  async function loadOverview(service: string) {
    await overview.load(service, scope.scopeId)
    if (overview.status !== 'loaded') return
    if (pendingOpen) {
      const state = decodeOverviewState(pendingOpen)
      pendingOpen = null
      await overview.restore(state)
    }
    // Restore lens + re-trace a deep-linked flow (after entries load). Consumed
    // once; a subsequent scope-follow reload starts fresh at Structure.
    if (pendingLens || pendingFlow) {
      const lensId = pendingLens
      const flowId = pendingFlow
      pendingLens = null
      pendingFlow = null
      await overview.restoreLens(lensId, flowId)
    }
  }

  // ── URL sync (debounced replaceState, one writer per mode) ────────────
  let urlTimer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    if (!bootstrapped) return
    // recompute the target for the active mode
    let target: string
    if (mode === 'workbench') {
      const qs = store.toParams().toString()
      target = qs ? `/graph?${qs}` : '/graph?view=workbench'
    } else {
      const open = overview.encodeOpen()
      const lensParam = overview.lensParam()
      const flowId = overview.activeFlowId
      const p = new URLSearchParams()
      p.set('view', 'overview')
      if (open) p.set('open', open)
      if (lensParam) p.set('lens', lensParam)
      if (lensParam === 'flows' && flowId) p.set('flow', flowId)
      target = `/graph?${p.toString()}`
    }
    clearTimeout(urlTimer)
    urlTimer = setTimeout(() => {
      if (page.url.pathname + page.url.search !== target) replaceState(target, {})
    }, 300)
  })

  // ── overview: follow the global scope store ───────────────────────────
  // A concrete scope selection (topbar or the picker) loads/reloads the
  // overview and resets its expansion. Null scope ("All services") leaves the
  // picker up. Read scope.service tracked; do the load untracked (flows pattern).
  $effect(() => {
    const svc = scope.service
    untrack(() => {
      if (!bootstrapped || mode !== 'overview') return
      if (!svc) return
      if (svc === overview.service && overview.status === 'loaded') return
      void loadOverview(svc)
    })
  })

  // ── mode toggle ───────────────────────────────────────────────────────
  function setMode(next: Mode) {
    if (next === mode) return
    mode = next
    // entering overview with a scope but no data yet → load it
    if (next === 'overview' && scope.service && overview.service !== scope.service) {
      void loadOverview(scope.service)
    }
  }
  const showOverview = () => setMode('overview')
  const showWorkbench = () => setMode('workbench')

  // ── omnibox wiring (works in both modes; 'open'/'add' switch to workbench) ──
  function addFound(n: FoundNode) {
    store.addFound(n)
    if (mode !== 'workbench') setMode('workbench')
  }
  function openFound(n: FoundNode) {
    store.addFound(n)
    store.select(n.node_id)
    if (mode !== 'workbench') setMode('workbench')
    omniboxOpen = false
  }

  // ── workbench expand/path/keyboard (unchanged behavior) ───────────────
  function quickExpand(nodeId: string) {
    store.expand(nodeId, ['CALLS'], 'both', 1)
  }

  async function runPath() {
    if (store.pinned.length !== 2 || pathBusy) return
    pathBusy = true
    pathNotice = null
    const count = await store.path(store.pinned[0], store.pinned[1], ['CALLS'], { direction: 'both' })
    pathBusy = false
    if (count === 0) pathNotice = 'no CALLS path between the pinned nodes'
    else if (count !== null) pathNotice = null
  }

  function onKeydown(ev: KeyboardEvent) {
    const inField =
      ev.target instanceof HTMLElement && ['INPUT', 'TEXTAREA'].includes(ev.target.tagName)
    if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === 'k') {
      ev.preventDefault()
      omniboxOpen = !omniboxOpen
      return
    }
    if (inField || omniboxOpen) return
    if (ev.key === '/') {
      ev.preventDefault()
      omniboxOpen = true
      return
    }
    if (mode === 'workbench') {
      switch (ev.key) {
        case 'e':
          if (store.selected) quickExpand(store.selected)
          break
        case 'p':
          if (store.selected) store.togglePin(store.selected)
          break
        case 'Escape':
          store.select(null)
          break
      }
    } else if (ev.key === 'Escape') {
      overview.select(null)
    }
  }

  const pinnedNames = $derived(store.pinned.map((id) => store.nodes.get(id)?.name ?? id.slice(-8)))

  // ── hoisted callback identities (workbench) ───────────────────────────
  const selectNode = (id: string | null) => store.select(id)
  const togglePin = (id: string) => store.togglePin(id)
  const removeNode = (id: string) => store.removeNode(id)
  const loadSource = (id: string) => store.source(id)
  const expandGroup = (nodeId: string, relType: string, direction: 'in' | 'out') =>
    store.expand(nodeId, [relType], direction, 1)
  const focusNode = (id: string) => store.select(id)
  const closeInspector = () => store.select(null)
  const dismissError = () => store.dismissError()

  // ── hoisted callback identities (overview) ────────────────────────────
  const ovSelect = (id: string | null) => overview.select(id)
  const ovToggle = (id: string) => {
    // double-tap on the canvas: dirs toggle expansion, files expand/collapse
    if (id.startsWith('dir:')) {
      overview.toggleDir(id.slice('dir:'.length))
    } else if (overview.expandedFiles.has(id)) {
      overview.collapseFile(id)
    } else {
      void overview.expandFile(id)
    }
  }
  const ovPanelExpand = (node: RenderNode) => {
    if (node.kind === 'dir') overview.toggleDir(node.id.slice('dir:'.length))
    else if (node.kind === 'file') void overview.expandFile(node.id)
  }
  const ovPanelCollapse = (node: RenderNode) => {
    if (node.kind === 'file') overview.collapseFile(node.id)
  }
  const ovRetryDrill = (node: RenderNode) => {
    if (node.kind === 'file') void overview.expandFile(node.id)
  }
  const ovSelectConnection = (id: string) => overview.select(id)
  const ovDismissError = () => overview.dismissError()
  const ovOpenInWorkbench = (_node: RenderNode) => {
    const seed: GraphNode | null = overview.workbenchSeed()
    if (!seed) return
    store.addNode(seed)
    store.select(seed.node_id)
    setMode('workbench')
  }

  // ── hoisted callback identities (lens system) ─────────────────────────
  const ovSetLens = (id: LensId) => overview.setLens(id)
  const ovSelectFlow = (entry: EntryPoint) => void overview.traceFlow(entry)
  const ovToggleEdgeMode = () => overview.toggleEdgeMode()
  const ovToggleUsageDir = () => overview.toggleUsageDirection()
  // A usage caller row selects that file's visible node (resolve path → id).
  const ovSelectCaller = (filePath: string) => {
    const leaf = overview.tree.fileByPath.get(filePath)
    if (leaf) overview.select(leaf.nodeId)
  }

  // The selected file's transient drilldown state, fed to the panel.
  const selectedFileId = $derived(overview.selectedNode?.kind === 'file' ? overview.selectedNode.id : null)
  const panelDrillLoading = $derived(selectedFileId ? overview.drillLoading.has(selectedFileId) : false)
  const panelDrillError = $derived(selectedFileId ? (overview.drillErrors.get(selectedFileId) ?? null) : null)

  // ── lens-derived panel/legend data ────────────────────────────────────
  // Usage lens: fetch the selected symbol's callers lazily (BFS seed source).
  const selectedSymbolId = $derived(
    overview.selectedNode?.kind === 'symbol' ? overview.selectedNode.id : null
  )
  $effect(() => {
    const id = selectedSymbolId
    const lens = overview.lens
    untrack(() => {
      if (lens === 'usage' && id) void overview.loadSymbolCallers(id)
    })
  })
  const panelCallers = $derived(selectedSymbolId ? (overview.symbolCallers.get(selectedSymbolId) ?? null) : null)
  const panelCallersLoading = $derived(
    selectedSymbolId ? overview.symbolCallersLoading.has(selectedSymbolId) : false
  )
  const panelDeadNames = $derived(
    overview.lens === 'dead' && overview.selectedNode && overview.selectedNode.kind !== 'symbol'
      ? overview.deadNamesFor(overview.selectedNode.id)
      : []
  )

  // Flow rail projection facts (recompute from the active decorations).
  const flowOnscreen = $derived(
    overview.lens === 'flows'
      ? [...overview.decorations.nodeClasses.values()].filter((c) => c === 'hl-path').length
      : 0
  )
  const flowMissing = $derived.by(() => {
    if (overview.lens !== 'flows' || overview.activeFlowSteps.length === 0) return 0
    // steps with a filePath that resolves vs. total — cheap recompute for the chip
    let missing = 0
    for (const s of overview.activeFlowSteps) {
      if (!s.filePath || !overview.tree.fileByPath.has(s.filePath)) missing += 1
    }
    return missing
  })

  // Hotspots/Dead legend visibility + dead counts.
  const deadCounts = $derived(overview.deadReport?.counts ?? null)
</script>

<svelte:window onkeydown={onKeydown} />

<svelte:head>
  <title>CodeGraph Studio — Graph</title>
</svelte:head>

<div class="explorer" class:has-panel={mode === 'workbench' ? !!store.selected : !!overview.selected}>
  <div class="stage">
    <!-- mode toggle, floated top-left -->
    <div class="modes" role="tablist" aria-label="Graph mode">
      <button
        class="modebtn"
        class:active={mode === 'overview'}
        role="tab"
        aria-selected={mode === 'overview'}
        data-testid="mode-overview"
        onclick={showOverview}
      >
        Overview
      </button>
      <button
        class="modebtn"
        class:active={mode === 'workbench'}
        role="tab"
        aria-selected={mode === 'workbench'}
        data-testid="mode-workbench"
        onclick={showWorkbench}
      >
        Workbench
      </button>
    </div>

    {#if mode === 'overview' && scope.service}
      <div class="lensbar-mount">
        <LensBar lens={overview.lens} onSelect={ovSetLens} />
      </div>
    {/if}

    {#if mode === 'workbench'}
      <GraphCanvas
        nodes={store.nodeList}
        edges={store.edgeList}
        selected={store.selected}
        pinned={store.pinned}
        onSelect={selectNode}
        onTogglePin={togglePin}
        onExpandRequest={quickExpand}
        onRemoveNode={removeNode}
      />

      {#if store.warnings.length > 0}
        <div class="notices">
          {#each store.warnings as w}
            <div class="notice mono">guardrail: {w}</div>
          {/each}
        </div>
      {/if}

      {#if store.error}
        <div class="errbar">
          <span>{store.error}</span>
          <button onclick={dismissError}>dismiss</button>
        </div>
      {/if}

      {#if store.pinned.length === 2}
        <div class="pathbar">
          <span class="mono">{pinnedNames[0]} &#8596; {pinnedNames[1]}</span>
          <button class="go" onclick={runPath} disabled={pathBusy}>
            {pathBusy ? 'finding…' : 'Find CALLS path'}
          </button>
          {#if pathNotice}<span class="none">{pathNotice}</span>{/if}
        </div>
      {/if}

      {#if store.pending > 0}
        <div class="busy mono">loading…</div>
      {/if}

      <button class="openomni" onclick={() => (omniboxOpen = true)}>
        &#8981; Search symbols, files, docs&#8230; <span class="kbd">&#8984;K</span>
      </button>
    {:else}
      <!-- overview mode -->
      {#if !scope.service}
        <ServicePicker />
      {:else}
        <div class="ostage">
          {#if overview.lens === 'flows'}
            <FlowRail
              entries={overview.flowEntries}
              status={overview.flowEntriesStatus}
              error={overview.flowEntriesError}
              activeId={overview.activeFlowId}
              activeName={overview.activeFlowName}
              tracing={overview.flowTracing}
              steps={overview.activeFlowSteps.length}
              onscreen={flowOnscreen}
              missing={flowMissing}
              onSelect={ovSelectFlow}
            />
          {/if}
          <div class="ocanvas-wrap">
            <OverviewCanvas
              nodes={overview.graph.nodes}
              edges={overview.graph.edges}
              selected={overview.selected}
              decorations={overview.decorations}
              onSelect={ovSelect}
              onToggle={ovToggle}
            />

            <!-- Structure edge-mode toggle -->
            {#if overview.lens === 'structure'}
              {@const total = overview.graph.edges.length}
              {@const kept = overview.decorations.visibleEdgeIds?.size ?? total}
              <button class="chip botleft" data-testid="edge-mode-toggle" onclick={ovToggleEdgeMode}>
                {#if overview.edgeMode === 'strong'}
                  edges: strong ({kept}/{total})
                {:else}
                  edges: all ({total})
                {/if}
              </button>
            {/if}

            <!-- Usage direction toggle -->
            {#if overview.lens === 'usage'}
              <button class="chip botleft" data-testid="usage-dir-toggle" onclick={ovToggleUsageDir}>
                {overview.usageDirection === 'up' ? 'who uses this (callers)' : 'what this uses (callees)'}
              </button>
            {/if}

            <!-- Hotspots legend -->
            {#if overview.lens === 'hotspots'}
              <div class="legend topcenter" data-testid="hotspot-legend">
                <span class="lg-label">incoming calls</span>
                <span class="ramp">
                  <i style="background:#FFF3BF"></i><i style="background:#FFD8A8"></i><i
                    style="background:#FFA94D"
                  ></i><i style="background:#F76707"></i><i style="background:#D9480F"></i>
                </span>
                <span class="lg-label">hot</span>
              </div>
            {/if}

            <!-- Dead code legend -->
            {#if overview.lens === 'dead'}
              <div class="legend topcenter" data-testid="dead-legend">
                {#if overview.deadStatus === 'loading'}
                  computing reachability…
                {:else if deadCounts}
                  dead {deadCounts.dead} · possibly {deadCounts.possiblyLive} · test-only {deadCounts.testOnly}
                {:else if overview.deadStatus === 'error'}
                  dead-code report failed
                {:else}
                  dead 0 · possibly 0 · test-only 0
                {/if}
              </div>
            {/if}
          </div>
        </div>

        <div class="ostats" data-testid="overview-stats">
          {overview.stats.dirs} dirs &middot; {overview.stats.files} files &middot; {overview.stats.symbols} symbols
        </div>

        {#if overview.status === 'loading'}
          <div class="busy mono">loading service…</div>
        {/if}

        {#if overview.isEmpty}
          <div class="oempty">This service has no files in the graph.</div>
        {/if}

        {#if overview.warnings.length > 0}
          <div class="notices">
            {#each overview.warnings as w}
              <div class="notice mono">guardrail: {w}</div>
            {/each}
          </div>
        {/if}

        {#if overview.error}
          <div class="errbar">
            <span>{overview.error}</span>
            <button onclick={ovDismissError}>dismiss</button>
          </div>
        {/if}
      {/if}
    {/if}
  </div>

  {#if mode === 'workbench' && store.selected}
    <aside class="insp">
      <Inspector
        node={store.nodes.get(store.selected) ?? null}
        edges={store.edgeList}
        allNodes={store.nodes}
        {loadSource}
        onExpandGroup={expandGroup}
        onFocusNode={focusNode}
        onClose={closeInspector}
      />
    </aside>
  {:else if mode === 'overview' && overview.selectedNode}
    <aside class="insp">
      <OverviewPanel
        node={overview.selectedNode}
        graph={overview.graph}
        drillLoading={panelDrillLoading}
        drillError={panelDrillError}
        lens={overview.lens}
        symbolCallers={panelCallers}
        symbolCallersLoading={panelCallersLoading}
        deadNames={panelDeadNames}
        onExpand={ovPanelExpand}
        onCollapse={ovPanelCollapse}
        onSelectConnection={ovSelectConnection}
        onOpenInWorkbench={ovOpenInWorkbench}
        onRetryDrill={ovRetryDrill}
        onSelectCaller={ovSelectCaller}
      />
    </aside>
  {/if}
</div>

<Omnibox bind:open={omniboxOpen} service={scope.service} onAdd={addFound} onOpen={openFound} />

<style>
  .explorer {
    height: 100%;
    display: grid;
    grid-template-columns: 1fr;
    overflow: hidden;
  }
  .explorer.has-panel {
    grid-template-columns: 1fr auto;
  }
  .stage {
    position: relative;
    min-width: 0;
    overflow: hidden;
  }
  .insp {
    width: 336px;
    border-left: 1px solid var(--border);
    background: var(--bg-panel);
    overflow-y: auto;
  }

  .modes {
    position: absolute;
    top: var(--s-3);
    left: var(--s-3);
    display: flex;
    gap: 2px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 3px;
    box-shadow: var(--shadow-1);
    /* above the service-picker overlay (z-index 5): the mode toggle must stay
       reachable when no service is selected, or workbench is unreachable */
    z-index: 6;
  }
  .modebtn {
    padding: 4px 12px;
    border-radius: var(--r-sm);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink-3);
  }
  .modebtn:hover {
    background: var(--bg-hover);
  }
  .modebtn.active {
    background: var(--accent);
    color: #fff;
  }

  .lensbar-mount {
    position: absolute;
    top: var(--s-3);
    left: 190px;
    z-index: 6;
  }

  /* overview stage: optional flow rail column + the canvas */
  .ostage {
    position: absolute;
    inset: 0;
    display: flex;
    overflow: hidden;
  }
  .ocanvas-wrap {
    position: relative;
    flex: 1;
    min-width: 0;
    overflow: hidden;
  }

  /* small bottom-left chip (edge-mode / usage direction) */
  .chip {
    position: absolute;
    bottom: 12px;
    left: 12px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 4px 10px;
    font-size: 10.5px;
    color: var(--ink-2);
    box-shadow: var(--shadow-1);
    z-index: 3;
  }
  .chip:hover {
    background: var(--bg-hover);
  }
  .chip.botleft {
    /* sit above the stats chip which is at bottom:12px left:12px */
    bottom: 40px;
  }

  /* top-center legend chip (hotspots ramp / dead counts) */
  .legend {
    position: absolute;
    top: 12px;
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-full);
    padding: 3px 12px;
    font-size: 10.5px;
    color: var(--ink-2);
    box-shadow: var(--shadow-1);
    z-index: 3;
  }
  .legend .lg-label {
    color: var(--ink-3);
    font-size: 10px;
  }
  .legend .ramp {
    display: inline-flex;
    border-radius: var(--r-sm);
    overflow: hidden;
  }
  .legend .ramp i {
    width: 14px;
    height: 10px;
    display: block;
  }

  .ostats {
    position: absolute;
    bottom: 12px;
    left: 12px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 4px 10px;
    font-size: 10.5px;
    color: var(--ink-3);
    box-shadow: var(--shadow-1);
    z-index: 2;
  }
  .oempty {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    color: var(--ink-3);
    font-size: var(--text-base);
    pointer-events: none;
  }

  .openomni {
    position: absolute;
    top: var(--s-3);
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 5px 12px;
    color: var(--ink-3);
    font-size: var(--text-sm);
    box-shadow: var(--shadow-1);
  }
  .openomni:hover {
    background: var(--bg-hover);
  }
  .openomni .kbd {
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 1px 5px;
  }

  .notices {
    position: absolute;
    bottom: var(--s-3);
    right: var(--s-3);
    display: flex;
    flex-direction: column;
    gap: 4px;
    max-width: 420px;
  }
  .notice {
    background: var(--warn-subtle);
    border: 1px solid var(--warn);
    border-radius: var(--r-md);
    padding: 4px 10px;
    font-size: 11px;
    color: var(--warn);
  }

  .errbar {
    position: absolute;
    top: var(--s-3);
    right: var(--s-3);
    display: flex;
    align-items: center;
    gap: 10px;
    background: var(--err-subtle);
    border: 1px solid var(--err);
    border-radius: var(--r-md);
    padding: 6px 12px;
    font-size: var(--text-sm);
    color: var(--err);
    max-width: 480px;
  }
  .errbar button {
    font-size: var(--text-xs);
    text-decoration: underline;
    color: var(--err);
  }

  .pathbar {
    position: absolute;
    bottom: var(--s-4);
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    align-items: center;
    gap: 10px;
    background: var(--bg-panel);
    border: 1px solid var(--accent-border);
    border-radius: var(--r-lg);
    padding: 8px 14px;
    box-shadow: var(--shadow-2);
    font-size: var(--text-sm);
  }
  .pathbar .go {
    background: var(--accent);
    color: #fff;
    border-radius: var(--r-md);
    padding: 4px 12px;
    font-size: var(--text-sm);
    font-weight: 500;
  }
  .pathbar .go:hover {
    background: var(--accent-hover);
  }
  .pathbar .go:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .pathbar .none {
    color: var(--ink-3);
    font-style: italic;
    font-size: var(--text-xs);
  }

  .busy {
    position: absolute;
    top: var(--s-3);
    left: 50%;
    transform: translateX(-50%);
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-full);
    padding: 2px 10px;
    font-size: 10px;
    color: var(--ink-3);
    z-index: 3;
  }
</style>
