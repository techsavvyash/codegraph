<script lang="ts">
  /**
   * A single results-table cell (RFC-012 R8). Scalars render inline; nodes,
   * relationships, arrays and objects render a compact summary that expands to
   * pretty-printed JSON on click. No truncation of the JSON itself — the
   * expanded block scrolls.
   */
  import { classifyCell, cellSummary, expandedJson } from './cells'
  import type { CypherValue } from '$lib/types/console'

  interface Props {
    value: CypherValue
  }
  let { value }: Props = $props()

  const kind = $derived(classifyCell(value))
  const expandable = $derived(kind === 'node' || kind === 'relationship' || kind === 'array' || kind === 'object')
  const summary = $derived(cellSummary(value))

  let open = $state(false)
</script>

{#if expandable}
  <button
    class="chip {kind}"
    onclick={() => (open = !open)}
    aria-expanded={open}
    title={open ? 'collapse' : 'expand'}
  >
    <span class="tw">{open ? '▾' : '▸'}</span>{summary}
  </button>
  {#if open}
    <pre class="json">{expandedJson(value)}</pre>
  {/if}
{:else if kind === 'null'}
  <span class="null">null</span>
{:else}
  <span class="scalar">{summary}</span>
{/if}

<style>
  .scalar {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink);
    white-space: pre-wrap;
    word-break: break-word;
  }
  .null {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink-3);
    font-style: italic;
  }
  .chip {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink-2);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 1px 6px;
    max-width: 100%;
    text-align: left;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chip:hover {
    background: var(--bg-hover);
    border-color: var(--border-strong);
  }
  .chip.node {
    color: var(--accent);
    border-color: var(--accent-border);
    background: var(--accent-subtle);
  }
  .tw {
    color: var(--ink-3);
    margin-right: 4px;
  }
  .json {
    margin: 4px 0 0;
    padding: var(--s-2);
    max-height: 260px;
    overflow: auto;
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.5;
    color: var(--ink);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    white-space: pre;
  }
</style>
