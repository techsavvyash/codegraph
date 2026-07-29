<script lang="ts">
  /**
   * Flow spine stage (RFC-012 R4): chip+connector rendering of a traced call
   * flow, with depth guides. Design: design/studio/screens/flows.html .spine.
   * Pure presentational — layout math lives in layout.ts so it stays testable
   * without mounting Svelte.
   */
  import type { Flow, FlowStep } from '$lib/types/flows'
  import { nodeColors } from '$lib/components/canvas/elements'
  import { buildSpineTree, layoutSpine, type ChipPosition } from './layout'

  interface Props {
    flow: Flow | null
    steps: FlowStep[]
    selectedNodeId: string | null
    status: 'idle' | 'loading' | 'loaded' | 'error'
    error: string
    onSelectStep: (step: FlowStep) => void
    onLoadCanvas: () => void
  }

  let { flow, steps, selectedNodeId, status, error, onSelectStep, onLoadCanvas }: Props = $props()

  const layout = $derived(layoutSpine(buildSpineTree(steps)))

  const canLoadCanvas = $derived(status === 'loaded' && steps.some((s) => s.nodeId))

  function selectChip(chip: ChipPosition) {
    if (!chip.step.nodeId) return
    onSelectStep(chip.step)
  }

  function locationText(step: FlowStep): string | null {
    if (!step.filePath) return null
    return step.startLine ? `${step.filePath}:${step.startLine}` : step.filePath
  }
</script>

<div class="spine">
  <div class="spine-head">
    {#if flow}
      <span class="spine-title">Flow &middot; <span class="fn mono">{flow.flowName}()</span></span>
      <span class="spine-meta"
        >depth {Math.max(0, ...steps.map((s) => s.depth))} &middot; {steps.length} steps</span
      >
    {:else}
      <span class="spine-title">Flow</span>
    {/if}
    <button class="btn-accent" disabled={!canLoadCanvas} onclick={onLoadCanvas}>
      Load onto canvas
    </button>
  </div>

  <div class="stage-wrap">
    {#if status === 'idle'}
      <div class="centerhint">Select an entry point to trace its flow</div>
    {:else if status === 'loading'}
      <div class="centerhint">tracing flow&hellip;</div>
    {:else if status === 'error'}
      <div class="centerhint err">{error}</div>
    {:else if steps.length === 0}
      <div class="centerhint">this flow has no steps</div>
    {:else}
      <div class="stage" style="width:{layout.width}px; height:{layout.height}px">
        {#each layout.depthGuides as guide (guide.depth)}
          <div class="dguide" style="left:{guide.x}px"></div>
          <span class="dlabel" style="left:{guide.x}px">d{guide.depth}</span>
        {/each}

        <svg style="position:absolute; inset:0; width:100%; height:100%">
          <defs>
            <marker
              id="flow-arrow"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="7"
              markerHeight="7"
              orient="auto"
            >
              <path d="M0 0 L8 4 L0 8 z" fill="#ADB5BD" />
            </marker>
          </defs>
          {#each layout.connectors as conn (conn.parentKey + '|' + conn.childKey)}
            <path
              d={conn.path}
              fill="none"
              stroke="#ADB5BD"
              stroke-width="1.4"
              marker-end="url(#flow-arrow)"
            />
          {/each}
        </svg>

        {#each layout.connectors as conn (conn.parentKey + '|' + conn.childKey + '|label')}
          <span class="clabel" style="left:{conn.labelX}px; top:{conn.labelY}px">CALLS</span>
        {/each}

        {#each layout.chips as chip (chip.step.nodeKey)}
          {@const colors = nodeColors(chip.step.label)}
          {@const loc = locationText(chip.step)}
          {@const missing = !chip.step.nodeId}
          <div
            class="step"
            class:sel={chip.step.nodeId === selectedNodeId}
            style="left:{chip.x}px; top:{chip.y}px"
          >
            <button
              class="chip"
              class:missing
              disabled={missing}
              title={missing ? 'node no longer in the graph' : undefined}
              onclick={() => selectChip(chip)}
            >
              <span class="dot" style="background:{colors.fg}"></span>
              <span class="id mono">{chip.step.name}()</span>
            </button>
            {#if loc}
              <span class="floc mono">{loc}</span>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .spine {
    background: var(--bg-canvas);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    height: 100%;
    min-width: 0;
  }
  .spine-head {
    display: flex;
    align-items: center;
    gap: var(--s-3);
    background: var(--bg-panel);
    border-bottom: 1px solid var(--border);
    padding: var(--s-2) var(--s-4);
    flex: none;
  }
  .spine-title {
    font-size: var(--text-md);
    font-weight: 600;
  }
  .spine-title .fn {
    font-weight: 500;
  }
  .spine-meta {
    font-size: var(--text-xs);
    color: var(--ink-3);
  }
  .btn-accent {
    margin-left: auto;
    background: var(--accent);
    color: #fff;
    font-size: var(--text-sm);
    font-weight: 500;
    border-radius: var(--r-md);
    padding: 6px 14px;
    cursor: pointer;
    box-shadow: var(--shadow-1);
  }
  .btn-accent:hover:not(:disabled) {
    background: var(--accent-hover);
  }
  .btn-accent:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .stage-wrap {
    position: relative;
    flex: 1;
    overflow: auto;
    background-image: radial-gradient(circle, #e9ecef 1px, transparent 1px);
    background-size: 22px 22px;
  }
  .centerhint {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--ink-3);
    font-size: var(--text-sm);
  }
  .centerhint.err {
    color: var(--err);
  }

  .stage {
    position: relative;
  }

  .dguide {
    position: absolute;
    top: 30px;
    bottom: 16px;
    width: 0;
    border-left: 1px dashed var(--border-strong);
  }
  .dlabel {
    position: absolute;
    top: 8px;
    transform: translateX(-50%);
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-disabled);
    background: var(--bg-canvas);
    padding: 0 4px;
  }

  .clabel {
    position: absolute;
    transform: translate(-50%, -50%);
    font-family: var(--font-mono);
    font-size: 9px;
    font-weight: 500;
    color: var(--ink-3);
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-full);
    padding: 0 6px;
    z-index: 1;
  }

  .step {
    position: absolute;
    display: flex;
    flex-direction: column;
    gap: 3px;
    z-index: 1;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 3px 10px 3px 8px;
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--ink-2);
    box-shadow: var(--shadow-1);
    cursor: pointer;
    line-height: 1.4;
    align-self: flex-start;
  }
  .chip:hover:not(:disabled) {
    background: var(--bg-hover);
  }
  .chip.missing {
    opacity: 0.55;
    cursor: default;
  }
  .chip .dot {
    width: 8px;
    height: 8px;
    border-radius: var(--r-full);
    flex: none;
  }
  .chip .id {
    font-size: var(--text-sm);
    color: var(--ink);
  }
  .step.sel .chip {
    border-color: var(--accent);
    box-shadow: 0 0 0 2px var(--accent-subtle);
  }
  .step.sel .chip .id {
    color: var(--accent-ink);
  }
  .step .floc {
    font-size: 10px;
    color: var(--ink-3);
    padding-left: 4px;
    background: rgba(255, 255, 255, 0.85);
    border-radius: var(--r-sm);
    align-self: flex-start;
  }
</style>
