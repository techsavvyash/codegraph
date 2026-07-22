<script lang="ts">
  interface Props {
    message: string
    onRetry: () => void
  }

  let { message, onRetry }: Props = $props()
</script>

<div class="error-banner" role="alert">
  <span class="txt">Couldn't load the dashboard &mdash; {message}</span>
  <button class="retry" onclick={onRetry}>Retry</button>
</div>

<style>
  .error-banner {
    display: flex;
    align-items: center;
    gap: var(--s-3);
    background: var(--err-subtle);
    border: 1px solid var(--err);
    border-radius: var(--r-lg);
    padding: var(--s-3) var(--s-4);
    color: var(--err);
    font-size: var(--text-sm);
  }
  .txt {
    flex: 1;
  }
  .retry {
    flex: none;
    font-size: var(--text-xs);
    font-weight: 500;
    color: var(--err);
    background: var(--bg-panel);
    border: 1px solid var(--err);
    border-radius: var(--r-md);
    padding: 4px 10px;
  }
  .retry:hover {
    background: var(--err-subtle);
  }
</style>
