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
      <div class="empty-icon" aria-hidden="true">
        <svg width="32" height="32" viewBox="0 0 32 32" fill="none">
          <rect x="1" y="1" width="30" height="30" rx="6" stroke="var(--border-strong)" stroke-width="1.5"/>
          <circle cx="10" cy="11" r="2" fill="var(--accent)" opacity="0.7"/>
          <circle cx="16" cy="11" r="2" fill="var(--border-strong)"/>
          <circle cx="22" cy="11" r="2" fill="var(--border-strong)"/>
          <path d="M7 18h18M7 23h12" stroke="var(--border-strong)" stroke-width="1.5" stroke-linecap="round"/>
        </svg>
      </div>
      <p class="empty-title">CODEGRAPH INTELLIGENCE</p>
      <p class="empty-sub">Ask about your indexed codebase. Search the graph, find references, explore the architecture.</p>
      <div class="prompt-list">
        <div class="prompt-chip">How does hybrid search work?</div>
        <div class="prompt-chip">Find all callers of HybridSearchManager</div>
        <div class="prompt-chip">What does the MCP server expose?</div>
      </div>
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
    animation: fade-in 240ms ease both;
  }

  .empty-icon {
    opacity: 0.6;
    margin-bottom: 4px;
  }

  .empty-title {
    font-size: 11px;
    font-weight: 600;
    color: var(--text-disabled);
    letter-spacing: 0.12em;
    text-transform: uppercase;
    font-family: var(--font-mono);
  }

  .empty-sub {
    font-size: 13px;
    color: var(--text-tertiary);
    max-width: 360px;
    line-height: 1.65;
    font-weight: 400;
  }

  .prompt-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 4px;
    width: 100%;
    max-width: 380px;
  }

  .prompt-chip {
    text-align: left;
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 9px 14px;
    font-family: var(--font-sans);
    font-size: 12.5px;
    color: var(--text-secondary);
    line-height: 1.4;
  }
</style>
