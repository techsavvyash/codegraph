<script lang="ts">
  import type { Message } from '$lib/types'
  import CitationsPanel from './CitationsPanel.svelte'

  export let message: Message
  export let streaming = false

  // Minimal markdown → HTML transform (no external dep)
  function renderMarkdown(text: string): string {
    return text
      // Fenced code blocks
      .replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) =>
        `<pre><code class="lang-${lang}">${escHtml(code.trimEnd())}</code></pre>`
      )
      // Inline code
      .replace(/`([^`\n]+)`/g, (_, c) => `<code>${escHtml(c)}</code>`)
      // Bold
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      // Italic
      .replace(/\*([^*\n]+)\*/g, '<em>$1</em>')
      // H3
      .replace(/^### (.+)$/gm, '<h3>$1</h3>')
      // H2
      .replace(/^## (.+)$/gm, '<h2>$1</h2>')
      // H1
      .replace(/^# (.+)$/gm, '<h1>$1</h1>')
      // HR
      .replace(/^---$/gm, '<hr>')
      // Bullet lists (simple)
      .replace(/^[-*] (.+)$/gm, '<li>$1</li>')
      .replace(/(<li>[\s\S]*?<\/li>)(?=\n|$)/g, '<ul>$1</ul>')
      // Paragraphs (double newlines)
      .split(/\n\n+/)
      .map(block => {
        if (/^<(pre|ul|h[1-3]|hr)/.test(block.trim())) return block
        return `<p>${block.replace(/\n/g, '<br>')}</p>`
      })
      .join('\n')
  }

  function escHtml(s: string): string {
    return s
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
  }

  $: isUser = message.role === 'user'
  $: html = isUser
    ? `<p>${escHtml(message.content).replace(/\n/g, '<br>')}</p>`
    : renderMarkdown(message.content)
  $: hasSources = (message.sources?.length ?? 0) > 0
</script>

<div class="message" class:user={isUser} class:assistant={!isUser}>
  <div class="avatar" aria-hidden="true">
    {#if isUser}
      <span class="avatar-label user-avatar">you</span>
    {:else}
      <span class="avatar-label bot-avatar">cg</span>
    {/if}
  </div>

  <div class="bubble">
    <div class="prose" class:user-prose={isUser}>
      <!-- eslint-disable-next-line svelte/no-at-html-tags -->
      {@html html}
      {#if streaming}
        <span class="cursor" aria-hidden="true" />
      {/if}
    </div>

    {#if hasSources && message.sources}
      <CitationsPanel sources={message.sources} />
    {/if}
  </div>
</div>

<style>
  .message {
    display: flex;
    gap: 11px;
    padding: 18px 0;
    animation: slide-in-up 160ms ease both;
  }

  .message + .message {
    border-top: 1px solid var(--border-light);
  }

  .message.user {
    flex-direction: row-reverse;
  }

  /* ── Avatar ── */
  .avatar {
    flex-shrink: 0;
    width: 26px;
    height: 26px;
    margin-top: 2px;
  }

  .avatar-label {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: var(--radius-full);
    font-size: 8.5px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    font-family: var(--font-sans);
  }

  .user-avatar {
    background: var(--accent);
    color: #fff;
  }

  .bot-avatar {
    background: var(--bg-subtle);
    color: var(--text-tertiary);
    border: 1px solid var(--border);
  }

  /* ── Bubble ── */
  .bubble {
    flex: 1;
    min-width: 0;
    max-width: 78%;
  }

  .message.user .bubble {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
  }

  /* ── Prose ── */
  .prose {
    font-family: var(--font-sans);
    font-size: 13.5px;
    line-height: 1.75;
    color: var(--text-primary);
  }

  .user-prose {
    background: var(--accent);
    color: #fff;
    padding: 9px 14px;
    border-radius: var(--radius-xl) var(--radius-xl) var(--radius-sm) var(--radius-xl);
    display: inline-block;
    max-width: 100%;
  }

  /* ── Streaming cursor ── */
  .cursor {
    display: inline-block;
    width: 1.5px;
    height: 1em;
    background: var(--text-secondary);
    vertical-align: text-bottom;
    margin-left: 2px;
    animation: blink-cursor 0.9s step-end infinite;
  }

  /* Prose element overrides (apply to {@html} output) */
  .prose :global(p) { margin-bottom: 0.6em; }
  .prose :global(p:last-child) { margin-bottom: 0; }
  .prose :global(h1),
  .prose :global(h2),
  .prose :global(h3) {
    font-weight: 600;
    margin: 1em 0 0.35em;
    color: var(--text-primary);
    letter-spacing: -0.01em;
  }
  .prose :global(h1) { font-size: 1.1em; }
  .prose :global(h2) { font-size: 1.02em; }
  .prose :global(h3) { font-size: 0.97em; color: var(--text-secondary); }
  .prose :global(strong) { font-weight: 600; color: var(--text-primary); }
  .prose :global(em) { font-style: italic; color: var(--text-secondary); }
  .prose :global(code) {
    font-family: var(--font-mono);
    font-size: 0.84em;
    background: var(--bg-subtle);
    color: var(--text-primary);
    padding: 1px 5px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
  }
  .prose :global(pre) {
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-left: 2px solid var(--border-strong);
    border-radius: var(--radius-md);
    padding: 11px 14px;
    overflow-x: auto;
    margin: 0.7em 0;
    font-size: 0.82em;
    line-height: 1.55;
  }
  .prose :global(pre code) {
    background: none;
    border: none;
    padding: 0;
    color: var(--text-primary);
    font-size: inherit;
  }
  .prose :global(ul) {
    margin: 0.4em 0 0.4em 1.2em;
    color: var(--text-secondary);
  }
  .prose :global(li) { margin-bottom: 0.2em; }
  .prose :global(hr) {
    border: none;
    border-top: 1px solid var(--border);
    margin: 0.8em 0;
  }
</style>
