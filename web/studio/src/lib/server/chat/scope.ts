/**
 * Scope injection (RFC-012 R6): "the active service/scope filter travels with
 * every tool call the agent makes." Before dispatching a model-chosen tool
 * call, if the tool's JSON Schema declares a `service_name` / `scope_id`
 * property AND the model did not already set it, we fill in the active scope.
 *
 * Rules (never override an explicit model choice):
 *  - inject `service_name` only when the schema has that property, the active
 *    service is non-null, and the model didn't set it.
 *  - inject `scope_id` only when the schema has that property and the model
 *    didn't set it (scopeId always has a value — defaults to 'main').
 *  - "model didn't set it" means the key is absent or undefined/empty-string;
 *    a model that passes service_name: "" is treated as unset (the Go handlers
 *    treat "" as no-filter, so honoring it would defeat the injection intent).
 *
 * Pure and schema-driven so it's unit-testable without a live MCP or model.
 */
import type { ChatScope } from '$lib/types/chat'

/** Minimal view of a JSON Schema we care about: which top-level props exist. */
export interface ToolInputSchema {
  properties?: Record<string, unknown>
}

/** Does the schema declare a top-level property with this name? */
function schemaHasProperty(schema: ToolInputSchema | undefined, name: string): boolean {
  return !!schema?.properties && Object.prototype.hasOwnProperty.call(schema.properties, name)
}

/** Treat undefined / null / "" (whitespace) as "the model didn't set it". */
function isUnset(v: unknown): boolean {
  return v === undefined || v === null || (typeof v === 'string' && v.trim() === '')
}

/**
 * Returns a NEW args object with scope values injected per the rules above.
 * The input args are never mutated. If nothing is injected the returned object
 * is still a fresh copy (callers pass it straight to the tool + emit it).
 */
export function injectScope(
  args: Record<string, unknown>,
  schema: ToolInputSchema | undefined,
  scope: ChatScope
): Record<string, unknown> {
  const out: Record<string, unknown> = { ...args }

  if (
    scope.service !== null &&
    schemaHasProperty(schema, 'service_name') &&
    isUnset(out.service_name)
  ) {
    out.service_name = scope.service
  }

  if (schemaHasProperty(schema, 'scope_id') && isUnset(out.scope_id)) {
    out.scope_id = scope.scopeId
  }

  return out
}
