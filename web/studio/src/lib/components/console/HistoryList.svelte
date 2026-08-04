<script lang="ts">
  /**
   * Query history dropdown (RFC-012 R8). Recalls a past query into the editor.
   * The parent owns the entries list (localStorage-backed via history.ts);
   * this component is purely presentational.
   */
  import type { HistoryEntry } from '$lib/types/console'

  interface Props {
    entries: HistoryEntry[]
    onPick: (entry: HistoryEntry) => void
  }
  let { entries, onPick }: Props = $props()

  let open = $state(false)

  function pick(entry: HistoryEntry) {
    onPick(entry)
    open = false
  }

  // One line preview so multi-line queries stay compact in the list.
  function preview(q: string): string {
    const firstLine = q.split('\n')[0].trim()
    return firstLine.length > 80 ? firstLine.slice(0, 79) + '…' : firstLine
  }
</script>

<div class="history">
  <button class="toggle" onclick={() => (open = !open)} disabled={entries.length === 0}>
    History ({entries.length})
  </button>
  {#if open && entries.length > 0}
    <ul class="menu">
      {#each entries as entry, i (entry.query + ':' + i)}
        <li>
          <button class="item" onclick={() => pick(entry)} title={entry.query}>
            <span class="q">{preview(entry.query)}</span>
            {#if entry.paramsText}
              <span class="pbadge" title={entry.paramsText}>params</span>
            {/if}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .history {
    position: relative;
  }
  .toggle {
    padding: 5px 12px;
    font-size: var(--text-sm);
    color: var(--ink-2);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
  }
  .toggle:hover:not(:disabled) {
    background: var(--bg-hover);
    border-color: var(--border-strong);
  }
  .toggle:disabled {
    color: var(--ink-disabled);
    cursor: not-allowed;
  }
  .menu {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    z-index: 10;
    width: 460px;
    max-width: 70vw;
    max-height: 340px;
    overflow: auto;
    list-style: none;
    margin: 0;
    padding: 4px;
    background: var(--bg-panel);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-md);
    box-shadow: var(--shadow-1);
  }
  .item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    text-align: left;
    padding: 6px 8px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink);
    border-radius: var(--r-sm);
  }
  .item:hover {
    background: var(--bg-hover);
  }
  .q {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .pbadge {
    flex: none;
    padding: 0 5px;
    font-size: 10px;
    color: var(--accent);
    background: var(--accent-subtle);
    border-radius: var(--r-full);
  }
</style>
