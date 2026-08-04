import { afterEach, describe, expect, it, vi } from 'vitest'
import { timedFetch } from './timedFetch'

const realFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = realFetch
  vi.restoreAllMocks()
})

/** A fetch stub that never settles unless aborted (rejects with the signal's reason). */
function hangingFetch(): typeof fetch {
  return ((_input: RequestInfo | URL, init?: RequestInit) =>
    new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal!.reason))
    })) as typeof fetch
}

describe('timedFetch', () => {
  it('passes through a successful response untouched', async () => {
    const res = new Response('{"ok":true}', { status: 200 })
    globalThis.fetch = vi.fn(async () => res) as typeof fetch
    await expect(timedFetch('/api/services')).resolves.toBe(res)
  })

  it('forwards an abort signal to fetch', async () => {
    let seen: RequestInit | undefined
    globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
      seen = init
      return new Response('{}')
    }) as typeof fetch
    await timedFetch('/api/find', { method: 'POST' })
    expect(seen?.signal).toBeInstanceOf(AbortSignal)
    expect(seen?.method).toBe('POST')
  })

  it('rejects with a readable error when the deadline passes on a hung request', async () => {
    globalThis.fetch = hangingFetch()
    await expect(timedFetch('/api/entrypoints', {}, 20)).rejects.toThrow(/timed out after 0s — server unreachable\?/)
  })

  it('preserves the caller AbortError when the caller aborts before the deadline', async () => {
    globalThis.fetch = hangingFetch()
    const controller = new AbortController()
    const pending = timedFetch('/api/find', { signal: controller.signal }, 10_000)
    controller.abort()
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
  })

  it('rejects real network errors unchanged', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError('fetch failed')
    }) as typeof fetch
    await expect(timedFetch('/api/flow')).rejects.toThrow('fetch failed')
  })
})
