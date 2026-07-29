<script lang="ts">
  /**
   * The ⌘K command palette: overlay + input + grouped, keyboard-navigable
   * results over /api/find. Parsing (`label:`/`svc:` filters) and grouping
   * are pure helpers in ./query.ts so they're unit-testable without mounting
   * this component; this file only wires fetch/debounce/keyboard/DOM.
   */
  import type { ApiEnvelope, ApiError, FindResponse, FoundNode } from '$lib/types/graph'
  import { nodeColors } from '$lib/components/canvas/elements'
  import { groupResults, parseQuery, semanticDisableReason, type ResultGroup } from './query'
  import { timedFetch } from '$lib/api/timedFetch'

  let {
    open = $bindable(false),
    service = null,
    onAdd,
    onOpen
  }: {
    open?: boolean
    service?: string | null
    onAdd: (n: FoundNode) => void
    onOpen: (n: FoundNode) => void
  } = $props()

  const DEBOUNCE_MS = 180
  const MAX_RESULTS = 50

  let query = $state('')
  let inputEl: HTMLInputElement | undefined = $state()
  let panelEl: HTMLDivElement | undefined = $state()

  let groups = $state<ResultGroup[]>([])
  let loading = $state(false)
  let errorMessage = $state<string | null>(null)
  let activeIndex = $state(0)

  let semanticEnabled = $state(false)
  let semanticDisabledReason = $state<string | null>(null)

  let debounceTimer: ReturnType<typeof setTimeout> | undefined
  let abortController: AbortController | undefined

  /** Flattened view of the grouped results, for keyboard nav across group boundaries. */
  const flatResults = $derived(groups.flatMap((g) => g.nodes))

  $effect(() => {
    if (open) {
      queueMicrotask(() => inputEl?.focus())
    } else {
      reset()
    }
  })

  function reset() {
    query = ''
    groups = []
    errorMessage = null
    activeIndex = 0
    loading = false
    clearTimeout(debounceTimer)
    abortController?.abort()
  }

  function close() {
    open = false
  }

  function handleInput() {
    activeIndex = 0
    errorMessage = null
    clearTimeout(debounceTimer)

    const parsed = parseQuery(query)
    if (parsed.text.length === 0 && !parsed.label && !parsed.service) {
      groups = []
      loading = false
      abortController?.abort()
      return
    }

    debounceTimer = setTimeout(() => runSearch(), DEBOUNCE_MS)
  }

  async function runSearch() {
    const parsed = parseQuery(query)
    abortController?.abort()
    const controller = new AbortController()
    abortController = controller

    const params = new URLSearchParams()
    if (parsed.text) params.set('q', parsed.text)
    if (parsed.label) params.set('label', parsed.label)
    const effectiveService = parsed.service ?? service ?? undefined
    if (effectiveService) params.set('service', effectiveService)
    params.set('limit', String(MAX_RESULTS))
    if (semanticEnabled) params.set('semantic', 'true')

    loading = true
    try {
      const res = await timedFetch(`/api/find?${params.toString()}`, { signal: controller.signal })
      const body = await res.json()

      if (!res.ok) {
        const err = body as ApiError
        if (semanticEnabled) {
          const reason = semanticDisableReason(err)
          if (reason) {
            semanticEnabled = false
            semanticDisabledReason = reason
            errorMessage = null
            loading = false
            return
          }
        }
        errorMessage = err.error
        groups = []
        loading = false
        return
      }

      const envelope = body as ApiEnvelope<FindResponse>
      groups = groupResults(envelope.data.results.slice(0, MAX_RESULTS))
      errorMessage = null
      loading = false
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return
      errorMessage = e instanceof Error ? e.message : String(e)
      groups = []
      loading = false
    }
  }

  function toggleSemantic() {
    if (semanticDisabledReason) return
    semanticEnabled = !semanticEnabled
    if (query.trim().length > 0) runSearch()
  }

  function moveActive(delta: number) {
    if (flatResults.length === 0) return
    activeIndex = (activeIndex + delta + flatResults.length) % flatResults.length
    scrollActiveIntoView()
  }

  function scrollActiveIntoView() {
    queueMicrotask(() => {
      panelEl?.querySelector('.rrow.sel')?.scrollIntoView({ block: 'nearest' })
    })
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      close()
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      moveActive(1)
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      moveActive(-1)
      return
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      const active = flatResults[activeIndex]
      if (!active) return
      if (e.altKey) {
        onAdd(active)
      } else {
        onOpen(active)
        close()
      }
    }
  }

  function handleBackdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget) close()
  }

  function indexOf(node: FoundNode): number {
    return flatResults.indexOf(node)
  }

  function rowClick(node: FoundNode) {
    onOpen(node)
    close()
  }

  function rowAddClick(e: MouseEvent, node: FoundNode) {
    e.stopPropagation()
    onAdd(node)
  }
</script>

{#if open}
  <div class="backdrop" onclick={handleBackdropClick} role="presentation">
    <div class="palette" bind:this={panelEl} role="dialog" aria-modal="true" aria-label="Search">
      <div class="prow">
        <span class="glass" aria-hidden="true">&#9906;</span>
        <input
          bind:this={inputEl}
          bind:value={query}
          oninput={handleInput}
          onkeydown={handleKeydown}
          class="q mono"
          type="text"
          placeholder="Search nodes… (label:Function svc:name)"
          spellcheck="false"
          autocomplete="off"
        />
        <button
          class="semantic-chip"
          class:on={semanticEnabled}
          class:disabled={!!semanticDisabledReason}
          disabled={!!semanticDisabledReason}
          title={semanticDisabledReason ?? 'Toggle semantic search'}
          onclick={toggleSemantic}
          type="button"
        >
          semantic
        </button>
        {#if loading}
          <span class="searching mono">searching…</span>
        {/if}
        <span class="kbd">esc</span>
      </div>

      <div class="results">
        {#if query.trim().length === 0}
          <div class="hint">
            <div class="hint-row"><span class="kbd">label:Function</span> filter by node type</div>
            <div class="hint-row"><span class="kbd">svc:name</span> filter by service</div>
          </div>
        {:else if errorMessage}
          <div class="error-row">{errorMessage}</div>
        {:else if groups.length === 0 && !loading}
          <div class="empty-row">no matches for &ldquo;{query}&rdquo;</div>
        {:else}
          {#each groups as group (group.label)}
            <div class="ghead">{group.label}</div>
            {#each group.nodes as node (node.node_id)}
              {@const idx = indexOf(node)}
              {@const colors = nodeColors(node.label)}
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <div
                class="rrow"
                class:sel={idx === activeIndex}
                onclick={() => rowClick(node)}
                onmouseenter={() => (activeIndex = idx)}
                role="option"
                aria-selected={idx === activeIndex}
                tabindex="-1"
              >
                <span class="dot" style="background:{colors.fg}"></span>
                <span class="nm mono">{node.name}</span>
                <span class="meta">
                  {#if node.file_path}{node.file_path}{#if node.start_line}:{node.start_line}{/if}{/if}
                  {#if node.file_path && node.service}&nbsp;&middot;&nbsp;{/if}
                  {#if node.service}{node.service}{/if}
                </span>
                <button class="add-btn" onclick={(e) => rowAddClick(e, node)} type="button" tabindex="-1">
                  + add
                </button>
              </div>
            {/each}
          {/each}
        {/if}
      </div>

      <div class="pfoot">
        <span class="h"><span class="kbd">&uarr;&darr;</span> navigate</span>
        <span class="h"><span class="kbd">&crarr;</span> open</span>
        <span class="h"><span class="kbd">&#8997;&crarr;</span> add to canvas</span>
      </div>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(22, 24, 29, 0.25);
    display: flex;
    justify-content: center;
    padding-top: var(--s-8);
    z-index: 100;
  }
  .palette {
    width: 640px;
    max-height: 70vh;
    display: flex;
    flex-direction: column;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    box-shadow: var(--shadow-3);
    overflow: hidden;
  }
  .prow {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: var(--s-3) var(--s-4);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .glass {
    color: var(--ink-3);
    font-size: 14px;
  }
  .q {
    flex: 1;
    border: none;
    outline: none;
    background: none;
    font-size: var(--text-md);
    color: var(--ink);
  }
  .q::placeholder {
    color: var(--ink-disabled);
  }
  .searching {
    font-size: 10px;
    color: var(--ink-3);
    white-space: nowrap;
  }
  .kbd {
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 1px 5px;
    color: var(--ink-3);
    white-space: nowrap;
  }
  .semantic-chip {
    font-size: var(--text-xs);
    font-weight: 500;
    color: var(--ink-3);
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-full);
    padding: 1px 10px;
    cursor: pointer;
    white-space: nowrap;
  }
  .semantic-chip:hover:not(.disabled) {
    background: var(--bg-hover);
  }
  .semantic-chip.on {
    background: var(--node-function-bg);
    color: var(--node-function);
    border-color: var(--node-function);
  }
  .semantic-chip.disabled {
    color: var(--ink-disabled);
    cursor: not-allowed;
  }

  .results {
    overflow-y: auto;
    padding-bottom: var(--s-1);
    flex: 1;
    min-height: 0;
  }
  .hint {
    padding: var(--s-4);
  }
  .hint-row {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: var(--text-sm);
    color: var(--ink-3);
    padding: 4px 0;
  }
  .empty-row,
  .error-row {
    padding: var(--s-4);
    font-size: var(--text-sm);
    color: var(--ink-3);
  }
  .error-row {
    color: var(--err);
  }

  .ghead {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-3);
    padding: var(--s-2) var(--s-4) var(--s-1);
  }
  .rrow {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px var(--s-4);
    cursor: pointer;
  }
  .rrow:hover {
    background: var(--bg-subtle);
  }
  .rrow.sel {
    background: var(--accent-subtle);
  }
  .rrow .dot {
    width: 8px;
    height: 8px;
    border-radius: var(--r-full);
    flex: none;
  }
  .rrow .nm {
    font-size: var(--text-sm);
    color: var(--ink);
    white-space: nowrap;
    flex: none;
  }
  .rrow .meta {
    flex: 1;
    font-size: var(--text-xs);
    color: var(--ink-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .add-btn {
    display: none;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--accent-ink);
    background: var(--bg-panel);
    border: 1px solid var(--accent-border);
    border-radius: var(--r-sm);
    padding: 0 5px;
    flex: none;
    cursor: pointer;
  }
  .rrow:hover .add-btn {
    display: inline-block;
  }

  .pfoot {
    display: flex;
    align-items: center;
    gap: var(--s-4);
    padding: var(--s-2) var(--s-4);
    border-top: 1px solid var(--border);
    background: var(--bg-subtle);
    font-size: var(--text-xs);
    color: var(--ink-3);
    flex: none;
  }
  .pfoot .h {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }
</style>
