/**
 * Chat dock types (RFC-012 R6). Shared between the server route, the runes
 * store, and the ChatDock UI. The wire protocol is newline-delimited JSON
 * (NDJSON): the client POSTs { messages, scope } and the server streams a
 * sequence of ChatStreamEvent objects, one per line, terminated by `done`.
 */

/** The active scope that travels with every tool call (RFC-012 R6/R9). */
export interface ChatScope {
  /** Selected service, or null for "All services" (unscoped). */
  service: string | null
  /** Active scope id (default 'main'). */
  scopeId: string
}

/** A single recorded tool invocation attached to an assistant turn. */
export interface ToolCallRecord {
  /** MCP tool name, e.g. `codegraph_search`. */
  tool: string
  /** FINAL args after scope injection — what actually ran. */
  args: Record<string, unknown>
  /** Raw text result (or error text). Empty until the result arrives. */
  result: string
  /** Wall-clock duration in ms, filled in on tool_result. */
  durationMs?: number
  /** True if the tool errored (isError / thrown McpRequestError). */
  isError?: boolean
}

/** A clickable node reference harvested from a tool result. */
export interface Citation {
  /** Real graph node id — never fabricated from a name. */
  nodeId: string
  /** Display label (node name, falls back to id tail). */
  name: string
  /** Node kind/label if known (Function, File, …). */
  kind?: string
  /** Tool that surfaced this citation, for provenance. */
  tool: string
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  /** Tool calls made while producing this (assistant) turn. */
  tools?: ToolCallRecord[]
  /** Node citations harvested from this turn's tool results. */
  citations?: Citation[]
}

/** History entry sent to the server (no client-only fields). */
export interface WireMessage {
  role: 'user' | 'assistant'
  content: string
}

export interface ChatRequest {
  messages: WireMessage[]
  scope: ChatScope
}

/**
 * NDJSON stream events. `tool_use` reflects the FINAL (post-scope-injection)
 * args so the activity display is honest about what ran.
 */
export type ChatStreamEvent =
  | { type: 'text'; delta: string }
  | { type: 'tool_use'; name: string; input: Record<string, unknown> }
  | { type: 'tool_result'; name: string; result: string; durationMs: number; isError?: boolean }
  | { type: 'warning'; message: string }
  | { type: 'error'; message: string }
  | { type: 'done' }
