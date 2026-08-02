import { describe, it, expect } from 'vitest'
import { buildCypherArgs, runCypher, ValidationError } from './api'
import type { McpClient } from '$lib/server/mcp/client'
import type { CypherResult } from '$lib/types/console'

describe('buildCypherArgs', () => {
  it('always requests JSON format and passes the query through verbatim', () => {
    const args = buildCypherArgs({ query: 'MATCH (n) RETURN n' })
    expect(args).toEqual({ query: 'MATCH (n) RETURN n', format: 'json' })
  })

  it('rejects empty / whitespace-only / non-string queries', () => {
    expect(() => buildCypherArgs({ query: '' })).toThrow(ValidationError)
    expect(() => buildCypherArgs({ query: '   ' })).toThrow(ValidationError)
    // @ts-expect-error deliberately wrong type
    expect(() => buildCypherArgs({ query: 123 })).toThrow(ValidationError)
  })

  it('passes a valid row_limit through (tool does its own clamping)', () => {
    expect(buildCypherArgs({ query: 'q', row_limit: 25 }).row_limit).toBe(25)
  })

  it('rejects a non-integer or sub-1 row_limit', () => {
    expect(() => buildCypherArgs({ query: 'q', row_limit: 1.5 })).toThrow(ValidationError)
    expect(() => buildCypherArgs({ query: 'q', row_limit: 0 })).toThrow(ValidationError)
  })

  it('passes an object params through and rejects non-object params', () => {
    expect(buildCypherArgs({ query: 'q', params: { x: 1 } }).params).toEqual({ x: 1 })
    // @ts-expect-error deliberately wrong type
    expect(() => buildCypherArgs({ query: 'q', params: [1, 2] })).toThrow(ValidationError)
  })

  it('does NOT rewrite or scope the query (no auto-scoping)', () => {
    const q = 'MATCH (n) RETURN n'
    expect(buildCypherArgs({ query: q }).query).toBe(q)
  })
})

describe('runCypher', () => {
  it('forwards to codegraph_cypher and returns the envelope verbatim', async () => {
    const data: CypherResult = { columns: ['name'], row_count: 0, rows: [], truncated: false }
    const calls: Array<{ name: string; args: Record<string, unknown> }> = []
    const client = {
      callTool: async (name: string, args: Record<string, unknown>) => {
        calls.push({ name, args })
        return { warnings: ['plan contains AllNodesScan'], data }
      }
    } as unknown as McpClient

    const env = await runCypher(client, { query: 'MATCH (s:Service) RETURN s.name AS name' })
    expect(calls[0].name).toBe('codegraph_cypher')
    expect(calls[0].args).toMatchObject({ format: 'json' })
    expect(env.warnings).toEqual(['plan contains AllNodesScan'])
    expect(env.data).toBe(data)
  })
})
