<script lang="ts">
  /**
   * Chat dock (RFC-012 R6): a self-contained floating chat panel. Mount ONCE in
   * +layout.svelte — it fixes itself to the bottom-right, owns its own scroll,
   * and does not touch page layout. Talks to /api/chat via a ChatStore; the
   * active global scope (service/scopeId) travels with every request so tool
   * calls stay scope-aware.
   */
  import { untrack } from 'svelte'
  import { ChatStore } from '$lib/stores/chat.svelte'
  import { scope } from '$lib/stores/scope.svelte'
  import ChatMessageView from './ChatMessageView.svelte'

  const chat = new ChatStore()

  let inputEl = $state<HTMLTextAreaElement | null>(null)
  let listEl = $state<HTMLElement | null>(null)
  let draft = $state('')
  // Resizable drawer height (px), persisted only in-session.
  let panelHeight = $state(440)

  const scopeLabel = $derived(scope.service ?? 'unscoped')
  const streamingId = $derived(
    chat.loading ? chat.messages.findLast((m) => m.role === 'assistant')?.id ?? null : null
  )

  // Autoscroll to the bottom as content streams in. Reading messages/activity
  // registers the dependency; the DOM write is deferred so it runs post-render.
  // untrack around the scroll write keeps it from re-subscribing to anything.
  $effect(() => {
    // touch reactive sources
    void chat.messages.length
    void chat.messages.at(-1)?.content
    void chat.activity
    const el = listEl
    if (!el) return
    untrack(() => {
      requestAnimationFrame(() => {
        el.scrollTop = el.scrollHeight
      })
    })
  })

  function submit() {
    const text = draft.trim()
    if (!text || chat.loading) return
    draft = ''
    if (inputEl) {
      inputEl.style.height = 'auto'
    }
    // Snapshot the active scope at send time.
    void chat.send(text, { service: scope.service, scopeId: scope.scopeId })
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  function autoResize() {
    const el = inputEl
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 140) + 'px'
  }

  function toggle() {
    chat.toggle()
    if (chat.open) {
      requestAnimationFrame(() => inputEl?.focus())
    }
  }

  // Drag-to-resize the panel height from the top edge.
  let resizing = false
  function startResize(e: PointerEvent) {
    resizing = true
    const startY = e.clientY
    const startH = panelHeight
    const move = (ev: PointerEvent) => {
      if (!resizing) return
      const next = startH + (startY - ev.clientY)
      panelHeight = Math.max(280, Math.min(next, window.innerHeight - 120))
    }
    const up = () => {
      resizing = false
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }
</script>

{#if !chat.open}
  <button class="fab" onclick={toggle} aria-label="Open chat">
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M4 5h16v11H8l-4 4V5z" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" />
      <circle cx="9" cy="10.5" r="1" fill="currentColor" />
      <circle cx="12.5" cy="10.5" r="1" fill="currentColor" />
      <circle cx="16" cy="10.5" r="1" fill="currentColor" />
    </svg>
  </button>
{:else}
  <section class="dock" style="height: {panelHeight}px" aria-label="Chat">
    <div class="resize" onpointerdown={startResize} role="separator" aria-orientation="horizontal" aria-label="Resize chat"></div>

    <header class="head">
      <span class="title">Chat</span>
      <span class="scope" class:unscoped={scope.service === null} title="Active service scope for tool calls">
        {scopeLabel}
      </span>
      <span class="grow"></span>
      <button class="icon" onclick={() => chat.clear()} title="Clear conversation" aria-label="Clear conversation">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
      <button class="icon" onclick={toggle} title="Close" aria-label="Close chat">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M6 6l12 12M18 6L6 18" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
        </svg>
      </button>
    </header>

    {#if chat.error}
      <div class="banner" role="status">
        <span>{chat.error}</span>
        <button class="dismiss" onclick={() => chat.dismissError()} aria-label="Dismiss">×</button>
      </div>
    {/if}

    <div class="list" bind:this={listEl}>
      {#if chat.messages.length === 0}
        <div class="empty">
          <p class="etitle">Ask about your codebase</p>
          <p class="esub">Searches the graph, finds references, and cites the functions and files it used. Tool calls run against the active scope.</p>
        </div>
      {:else}
        {#each chat.messages as m (m.id)}
          <ChatMessageView message={m} streaming={m.id === streamingId} />
        {/each}
      {/if}
      {#if chat.activity}
        <div class="activity" role="status" aria-live="polite">
          <span class="spin" aria-hidden="true"></span>
          <span class="alabel">{chat.activity}</span>
        </div>
      {/if}
    </div>

    <div class="input">
      <textarea
        bind:this={inputEl}
        bind:value={draft}
        onkeydown={onKeydown}
        oninput={autoResize}
        rows="1"
        placeholder="Ask about your codebase…"
        aria-label="Chat message"
        spellcheck="false"
      ></textarea>
      <button class="send" onclick={submit} disabled={chat.loading || !draft.trim()} aria-label="Send message">
        {#if chat.loading}
          <span class="spin" aria-hidden="true"></span>
        {:else}
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M5 12h14M13 6l6 6-6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        {/if}
      </button>
    </div>
  </section>
{/if}

<style>
  .fab {
    position: fixed;
    right: 20px;
    bottom: 20px;
    z-index: 900;
    width: 46px;
    height: 46px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: var(--r-full);
    box-shadow: var(--shadow-3);
    cursor: pointer;
    transition: background 120ms, transform 120ms;
  }
  .fab:hover { background: var(--accent-hover); transform: translateY(-1px); }

  .dock {
    position: fixed;
    right: 20px;
    bottom: 20px;
    z-index: 900;
    width: min(440px, calc(100vw - 40px));
    display: flex;
    flex-direction: column;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    box-shadow: var(--shadow-3);
    overflow: hidden;
  }

  .resize {
    height: 8px;
    margin-bottom: -4px;
    cursor: ns-resize;
    flex-shrink: 0;
    touch-action: none;
  }
  .resize:hover { background: linear-gradient(var(--border), transparent); }

  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 9px 12px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .title { font-size: var(--text-sm); font-weight: 600; color: var(--ink); font-family: var(--font-sans); }
  .scope {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--accent-ink);
    background: var(--accent-subtle);
    border: 1px solid var(--accent-border);
    padding: 1px 7px;
    border-radius: var(--r-full);
    max-width: 160px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .scope.unscoped { color: var(--warn); background: var(--warn-subtle); border-color: var(--warn); }
  .grow { flex: 1; }
  .icon {
    display: flex;
    padding: 4px;
    background: none;
    border: none;
    border-radius: var(--r-sm);
    color: var(--ink-3);
    cursor: pointer;
    transition: background 120ms, color 120ms;
  }
  .icon:hover { background: var(--bg-hover); color: var(--ink); }

  .banner {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 7px 12px;
    background: var(--warn-subtle);
    border-bottom: 1px solid var(--warn);
    color: var(--ink-2);
    font-size: var(--text-xs);
    font-family: var(--font-sans);
    line-height: 1.4;
  }
  .banner span { flex: 1; }
  .dismiss {
    background: none;
    border: none;
    cursor: pointer;
    color: var(--ink-3);
    font-size: 16px;
    line-height: 1;
    padding: 0 2px;
  }

  .list {
    flex: 1;
    overflow-y: auto;
    padding: 4px 14px 8px;
    min-height: 0;
  }

  .empty {
    display: flex;
    flex-direction: column;
    justify-content: center;
    height: 100%;
    text-align: center;
    padding: 20px;
    gap: 6px;
  }
  .etitle { font-size: var(--text-md); font-weight: 600; color: var(--ink-2); font-family: var(--font-sans); }
  .esub { font-size: var(--text-xs); color: var(--ink-3); line-height: 1.55; max-width: 300px; margin: 0 auto; }

  .activity {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    margin: 4px 0 8px 30px;
    padding: 3px 10px;
    background: var(--accent-subtle);
    border: 1px solid var(--accent-border);
    border-radius: var(--r-full);
    font-size: var(--text-xs);
    color: var(--accent-ink);
    font-family: var(--font-sans);
  }
  .alabel { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 320px; }

  .input {
    display: flex;
    align-items: flex-end;
    gap: 8px;
    padding: 9px 12px;
    border-top: 1px solid var(--border);
    flex-shrink: 0;
    background: var(--bg-page);
  }
  textarea {
    flex: 1;
    resize: none;
    border: 1px solid var(--border-strong);
    border-radius: var(--r-md);
    padding: 7px 10px;
    font-family: var(--font-sans);
    font-size: var(--text-sm);
    color: var(--ink);
    line-height: 1.5;
    max-height: 140px;
    background: var(--bg-panel);
    outline: none;
    transition: border-color 120ms, box-shadow 120ms;
  }
  textarea:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-border); }
  textarea::placeholder { color: var(--ink-disabled); }

  .send {
    flex-shrink: 0;
    width: 32px;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: var(--r-md);
    cursor: pointer;
    transition: background 120ms, opacity 120ms;
  }
  .send:hover:not(:disabled) { background: var(--accent-hover); }
  .send:disabled { opacity: 0.4; cursor: not-allowed; }

  .spin {
    width: 13px;
    height: 13px;
    border: 2px solid rgba(255, 255, 255, 0.4);
    border-top-color: #fff;
    border-radius: 50%;
    animation: cd-dock-spin 0.8s linear infinite;
    display: inline-block;
    flex-shrink: 0;
  }
  .activity .spin { border-color: var(--accent-border); border-top-color: var(--accent); }
  @keyframes cd-dock-spin { to { transform: rotate(360deg); } }
</style>
