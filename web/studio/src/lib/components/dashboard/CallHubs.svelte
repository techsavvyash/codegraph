<script lang="ts">
  import type { CallHub } from '$lib/types/dashboard'
  import { fmtInt } from '$lib/format'

  interface Props {
    hubs: CallHub[]
  }

  let { hubs }: Props = $props()

  const maxInDegree = $derived(Math.max(1, ...hubs.map((h) => h.inDegree)))
</script>

<div class="bcard">
  <h2>Largest call hubs</h2>
  {#if hubs.length === 0}
    <div class="none">none yet</div>
  {:else}
    {#each hubs as hub}
      <div class="hub">
        <span class="nm">
          <span class="name-text">{hub.name}</span>
          {#if hub.service}
            <span class="svc">{hub.service}</span>
          {/if}
        </span>
        <span class="bar"><i style="width:{(hub.inDegree / maxInDegree) * 100}%"></i></span>
        <span class="deg">{fmtInt(hub.inDegree)}</span>
      </div>
    {/each}
  {/if}
</div>

<style>
  .bcard {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 12px 14px;
    box-shadow: var(--shadow-1);
  }
  h2 {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-3);
    margin-bottom: 8px;
  }
  .none {
    font-size: var(--text-sm);
    color: var(--ink-disabled);
    font-style: italic;
  }
  .hub {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 4px 0;
    font-size: var(--text-sm);
  }
  .nm {
    display: flex;
    flex-direction: column;
    width: 190px;
    flex: none;
    overflow: hidden;
  }
  .name-text {
    font-family: var(--font-mono);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .svc {
    font-size: 10px;
    color: var(--ink-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .bar {
    flex: 1;
    height: 6px;
    border-radius: var(--r-full);
    background: var(--bg-subtle);
    overflow: hidden;
  }
  .bar i {
    display: block;
    height: 100%;
    background: var(--node-function);
    border-radius: var(--r-full);
  }
  .deg {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink-3);
    width: 34px;
    text-align: right;
    flex: none;
  }
</style>
