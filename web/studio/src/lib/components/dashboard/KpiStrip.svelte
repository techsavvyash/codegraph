<script lang="ts">
  import type { DashboardTotals } from '$lib/types/dashboard'
  import { fmtInt } from '$lib/format'

  interface Props {
    totals: DashboardTotals
  }

  let { totals }: Props = $props()

  const docLinksTotal = $derived(totals.docLinks.docmine + totals.docLinks.semlink)
</script>

<div class="kpis">
  <div class="kpi">
    <div class="num">{fmtInt(totals.services)}</div>
    <div class="lbl">services</div>
  </div>
  <div class="kpi">
    <div class="num">{fmtInt(totals.nodesByLabel.Symbol ?? 0)}</div>
    <div class="lbl">symbols</div>
  </div>
  <div class="kpi">
    <div class="num">{fmtInt(totals.edgesByType.CALLS ?? 0)}</div>
    <div class="lbl">CALLS edges</div>
  </div>
  <div class="kpi">
    <div class="num">{fmtInt(docLinksTotal)}</div>
    <div class="lbl">doc links</div>
    {#if docLinksTotal > 0}
      <div class="sub">
        <span class="dm">{fmtInt(totals.docLinks.docmine)} docmine</span> &middot;
        <span class="sl">{fmtInt(totals.docLinks.semlink)} semlink</span>
      </div>
    {/if}
  </div>
</div>

<style>
  .kpis {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: var(--s-3);
  }
  .kpi {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    padding: 12px 16px;
    box-shadow: var(--shadow-1);
  }
  .num {
    font-size: var(--text-2xl);
    font-weight: 600;
    letter-spacing: -0.02em;
    line-height: 1.2;
    font-variant-numeric: tabular-nums;
  }
  .lbl {
    font-size: var(--text-sm);
    color: var(--ink-3);
  }
  .sub {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-3);
    margin-top: 2px;
  }
  .sub .dm {
    color: var(--edge-docmine);
  }
  .sub .sl {
    color: var(--edge-semlink);
  }
</style>
