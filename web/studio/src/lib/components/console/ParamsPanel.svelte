<script lang="ts">
  /**
   * Params panel (RFC-012 R8 "parameter support"): a collapsible JSON-object
   * editor for $named query parameters. Collapsed by default; a badge shows the
   * key count when non-empty. Validation is client-side (params.ts) — invalid
   * JSON shows an inline message and, via the parent, disables Run. Empty text
   * means no params are sent.
   */
  import { validateParams } from './params'

  interface Props {
    value: string
    /** Whether the panel is expanded. */
    open: boolean
  }
  let { value = $bindable(), open = $bindable() }: Props = $props()

  const validation = $derived(validateParams(value))
</script>

<div class="params" class:invalid={!validation.valid}>
  <button class="head" onclick={() => (open = !open)} aria-expanded={open}>
    <span class="tw">{open ? '▾' : '▸'}</span>
    Params
    {#if validation.count > 0}
      <span class="badge">{validation.count}</span>
    {/if}
    {#if !validation.valid}
      <span class="badge err">!</span>
    {/if}
  </button>
  {#if open}
    <div class="body">
      <textarea
        bind:value
        spellcheck="false"
        autocapitalize="off"
        autocomplete="off"
        placeholder={'{ "name": "codegraph" }'}
      ></textarea>
      {#if validation.error}
        <p class="msg" role="alert">{validation.error}</p>
      {:else}
        <p class="msg hint">JSON object of $named params. Leave empty for none.</p>
      {/if}
    </div>
  {/if}
</div>

<style>
  .params {
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    background: var(--bg-panel);
    overflow: hidden;
  }
  .params.invalid {
    border-color: var(--err);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    text-align: left;
    padding: 6px var(--s-3);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink-2);
    background: var(--bg-subtle);
  }
  .head:hover {
    color: var(--ink);
  }
  .tw {
    color: var(--ink-3);
  }
  .badge {
    min-width: 16px;
    padding: 0 5px;
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 600;
    line-height: 16px;
    text-align: center;
    color: var(--accent-ink);
    background: var(--accent);
    border-radius: var(--r-full);
  }
  .badge.err {
    color: #fff;
    background: var(--err);
  }
  .body {
    padding: var(--s-2) var(--s-3);
    border-top: 1px solid var(--border);
  }
  textarea {
    width: 100%;
    resize: vertical;
    min-height: 64px;
    padding: var(--s-2);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    line-height: 1.5;
    color: var(--ink);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    outline: none;
  }
  .msg {
    margin: 4px 0 0;
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .msg.hint {
    color: var(--ink-3);
  }
  .msg:not(.hint) {
    color: var(--err);
  }
</style>
