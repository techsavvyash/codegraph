<script lang="ts">
  import type { HealthFlag } from '$lib/types/dashboard'

  interface Props {
    flags: HealthFlag[]
  }

  let { flags }: Props = $props()
</script>

<div class="secttl">Health</div>
{#if flags.length === 0}
  <div class="health empty">
    <span class="note">no health flags</span>
  </div>
{:else}
  <div class="health">
    {#each flags as flag}
      <div class="flag">
        <span class="sev {flag.severity}">{flag.severity}</span>
        <span class="txt">{flag.text}</span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .secttl {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-3);
    margin: 0 0 var(--s-2);
  }
  .health {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    box-shadow: var(--shadow-1);
    margin-bottom: var(--s-4);
  }
  .health.empty {
    padding: var(--s-3) 14px;
  }
  .note {
    font-size: var(--text-sm);
    color: var(--ink-disabled);
    font-style: italic;
  }
  .flag {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 14px;
    font-size: var(--text-sm);
    border-bottom: 1px solid var(--border);
  }
  .flag:last-child {
    border-bottom: none;
  }
  .sev {
    flex: none;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    border-radius: var(--r-full);
    padding: 1px 9px;
    width: 52px;
    text-align: center;
  }
  .sev.warn {
    background: var(--warn-subtle);
    color: var(--warn);
  }
  .sev.err {
    background: var(--err-subtle);
    color: var(--err);
  }
  .sev.ok {
    background: var(--ok-subtle);
    color: var(--ok);
  }
  .txt {
    color: var(--ink-2);
  }
</style>
