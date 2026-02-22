<script lang="ts">
  export let activity: string
</script>

<div class="tool-activity" role="status" aria-live="polite">
  <span class="icon">
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M12 2a10 10 0 1 0 10 10"
        stroke="currentColor"
        stroke-width="2.5"
        stroke-linecap="round"
      />
    </svg>
  </span>
  <span class="label">{activity}</span>
  <span class="dots">
    <span class="dot" />
    <span class="dot" />
    <span class="dot" />
  </span>
</div>

<style>
  .tool-activity {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 5px 10px 5px 8px;
    background: var(--cyan-dim);
    border: 1px solid var(--cyan-soft);
    border-radius: var(--radius-md);
    color: var(--cyan);
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.03em;
    animation: slide-in-left var(--t-mid) both;
  }

  .icon {
    display: flex;
    align-items: center;
    animation: spin 1s linear infinite;
    color: var(--cyan);
  }

  .label {
    color: var(--cyan-bright);
  }

  .dots {
    display: flex;
    gap: 3px;
    align-items: center;
  }

  .dot {
    width: 3px;
    height: 3px;
    border-radius: 50%;
    background: var(--cyan-soft);
    animation: pulse-amber 1.2s ease-in-out infinite;
  }

  .dot:nth-child(2) { animation-delay: 0.2s; }
  .dot:nth-child(3) { animation-delay: 0.4s; }
</style>
