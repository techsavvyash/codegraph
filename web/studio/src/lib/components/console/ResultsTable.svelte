<script lang="ts">
  /**
   * Results table (RFC-012 R8): columns × rows. Shows row_count and elapsed
   * client time, a PROMINENT banner when the tool truncated the result, and a
   * "send to graph" affordance when a column of genuine node element ids is
   * present. Never hides truncation — silent truncation is a bug (R9).
   */
  import ResultCell from './ResultCell.svelte'
  import { collectNodeIds, graphLinkForIds } from './cells'
  import type { CypherResult } from '$lib/types/console'

  interface Props {
    result: CypherResult
    elapsedMs: number
    /** Max ids to offer for "send to graph". */
    sendLimit?: number
    onSendToGraph: (href: string, count: number) => void
  }

  let { result, elapsedMs, sendLimit = 50, onSendToGraph }: Props = $props()

  const rows = $derived(result.rows ?? [])
  const nodeIds = $derived(collectNodeIds(result.columns, rows, sendLimit))
</script>

<div class="results">
  <div class="meta">
    <span class="count">{result.row_count} {result.row_count === 1 ? 'row' : 'rows'}</span>
    <span class="dot">·</span>
    <span class="elapsed">{elapsedMs} ms</span>
    {#if nodeIds.length > 0}
      <span class="dot">·</span>
      <button
        class="send"
        onclick={() => onSendToGraph(graphLinkForIds(nodeIds), nodeIds.length)}
      >
        open {nodeIds.length} in graph →
      </button>
    {/if}
  </div>

  {#if result.truncated}
    <div class="trunc" role="alert">
      Results truncated at {result.row_count} rows — raise the row limit or add a
      LIMIT / more selective filter to see the rest.
    </div>
  {/if}

  {#if result.columns.length === 0}
    <div class="empty">query returned no columns</div>
  {:else if rows.length === 0}
    <div class="empty">query returned no rows</div>
  {:else}
    <div class="tablewrap">
      <table>
        <thead>
          <tr>
            <th class="rownum">#</th>
            {#each result.columns as col (col)}
              <th>{col}</th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each rows as row, i (i)}
            <tr>
              <td class="rownum">{i + 1}</td>
              {#each result.columns as col (col)}
                <td><ResultCell value={row[col] ?? null} /></td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<style>
  .results {
    display: flex;
    flex-direction: column;
    min-height: 0;
    flex: 1;
  }
  .meta {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: var(--s-2) 0;
    font-size: var(--text-xs);
    color: var(--ink-2);
    font-family: var(--font-mono);
  }
  .count {
    font-weight: 600;
    color: var(--ink);
  }
  .dot {
    color: var(--ink-3);
  }
  .send {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    color: var(--accent);
    text-decoration: underline;
  }
  .trunc {
    padding: 6px 12px;
    margin-bottom: var(--s-2);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--warn);
    background: var(--warn-subtle);
    border: 1px solid var(--warn);
    border-radius: var(--r-md);
  }
  .empty {
    padding: var(--s-4);
    font-size: var(--text-sm);
    color: var(--ink-3);
    text-align: center;
  }
  .tablewrap {
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    min-height: 0;
  }
  table {
    border-collapse: collapse;
    width: 100%;
    font-size: var(--text-sm);
  }
  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    text-align: left;
    padding: 6px 10px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--ink-2);
    background: var(--bg-subtle);
    border-bottom: 1px solid var(--border-strong);
    white-space: nowrap;
  }
  tbody td {
    padding: 5px 10px;
    vertical-align: top;
    border-bottom: 1px solid var(--border);
    max-width: 480px;
  }
  tbody tr:hover td {
    background: var(--bg-hover);
  }
  .rownum {
    color: var(--ink-3);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    text-align: right;
    width: 1%;
    white-space: nowrap;
    user-select: none;
  }
</style>
