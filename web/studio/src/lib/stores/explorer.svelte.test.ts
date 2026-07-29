import { describe, it, expect, vi } from 'vitest'
import { ExplorerStore, foundToGraphNode, type FetchLike } from './explorer.svelte'
import type { ExpandResponse, FoundNode, GraphNode } from '$lib/types/graph'

function node(id: string, extra: Partial<GraphNode> = {}): GraphNode {
  return { node_id: id, label: 'Function', name: `fn-${id}`, ...extra }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' }
  })
}

const found: FoundNode = {
  node_id: 'n1',
  node_key: 'k1',
  label: 'Method',
  name: 'matchChunks',
  signature: 'sig',
  file_path: 'internal/x.go',
  service: 'codegraph',
  start_line: 41,
  end_line: 141,
  score: 10
}

describe('foundToGraphNode', () => {
  it('maps fields and drops empty strings', () => {
    const g = foundToGraphNode({ ...found, file_path: '', signature: '' })
    expect(g.node_id).toBe('n1')
    expect(g.file_path).toBeUndefined()
    expect(g.signature).toBeUndefined()
    expect(g.service).toBe('codegraph')
  })
})

describe('ExplorerStore working set', () => {
  it('adds nodes and only keeps edges with both endpoints loaded', () => {
    const s = new ExplorerStore()
    s.addNode(node('a'))
    s.addNode(node('b'))
    s.mergeExpand({
      start: node('a'),
      nodes: [node('a'), node('c')],
      edges: [
        { from: 'a', to: 'c', type: 'CALLS' },
        { from: 'a', to: 'ghost', type: 'CALLS' }
      ],
      node_count: 2,
      edge_count: 2,
      truncated: false
    })
    expect(s.nodes.size).toBe(3)
    expect(s.edgeList).toEqual([{ from: 'a', to: 'c', type: 'CALLS' }])
  })

  it('merging a slim record onto a rich one keeps the rich fields', () => {
    const s = new ExplorerStore()
    s.addNode(node('a', { file_path: 'x.go', start_line: 3 }))
    s.addNode(node('a')) // slim re-add (e.g. from a path result)
    expect(s.nodes.get('a')!.file_path).toBe('x.go')
    expect(s.nodes.get('a')!.start_line).toBe(3)
  })

  it('removeNode drops incident edges, selection, and pin', () => {
    const s = new ExplorerStore()
    s.addNode(node('a'))
    s.addNode(node('b'))
    s.mergeExpand({
      start: node('a'),
      nodes: [],
      edges: [{ from: 'a', to: 'b', type: 'CALLS' }],
      node_count: 0,
      edge_count: 1,
      truncated: false
    })
    s.select('a')
    s.togglePin('a')
    s.removeNode('a')
    expect(s.nodes.has('a')).toBe(false)
    expect(s.edges.size).toBe(0)
    expect(s.selected).toBeNull()
    expect(s.pinned).toEqual([])
  })

  it('togglePin keeps at most two pins with FIFO eviction and unpins on re-toggle', () => {
    const s = new ExplorerStore()
    s.togglePin('a')
    s.togglePin('b')
    s.togglePin('c')
    expect(s.pinned).toEqual(['b', 'c'])
    s.togglePin('b')
    expect(s.pinned).toEqual(['c'])
  })

  it('toParams round-trips the working set shape', () => {
    const s = new ExplorerStore()
    s.addNode(node('a'))
    s.addNode(node('b'))
    s.select('a')
    s.togglePin('b')
    const p = s.toParams()
    expect(p.get('nodes')).toBe('a,b')
    expect(p.get('sel')).toBe('a')
    expect(p.get('pin')).toBe('b')
  })
})

describe('ExplorerStore API actions', () => {
  const expandResponse: ExpandResponse = {
    start: node('a'),
    nodes: [node('a'), node('b')],
    edges: [{ from: 'a', to: 'b', type: 'CALLS' }],
    node_count: 2,
    edge_count: 1,
    truncated: true
  }

  it('expand posts the right body, merges, and surfaces warnings + truncation', async () => {
    const fetchFn = vi.fn<FetchLike>().mockResolvedValue(
      jsonResponse({ warnings: ['AllNodesScan'], data: expandResponse })
    )
    const s = new ExplorerStore(fetchFn)
    s.addNode(node('a'))
    const ok = await s.expand('a', ['CALLS'], 'out', 2)
    expect(ok).toBe(true)
    expect(fetchFn).toHaveBeenCalledWith('/api/expand', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ node_id: 'a', rel_types: ['CALLS'], direction: 'out', depth: 2 })
    })
    expect(s.nodes.has('b')).toBe(true)
    expect(s.warnings).toContain('AllNodesScan')
    expect(s.warnings.some((w) => w.includes('truncated'))).toBe(true)
    expect(s.pending).toBe(0)
  })

  it('a failing call sets error, returns false, and leaves the set untouched', async () => {
    const fetchFn = vi.fn<FetchLike>().mockResolvedValue(
      jsonResponse({ error: 'unknown node_id', kind: 'tool-error' }, 422)
    )
    const s = new ExplorerStore(fetchFn)
    s.addNode(node('a'))
    const ok = await s.expand('a', ['CALLS'])
    expect(ok).toBe(false)
    expect(s.error).toContain('unknown node_id')
    expect(s.nodes.size).toBe(1)
    expect(s.pending).toBe(0)
  })

  it('path merges hydrated nodes and returns the count', async () => {
    const fetchFn = vi.fn<FetchLike>().mockResolvedValue(
      jsonResponse({
        warnings: [],
        data: {
          path_count: 1,
          paths: [
            {
              hops: 1,
              nodes: [
                { node_id: 'a', label: 'Method', name: 'x' },
                { node_id: 'b', label: 'Method', name: 'y' }
              ],
              edges: [{ from: 'a', to: 'b', type: 'CALLS' }]
            }
          ]
        }
      })
    )
    const s = new ExplorerStore(fetchFn)
    const count = await s.path('a', 'b', ['CALLS'])
    expect(count).toBe(1)
    expect(s.nodes.size).toBe(2)
    expect(s.edges.size).toBe(1)
  })

  it('edgesOnly expand stitches edges without growing the node set', async () => {
    const fetchFn = vi.fn<FetchLike>().mockResolvedValue(
      jsonResponse({
        warnings: [],
        data: {
          start: node('a'),
          nodes: [node('a'), node('b'), node('stranger')],
          edges: [
            { from: 'a', to: 'b', type: 'CALLS' },
            { from: 'a', to: 'stranger', type: 'CALLS' }
          ],
          node_count: 3,
          edge_count: 2,
          truncated: true
        }
      })
    )
    const s = new ExplorerStore(fetchFn)
    s.addNode(node('a'))
    s.addNode(node('b'))
    const ok = await s.expand('a', ['CALLS'], 'out', 1, { edgesOnly: true, quiet: true })
    expect(ok).toBe(true)
    expect(s.nodes.size).toBe(2) // 'stranger' was not admitted
    expect(s.edgeList).toEqual([{ from: 'a', to: 'b', type: 'CALLS' }])
    expect(s.warnings).toEqual([]) // quiet suppresses the truncation notice
  })

  it('hydrate with stitch rels loads nodes then draws edges among the set only', async () => {
    const fetchFn = vi.fn<FetchLike>().mockImplementation(async (_url, init) => {
      const body = JSON.parse(String(init?.body)) as { node_id: string; max_nodes?: number }
      if (body.max_nodes === 1) {
        // hydration pass: bare node only
        return jsonResponse({
          warnings: [],
          data: {
            start: node(body.node_id),
            nodes: [node(body.node_id)],
            edges: [],
            node_count: 1,
            edge_count: 0,
            truncated: true
          }
        })
      }
      // stitch pass: each node's callees incl. one outside the working set
      const callee = body.node_id === 'a' ? 'b' : 'a'
      return jsonResponse({
        warnings: [],
        data: {
          start: node(body.node_id),
          nodes: [node(body.node_id), node(callee), node('outsider')],
          edges: [
            { from: body.node_id, to: callee, type: 'CALLS' },
            { from: body.node_id, to: 'outsider', type: 'CALLS' }
          ],
          node_count: 3,
          edge_count: 2,
          truncated: false
        }
      })
    })
    const s = new ExplorerStore(fetchFn)
    await s.hydrate(['a', 'b'], ['CALLS'])
    expect([...s.nodes.keys()].sort()).toEqual(['a', 'b'])
    expect(s.edgeList).toEqual(
      expect.arrayContaining([
        { from: 'a', to: 'b', type: 'CALLS' },
        { from: 'b', to: 'a', type: 'CALLS' }
      ])
    )
    expect(s.edges.size).toBe(2)
    expect(s.pending).toBe(0)
  })

  it('source GET encodes the node id and returns the payload', async () => {
    const fetchFn = vi.fn<FetchLike>().mockResolvedValue(
      jsonResponse({
        warnings: [],
        data: { kind: 'code', name: 'x', lang: 'go', source: 'func x() {}' }
      })
    )
    const s = new ExplorerStore(fetchFn)
    const src = await s.source('4:ab:12')
    expect(fetchFn).toHaveBeenCalledWith('/api/source?node_id=4%3Aab%3A12')
    expect(src?.source).toBe('func x() {}')
  })
})
