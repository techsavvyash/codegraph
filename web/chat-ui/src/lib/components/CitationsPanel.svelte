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
    padding-top: 10px;
    border-top: 1px solid var(--border-light);
  }

  .toggle {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    background: none;
    border: none;
    cursor: pointer;
    color: var(--text-tertiary);
    font-family: var(--font-sans);
    font-size: 11.5px;
    font-weight: 500;
    padding: 3px 6px;
    border-radius: var(--radius-sm);
    transition: color var(--t-fast), background var(--t-fast);
    margin-left: -6px;
  }

  .toggle:hover {
    color: var(--text-secondary);
    background: var(--bg-subtle);
  }

  .toggle-label { user-select: none; }

  .chevron {
    color: var(--text-disabled);
    transition: transform var(--t-mid);
  }
  .chevron.rotated { transform: rotate(180deg); }

  .sources-list {
    margin-top: 8px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    animation: slide-in-up var(--t-mid) both;
  }

  .source-item {
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    overflow: hidden;
    background: var(--bg-page);
  }

  .source-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 10px;
    background: var(--bg-surface);
    border-bottom: 1px solid var(--border-light);
  }

  .source-badge {
    font-size: 10.5px;
    font-weight: 600;
    color: var(--accent-dark);
    letter-spacing: 0.03em;
    text-transform: uppercase;
    font-family: var(--font-sans);
  }

  .source-tool-name {
    font-size: 10.5px;
    color: var(--text-disabled);
    font-family: var(--font-mono);
  }

  .source-result {
    margin: 0;
    padding: 9px 11px;
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.55;
    color: var(--text-secondary);
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
    border: none;
    background: transparent;
  }

  .source-result code {
    background: none;
    border: none;
    padding: 0;
    color: inherit;
    font-size: inherit;
  }
</style>
