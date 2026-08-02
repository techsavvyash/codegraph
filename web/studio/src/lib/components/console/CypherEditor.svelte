<script lang="ts">
  /**
   * Cypher editor (RFC-012 R8): a plain monospace textarea — no editor deps.
   * Cmd/Ctrl+Enter runs. The parent owns the query text (bind:value) and the
   * caret position (bind:selStart/selEnd) so the scope-filter snippet can be
   * inserted at the cursor. Run is disabled while a request is in flight.
   */
  interface Props {
    value: string
    selStart: number
    selEnd: number
    running: boolean
    /** Whether the query is currently runnable (query non-empty, params valid). */
    canRun: boolean
    onRun: () => void
  }

  let {
    value = $bindable(),
    selStart = $bindable(),
    selEnd = $bindable(),
    running,
    canRun,
    onRun
  }: Props = $props()

  let ta: HTMLTextAreaElement | undefined = $state()

  function syncSelection() {
    if (!ta) return
    selStart = ta.selectionStart
    selEnd = ta.selectionEnd
  }

  function onKeydown(ev: KeyboardEvent) {
    if ((ev.metaKey || ev.ctrlKey) && ev.key === 'Enter') {
      ev.preventDefault()
      if (canRun) onRun()
    }
  }

  // Exposed so the parent can restore the caret after inserting a snippet.
  export function focusCaret(pos: number) {
    if (!ta) return
    ta.focus()
    ta.setSelectionRange(pos, pos)
    selStart = pos
    selEnd = pos
  }
</script>

<div class="editor">
  <textarea
    bind:this={ta}
    bind:value
    onkeydown={onKeydown}
    onselect={syncSelection}
    onkeyup={syncSelection}
    onclick={syncSelection}
    spellcheck="false"
    autocapitalize="off"
    autocomplete="off"
    placeholder="MATCH (s:Service) RETURN s.name ORDER BY s.name"
  ></textarea>
  <div class="bar">
    <span class="hint">⌘/Ctrl+Enter to run · read-only</span>
    <button class="run" onclick={onRun} disabled={!canRun}>
      {running ? 'Running…' : 'Run'}
    </button>
  </div>
</div>

<style>
  .editor {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    background: var(--bg-panel);
    overflow: hidden;
  }
  textarea {
    resize: vertical;
    min-height: 120px;
    padding: var(--s-3);
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    line-height: 1.5;
    color: var(--ink);
    background: var(--bg-panel);
    border: none;
    outline: none;
    tab-size: 2;
  }
  .bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--s-2) var(--s-3);
    border-top: 1px solid var(--border);
    background: var(--bg-subtle);
  }
  .hint {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink-3);
  }
  .run {
    padding: 5px 14px;
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--accent-ink);
    background: var(--accent);
    border: 1px solid var(--accent-border);
    border-radius: var(--r-md);
  }
  .run:hover:not(:disabled) {
    background: var(--accent-hover);
  }
  .run:disabled {
    color: var(--ink-disabled);
    background: var(--bg-hover);
    border-color: var(--border);
    cursor: not-allowed;
  }
</style>
