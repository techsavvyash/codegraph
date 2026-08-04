<script lang="ts">
  /**
   * One collapsible tool-call row inside an assistant turn: tool name, args
   * summary, duration; expands to show the raw result. Presentational — all
   * data comes from the ToolCallRecord the store built.
   */
  import type { ToolCallRecord } from '$lib/types/chat'
  import { toolLabel, argsSummary } from '$lib/stores/chat.svelte'

  let { record }: { record: ToolCallRecord } = $props()

  let expanded = $state(false)

  const summary = $derived(argsSummary(record.args))
  const running = $derived(record.result === '')

  function truncate(s: string, max = 600): string {
    return s.length > max ? s.slice(0, max) + '…' : s
  }
</script>

<div class="tool" class:err={record.isError}>
  <button
    class="head"
    onclick={() => (expanded = !expanded)}
    aria-expanded={expanded}
    disabled={running}
  >
    <svg class="chev" class:open={expanded} width="9" height="9" viewBox="0 0 10 10" aria-hidden="true">
      <path d="M2 3.5L5 6.5L8 3.5" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
    {#if running}
      <span class="spin" aria-hidden="true"></span>
    {/if}
    <span class="name">{toolLabel(record.tool)}</span>
    {#if summary}<span class="args">{summary}</span>{/if}
    <span class="spacer"></span>
    {#if record.isError}
      <span class="badge err-badge">error</span>
    {:else if record.durationMs !== undefined}
      <span class="dur">{record.durationMs}ms</span>
    {/if}
  </button>

  {#if expanded && !running}
    <pre class="result"><code>{truncate(record.result)}</code></pre>
  {/if}
</div>

<style>
  .tool {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-subtle);
    overflow: hidden;
  }
  .tool.err {
    border-color: var(--err);
    background: var(--err-subtle);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 8px;
    background: none;
    border: none;
    cursor: pointer;
    text-align: left;
    font-family: var(--font-sans);
    font-size: var(--text-xs);
    color: var(--ink-2);
  }
  .head:disabled { cursor: default; }
  .chev { color: var(--ink-3); transition: transform 120ms; flex-shrink: 0; }
  .chev.open { transform: rotate(180deg); }
  .name { font-weight: 600; color: var(--ink); flex-shrink: 0; }
  .args {
    color: var(--ink-3);
    font-family: var(--font-mono);
    font-size: 10.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .spacer { flex: 1; }
  .dur { color: var(--ink-3); font-family: var(--font-mono); font-size: 10.5px; flex-shrink: 0; }
  .badge {
    font-size: 9.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    padding: 1px 5px;
    border-radius: var(--r-full);
    flex-shrink: 0;
  }
  .err-badge { background: var(--err); color: #fff; }
  .spin {
    width: 9px;
    height: 9px;
    border: 1.5px solid var(--border-strong);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: cd-spin 0.8s linear infinite;
    flex-shrink: 0;
  }
  .result {
    margin: 0;
    padding: 7px 10px;
    border-top: 1px solid var(--border);
    background: var(--bg-panel);
    font-family: var(--font-mono);
    font-size: 10.5px;
    line-height: 1.5;
    color: var(--ink-2);
    white-space: pre-wrap;
    word-break: break-word;
    overflow-x: auto;
    max-height: 200px;
    overflow-y: auto;
  }
  .result code { background: none; border: none; padding: 0; color: inherit; }
  @keyframes cd-spin { to { transform: rotate(360deg); } }
</style>
