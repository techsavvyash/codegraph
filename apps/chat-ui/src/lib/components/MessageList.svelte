<script lang="ts">
  import { afterUpdate } from 'svelte'
  import type { Message } from '$lib/types'
  import ChatMessage from './ChatMessage.svelte'

  export let messages: Message[]
  export let loading: boolean

  let listEl: HTMLElement

  afterUpdate(() => {
    if (listEl) {
      listEl.scrollTo({ top: listEl.scrollHeight, behavior: 'smooth' })
    }
  })

  // Last assistant message is "streaming" while loading
  $: lastAssistantIdx = loading
    ? [...messages].reverse().findIndex(m => m.role === 'assistant')
    : -1
  $: streamingId = lastAssistantIdx >= 0
    ? messages[messages.length - 1 - lastAssistantIdx]?.id
    : null
</script>

<div class="message-list" bind:this={listEl}>
  {#if messages.length === 0}
    <div class="empty-state">
      <div class="empty-logo" aria-hidden="true">
        <svg width="36" height="36" viewBox="0 0 36 36" fill="none">
          <rect x="1" y="1" width="34" height="34" rx="4" stroke="var(--border-mid)" stroke-width="1.5"/>
          <path d="M10 12h6M10 18h16M10 24h12" stroke="var(--amber)" stroke-width="1.5" stroke-linecap="round"/>
          <circle cx="28" cy="12" r="2" fill="var(--amber)" opacity="0.7"/>
        </svg>
      </div>
      <p class="empty-title">CodeGraph Intelligence</p>
      <p class="empty-sub">Ask about your indexed codebase. I'll search the graph and cite sources.</p>
      <ul class="empty-hints">
        <li><span class="hint-prompt">→</span> How does hybrid search work?</li>
        <li><span class="hint-prompt">→</span> Find all callers of HybridSearchManager</li>
        <li><span class="hint-prompt">→</span> What does the MCP server expose?</li>
      </ul>
    </div>
  {:else}
    {#each messages as msg (msg.id)}
      <ChatMessage
        message={msg}
        streaming={loading && msg.id === streamingId}
      />
    {/each}
  {/if}
</div>

<style>
  .message-list {
    flex: 1;
    overflow-y: auto;
    padding: 0 var(--space-6) var(--space-4);
    scroll-behavior: smooth;
  }

  /* ── Empty state ── */
  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    height: 100%;
    text-align: center;
    padding: var(--space-12);
    gap: var(--space-4);
    animation: slide-in-up 300ms ease both;
  }

  .empty-logo {
    opacity: 0.5;
    margin-bottom: var(--space-2);
  }

  .empty-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text-mid);
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  .empty-sub {
    font-size: 12px;
    color: var(--text-dim);
    max-width: 340px;
    line-height: 1.6;
  }

  .empty-hints {
    list-style: none;
    margin-top: var(--space-2);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    border: 1px solid var(--border-dim);
    border-radius: var(--radius-md);
    padding: var(--space-4) var(--space-5);
    background: var(--bg-surface);
    text-align: left;
    min-width: 300px;
  }

  .empty-hints li {
    font-size: 11.5px;
    color: var(--text-soft);
    display: flex;
    gap: 8px;
    align-items: baseline;
  }

  .hint-prompt {
    color: var(--amber);
    font-weight: 700;
    flex-shrink: 0;
  }
</style>
