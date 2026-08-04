<script lang="ts">
  /**
   * Service picker overlay for the Overview visualizer, shown when overview mode
   * is active and no service is selected in the global scope store. Card click
   * promotes the choice into the global scope (scope.setService) — the topbar
   * selector follows it and the overview loads. Reads GET /api/services (the
   * same endpoint the dashboard/topbar consume).
   */
  import { onMount } from 'svelte'
  import { scope } from '$lib/stores/scope.svelte'
  import { timedFetch } from '$lib/api/timedFetch'
  import type { ApiEnvelope, ApiError } from '$lib/types/graph'
  import type { ServicesResponse } from '$lib/types/flows'

  type Status = 'loading' | 'loaded' | 'error'

  let services = $state<string[]>([])
  let status = $state<Status>('loading')
  let errorMessage = $state('')

  async function loadServices() {
    status = 'loading'
    errorMessage = ''
    try {
      const res = await timedFetch('/api/services')
      const body = (await res.json()) as ApiEnvelope<ServicesResponse> | ApiError
      if (!res.ok || 'error' in body) {
        throw new Error((body as ApiError).error ?? `HTTP ${res.status}`)
      }
      services = (body as ApiEnvelope<ServicesResponse>).data.services
      status = 'loaded'
    } catch (e) {
      status = 'error'
      errorMessage = e instanceof Error ? e.message : String(e)
    }
  }

  onMount(loadServices)

  // hoisted handlers — never inline arrows (the Svelte-5 effect-loop guard)
  const pick = (name: string) => scope.setService(name)
  const retry = () => void loadServices()
</script>

<div class="picker" data-testid="service-picker">
  <div class="card-well">
    <h1>Select a service to explore</h1>
    <p class="sub">Pick a service to see its whole file graph — drill into directories, files, and symbols.</p>

    {#if status === 'loading'}
      <div class="state">Loading services…</div>
    {:else if status === 'error'}
      <div class="state err">
        <span>{errorMessage || 'services unavailable'}</span>
        <button class="retry" type="button" onclick={retry}>retry</button>
      </div>
    {:else if services.length === 0}
      <div class="state">No services in the graph yet — index a project first.</div>
    {:else}
      <div class="grid">
        {#each services as svc (svc)}
          <button class="scard" type="button" data-testid="service-card" data-service={svc} onclick={() => pick(svc)}>
            <span class="svc-name">{svc}</span>
            <span class="svc-go">Explore →</span>
          </button>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .picker {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    background: var(--bg-canvas);
    padding: var(--s-6);
    overflow-y: auto;
    z-index: 5;
  }
  .card-well {
    width: 100%;
    max-width: 640px;
    text-align: center;
  }
  h1 {
    font-size: var(--text-xl);
    font-weight: 600;
    color: var(--ink);
    margin: 0 0 6px;
  }
  .sub {
    font-size: var(--text-sm);
    color: var(--ink-3);
    margin: 0 0 var(--s-5);
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
    gap: var(--s-3);
  }
  .scard {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
    padding: var(--s-3) var(--s-4);
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    box-shadow: var(--shadow-1);
    text-align: left;
    transition: border-color 0.1s;
  }
  .scard:hover {
    border-color: var(--accent-border);
    background: var(--bg-hover);
  }
  .svc-name {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink);
    word-break: break-all;
  }
  .svc-go {
    font-size: var(--text-xs);
    color: var(--accent-ink);
  }
  .state {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--ink-3);
    font-size: var(--text-sm);
    padding: var(--s-6);
  }
  .state.err {
    color: var(--err);
  }
  .retry {
    text-decoration: underline;
    color: var(--accent-ink);
    font-size: var(--text-xs);
  }
</style>
