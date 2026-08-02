<script lang="ts">
  /**
   * Global service/scope selector (RFC-012 R9) mounted in the studio topbar.
   * Fetches the service list on mount, writes selection into the scope store,
   * and reconciles a stale persisted service against the live list. "All
   * services" is styled as a warned state — unscoped queries can be slow.
   */
  import { onMount } from 'svelte'
  import { scope } from '$lib/stores/scope.svelte'
  import { timedFetch } from '$lib/api/timedFetch'
  import type { ApiEnvelope, ApiError } from '$lib/types/graph'
  import type { ServicesResponse } from '$lib/types/flows'

  type Status = 'loading' | 'loaded' | 'error'

  const ALL = '__all__'

  let services = $state<string[]>([])
  let status = $state<Status>('loading')
  let errorMessage = $state('')

  // Reading scope.service inside a $derived subscribes to the store's rune.
  const value = $derived(scope.service ?? ALL)
  const isAll = $derived(scope.service === null)

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
      // Drop a stale persisted service that no longer exists in the graph.
      scope.reconcile(services)
    } catch (e) {
      status = 'error'
      errorMessage = e instanceof Error ? e.message : String(e)
    }
  }

  onMount(loadServices)

  // Callback props / handlers hoisted to stable consts (never inline arrows in
  // the template) — the codebase's Svelte-5 effect-loop guard.
  const onSelect = (ev: Event) => {
    const v = (ev.target as HTMLSelectElement).value
    scope.setService(v === ALL ? null : v)
  }
  const retry = () => void loadServices()
</script>

<div class="scope" class:warned={isAll && status === 'loaded'}>
  {#if status === 'error'}
    <span class="err" title={errorMessage}>services unavailable</span>
    <button class="retry" type="button" onclick={retry}>retry</button>
  {:else}
    <select
      class="sel"
      class:warned={isAll}
      value={status === 'loaded' ? value : ''}
      disabled={status !== 'loaded'}
      onchange={onSelect}
      title={isAll
        ? 'All services — unscoped queries scan the whole graph and can be slow'
        : `Scoped to ${scope.service}`}
      aria-label="Service scope"
    >
      {#if status === 'loading'}
        <option value="" disabled>loading services…</option>
      {:else}
        <option value={ALL}>All services</option>
        {#each services as svc (svc)}
          <option value={svc}>{svc}</option>
        {/each}
      {/if}
    </select>
    {#if isAll && status === 'loaded'}
      <span class="badge" title="Unscoped queries can be slow" aria-hidden="true">unscoped</span>
    {/if}
  {/if}
</div>

<style>
  .scope {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .sel {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 4px 8px;
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink);
    max-width: 220px;
  }
  .sel:disabled {
    color: var(--ink-disabled);
    cursor: default;
  }
  /* "All services" — warned amber tint per RFC R9 */
  .sel.warned {
    background: var(--warn-subtle);
    border-color: var(--warn);
    color: var(--warn);
  }
  .badge {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--warn);
    background: var(--warn-subtle);
    border: 1px solid var(--warn);
    border-radius: var(--r-full);
    padding: 1px 8px;
  }
  .err {
    font-size: var(--text-sm);
    color: var(--err);
  }
  .retry {
    font-size: var(--text-xs);
    text-decoration: underline;
    color: var(--accent-ink);
  }
</style>
