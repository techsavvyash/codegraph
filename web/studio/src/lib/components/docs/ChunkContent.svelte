<script lang="ts">
  /**
   * Docs plane right pane (RFC-012 R5): the selected chunk's content rendered as
   * sanitized markdown (marked → DOMPurify, mirroring inspector/SourcePane), and
   * below it "code this section mentions" — the chunk's MENTIONS out-links, each
   * a MentionLinkChip carrying strategy + confidence so nothing inferred reads
   * as ground truth. Clicking a link opens the code node on the canvas.
   *
   * Rendering is sequence-guarded: content changes race the async marked import,
   * so a stale render must not clobber the current chunk (SourcePane pattern).
   */
  import type { DocChunk } from '$lib/types/docs'
  import MentionLinkChip from './MentionLinkChip.svelte'

  interface Props {
    chunk: DocChunk | null
    onOpenNode: (nodeId: string) => void
  }

  let { chunk, onOpenNode }: Props = $props()

  let proseHtml = $state<string | null>(null)
  let renderSeq = 0

  $effect(() => {
    const content = chunk?.content ?? ''
    const seq = ++renderSeq
    proseHtml = null
    if (!content) return
    void renderMarkdown(content, seq)
  })

  async function renderMarkdown(source: string, seq: number) {
    const [{ marked }, { default: DOMPurify }] = await Promise.all([import('marked'), import('dompurify')])
    const raw = await marked.parse(source)
    if (seq !== renderSeq) return
    proseHtml = DOMPurify.sanitize(raw)
  }
</script>

<div class="pane">
  {#if !chunk}
    <div class="empty">
      <span class="placeholder">select a chunk</span>
    </div>
  {:else}
    <div class="pane-head">
      {#if chunk.headingPath}
        <span class="crumbs" title={chunk.headingPath}>{chunk.headingPath}</span>
      {/if}
      <span class="idx mono">chunk #{chunk.chunkIndex}</span>
    </div>

    <div class="pane-body">
      <section class="content">
        {#if proseHtml}
          <div class="md">{@html proseHtml}</div>
        {:else}
          <div class="md plain">{chunk.content}</div>
        {/if}
      </section>

      <section class="links">
        <div class="links-head">
          Code this section mentions
          <span class="cnt">{chunk.mentions.length}</span>
        </div>
        {#if chunk.mentions.length === 0}
          <p class="hint">no code links mined from this chunk</p>
        {:else}
          <div class="links-list">
            {#each chunk.mentions as link (link.nodeId + link.strategy)}
              <MentionLinkChip {link} onOpen={onOpenNode} />
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</div>

<style>
  .pane {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    height: 100%;
    background: var(--bg-panel);
  }
  .empty {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
  }
  .placeholder {
    color: var(--ink-3);
    font-size: var(--text-sm);
  }
  .pane-head {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--s-3);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .crumbs {
    font-size: 11px;
    color: var(--ink-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .idx {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-disabled);
  }
  .pane-body {
    overflow: auto;
    flex: 1;
    padding: var(--s-3);
    display: flex;
    flex-direction: column;
    gap: var(--s-4);
  }

  .links-head {
    display: flex;
    align-items: center;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-3);
    margin-bottom: var(--s-2);
  }
  .links-head .cnt {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-3);
    background: var(--bg-subtle);
    border-radius: var(--r-full);
    padding: 1px 8px;
  }
  .links-list {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .hint {
    font-size: var(--text-sm);
    color: var(--ink-disabled);
    font-style: italic;
  }

  .md {
    font-size: 13px;
    color: var(--ink-2);
    line-height: 1.6;
  }
  .md.plain {
    white-space: pre-wrap;
    word-break: break-word;
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .md :global(h1),
  .md :global(h2),
  .md :global(h3),
  .md :global(h4),
  .md :global(h5),
  .md :global(h6) {
    font-size: var(--text-md);
    font-weight: 600;
    color: var(--ink);
    margin: 12px 0 6px;
  }
  .md :global(h1:first-child),
  .md :global(h2:first-child),
  .md :global(h3:first-child),
  .md :global(h4:first-child) {
    margin-top: 0;
  }
  .md :global(p) {
    margin: 8px 0;
  }
  .md :global(p:first-child) {
    margin-top: 0;
  }
  .md :global(code) {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 0 4px;
  }
  .md :global(pre) {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 10px;
    overflow-x: auto;
  }
  .md :global(pre code) {
    border: none;
    padding: 0;
    background: transparent;
  }
  .md :global(ul),
  .md :global(ol) {
    padding-left: 20px;
    margin: 8px 0;
  }
  .md :global(a) {
    color: var(--accent);
    text-decoration: underline;
  }
  .md :global(table) {
    border-collapse: collapse;
    display: block;
    overflow-x: auto;
    max-width: 100%;
  }
  .md :global(th),
  .md :global(td) {
    border: 1px solid var(--border);
    padding: 4px 8px;
    text-align: left;
  }
  .md :global(blockquote) {
    border-left: 3px solid var(--border-strong);
    padding-left: 10px;
    margin: 8px 0;
    color: var(--ink-3);
  }
</style>
