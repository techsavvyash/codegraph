import type { RequestHandler } from './$types'
import { mcpClient } from '$lib/server/mcp-client'
import { SYSTEM_PROMPT } from '$lib/constants'
import { env } from '$env/dynamic/private'
import OpenAI from 'openai'

function ndjsonError(message: string): Response {
  const body = JSON.stringify({ type: 'error', message }) + '\n'
  return new Response(body, {
    headers: { 'Content-Type': 'application/x-ndjson', 'Cache-Control': 'no-cache' }
  })
}

export const POST: RequestHandler = async ({ request }) => {
  if (!env.OPENAI_API_KEY) {
    return ndjsonError('OPENAI_API_KEY is not set. Add it to apps/chat-ui/.env and restart the dev server.')
  }

  const MODEL = env.OPENAI_MODEL ?? 'gpt-4.1-nano'
  const openai = new OpenAI({ apiKey: env.OPENAI_API_KEY })
  const { messages } = await request.json()

  let tools: Awaited<ReturnType<typeof mcpClient.listTools>>
  try {
    tools = await mcpClient.listTools()
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    const body = JSON.stringify({ type: 'error', message: `MCP unavailable: ${msg}` }) + '\n'
    return new Response(body, {
      status: 200,
      headers: { 'Content-Type': 'application/x-ndjson', 'Cache-Control': 'no-cache' }
    })
  }

  const oaiTools: OpenAI.Chat.Completions.ChatCompletionTool[] = tools.map(t => ({
    type: 'function',
    function: {
      name: t.name,
      description: t.description,
      parameters: t.inputSchema as Record<string, unknown>
    }
  }))

  const stream = new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      const send = (obj: unknown) =>
        controller.enqueue(enc.encode(JSON.stringify(obj) + '\n'))

      const history: OpenAI.Chat.Completions.ChatCompletionMessageParam[] = [
        { role: 'system', content: SYSTEM_PROMPT },
        ...messages
      ]

      try {
        while (true) {
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

          if (msg.content) {
            send({ type: 'text', delta: msg.content })
          }

          if (choice.finish_reason === 'stop' || !msg.tool_calls?.length) break

          for (const tc of msg.tool_calls) {
            let args: Record<string, unknown>
            try {
              args = JSON.parse(tc.function.arguments)
            } catch {
              args = {}
            }

            send({ type: 'tool_use', name: tc.function.name, input: args })

            let result: string
            try {
              result = await mcpClient.callTool(tc.function.name, args)
              send({ type: 'tool_result', name: tc.function.name, result })
            } catch (e) {
              result = `Error: ${e instanceof Error ? e.message : String(e)}`
              send({ type: 'tool_result', name: tc.function.name, result })
            }

            history.push({
              role: 'tool',
              tool_call_id: tc.id,
              content: result
            })
          }
        }
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e)
        send({ type: 'error', message: msg })
      }

      send({ type: 'done' })
      controller.close()
    }
  })

  return new Response(stream, {
    headers: {
      'Content-Type': 'application/x-ndjson',
      'Cache-Control': 'no-cache',
      'X-Content-Type-Options': 'nosniff'
    }
  })
}
