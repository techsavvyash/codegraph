/**
 * Global scope store (RFC-012 R9): the studio-wide service + scope selection
 * that every pane reads. `service === null` means "All services" — an explicit,
 * warned choice (unscoped queries can be slow). `scopeId` defaults to 'main';
 * the dev graph only uses one scope, but the model carries it so scoped
 * queries stay expressible.
 *
 * Svelte 5 runes module singleton — import { scope } and read scope.service.
 * Selection is persisted to localStorage and hydrated eagerly at module init
 * (SSR has no localStorage, so the server instance stays at defaults).
 *
 * Serialize/deserialize/validation live as pure functions below so they're
 * unit-testable without a browser: a stale persisted service name that no
 * longer exists in the graph must degrade to null, never crash a pane.
 */

export const SCOPE_STORAGE_KEY = 'studio:scope'
export const DEFAULT_SCOPE_ID = 'main'

/** The shape persisted to localStorage. */
export interface PersistedScope {
  service: string | null
  scopeId: string
}

/**
 * Parses a raw localStorage string into a PersistedScope, tolerating any
 * malformed/legacy payload by falling back to the null-service default.
 * Never throws — a corrupt value must not brick the app on load.
 */
export function deserializeScope(raw: string | null): PersistedScope {
  if (!raw) return { service: null, scopeId: DEFAULT_SCOPE_ID }
  try {
    const parsed = JSON.parse(raw) as unknown
    if (typeof parsed !== 'object' || parsed === null) {
      return { service: null, scopeId: DEFAULT_SCOPE_ID }
    }
    const obj = parsed as Record<string, unknown>
    const service = typeof obj.service === 'string' && obj.service.length > 0 ? obj.service : null
    const scopeId =
      typeof obj.scopeId === 'string' && obj.scopeId.length > 0 ? obj.scopeId : DEFAULT_SCOPE_ID
    return { service, scopeId }
  } catch {
    return { service: null, scopeId: DEFAULT_SCOPE_ID }
  }
}

/** Serializes a scope selection for localStorage. */
export function serializeScope(scope: PersistedScope): string {
  return JSON.stringify({ service: scope.service, scopeId: scope.scopeId })
}

/**
 * Reconciles a persisted/selected service against the set of services that
 * actually exist in the graph. An unknown name (stale link, deleted service)
 * degrades to null rather than pinning every pane to a service that returns
 * nothing. A null selection ("All services") is always valid.
 */
export function reconcileService(service: string | null, known: readonly string[]): string | null {
  if (service === null) return null
  return known.includes(service) ? service : null
}

class ScopeStore {
  #service = $state<string | null>(null)
  #scopeId = $state<string>(DEFAULT_SCOPE_ID)

  constructor() {
    // Hydration is EAGER, at module init: the getters are read inside
    // $derived expressions, and mutating $state during a derived computation
    // is state_unsafe_mutation — it crashes hydration app-wide. On the server
    // there is no localStorage and the store stays at defaults (the browser
    // gets its own module instance).
    if (typeof localStorage !== 'undefined') {
      const persisted = deserializeScope(localStorage.getItem(SCOPE_STORAGE_KEY))
      this.#service = persisted.service
      this.#scopeId = persisted.scopeId
    }
  }

  #persist(): void {
    if (typeof localStorage === 'undefined') return
    localStorage.setItem(
      SCOPE_STORAGE_KEY,
      serializeScope({ service: this.#service, scopeId: this.#scopeId })
    )
  }

  /** The selected service, or null for "All services". Pure read — safe in $derived. */
  get service(): string | null {
    return this.#service
  }

  /** The active scope id (default 'main'). Pure read — safe in $derived. */
  get scopeId(): string {
    return this.#scopeId
  }

  /** Sets the active service (null = All services) and persists it. */
  setService(name: string | null): void {
    const next = name && name.length > 0 ? name : null
    if (next === this.#service) return
    this.#service = next
    this.#persist()
  }

  /** Sets the active scope id and persists it. */
  setScopeId(id: string): void {
    const next = id && id.length > 0 ? id : DEFAULT_SCOPE_ID
    if (next === this.#scopeId) return
    this.#scopeId = next
    this.#persist()
  }

  /**
   * Drops the current selection to null if it isn't in the known set —
   * called by ScopeSelector once /api/services resolves, so a stale
   * persisted service doesn't silently scope every pane to nothing.
   */
  reconcile(known: readonly string[]): void {
    const next = reconcileService(this.#service, known)
    if (next !== this.#service) {
      this.#service = next
      this.#persist()
    }
  }
}

/** Studio-wide singleton. Import and read `scope.service` / call `scope.setService(...)`. */
export const scope = new ScopeStore()
