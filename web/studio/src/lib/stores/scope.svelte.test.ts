import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  deserializeScope,
  serializeScope,
  reconcileService,
  DEFAULT_SCOPE_ID,
  SCOPE_STORAGE_KEY
} from './scope.svelte'

describe('deserializeScope', () => {
  it('returns the null-service default for null/empty input', () => {
    expect(deserializeScope(null)).toEqual({ service: null, scopeId: DEFAULT_SCOPE_ID })
    expect(deserializeScope('')).toEqual({ service: null, scopeId: DEFAULT_SCOPE_ID })
  })

  it('parses a well-formed payload', () => {
    expect(deserializeScope(JSON.stringify({ service: 'codegraph', scopeId: 'main' }))).toEqual({
      service: 'codegraph',
      scopeId: 'main'
    })
  })

  it('treats an empty-string service as null', () => {
    expect(deserializeScope(JSON.stringify({ service: '', scopeId: 'main' })).service).toBeNull()
  })

  it('falls back scopeId to the default when absent or blank', () => {
    expect(deserializeScope(JSON.stringify({ service: 'khaata/backend' })).scopeId).toBe(
      DEFAULT_SCOPE_ID
    )
    expect(deserializeScope(JSON.stringify({ service: 'x', scopeId: '' })).scopeId).toBe(
      DEFAULT_SCOPE_ID
    )
  })

  it('never throws on malformed JSON or non-object payloads', () => {
    expect(deserializeScope('not json')).toEqual({ service: null, scopeId: DEFAULT_SCOPE_ID })
    expect(deserializeScope('42')).toEqual({ service: null, scopeId: DEFAULT_SCOPE_ID })
    expect(deserializeScope('null')).toEqual({ service: null, scopeId: DEFAULT_SCOPE_ID })
    expect(deserializeScope('[1,2,3]')).toEqual({ service: null, scopeId: DEFAULT_SCOPE_ID })
  })

  it('ignores non-string service/scopeId values', () => {
    expect(deserializeScope(JSON.stringify({ service: 123, scopeId: true }))).toEqual({
      service: null,
      scopeId: DEFAULT_SCOPE_ID
    })
  })
})

describe('serializeScope', () => {
  it('round-trips through deserializeScope', () => {
    const s = { service: 'dough-core', scopeId: 'main' }
    expect(deserializeScope(serializeScope(s))).toEqual(s)
  })

  it('serializes a null service', () => {
    expect(deserializeScope(serializeScope({ service: null, scopeId: 'main' }))).toEqual({
      service: null,
      scopeId: 'main'
    })
  })
})

describe('reconcileService', () => {
  const known = ['codegraph', 'khaata/backend', 'dough-core']

  it('keeps a known service', () => {
    expect(reconcileService('codegraph', known)).toBe('codegraph')
  })

  it('degrades an unknown (stale) service to null', () => {
    expect(reconcileService('deleted-service', known)).toBeNull()
  })

  it('leaves null (All services) as null', () => {
    expect(reconcileService(null, known)).toBeNull()
  })

  it('degrades everything to null against an empty known set', () => {
    expect(reconcileService('codegraph', [])).toBeNull()
  })
})

// ── store integration (localStorage-backed) ──────────────────────────────
// The store is a module singleton; each test resets storage AND re-imports a
// fresh module instance via vi.resetModules so #hydrated starts false.
describe('scope store', () => {
  let store: typeof import('./scope.svelte')

  function installLocalStorage(seed: Record<string, string> = {}): void {
    const backing = new Map<string, string>(Object.entries(seed))
    const mock = {
      getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
      setItem: (k: string, v: string) => void backing.set(k, v),
      removeItem: (k: string) => void backing.delete(k),
      clear: () => backing.clear(),
      key: (i: number) => [...backing.keys()][i] ?? null,
      get length() {
        return backing.size
      }
    }
    vi.stubGlobal('localStorage', mock)
  }

  beforeEach(async () => {
    vi.resetModules()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('defaults to All services + main scope with no persisted value', async () => {
    installLocalStorage()
    store = await import('./scope.svelte')
    expect(store.scope.service).toBeNull()
    expect(store.scope.scopeId).toBe(DEFAULT_SCOPE_ID)
  })

  it('hydrates a persisted selection at module init', async () => {
    installLocalStorage({
      [SCOPE_STORAGE_KEY]: JSON.stringify({ service: 'khaata/backend', scopeId: 'main' })
    })
    store = await import('./scope.svelte')
    expect(store.scope.service).toBe('khaata/backend')
  })

  it('persists a setService call to localStorage', async () => {
    installLocalStorage()
    store = await import('./scope.svelte')
    store.scope.setService('dough-gateway')
    expect(store.scope.service).toBe('dough-gateway')
    expect(localStorage.getItem(SCOPE_STORAGE_KEY)).toBe(
      JSON.stringify({ service: 'dough-gateway', scopeId: 'main' })
    )
  })

  it('setService(null) clears back to All services and persists', async () => {
    installLocalStorage({
      [SCOPE_STORAGE_KEY]: JSON.stringify({ service: 'codegraph', scopeId: 'main' })
    })
    store = await import('./scope.svelte')
    expect(store.scope.service).toBe('codegraph')
    store.scope.setService(null)
    expect(store.scope.service).toBeNull()
    expect(deserializeScope(localStorage.getItem(SCOPE_STORAGE_KEY)).service).toBeNull()
  })

  it('setService treats an empty string as null', async () => {
    installLocalStorage()
    store = await import('./scope.svelte')
    store.scope.setService('')
    expect(store.scope.service).toBeNull()
  })

  it('reconcile degrades a stale persisted service to null and persists', async () => {
    installLocalStorage({
      [SCOPE_STORAGE_KEY]: JSON.stringify({ service: 'was-deleted', scopeId: 'main' })
    })
    store = await import('./scope.svelte')
    expect(store.scope.service).toBe('was-deleted')
    store.scope.reconcile(['codegraph', 'dough-core'])
    expect(store.scope.service).toBeNull()
    expect(deserializeScope(localStorage.getItem(SCOPE_STORAGE_KEY)).service).toBeNull()
  })

  it('reconcile keeps a still-valid persisted service (no spurious write)', async () => {
    installLocalStorage({
      [SCOPE_STORAGE_KEY]: JSON.stringify({ service: 'codegraph', scopeId: 'main' })
    })
    store = await import('./scope.svelte')
    const setSpy = vi.spyOn(localStorage, 'setItem')
    store.scope.reconcile(['codegraph', 'dough-core'])
    expect(store.scope.service).toBe('codegraph')
    expect(setSpy).not.toHaveBeenCalled()
  })

  it('setScopeId persists and coerces blank to the default', async () => {
    installLocalStorage()
    store = await import('./scope.svelte')
    store.scope.setScopeId('feature-x')
    expect(store.scope.scopeId).toBe('feature-x')
    store.scope.setScopeId('')
    expect(store.scope.scopeId).toBe(DEFAULT_SCOPE_ID)
  })

  it('does not crash without localStorage (SSR guard) and stays at defaults', async () => {
    // no installLocalStorage(): localStorage is undefined in the node test env
    expect(typeof localStorage).toBe('undefined')
    store = await import('./scope.svelte')
    expect(store.scope.service).toBeNull()
    expect(store.scope.scopeId).toBe(DEFAULT_SCOPE_ID)
    // mutating without storage must not throw
    expect(() => store.scope.setService('codegraph')).not.toThrow()
    expect(store.scope.service).toBe('codegraph')
  })
})
