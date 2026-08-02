<script lang="ts">
  /**
   * One chat message. User messages render as escaped plain text; assistant
   * messages render sanitized markdown (renderMarkdown → marked + DOMPurify),
   * plus their tool-activity rows and citation chips. Clicking a citation
   * navigates to /graph?nodes=<id> — only chips with a real node id exist, so
   * the deep-link always resolves.
   */
  import { goto } from '$app/navigation'
  import type { ChatMessage } from '$lib/types/chat'
  import { renderMarkdown } from './markdown'
  import ToolActivityEntry from './ToolActivityEntry.svelte'

  let { message, streaming = false }: { message: ChatMessage; streaming?: boolean } = $props()

  const isUser = $derived(message.role === 'user')
  const html = $derived(isUser ? '' : renderMarkdown(message.content))
  const tools = $derived(message.tools ?? [])
  const citations = $derived(message.citations ?? [])

  function openCitation(nodeId: string) {
    goto(`/graph?nodes=${encodeURIComponent(nodeId)}&sel=${encodeURIComponent(nodeId)}`)
  }

  function pinAll() {
    if (citations.length === 0) return
    const ids = citations.map((c) => c.nodeId)
    goto(`/graph?nodes=${ids.map(encodeURIComponent).join(',')}`)
  }
</script>

<div class="msg" class:user={isUser}>
  <span class="who" aria-hidden="true">{isUser ? 'you' : 'cg'}</span>
  <div class="body">
    {#if isUser}
      <div class="bubble user-bubble">{message.content}</div>
    {:else}
      {#if tools.length > 0}
        <div class="tools">
          {#each tools as t, i (i)}
            <ToolActivityEntry record={t} />
          {/each}
        </div>
      {/if}

      <div class="prose">
        <!-- html is sanitized by renderMarkdown (DOMPurify) -->
        {@html html}
        {#if streaming}<span class="cursor" aria-hidden="true"></span>{/if}
      </div>

      {#if citations.length > 0}
        <div class="citations">
          {#each citations as c (c.nodeId)}
            <button class="chip" title={`${c.kind ?? 'node'} · ${c.tool}`} onclick={() => openCitation(c.nodeId)}>
              {#if c.kind}<span class="kind">{c.kind}</span>{/if}
              <span class="cname">{c.name}</span>
            </button>
          {/each}
          {#if citations.length > 1}
            <button class="chip pin-all" onclick={pinAll}>Pin all ({citations.length})</button>
          {/if}
        </div>
      {/if}
    {/if}
  </div>
</div>

<style>
  .msg {
    display: flex;
    gap: 8px;
    padding: 10px 0;
  }
  .msg.user { flex-direction: row-reverse; }

  .who {
    flex-shrink: 0;
    width: 22px;
    height: 22px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--r-full);
    font-size: 8px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-family: var(--font-sans);
    margin-top: 2px;
  }
  .msg.user .who { background: var(--accent); color: #fff; }
  .msg:not(.user) .who { background: var(--bg-hover); color: var(--ink-3); border: 1px solid var(--border); }

  .body { min-width: 0; max-width: 85%; flex: 1; }
  .msg.user .body { display: flex; flex-direction: column; align-items: flex-end; }

  .bubble.user-bubble {
    background: var(--accent);
    color: #fff;
    padding: 7px 11px;
    border-radius: var(--r-lg) var(--r-lg) var(--r-sm) var(--r-lg);
    font-size: var(--text-sm);
    line-height: 1.6;
    white-space: pre-wrap;
    word-break: break-word;
    max-width: 100%;
  }

  .tools {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 7px;
  }

  .prose {
    font-family: var(--font-sans);
    font-size: var(--text-sm);
    line-height: 1.65;
    color: var(--ink);
    word-break: break-word;
  }

  .cursor {
    display: inline-block;
    width: 1.5px;
    height: 1em;
    background: var(--ink-3);
    vertical-align: text-bottom;
    margin-left: 2px;
    animation: cd-blink 0.9s step-end infinite;
  }
  @keyframes cd-blink { 50% { opacity: 0; } }

  .citations {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    margin-top: 9px;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    max-width: 100%;
    padding: 3px 9px;
    background: var(--bg-panel);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-full);
    font-family: var(--font-sans);
    font-size: 11px;
    color: var(--ink-2);
    cursor: pointer;
    transition: border-color 120ms, color 120ms, background 120ms;
  }
  .chip:hover { border-color: var(--accent); color: var(--accent-ink); background: var(--accent-subtle); }
  .chip .kind {
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    color: var(--ink-3);
  }
  .chip .cname {
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .pin-all { font-weight: 600; color: var(--accent-ink); }

  /* markdown output (@html) */
  .prose :global(p) { margin: 0 0 0.55em; }
  .prose :global(p:last-child) { margin-bottom: 0; }
  .prose :global(h1), .prose :global(h2), .prose :global(h3), .prose :global(h4) {
    font-weight: 600; margin: 0.8em 0 0.3em; color: var(--ink); line-height: 1.3;
  }
  .prose :global(h1) { font-size: 1.12em; }
  .prose :global(h2) { font-size: 1.05em; }
  .prose :global(h3), .prose :global(h4) { font-size: 1em; }
  .prose :global(strong) { font-weight: 600; }
  .prose :global(em) { font-style: italic; }
  .prose :global(a) { color: var(--accent); text-decoration: underline; }
  .prose :global(code) {
    font-family: var(--font-mono);
    font-size: 0.85em;
    background: var(--bg-subtle);
    padding: 1px 4px;
    border-radius: var(--r-sm);
    border: 1px solid var(--border);
  }
  .prose :global(pre) {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 9px 11px;
    overflow-x: auto;
    margin: 0.6em 0;
    font-size: 0.82em;
    line-height: 1.5;
  }
  .prose :global(pre code) { background: none; border: none; padding: 0; }
  .prose :global(ul), .prose :global(ol) { margin: 0.4em 0 0.4em 1.3em; }
  .prose :global(li) { margin-bottom: 0.15em; }
  .prose :global(blockquote) {
    border-left: 2px solid var(--border-strong);
    padding-left: 10px;
    color: var(--ink-2);
    margin: 0.5em 0;
  }
  .prose :global(hr) { border: none; border-top: 1px solid var(--border); margin: 0.7em 0; }
  .prose :global(table) { border-collapse: collapse; font-size: 0.9em; margin: 0.5em 0; display: block; overflow-x: auto; }
  .prose :global(th), .prose :global(td) { border: 1px solid var(--border); padding: 3px 8px; text-align: left; }
</style>
