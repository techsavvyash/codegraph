/**
 * fetch with a hard deadline. A downed backend behind the tailscale proxy
 * hangs the connection instead of refusing it, so a plain fetch never
 * settles and every "loading…" state it gates sticks forever. Deadline
 * expiry surfaces as a normal Error (readable message), so existing
 * catch-into-error-state paths render it; caller-provided abort signals
 * (e.g. Omnibox debounce) still reject with their own AbortError.
 */

export const API_TIMEOUT_MS = 30_000

export async function timedFetch(
  input: RequestInfo | URL,
  init: RequestInit = {},
  timeoutMs: number = API_TIMEOUT_MS
): Promise<Response> {
  const deadline = AbortSignal.timeout(timeoutMs)
  const signal = init.signal ? AbortSignal.any([init.signal, deadline]) : deadline
  try {
    return await fetch(input, { ...init, signal })
  } catch (e) {
    if (deadline.aborted && e instanceof DOMException && e.name === 'TimeoutError') {
      throw new Error(`request timed out after ${Math.round(timeoutMs / 1000)}s — server unreachable?`)
    }
    throw e
  }
}
