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
    <div class="header-brand">
      <div class="brand-mark" aria-hidden="true">
        <div class="mark-dot" />
      </div>
      <span class="brand-name">CodeGraph</span>
      <span class="brand-sep">/</span>
      <span class="brand-sub">Intelligence</span>
    </div>

    <div class="header-right">
      {#if $loading}
        <span class="spinner-wrap" aria-label="Processing">
          <svg class="spin-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M12 2a10 10 0 1 0 10 10" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"/>
          </svg>
        </span>
      {:else}
        <span class="status-idle" title="Ready" aria-label="Ready" />
      {/if}

      {#if $messages.length > 0}
        <button class="clear-btn" on:click={clearMessages} title="Clear conversation">
          clear
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
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="1.8"/>
          <path d="M12 8v4M12 16h.01" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>
        </svg>
        <span>{$error}</span>
        <button class="error-dismiss" on:click={dismissError} aria-label="Dismiss error">×</button>
      </div>
    {/if}

    <InputBox disabled={$loading} onsubmit={handleSubmit} />
  </main>
</div>

<style>
  /* ── Shell ── */
  .shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
    max-width: 860px;
    margin: 0 auto;
    border-left: 1px solid var(--border);
    border-right: 1px solid var(--border);
    background: var(--bg-page);
  }

  /* ── Header ── */
  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 var(--space-6);
    height: 52px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-page);
    flex-shrink: 0;
  }

  .header-brand {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .brand-mark {
    width: 20px;
    height: 20px;
    border: 1.5px solid var(--text-primary);
    border-radius: var(--radius-sm);
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .mark-dot {
    width: 6px;
    height: 6px;
    background: var(--accent);
    border-radius: 50%;
  }

  .brand-name {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-primary);
    letter-spacing: -0.01em;
  }

  .brand-sep {
    color: var(--border-strong);
    font-size: 14px;
    font-weight: 300;
  }

  .brand-sub {
    font-size: 12px;
    color: var(--text-tertiary);
    font-weight: 400;
  }

  .header-right {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .spinner-wrap {
    color: var(--accent);
    display: flex;
    align-items: center;
  }

  .spin-icon { animation: spin 0.9s linear infinite; }

  .status-idle {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--border-strong);
    display: inline-block;
  }

  .clear-btn {
    font-family: var(--font-sans);
    font-size: 12px;
    font-weight: 500;
    color: var(--text-tertiary);
    background: none;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 4px 10px;
    cursor: pointer;
    transition: color var(--t-fast), border-color var(--t-fast), background var(--t-fast);
    letter-spacing: 0.01em;
  }

  .clear-btn:hover {
    color: var(--text-secondary);
    border-color: var(--border-strong);
    background: var(--bg-surface);
  }

  /* ── Main ── */
  .main {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
  }

  /* ── Tool strip ── */
  .tool-strip {
    padding: 8px var(--space-6);
    border-top: 1px solid var(--border-light);
    background: var(--bg-page);
    flex-shrink: 0;
  }

  /* ── Error banner ── */
  .error-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 var(--space-6) var(--space-2);
    padding: 9px 12px;
    background: var(--red-subtle);
    border: 1px solid var(--red-border);
    border-radius: var(--radius-md);
    color: var(--red);
    font-size: 12.5px;
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
    font-size: 17px;
    line-height: 1;
    padding: 0 2px;
    opacity: 0.6;
    transition: opacity var(--t-fast);
    font-family: var(--font-sans);
  }
  .error-dismiss:hover { opacity: 1; }
</style>
