<script lang="ts">
  /**
   * Graph explorer (RFC-012 R1–R3): omnibox → canvas → inspector, wired
   * through ExplorerStore. This page owns the store, keyboard shortcuts,
   * and the deep-link round-trip; every panel below it is presentational.
   */
  import { onMount } from 'svelte'
  import { replaceState } from '$app/navigation'
  import { page } from '$app/state'
  import GraphCanvas from '$lib/components/canvas/GraphCanvas.svelte'
  import Omnibox from '$lib/components/omnibox/Omnibox.svelte'
  import Inspector from '$lib/components/inspector/Inspector.svelte'
  import { ExplorerStore } from '$lib/stores/explorer.svelte'
  import type { FoundNode } from '$lib/types/graph'

  const store = new ExplorerStore()
  let omniboxOpen = $state(false)
  let pathBusy = $state(false)
  let pathNotice = $state<string | null>(null)

  // ── deep-link restore + persist ──────────────────────────
  onMount(() => {
    const p = page.url.searchParams
    const ids = (p.get('nodes') ?? '').split(',').filter(Boolean)
    const sel = p.get('sel')
    const pins = (p.get('pin') ?? '').split(',').filter(Boolean)
    if (ids.length) {
      store.hydrate(ids).then(() => {
        if (sel && store.nodes.has(sel)) store.select(sel)
        for (const pin of pins) if (store.nodes.has(pin)) store.togglePin(pin)
      })
    } else {
      omniboxOpen = true
    }
  })

  let urlTimer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    const qs = store.toParams().toString()
    clearTimeout(urlTimer)
    urlTimer = setTimeout(() => {
      const target = qs ? `/graph?${qs}` : '/graph'
      if (page.url.pathname + page.url.search !== target) {
        replaceState(target, {})
      }
    }, 300)
  })

  // ── omnibox wiring ───────────────────────────────────────
  function addFound(n: FoundNode) {
    store.addFound(n)
  }
  function openFound(n: FoundNode) {
    store.addFound(n)
    store.select(n.node_id)
    omniboxOpen = false
  }

  // ── expand defaults ──────────────────────────────────────
  // Double-click / `e`: the everyday traversal — one hop of calls, both ways.
  function quickExpand(nodeId: string) {
    store.expand(nodeId, ['CALLS'], 'both', 1)
  }

  async function runPath() {
    if (store.pinned.length !== 2 || pathBusy) return
    pathBusy = true
    pathNotice = null
    const count = await store.path(store.pinned[0], store.pinned[1], ['CALLS'], {
      direction: 'both'
    })
    pathBusy = false
    if (count === 0) pathNotice = 'no CALLS path between the pinned nodes'
    else if (count !== null) pathNotice = null
  }

  // ── keyboard ─────────────────────────────────────────────
  function onKeydown(ev: KeyboardEvent) {
    const inField =
      ev.target instanceof HTMLElement && ['INPUT', 'TEXTAREA'].includes(ev.target.tagName)
    if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === 'k') {
      ev.preventDefault()
      omniboxOpen = !omniboxOpen
      return
    }
    if (inField || omniboxOpen) return
    switch (ev.key) {
      case '/':
        ev.preventDefault()
        omniboxOpen = true
        break
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
  }

  const pinnedNames = $derived(
    store.pinned.map((id) => store.nodes.get(id)?.name ?? id.slice(-8))
  )

  // Stable callback identities: inline arrows in the template are re-created
  // on every render, and any child $effect that calls one tracks the prop —
  // combined with callbacks that write store state (pending), that's an
  // infinite effect loop (effect_update_depth_exceeded).
  const selectNode = (id: string | null) => store.select(id)
  const togglePin = (id: string) => store.togglePin(id)
  const removeNode = (id: string) => store.removeNode(id)
  const loadSource = (id: string) => store.source(id)
  const expandGroup = (nodeId: string, relType: string, direction: 'in' | 'out') =>
    store.expand(nodeId, [relType], direction, 1)
  const focusNode = (id: string) => store.select(id)
  const closeInspector = () => store.select(null)
  const dismissError = () => store.dismissError()
</script>

<svelte:window onkeydown={onKeydown} />

<svelte:head>
  <title>CodeGraph Studio — Graph</title>
</svelte:head>

<div class="explorer">
  <div class="stage">
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
  </div>

  {#if store.selected}
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
  {/if}
</div>

<Omnibox bind:open={omniboxOpen} onAdd={addFound} onOpen={openFound} />

<style>
  .explorer {
    height: 100%;
    display: grid;
    grid-template-columns: 1fr auto;
    overflow: hidden;
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
    left: var(--s-3);
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-full);
    padding: 2px 10px;
    font-size: 10px;
    color: var(--ink-3);
  }
</style>
