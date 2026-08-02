/**
 * Cypher parameter panel logic (RFC-012 R8: "read-only editor with parameter
 * support"). Pure validation so the panel's Run-gating is unit-testable.
 *
 * The panel holds a JSON *object* of $name → value. Empty/whitespace text means
 * "no params" (valid — nothing is sent). Non-empty text must parse to a plain
 * object; anything else (array, scalar, malformed JSON) is invalid and disables
 * Run with an inline message.
 */

export interface ParamsValidation {
  /** Whether the text is a usable params value (empty counts as valid). */
  valid: boolean
  /** The parsed object to send, or null when there are no params. */
  params: Record<string, unknown> | null
  /** Inline error message when invalid, else null. */
  error: string | null
  /** Number of keys in the object (0 when empty) — drives the panel badge. */
  count: number
}

export function validateParams(text: string): ParamsValidation {
  const trimmed = text.trim()
  if (trimmed.length === 0) {
    return { valid: true, params: null, error: null, count: 0 }
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch (e) {
    return { valid: false, params: null, error: `invalid JSON: ${(e as Error).message}`, count: 0 }
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return {
      valid: false,
      params: null,
      error: 'params must be a JSON object, e.g. {"name": "codegraph"}',
      count: 0
    }
  }
  const obj = parsed as Record<string, unknown>
  return { valid: true, params: obj, error: null, count: Object.keys(obj).length }
}
