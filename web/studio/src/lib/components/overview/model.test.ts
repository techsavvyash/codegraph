import { describe, it, expect } from 'vitest'
import {
  buildTree,
  visibleGraph,
  visibleFor,
  reAggregateEdges,
  toggleDir,
  expandFile,
  collapseFile,
  emptyVisibility,
  dirId,
  topConnections,
  encodeOverviewState,
  decodeOverviewState,
  type OverviewVisibility
} from './model'
import type { FileEdge, FileSymbol, OverviewFile } from '$lib/types/overview'

function file(nodeId: string, path: string, symbolCount = 0): OverviewFile {
  return { nodeId, path, language: 'Go', lineCount: 100, symbolCount }
}

function edge(fromPath: string, toPath: string, fnWeight = 0, moduleWeight = 0): FileEdge {
  return { fromPath, toPath, fnWeight, moduleWeight }
}

function sym(
  nodeId: string,
  name: string,
  outCalls: Array<{ targetId: string; targetName: string; targetPath: string }> = [],
  opts: { inCalls?: number; externalOutCalls?: number; label?: 'Function' | 'Method' } = {}
): FileSymbol {
  return {
    nodeId,
    name,
    label: opts.label ?? 'Function',
    startLine: 1,
    inCalls: opts.inCalls ?? 0,
    outCalls,
    externalOutCalls: opts.externalOutCalls ?? 0
  }
}

const NO_DRILL = new Map<string, FileSymbol[]>()

describe('buildTree', () => {
  it('nests files under their directories with basenames as labels', () => {
    const tree = buildTree([
      file('1', 'internal/graph/client.go', 3),
      file('2', 'internal/graph/schema.go', 1)
    ])
    // chain a/b merges to one node labeled "internal/graph"
    expect(tree.root.dirs).toHaveLength(1)
    const merged = tree.root.dirs[0]
    expect(merged.label).toBe('internal/graph')
    expect(merged.path).toBe('internal/graph')
    expect(merged.files.map((f) => f.label)).toEqual(['client.go', 'schema.go'])
  })

  it('merges single-child directory chains into one node with a joined label', () => {
    const tree = buildTree([file('1', 'web/studio/src/app.ts')])
    expect(tree.root.dirs).toHaveLength(1)
    const merged = tree.root.dirs[0]
    expect(merged.label).toBe('web/studio/src')
    expect(merged.path).toBe('web/studio/src')
    expect(merged.files.map((f) => f.label)).toEqual(['app.ts'])
  })

  it('does NOT merge when a directory holds files of its own', () => {
    // web has a file directly + a subdir → the chain breaks at web
    const tree = buildTree([file('1', 'web/index.ts'), file('2', 'web/studio/app.ts')])
    expect(tree.root.dirs).toHaveLength(1)
    const web = tree.root.dirs[0]
    expect(web.label).toBe('web')
    expect(web.files.map((f) => f.label)).toEqual(['index.ts'])
    expect(web.dirs).toHaveLength(1)
    expect(web.dirs[0].label).toBe('studio')
  })

  it('does NOT merge when a directory has multiple children (branch point)', () => {
    const tree = buildTree([
      file('1', 'internal/graph/client.go'),
      file('2', 'internal/search/index.go')
    ])
    expect(tree.root.dirs).toHaveLength(1)
    const internal = tree.root.dirs[0]
    expect(internal.label).toBe('internal')
    expect(internal.dirs.map((d) => d.label).sort()).toEqual(['graph', 'search'])
  })

  it('keeps root-level files hanging off the root', () => {
    const tree = buildTree([file('1', 'main.go', 2), file('2', 'internal/x.go')])
    expect(tree.root.files.map((f) => f.label)).toEqual(['main.go'])
    expect(tree.root.dirs.map((d) => d.label)).toEqual(['internal'])
  })

  it('indexes files by path and by id', () => {
    const tree = buildTree([file('id1', 'a/b.go')])
    expect(tree.fileByPath.get('a/b.go')?.nodeId).toBe('id1')
    expect(tree.fileById.get('id1')?.path).toBe('a/b.go')
  })
})

describe('visibleFor', () => {
  const tree = buildTree([
    file('f1', 'internal/graph/client.go'),
    file('f2', 'internal/search/index.go')
  ])
  // internal is a branch point (two subdirs), so it is a real dir node

  it('resolves to the first collapsed dir when nothing is expanded', () => {
    const state = emptyVisibility()
    expect(visibleFor(tree, 'internal/graph/client.go', state)).toBe(dirId('internal'))
  })

  it('descends through expanded dirs to the next collapsed one', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal']),
      expandedFiles: new Set()
    }
    // internal expanded → graph/search now visible; graph still collapsed
    expect(visibleFor(tree, 'internal/graph/client.go', state)).toBe(dirId('internal/graph'))
  })

  it('resolves to the file itself when every ancestor dir is expanded', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal', 'internal/graph']),
      expandedFiles: new Set()
    }
    expect(visibleFor(tree, 'internal/graph/client.go', state)).toBe('f1')
  })

  it('walks a merged chain as a single unit keyed by its deepest path', () => {
    const chain = buildTree([file('c1', 'web/studio/src/app.ts')])
    const collapsed = emptyVisibility()
    expect(visibleFor(chain, 'web/studio/src/app.ts', collapsed)).toBe(dirId('web/studio/src'))
    const expanded: OverviewVisibility = {
      expandedDirs: new Set(['web/studio/src']),
      expandedFiles: new Set()
    }
    expect(visibleFor(chain, 'web/studio/src/app.ts', expanded)).toBe('c1')
  })
})

describe('visibleGraph — nodes', () => {
  const files = [
    file('f1', 'internal/graph/client.go', 3),
    file('f2', 'internal/graph/schema.go', 2),
    file('f3', 'internal/search/index.go', 5)
  ]
  const tree = buildTree(files)

  it('renders a collapsed dir as one node carrying rolled-up stats', () => {
    const { nodes } = visibleGraph(tree, [], emptyVisibility(), NO_DRILL)
    // internal is the single top dir; collapsed → one node
    expect(nodes).toHaveLength(1)
    const internal = nodes[0]
    expect(internal.kind).toBe('dir')
    expect(internal.id).toBe(dirId('internal'))
    expect(internal.fileCount).toBe(3)
    expect(internal.symbolCount).toBe(10)
  })

  it('replaces an expanded dir with its children (flat, no compound)', () => {
    const state: OverviewVisibility = { expandedDirs: new Set(['internal']), expandedFiles: new Set() }
    const { nodes } = visibleGraph(tree, [], state, NO_DRILL)
    const ids = nodes.map((n) => n.id).sort()
    // internal replaced by graph (2 files) + search (1 file) dir nodes
    expect(ids).toEqual([dirId('internal/graph'), dirId('internal/search')].sort())
    for (const n of nodes) expect(n.parentId).toBeUndefined()
  })

  it('renders expanded files as compound parents with symbol children', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal', 'internal/graph']),
      expandedFiles: new Set(['f1'])
    }
    const drill = new Map<string, FileSymbol[]>([
      ['f1', [sym('s1', 'Connect'), sym('s2', 'Close')]]
    ])
    const { nodes } = visibleGraph(tree, [], state, drill)
    const f1 = nodes.find((n) => n.id === 'f1')
    expect(f1?.kind).toBe('file')
    const symbols = nodes.filter((n) => n.kind === 'symbol')
    expect(symbols.map((s) => s.id).sort()).toEqual(['s1', 's2'])
    for (const s of symbols) expect(s.parentId).toBe('f1')
  })
})

describe('reAggregateEdges', () => {
  const files = [
    file('f1', 'internal/graph/client.go'),
    file('f2', 'internal/graph/schema.go'),
    file('f3', 'internal/search/index.go')
  ]
  const tree = buildTree(files)

  it('rolls fnWeight onto the visible ancestor edge and drops the resulting self-edge', () => {
    // Both files roll up to dir internal when nothing expanded → self-edge dropped.
    const edges = [edge('internal/graph/client.go', 'internal/search/index.go', 4)]
    const collapsed = reAggregateEdges(tree, edges, emptyVisibility(), NO_DRILL)
    expect(collapsed).toHaveLength(0)
  })

  it('surfaces an aggregate edge between distinct visible dirs', () => {
    const state: OverviewVisibility = { expandedDirs: new Set(['internal']), expandedFiles: new Set() }
    const edges = [edge('internal/graph/client.go', 'internal/search/index.go', 4)]
    const out = reAggregateEdges(tree, edges, state, NO_DRILL)
    expect(out).toHaveLength(1)
    expect(out[0]).toMatchObject({
      source: dirId('internal/graph'),
      target: dirId('internal/search'),
      weight: 4,
      kind: 'aggregate'
    })
  })

  it('merges duplicate pairs summing weight', () => {
    const state: OverviewVisibility = { expandedDirs: new Set(['internal']), expandedFiles: new Set() }
    const edges = [
      edge('internal/graph/client.go', 'internal/search/index.go', 4),
      edge('internal/graph/schema.go', 'internal/search/index.go', 3)
    ]
    const out = reAggregateEdges(tree, edges, state, NO_DRILL)
    // both graph files roll to dir internal/graph → one merged edge weight 7
    expect(out).toHaveLength(1)
    expect(out[0].weight).toBe(7)
  })

  it('EXCLUDES fnWeight from the aggregate when the source file is expanded', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal', 'internal/graph', 'internal/search']),
      expandedFiles: new Set(['f1'])
    }
    // f1 expanded, drilldown owns its fn calls → the fnWeight aggregate is gone.
    const edges = [edge('internal/graph/client.go', 'internal/search/index.go', 4)]
    const out = reAggregateEdges(tree, edges, state, new Map([['f1', []]]))
    const aggFromF1 = out.filter((e) => e.source === 'f1' && e.kind === 'aggregate')
    expect(aggFromF1).toHaveLength(0)
  })

  it('KEEPS moduleWeight at the aggregate level even when the source file is expanded', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal', 'internal/graph', 'internal/search']),
      expandedFiles: new Set(['f1'])
    }
    const edges = [edge('internal/graph/client.go', 'internal/search/index.go', 0, 5)]
    const out = reAggregateEdges(tree, edges, state, new Map([['f1', []]]))
    // module edge from the compound file node itself → visible target file f3
    const mod = out.find((e) => e.source === 'f1' && e.target === 'f3')
    expect(mod).toMatchObject({ weight: 5, kind: 'aggregate' })
  })

  it('draws symbol→symbol edges when both files are expanded and targets present', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal', 'internal/graph', 'internal/search']),
      expandedFiles: new Set(['f1', 'f3'])
    }
    const drill = new Map<string, FileSymbol[]>([
      ['f1', [sym('s1', 'A', [{ targetId: 't1', targetName: 'B', targetPath: 'internal/search/index.go' }])]],
      ['f3', [sym('t1', 'B')]]
    ])
    const out = reAggregateEdges(tree, [], state, drill)
    const symEdge = out.find((e) => e.kind === 'symbol')
    expect(symEdge).toMatchObject({ source: 's1', target: 't1', weight: 1 })
  })

  it('routes a symbol out-call to the target visible node when the target file is collapsed', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal', 'internal/graph']),
      expandedFiles: new Set(['f1'])
    }
    const drill = new Map<string, FileSymbol[]>([
      ['f1', [sym('s1', 'A', [{ targetId: 't1', targetName: 'B', targetPath: 'internal/search/index.go' }])]]
    ])
    const out = reAggregateEdges(tree, [], state, drill)
    // internal/search collapsed → symbol edge lands on the dir node
    const symEdge = out.find((e) => e.kind === 'symbol')
    expect(symEdge).toMatchObject({ source: 's1', target: dirId('internal/search') })
  })

  it('merges duplicate symbol out-calls summing weight', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal', 'internal/graph']),
      expandedFiles: new Set(['f1'])
    }
    const drill = new Map<string, FileSymbol[]>([
      [
        'f1',
        [
          sym('s1', 'A', [{ targetId: 't1', targetName: 'B', targetPath: 'internal/search/index.go' }]),
          sym('s2', 'C', [{ targetId: 't1', targetName: 'B', targetPath: 'internal/search/index.go' }])
        ]
      ]
    ])
    const out = reAggregateEdges(tree, [], state, drill)
    const toSearch = out.filter((e) => e.kind === 'symbol' && e.target === dirId('internal/search'))
    // s1 and s2 are distinct sources → two edges, each weight 1 (merge is per pair)
    expect(toSearch).toHaveLength(2)
    for (const e of toSearch) expect(e.weight).toBe(1)
  })

  it('ignores edges referencing a file outside the service set', () => {
    const out = reAggregateEdges(tree, [edge('nope/x.go', 'internal/graph/client.go', 9)], emptyVisibility(), NO_DRILL)
    expect(out).toHaveLength(0)
  })
})

describe('reducers', () => {
  it('toggleDir adds then removes, returning new objects', () => {
    const s0 = emptyVisibility()
    const s1 = toggleDir(s0, 'internal')
    expect(s1).not.toBe(s0)
    expect(s1.expandedDirs.has('internal')).toBe(true)
    const s2 = toggleDir(s1, 'internal')
    expect(s2.expandedDirs.has('internal')).toBe(false)
  })

  it('expandFile / collapseFile toggle the file set immutably', () => {
    const s0 = emptyVisibility()
    const s1 = expandFile(s0, 'f1')
    expect(s1.expandedFiles.has('f1')).toBe(true)
    expect(s0.expandedFiles.has('f1')).toBe(false)
    const s2 = collapseFile(s1, 'f1')
    expect(s2.expandedFiles.has('f1')).toBe(false)
  })
})

describe('topConnections', () => {
  const graph = {
    nodes: [
      { id: 'a', kind: 'dir' as const, label: 'A' },
      { id: 'b', kind: 'dir' as const, label: 'B' },
      { id: 'c', kind: 'dir' as const, label: 'C' }
    ],
    edges: [
      { id: 'e1', source: 'a', target: 'b', weight: 2, kind: 'aggregate' as const },
      { id: 'e2', source: 'a', target: 'c', weight: 5, kind: 'aggregate' as const },
      { id: 'e3', source: 'c', target: 'a', weight: 3, kind: 'aggregate' as const }
    ]
  }

  it('returns incoming and outgoing sorted by weight with resolved labels', () => {
    const { incoming, outgoing } = topConnections(graph, 'a')
    expect(outgoing.map((c) => c.label)).toEqual(['C', 'B']) // 5 then 2
    expect(incoming.map((c) => c.label)).toEqual(['C']) // c→a weight 3
    expect(outgoing[0]).toMatchObject({ nodeId: 'c', weight: 5 })
  })

  it('respects the limit', () => {
    const { outgoing } = topConnections(graph, 'a', 1)
    expect(outgoing).toHaveLength(1)
    expect(outgoing[0].label).toBe('C')
  })

  it('excludes self-edges', () => {
    const selfGraph = {
      nodes: [{ id: 'a', kind: 'dir' as const, label: 'A' }],
      edges: [{ id: 'e', source: 'a', target: 'a', weight: 9, kind: 'aggregate' as const }]
    }
    const { incoming, outgoing } = topConnections(selfGraph, 'a')
    expect(incoming).toHaveLength(0)
    expect(outgoing).toHaveLength(0)
  })
})

describe('deep-link codec', () => {
  it('round-trips dirs and expanded files', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(['internal/graph', 'web/studio/src']),
      expandedFiles: new Set(['4:uuid:126409'])
    }
    const encoded = encodeOverviewState(state)
    const decoded = decodeOverviewState(encoded)
    expect([...decoded.expandedDirs].sort()).toEqual(['internal/graph', 'web/studio/src'])
    expect([...decoded.expandedFiles]).toEqual(['4:uuid:126409'])
  })

  it('URL-encodes entries so colons and commas in ids survive', () => {
    const state: OverviewVisibility = {
      expandedDirs: new Set(),
      expandedFiles: new Set(['4:abc:1,2'])
    }
    const encoded = encodeOverviewState(state)
    // the raw comma is escaped, so splitting on ',' still yields one token
    expect(encoded.split(',')).toHaveLength(1)
    const decoded = decodeOverviewState(encoded)
    expect([...decoded.expandedFiles]).toEqual(['4:abc:1,2'])
  })

  it('is stable/sorted regardless of insertion order', () => {
    const a: OverviewVisibility = { expandedDirs: new Set(['b', 'a']), expandedFiles: new Set() }
    const b: OverviewVisibility = { expandedDirs: new Set(['a', 'b']), expandedFiles: new Set() }
    expect(encodeOverviewState(a)).toBe(encodeOverviewState(b))
  })

  it('returns empty state for null/empty input', () => {
    expect(decodeOverviewState(null).expandedDirs.size).toBe(0)
    expect(decodeOverviewState('').expandedFiles.size).toBe(0)
  })

  it('tolerates malformed entries, keeping the valid ones', () => {
    // '%' is an invalid escape; the valid dir survives
    const decoded = decodeOverviewState('%,internal/graph')
    expect([...decoded.expandedDirs]).toEqual(['internal/graph'])
  })
})
