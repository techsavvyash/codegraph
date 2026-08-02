import { describe, it, expect } from 'vitest'
import {
  mergeEdges,
  fetchOverview,
  fetchFileSymbols,
  ValidationError,
  OVERVIEW_FILES_QUERY,
  FILE_SYMBOLS_QUERY
} from './api'
import type { McpClient } from '$lib/server/mcp/client'

/**
 * A fake McpClient that answers callTool by matching the query against a script
 * of {match, rows, warnings}. Lets us exercise the fan-in/mapping logic without
 * a live graph — the queries themselves are validated against the dev graph in
 * the e2e/manual layer.
 */
function fakeClient(
  script: Array<{ match: RegExp; rows: unknown[]; warnings?: string[] }>
): McpClient {
  return {
    async callTool<T>(_name: string, args: Record<string, unknown>): Promise<{ warnings: string[]; data: T }> {
      const query = String(args.query)
      const entry = script.find((s) => s.match.test(query))
      if (!entry) throw new Error(`unexpected query: ${query.slice(0, 60)}`)
      return {
        warnings: entry.warnings ?? [],
        data: { columns: [], row_count: entry.rows.length, rows: entry.rows } as unknown as T
      }
    }
  } as unknown as McpClient
}

describe('mergeEdges', () => {
  it('fills fnWeight and moduleWeight into one FileEdge per pair', () => {
    const edges = mergeEdges(
      [{ fromPath: 'a.go', toPath: 'b.go', weight: 4 }],
      [{ fromPath: 'a.go', toPath: 'b.go', weight: 2 }]
    )
    expect(edges).toHaveLength(1)
    expect(edges[0]).toEqual({ fromPath: 'a.go', toPath: 'b.go', fnWeight: 4, moduleWeight: 2 })
  })

  it('keeps fn-only and module-only pairs with the missing side at 0', () => {
    const edges = mergeEdges(
      [{ fromPath: 'a.go', toPath: 'b.go', weight: 4 }],
      [{ fromPath: 'c.go', toPath: 'd.go', weight: 5 }]
    )
    const byPair = Object.fromEntries(edges.map((e) => [`${e.fromPath}->${e.toPath}`, e]))
    expect(byPair['a.go->b.go']).toMatchObject({ fnWeight: 4, moduleWeight: 0 })
    expect(byPair['c.go->d.go']).toMatchObject({ fnWeight: 0, moduleWeight: 5 })
  })

  it('treats direction as significant (a→b and b→a are distinct pairs)', () => {
    const edges = mergeEdges(
      [
        { fromPath: 'a.go', toPath: 'b.go', weight: 1 },
        { fromPath: 'b.go', toPath: 'a.go', weight: 3 }
      ],
      []
    )
    expect(edges).toHaveLength(2)
  })

  it('skips rows with a null endpoint', () => {
    const edges = mergeEdges([{ fromPath: null, toPath: 'b.go', weight: 4 }], [])
    expect(edges).toHaveLength(0)
  })
})

describe('fetchOverview', () => {
  const client = fakeClient([
    {
      match: /MATCH \(f:File \{serviceName: \$service/,
      rows: [
        { nodeId: 'f1', path: 'internal/graph/client.go', language: 'Go', lineCount: 120, symbolCount: 3 },
        { nodeId: 'f2', path: 'main.go', language: 'Go', lineCount: 10, symbolCount: null }
      ],
      warnings: ['files-warn']
    },
    {
      match: /-\[:CONTAINS\]->\(a\)/, // fn edges query
      rows: [{ fromPath: 'main.go', toPath: 'internal/graph/client.go', weight: 7 }],
      warnings: ['fn-warn']
    },
    {
      match: /MATCH \(f1:File \{serviceName: \$service, scopeId: \$scope\}\)-\[:CALLS\]->/, // module edges
      rows: [{ fromPath: 'main.go', toPath: 'internal/graph/client.go', weight: 2 }],
      warnings: ['fn-warn'] // duplicate warning — must dedupe
    }
  ])

  it('assembles files, merged edges, and deduped warnings', async () => {
    const env = await fetchOverview(client, { service: 'codegraph', scopeId: 'main' })
    expect(env.data.service).toBe('codegraph')
    expect(env.data.files).toHaveLength(2)
    // null symbolCount coerced to 0
    expect(env.data.files.find((f) => f.path === 'main.go')?.symbolCount).toBe(0)
    expect(env.data.edges).toHaveLength(1)
    expect(env.data.edges[0]).toMatchObject({ fnWeight: 7, moduleWeight: 2 })
    expect(env.warnings.sort()).toEqual(['files-warn', 'fn-warn'])
  })

  it('rejects a blank service', async () => {
    await expect(fetchOverview(client, { service: '', scopeId: 'main' })).rejects.toBeInstanceOf(
      ValidationError
    )
  })

  it('rejects a blank scope', async () => {
    await expect(fetchOverview(client, { service: 'codegraph', scopeId: '' })).rejects.toBeInstanceOf(
      ValidationError
    )
  })

  it('pages past the 1000-row MCP clamp instead of truncating the file list', async () => {
    // A service with 1005 files: page 0 returns a full 1000 rows (the MCP
    // row_limit clamp), page 1 the remaining 5. The paginated fetch must issue
    // SKIP/LIMIT requests until a short page and stitch every row together.
    const fileRow = (i: number) => ({
      nodeId: `f${i}`,
      path: `pkg/file${String(i).padStart(4, '0')}.go`,
      language: 'Go',
      lineCount: 1,
      symbolCount: 1
    })
    const offsetsSeen: number[] = []
    const paged = {
      async callTool<T>(_name: string, args: Record<string, unknown>) {
        const query = String(args.query)
        const params = (args.params ?? {}) as { offset?: number; pageSize?: number }
        if (/OPTIONAL MATCH \(f\)-\[:CONTAINS\]/.test(query)) {
          expect(query).toContain('SKIP toInteger($offset) LIMIT toInteger($pageSize)')
          expect(args.row_limit).toBe(1000)
          const offset = params.offset ?? 0
          offsetsSeen.push(offset)
          const rows = Array.from({ length: offset === 0 ? 1000 : 5 }, (_, i) => fileRow(offset + i))
          return { warnings: [], data: { columns: [], row_count: rows.length, rows } as unknown as T }
        }
        // both edge queries: empty single page
        return { warnings: [], data: { columns: [], row_count: 0, rows: [] } as unknown as T }
      }
    } as unknown as McpClient

    const env = await fetchOverview(paged, { service: 'big', scopeId: 'main' })
    expect(offsetsSeen).toEqual([0, 1000])
    expect(env.data.files).toHaveLength(1005)
    expect(env.data.files[0].nodeId).toBe('f0')
    expect(env.data.files[1004].nodeId).toBe('f1004')
  })
})

describe('fetchFileSymbols', () => {
  const client = fakeClient([
    { match: /RETURN elementId\(f\) AS nodeId, f\.path AS path$/m, rows: [{ nodeId: 'f1', path: 'internal/graph/client.go' }] },
    {
      match: /-\[:CONTAINS\]->\(fn\) WHERE fn:Function OR fn:Method\nCALL/,
      rows: [
        {
          nodeId: 's1',
          name: 'Connect',
          label: 'Method',
          startLine: 10,
          inCalls: 4,
          sameSvcOut: [{ targetId: 't1', targetName: 'helper', targetPath: 'internal/graph/util.go' }],
          externalOutCalls: 2
        },
        {
          nodeId: 's2',
          name: null,
          label: null,
          startLine: null,
          inCalls: null,
          sameSvcOut: null,
          externalOutCalls: null
        }
      ]
    }
  ])

  it('maps symbols, defaulting nulls and defaulting label to Function', async () => {
    const env = await fetchFileSymbols(client, { fileNodeId: 'f1' })
    expect(env.data.file).toEqual({ nodeId: 'f1', path: 'internal/graph/client.go' })
    expect(env.data.symbols).toHaveLength(2)
    const s1 = env.data.symbols[0]
    expect(s1).toMatchObject({ name: 'Connect', label: 'Method', inCalls: 4, externalOutCalls: 2 })
    expect(s1.outCalls).toEqual([{ targetId: 't1', targetName: 'helper', targetPath: 'internal/graph/util.go' }])
    const s2 = env.data.symbols[1]
    expect(s2).toMatchObject({ name: '(anonymous)', label: 'Function', inCalls: 0, externalOutCalls: 0 })
    expect(s2.outCalls).toEqual([])
  })

  it('throws ValidationError when the file id is unknown (empty head)', async () => {
    const empty = fakeClient([
      { match: /RETURN elementId\(f\) AS nodeId, f\.path AS path$/m, rows: [] },
      { match: /CALL/, rows: [] }
    ])
    await expect(fetchFileSymbols(empty, { fileNodeId: 'ghost' })).rejects.toBeInstanceOf(ValidationError)
  })

  it('rejects a blank node id', async () => {
    await expect(fetchFileSymbols(client, { fileNodeId: '' })).rejects.toBeInstanceOf(ValidationError)
  })
})

describe('query anchors', () => {
  it('files query is anchored on the File label + service param (no AllNodesScan)', () => {
    expect(OVERVIEW_FILES_QUERY).toContain('(f:File {serviceName: $service, scopeId: $scope})')
  })
  it('symbols query anchors on elementId and filters same-service out-calls', () => {
    expect(FILE_SYMBOLS_QUERY).toContain('elementId(f) = $id')
    expect(FILE_SYMBOLS_QUERY).toContain('o.targetService = svc')
  })
})
