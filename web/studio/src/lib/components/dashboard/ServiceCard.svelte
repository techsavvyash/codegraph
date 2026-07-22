<script lang="ts">
  import type { ServiceCard } from '$lib/types/dashboard'
  import { fmtInt, pctSplit } from '$lib/format'

  interface Props {
    service: ServiceCard
  }

  let { service }: Props = $props()

  const functions = $derived(service.nodesByLabel['Function'] ?? 0)
  const methods = $derived(service.nodesByLabel['Method'] ?? 0)
  const files = $derived(service.nodesByLabel['File'] ?? 0)
  const hasCode = $derived(functions > 0 || methods > 0 || files > 0)

  const docLinkTotal = $derived(service.docLinks.docmine + service.docLinks.semlink)
  const split = $derived(pctSplit(service.docLinks.docmine, service.docLinks.semlink))
</script>

<div class="scard">
  <div class="head">
    <span class="dot"></span>
    <span class="nm">{service.name}</span>
    <span class="scope mono">{service.scopeId}</span>
  </div>

  {#if hasCode}
    <div class="stat">
      {#if functions > 0}Functions <b>{fmtInt(functions)}</b>{/if}
      {#if functions > 0 && methods > 0} &middot; {/if}
      {#if methods > 0}Methods <b>{fmtInt(methods)}</b>{/if}
      {#if (functions > 0 || methods > 0) && files > 0} &middot; {/if}
      {#if files > 0}Files <b>{fmtInt(files)}</b>{/if}
    </div>
  {/if}

  {#if service.docs > 0 || service.chunks > 0 || service.calls > 0 || service.apiRoutes > 0 || service.flows > 0}
    <div class="stat">
      {#if service.docs > 0}Docs <b>{fmtInt(service.docs)}</b>{/if}
      {#if service.docs > 0 && service.chunks > 0} &middot; {/if}
      {#if service.chunks > 0}Chunks <b>{fmtInt(service.chunks)}</b>{/if}
      {#if (service.docs > 0 || service.chunks > 0) && service.calls > 0} &middot; {/if}
      {#if service.calls > 0}CALLS <b>{fmtInt(service.calls)}</b>{/if}
      {#if service.apiRoutes > 0}
        {#if service.docs > 0 || service.chunks > 0 || service.calls > 0} &middot; {/if}
        APIs <b>{fmtInt(service.apiRoutes)}</b>
      {/if}
      {#if service.flows > 0}
        {#if service.docs > 0 || service.chunks > 0 || service.calls > 0 || service.apiRoutes > 0} &middot; {/if}
        Flows <b>{fmtInt(service.flows)}</b>
      {/if}
    </div>
  {/if}

  {#if hasCode && service.docs === 0}
    <div class="note">no docs indexed</div>
  {:else if service.docs > 0 && !hasCode}
    <div class="note">code not indexed</div>
  {/if}

  {#if docLinkTotal > 0}
    <div class="covbar">
      <span class="dm" style="width:{split.a}%"></span>
      <span class="sl" style="width:{split.b}%"></span>
    </div>
    <div class="covleg">
      <span class="li"><span class="sw dm-sw"></span>docmine {fmtInt(service.docLinks.docmine)}</span>
      <span class="li"><span class="sw sl-sw"></span>semlink {fmtInt(service.docLinks.semlink)}</span>
    </div>
  {/if}

  <div class="foot mono">
    {#if service.semantic}
      {service.semantic.embeddingModel ?? 'unknown model'} &middot; {service.semantic.dims ?? '?'}d &middot; threshold
      {service.semantic.semlinkThreshold ?? '?'}
    {:else}
      {service.language ?? 'unknown'} &middot; scope: {service.scopeId}
    {/if}
  </div>
</div>

<style>
  .scard {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 12px 14px;
    box-shadow: var(--shadow-1);
    display: flex;
    flex-direction: column;
    gap: 7px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .head .dot {
    width: 8px;
    height: 8px;
    border-radius: var(--r-full);
    background: var(--node-service);
    flex: none;
  }
  .head .nm {
    font-weight: 600;
    font-size: var(--text-base);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .scope {
    margin-left: auto;
    flex: none;
    font-size: 10px;
    color: var(--ink-3);
    background: var(--bg-subtle);
    border-radius: var(--r-full);
    padding: 1px 8px;
  }
  .stat {
    font-size: var(--text-sm);
    color: var(--ink-2);
  }
  .stat :global(b) {
    font-family: var(--font-mono);
    font-weight: 500;
    color: var(--ink);
  }
  .note {
    font-size: var(--text-xs);
    color: var(--ink-disabled);
    font-style: italic;
  }
  .covbar {
    display: flex;
    height: 8px;
    border-radius: var(--r-full);
    overflow: hidden;
    background: var(--bg-subtle);
  }
  .covbar .dm {
    background: var(--edge-docmine);
  }
  .covbar .sl {
    background: var(--edge-semlink);
  }
  .covleg {
    display: flex;
    gap: 12px;
    font-size: 10px;
    color: var(--ink-3);
  }
  .covleg .li {
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .sw {
    width: 8px;
    height: 8px;
    border-radius: 2px;
  }
  .dm-sw {
    background: var(--edge-docmine);
  }
  .sl-sw {
    background: var(--edge-semlink);
  }
  .foot {
    margin-top: auto;
    padding-top: 7px;
    border-top: 1px solid var(--border);
    font-size: 10px;
    color: var(--ink-3);
  }
</style>
