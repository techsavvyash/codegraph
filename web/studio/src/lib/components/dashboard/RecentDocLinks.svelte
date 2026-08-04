<script lang="ts">
  import type { RecentDocLink } from '$lib/types/dashboard'
  import { fmtConfidence, relTime } from '$lib/format'

  interface Props {
    links: RecentDocLink[]
  }

  let { links }: Props = $props()
</script>

<div class="bcard">
  <h2>Recently linked docs</h2>
  {#if links.length === 0}
    <div class="none">none yet</div>
  {:else}
    {#each links as link}
      <div class="dlink">
        <span class="dot"></span>
        <span class="path">{link.docPath} &rsaquo; {link.headingPath}</span>
        {#if link.createdAt}
          <span class="when">{relTime(link.createdAt)}</span>
        {/if}
        <span class="badge {link.family === 'docmine' ? 'dm' : 'sl'}"
          >{link.family} {fmtConfidence(link.confidence)}</span
        >
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
  .dlink {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 0;
    font-size: var(--text-sm);
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: var(--r-full);
    background: var(--node-chunk);
    flex: none;
  }
  .path {
    font-family: var(--font-mono);
    color: var(--ink-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .when {
    margin-left: auto;
    flex: none;
    font-size: 10px;
    color: var(--ink-disabled);
  }
  .badge {
    flex: none;
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 500;
    padding: 1px 8px;
    border-radius: var(--r-full);
  }
  .badge.dm {
    background: var(--edge-docmine-bg);
    color: var(--edge-docmine);
  }
  .badge.sl {
    background: var(--edge-semlink-bg);
    color: var(--edge-semlink);
  }
</style>
