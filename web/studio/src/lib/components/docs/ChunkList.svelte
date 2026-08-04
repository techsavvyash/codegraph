<script lang="ts">
  /**
   * Docs plane middle pane (RFC-012 R5): the chunks of the selected document,
   * ordered, each showing its heading path (the leaf segment emphasised) and
   * how many code links it carries. Selecting a chunk drives the right pane.
   */
  import type { DocChunk, DocSummary } from '$lib/types/docs'

  type Status = 'idle' | 'loading' | 'loaded' | 'error'

  interface Props {
    document: DocSummary | null
    chunks: DocChunk[]
    status: Status
    error: string
    selectedChunkId: string | null
    onSelect: (chunkId: string) => void
  }

  let { document, chunks, status, error, selectedChunkId, onSelect }: Props = $props()

  /** Last segment of "A > B > C" — the chunk's own heading. */
  function leafHeading(headingPath: string | null): string {
    if (!headingPath) return '(no heading)'
    const parts = headingPath.split(' > ')
    return parts[parts.length - 1] || headingPath
  }
</script>

<div class="pane">
  <div class="pane-head">
    {#if document}
      <span class="doc-title">{document.title}</span>
      {#if document.filePath}<span class="doc-path mono">{document.filePath}</span>{/if}
    {:else}
      <span class="doc-title muted">No document selected</span>
    {/if}
  </div>

  <div class="pane-body">
    {#if status === 'idle'}
      <p class="hint">select a document to see its chunks</p>
    {:else if status === 'loading'}
      <p class="hint">loading chunks…</p>
    {:else if status === 'error'}
      <p class="hint err">{error}</p>
    {:else if chunks.length === 0}
      <p class="hint">this document has no chunks</p>
    {:else}
      {#each chunks as chunk (chunk.nodeId)}
        <button class="chunk" class:sel={chunk.nodeId === selectedChunkId} onclick={() => onSelect(chunk.nodeId)}>
          <span class="idx mono">#{chunk.chunkIndex}</span>
          <span class="heading">{leafHeading(chunk.headingPath)}</span>
          {#if chunk.mentions.length > 0}
            <span class="links">{chunk.mentions.length} link{chunk.mentions.length === 1 ? '' : 's'}</span>
          {/if}
        </button>
      {/each}
    {/if}
  </div>
</div>

<style>
  .pane {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    height: 100%;
    background: var(--bg-page);
    border-right: 1px solid var(--border);
  }
  .pane-head {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--s-3);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .doc-title {
    font-size: var(--text-md);
    font-weight: 600;
    color: var(--ink);
  }
  .doc-title.muted {
    color: var(--ink-3);
    font-weight: 400;
  }
  .doc-path {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-disabled);
  }
  .pane-body {
    overflow: auto;
    flex: 1;
    padding: var(--s-2) 0;
  }

  .hint {
    padding: var(--s-3);
    font-size: var(--text-sm);
    color: var(--ink-3);
  }
  .hint.err {
    color: var(--err);
  }

  .chunk {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    text-align: left;
    padding: 6px var(--s-3);
    cursor: pointer;
    font-size: var(--text-sm);
  }
  .chunk:hover {
    background: var(--bg-subtle);
  }
  .chunk.sel {
    background: var(--accent-subtle);
  }
  .idx {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-disabled);
    flex: none;
  }
  .heading {
    color: var(--ink-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
  }
  .chunk.sel .heading {
    color: var(--accent-ink);
    font-weight: 500;
  }
  .links {
    margin-left: auto;
    flex: none;
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--edge-docmine);
    background: var(--edge-docmine-bg);
    border-radius: var(--r-full);
    padding: 0 7px;
  }
</style>
