<script lang="ts">
  /**
   * Cytoscape canvas for the service Overview. Presentational: the visible
   * node/edge set comes in as props (RenderNode/RenderEdge from the pure model
   * via OverviewStore.graph), selection comes in, and tap/double-tap intents go
   * back out through hoisted callback props. Layout is fcose (already registered
   * by canvas/elements.ts, which we import for its color tokens).
   *
   * Landmines honored (see canvas/elements.ts + visibility.test.ts):
   *  - never width:'label' (aborts the stylesheet → invisible nodes); node width
   *    is a numeric function of symbolCount / label length;
   *  - never cy.add() inside cy.batch() (added elements never get initial style);
   *  - destroy the instance on unmount; re-layout only on node-set change,
   *    preserving surviving node positions.
   */
  import cytoscape from 'cytoscape'
  import type { Core, ElementDefinition, EventObject, LayoutOptions, NodeSingular } from 'cytoscape'
  import { onMount, onDestroy } from 'svelte'
  import type { RenderEdge, RenderNode } from '$lib/types/overview'
  import { nodeColors } from '$lib/components/canvas/elements'
  import { overviewStyle } from './style'

  let {
    nodes,
    edges,
    selected,
    onSelect,
    onToggle
  }: {
    nodes: RenderNode[]
    edges: RenderEdge[]
    selected: string | null
    onSelect: (id: string | null) => void
    onToggle: (id: string) => void
  } = $props()

  let container: HTMLDivElement | undefined = $state()
  let cy: Core | undefined

  // A node's cytoscape data record — the style function reads these.
  function toEl(n: RenderNode): ElementDefinition {
    const colors =
      n.kind === 'symbol'
        ? nodeColors(n.symbolLabel ?? 'Function')
        : n.kind === 'file'
          ? nodeColors('File')
          : nodeColors('Service') // dirs borrow the Service (purple) palette
    return {
      group: 'nodes',
      data: {
        id: n.id,
        kind: n.kind,
        label: n.label,
        parent: n.parentId,
        symbolCount: n.symbolCount ?? 0,
        fg: colors.fg,
        bg: colors.bg
      }
    }
  }

  function toEdgeEl(e: RenderEdge): ElementDefinition {
    return {
      group: 'edges',
      data: {
        id: e.id,
        source: e.source,
        target: e.target,
        weight: e.weight,
        // label only when the edge is heavy enough that a number aids reading
        wlabel: e.weight >= 3 ? String(e.weight) : '',
        kind: e.kind
      }
    }
  }

  function idSet(list: { id: string }[]): Set<string> {
    return new Set(list.map((x) => x.id))
  }

  /** Incrementally reconcile the cy instance to the current props. */
  function sync() {
    if (!cy) return
    const nextNodeIds = idSet(nodes)
    const nextEdgeIds = idSet(edges)
    const curNodeIds = new Set(cy.nodes().map((n) => n.id()))
    const curEdgeIds = new Set(cy.edges().map((e) => e.id()))

    const addNodes = nodes.filter((n) => !curNodeIds.has(n.id))
    const addEdges = edges.filter((e) => !curEdgeIds.has(e.id))
    const removeNodeIds = [...curNodeIds].filter((id) => !nextNodeIds.has(id))
    const removeEdgeIds = [...curEdgeIds].filter((id) => !nextEdgeIds.has(id))

    const structuralChange =
      addNodes.length > 0 || addEdges.length > 0 || removeNodeIds.length > 0 || removeEdgeIds.length > 0

    // Not batched — see the landmine note above.
    for (const id of removeEdgeIds) cy.getElementById(id).remove()
    for (const id of removeNodeIds) cy.getElementById(id).remove()

    // Seed positions for new nodes: fcose with randomize:false starts from
    // current coordinates, and a pile of nodes at the same spot is a degenerate
    // start it cannot untangle (same fix as the workbench canvas controller).
    // Anchor each new node near a connected already-placed node — symbols near
    // their parent file — then scatter on a deterministic golden-angle spiral.
    const placed = new Set(curNodeIds)
    for (const id of removeNodeIds) placed.delete(id)
    const anchorFor = (n: RenderNode): { x: number; y: number } => {
      if (n.parentId && placed.has(n.parentId)) {
        const pos = cy!.getElementById(n.parentId).position()
        return { x: pos.x, y: pos.y }
      }
      for (const e of edges) {
        const other = e.source === n.id ? e.target : e.target === n.id ? e.source : null
        if (other && placed.has(other)) {
          const pos = cy!.getElementById(other).position()
          return { x: pos.x, y: pos.y }
        }
      }
      if (cy!.nodes().length > 0) {
        const bb = cy!.nodes().boundingBox()
        return { x: (bb.x1 + bb.x2) / 2, y: (bb.y1 + bb.y2) / 2 }
      }
      return { x: 0, y: 0 }
    }
    let seedIndex = 0
    const seeded = (n: RenderNode): ElementDefinition => {
      const el = toEl(n)
      const anchor = anchorFor(n)
      const angle = seedIndex * 2.39996 // golden angle — stable, no Math.random
      const radius = 90 + 24 * Math.sqrt(seedIndex)
      seedIndex += 1
      el.position = { x: anchor.x + Math.cos(angle) * radius, y: anchor.y + Math.sin(angle) * radius }
      return el
    }

    const toAdd: ElementDefinition[] = [
      // parents (files) must exist before their symbol children reference them,
      // so add non-symbol nodes first
      ...addNodes.filter((n) => n.kind !== 'symbol').map(seeded),
      ...addNodes.filter((n) => n.kind === 'symbol').map(seeded),
      ...addEdges.map(toEdgeEl)
    ]
    if (toAdd.length > 0) cy.add(toAdd)

    // reconcile selection class every sync (selection can change alone)
    cy.nodes().forEach((n) => {
      n.toggleClass('is-selected', n.id() === selected)
    })

    if (structuralChange && cy.nodes().length > 0) {
      const animate = cy.container() !== null
      const layout = cy.layout({
        name: 'fcose',
        animate,
        animationDuration: 300,
        randomize: false,
        fit: false,
        // give compound (expanded file) nodes room for their symbol children
        nodeSeparation: 90,
        packComponents: true,
        // overview nodes are large boxes (up to ~100px) — fcose defaults assume
        // small dots and leave them overlapping
        nodeRepulsion: 12000,
        idealEdgeLength: 220
      } as unknown as LayoutOptions)
      if (animate) {
        layout.one('layoutstop', () => {
          cy?.animate({ fit: { eles: cy.elements(), padding: 50 }, duration: 200 })
        })
      }
      layout.run()
    }
  }

  // Bound event handlers (removed on destroy).
  const handleBgTap = (evt: EventObject) => {
    if (evt.target === cy) onSelect(null)
  }
  const handleNodeTap = (evt: EventObject) => {
    onSelect((evt.target as NodeSingular).id())
  }
  const handleNodeDblTap = (evt: EventObject) => {
    onToggle((evt.target as NodeSingular).id())
  }

  onMount(() => {
    cy = cytoscape({ container, style: overviewStyle, wheelSensitivity: 0.2 })
    cy.on('tap', handleBgTap)
    cy.on('tap', 'node', handleNodeTap)
    cy.on('dbltap', 'node', handleNodeDblTap)
    sync()
  })

  onDestroy(() => {
    if (!cy) return
    cy.removeListener('tap', handleBgTap)
    cy.removeListener('tap', 'node', handleNodeTap)
    cy.removeListener('dbltap', 'node', handleNodeDblTap)
    cy.destroy()
  })

  $effect(() => {
    // reactive on the visible graph + selection
    void nodes
    void edges
    void selected
    sync()
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
</script>

<div class="ocanvas" role="application" aria-label="Service overview canvas">
  <div class="stage" bind:this={container}></div>

  {#if nodes.length === 0}
    <div class="empty">Nothing to show — this service has no files, or the graph failed to load.</div>
  {/if}

  <div class="ctools">
    <button class="ctool" type="button" title="Fit" onclick={handleFit}>&#9974;</button>
    <button class="ctool" type="button" title="Zoom in" onclick={handleZoomIn}>+</button>
    <button class="ctool" type="button" title="Zoom out" onclick={handleZoomOut}>&minus;</button>
  </div>
</div>

<style>
  .ocanvas {
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
  .ctool:hover {
    background: var(--bg-hover);
  }
</style>
