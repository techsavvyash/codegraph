<script lang="ts">
  import { onMount } from 'svelte'

  export let disabled = false
  export let placeholder = 'Ask about your codebase...'
  export let onsubmit: (text: string) => void = () => {}

  let value = ''
  let textareaEl: HTMLTextAreaElement

  function autoResize() {
    if (!textareaEl) return
    textareaEl.style.height = 'auto'
    textareaEl.style.height = Math.min(textareaEl.scrollHeight, 200) + 'px'
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  function submit() {
    // Read from DOM directly so keydown path works regardless of reactive flush timing
    const text = (textareaEl ? textareaEl.value : value).trim()
    if (!text || disabled) return
    onsubmit(text)
    value = ''
    if (textareaEl) {
      textareaEl.value = ''
      textareaEl.style.height = 'auto'
      textareaEl.focus()
    }
  }

  // Attach keydown directly to bypass Svelte 5 event delegation
  onMount(() => {
    if (!textareaEl) return
    textareaEl.addEventListener('keydown', handleKeydown)
    return () => textareaEl.removeEventListener('keydown', handleKeydown)
  })
</script>

<form class="input-box" on:submit|preventDefault={submit}>
  <div class="input-wrap" class:focused={false}>
    <textarea
      bind:this={textareaEl}
      bind:value
      on:input={autoResize}
      on:keydown={handleKeydown}
      {disabled}
      {placeholder}
      rows="1"
      autocomplete="off"
      autocorrect="off"
      spellcheck="false"
      class="textarea"
      aria-label="Message input"
    />
    <button
      type="submit"
      class="send-btn"
      disabled={disabled || !value.trim()}
      aria-label="Send message"
    >
      {#if disabled}
        <svg class="spin-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M12 2a10 10 0 1 0 10 10" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"/>
        </svg>
      {:else}
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M5 12h14M13 6l6 6-6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      {/if}
    </button>
  </div>
  <p class="hint">
    <kbd>Enter</kbd> to send &nbsp;·&nbsp; <kbd>Shift+Enter</kbd> for new line
  </p>
</form>

<style>
  .input-box {
    padding: var(--space-3) var(--space-6) var(--space-5);
    border-top: 1px solid var(--border);
    background: var(--bg-page);
    flex-shrink: 0;
  }

  .input-wrap {
    display: flex;
    align-items: flex-end;
    gap: 8px;
    background: var(--bg-page);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    padding: 8px 8px 8px 14px;
    transition: border-color var(--t-mid), box-shadow var(--t-mid);
  }

  .input-wrap:focus-within {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-border);
  }

  .textarea {
    flex: 1;
    background: none;
    border: none;
    outline: none;
    resize: none;
    font-family: var(--font-sans);
    font-size: 13.5px;
    color: var(--text-primary);
    line-height: 1.6;
    min-height: 22px;
    max-height: 200px;
    padding: 0;
    caret-color: var(--accent);
    overflow-y: auto;
  }

  .textarea::placeholder {
    color: var(--text-disabled);
  }

  .textarea:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .send-btn {
    flex-shrink: 0;
    width: 30px;
    height: 30px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--accent);
    border: none;
    border-radius: var(--radius-md);
    color: #fff;
    cursor: pointer;
    transition: background var(--t-fast), opacity var(--t-fast);
  }

  .send-btn:hover:not(:disabled) {
    background: var(--accent-hover);
  }

  .send-btn:disabled {
    opacity: 0.35;
    cursor: not-allowed;
  }

  .spin-icon {
    animation: spin 0.9s linear infinite;
  }

  .hint {
    margin-top: 6px;
    font-size: 11px;
    color: var(--text-disabled);
    text-align: center;
    font-family: var(--font-sans);
  }

  kbd {
    display: inline-block;
    padding: 1px 4px;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: 3px;
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-tertiary);
  }
</style>
