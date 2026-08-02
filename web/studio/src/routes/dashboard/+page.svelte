<script lang="ts">
  import KpiStrip from '$lib/components/dashboard/KpiStrip.svelte'
  import ServiceCard from '$lib/components/dashboard/ServiceCard.svelte'
  import HealthFlags from '$lib/components/dashboard/HealthFlags.svelte'
  import CallHubs from '$lib/components/dashboard/CallHubs.svelte'
  import RecentDocLinks from '$lib/components/dashboard/RecentDocLinks.svelte'
  import Skeleton from '$lib/components/dashboard/Skeleton.svelte'
  import ErrorBanner from '$lib/components/dashboard/ErrorBanner.svelte'
  import { scope } from '$lib/stores/scope.svelte'
  import { filterServiceCards, filterHealthFlags } from '$lib/dashboard/scopeFilter'

  let { data } = $props()

  function formatTime(iso: string): string {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return iso
    return d.toTimeString().slice(0, 8)
  }

  function retry() {
    location.reload()
  }
</script>

<svelte:head>
  <title>CodeGraph Studio</title>
</svelte:head>

<div class="pane">
  {#await data.dashboard}
    <Skeleton />
  {:then dash}
    <div class="phead">
      <h1>Index overview</h1>
      <div class="meta">
        <span class="okd"></span>{dash.neo4jTarget} &middot; connected
      </div>
    </div>
    <div class="snapshot">snapshot {formatTime(dash.generatedAt)}</div>

    {#if dash.warnings.length > 0}
      <div class="warnbanner">
        {#each dash.warnings as warning}
          <div class="wline mono">guardrail: {warning}</div>
        {/each}
      </div>
    {/if}

    {@const knownServices = dash.services.map((s) => s.name)}
    {@const scopedService =
      scope.service && knownServices.includes(scope.service) ? scope.service : null}
    {@const shownServices = filterServiceCards(dash.services, scopedService)}
    {@const shownHealth = filterHealthFlags(dash.health, scopedService, knownServices)}

    <KpiStrip totals={dash.totals} />
    {#if scopedService}
      <div class="scopenote">
        filtered to <b>{scopedService}</b> — showing graph-wide totals
      </div>
    {/if}

    <div class="secttl">Services</div>
    {#if dash.services.length === 0}
      <div class="empty-services">
        <p>No services indexed yet</p>
        <code class="well">codegraph index scip &lt;path&gt; --service=&lt;name&gt;</code>
      </div>
    {:else if shownServices.length === 0}
      <div class="empty-services">
        <p>No card for <b>{scopedService}</b> in this snapshot</p>
      </div>
    {:else}
      <div class="services">
        {#each shownServices as service (service.name)}
          <ServiceCard {service} />
        {/each}
      </div>
    {/if}

    <HealthFlags flags={shownHealth} />

    <div class="bottom">
      <CallHubs hubs={dash.callHubs} />
      <RecentDocLinks links={dash.recentDocLinks} />
    </div>
  {:catch err}
    <ErrorBanner message={err.message} onRetry={retry} />
  {/await}
</div>

<style>
  .pane {
    height: 100%;
    overflow: auto;
    padding: var(--s-5) var(--s-6);
    max-width: 1400px;
    margin: 0 auto;
  }
  .phead {
    display: flex;
    align-items: baseline;
    gap: 12px;
  }
  .phead h1 {
    font-size: var(--text-xl);
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .meta {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 7px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink-3);
  }
  .okd {
    width: 8px;
    height: 8px;
    border-radius: var(--r-full);
    background: var(--ok);
  }
  .snapshot {
    text-align: right;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-disabled);
    margin-bottom: var(--s-4);
  }
  .warnbanner {
    background: var(--warn-subtle);
    border: 1px solid var(--warn);
    border-radius: var(--r-md);
    padding: var(--s-2) var(--s-3);
    margin-bottom: var(--s-4);
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .wline {
    font-size: 11px;
    color: var(--warn);
  }
  .scopenote {
    margin: var(--s-2) 0 var(--s-4);
    font-size: var(--text-sm);
    color: var(--ink-3);
  }
  .scopenote b {
    color: var(--ink);
    font-weight: 600;
  }
  .secttl {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-3);
    margin: 0 0 var(--s-2);
  }
  .services {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: var(--s-3);
    margin-bottom: var(--s-4);
  }
  .empty-services {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: var(--s-6);
    margin-bottom: var(--s-4);
    text-align: center;
    color: var(--ink-3);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--s-3);
  }
  .well {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 6px 12px;
    color: var(--ink-2);
  }
  .bottom {
    display: grid;
    /* minmax(0,…) — plain 1fr lets a card's long mono paths blow the column
       out past the viewport (grid items have min-width:auto) */
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: var(--s-3);
  }

  @media (max-width: 720px) {
    .bottom {
      grid-template-columns: 1fr;
    }
  }
</style>
