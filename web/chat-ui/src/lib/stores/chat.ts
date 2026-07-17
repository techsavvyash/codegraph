import { writable, get } from 'svelte/store'
import type { Message, StreamEvent } from '$lib/types'
import { TOOL_ACTIVITY_LABELS } from '$lib/constants'

export const messages = writable<Message[]>([])
export const loading = writable(false)
export const toolActivity = writable<string | null>(null)
export const error = writable<string | null>(null)

export async function sendMessage(userText: string): Promise<void> {
  const trimmed = userText.trim()
  if (!trimmed) return

  error.set(null)

  // Append user message
  messages.update(m => [
    ...m,
    { role: 'user', content: trimmed, id: crypto.randomUUID() }
  ])

  loading.set(true)

  // Build history snapshot for API (omit internal ids/sources)
  const history = get(messages).map(m => ({ role: m.role, content: m.content }))

  let resp: Response
  try {
    resp = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ messages: history })
    })
  } catch (e) {
    loading.set(false)
    error.set(`Network error: ${e instanceof Error ? e.message : String(e)}`)
    return
  }

  if (!resp.ok) {
    loading.set(false)
    error.set(`Server error: ${resp.status} ${resp.statusText}`)
    return
  }

  // Seed assistant bubble
  const assistantId = crypto.randomUUID()
  messages.update(m => [
    ...m,
    { role: 'assistant', content: '', id: assistantId, sources: [] }
  ])

  const reader = resp.body!.getReader()
  const dec = new TextDecoder()
  let buffer = ''

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += dec.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() ?? ''

      for (const line of lines) {
        const trimmedLine = line.trim()
        if (!trimmedLine) continue
        let ev: StreamEvent
        try {
          ev = JSON.parse(trimmedLine)
        } catch {
          continue
        }

        if (ev.type === 'text' && ev.delta) {
          messages.update(m =>
            m.map(msg =>
              msg.id === assistantId
                ? { ...msg, content: msg.content + ev.delta }
                : msg
            )
          )
        } else if (ev.type === 'tool_use' && ev.name) {
          toolActivity.set(TOOL_ACTIVITY_LABELS[ev.name] ?? `Running ${ev.name}...`)
        } else if (ev.type === 'tool_result' && ev.name) {
          toolActivity.set(null)
          messages.update(m =>
            m.map(msg =>
              msg.id === assistantId
                ? {
                    ...msg,
                    sources: [
                      ...(msg.sources ?? []),
                      { tool: ev.name!, result: ev.result ?? '' }
                    ]
                  }
                : msg
            )
          )
        } else if (ev.type === 'done') {
          toolActivity.set(null)
        } else if (ev.type === 'warning') {
          // Non-fatal warning (e.g. MCP unavailable) — show in error banner but keep going
          error.set(ev.message ?? 'Warning')
        } else if (ev.type === 'error') {
          toolActivity.set(null)
          error.set(ev.message ?? 'Unknown streaming error')
        }
      }
    }
  } finally {
    loading.set(false)
    toolActivity.set(null)
  }
}

export function clearMessages() {
  messages.set([])
  error.set(null)
  toolActivity.set(null)
}
