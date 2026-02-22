import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import { resolve } from 'node:path'

// Path relative to repo root (CWD when running `pnpm dev` from apps/chat-ui/)
const MCP_BINARY = resolve(process.cwd(), '../../bin/codegraph-mcp')

type Pending = { resolve: (v: unknown) => void; reject: (e: Error) => void }

class MCPClient {
  private proc: ChildProcess | null = null
  private pending = new Map<number, Pending>()
  private seq = 1
  private initialized = false

  private getProc(): ChildProcess {
    if (this.proc) return this.proc

    this.proc = spawn(MCP_BINARY, [], {
      stdio: ['pipe', 'pipe', 'inherit'],
      env: { ...process.env }
    })

    const rl = createInterface({ input: this.proc.stdout! })
    rl.on('line', (line) => {
      try {
        const msg = JSON.parse(line)
        // Ignore notifications (no id)
        if (msg.id === undefined || msg.id === null) return
        const cb = this.pending.get(msg.id)
        if (!cb) return
        this.pending.delete(msg.id)
        if (msg.error) cb.reject(new Error(msg.error.message ?? JSON.stringify(msg.error)))
        else cb.resolve(msg.result)
      } catch {
        // non-JSON line from MCP (e.g. debug output) — ignore
      }
    })

    this.proc.on('exit', (code) => {
      console.warn(`[mcp-client] process exited (code=${code}), will respawn on next call`)
      this.proc = null
      this.initialized = false
      // Reject all pending
      for (const [id, cb] of this.pending) {
        cb.reject(new Error(`MCP process exited (code=${code})`))
        this.pending.delete(id)
      }
    })

    this.proc.on('error', (err) => {
      console.error('[mcp-client] spawn error:', err.message)
    })

    // MCP initialize handshake (notification, no id)
    this.writeRaw({
      jsonrpc: '2.0',
      method: 'initialize',
      params: {
        protocolVersion: '2024-11-05',
        capabilities: {},
        clientInfo: { name: 'chat-ui', version: '1.0.0' }
      }
    })
    this.initialized = true

    return this.proc
  }

  private writeRaw(obj: unknown) {
    const proc = this.getProc()
    proc.stdin!.write(JSON.stringify(obj) + '\n')
  }

  private request<T>(method: string, params: unknown): Promise<T> {
    const id = this.seq++
    const proc = this.getProc()
    return new Promise((resolve, reject) => {
      this.pending.set(id, {
        resolve: (r) => resolve(r as T),
        reject
      })
      proc.stdin!.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n')
    })
  }

  async listTools(): Promise<Array<{ name: string; description: string; inputSchema: unknown }>> {
    const result = await this.request<{ tools: Array<{ name: string; description: string; inputSchema: unknown }> }>(
      'tools/list',
      {}
    )
    return result.tools
  }

  async callTool(name: string, args: Record<string, unknown>): Promise<string> {
    const result = await this.request<{ content?: Array<{ text?: string }> }>(
      'tools/call',
      { name, arguments: args }
    )
    return result?.content?.map(c => c.text ?? '').join('\n') ?? JSON.stringify(result)
  }
}

// Singleton — one subprocess per server process lifetime
export const mcpClient = new MCPClient()
