<script lang="ts">
  /**
   * Flows screen (RFC-012 R4): tiered entry-point rail → traced call spine →
   * inspector, plus a "Load onto canvas" handoff to /graph. Store-less
   * orchestration, following the pattern in routes/graph/+page.svelte —
   * see the comments there for the Svelte 5 effect-loop hazards this page
   * also has to avoid (stable callback identities, untrack on RMW inside
   * effects, stable object identity into Inspector's `node` prop).
   */
  import { onMount } from 'svelte'
  import { goto, replaceState } from '$app/navigation'
  import { page } from '$app/state'
  import EntryRail from '$lib/components/flows/EntryRail.svelte'
  import FlowSpine from '$lib/components/flows/FlowSpine.svelte'
  import Inspector from '$lib/components/inspector/Inspector.svelte'
  import type { ApiEnvelope, ApiError, GraphEdge, GraphNode, SourceResponse } from '$lib/types/graph'
  import type { EntryPoint, EntryPointsResponse, Flow, FlowResponse, FlowStep, ServicesResponse } from '$lib/types/flows'

  type Status = 'idle' | 'loading' | 'loaded' | 'error'

  let services = $state<string[]>([])
  let activeService = $state('')
  let servicesStatus = $state<Status>('idle')

  let entries = $state<EntryPoint[]>([])
  let entriesStatus = $state<'loading' | 'loaded' | 'error'>('loading')
  let entriesError = $state('')

  let selectedEntry = $state<EntryPoint | null>(null)
  let flow = $state<Flow | null>(null)
  let flowStatus = $state<'idle' | 'loading' | 'loaded' | 'error'>('idle')
  let flowError = $state('')
  let depth = $state(4)

  let selectedStep = $state<FlowStep | null>(null)

  let warnings = $state<string[]>([])
  let fatalError = $state<string | null>(null)

  let bootstrapped = false

  async function unwrap<T>(res: Response): Promise<T> {
    const body = (await res.json()) as ApiEnvelope<T> | ApiError
    if (!res.ok || 'error' in body) {
      const err = body as ApiError
      throw new Error(err.error ?? `HTTP ${res.status}`)
    }
    const env = body as ApiEnvelope<T>
    if (env.warnings.length) {
      warnings = [...new Set([...warnings, ...env.warnings])].slice(-5)
    }
    return env.data
  }

  async function loadServices() {
    servicesStatus = 'loading'
    try {
      const data = await unwrap<ServicesResponse>(await fetch('/api/services'))
      services = data.services
      servicesStatus = 'loaded'
    } catch (e) {
      servicesStatus = 'error'
      fatalError = e instanceof Error ? e.message : String(e)
    }
  }

  async function loadEntries(service: string) {
    entriesStatus = 'loading'
    entriesError = ''
    try {
      const data = await unwrap<EntryPointsResponse>(
        await fetch(`/api/entrypoints?service=${encodeURIComponent(service)}&limit=100`)
      )
      entries = data.entries
      entriesStatus = 'loaded'
      if (data.tier_errors?.length) {
        warnings = [...new Set([...warnings, ...data.tier_errors])].slice(-5)
      }
    } catch (e) {
      entriesStatus = 'error'
      entriesError = e instanceof Error ? e.message : String(e)
    }
  }

  async function traceFlow(entry: EntryPoint, requestedDepth: number) {
    flowStatus = 'loading'
    flowError = ''
    try {
      const data = await unwrap<FlowResponse>(
        await fetch('/api/flow', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ node_id: entry.node_id, max_depth: requestedDepth })
        })
      )
      const traced = data.flows[0] ?? null
      flow = traced
      flowStatus = 'loaded'
      const root = traced?.steps.find((s) => s.depth === 0) ?? traced?.steps[0] ?? null
      selectedStep = root
    } catch (e) {
      flow = null
      flowStatus = 'error'
      flowError = e instanceof Error ? e.message : String(e)
      selectedStep = null
    }
  }

  // ── mount: services → entries → (optional) deep-linked entry+flow ─────
  onMount(() => {
    const p = page.url.searchParams
    const qsService = p.get('service')
    const qsSel = p.get('sel')
    const qsDepth = p.get('depth')
    if (qsDepth) {
      const d = Number(qsDepth)
      if (Number.isFinite(d)) depth = Math.min(10, Math.max(1, Math.trunc(d)))
    }

    void (async () => {
      await loadServices()
      const svc =
        qsService && services.includes(qsService)
          ? qsService
          : services.includes('codegraph')
            ? 'codegraph'
            : (services[0] ?? '')
      activeService = svc
      if (svc) await loadEntries(svc)

      if (qsSel) {
        const match = entries.find((e) => e.node_id === qsSel)
        if (match) {
          selectedEntry = match
          await traceFlow(match, depth)
          const qsStep = p.get('step')
          if (qsStep && flow) {
            const stepMatch = flow.steps.find((s) => s.nodeId === qsStep)
            if (stepMatch) selectedStep = stepMatch
          }
        }
      }
      bootstrapped = true
    })()
  })

  // ── URL sync (debounced replaceState, mirrors routes/graph/+page.svelte) ──
  let urlTimer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    if (!bootstrapped) return
    const p = new URLSearchParams()
    if (activeService) p.set('service', activeService)
    if (selectedEntry) p.set('sel', selectedEntry.node_id)
    if (selectedStep?.nodeId) p.set('step', selectedStep.nodeId)
    if (depth !== 4) p.set('depth', String(depth))
    const qs = p.toString()
    clearTimeout(urlTimer)
    urlTimer = setTimeout(() => {
      const target = qs ? `/flows?${qs}` : '/flows'
      if (page.url.pathname + page.url.search !== target) {
        replaceState(target, {})
      }
    }, 300)
  })

  // ── service switch ──────────────────────────────────────
  async function changeService(next: string) {
    if (next === activeService) return
    activeService = next
    selectedEntry = null
    flow = null
    flowStatus = 'idle'
    selectedStep = null
    await loadEntries(next)
  }
  function onServiceChange(ev: Event) {
    void changeService((ev.target as HTMLSelectElement).value)
  }

  // ── entry select ─────────────────────────────────────────
  const selectEntry = (entry: EntryPoint) => {
    selectedEntry = entry
    void traceFlow(entry, depth)
  }

  // ── step select / inspector wiring ──────────────────────
  const selectStep = (step: FlowStep) => {
    selectedStep = step
  }
  const closeInspector = () => {
    selectedStep = null
  }

  // GraphNode map built once per flow, so Inspector's `node` prop keeps
  // stable object identity across renders (only the flow reference changes).
  const stepNodes = $derived.by(() => {
    const map = new Map<string, GraphNode>()
    for (const s of flow?.steps ?? []) {
      if (!s.nodeId) continue
      map.set(s.nodeId, {
        node_id: s.nodeId,
        label: s.label,
        name: s.name,
        file_path: s.filePath,
        start_line: s.startLine
      })
    }
    return map
  })

  const stepEdges = $derived.by(() => {
    const byKey = new Map<string, FlowStep>()
    for (const s of flow?.steps ?? []) byKey.set(s.nodeKey, s)
    const edges: GraphEdge[] = []
    for (const s of flow?.steps ?? []) {
      if (!s.parentKey || !s.nodeId) continue
      const parent = byKey.get(s.parentKey)
      if (!parent?.nodeId) continue
      edges.push({ from: parent.nodeId, to: s.nodeId, type: 'CALLS' })
    }
    return edges
  })

  const inspectorNode = $derived(selectedStep?.nodeId ? (stepNodes.get(selectedStep.nodeId) ?? null) : null)

  const loadSource = async (nodeId: string): Promise<SourceResponse | null> => {
    try {
      return await unwrap<SourceResponse>(await fetch(`/api/source?node_id=${encodeURIComponent(nodeId)}`))
    } catch (e) {
      fatalError = e instanceof Error ? e.message : String(e)
      return null
    }
  }
  const expandGroup = (nodeId: string) => {
    void goto(`/graph?nodes=${encodeURIComponent(nodeId)}&sel=${encodeURIComponent(nodeId)}`)
  }
  const focusNode = (nodeId: string) => {
    void goto(`/graph?nodes=${encodeURIComponent(nodeId)}&sel=${encodeURIComponent(nodeId)}`)
  }

  const loadOntoCanvas = () => {
    if (!flow) return
    const ids = flow.steps.filter((s) => s.nodeId).map((s) => s.nodeId as string)
    if (ids.length === 0) return
    const selId = selectedEntry?.node_id ?? ids[0]
    void goto(
      `/graph?nodes=${ids.map(encodeURIComponent).join(',')}&sel=${encodeURIComponent(selId)}&stitch=CALLS`
    )
  }

  const dismissError = () => {
    fatalError = null
  }
</script>

<svelte:head>
  <title>CodeGraph Studio — Flows</title>
</svelte:head>

<div class="flows">
  <div class="rail-col">
    <div class="svcrow">
      <select class="svcselect" value={activeService} onchange={onServiceChange} disabled={servicesStatus !== 'loaded'}>
        {#each services as svc (svc)}
          <option value={svc}>{svc}</option>
        {/each}
      </select>
    </div>
    <EntryRail
      {entries}
      selectedId={selectedEntry?.node_id ?? null}
      status={entriesStatus}
      error={entriesError}
      onSelect={selectEntry}
    />
  </div>

  <FlowSpine
    {flow}
    steps={flow?.steps ?? []}
    selectedNodeId={selectedStep?.nodeId ?? null}
    status={flowStatus}
    error={flowError}
    onSelectStep={selectStep}
    onLoadCanvas={loadOntoCanvas}
  />

  {#if selectedStep}
    <aside class="insp">
      <Inspector
        node={inspectorNode}
        edges={stepEdges}
        allNodes={stepNodes}
        {loadSource}
        onExpandGroup={expandGroup}
        onFocusNode={focusNode}
        onClose={closeInspector}
      />
    </aside>
  {:else}
    <aside class="insp empty">
      <span class="placeholder">select a step</span>
    </aside>
  {/if}

  {#if warnings.length > 0}
    <div class="notices">
      {#each warnings as w (w)}
        <div class="notice mono">guardrail: {w}</div>
      {/each}
    </div>
  {/if}

  {#if fatalError}
    <div class="errbar">
      <span>{fatalError}</span>
      <button onclick={dismissError}>dismiss</button>
    </div>
  {/if}
</div>

<style>
  .flows {
    height: 100%;
    display: grid;
    grid-template-columns: 300px minmax(0, 1fr) 336px;
    overflow: hidden;
    position: relative;
  }

  .rail-col {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    border-right: 1px solid var(--border);
    background: var(--bg-panel);
  }
  .svcrow {
    padding: var(--s-2) var(--s-3);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .svcselect {
    width: 100%;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 5px 8px;
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink);
  }
  .rail-col :global(.rail) {
    border-right: none;
    flex: 1;
    min-height: 0;
  }

  .insp {
    width: 336px;
    border-left: 1px solid var(--border);
    background: var(--bg-panel);
    overflow-y: auto;
  }
  .insp.empty {
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .placeholder {
    color: var(--ink-3);
    font-size: var(--text-sm);
  }

  .notices {
    position: absolute;
    bottom: var(--s-3);
    right: 352px;
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
    right: 352px;
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
</style>
