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
      <span class="avatar-icon user-icon">you</span>
    {:else}
      <span class="avatar-icon bot-icon">cg</span>
    {/if}
  </div>

  <div class="bubble">
    <div class="prose" class:user-prose={isUser}>
      <!-- eslint-disable-next-line svelte/no-at-html-tags -->
      {@html html}
      {#if streaming}
        <span class="cursor" aria-hidden="true">▌</span>
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
    gap: 12px;
    padding: 14px 0;
    animation: slide-in-up 180ms ease both;
  }

  .message + .message {
    border-top: 1px solid var(--border-dim);
  }

  /* User messages align right */
  .message.user {
    flex-direction: row-reverse;
  }

  /* ── Avatar ── */
  .avatar {
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    margin-top: 2px;
  }

  .avatar-icon {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-sm);
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .user-icon {
    background: var(--bg-overlay);
    border: 1px solid var(--border-mid);
    color: var(--text-soft);
  }

  .bot-icon {
    background: var(--amber-dim);
    border: 1px solid var(--amber-soft);
    color: var(--amber);
    box-shadow: var(--glow-amber);
  }

  /* ── Bubble ── */
  .bubble {
    flex: 1;
    min-width: 0;
    max-width: 82%;
  }

  .message.user .bubble {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
  }

  /* ── Prose ── */
  .prose {
    background: var(--bg-surface);
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    padding: 10px 14px;
    min-width: 2rem;
    color: var(--text-bright);
    font-size: 12.5px;
    line-height: 1.7;
  }

  /* User bubble slightly different tint */
  .prose.user-prose {
    background: var(--bg-overlay);
    border-color: var(--border-mid);
    color: var(--text-mid);
  }

  /* ── Streaming cursor ── */
  .cursor {
    display: inline-block;
    color: var(--amber);
    animation: blink-cursor 0.9s step-end infinite;
    font-size: 0.9em;
    vertical-align: text-bottom;
    margin-left: 1px;
  }

  /* Prose element overrides (these apply to {@html} output) */
  .prose :global(p) { margin-bottom: 0.65em; }
  .prose :global(p:last-child) { margin-bottom: 0; }
  .prose :global(h1),
  .prose :global(h2),
  .prose :global(h3) {
    color: var(--text-primary);
    font-weight: 700;
    margin: 0.8em 0 0.3em;
  }
  .prose :global(h2) { color: var(--amber); }
  .prose :global(h3) { color: var(--cyan); }
  .prose :global(strong) { color: var(--text-primary); font-weight: 700; }
  .prose :global(em) { font-style: italic; color: var(--text-mid); }
  .prose :global(code) {
    background: var(--bg-void);
    color: var(--amber-bright);
    padding: 1px 5px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-dim);
    font-size: 0.86em;
    font-family: var(--font-mono);
  }
  .prose :global(pre) {
    background: var(--bg-void);
    border: 1px solid var(--border-soft);
    border-left: 3px solid var(--border-bright);
    border-radius: var(--radius-md);
    padding: 10px 14px;
    overflow-x: auto;
    margin: 0.65em 0;
    font-size: 0.84em;
    line-height: 1.5;
  }
  .prose :global(pre code) {
    background: none;
    border: none;
    padding: 0;
    color: var(--text-bright);
    font-size: inherit;
  }
  .prose :global(ul) {
    margin: 0.4em 0 0.4em 1.2em;
    color: var(--text-mid);
  }
  .prose :global(li) { margin-bottom: 0.2em; }
  .prose :global(hr) {
    border: none;
    border-top: 1px solid var(--border-dim);
    margin: 0.8em 0;
  }
</style>
