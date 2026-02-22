<script lang="ts">
  import type { ToolSource } from '$lib/types'
  import { TOOL_ACTIVITY_LABELS } from '$lib/constants'

  export let sources: ToolSource[]

  let open = false

  function label(tool: string): string {
    const raw = TOOL_ACTIVITY_LABELS[tool]
    return raw ? raw.replace(/\.\.\.$/, '') : tool.replace(/_/g, ' ')
  }

  function truncate(s: string, max = 280): string {
    return s.length > max ? s.slice(0, max) + '…' : s
  }
</script>

{#if sources.length > 0}
  <div class="citations">
    <button
      class="toggle"
      on:click={() => (open = !open)}
      aria-expanded={open}
    >
      <svg
        class="chevron"
        class:rotated={open}
        width="10"
        height="10"
        viewBox="0 0 10 10"
        fill="none"
        aria-hidden="true"
      >
        <path d="M2 3.5L5 6.5L8 3.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      <span class="toggle-label">
        {sources.length} tool {sources.length === 1 ? 'call' : 'calls'}
      </span>
    </button>

    {#if open}
      <div class="sources-list">
        {#each sources as src, i (i)}
          <div class="source-item">
            <div class="source-header">
              <span class="source-badge">{label(src.tool)}</span>
              <span class="source-tool-name">{src.tool}</span>
            </div>
            <pre class="source-result"><code>{truncate(src.result)}</code></pre>
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .citations {
    margin-top: 10px;
    border-top: 1px solid var(--border-dim);
    padding-top: 8px;
  }

  .toggle {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    background: none;
    border: none;
    cursor: pointer;
    color: var(--text-dim);
    font-family: var(--font-mono);
    font-size: 11px;
    letter-spacing: 0.04em;
    padding: 2px 4px;
    border-radius: var(--radius-sm);
    transition: color var(--t-fast), background var(--t-fast);
  }

  .toggle:hover {
    color: var(--text-soft);
    background: var(--bg-overlay);
  }

  .toggle-label { user-select: none; }

  .chevron {
    transition: transform var(--t-mid);
    color: var(--text-muted);
  }
  .chevron.rotated { transform: rotate(180deg); }

  .sources-list {
    margin-top: 8px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    animation: slide-in-up var(--t-mid) both;
  }

  .source-item {
    background: var(--bg-void);
    border: 1px solid var(--border-dim);
    border-radius: var(--radius-md);
    overflow: hidden;
  }

  .source-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 10px;
    background: var(--bg-surface);
    border-bottom: 1px solid var(--border-dim);
  }

  .source-badge {
    font-size: 10px;
    font-weight: 600;
    color: var(--amber);
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  .source-tool-name {
    font-size: 10px;
    color: var(--text-muted);
    font-style: italic;
  }

  .source-result {
    margin: 0;
    padding: 8px 10px;
    font-family: var(--font-mono);
    font-size: 10.5px;
    line-height: 1.55;
    color: var(--text-soft);
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
    border: none;
    background: transparent;
    border-left: none;
    border-radius: 0;
  }

  .source-result code {
    background: none;
    border: none;
    padding: 0;
    color: inherit;
    font-size: inherit;
  }
</style>
