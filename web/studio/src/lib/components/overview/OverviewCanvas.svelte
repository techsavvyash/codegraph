<script lang="ts">
  /**
   * Cytoscape canvas for the service Overview. Presentational: the visible
   * node/edge set comes in as props (RenderNode/RenderEdge from the pure model
   * via OverviewStore.graph), selection comes in, and tap/double-tap intents go
   * back out through hoisted callback props. Layout is ELK layered (registered
   * below) — a hierarchical top→down layout, NOT the workbench's fcose.
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
  import elk from 'cytoscape-elk'
  import { onMount, onDestroy } from 'svelte'
  import type { OverviewDecorations, RenderEdge, RenderNode } from '$lib/types/overview'
  import { nodeColors } from '$lib/components/canvas/elements'
  import { overviewStyle } from './style'

  /** Every lens node/edge class the reconciler may need to strip before applying the current one. */
  const LENS_CLASSES = [
    'hl-path',
    'usage-d0',
    'usage-d1',
    'usage-d2',
    'usage-d3',
    'heat-0',
    'heat-1',
    'heat-2',
    'heat-3',
    'heat-4',
    'dead-1',
    'dead-2',
    'dead-3'
  ]

  // Same double-registration guard as canvas/elements.ts uses for fcose.
  const elkRegistered = (cytoscape as unknown as { __elkRegistered?: boolean }).__elkRegistered
  if (!elkRegistered) {
    cytoscape.use(elk)
    ;(cytoscape as unknown as { __elkRegistered?: boolean }).__elkRegistered = true
  }

  let {
    nodes,
    edges,
    selected,
    decorations,
    onSelect,
    onToggle
  }: {
    nodes: RenderNode[]
    edges: RenderEdge[]
    selected: string | null
    decorations: OverviewDecorations
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
        // rendered only while the edge has .focus (incident to the selection)
        wlabel: String(e.weight),
        kind: e.kind
      }
    }
  }

  function idSet(list: { id: string }[]): Set<string> {
    return new Set(list.map((x) => x.id))
  }

  /**
   * Ghost wrap edges: ELK layered assigns symbols with the same call
   * relationships to the SAME layer, so a file with many sibling symbols (all
   * leaves, or all calling one hub) expands into a needle-thin column.
   * Chaining ALL of a compound's children alphabetically into ~sqrt(n) chunks
   * with invisible edges makes ELK lay the compound out as a compact,
   * scannable grid — real intra-file call edges still pull callees rightward
   * (ghost chains that conflict with real structure are just cycle-broken by
   * ELK), and because ELK knows the true compound size, surrounding nodes
   * never overlap it.
   */
  function ghostWrapEdges(): ElementDefinition[] {
    const byParent = new Map<string, RenderNode[]>()
    for (const n of nodes) {
      if (n.kind !== 'symbol' || !n.parentId) continue
      const list = byParent.get(n.parentId) ?? []
      list.push(n)
      byParent.set(n.parentId, list)
    }
    const ghosts: ElementDefinition[] = []
    for (const [parentId, children] of byParent) {
      if (children.length < 4) continue
      const ordered = [...children].sort((a, b) => a.label.localeCompare(b.label) || a.id.localeCompare(b.id))
      const col = Math.ceil(Math.sqrt(ordered.length))
      for (let i = 0; i + col < ordered.length; i += 1) {
        ghosts.push({
          group: 'edges',
          data: {
            id: `ghost:${parentId}:${i}`,
            source: ordered[i].id,
            target: ordered[i + col].id,
            weight: 0,
            wlabel: '',
            kind: 'ghost'
          }
        })
      }
    }
    return ghosts
  }

  /** Synthetic flow-segment edge (dashed accent, not hit-testable — skipped by
   * focus/dim reconciliation exactly like ghost edges). */
  function toFlowSegEl(seg: { id: string; source: string; target: string }): ElementDefinition {
    return {
      group: 'edges',
      data: { id: seg.id, source: seg.source, target: seg.target, weight: 0, wlabel: '', kind: 'flowseg' }
    }
  }

  /** Incrementally reconcile the cy instance to the current props. */
  function sync() {
    if (!cy) return
    const nextNodeIds = idSet(nodes)
    // Structure strong-mode: drop base edges not in the visible set. flowseg
    // edges (from the flows lens) join the base edges; ghosts always present.
    const filter = decorations.visibleEdgeIds
    const baseEdges = filter ? edges.filter((e) => filter.has(e.id)) : edges
    const flowSegs = decorations.extraEdges.filter(
      (seg) => nextNodeIds.has(seg.source) && nextNodeIds.has(seg.target)
    )
    const edgeDefs: ElementDefinition[] = [
      ...baseEdges.map(toEdgeEl),
      ...flowSegs.map(toFlowSegEl),
      ...ghostWrapEdges()
    ]
    const nextEdgeIds = new Set(edgeDefs.map((d) => String(d.data.id)))
    const curNodeIds = new Set(cy.nodes().map((n) => n.id()))
    const curEdgeIds = new Set(cy.edges().map((e) => e.id()))

    const addNodes = nodes.filter((n) => !curNodeIds.has(n.id))
    const addEdgeDefs = edgeDefs.filter((d) => !curEdgeIds.has(String(d.data.id)))
    const removeNodeIds = [...curNodeIds].filter((id) => !nextNodeIds.has(id))
    const removeEdgeIds = [...curEdgeIds].filter((id) => !nextEdgeIds.has(id))

    const structuralChange =
      addNodes.length > 0 || addEdgeDefs.length > 0 || removeNodeIds.length > 0 || removeEdgeIds.length > 0

    // Not batched — see the landmine note above.
    for (const id of removeEdgeIds) cy.getElementById(id).remove()
    for (const id of removeNodeIds) cy.getElementById(id).remove()

    // No position seeding needed here: ELK computes a full deterministic
    // layered layout on every structural change (unlike the workbench's
    // incremental fcose, which needs golden-angle seeds).
    const toAdd: ElementDefinition[] = [
      // parents (files) must exist before their symbol children reference them,
      // so add non-symbol nodes first
      ...addNodes.filter((n) => n.kind !== 'symbol').map(toEl),
      ...addNodes.filter((n) => n.kind === 'symbol').map(toEl),
      ...addEdgeDefs
    ]
    if (toAdd.length > 0) cy.add(toAdd)

    // Reconcile selection + lens decorations every sync (either can change
    // alone). Under Structure the selection drives focus/dim exactly as before;
    // under any other lens the LENS owns dimming (dimUnmatched) and the selection
    // still highlights (is-selected) + shows the panel but does not dim.
    const lensDimming = decorations.dimUnmatched
    const sel = selected ? cy.getElementById(selected) : cy.collection()
    const hasSel = sel.nonempty()
    const litNodeIds = new Set<string>()
    if (hasSel) {
      litNodeIds.add(sel.id())
      sel.ancestors().forEach((a) => {
        litNodeIds.add(a.id())
      })
      sel.descendants().forEach((d) => {
        litNodeIds.add(d.id())
      })
      sel.connectedEdges().forEach((e) => {
        if (e.data('kind') === 'ghost' || e.data('kind') === 'flowseg') return
        litNodeIds.add(e.source().id())
        litNodeIds.add(e.target().id())
      })
    }
    // Selection-driven dim applies only under Structure (lensDimming false).
    const selDimActive = hasSel && !lensDimming
    cy.nodes().forEach((n) => {
      // lens class: strip any stale one, apply the decorated one (O(classes))
      const want = decorations.nodeClasses.get(n.id())
      for (const c of LENS_CLASSES) {
        if (c !== want && n.hasClass(c)) n.removeClass(c)
      }
      if (want && !n.hasClass(want)) n.addClass(want)

      n.toggleClass('is-selected', n.id() === selected)
      // dim: (structure selection dim) OR (lens dim and this node has no lens
      // class and isn't the selection)
      const lensDim = lensDimming && !want && n.id() !== selected
      const selDim = selDimActive && !litNodeIds.has(n.id())
      n.toggleClass('dimmed', lensDim || selDim)
    })
    cy.edges().forEach((e) => {
      const kind = e.data('kind')
      if (kind === 'ghost' || kind === 'flowseg') return // layout-only / lens-only, never focus/dim reconciled
      const wantEdge = decorations.edgeClasses.get(e.id())
      if (wantEdge !== 'hl-path' && e.hasClass('hl-path')) e.removeClass('hl-path')
      if (wantEdge === 'hl-path' && !e.hasClass('hl-path')) e.addClass('hl-path')

      const incident = selDimActive && (e.source().id() === selected || e.target().id() === selected)
      e.toggleClass('focus', incident)
      // dim: structure selection dim as before; under a lens, dim any edge not
      // on the highlighted path (no hl-path class).
      const lensEdgeDim = lensDimming && wantEdge !== 'hl-path'
      const selEdgeDim =
        selDimActive && !incident && !(litNodeIds.has(e.source().id()) && litNodeIds.has(e.target().id()))
      e.toggleClass('dimmed', lensEdgeDim || selEdgeDim)
    })

    if (structuralChange && cy.nodes().length > 0) {
      const animate = cy.container() !== null
      // ELK layered: call direction flows top→down (callers above callees), so
      // the codebase reads as an architecture diagram instead of a force-
      // directed hairball. INCLUDE_CHILDREN layers symbols inside an expanded
      // file compound by their own call order; label dimensions count toward
      // spacing so the chips below dots never overlap.
      const layout = cy.layout({
        name: 'elk',
        animate,
        animationDuration: 300,
        fit: false,
        nodeDimensionsIncludeLabels: true,
        elk: {
          algorithm: 'layered',
          // RIGHT: callers left, callees right — landscape canvases get a wide
          // architecture diagram instead of a tall narrow column
          'elk.direction': 'RIGHT',
          'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
          'elk.layered.spacing.nodeNodeBetweenLayers': 80,
          'elk.spacing.nodeNode': 28,
          'elk.spacing.componentComponent': 70,
          'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES'
        }
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
    // reactive on the visible graph + selection + lens decorations
    void nodes
    void edges
    void selected
    void decorations
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
