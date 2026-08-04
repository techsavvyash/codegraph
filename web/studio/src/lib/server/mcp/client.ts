import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import { resolve, dirname } from 'node:path'
import { parseToolPayload, type ToolPayload } from './payload'

/**
 * Connection manager for the codegraph MCP server (RFC-012 §4).
 *
 * One long-lived child process speaking newline-delimited JSON-RPC 2.0 over
 * stdio; concurrent requests are multiplexed by id. The child is spawned
 * lazily, restarted on the next call after a crash, and a rapid crash loop
 * trips a cooldown so callers fail fast with a real error instead of
 * hammering a broken binary.
 */

export interface McpProcess {
  stdin: NodeJS.WritableStream | null
  stdout: NodeJS.ReadableStream | null
  stderr: NodeJS.ReadableStream | null
  on(event: 'exit', listener: (code: number | null) => void): unknown
  on(event: 'error', listener: (err: Error) => void): unknown
  kill(): unknown
}

export interface McpClientOptions {
  /** Path to the MCP binary; defaults to $CODEGRAPH_MCP_BIN, then ../../bin/codegraph-mcp relative to cwd. */
  binPath?: string
  /** Injectable spawner for tests. */
  spawnFn?: (binPath: string, cwd: string) => McpProcess
  /** Default per-request timeout. */
  requestTimeoutMs?: number
  /** Consecutive fast crashes before the cooldown trips. */
  crashLoopThreshold?: number
  /** How long calls fail fast after a crash loop. */
  crashLoopCooldownMs?: number
  /** Injectable clock for tests. */
  now?: () => number
}

export class McpRequestError extends Error {
  constructor(
    message: string,
    readonly kind: 'timeout' | 'rpc-error' | 'tool-error' | 'process' | 'cooldown'
  ) {
    super(message)
    this.name = 'McpRequestError'
  }
}

interface Pending {
  resolve: (v: unknown) => void
  reject: (e: Error) => void
  timer: ReturnType<typeof setTimeout>
}

interface ToolContent {
  content?: Array<{ type?: string; text?: string }>
  isError?: boolean
}

const PROTOCOL_VERSION = '2024-11-05'

function defaultSpawn(binPath: string, cwd: string): McpProcess {
  // cwd = bin/ so the server's godotenv.Load("../.env") finds the repo-root .env
  return spawn(binPath, [], {
    stdio: ['pipe', 'pipe', 'pipe'],
    cwd,
    env: { ...process.env }
  }) as ChildProcess as McpProcess
}

export class McpClient {
  private proc: McpProcess | null = null
  private pending = new Map<number, Pending>()
  private seq = 1
  private initPromise: Promise<void> | null = null
  private stderrTail: string[] = []
  private lastSpawnAt = 0
  private fastCrashes = 0
  private cooldownUntil = 0

  private readonly binPath: string
  private readonly spawnFn: (binPath: string, cwd: string) => McpProcess
  private readonly requestTimeoutMs: number
  private readonly crashLoopThreshold: number
  private readonly crashLoopCooldownMs: number
  private readonly now: () => number

  constructor(opts: McpClientOptions = {}) {
    this.binPath = resolve(
      opts.binPath ?? process.env.CODEGRAPH_MCP_BIN ?? resolve(process.cwd(), '../../bin/codegraph-mcp')
    )
    this.spawnFn = opts.spawnFn ?? defaultSpawn
    this.requestTimeoutMs = opts.requestTimeoutMs ?? 30_000
    this.crashLoopThreshold = opts.crashLoopThreshold ?? 3
    this.crashLoopCooldownMs = opts.crashLoopCooldownMs ?? 15_000
    this.now = opts.now ?? Date.now
  }

  /** Call a tool and return its raw text content. Rejects on tool isError. */
  async callToolText(
    name: string,
    args: Record<string, unknown>,
    timeoutMs?: number
  ): Promise<string> {
    const result = (await this.request('tools/call', { name, arguments: args }, timeoutMs)) as ToolContent
    const text = (result.content ?? [])
      .map((c) => c.text ?? '')
      .join('\n')
    if (result.isError) {
      throw new McpRequestError(`${name}: ${text || 'tool error with empty message'}`, 'tool-error')
    }
    return text
  }

  /** Call a JSON-format tool; returns parsed body + guardrail warnings. */
  async callTool<T>(
    name: string,
    args: Record<string, unknown>,
    timeoutMs?: number
  ): Promise<ToolPayload<T>> {
    return parseToolPayload<T>(await this.callToolText(name, args, timeoutMs))
  }

  async listTools(): Promise<Array<{ name: string; description: string }>> {
    const result = (await this.request('tools/list', {})) as {
      tools: Array<{ name: string; description: string }>
    }
    return result.tools
  }

  /**
   * tools/list including each tool's JSON Schema, for the chat tool-loop
   * (which needs the schema to hand to the model and to drive scope injection).
   * listTools() intentionally strips inputSchema; this is the full record.
   */
  async listToolsWithSchema(): Promise<
    Array<{ name: string; description: string; inputSchema: Record<string, unknown> }>
  > {
    const result = (await this.request('tools/list', {})) as {
      tools: Array<{ name: string; description: string; inputSchema?: Record<string, unknown> }>
    }
    return result.tools.map((t) => ({
      name: t.name,
      description: t.description,
      inputSchema: t.inputSchema ?? { type: 'object', properties: {} }
    }))
  }

  /** Liveness probe: true if the child is up (or can be started) and answers tools/list. */
  async healthy(): Promise<boolean> {
    try {
      await this.listTools()
      return true
    } catch {
      return false
    }
  }

  close(): void {
    if (this.proc) {
      this.proc.kill()
      this.proc = null
      this.initPromise = null
    }
  }

  private ensureProc(): McpProcess {
    if (this.proc) return this.proc

    const t = this.now()
    if (t < this.cooldownUntil) {
      throw new McpRequestError(
        `MCP server crash-looping; retrying after ${new Date(this.cooldownUntil).toISOString()}. Last stderr: ${this.stderrTail.join(' | ') || '(empty)'}`,
        'cooldown'
      )
    }

    const proc = this.spawnFn(this.binPath, dirname(this.binPath))
    this.proc = proc
    this.lastSpawnAt = t
    this.stderrTail = []

    const rl = createInterface({ input: proc.stdout! })
    rl.on('line', (line) => this.onLine(line))

    if (proc.stderr) {
      const rlErr = createInterface({ input: proc.stderr })
      rlErr.on('line', (line) => {
        this.stderrTail.push(line)
        if (this.stderrTail.length > 20) this.stderrTail.shift()
      })
    }

    proc.on('error', (err) => {
      this.failAllPending(new McpRequestError(`MCP spawn failed: ${err.message}`, 'process'))
      this.proc = null
      this.initPromise = null
    })

    proc.on('exit', (code) => {
      const aliveMs = this.now() - this.lastSpawnAt
      if (aliveMs < 2_000) {
        this.fastCrashes += 1
        if (this.fastCrashes >= this.crashLoopThreshold) {
          this.cooldownUntil = this.now() + this.crashLoopCooldownMs
          this.fastCrashes = 0
        }
      } else {
        this.fastCrashes = 0
      }
      console.warn(`[mcp] server exited (code=${code}); will respawn on next call`)
      this.failAllPending(
        new McpRequestError(
          `MCP server exited (code=${code}). Last stderr: ${this.stderrTail.join(' | ') || '(empty)'}`,
          'process'
        )
      )
      this.proc = null
      this.initPromise = null
    })

    return proc
  }

  private failAllPending(err: Error): void {
    for (const [id, p] of this.pending) {
      clearTimeout(p.timer)
      p.reject(err)
      this.pending.delete(id)
    }
  }

  private onLine(line: string): void {
    let msg: { id?: number | null; result?: unknown; error?: { message?: string; code?: number } }
    try {
      msg = JSON.parse(line)
    } catch {
      return // non-JSON debug output — ignore
    }
    if (msg.id === undefined || msg.id === null) return // notification
    const p = this.pending.get(msg.id)
    if (!p) return // late response after timeout — ignore
    this.pending.delete(msg.id)
    clearTimeout(p.timer)
    if (msg.error) {
      p.reject(
        new McpRequestError(
          `MCP error ${msg.error.code ?? ''}: ${msg.error.message ?? 'unknown'}`.trim(),
          'rpc-error'
        )
      )
    } else {
      p.resolve(msg.result)
    }
  }

  private write(obj: unknown): void {
    const proc = this.ensureProc()
    proc.stdin!.write(JSON.stringify(obj) + '\n')
  }

  private rawRequest(method: string, params: unknown, timeoutMs: number): Promise<unknown> {
    const id = this.seq++
    return new Promise((resolvePromise, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(
          new McpRequestError(`${method} timed out after ${timeoutMs}ms`, 'timeout')
        )
      }, timeoutMs)
      this.pending.set(id, { resolve: resolvePromise, reject, timer })
      try {
        this.write({ jsonrpc: '2.0', id, method, params })
      } catch (e) {
        this.pending.delete(id)
        clearTimeout(timer)
        reject(e as Error)
      }
    })
  }

  private ensureInitialized(): Promise<void> {
    if (this.initPromise) return this.initPromise
    this.initPromise = (async () => {
      await this.rawRequest(
        'initialize',
        {
          protocolVersion: PROTOCOL_VERSION,
          capabilities: {},
          clientInfo: { name: 'codegraph-studio', version: '0.0.1' }
        },
        this.requestTimeoutMs
      )
      this.write({ jsonrpc: '2.0', method: 'notifications/initialized' })
    })()
    this.initPromise.catch(() => {
      this.initPromise = null
    })
    return this.initPromise
  }

  private async request(method: string, params: unknown, timeoutMs?: number): Promise<unknown> {
    await this.ensureInitialized()
    return this.rawRequest(method, params, timeoutMs ?? this.requestTimeoutMs)
  }
}

/** Singleton used by server routes — one child per node process lifetime. */
export const mcp = new McpClient()
