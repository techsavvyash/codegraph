<script lang="ts">
  /**
   * Docs plane left rail (RFC-012 R5): a search box over the whole docs corpus,
   * plus the document list grouped by owning service. When a search is active
   * the groups are replaced by ranked hits; clearing the box restores the list.
   * All list keys are genuinely unique elementIds (Svelte 5 duplicate-key
   * crash hazard) — documents can't repeat within the graph.
   */
  import type { DocGroup, DocSearchHit } from '$lib/types/docs'

  // 'idle' renders like 'loading': the page fires its first load on mount,
  // so idle is only ever a pre-mount flicker.
  type Status = 'idle' | 'loading' | 'loaded' | 'error'

  interface Props {
    groups: DocGroup[]
    status: Status
    error: string
    selectedDocId: string | null
    searchQuery: string
    searchStatus: 'idle' | 'loading' | 'loaded' | 'error'
    searchHits: DocSearchHit[]
    searchFallback: boolean
    searchError: string
    totalCount: number
    onSelect: (docId: string) => void
    onSearchInput: (value: string) => void
  }

  let {
    groups,
    status,
    error,
    selectedDocId,
    searchQuery,
    searchStatus,
    searchHits,
    searchFallback,
    searchError,
    totalCount,
    onSelect,
    onSearchInput
  }: Props = $props()

  const searching = $derived(searchQuery.trim().length > 0)

  function onInput(ev: Event) {
    onSearchInput((ev.target as HTMLInputElement).value)
  }
</script>

<div class="rail">
  <div class="search">
    <input
      type="search"
      placeholder="Search docs…"
      value={searchQuery}
      oninput={onInput}
      aria-label="Search documents"
    />
  </div>

  <div class="rail-body">
    {#if searching}
      {#if searchStatus === 'loading'}
        <p class="hint">searching…</p>
      {:else if searchStatus === 'error'}
        <p class="hint err">{searchError}</p>
      {:else if searchHits.length === 0}
        <p class="hint">no documents match “{searchQuery.trim()}”</p>
      {:else}
        {#if searchFallback}
          <p class="fallback">fulltext index unavailable — substring match</p>
        {/if}
        <div class="group-head">
          <span class="gname">Results</span>
          <span class="gk">{searchHits.length}</span>
        </div>
        {#each searchHits as hit (hit.nodeId)}
          <button class="doc" class:sel={hit.nodeId === selectedDocId} onclick={() => onSelect(hit.nodeId)}>
            <span class="title">{hit.title}</span>
            <span class="meta">
              <span class="svc">{hit.service ?? '(unassigned)'}</span>
              <span class="matched">{hit.matchedIn}{hit.score !== null ? ` · ${hit.score.toFixed(1)}` : ''}</span>
            </span>
          </button>
        {/each}
      {/if}
    {:else if status === 'loading' || status === 'idle'}
      <p class="hint">loading documents…</p>
    {:else if status === 'error'}
      <p class="hint err">{error}</p>
    {:else if totalCount === 0}
      <p class="hint">no documents in this scope</p>
    {:else}
      {#each groups as group (group.service)}
        <div class="group-head">
          <span class="gname">{group.service}</span>
          <span class="gk">{group.documents.length}</span>
        </div>
        {#each group.documents as doc (doc.nodeId)}
          <button class="doc" class:sel={doc.nodeId === selectedDocId} onclick={() => onSelect(doc.nodeId)}>
            <span class="title">{doc.title}</span>
            <span class="meta">
              {#if doc.filePath}<span class="path mono">{doc.filePath}</span>{/if}
              <span class="chunks">{doc.chunkCount} chunk{doc.chunkCount === 1 ? '' : 's'}</span>
            </span>
          </button>
        {/each}
      {/each}
    {/if}
  </div>
</div>

<style>
  .rail {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    height: 100%;
    background: var(--bg-panel);
    border-right: 1px solid var(--border);
  }
  .search {
    padding: var(--s-2) var(--s-3);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .search input {
    width: 100%;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 6px 10px;
    font-size: var(--text-sm);
    color: var(--ink);
  }
  .rail-body {
    overflow: auto;
    flex: 1;
    padding-bottom: var(--s-3);
  }

  .hint {
    padding: var(--s-3);
    font-size: var(--text-sm);
    color: var(--ink-3);
  }
  .hint.err {
    color: var(--err);
  }
  .fallback {
    margin: var(--s-2) var(--s-3) 0;
    padding: 4px 8px;
    font-size: 10px;
    color: var(--warn);
    background: var(--warn-subtle);
    border: 1px solid var(--warn);
    border-radius: var(--r-sm);
  }

  .group-head {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: var(--s-3) var(--s-3) var(--s-1);
  }
  .gname {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--ink-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .gk {
    margin-left: auto;
    font-size: 10px;
    color: var(--ink-disabled);
    font-family: var(--font-mono);
  }

  .doc {
    display: flex;
    flex-direction: column;
    gap: 2px;
    width: 100%;
    text-align: left;
    padding: 6px var(--s-3);
    cursor: pointer;
  }
  .doc:hover {
    background: var(--bg-subtle);
  }
  .doc.sel {
    background: var(--accent-subtle);
  }
  .doc .title {
    font-size: var(--text-sm);
    color: var(--ink);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .doc.sel .title {
    color: var(--accent-ink);
    font-weight: 500;
  }
  .doc .meta {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 10px;
    color: var(--ink-disabled);
    min-width: 0;
  }
  .doc .path {
    font-family: var(--font-mono);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }
  .doc .chunks,
  .doc .matched {
    margin-left: auto;
    flex: none;
    white-space: nowrap;
  }
  .doc .svc {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
