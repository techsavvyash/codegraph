import { describe, it, expect, vi } from 'vitest'
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
import { createInterface } from 'node:readline'
import { McpClient, McpRequestError, type McpProcess } from './client'

interface RpcMessage {
  jsonrpc: string
  id?: number
  method: string
  params?: Record<string, unknown>
}

/**
 * Scripted stand-in for the codegraph-mcp child process. Parses each
 * newline-delimited JSON-RPC message off stdin and hands it to the script;
 * the script replies (or doesn't) via respond()/respondError().
 */
class FakeProc extends EventEmitter implements McpProcess {
  stdin = new PassThrough()
  stdout = new PassThrough()
  stderr = new PassThrough()
  received: RpcMessage[] = []
  killed = false

  constructor(script?: (msg: RpcMessage, proc: FakeProc) => void) {
    super()
    createInterface({ input: this.stdin }).on('line', (line) => {
      const msg = JSON.parse(line) as RpcMessage
      this.received.push(msg)
      script?.(msg, this)
    })
  }

  respond(id: number, result: unknown): void {
    this.stdout.write(JSON.stringify({ jsonrpc: '2.0', id, result }) + '\n')
  }

  respondError(id: number, code: number, message: string): void {
    this.stdout.write(JSON.stringify({ jsonrpc: '2.0', id, error: { code, message } }) + '\n')
  }

  writeGarbage(line: string): void {
    this.stdout.write(line + '\n')
  }

  kill(): void {
    this.killed = true
  }
}

/** Script fragment: answer the MCP initialize handshake. */
function handshake(msg: RpcMessage, proc: FakeProc): boolean {
  if (msg.method === 'initialize' && msg.id !== undefined) {
    proc.respond(msg.id, { protocolVersion: '2024-11-05', capabilities: { tools: {} } })
    return true
  }
  return msg.method === 'notifications/initialized'
}

function toolText(text: string, isError = false) {
  return { content: [{ type: 'text', text }], isError: isError || undefined }
}

function makeClient(script?: (msg: RpcMessage, proc: FakeProc) => void, opts: Partial<ConstructorParameters<typeof McpClient>[0] & object> = {}) {
  const procs: FakeProc[] = []
  const spawnFn = vi.fn(() => {
    const p = new FakeProc(script)
    procs.push(p)
    return p
  })
  const client = new McpClient({ binPath: '/fake/bin/codegraph-mcp', spawnFn, ...opts })
  return { client, spawnFn, procs }
}

describe('McpClient', () => {
  it('performs the initialize handshake before the first tool call, in order', async () => {
    const { client, procs } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call') proc.respond(msg.id!, toolText('{"ok":true}'))
    })
    const text = await client.callToolText('codegraph_schema', {})
    expect(text).toBe('{"ok":true}')
    const methods = procs[0].received.map((m) => m.method)
    expect(methods).toEqual(['initialize', 'notifications/initialized', 'tools/call'])
    const call = procs[0].received[2]
    expect(call.params).toEqual({ name: 'codegraph_schema', arguments: {} })
  })

  it('multiplexes concurrent requests and resolves out-of-order responses correctly', async () => {
    const held: Array<{ id: number; args: Record<string, unknown> }> = []
    const { client, procs } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call') {
        held.push({ id: msg.id!, args: msg.params as Record<string, unknown> })
      }
    })
    const a = client.callToolText('codegraph_find', { query: 'A' })
    const b = client.callToolText('codegraph_find', { query: 'B' })
    await vi.waitFor(() => expect(held).toHaveLength(2))
    // Answer B first, then A — each promise must still get its own payload
    procs[0].respond(held[1].id, toolText('{"answer":"B"}'))
    procs[0].respond(held[0].id, toolText('{"answer":"A"}'))
    expect(JSON.parse(await a).answer).toBe('A')
    expect(JSON.parse(await b).answer).toBe('B')
  })

  it('parses warnings + JSON via callTool', async () => {
    const { client } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call')
        proc.respond(msg.id!, toolText('warning: AllNodesScan\n\n{"rows":[1]}'))
    })
    const p = await client.callTool<{ rows: number[] }>('codegraph_cypher', { query: 'x' })
    expect(p.warnings).toEqual(['AllNodesScan'])
    expect(p.data.rows).toEqual([1])
  })

  it('rejects with tool-error when the tool sets isError', async () => {
    const { client } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call')
        proc.respond(msg.id!, toolText('codegraph_cypher: write operations are rejected', true))
    })
    const err = await client.callToolText('codegraph_cypher', { query: 'CREATE ()' }).catch((e) => e)
    expect(err).toBeInstanceOf(McpRequestError)
    expect((err as McpRequestError).kind).toBe('tool-error')
    expect((err as Error).message).toContain('write operations are rejected')
  })

  it('rejects with rpc-error on a JSON-RPC error response', async () => {
    const { client } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call') proc.respondError(msg.id!, -32601, 'Unknown tool')
    })
    const err = await client.callToolText('nope', {}).catch((e) => e)
    expect((err as McpRequestError).kind).toBe('rpc-error')
    expect((err as Error).message).toContain('Unknown tool')
  })

  it('times out unanswered requests and ignores the late response', async () => {
    const held: number[] = []
    const { client, procs } = makeClient(
      (msg, proc) => {
        if (handshake(msg, proc)) return
        if (msg.method === 'tools/call') held.push(msg.id!)
      },
      { requestTimeoutMs: 60 }
    )
    const err = await client.callToolText('codegraph_find', {}).catch((e) => e)
    expect((err as McpRequestError).kind).toBe('timeout')
    // Late response must not blow anything up
    procs[0].respond(held[0], toolText('{"late":true}'))
    await new Promise((r) => setTimeout(r, 20))
  })

  it('ignores non-JSON stdout noise', async () => {
    const { client } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call') {
        proc.writeGarbage('some debug log line')
        proc.respond(msg.id!, toolText('{"ok":1}'))
      }
    })
    expect(JSON.parse(await client.callToolText('t', {})).ok).toBe(1)
  })

  it('rejects in-flight requests when the child exits, then respawns on the next call', async () => {
    const held: Array<{ id: number; proc: FakeProc }> = []
    const { client, spawnFn, procs } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call') held.push({ id: msg.id!, proc })
    })
    const inflight = client.callToolText('codegraph_find', {})
    await vi.waitFor(() => expect(held).toHaveLength(1))
    procs[0].emit('exit', 1)
    const err = await inflight.catch((e) => e)
    expect((err as McpRequestError).kind).toBe('process')
    expect((err as Error).message).toContain('exited (code=1)')

    // Next call spawns a fresh child and completes
    const second = client.callToolText('codegraph_find', {})
    await vi.waitFor(() => expect(held).toHaveLength(2))
    held[1].proc.respond(held[1].id, toolText('{"alive":true}'))
    expect(JSON.parse(await second).alive).toBe(true)
    expect(spawnFn).toHaveBeenCalledTimes(2)
  })

  it('includes captured stderr in the exit error', async () => {
    const held: number[] = []
    const { client, procs } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/call') held.push(msg.id!)
    })
    const inflight = client.callToolText('x', {})
    await vi.waitFor(() => expect(held).toHaveLength(1))
    procs[0].stderr.write('failed to connect to neo4j\n')
    await new Promise((r) => setTimeout(r, 10))
    procs[0].emit('exit', 1)
    const err = await inflight.catch((e) => e)
    expect((err as Error).message).toContain('failed to connect to neo4j')
  })

  it('listToolsWithSchema returns each tool including its inputSchema', async () => {
    const { client } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/list') {
        proc.respond(msg.id!, {
          tools: [
            {
              name: 'codegraph_find',
              description: 'find symbols',
              inputSchema: {
                type: 'object',
                properties: { query: { type: 'string' }, service_name: { type: 'string' } }
              }
            }
          ]
        })
      }
    })
    const tools = await client.listToolsWithSchema()
    expect(tools).toHaveLength(1)
    expect(tools[0]).toMatchObject({ name: 'codegraph_find', description: 'find symbols' })
    expect(tools[0].inputSchema).toEqual({
      type: 'object',
      properties: { query: { type: 'string' }, service_name: { type: 'string' } }
    })
  })

  it('listToolsWithSchema substitutes an empty object schema when a tool omits inputSchema', async () => {
    const { client } = makeClient((msg, proc) => {
      if (handshake(msg, proc)) return
      if (msg.method === 'tools/list') {
        proc.respond(msg.id!, { tools: [{ name: 't', description: 'd' }] })
      }
    })
    const tools = await client.listToolsWithSchema()
    expect(tools[0].inputSchema).toEqual({ type: 'object', properties: {} })
  })

  it('trips a cooldown after a crash loop and fails fast without spawning', async () => {
    let clock = 1_000_000
    // Children die immediately after spawn, before answering anything
    const procs: FakeProc[] = []
    const spawnFn = vi.fn(() => {
      const p = new FakeProc()
      procs.push(p)
      setImmediate(() => p.emit('exit', 1))
      return p
    })
    const client = new McpClient({
      binPath: '/fake/bin/codegraph-mcp',
      spawnFn,
      crashLoopThreshold: 3,
      crashLoopCooldownMs: 15_000,
      now: () => clock
    })

    for (let i = 0; i < 3; i++) {
      const err = await client.callToolText('t', {}).catch((e) => e)
      expect((err as McpRequestError).kind).toBe('process')
    }
    expect(spawnFn).toHaveBeenCalledTimes(3)

    // Cooldown active: no fourth spawn, immediate cooldown error
    const err = await client.callToolText('t', {}).catch((e) => e)
    expect((err as McpRequestError).kind).toBe('cooldown')
    expect(spawnFn).toHaveBeenCalledTimes(3)

    // After the cooldown window, spawning resumes
    clock += 20_000
    const errAfter = await client.callToolText('t', {}).catch((e) => e)
    expect((errAfter as McpRequestError).kind).toBe('process')
    expect(spawnFn).toHaveBeenCalledTimes(4)
  })
})
