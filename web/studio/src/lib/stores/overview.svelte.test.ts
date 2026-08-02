import { describe, it, expect, vi } from 'vitest'
import { OverviewStore } from './overview.svelte'
import { dirId, emptyVisibility } from '$lib/components/overview/model'
import type { OverviewResponse, FileSymbolsResponse } from '$lib/types/overview'

function envJson(data: unknown, warnings: string[] = []): Response {
  return new Response(JSON.stringify({ warnings, data }), {
    status: 200,
    headers: { 'content-type': 'application/json' }
  })
}

function errJson(status: number, error: string, kind: string): Response {
  return new Response(JSON.stringify({ error, kind }), {
    status,
    headers: { 'content-type': 'application/json' }
  })
}

const OVERVIEW: OverviewResponse = {
  service: 'codegraph',
  files: [
    { nodeId: 'f1', path: 'internal/graph/client.go', language: 'Go', lineCount: 100, symbolCount: 2 },
    { nodeId: 'f2', path: 'internal/search/index.go', language: 'Go', lineCount: 50, symbolCount: 1 }
  ],
  edges: [{ fromPath: 'internal/graph/client.go', toPath: 'internal/search/index.go', fnWeight: 3, moduleWeight: 0 }]
}

const DRILL_F1: FileSymbolsResponse = {
  file: { nodeId: 'f1', path: 'internal/graph/client.go' },
  symbols: [
    { nodeId: 's1', name: 'Connect', label: 'Method', startLine: 10, inCalls: 2, outCalls: [], externalOutCalls: 1 }
  ]
}

describe('OverviewStore.load', () => {
  it('loads files/edges and reports loaded status', async () => {
    const fetchFn = vi.fn(async () => envJson(OVERVIEW, ['w1']))
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    expect(s.status).toBe('loaded')
    expect(s.service).toBe('codegraph')
    expect(s.warnings).toEqual(['w1'])
    // internal branch-points into graph/search → collapsed root shows one dir
    expect(s.stats).toEqual({ dirs: 1, files: 0, symbols: 0 })
  })

  it('surfaces an MCP-down error', async () => {
    const fetchFn = vi.fn(async () => errJson(503, 'MCP server exited', 'process'))
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    expect(s.status).toBe('error')
    expect(s.error).toContain('MCP server exited')
    expect(s.isEmpty).toBe(false)
  })

  it('reports isEmpty for a service with no files', async () => {
    const fetchFn = vi.fn(async () => envJson({ service: 'empty', files: [], edges: [] }))
    const s = new OverviewStore(fetchFn)
    await s.load('empty')
    expect(s.isEmpty).toBe(true)
    expect(s.stats).toEqual({ dirs: 0, files: 0, symbols: 0 })
  })

  it('skips a redundant load of the already-loaded service', async () => {
    const fetchFn = vi.fn(async () => envJson(OVERVIEW))
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    await s.load('codegraph')
    expect(fetchFn).toHaveBeenCalledTimes(1)
  })

  it('reloads the same service when force is set', async () => {
    const fetchFn = vi.fn(async () => envJson(OVERVIEW))
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    await s.load('codegraph', 'main', { force: true })
    expect(fetchFn).toHaveBeenCalledTimes(2)
  })
})

describe('OverviewStore expansion', () => {
  it('toggleDir reveals children in the visible graph', async () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    await s.load('codegraph')
    s.toggleDir('internal')
    // internal replaced by graph + search dir nodes
    expect(s.stats.dirs).toBe(2)
    s.toggleDir('internal')
    expect(s.stats.dirs).toBe(1)
  })

  it('expandFile lazily fetches the drilldown once and caches it', async () => {
    const fetchFn = vi.fn(async (url: string) => {
      if (url.startsWith('/api/overview/file')) return envJson(DRILL_F1)
      return envJson(OVERVIEW)
    })
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    s.toggleDir('internal')
    s.toggleDir('internal/graph')
    await s.expandFile('f1')
    expect(s.drilldowns.get('f1')).toHaveLength(1)
    // one file (f1) is a compound with one symbol child
    expect(s.stats.symbols).toBe(1)

    s.collapseFile('f1')
    await s.expandFile('f1')
    // second expand does not re-fetch
    const drillCalls = fetchFn.mock.calls.filter((c) => String(c[0]).startsWith('/api/overview/file'))
    expect(drillCalls).toHaveLength(1)
  })

  it('records a per-file error and leaves the file collapsed on drilldown failure', async () => {
    const fetchFn = vi.fn(async (url: string) => {
      if (url.startsWith('/api/overview/file')) return errJson(422, 'bad query', 'tool-error')
      return envJson(OVERVIEW)
    })
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    await s.expandFile('f1')
    expect(s.expandedFiles.has('f1')).toBe(false)
    expect(s.drillErrors.get('f1')).toContain('bad query')
  })
})

describe('OverviewStore selection + handoff', () => {
  it('workbenchSeed builds a File GraphNode for a selected file', async () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    await s.load('codegraph')
    s.select('f1')
    const seed = s.workbenchSeed()
    expect(seed).toMatchObject({ node_id: 'f1', label: 'File', name: 'client.go', file_path: 'internal/graph/client.go' })
  })

  it('workbenchSeed builds a symbol GraphNode for a selected symbol', async () => {
    const fetchFn = vi.fn(async (url: string) => (url.startsWith('/api/overview/file') ? envJson(DRILL_F1) : envJson(OVERVIEW)))
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    s.toggleDir('internal')
    s.toggleDir('internal/graph')
    await s.expandFile('f1')
    s.select('s1')
    const seed = s.workbenchSeed()
    expect(seed).toMatchObject({ node_id: 's1', label: 'Method', name: 'Connect' })
  })

  it('workbenchSeed returns null with no selection', async () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    await s.load('codegraph')
    expect(s.workbenchSeed()).toBeNull()
  })
})

describe('OverviewStore deep-link', () => {
  it('encodeOpen reflects expanded dirs and files', async () => {
    const fetchFn = vi.fn(async (url: string) => (url.startsWith('/api/overview/file') ? envJson(DRILL_F1) : envJson(OVERVIEW)))
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    s.toggleDir('internal')
    s.toggleDir('internal/graph')
    await s.expandFile('f1')
    const open = s.encodeOpen()
    expect(open).toContain(encodeURIComponent('internal'))
    expect(open).toContain(encodeURIComponent('f:f1'))
  })

  it('restore re-applies dirs and fetches drilldowns for expanded files', async () => {
    const fetchFn = vi.fn(async (url: string) => (url.startsWith('/api/overview/file') ? envJson(DRILL_F1) : envJson(OVERVIEW)))
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    const state = emptyVisibility()
    state.expandedDirs.add('internal')
    state.expandedDirs.add('internal/graph')
    state.expandedFiles.add('f1')
    await s.restore(state)
    expect(s.expandedDirs.has('internal')).toBe(true)
    expect(s.expandedFiles.has('f1')).toBe(true)
    expect(s.drilldowns.get('f1')).toHaveLength(1)
  })

  it('restore ignores expanded-file ids not present in the loaded service', async () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    await s.load('codegraph')
    const state = emptyVisibility()
    state.expandedFiles.add('ghost')
    await s.restore(state)
    expect(s.expandedFiles.has('ghost')).toBe(false)
  })
})

describe('dirId helper wiring', () => {
  it('collapsed root dir uses the dir: id prefix', async () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    await s.load('codegraph')
    expect(s.graph.nodes[0].id).toBe(dirId('internal'))
  })
})
