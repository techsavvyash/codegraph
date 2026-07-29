<script lang="ts">
  /**
   * Renders the Inspector's "Source"/"Content" section from an already-loaded
   * SourceResponse (loading/fetching and stale-response guarding live in
   * Inspector.svelte, which owns node-change sequencing). kind 'code' is
   * shiki-highlighted; kind 'document'/'chunk' is marked-rendered and
   * DOMPurify-sanitized prose.
   */
  import type { SourceResponse } from '$lib/types/graph'
  import type { Highlighter } from 'shiki'

  type LoadStatus = 'loading' | 'loaded' | 'error'

  interface Props {
    status: LoadStatus
    response: SourceResponse | null
    errorMessage: string
  }

  let { status, response, errorMessage }: Props = $props()

  const SHIKI_LANGS = ['go', 'typescript', 'javascript', 'python', 'java', 'markdown'] as const
  const SHIKI_THEME = 'github-light'

  let highlighterPromise: Promise<Highlighter> | null = null

  function getHighlighter(): Promise<Highlighter> {
    if (!highlighterPromise) {
      highlighterPromise = import('shiki').then(({ createHighlighter }) =>
        createHighlighter({ themes: [SHIKI_THEME], langs: [...SHIKI_LANGS] })
      )
    }
    return highlighterPromise
  }

  let codeHtml = $state<string | null>(null)
  let proseHtml = $state<string | null>(null)
  let renderSeq = 0

  $effect(() => {
    const res = response
    const seq = ++renderSeq
    codeHtml = null
    proseHtml = null
    if (!res) return

    if (res.kind === 'document' || res.kind === 'chunk') {
      void renderMarkdown(res.source, seq)
    } else {
      void renderCode(res, seq)
    }
  })

  async function renderCode(res: SourceResponse, seq: number) {
    try {
      const highlighter = await getHighlighter()
      const lang = SHIKI_LANGS.includes(res.lang as (typeof SHIKI_LANGS)[number]) ? res.lang : 'text'
      const html = highlighter.codeToHtml(res.source, { lang, theme: SHIKI_THEME })
      if (seq !== renderSeq) return
      codeHtml = html
    } catch {
      // Highlighting is a progressive enhancement — the <pre> fallback below covers this.
    }
  }

  async function renderMarkdown(source: string, seq: number) {
    const [{ marked }, { default: DOMPurify }] = await Promise.all([import('marked'), import('dompurify')])
    const raw = await marked.parse(source)
    if (seq !== renderSeq) return
    proseHtml = DOMPurify.sanitize(raw)
  }
</script>

{#if status === 'loading'}
  <div class="skeleton" aria-label="loading source">
    <div class="line" style="width: 88%"></div>
    <div class="line" style="width: 95%"></div>
    <div class="line" style="width: 70%"></div>
    <div class="line" style="width: 82%"></div>
  </div>
{:else if status === 'error' || !response}
  <p class="unavailable">{errorMessage}</p>
{:else if response.kind === 'document' || response.kind === 'chunk'}
  {#if proseHtml}
    <div class="md">{@html proseHtml}</div>
  {:else}
    <div class="md plain">{response.source}</div>
  {/if}
{:else if codeHtml}
  <div class="code shiki-host">{@html codeHtml}</div>
{:else}
  <pre class="code plain">{response.source}</pre>
{/if}

<style>
  .code {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 10px 12px;
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.65;
    overflow-x: auto;
    color: var(--ink-2);
  }
  .code.plain {
    white-space: pre-wrap;
    word-break: break-word;
  }
  .code.shiki-host :global(pre) {
    margin: 0;
    background: transparent !important;
    white-space: pre;
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.65;
  }

  .unavailable {
    font-size: var(--text-sm);
    color: var(--ink-disabled);
    font-style: italic;
  }

  .skeleton {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .skeleton .line {
    height: 10px;
    border-radius: var(--r-sm);
    background: var(--bg-hover);
  }

  .md {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 10px 12px;
    font-size: 13px;
    color: var(--ink-2);
    line-height: 1.6;
  }
  .md.plain {
    white-space: pre-wrap;
    word-break: break-word;
  }
  .md :global(h1),
  .md :global(h2),
  .md :global(h3),
  .md :global(h4),
  .md :global(h5),
  .md :global(h6) {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--ink);
    margin: 8px 0 4px;
  }
  .md :global(h1:first-child),
  .md :global(h2:first-child),
  .md :global(h3:first-child),
  .md :global(h4:first-child) {
    margin-top: 0;
  }
  .md :global(p) {
    margin: 6px 0;
  }
  .md :global(p:first-child) {
    margin-top: 0;
  }
  .md :global(p:last-child) {
    margin-bottom: 0;
  }
  .md :global(code) {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 0 4px;
  }
  .md :global(pre) {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    padding: 8px;
    overflow-x: auto;
  }
  .md :global(pre code) {
    border: none;
    padding: 0;
  }
  .md :global(ul),
  .md :global(ol) {
    padding-left: 18px;
    margin: 6px 0;
  }
  .md :global(a) {
    color: var(--accent);
    text-decoration: underline;
  }
</style>
