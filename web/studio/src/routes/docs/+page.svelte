<script lang="ts">
  /**
   * Docs plane (RFC-012 R5 / RFC-011): browse Document/DocumentChunk nodes,
   * their chunks, and each chunk's doc→code MENTIONS links. Three panes —
   * left: document list grouped by service + search; middle: chunk list;
   * right: chunk markdown + code links. Store-less orchestration mirroring
   * routes/flows/+page.svelte, with the same Svelte 5 effect-loop guards
   * (untrack on scope-follow RMW, stable callback consts, no $state mutation
   * inside $derived, unique {#each} keys).
   *
   * Scope: reads the global scope store. A concrete service filters the doc
   * list to that service; null ("All services") shows everything. Only
   * codegraph and dough-core actually have docs, so most services get an
   * honest empty state.
   *
   * Deep-linkable: selected doc + chunk + search query live in the URL query
   * string and are restored on load. Code links navigate to /graph?nodes=<id>.
   */
  import { onMount, untrack } from 'svelte'
  import { goto, replaceState } from '$app/navigation'
  import { page } from '$app/state'
  import { scope } from '$lib/stores/scope.svelte'
  import DocList from '$lib/components/docs/DocList.svelte'
  import ChunkList from '$lib/components/docs/ChunkList.svelte'
  import ChunkContent from '$lib/components/docs/ChunkContent.svelte'
  import { parseDocsSelection, serializeDocsSelection } from '$lib/components/docs/urlState'
  import { timedFetch } from '$lib/api/timedFetch'
  import type { ApiEnvelope, ApiError } from '$lib/types/graph'
  import type {
    DocChunk,
    DocDetail,
    DocGroup,
    DocListResponse,
    DocSearchHit,
    DocSearchResponse,
    DocSummary
  } from '$lib/types/docs'
  import { groupDocumentsByService } from '$lib/components/docs/grouping'

  type Status = 'idle' | 'loading' | 'loaded' | 'error'

  // ── document list ──
  let documents = $state<DocSummary[]>([])
  let listStatus = $state<Status>('idle')
  let listError = $state('')

  // ── selected document detail ──
  let selectedDocId = $state<string | null>(null)
  let detail = $state<DocDetail | null>(null)
  let detailStatus = $state<Status>('idle')
  let detailError = $state('')

  // ── selected chunk ──
  let selectedChunkId = $state<string | null>(null)

  // ── search ──
  let searchQuery = $state('')
  let searchHits = $state<DocSearchHit[]>([])
  let searchFallback = $state(false)
  let searchStatus = $state<'idle' | 'loading' | 'loaded' | 'error'>('idle')
  let searchError = $state('')

  let warnings = $state<string[]>([])
  let fatalError = $state<string | null>(null)
  let bootstrapped = false

  // groups + selected-chunk are pure projections of state → safe in $derived
  const groups = $derived<DocGroup[]>(groupDocumentsByService(documents))
  const selectedDoc = $derived<DocSummary | null>(
    detail?.document ?? documents.find((d) => d.nodeId === selectedDocId) ?? null
  )
  const selectedChunk = $derived<DocChunk | null>(
    detail?.chunks.find((c) => c.nodeId === selectedChunkId) ?? null
  )

  function pushWarnings(w: string[]) {
    if (w.length) warnings = [...new Set([...warnings, ...w])].slice(-5)
  }

  async function unwrap<T>(res: Response): Promise<T> {
    const body = (await res.json()) as ApiEnvelope<T> | ApiError
    if (!res.ok || 'error' in body) {
      const err = body as ApiError
      throw new Error(err.error ?? `HTTP ${res.status}`)
    }
    const env = body as ApiEnvelope<T>
    pushWarnings(env.warnings)
    return env.data
  }

  // ── loaders ────────────────────────────────────────────────
  async function loadDocuments(service: string | null) {
    listStatus = 'loading'
    listError = ''
    try {
      const qs = service ? `?service=${encodeURIComponent(service)}` : ''
      const data = await unwrap<DocListResponse>(await timedFetch(`/api/docs${qs}`))
      documents = data.documents
      listStatus = 'loaded'
    } catch (e) {
      listStatus = 'error'
      listError = e instanceof Error ? e.message : String(e)
    }
  }

  async function loadDetail(docId: string) {
    detailStatus = 'loading'
    detailError = ''
    try {
      const data = await unwrap<DocDetail>(await timedFetch(`/api/docs/${encodeURIComponent(docId)}`))
      // guard against a stale response after a fast re-select
      if (selectedDocId !== docId) return
      detail = data
      detailStatus = 'loaded'
      // keep an existing chunk selection if it's still present; else clear
      if (selectedChunkId && !data.chunks.some((c) => c.nodeId === selectedChunkId)) {
        selectedChunkId = null
      }
    } catch (e) {
      if (selectedDocId !== docId) return
      detail = null
      detailStatus = 'error'
      detailError = e instanceof Error ? e.message : String(e)
    }
  }

  let searchToken = 0
  async function runSearch(query: string, service: string | null) {
    const q = query.trim()
    if (q.length === 0) {
      searchStatus = 'idle'
      searchHits = []
      searchFallback = false
      return
    }
    const token = ++searchToken
    searchStatus = 'loading'
    searchError = ''
    try {
      const svc = service ? `&service=${encodeURIComponent(service)}` : ''
      const data = await unwrap<DocSearchResponse>(
        await timedFetch(`/api/docs/search?q=${encodeURIComponent(q)}${svc}`)
      )
      if (token !== searchToken) return // superseded by a newer keystroke
      searchHits = data.hits
      searchFallback = data.fallback
      searchStatus = 'loaded'
    } catch (e) {
      if (token !== searchToken) return
      searchStatus = 'error'
      searchError = e instanceof Error ? e.message : String(e)
    }
  }

  // ── selection handlers (stable consts — hoisted, not inline arrows) ──
  const selectDoc = (docId: string) => {
    if (docId === selectedDocId) return
    selectedDocId = docId
    selectedChunkId = null
    detail = null
    void loadDetail(docId)
  }

  const selectChunk = (chunkId: string) => {
    selectedChunkId = chunkId
  }

  let searchDebounce: ReturnType<typeof setTimeout> | undefined
  const onSearchInput = (value: string) => {
    searchQuery = value
    clearTimeout(searchDebounce)
    searchDebounce = setTimeout(() => void runSearch(value, scope.service), 220)
  }

  const openNode = (nodeId: string) => {
    void goto(`/graph?nodes=${encodeURIComponent(nodeId)}&sel=${encodeURIComponent(nodeId)}`)
  }

  const dismissError = () => {
    fatalError = null
  }

  // ── mount: restore scope + URL selection ───────────────────
  onMount(() => {
    const sel = parseDocsSelection(page.url.searchParams)
    searchQuery = sel.query

    void (async () => {
      await loadDocuments(scope.service)
      if (sel.query) await runSearch(sel.query, scope.service)
      if (sel.doc) {
        selectedDocId = sel.doc
        selectedChunkId = sel.chunk
        await loadDetail(sel.doc)
      }
      bootstrapped = true
    })()
  })

  // ── follow the global scope store ──────────────────────────
  // A scope change reloads the document list and (if the selected doc is no
  // longer in scope) clears the selection. untrack() around the RMW so this
  // effect only re-runs on scope.service, never on the state it writes.
  let lastScopeService: string | null | undefined = undefined
  $effect(() => {
    const svc = scope.service
    untrack(() => {
      if (!bootstrapped) return
      if (svc === lastScopeService) return
      lastScopeService = svc
      void loadDocuments(svc)
      if (searchQuery.trim()) void runSearch(searchQuery, svc)
    })
  })

  // ── URL sync (debounced replaceState, mirrors flows/+page.svelte) ──
  let urlTimer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    if (!bootstrapped) return
    const target = serializeDocsSelection({
      doc: selectedDocId,
      chunk: selectedChunkId,
      query: searchQuery.trim()
    })
    clearTimeout(urlTimer)
    urlTimer = setTimeout(() => {
      if (page.url.pathname + page.url.search !== target) {
        replaceState(target, {})
      }
    }, 300)
  })
</script>

<svelte:head>
  <title>CodeGraph Studio — Docs</title>
</svelte:head>

<div class="docs">
  <div class="col list">
    <DocList
      {groups}
      status={listStatus}
      error={listError}
      {selectedDocId}
      {searchQuery}
      {searchStatus}
      {searchHits}
      {searchFallback}
      {searchError}
      totalCount={documents.length}
      onSelect={selectDoc}
      {onSearchInput}
    />
  </div>

  <div class="col chunks">
    <ChunkList
      document={selectedDoc}
      chunks={detail?.chunks ?? []}
      status={selectedDocId ? detailStatus : 'idle'}
      error={detailError}
      {selectedChunkId}
      onSelect={selectChunk}
    />
  </div>

  <aside class="col content">
    <ChunkContent chunk={selectedChunk} onOpenNode={openNode} />
  </aside>

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
  .docs {
    height: 100%;
    display: grid;
    grid-template-columns: 300px minmax(220px, 340px) minmax(0, 1fr);
    overflow: hidden;
    position: relative;
  }
  .col {
    overflow: hidden;
    min-height: 0;
  }
  .col.content {
    border-left: 1px solid var(--border);
    background: var(--bg-panel);
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
</style>
