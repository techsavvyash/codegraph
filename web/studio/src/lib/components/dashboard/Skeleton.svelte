<script lang="ts">
  // Shimmer-free loading placeholder matching the dashboard layout: KPI strip
  // (4 blocks), service cards (3 blocks), and a health list block.
</script>

<div class="skeleton" aria-busy="true" aria-label="Loading dashboard">
  <div class="kpis">
    {#each { length: 4 } as _}
      <div class="block kpi"></div>
    {/each}
  </div>

  <div class="secttl-block"></div>
  <div class="services">
    {#each { length: 3 } as _}
      <div class="block card"></div>
    {/each}
  </div>

  <div class="secttl-block"></div>
  <div class="block list"></div>
</div>

<style>
  .skeleton {
    display: flex;
    flex-direction: column;
    gap: var(--s-4);
  }
  .block {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
  }
  .kpis {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: var(--s-3);
  }
  .kpi {
    height: 74px;
  }
  .secttl-block {
    width: 80px;
    height: 10px;
    border-radius: var(--r-sm);
    background: var(--bg-subtle);
  }
  .services {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: var(--s-3);
  }
  .card {
    height: 168px;
  }
  .list {
    height: 120px;
  }
</style>
