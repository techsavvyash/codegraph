<script lang="ts">
  /**
   * Segmented control selecting the active Overview lens. Same visual family as
   * the mode toggle on /graph (.modes/.modebtn) — mounted next to it, Overview
   * mode only. One lens active at a time; the parent owns lens state.
   */
  import type { LensId } from '$lib/types/overview'

  interface Props {
    lens: LensId
    onSelect: (id: LensId) => void
  }

  let { lens, onSelect }: Props = $props()

  const LENSES: Array<{ id: LensId; label: string; testid: string }> = [
    { id: 'structure', label: 'Structure', testid: 'lens-structure' },
    { id: 'flows', label: 'Flows', testid: 'lens-flows' },
    { id: 'usage', label: 'Usage', testid: 'lens-usage' },
    { id: 'hotspots', label: 'Hotspots', testid: 'lens-hotspots' },
    { id: 'dead', label: 'Dead code', testid: 'lens-dead' }
  ]
</script>

<div class="lensbar" role="tablist" aria-label="Overview lens">
  {#each LENSES as l (l.id)}
    <button
      class="lensbtn"
      class:active={lens === l.id}
      role="tab"
      aria-selected={lens === l.id}
      data-testid={l.testid}
      onclick={() => onSelect(l.id)}
    >
      {l.label}
    </button>
  {/each}
</div>

<style>
  .lensbar {
    display: flex;
    gap: 2px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 3px;
    box-shadow: var(--shadow-1);
  }
  .lensbtn {
    padding: 4px 10px;
    border-radius: var(--r-sm);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink-3);
    white-space: nowrap;
  }
  .lensbtn:hover {
    background: var(--bg-hover);
  }
  .lensbtn.active {
    background: var(--accent);
    color: #fff;
  }
</style>
