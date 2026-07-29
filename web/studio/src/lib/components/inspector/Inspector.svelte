<script lang="ts">
  /**
   * Right-pane inspector for the selected node (RFC-012 R3): identity header,
   * properties, source/content, and grouped relationships with expand-onto-
   * canvas affordances. Design: design/studio/components/inspector.html
   * (Function + DocumentChunk variants) and the .insp pane in
   * design/studio/screens/graph.html.
   */
  import type { GraphEdge, GraphNode, SourceResponse } from '$lib/types/graph'
  import { nodeColors } from '$lib/components/canvas/elements'
  import { fmtConfidence } from '$lib/format'
  import { displayName, groupIncident, type EdgeGroup } from './edges'
  import SourcePane from './SourcePane.svelte'

  interface Props {
    node: GraphNode | null
    edges: GraphEdge[]
    allNodes: Map<string, GraphNode>
    loadSource: (nodeId: string) => Promise<SourceResponse | null>
    onExpandGroup: (nodeId: string, relType: string, direction: 'in' | 'out') => void
    onFocusNode: (nodeId: string) => void
    onClose: () => void
  }

  let { node, edges, allNodes, loadSource, onExpandGroup, onFocusNode, onClose }: Props = $props()

  const groups = $derived<EdgeGroup[]>(node ? groupIncident(node.node_id, edges) : [])

  let openGroupKey = $state<string | null>(null)
  let sigExpanded = $state(false)

  type SourceStatus = 'loading' | 'loaded' | 'error'
  let sourceStatus = $state<SourceStatus>('loading')
  let sourceResponse = $state<SourceResponse | null>(null)
  let sourceError = $state('source unavailable')

  let requestSeq = 0

  // Reset transient UI state and (re)load source whenever the selected node changes.
  $effect(() => {
    const current = node
    sigExpanded = false
    openGroupKey = null

    const seq = ++requestSeq
    if (!current) {
      sourceStatus = 'error'
      sourceResponse = null
      sourceError = 'source unavailable'
      return
    }

    sourceStatus = 'loading'
    sourceResponse = null
    void fetchSource(current.node_id, seq)
  })

  async function fetchSource(nodeId: string, seq: number) {
    try {
      const res = await loadSource(nodeId)
      if (seq !== requestSeq) return // a newer node selection has since superseded this request
      if (!res) {
        sourceStatus = 'error'
        sourceError = 'source unavailable'
        return
      }
      sourceResponse = res
      sourceStatus = 'loaded'
    } catch (err) {
      if (seq !== requestSeq) return
      sourceStatus = 'error'
      sourceError = err instanceof Error ? err.message : 'source unavailable'
    }
  }

  function groupKey(g: EdgeGroup): string {
    return `${g.relType}|${g.direction}`
  }

  function toggleGroup(g: EdgeGroup) {
    const key = groupKey(g)
    openGroupKey = openGroupKey === key ? null : key
  }

  function neighborName(nodeId: string): string {
    return allNodes.get(nodeId)?.name ?? nodeId
  }

  function neighborLabel(nodeId: string): string | undefined {
    return allNodes.get(nodeId)?.label
  }

  function isMentionProvenance(edge: GraphEdge): boolean {
    return edge.type === 'MENTIONS' && !!edge.strategy
  }

  function provenanceKind(strategy: string): 'docmine' | 'semlink' | null {
    if (strategy.startsWith('docmine')) return 'docmine'
    if (strategy.startsWith('semlink')) return 'semlink'
    return null
  }

  function locationText(n: GraphNode): string {
    if (!n.start_line) return n.file_path ?? ''
    return n.end_line ? `${n.file_path}:${n.start_line}-${n.end_line}` : `${n.file_path}:${n.start_line}`
  }
</script>

{#if !node}
  <div class="insp empty">
    <span class="placeholder">select a node</span>
  </div>
{:else}
  {@const colors = nodeColors(node.label)}
  <div class="insp">
    <div class="insp-head">
      <div class="head-row">
        <span class="insp-kind" style="color:{colors.fg}; background:{colors.bg}">
          <span class="dot" style="background:{colors.fg}"></span>{node.label}
        </span>
        <button class="close" onclick={onClose} aria-label="close inspector">&times;</button>
      </div>
      <div class="insp-name">{node.name}</div>
      {#if node.file_path || node.service}
        <div class="insp-sub">
          {#if node.file_path}<span class="mono">{locationText(node)}</span>{/if}
          {#if node.file_path && node.service} &middot; {/if}
          {#if node.service}{node.service}{/if}
        </div>
      {/if}
    </div>

    <div class="insp-body">
      <div class="isec">
        <h3>Properties</h3>
        {#if node.service}
          <div class="prop"><span class="k">service</span><span class="v">{node.service}</span></div>
        {/if}
        {#if node.file_path}
          <div class="prop">
            <span class="k">location</span>
            <span class="v mono">{locationText(node)}</span>
          </div>
        {/if}
        {#if node.signature}
          <div class="prop sig-prop">
            <span class="k">signature</span>
            <button
              class="v sig mono"
              class:clamped={!sigExpanded}
              onclick={() => (sigExpanded = !sigExpanded)}
              title={sigExpanded ? 'collapse' : 'expand'}
            >
              {node.signature}
            </button>
          </div>
        {/if}
        {#if sourceStatus === 'loaded' && sourceResponse?.range_source === 'scip-declaration'}
          <div class="prop">
            <span class="k">range</span>
            <span class="v tag warn">declaration line only</span>
          </div>
        {/if}
      </div>

      <div class="isec">
        <h3>{sourceResponse?.kind === 'document' || sourceResponse?.kind === 'chunk' ? 'Content' : 'Source'}</h3>
        <SourcePane status={sourceStatus} response={sourceResponse} errorMessage={sourceError} />
      </div>

      <div class="isec">
        <h3>Relationships</h3>
        {#if groups.length === 0}
          <p class="none">no relationships in the working set</p>
        {:else}
          {#each groups as group (groupKey(group))}
            {@const key = groupKey(group)}
            {@const isMentions = group.relType === 'MENTIONS'}
            <div class="egroup">
              <button class="egroup-row" onclick={() => toggleGroup(group)}>
                <span class="dir">{group.direction === 'out' ? '→' : '←'}</span>
                <span class="rel" style={isMentions ? `color:var(--edge-semlink)` : undefined}
                  >{displayName(group.relType, group.direction)}</span
                >
                <span class="cnt">{group.edges.length}</span>
              </button>
              <button
                class="plus"
                onclick={() => onExpandGroup(node!.node_id, group.relType, group.direction)}
                title="expand onto canvas"
              >
                +
              </button>
            </div>
            {#if openGroupKey === key}
              <div class="neighbors">
                {#each group.edges as inc (inc.edge.from + '|' + inc.edge.type + '|' + inc.edge.to)}
                  {@const nColors = nodeColors(neighborLabel(inc.neighborId) ?? '')}
                  <button class="mrow" onclick={() => onFocusNode(inc.neighborId)}>
                    <span class="ndot" style="background:{nColors.fg}"></span>
                    <span class="nm mono">{neighborName(inc.neighborId)}</span>
                    {#if isMentionProvenance(inc.edge)}
                      {@const kind = provenanceKind(inc.edge.strategy!)}
                      {#if kind}
                        <span class="badge {kind} mono">{kind} {fmtConfidence(inc.edge.confidence ?? 0)}</span>
                      {/if}
                    {/if}
                  </button>
                {/each}
              </div>
            {/if}
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .insp {
    background: var(--bg-panel);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    height: 100%;
  }
  .insp.empty {
    align-items: center;
    justify-content: center;
  }
  .placeholder {
    color: var(--ink-3);
    font-size: var(--text-sm);
  }

  .insp-head {
    padding: var(--s-3) var(--s-4);
    border-bottom: 1px solid var(--border);
  }
  .head-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .insp-kind {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    border-radius: var(--r-full);
    padding: 2px 9px;
  }
  .insp-kind .dot {
    width: 6px;
    height: 6px;
    border-radius: var(--r-full);
  }
  .close {
    font-size: 16px;
    line-height: 1;
    color: var(--ink-3);
    padding: 2px 4px;
  }
  .close:hover {
    color: var(--ink);
  }
  .insp-name {
    font-family: var(--font-mono);
    font-size: 15px;
    font-weight: 500;
    margin-top: 6px;
    word-break: break-word;
  }
  .insp-sub {
    font-size: var(--text-xs);
    color: var(--ink-3);
    margin-top: 2px;
  }

  .insp-body {
    overflow: auto;
    flex: 1;
  }
  .isec {
    padding: var(--s-3) var(--s-4);
    border-bottom: 1px solid var(--border);
  }
  .isec:last-child {
    border-bottom: none;
  }
  .isec h3 {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-3);
    margin-bottom: 8px;
  }

  .prop {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    font-size: var(--text-sm);
    padding: 2px 0;
  }
  .prop .k {
    color: var(--ink-3);
    flex: none;
  }
  .prop .v {
    font-size: var(--text-sm);
    text-align: right;
  }
  .prop .v.tag {
    font-size: 10px;
    border-radius: var(--r-full);
    padding: 1px 8px;
  }
  .prop .v.tag.warn {
    background: var(--warn-subtle);
    color: var(--warn);
  }
  .sig-prop {
    align-items: flex-start;
  }
  .sig {
    text-align: right;
    white-space: normal;
    word-break: break-word;
    cursor: pointer;
  }
  .sig.clamped {
    display: -webkit-box;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .none {
    font-size: var(--text-sm);
    color: var(--ink-disabled);
    font-style: italic;
  }

  .egroup {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 2px 0;
  }
  .egroup-row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
    padding: 4px 0;
    font-size: var(--text-sm);
    text-align: left;
  }
  .egroup-row:hover .rel {
    text-decoration: underline;
  }
  .egroup .dir {
    color: var(--ink-disabled);
    font-size: 10px;
    flex: none;
  }
  .egroup .rel {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    font-weight: 500;
  }
  .egroup .cnt {
    font-size: var(--text-xs);
    color: var(--ink-3);
    background: var(--bg-subtle);
    border-radius: var(--r-full);
    padding: 1px 8px;
    margin-left: auto;
  }
  .egroup .plus {
    color: var(--accent);
    font-size: var(--text-xs);
    flex: none;
    padding: 2px 6px;
  }
  .egroup .plus:hover {
    text-decoration: underline;
  }

  .neighbors {
    padding: 2px 0 6px 18px;
  }
  .mrow {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 5px 0;
    font-size: var(--text-sm);
    width: 100%;
    text-align: left;
  }
  .mrow:hover .nm {
    text-decoration: underline;
  }
  .mrow .ndot {
    width: 7px;
    height: 7px;
    border-radius: var(--r-full);
    flex: none;
  }
  .mrow .nm {
    font-size: var(--text-sm);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
  }
  .mrow .badge {
    font-size: 10px;
    font-weight: 500;
    border-radius: var(--r-full);
    padding: 1px 7px;
    flex: none;
  }
  .mrow .badge.semlink {
    background: var(--edge-semlink-bg);
    color: var(--edge-semlink);
  }
  .mrow .badge.docmine {
    background: var(--edge-docmine-bg);
    color: var(--edge-docmine);
  }
</style>
