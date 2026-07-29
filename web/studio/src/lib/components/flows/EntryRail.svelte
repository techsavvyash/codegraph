<script lang="ts">
  /**
   * Entry-point rail (RFC-012 R4): tiered list of candidate flow starting
   * points. Design: design/studio/screens/flows.html .rail. Tiers are fixed
   * (1-4); we only ever omit an empty one, never reorder or relabel.
   */
  import type { EntryPoint } from '$lib/types/flows'
  import { nodeColors } from '$lib/components/canvas/elements'

  interface Props {
    entries: EntryPoint[]
    selectedId: string | null
    status: 'loading' | 'loaded' | 'error'
    error: string
    onSelect: (entry: EntryPoint) => void
  }

  let { entries, selectedId, status, error, onSelect }: Props = $props()

  const TIER_NAMES: Record<1 | 2 | 3 | 4, string> = {
    1: 'API-exposed',
    2: 'Iface impls, no callers',
    3: 'Exported roots',
    4: 'High centrality'
  }
  const TIER_ORDER = [1, 2, 3, 4] as const

  const grouped = $derived.by(() => {
    const byTier = new Map<number, EntryPoint[]>()
    for (const e of entries) {
      const list = byTier.get(e.tier) ?? []
      list.push(e)
      byTier.set(e.tier, list)
    }
    return TIER_ORDER.map((tier) => ({ tier, entries: byTier.get(tier) ?? [] })).filter(
      (g) => g.entries.length > 0
    )
  })

  function ioLabel(e: EntryPoint): string {
    return e.tier === 4 ? `in ${e.in_degree ?? 0}` : `out ${e.out_degree ?? 0}`
  }

  function selectEntry(e: EntryPoint) {
    onSelect(e)
  }
</script>

<div class="rail">
  <div class="rail-head">
    Entry points
    <span class="cnt">{entries.length}</span>
  </div>
  <div class="rail-body">
    {#if status === 'loading'}
      <p class="hint">loading entry points…</p>
    {:else if status === 'error'}
      <p class="hint err">{error}</p>
    {:else if entries.length === 0}
      <p class="hint">no entry points found for this service</p>
    {:else}
      {#each grouped as group (group.tier)}
        <div class="tier">
          <span class="tbadge t{group.tier}">T{group.tier}</span>
          <span class="tname">{TIER_NAMES[group.tier as 1 | 2 | 3 | 4]}</span>
          {#if group.tier === 2}
            <span
              class="info"
              title="Decorator- or interface-invoked at runtime; the static call graph sees no callers"
              >(?)</span
            >
          {/if}
          <span class="tk">{group.entries.length}</span>
        </div>
        {#each group.entries as entry (entry.node_key)}
          {@const colors = nodeColors(entry.label)}
          <button
            class="ep"
            class:sel={entry.node_id === selectedId}
            onclick={() => selectEntry(entry)}
          >
            <span class="dot" style="background:{colors.fg}"></span>
            <span class="nm mono">{entry.name}</span>
            <span class="io">{ioLabel(entry)}</span>
          </button>
        {/each}
      {/each}
    {/if}
  </div>
</div>

<style>
  .rail {
    background: var(--bg-panel);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    height: 100%;
  }
  .rail-head {
    display: flex;
    align-items: center;
    padding: var(--s-3) var(--s-3) var(--s-2);
    border-bottom: 1px solid var(--border);
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-3);
  }
  .rail-head .cnt {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-3);
    background: var(--bg-subtle);
    border-radius: var(--r-full);
    padding: 1px 8px;
  }
  .rail-body {
    overflow: auto;
    flex: 1;
    padding-bottom: var(--s-3);
  }

  .hint {
    padding: var(--s-3);
    font-size: var(--text-sm);
    color: var(--ink-3);
  }
  .hint.err {
    color: var(--err);
  }

  .tier {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: var(--s-3) var(--s-3) var(--s-1);
  }
  .tbadge {
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 500;
    border-radius: var(--r-full);
    padding: 1px 8px;
    flex: none;
  }
  .tbadge.t1 {
    background: var(--accent);
    color: #fff;
  }
  .tbadge.t2 {
    background: var(--bg-panel);
    color: var(--accent-ink);
    border: 1px solid var(--accent-border);
  }
  .tbadge.t3,
  .tbadge.t4 {
    background: var(--bg-subtle);
    color: var(--ink-3);
    border: 1px solid var(--border);
  }
  .tier .tname {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--ink-3);
  }
  .tier .tk {
    margin-left: auto;
    font-size: 10px;
    color: var(--ink-disabled);
  }
  .tier .info {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-disabled);
    border: 1px solid var(--border);
    border-radius: var(--r-full);
    padding: 0 4px;
    cursor: help;
  }

  .ep {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 5px var(--s-3) 5px 22px;
    font-size: var(--text-sm);
    cursor: pointer;
    width: 100%;
    text-align: left;
  }
  .ep:hover {
    background: var(--bg-subtle);
  }
  .ep.sel {
    background: var(--accent-subtle);
  }
  .ep .dot {
    width: 7px;
    height: 7px;
    border-radius: var(--r-full);
    flex: none;
  }
  .ep .nm {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
  }
  .ep.sel .nm {
    color: var(--accent-ink);
    font-weight: 500;
  }
  .ep .io {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-disabled);
    flex: none;
  }
</style>
