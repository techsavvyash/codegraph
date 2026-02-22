<script lang="ts">
  import { messages, loading, toolActivity, error, sendMessage, clearMessages } from '$lib/stores/chat'
  import MessageList from '$lib/components/MessageList.svelte'
  import InputBox from '$lib/components/InputBox.svelte'
  import ToolActivity from '$lib/components/ToolActivity.svelte'

  async function handleSubmit(text: string) {
    await sendMessage(text)
  }

  function dismissError() {
    error.set(null)
  }
</script>

<svelte:head>
  <title>CodeGraph Intelligence</title>
</svelte:head>

<div class="shell">
  <!-- ── Header ── -->
  <header class="header">
    <div class="header-left">
      <!-- Logo mark -->
      <svg class="logo-mark" width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
        <rect x="1" y="1" width="18" height="18" rx="3" stroke="var(--amber)" stroke-width="1.5"/>
        <path d="M5 7h4M5 10h10M5 13h7" stroke="var(--amber)" stroke-width="1.5" stroke-linecap="round"/>
        <circle cx="15" cy="7" r="1.5" fill="var(--amber)" opacity="0.8"/>
      </svg>
      <span class="header-title">CodeGraph</span>
      <span class="header-sep">/</span>
      <span class="header-sub">intelligence</span>
    </div>

    <div class="header-right">
      {#if $loading}
        <span class="status-dot active" title="Processing" aria-label="Processing" />
      {:else}
        <span class="status-dot idle" title="Ready" aria-label="Ready" />
      {/if}

      {#if $messages.length > 0}
        <button class="clear-btn" on:click={clearMessages} title="Clear conversation">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <span>clear</span>
        </button>
      {/if}
    </div>
  </header>

  <!-- ── Main area ── -->
  <main class="main">
    <MessageList messages={$messages} loading={$loading} />

    <!-- Tool activity strip -->
    {#if $toolActivity}
      <div class="tool-strip">
        <ToolActivity activity={$toolActivity} />
      </div>
    {/if}

    <!-- Error banner -->
    {#if $error}
      <div class="error-banner" role="alert">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <circle cx="12" cy="12" r="10" stroke="var(--red)" stroke-width="1.8"/>
          <path d="M12 8v4M12 16h.01" stroke="var(--red)" stroke-width="1.8" stroke-linecap="round"/>
        </svg>
        <span>{$error}</span>
        <button class="error-dismiss" on:click={dismissError} aria-label="Dismiss error">×</button>
      </div>
    {/if}

    <InputBox disabled={$loading} onsubmit={handleSubmit} />
  </main>
</div>

<style>
  /* ── Shell layout ── */
  .shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
    max-width: 900px;
    margin: 0 auto;
    border-left: 1px solid var(--border-dim);
    border-right: 1px solid var(--border-dim);
    background: var(--bg-base);
  }

  /* ── Header ── */
  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 var(--space-6);
    height: 44px;
    border-bottom: 1px solid var(--border-soft);
    background: var(--bg-surface);
    flex-shrink: 0;
    position: relative;
  }

  /* Top accent line */
  .header::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 2px;
    background: linear-gradient(90deg, var(--amber-dim), var(--amber), var(--amber-dim));
    opacity: 0.7;
  }

  .header-left {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .logo-mark {
    flex-shrink: 0;
  }

  .header-title {
    font-size: 12px;
    font-weight: 700;
    color: var(--amber);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .header-sep {
    color: var(--border-bright);
    font-size: 14px;
  }

  .header-sub {
    font-size: 11px;
    color: var(--text-dim);
    letter-spacing: 0.04em;
  }

  .header-right {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  /* Status dot */
  .status-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    display: inline-block;
  }
  .status-dot.active {
    background: var(--cyan);
    box-shadow: 0 0 6px var(--cyan);
    animation: pulse-amber 1.4s ease-in-out infinite;
  }
  .status-dot.idle {
    background: var(--text-muted);
  }

  .clear-btn {
    display: flex;
    align-items: center;
    gap: 5px;
    background: none;
    border: 1px solid var(--border-dim);
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 3px 7px;
    cursor: pointer;
    transition: color var(--t-fast), border-color var(--t-fast), background var(--t-fast);
  }
  .clear-btn:hover {
    color: var(--text-soft);
    border-color: var(--border-soft);
    background: var(--bg-overlay);
  }

  /* ── Main ── */
  .main {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
  }

  /* ── Tool activity strip ── */
  .tool-strip {
    padding: 6px var(--space-6);
    border-top: 1px solid var(--border-dim);
    background: var(--bg-surface);
    flex-shrink: 0;
  }

  /* ── Error banner ── */
  .error-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 var(--space-6) var(--space-2);
    padding: 8px 12px;
    background: var(--red-dim);
    border: 1px solid var(--red);
    border-radius: var(--radius-md);
    color: var(--red);
    font-size: 11.5px;
    animation: slide-in-up var(--t-mid) both;
    flex-shrink: 0;
  }

  .error-banner span {
    flex: 1;
    min-width: 0;
  }

  .error-dismiss {
    background: none;
    border: none;
    color: var(--red);
    cursor: pointer;
    font-size: 16px;
    line-height: 1;
    padding: 0 2px;
    opacity: 0.7;
    transition: opacity var(--t-fast);
  }
  .error-dismiss:hover { opacity: 1; }
</style>
