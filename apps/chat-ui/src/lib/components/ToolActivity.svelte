<script lang="ts">
  export let activity: string
</script>

<div class="tool-activity" role="status" aria-live="polite">
  <svg class="spin-icon" width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <path
      d="M12 2a10 10 0 1 0 10 10"
      stroke="currentColor"
      stroke-width="2.5"
      stroke-linecap="round"
    />
  </svg>
  <span class="label">{activity}</span>
</div>

<style>
  .tool-activity {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 4px 10px 4px 8px;
    background: var(--accent-subtle);
    border: 1px solid var(--accent-border);
    border-radius: var(--radius-full);
    color: var(--accent-dark);
    font-family: var(--font-sans);
    font-size: 11.5px;
    font-weight: 500;
    animation: slide-in-left var(--t-mid) both;
  }

  .spin-icon {
    color: var(--accent);
    animation: spin 0.9s linear infinite;
    flex-shrink: 0;
  }

  .label {
    color: var(--accent-dark);
  }
</style>
