<script lang="ts">
  /**
   * Cypher console (RFC-012 R8): read-only query editor → tabular results,
   * with the tool's guardrails surfaced verbatim (R9 failure honesty). Raw
   * cypher is NOT auto-scoped; the scope affordance only *offers* to insert a
   * serviceName filter at the cursor. Store-less orchestration, mirroring
   * routes/flows/+page.svelte for the Svelte-5 effect-loop discipline
   * (untrack on RMW inside effects, stable callback identities, keyed each).
   */
  import { onMount, untrack } from 'svelte'
  import { goto, replaceState } from '$app/navigation'
  import { page } from '$app/state'
  import { scope } from '$lib/stores/scope.svelte'
  import CypherEditor from '$lib/components/console/CypherEditor.svelte'
  import ParamsPanel from '$lib/components/console/ParamsPanel.svelte'
  import ResultsTable from '$lib/components/console/ResultsTable.svelte'
  import HistoryList from '$lib/components/console/HistoryList.svelte'
  import {
    loadHistory,
    pushHistory,
    saveHistory,
    type HistoryStorage
  } from '$lib/components/console/history'
  import {
    decodeConsoleState,
    encodeConsoleState,
    exampleQueries,
    insertAtCursor,
    scopeFilterSnippet
  } from '$lib/components/console/query'
  import { validateParams } from '$lib/components/console/params'
  import { timedFetch } from '$lib/api/timedFetch'
  import type { ApiEnvelope, ApiError } from '$lib/types/graph'
  import type { CypherResult, HistoryEntry } from '$lib/types/console'

  type Status = 'idle' | 'loading' | 'loaded' | 'error'

  let query = $state('MATCH (s:Service) RETURN s.name AS name ORDER BY name LIMIT 25')
  let selStart = $state(0)
  let selEnd = $state(0)

  let paramsText = $state('')
  let paramsOpen = $state(false)
  const paramsValidation = $derived(validateParams(paramsText))

  let status = $state<Status>('idle')
  let result = $state<CypherResult | null>(null)
  let warnings = $state<string[]>([])
  let errorMsg = $state<string | null>(null)
  let elapsedMs = $state(0)

  let history = $state<HistoryEntry[]>([])
  let editor: CypherEditor | undefined = $state()

  let bootstrapped = false

  const examples = $derived(exampleQueries(scope.service))
  const scopeLabel = $derived(scope.service ?? 'All services')

  // history storage handle — browser localStorage; a no-op double on the server.
  function storage(): HistoryStorage {
    if (typeof localStorage !== 'undefined') return localStorage
    return { getItem: () => null, setItem: () => {} }
  }

  onMount(() => {
    history = loadHistory(storage())
    const restored = decodeConsoleState(page.url.searchParams.get('q'))
    if (restored) {
      query = restored.query
      if (restored.paramsText) {
        paramsText = restored.paramsText
        paramsOpen = true
      }
    }
    bootstrapped = true
  })

  // Deep-link: keep the query + params in ?q= (base64url) so it's shareable.
  // A params-free state encodes to the legacy raw-query shape (back-compat).
  let urlTimer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    if (!bootstrapped) return
    const q = query
    const pt = paramsText
    clearTimeout(urlTimer)
    urlTimer = setTimeout(() => {
      const p = new URLSearchParams()
      if (q.trim().length > 0) p.set('q', encodeConsoleState({ query: q, paramsText: pt }))
      const qs = p.toString()
      const target = qs ? `/console?${qs}` : '/console'
      if (page.url.pathname + page.url.search !== target) {
        replaceState(target, {})
      }
    }, 400)
  })

  // Run is blocked while the params text is present-but-invalid — we never send
  // a malformed params object; the panel shows the reason inline.
  const canRun = $derived(
    query.trim().length > 0 && paramsValidation.valid && status !== 'loading'
  )

  async function run() {
    if (!canRun) return
    status = 'loading'
    errorMsg = null
    warnings = []
    const started = performance.now()
    const paramsAtRun = paramsText
    try {
      const reqBody: Record<string, unknown> = { query }
      if (paramsValidation.params !== null) reqBody.params = paramsValidation.params
      const res = await timedFetch('/api/cypher', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(reqBody)
      })
      const body = (await res.json()) as ApiEnvelope<CypherResult> | ApiError
      elapsedMs = Math.round(performance.now() - started)
      if (!res.ok || 'error' in body) {
        // Guardrail rejections (422) and transport failures (503) both land
        // here; render the tool's message verbatim — never swallowed.
        const err = body as ApiError
        errorMsg = err.error ?? `HTTP ${res.status}`
        result = null
        status = 'error'
        return
      }
      const env = body as ApiEnvelope<CypherResult>
      warnings = env.warnings
      result = env.data
      status = 'loaded'
      recordHistory(query, paramsAtRun)
    } catch (e) {
      elapsedMs = Math.round(performance.now() - started)
      errorMsg = e instanceof Error ? e.message : String(e)
      result = null
      status = 'error'
    }
  }

  function recordHistory(q: string, pt: string) {
    const next = pushHistory(history, q, Date.now(), pt)
    history = next
    saveHistory(storage(), next)
  }

  // ── scope-filter snippet insertion (R9) ──────────────────────
  // Callback props are stable consts (Svelte 5 hoist), not inline arrows.
  const insertScopeFilter = () => {
    // Guess the pattern variable to filter: default 'n'. We don't parse the
    // query — the user places the caret; we insert a well-formed predicate.
    const snippet = scopeFilterSnippet('n', scope.service)
    const { text, cursor } = insertAtCursor(query, selStart, selEnd, snippet)
    query = text
    editor?.focusCaret(cursor)
  }

  const useExample = (q: string) => {
    query = q
    selStart = q.length
    selEnd = q.length
    // Examples are self-contained (no $params); clear any lingering params so
    // the user doesn't unknowingly run an example against stale params.
    paramsText = ''
  }

  const recallHistory = (entry: HistoryEntry) => {
    query = entry.query
    selStart = entry.query.length
    selEnd = entry.query.length
    paramsText = entry.paramsText ?? ''
    if (entry.paramsText) paramsOpen = true
  }

  const sendToGraph = (href: string) => {
    void goto(href)
  }

  const dismissError = () => {
    errorMsg = null
  }
</script>

<svelte:head>
  <title>CodeGraph Studio — Console</title>
</svelte:head>

<div class="console">
  <div class="topbar">
    <div class="scope">
      <span class="scope-label">scope</span>
      <span class="scope-val">{scopeLabel}</span>
      <button class="scope-insert" onclick={insertScopeFilter}>insert scope filter</button>
    </div>
    <HistoryList entries={history} onPick={recallHistory} />
  </div>

  <p class="hint">
    Raw Cypher is <strong>not</strong> auto-scoped. Use “insert scope filter” or an example to
    constrain a query to <code>{scopeLabel}</code>.
  </p>

  <div class="examples">
    {#each examples as ex (ex.label)}
      <button class="example" onclick={() => useExample(ex.query)}>{ex.label}</button>
    {/each}
  </div>

  <CypherEditor
    bind:this={editor}
    bind:value={query}
    bind:selStart
    bind:selEnd
    running={status === 'loading'}
    {canRun}
    onRun={run}
  />

  <ParamsPanel bind:value={paramsText} bind:open={paramsOpen} />

  {#if warnings.length > 0}
    <ul class="warnings">
      {#each warnings as w, i (w + ':' + i)}
        <li>guardrail: {w}</li>
      {/each}
    </ul>
  {/if}

  {#if errorMsg}
    <div class="errpanel" role="alert">
      <pre class="errtext">{errorMsg}</pre>
      <button class="dismiss" onclick={dismissError}>dismiss</button>
    </div>
  {/if}

  {#if status === 'loading'}
    <div class="state">Running query…</div>
  {:else if result}
    <ResultsTable {result} {elapsedMs} onSendToGraph={sendToGraph} />
  {:else if status === 'idle'}
    <div class="state">Run a query to see results.</div>
  {/if}
</div>

<style>
  .console {
    height: 100%;
    display: flex;
    flex-direction: column;
    gap: var(--s-2);
    padding: var(--s-3);
    overflow: hidden;
  }
  .topbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex: none;
  }
  .scope {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .scope-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--ink-3);
  }
  .scope-val {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--ink);
  }
  .scope-insert {
    font-size: var(--text-xs);
    color: var(--accent);
    text-decoration: underline;
  }
  .hint {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--ink-3);
    flex: none;
  }
  .hint code {
    font-family: var(--font-mono);
    color: var(--ink-2);
  }
  .examples {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    flex: none;
  }
  .example {
    padding: 4px 10px;
    font-size: var(--text-xs);
    color: var(--ink-2);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-full);
  }
  .example:hover {
    background: var(--bg-hover);
    border-color: var(--border-strong);
    color: var(--ink);
  }
  .warnings {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex: none;
  }
  .warnings li {
    padding: 4px 10px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--warn);
    background: var(--warn-subtle);
    border: 1px solid var(--warn);
    border-radius: var(--r-md);
  }
  .errpanel {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
    padding: 8px 12px;
    background: var(--err-subtle);
    border: 1px solid var(--err);
    border-radius: var(--r-md);
    flex: none;
  }
  .errtext {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--err);
    white-space: pre-wrap;
    word-break: break-word;
    flex: 1;
  }
  .dismiss {
    font-size: var(--text-xs);
    text-decoration: underline;
    color: var(--err);
    flex: none;
  }
  .state {
    padding: var(--s-4);
    font-size: var(--text-sm);
    color: var(--ink-3);
    text-align: center;
  }
</style>
