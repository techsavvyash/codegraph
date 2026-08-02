<script lang="ts">
  /**
   * Right-side detail panel for the Overview visualizer, mirroring the workbench
   * inspector's width/styling. Presentational: the selected RenderNode + the
   * visible graph (for top connections) come in as props, and every action
   * (expand/collapse, select-a-connection, open-in-workbench, retry-drilldown)
   * goes back out through hoisted callback props. Buttons here are the
   * e2e-reliable path for expand/collapse; the canvas double-tap is the fast path.
   */
  import type { LensId, RenderNode, SymbolCaller, VisibleGraph } from '$lib/types/overview'
  import { topConnections } from './model'

  interface Props {
    node: RenderNode | null
    graph: VisibleGraph
    /** true when this file's drilldown fetch is in flight */
    drillLoading: boolean
    /** per-file drilldown error message, if any */
    drillError: string | null
    /** active lens — drives the per-lens panel sections below the base props */
    lens?: LensId
    /** Usage lens: 1-hop callers of the selected symbol (null until loaded) */
    symbolCallers?: SymbolCaller[] | null
    symbolCallersLoading?: boolean
    /** Dead lens: dead-function names in the selected file/dir */
    deadNames?: string[]
    onExpand: (node: RenderNode) => void
    onCollapse: (node: RenderNode) => void
    onSelectConnection: (nodeId: string) => void
    onOpenInWorkbench: (node: RenderNode) => void
    onRetryDrill: (node: RenderNode) => void
    /** Usage lens: select the file node behind a caller row */
    onSelectCaller?: (filePath: string) => void
  }

  let {
    node,
    graph,
    drillLoading,
    drillError,
    lens = 'structure',
    symbolCallers = null,
    symbolCallersLoading = false,
    deadNames = [],
    onExpand,
    onCollapse,
    onSelectConnection,
    onOpenInWorkbench,
    onRetryDrill,
    onSelectCaller
  }: Props = $props()

  const conns = $derived(node ? topConnections(graph, node.id) : { incoming: [], outgoing: [] })

  // Is the selected file currently expanded? A compound file has symbol children
  // in the render set (parentId === node.id).
  const isFileExpanded = $derived(
    node?.kind === 'file' && graph.nodes.some((n) => n.parentId === node.id)
  )
</script>

{#if node}
  <div class="panel" data-testid="overview-panel">
    <div class="kind kind-{node.kind}">{node.kind}</div>
    <h2 class="title" title={node.label}>{node.label || '(root)'}</h2>

    {#if node.kind === 'dir'}
      <dl class="props">
        <dt>files</dt>
        <dd>{node.fileCount ?? 0}</dd>
        <dt>symbols</dt>
        <dd>{node.symbolCount ?? 0}</dd>
      </dl>
    {:else if node.kind === 'file'}
      <dl class="props">
        <dt>path</dt>
        <dd class="mono wrap">{node.path ?? node.label}</dd>
        <dt>language</dt>
        <dd>{node.language ?? '—'}</dd>
        <dt>lines</dt>
        <dd>{node.lineCount ?? 0}</dd>
        <dt>symbols</dt>
        <dd>{node.symbolCount ?? 0}</dd>
      </dl>
    {:else}
      <dl class="props">
        <dt>kind</dt>
        <dd>{node.symbolLabel ?? 'Function'}</dd>
        <dt>line</dt>
        <dd>{node.startLine ?? '—'}</dd>
        <dt>callers</dt>
        <dd>{node.inCalls ?? 0}</dd>
      </dl>
      {#if (node.externalOutCalls ?? 0) > 0}
        <p class="note">
          {node.externalOutCalls} out-call{(node.externalOutCalls ?? 0) === 1 ? '' : 's'} to other services (not drawn)
        </p>
      {/if}
    {/if}

    <!-- expand / collapse (dirs and files) -->
    {#if node.kind === 'dir'}
      <button class="act" data-testid="overview-toggle-dir" onclick={() => onExpand(node)}>
        Expand / collapse
      </button>
    {:else if node.kind === 'file'}
      {#if isFileExpanded}
        <button class="act" data-testid="overview-collapse-file" onclick={() => onCollapse(node)}>
          Collapse
        </button>
      {:else}
        <button
          class="act"
          data-testid="overview-expand-file"
          disabled={drillLoading}
          onclick={() => onExpand(node)}
        >
          {drillLoading ? 'Loading symbols…' : 'Expand symbols'}
        </button>
      {/if}
      {#if drillError}
        <div class="drillerr">
          <span>{drillError}</span>
          <button class="retry" onclick={() => onRetryDrill(node)}>retry</button>
        </div>
      {/if}
    {/if}

    <!-- top connections -->
    {#if conns.outgoing.length > 0 || conns.incoming.length > 0}
      <section class="conns">
        {#if conns.outgoing.length > 0}
          <h3>calls out</h3>
          <ul>
            {#each conns.outgoing as c (c.nodeId)}
              <li>
                <button class="conn" onclick={() => onSelectConnection(c.nodeId)}>
                  <span class="cname" title={c.label}>{c.label}</span>
                  <span class="cweight">{c.weight}</span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
        {#if conns.incoming.length > 0}
          <h3>called by</h3>
          <ul>
            {#each conns.incoming as c (c.nodeId)}
              <li>
                <button class="conn" onclick={() => onSelectConnection(c.nodeId)}>
                  <span class="cname" title={c.label}>{c.label}</span>
                  <span class="cweight">{c.weight}</span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/if}

    <!-- Usage lens: 1-hop callers of the selected symbol -->
    {#if lens === 'usage' && node.kind === 'symbol'}
      <section class="conns" data-testid="usage-callers">
        <h3>called by</h3>
        {#if symbolCallersLoading}
          <p class="note">loading callers…</p>
        {:else if !symbolCallers || symbolCallers.length === 0}
          <p class="note">no direct callers found</p>
        {:else}
          <ul>
            {#each symbolCallers as c, i (c.filePath + ':' + c.name + ':' + i)}
              <li>
                <button
                  class="conn"
                  onclick={() => onSelectCaller?.(c.filePath)}
                  disabled={!c.filePath || !onSelectCaller}
                >
                  <span class="cname mono" title={c.name}>{c.name}</span>
                  <span class="cweight" title={c.filePath}>{c.filePath}</span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/if}

    <!-- Dead lens: dead-function names in the selected file/dir -->
    {#if lens === 'dead' && node.kind !== 'symbol' && deadNames.length > 0}
      <section class="conns" data-testid="dead-symbols">
        <h3>dead functions ({deadNames.length})</h3>
        <ul>
          {#each deadNames as name, i (name + ':' + i)}
            <li class="deadname mono">{name}</li>
          {/each}
        </ul>
      </section>
    {/if}

    <!-- workbench handoff (files + symbols) -->
    {#if node.kind !== 'dir'}
      <button class="act ghost" data-testid="overview-open-workbench" onclick={() => onOpenInWorkbench(node)}>
        Open in workbench →
      </button>
    {/if}
  </div>
{/if}

<style>
  .panel {
    padding: var(--s-4);
    display: flex;
    flex-direction: column;
    gap: var(--s-3);
  }
  .kind {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--ink-3);
  }
  .kind-dir {
    color: #7048e8;
  }
  .kind-file {
    color: #495057;
  }
  .kind-symbol {
    color: #1c7ed6;
  }
  .title {
    font-size: var(--text-lg);
    font-weight: 600;
    color: var(--ink);
    word-break: break-word;
    margin: 0;
  }
  .props {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 4px 12px;
    margin: 0;
    font-size: var(--text-sm);
  }
  .props dt {
    color: var(--ink-3);
  }
  .props dd {
    margin: 0;
    color: var(--ink);
  }
  .mono {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }
  .wrap {
    word-break: break-all;
  }
  .note {
    font-size: var(--text-xs);
    color: var(--ink-3);
    font-style: italic;
    margin: 0;
  }
  .act {
    background: var(--accent);
    color: #fff;
    border-radius: var(--r-md);
    padding: 6px 12px;
    font-size: var(--text-sm);
    font-weight: 500;
    text-align: center;
  }
  .act:hover:not(:disabled) {
    background: var(--accent-hover);
  }
  .act:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .act.ghost {
    background: transparent;
    color: var(--accent-ink);
    border: 1px solid var(--border);
  }
  .act.ghost:hover {
    background: var(--bg-hover);
  }
  .drillerr {
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--err-subtle);
    border: 1px solid var(--err);
    border-radius: var(--r-md);
    padding: 6px 10px;
    font-size: var(--text-xs);
    color: var(--err);
  }
  .drillerr .retry {
    text-decoration: underline;
    color: var(--err);
  }
  .conns h3 {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--ink-3);
    margin: 0 0 4px;
  }
  .conns ul {
    list-style: none;
    margin: 0 0 var(--s-2);
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .conn {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 3px 8px;
    border-radius: var(--r-sm);
    font-size: var(--text-sm);
    color: var(--ink);
  }
  .conn:hover {
    background: var(--bg-hover);
  }
  .cname {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cweight {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--ink-3);
    flex: none;
    max-width: 55%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .conn:disabled {
    cursor: default;
  }
  .deadname {
    font-size: var(--text-xs);
    color: var(--err);
    padding: 2px 8px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
