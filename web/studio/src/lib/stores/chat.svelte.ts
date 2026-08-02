/**
 * Chat dock store (RFC-012 R6) — Svelte 5 runes port of web/chat-ui's chat
 * store. Owns the message list, consumes the NDJSON stream from /api/chat,
 * tracks in-flight state, and supports abort. Citations are harvested from
 * tool_result payloads client-side (chat-ui never did this — it only displayed
 * raw results; live node chips are new for studio).
 *
 * The NDJSON parsing/accumulation is factored into pure functions
 * (parseCitationsFromResult, argsSummary, splitNdjson) so the stream logic is
 * unit-testable without a browser.
 */
import type {
  ChatMessage,
  ChatScope,
  ChatStreamEvent,
  Citation,
  ToolCallRecord,
  WireMessage
} from '$lib/types/chat'

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

/** Human label for a tool (drops the codegraph_ prefix, spaces the rest). */
export function toolLabel(tool: string): string {
  return tool.replace(/^codegraph_/, '').replace(/_/g, ' ')
}

/** One-line summary of tool args for the activity row. */
export function argsSummary(args: Record<string, unknown>): string {
  const parts: string[] = []
  for (const [k, v] of Object.entries(args)) {
    if (v === undefined || v === null || v === '') continue
    let val: string
    if (typeof v === 'string') val = v
    else if (typeof v === 'number' || typeof v === 'boolean') val = String(v)
    else val = JSON.stringify(v)
    if (val.length > 40) val = val.slice(0, 39) + '…'
    parts.push(`${k}: ${val}`)
  }
  return parts.join(', ')
}

/**
 * Harvests node citations from a tool result. The codegraph tools return JSON
 * (optionally preceded by `warning:` lines) whose records carry `node_id`
 * (elementId), `name`, and `label`. We walk the parsed structure and collect
 * every object that has a NON-EMPTY string `node_id`. Names/labels are never
 * fabricated — an object without a real node_id yields no citation, so a chip
 * always deep-links to something that exists.
 *
 * Deduplicated by node_id (first occurrence wins for name/kind).
 */
export function parseCitationsFromResult(tool: string, result: string): Citation[] {
  const body = stripWarnings(result)
  if (!body || (body[0] !== '{' && body[0] !== '[')) return []
  let parsed: unknown
  try {
    parsed = JSON.parse(body)
  } catch {
    return []
  }
  const seen = new Set<string>()
  const out: Citation[] = []
  const visit = (v: unknown): void => {
    if (Array.isArray(v)) {
      for (const item of v) visit(item)
      return
    }
    if (v === null || typeof v !== 'object') return
    const obj = v as Record<string, unknown>
    const nodeId = obj.node_id ?? obj.nodeId
    if (typeof nodeId === 'string' && nodeId.length > 0 && !seen.has(nodeId)) {
      seen.add(nodeId)
      const name = typeof obj.name === 'string' && obj.name.length > 0 ? obj.name : nodeId.slice(-8)
      const kind = typeof obj.label === 'string' && obj.label.length > 0 ? obj.label : undefined
      out.push({ nodeId, name, kind, tool })
    }
    for (const val of Object.values(obj)) {
      if (val !== null && typeof val === 'object') visit(val)
    }
  }
  visit(parsed)
  return out
}

/** Peels leading `warning:`/blank lines, mirroring the server payload parser. */
function stripWarnings(text: string): string {
  let rest = text
  for (;;) {
    const nl = rest.indexOf('\n')
    const line = (nl === -1 ? rest : rest.slice(0, nl)).trim()
    if (line.startsWith('warning:') || (line === '' && nl !== -1)) {
      rest = nl === -1 ? '' : rest.slice(nl + 1)
    } else {
      break
    }
  }
  return rest.trim()
}

/**
 * Splits a buffer of concatenated NDJSON into complete parsed events plus the
 * leftover partial line. Pure, so the stream reducer can be tested directly.
 */
export function splitNdjson(buffer: string): { events: ChatStreamEvent[]; rest: string } {
  const lines = buffer.split('\n')
  const rest = lines.pop() ?? ''
  const events: ChatStreamEvent[] = []
  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed) continue
    try {
      events.push(JSON.parse(trimmed) as ChatStreamEvent)
    } catch {
      // ignore malformed line
    }
  }
  return { events, rest }
}

function newId(): string {
  return typeof crypto !== 'undefined' && crypto.randomUUID
    ? crypto.randomUUID()
    : Math.random().toString(36).slice(2)
}

export class ChatStore {
  messages = $state<ChatMessage[]>([])
  loading = $state(false)
  /** Current tool activity label (null when idle) — drives the pill. */
  activity = $state<string | null>(null)
  /** Last error / non-fatal warning surfaced to the user (dismissable). */
  error = $state<string | null>(null)
  open = $state(false)

  private fetchFn: FetchLike
  private controller: AbortController | null = null

  constructor(fetchFn?: FetchLike) {
    this.fetchFn = fetchFn ?? ((input, init) => fetch(input, init))
  }

  toggle(): void {
    this.open = !this.open
  }

  dismissError(): void {
    this.error = null
  }

  clear(): void {
    this.abort()
    this.messages = []
    this.error = null
    this.activity = null
  }

  abort(): void {
    this.controller?.abort()
    this.controller = null
    this.loading = false
    this.activity = null
  }

  /** History for the wire: role + content only, no client-side fields. */
  private wireHistory(): WireMessage[] {
    return this.messages.map((m) => ({ role: m.role, content: m.content }))
  }

  private patchAssistant(id: string, fn: (m: ChatMessage) => ChatMessage): void {
    this.messages = this.messages.map((m) => (m.id === id ? fn(m) : m))
  }

  async send(userText: string, scope: ChatScope): Promise<void> {
    const trimmed = userText.trim()
    if (!trimmed || this.loading) return

    this.error = null
    this.messages = [...this.messages, { id: newId(), role: 'user', content: trimmed }]

    const history = this.wireHistory()
    this.loading = true
    this.controller = new AbortController()

    let resp: Response
    try {
      resp = await this.fetchFn('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ messages: history, scope }),
        signal: this.controller.signal
      })
    } catch (e) {
      if ((e as Error)?.name === 'AbortError') {
        this.loading = false
        this.activity = null
        return
      }
      this.loading = false
      this.activity = null
      this.error = `Network error: ${e instanceof Error ? e.message : String(e)}`
      return
    }

    if (!resp.ok || !resp.body) {
      this.loading = false
      this.error = `Server error: ${resp.status} ${resp.statusText}`
      return
    }

    const assistantId = newId()
    this.messages = [
      ...this.messages,
      { id: assistantId, role: 'assistant', content: '', tools: [], citations: [] }
    ]

    const reader = resp.body.getReader()
    const dec = new TextDecoder()
    let buffer = ''

    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += dec.decode(value, { stream: true })
        const { events, rest } = splitNdjson(buffer)
        buffer = rest
        for (const ev of events) this.applyEvent(assistantId, ev)
      }
    } catch (e) {
      if ((e as Error)?.name !== 'AbortError') {
        this.error = `Stream error: ${e instanceof Error ? e.message : String(e)}`
      }
    } finally {
      this.loading = false
      this.activity = null
      this.controller = null
    }
  }

  private applyEvent(assistantId: string, ev: ChatStreamEvent): void {
    switch (ev.type) {
      case 'text':
        this.patchAssistant(assistantId, (m) => ({ ...m, content: m.content + ev.delta }))
        break
      case 'tool_use': {
        this.activity = `${toolLabel(ev.name)}${argsSummary(ev.input) ? ` · ${argsSummary(ev.input)}` : ''}`
        const rec: ToolCallRecord = { tool: ev.name, args: ev.input, result: '' }
        this.patchAssistant(assistantId, (m) => ({ ...m, tools: [...(m.tools ?? []), rec] }))
        break
      }
      case 'tool_result': {
        this.activity = null
        const cites = ev.isError ? [] : parseCitationsFromResult(ev.name, ev.result)
        this.patchAssistant(assistantId, (m) => {
          const tools = [...(m.tools ?? [])]
          // Fill the most recent still-unfilled record for this tool.
          for (let i = tools.length - 1; i >= 0; i--) {
            if (tools[i].tool === ev.name && tools[i].result === '') {
              tools[i] = {
                ...tools[i],
                result: ev.result,
                durationMs: ev.durationMs,
                isError: ev.isError
              }
              break
            }
          }
          const existing = m.citations ?? []
          const known = new Set(existing.map((c) => c.nodeId))
          const merged = [...existing, ...cites.filter((c) => !known.has(c.nodeId))]
          return { ...m, tools, citations: merged }
        })
        break
      }
      case 'warning':
        // Non-fatal: surface but keep streaming.
        this.error = ev.message
        break
      case 'error':
        this.activity = null
        this.error = ev.message
        break
      case 'done':
        this.activity = null
        break
    }
  }
}
