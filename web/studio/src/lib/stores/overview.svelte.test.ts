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

// ── lens system ──────────────────────────────────────────────────────────

const ENTRIES_T1 = {
  count: 1,
  entries: [
    {
      node_id: 'e1',
      node_key: 'k1',
      name: 'HandleThing',
      label: 'Function',
      file_path: 'internal/graph/client.go',
      tier: 1,
      tier_label: 'API-exposed'
    }
  ]
}
const EMPTY_ENTRIES = { count: 0, entries: [] }

const FLOW = {
  flow_count: 1,
  flows: [
    {
      flowNodeKey: 'k1',
      flowName: 'HandleThing',
      flowType: 'api',
      steps: [
        { nodeKey: 'k1', name: 'HandleThing', label: 'Function', order: 0, depth: 0, filePath: 'internal/graph/client.go' },
        {
          nodeKey: 'k2',
          name: 'Index',
          label: 'Function',
          order: 1,
          depth: 1,
          parentKey: 'k1',
          filePath: 'internal/search/index.go'
        }
      ]
    }
  ]
}

const DEAD = {
  service: 'codegraph',
  counts: { total: 3, live: 1, testOnly: 0, dead: 2, deadCluster: 0, possiblyLive: 0, unknown: 0 },
  entries: [
    { name: 'Unused', label: 'Function', filePath: 'internal/graph/client.go', startLine: 5, verdict: 'dead', deadCluster: false, isExported: false },
    { name: 'AlsoUnused', label: 'Function', filePath: 'internal/graph/client.go', startLine: 9, verdict: 'dead', deadCluster: false, isExported: false }
  ]
}

/** Routes a fetch by URL to the right canned envelope. */
function lensFetch(overrides: Partial<Record<string, unknown>> = {}) {
  return vi.fn(async (url: string) => {
    if (url.startsWith('/api/overview/file')) return envJson(DRILL_F1)
    if (url.startsWith('/api/overview/dead')) return envJson(overrides.dead ?? DEAD)
    if (url.startsWith('/api/overview/callers')) return envJson(overrides.callers ?? { callers: [] })
    if (url.startsWith('/api/entrypoints')) {
      const tier = new URL(url, 'http://x').searchParams.get('tier')
      return envJson(tier === '1' ? (overrides.entriesT1 ?? ENTRIES_T1) : EMPTY_ENTRIES)
    }
    if (url.startsWith('/api/flow')) return envJson(overrides.flow ?? FLOW)
    return envJson(OVERVIEW)
  })
}

describe('OverviewStore lens state', () => {
  it('defaults to structure lens with strong edge mode', () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    expect(s.lens).toBe('structure')
    expect(s.edgeMode).toBe('strong')
  })

  it('setLens switches lens and does not clear expansion/selection', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    s.toggleDir('internal')
    s.select(dirId('internal/graph'))
    s.setLens('hotspots')
    expect(s.lens).toBe('hotspots')
    expect(s.expandedDirs.has('internal')).toBe(true)
    expect(s.selected).toBe(dirId('internal/graph'))
  })

  it('toggleEdgeMode flips strong ⇄ all', () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    s.toggleEdgeMode()
    expect(s.edgeMode).toBe('all')
    s.toggleEdgeMode()
    expect(s.edgeMode).toBe('strong')
  })

  it('service reload resets lens transient state but not the lens id', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    await s.loadFlowEntries()
    expect(s.flowEntries.length).toBeGreaterThan(0)
    await s.load('codegraph', 'main', { force: true })
    // caches cleared by resetExpansion; lens id preserved (a UI concern)
    expect(s.flowEntries).toEqual([])
    expect(s.flowEntriesStatus).toBe('idle')
    expect(s.activeFlowId).toBeNull()
  })
})

describe('OverviewStore structure decorations', () => {
  it('strong mode limits visibleEdgeIds; all mode is null', async () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    await s.load('codegraph')
    s.toggleDir('internal') // surface the graph→search aggregate edge
    expect(s.graph.edges.length).toBeGreaterThan(0)
    expect(s.decorations.visibleEdgeIds).not.toBeNull()
    s.setEdgeMode('all')
    expect(s.decorations.visibleEdgeIds).toBeNull()
  })
})

describe('OverviewStore flows lens', () => {
  it('loadFlowEntries fetches per tier and dedupes', async () => {
    const fetchFn = lensFetch()
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    await s.loadFlowEntries()
    expect(s.flowEntriesStatus).toBe('loaded')
    expect(s.flowEntries.map((e) => e.node_id)).toEqual(['e1'])
    // one request per tier (4) plus the initial overview load
    const tierCalls = fetchFn.mock.calls.filter((c) => String(c[0]).startsWith('/api/entrypoints'))
    expect(tierCalls).toHaveLength(4)
  })

  it('traceFlow records steps and the flow decoration lights the path', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    s.toggleDir('internal') // so graph/ and search/ are distinct visible nodes
    s.lens = 'flows'
    await s.loadFlowEntries()
    await s.traceFlow(s.flowEntries[0])
    expect(s.activeFlowSteps).toHaveLength(2)
    const dec = s.decorations
    expect(dec.dimUnmatched).toBe(true)
    // both step files roll to distinct dir nodes → both lit as hl-path
    const lit = [...dec.nodeClasses.values()].filter((c) => c === 'hl-path')
    expect(lit.length).toBe(2)
    // the graph→search aggregate edge coincides with the flow segment: it lands
    // as an hl-path edge class (or, if absent from the base set, an extra segment)
    const onPathEdges = [...dec.edgeClasses.values()].filter((c) => c === 'hl-path')
    expect(onPathEdges.length + dec.extraEdges.length).toBeGreaterThan(0)
  })

  it('traceFlow ignores a stale response when a newer trace supersedes it', async () => {
    // Hold the slow trace's resolver in a mutable box (a plain `let` narrows to
    // null since TS can't see the in-callback assignment).
    const gate: { resolve: ((r: Response) => void) | null } = { resolve: null }
    const fetchFn = vi.fn(async (url: string, init?: RequestInit) => {
      if (url.startsWith('/api/flow')) {
        const body = JSON.parse(String(init?.body))
        if (body.node_id === 'slow') {
          return new Promise<Response>((res) => {
            gate.resolve = res
          })
        }
        return envJson(FLOW)
      }
      if (url.startsWith('/api/entrypoints')) return envJson(EMPTY_ENTRIES)
      return envJson(OVERVIEW)
    })
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    const slow = { node_id: 'slow', node_key: 'ks', name: 'Slow', label: 'Function', tier: 1 as const, tier_label: 't1' }
    const fast = { node_id: 'e1', node_key: 'k1', name: 'Fast', label: 'Function', tier: 1 as const, tier_label: 't1' }
    const p1 = s.traceFlow(slow) // stalls
    await s.traceFlow(fast) // supersedes; activeFlowId now e1
    // now let the slow one land — it must NOT overwrite the fast result
    gate.resolve?.(
      envJson({
        flow_count: 1,
        flows: [
          {
            flowNodeKey: 'ks',
            flowName: 'Slow',
            flowType: 'api',
            steps: [{ nodeKey: 'ks', name: 'Slow', label: 'Function', order: 0, depth: 0, filePath: 'internal/graph/client.go' }]
          }
        ]
      })
    )
    await p1
    expect(s.activeFlowId).toBe('e1')
    expect(s.activeFlowName).toBe('Fast')
  })

  it('clearFlow drops the active trace', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    s.lens = 'flows'
    await s.loadFlowEntries()
    await s.traceFlow(s.flowEntries[0])
    s.clearFlow()
    expect(s.activeFlowId).toBeNull()
    expect(s.activeFlowSteps).toEqual([])
  })
})

describe('OverviewStore usage lens', () => {
  it('seeds BFS from a selected file and depth-fades callers', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    // expand fully so files are their own visible nodes
    s.toggleDir('internal')
    s.toggleDir('internal/graph')
    s.toggleDir('internal/search')
    s.setLens('usage')
    s.setUsageDirection('up') // who calls index.go → client.go (client calls search)
    s.select('f2') // internal/search/index.go
    const dec = s.decorations
    expect(dec.dimUnmatched).toBe(true)
    expect(dec.nodeClasses.get('f2')).toBe('usage-d0')
    expect(dec.nodeClasses.get('f1')).toBe('usage-d1') // client.go calls index.go
  })

  it('loadSymbolCallers caches by symbol id', async () => {
    const callers = { callers: [{ name: 'Boot', label: 'File', filePath: 'main.go', service: 'codegraph' }] }
    const fetchFn = lensFetch({ callers })
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    await s.loadSymbolCallers('s1')
    expect(s.symbolCallers.get('s1')).toEqual(callers.callers)
    await s.loadSymbolCallers('s1') // cached — no second call
    const callerCalls = fetchFn.mock.calls.filter((c) => String(c[0]).startsWith('/api/overview/callers'))
    expect(callerCalls).toHaveLength(1)
  })
})

describe('OverviewStore dead lens', () => {
  it('loadDeadReport caches and drives the dead decoration + panel names', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    s.lens = 'dead'
    await s.loadDeadReport()
    expect(s.deadStatus).toBe('loaded')
    expect(s.deadReport?.counts.dead).toBe(2)
    // internal collapsed → both dead entries roll to dir:internal, bucket 2 (2-4)
    const dec = s.decorations
    expect(dec.dimUnmatched).toBe(true)
    expect(dec.nodeClasses.get(dirId('internal'))).toBe('dead-2')
    expect(s.deadNamesFor(dirId('internal')).sort()).toEqual(['AlsoUnused', 'Unused'])
  })

  it('setLens(dead) triggers the one-time fetch', async () => {
    const fetchFn = lensFetch()
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    s.setLens('dead')
    // allow the fire-and-forget fetch to settle
    await vi.waitFor(() => expect(s.deadStatus).toBe('loaded'))
    const deadCalls = fetchFn.mock.calls.filter((c) => String(c[0]).startsWith('/api/overview/dead'))
    expect(deadCalls).toHaveLength(1)
  })
})

describe('OverviewStore hotspots lens', () => {
  it('heat-buckets nodes by incoming weight, no dimming', async () => {
    const s = new OverviewStore(vi.fn(async () => envJson(OVERVIEW)))
    await s.load('codegraph')
    s.toggleDir('internal') // graph→search edge weight 3
    s.setLens('hotspots')
    const dec = s.decorations
    expect(dec.dimUnmatched).toBe(false)
    // search is the sole in-weight node → hottest bucket
    expect(dec.nodeClasses.get(dirId('internal/search'))).toBe('heat-4')
    expect(dec.nodeClasses.has(dirId('internal/graph'))).toBe(false)
  })
})

describe('OverviewStore lens URL helpers', () => {
  it('lensParam omits structure, emits others', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    expect(s.lensParam()).toBe('')
    s.setLens('usage')
    expect(s.lensParam()).toBe('usage')
  })

  it('restoreLens sets the lens and re-traces a deep-linked flow entry', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    await s.restoreLens('flows', 'e1')
    expect(s.lens).toBe('flows')
    expect(s.activeFlowId).toBe('e1')
    expect(s.activeFlowSteps).toHaveLength(2)
  })

  it('restoreLens drops a flow entry that no longer exists', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    await s.restoreLens('flows', 'gone')
    expect(s.lens).toBe('flows')
    expect(s.activeFlowId).toBeNull()
  })

  it('restoreLens falls back to structure for an unknown lens id', async () => {
    const s = new OverviewStore(lensFetch())
    await s.load('codegraph')
    await s.restoreLens('bogus', null)
    expect(s.lens).toBe('structure')
  })
})

describe('OverviewStore lens cache refetch on service switch', () => {
  it('reloads flow entries for the new service when the flows lens stays active', async () => {
    const fetchFn = lensFetch()
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    s.setLens('flows')
    await vi.waitFor(() => expect(s.flowEntriesStatus).toBe('loaded'))
    const before = fetchFn.mock.calls.filter((c) => String(c[0]).startsWith('/api/entrypoints')).length
    expect(before).toBe(4)

    await s.load('other')
    // resetExpansion dropped the cache; load() must have re-kicked the fetch
    await vi.waitFor(() => expect(s.flowEntriesStatus).toBe('loaded'))
    const after = fetchFn.mock.calls.filter((c) => String(c[0]).startsWith('/api/entrypoints')).length
    expect(after).toBe(8)
  })

  it('reloads the dead report for the new service when the dead lens stays active', async () => {
    const fetchFn = lensFetch()
    const s = new OverviewStore(fetchFn)
    await s.load('codegraph')
    s.setLens('dead')
    await vi.waitFor(() => expect(s.deadStatus).toBe('loaded'))

    await s.load('other')
    await vi.waitFor(() => expect(s.deadStatus).toBe('loaded'))
    const deadCalls = fetchFn.mock.calls.filter((c) => String(c[0]).startsWith('/api/overview/dead')).length
    expect(deadCalls).toBe(2)
  })
})

describe('OverviewStore flow symbol precision', () => {
  it('lights the exact drilled symbols a flow touches inside an expanded file', async () => {
    const flow = {
      flow_count: 1,
      flows: [
        {
          flowNodeKey: 'k1',
          flowName: 'Connect',
          flowType: 'api',
          steps: [
            { nodeKey: 'k1', name: 'Connect', label: 'Method', order: 0, depth: 0, filePath: 'internal/graph/client.go' },
            { nodeKey: 'k2', name: 'Index', label: 'Function', order: 1, depth: 1, parentKey: 'k1', filePath: 'internal/search/index.go' }
          ]
        }
      ]
    }
    const s = new OverviewStore(lensFetch({ flow }))
    await s.load('codegraph')
    s.toggleDir('internal')
    s.toggleDir('internal/graph')
    await s.expandFile('f1') // drilldown: symbol s1 named Connect
    s.lens = 'flows'
    await s.loadFlowEntries()
    await s.traceFlow(s.flowEntries[0])

    const dec = s.decorations
    // the expanded file compound is on the path AND its touched symbol is lit
    expect(dec.nodeClasses.get('f1')).toBe('hl-path')
    expect(dec.nodeClasses.get('s1')).toBe('hl-path')
  })
})
