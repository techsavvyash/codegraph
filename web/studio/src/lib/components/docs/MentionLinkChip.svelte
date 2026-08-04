<script lang="ts">
  /**
   * A single doc→code link, presented so nothing inferred reads as ground truth
   * (RFC-011 / RFC-012 R5): the target node (colored by label) plus the raw
   * strategy string and confidence, banded by the edge-provenance grammar
   * (docmine = amber/high-trust, semlink = purple/lower). Clicking navigates to
   * the node on the canvas. Provenance styling matches the canvas edge grammar
   * (--edge-docmine / --edge-semlink) so a link means the same thing everywhere.
   */
  import type { MentionLink } from '$lib/types/docs'
  import { nodeColors } from '$lib/components/canvas/elements'

  interface Props {
    link: MentionLink
    onOpen: (nodeId: string) => void
  }

  let { link, onOpen }: Props = $props()

  const colors = $derived(nodeColors(link.label ?? ''))
  const confPct = $derived(Math.round(link.confidence * 100))
</script>

<button
  class="chip {link.family}"
  onclick={() => onOpen(link.nodeId)}
  title="{link.strategy} · confidence {link.confidence.toFixed(2)} — open {link.name ?? 'node'} on canvas"
>
  <span class="dot" style="background:{colors.fg}"></span>
  <span class="name mono">{link.name ?? '(unnamed)'}</span>
  {#if link.label}
    <span class="lbl">{link.label}</span>
  {/if}
  <span class="prov" class:high={link.band === 'high'} class:medium={link.band === 'medium'} class:low={link.band === 'low'}>
    <span class="strategy mono">{link.strategy}</span>
    <span class="conf mono">{confPct}%</span>
  </span>
</button>

<style>
  .chip {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    text-align: left;
    padding: 5px 8px;
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    background: var(--bg-panel);
    cursor: pointer;
    font-size: var(--text-sm);
  }
  .chip:hover {
    background: var(--bg-subtle);
    border-color: var(--border-strong);
  }
  .chip.docmine {
    border-left: 3px solid var(--edge-docmine);
  }
  .chip.semlink {
    border-left: 3px solid var(--edge-semlink);
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: var(--r-full);
    flex: none;
  }
  .name {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
    color: var(--ink);
  }
  .lbl {
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--ink-3);
    flex: none;
  }
  .prov {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }
  .strategy {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-disabled);
    white-space: nowrap;
  }
  .conf {
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 600;
    border-radius: var(--r-full);
    padding: 0 6px;
  }
  .prov.high .conf {
    background: var(--edge-docmine-bg);
    color: var(--edge-docmine);
  }
  .prov.medium .conf {
    background: var(--edge-semlink-bg);
    color: var(--edge-semlink);
  }
  .prov.low .conf {
    background: var(--bg-subtle);
    color: var(--ink-3);
  }
</style>
