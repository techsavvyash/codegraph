/**
 * Chat tool-loop (RFC-012 R6). Ported from web/chat-ui, adapted to studio:
 *  - uses the studio `mcp` singleton (crash-loop cooldown) via callToolText,
 *    which THROWS McpRequestError on tool isError — we catch it and feed the
 *    error text back to the model as the tool result (self-correction), rather
 *    than aborting the turn.
 *  - scope-aware: the client sends { messages, scope }; before each tool call
 *    we inject the active service/scope into args the model left unset (see
 *    injectScope). The emitted tool_use reflects the FINAL (post-injection)
 *    args so the activity display is honest about what ran.
 *  - degrades gracefully when OPENAI_API_KEY is absent (NDJSON error event).
 *
 * Wire protocol: request { messages: WireMessage[], scope: ChatScope };
 * response is newline-delimited JSON ChatStreamEvent objects, terminated by
 * { type: 'done' }.
 */
import type { RequestHandler } from './$types'
import { mcp } from '$lib/server/mcp/client'
import { injectScope, type ToolInputSchema } from '$lib/server/chat/scope'
import type { ChatScope, ChatStreamEvent } from '$lib/types/chat'
import { env } from '$env/dynamic/private'
import OpenAI from 'openai'

const SYSTEM_PROMPT = `You are a code intelligence assistant embedded in CodeGraph Studio.
You have access to a Neo4j graph database of code entities indexed from the user's repositories, exposed as MCP tools.
Use the tools to search and retrieve real code before answering — never guess at symbol names, file paths, or call relationships.
Always cite the specific functions/files you used to derive your answer.
The user has an active service scope; scoped tool arguments are filled in for you automatically, so you do not need to pass service_name unless you want to query a different service than the active scope.`

const NDJSON_HEADERS = {
  'Content-Type': 'application/x-ndjson',
  'Cache-Control': 'no-cache',
  'X-Content-Type-Options': 'nosniff'
}

function ndjsonError(message: string): Response {
  const body = JSON.stringify({ type: 'error', message } satisfies ChatStreamEvent) + '\n'
  return new Response(body, { headers: NDJSON_HEADERS })
}

function defaultScope(): ChatScope {
  return { service: null, scopeId: 'main' }
}

export const POST: RequestHandler = async ({ request }) => {
  if (!env.OPENAI_API_KEY) {
    return ndjsonError(
      'OPENAI_API_KEY is not set. Add it to web/studio/.env (or the environment) and restart the server to enable chat.'
    )
  }

  const MODEL = env.OPENAI_MODEL ?? 'gpt-4.1-nano'
  const openai = new OpenAI({ apiKey: env.OPENAI_API_KEY })

  const payload = (await request.json()) as {
    messages?: OpenAI.Chat.Completions.ChatCompletionMessageParam[]
    scope?: ChatScope
  }
  const incoming = payload.messages ?? []
  const scope = payload.scope ?? defaultScope()

  // Load tools WITH schema — needed to hand the model parameters and to drive
  // scope injection. MCP being down is non-fatal: the model still answers,
  // just without graph tools.
  let tools: Array<{ name: string; description: string; inputSchema: Record<string, unknown> }> = []
  let mcpWarning: string | null = null
  try {
    tools = await mcp.listToolsWithSchema()
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    mcpWarning = `Code graph tools are unavailable (${msg}). Answering without them — start Neo4j / the MCP server to enable them.`
    console.warn('[api/chat] MCP unavailable, proceeding without tools:', msg)
  }

  const schemaByName = new Map<string, ToolInputSchema>(
    tools.map((t) => [t.name, t.inputSchema as ToolInputSchema])
  )

  const oaiTools: OpenAI.Chat.Completions.ChatCompletionFunctionTool[] = tools.map((t) => ({
    type: 'function',
    function: {
      name: t.name,
      description: t.description,
      parameters: t.inputSchema
    }
  }))

  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const enc = new TextEncoder()
      const send = (ev: ChatStreamEvent) => controller.enqueue(enc.encode(JSON.stringify(ev) + '\n'))

      const history: OpenAI.Chat.Completions.ChatCompletionMessageParam[] = [
        { role: 'system', content: SYSTEM_PROMPT },
        ...incoming
      ]

      if (mcpWarning) send({ type: 'warning', message: mcpWarning })

      try {
        // Bounded loop: a runaway model that keeps calling tools without ever
        // answering must not stream forever.
        for (let turn = 0; turn < 12; turn++) {
          const response = await openai.chat.completions.create({
            model: MODEL,
            messages: history,
            tools: oaiTools.length > 0 ? oaiTools : undefined,
            tool_choice: oaiTools.length > 0 ? 'auto' : undefined,
            stream: false
          })

          const choice = response.choices[0]
          const msg = choice.message
          history.push(msg)

          if (msg.content) send({ type: 'text', delta: msg.content })

          const toolCalls = msg.tool_calls ?? []
          if (choice.finish_reason === 'stop' || toolCalls.length === 0) break

          for (const tc of toolCalls) {
            // v7: tool_calls is a union (function | custom). We only issue
            // function tools; anything else we can't service, so tell the model.
            if (tc.type !== 'function') {
              history.push({
                role: 'tool',
                tool_call_id: tc.id,
                content: `Error: unsupported tool call type "${tc.type}"`
              })
              continue
            }

            let modelArgs: Record<string, unknown>
            try {
              modelArgs = JSON.parse(tc.function.arguments || '{}')
            } catch {
              modelArgs = {}
            }

            const args = injectScope(modelArgs, schemaByName.get(tc.function.name), scope)

            // Emit the FINAL (post-injection) args so the UI shows what ran.
            send({ type: 'tool_use', name: tc.function.name, input: args })

            const started = Date.now()
            let result: string
            let isError = false
            try {
              result = await mcp.callToolText(tc.function.name, args)
            } catch (e) {
              isError = true
              // McpRequestError (tool-error / rpc-error / timeout / cooldown /
              // process) extends Error, so one check covers them all — the
              // readable message is fed back so the model can adjust, exactly
              // like a normal tool result.
              result = e instanceof Error ? `Error: ${e.message}` : `Error: ${String(e)}`
            }
            const durationMs = Date.now() - started

            send({ type: 'tool_result', name: tc.function.name, result, durationMs, isError })

            history.push({ role: 'tool', tool_call_id: tc.id, content: result })
          }
        }
      } catch (e) {
        send({ type: 'error', message: e instanceof Error ? e.message : String(e) })
      }

      send({ type: 'done' })
      controller.close()
    }
  })

  return new Response(stream, { headers: NDJSON_HEADERS })
}
