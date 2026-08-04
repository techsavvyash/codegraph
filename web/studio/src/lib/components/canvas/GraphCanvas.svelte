<script lang="ts">
  /**
   * Presentational Cytoscape wrapper for the graph explorer (RFC-012 R2).
   * Owns no data: nodes/edges/selection/pins come in as props (the working
   * set), and all mutation intents go back out through CanvasEvents. All
   * cytoscape sync logic lives in `createCanvasController` (elements.ts) so
   * it's unit-testable without mounting this component.
   */
  import cytoscape from 'cytoscape'
  import { onMount, onDestroy } from 'svelte'
  import type { GraphEdge, GraphNode } from '$lib/types/graph'
  import type { CanvasEvents } from '$lib/types/workingset'
  import { canvasStyle, createCanvasController, type CanvasController } from './elements'

  let {
    nodes,
    edges,
    selected,
    pinned,
    onSelect,
    onTogglePin,
    onExpandRequest,
    onRemoveNode
  }: {
    nodes: GraphNode[]
    edges: GraphEdge[]
    selected: string | null
    pinned: string[]
  } & CanvasEvents = $props()

  let container: HTMLDivElement | undefined = $state()
  let cy: cytoscape.Core | undefined
  let controller: CanvasController | undefined

  onMount(() => {
    cy = cytoscape({
      container,
      style: canvasStyle,
      wheelSensitivity: 0.2
    })
    controller = createCanvasController(cy, { onSelect, onTogglePin, onExpandRequest })
    controller.sync(nodes, edges, selected, pinned)
  })

  onDestroy(() => {
    controller?.destroy()
    cy?.destroy()
  })

  $effect(() => {
    // Re-run on every change to the working set or selection/pin state.
    // svelte-ignore: reading these here is what makes the effect reactive.
    void nodes
    void edges
    void selected
    void pinned
    controller?.sync(nodes, edges, selected, pinned)
  })

  function handleFit() {
    cy?.fit(undefined, 40)
  }

  function handleZoomIn() {
    if (!cy) return
    cy.zoom({ level: cy.zoom() * 1.25, renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } })
  }

  function handleZoomOut() {
    if (!cy) return
    cy.zoom({ level: cy.zoom() / 1.25, renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } })
  }

  function handleRemove() {
    if (selected) onRemoveNode(selected)
  }
</script>

<div class="canvas" role="application" aria-label="Graph canvas">
  <div class="stage" bind:this={container}></div>

  {#if nodes.length === 0}
    <div class="empty">Canvas is empty — search and add nodes, or expand from a result</div>
  {/if}

  <div class="wsinfo">{nodes.length} nodes &middot; {edges.length} edges</div>

  <div class="ctools">
    <button class="ctool" type="button" title="Fit" onclick={handleFit}>&#9974;</button>
    <button class="ctool" type="button" title="Zoom in" onclick={handleZoomIn}>+</button>
    <button class="ctool" type="button" title="Zoom out" onclick={handleZoomOut}>&minus;</button>
    <button class="ctool" type="button" title="Remove" disabled={!selected} onclick={handleRemove}>&#10005;</button>
  </div>

  <div class="legend">
    <span class="li"><svg width="22" height="6"><line x1="0" y1="3" x2="22" y2="3" stroke="var(--edge-structural)" stroke-width="1.6" /></svg>structural</span>
    <span class="li"><svg width="22" height="6"><line x1="0" y1="3" x2="22" y2="3" stroke="var(--edge-docmine)" stroke-width="1.6" /></svg>docmine &ge;0.70</span>
    <span class="li"><svg width="22" height="6"><line x1="0" y1="3" x2="22" y2="3" stroke="var(--edge-semlink)" stroke-width="1.6" stroke-dasharray="5 4" /></svg>semlink &le;0.60</span>
  </div>
</div>

<style>
  .canvas {
    position: relative;
    width: 100%;
    height: 100%;
    overflow: hidden;
    background: var(--bg-canvas);
    background-image: radial-gradient(circle, #e3e6ea 1px, transparent 1px);
    background-size: 16px 16px;
  }

  .stage {
    position: absolute;
    inset: 0;
  }

  .empty {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    padding: 0 var(--s-8);
    text-align: center;
    color: var(--ink-3);
    font-size: var(--text-base);
    pointer-events: none;
  }

  .wsinfo {
    position: absolute;
    top: 12px;
    left: 12px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 4px 10px;
    font-size: 10.5px;
    color: var(--ink-3);
    box-shadow: var(--shadow-1);
    z-index: 2;
  }

  .ctools {
    position: absolute;
    top: 12px;
    right: 12px;
    display: flex;
    flex-direction: column;
    gap: 4px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 3px;
    box-shadow: var(--shadow-2);
    z-index: 2;
  }

  .ctool {
    width: 26px;
    height: 26px;
    display: grid;
    place-items: center;
    border-radius: var(--r-sm);
    color: var(--ink-2);
    font-size: 13px;
    cursor: pointer;
  }

  .ctool:hover:not(:disabled) {
    background: var(--bg-hover);
  }

  .ctool:disabled {
    color: var(--ink-disabled);
    cursor: not-allowed;
  }

  .legend {
    position: absolute;
    bottom: 12px;
    left: 12px;
    display: flex;
    gap: 12px;
    align-items: center;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 6px 12px;
    font-size: 10.5px;
    color: var(--ink-3);
    box-shadow: var(--shadow-1);
    z-index: 2;
  }

  .legend .li {
    display: flex;
    align-items: center;
    gap: 5px;
  }

  .legend svg {
    display: block;
  }
</style>
