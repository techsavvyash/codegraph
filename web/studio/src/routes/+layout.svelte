<script lang="ts">
  import '../app.css'
  import { page } from '$app/state'

  let { children } = $props()

  const tabs = [
    { label: 'Graph', href: '/graph', built: false },
    { label: 'Flows', href: '/flows', built: false },
    { label: 'Docs', href: '/docs', built: false },
    { label: 'Console', href: '/console', built: false },
    { label: 'Dashboard', href: '/dashboard', built: true }
  ]
</script>

<div class="app">
  <header class="topbar">
    <a class="brand" href="/dashboard">
      <span class="mark"></span>
      CodeGraph <small>Studio</small>
    </a>
    <div class="tabs">
      {#each tabs as tab}
        {#if tab.built}
          <a
            class="tab"
            class:active={page.url.pathname.startsWith(tab.href)}
            href={tab.href}>{tab.label}</a
          >
        {:else}
          <span class="tab disabled" title="Not built yet">{tab.label}</span>
        {/if}
      {/each}
    </div>
  </header>
  <main class="main">
    {@render children()}
  </main>
</div>

<style>
  .app {
    height: 100vh;
    display: grid;
    grid-template-rows: 48px 1fr;
    overflow: hidden;
  }
  .topbar {
    display: flex;
    align-items: center;
    gap: var(--s-4);
    padding: 0 var(--s-4);
    background: var(--bg-panel);
    border-bottom: 1px solid var(--border);
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 600;
    font-size: var(--text-md);
    letter-spacing: -0.01em;
  }
  .brand .mark {
    width: 20px;
    height: 20px;
    border-radius: 6px;
    background: var(--accent);
    position: relative;
  }
  .brand .mark::after {
    content: '';
    position: absolute;
    inset: 6px;
    border-radius: 2px;
    background: #fff;
    opacity: 0.9;
  }
  .brand small {
    font-weight: 450;
    color: var(--ink-3);
    font-size: var(--text-sm);
  }
  .tabs {
    display: flex;
    gap: 2px;
    margin-left: auto;
  }
  .tab {
    padding: 5px 12px;
    border-radius: var(--r-md);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink-2);
  }
  .tab.active {
    background: var(--accent-subtle);
    color: var(--accent-ink);
  }
  .tab:not(.active):not(.disabled):hover {
    background: var(--bg-hover);
  }
  .tab.disabled {
    color: var(--ink-disabled);
    cursor: default;
  }
  .main {
    overflow: hidden;
    min-height: 0;
  }
</style>
