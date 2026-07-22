/**
 * The codegraph MCP tools return a single text content block. For JSON-format
 * tools that block is a JSON document, optionally preceded by guardrail
 * warning lines (e.g. "warning: query plan contains AllNodesScan — ...").
 * Warnings are surfaced, never silently dropped (RFC-012 failure honesty).
 */
export interface ToolPayload<T> {
  warnings: string[]
  data: T
}

export class ToolPayloadError extends Error {
  readonly raw: string
  constructor(message: string, raw: string) {
    super(message)
    this.name = 'ToolPayloadError'
    this.raw = raw
  }
}

export function parseToolPayload<T>(text: string): ToolPayload<T> {
  const warnings: string[] = []
  let rest = text
  // Peel leading warning lines (blank lines between warnings and body are noise)
  for (;;) {
    const nl = rest.indexOf('\n')
    const line = (nl === -1 ? rest : rest.slice(0, nl)).trim()
    if (line.startsWith('warning:')) {
      warnings.push(line.slice('warning:'.length).trim())
      rest = nl === -1 ? '' : rest.slice(nl + 1)
    } else if (line === '' && nl !== -1) {
      rest = rest.slice(nl + 1)
    } else {
      break
    }
  }
  const body = rest.trim()
  if (!body.startsWith('{') && !body.startsWith('[')) {
    throw new ToolPayloadError(
      `tool returned non-JSON payload: ${body.slice(0, 200)}`,
      text
    )
  }
  let data: T
  try {
    data = JSON.parse(body) as T
  } catch (e) {
    throw new ToolPayloadError(
      `tool returned malformed JSON: ${(e as Error).message}`,
      text
    )
  }
  return { warnings, data }
}
