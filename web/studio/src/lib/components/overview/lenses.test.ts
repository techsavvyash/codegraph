import { describe, it, expect } from 'vitest'
import {
  strongEdges,
  projectFlow,
  usageDepths,
  heatBuckets,
  aggregateDead,
  deadBucket
} from './lenses'
import type { RenderEdge } from '$lib/types/overview'
import type { FlowStep } from '$lib/types/flows'

function agg(id: string, source: string, target: string, weight: number): RenderEdge {
  return { id, source, target, weight, kind: 'aggregate' }
}

function step(
  nodeKey: string,
  filePath: string | undefined,
  opts: { parentKey?: string; depth?: number; order?: number; name?: string } = {}
): FlowStep {
  return {
    nodeKey,
    name: opts.name ?? nodeKey,
    label: 'Function',
    order: opts.order ?? 0,
    depth: opts.depth ?? 0,
    parentKey: opts.parentKey,
    filePath
  }
}

describe('strongEdges', () => {
  it('keeps every node its single heaviest incident edge (nothing goes edge-less)', () => {
    // a hub b/c/d each connect to a with different weights; with maxEdges tiny,
    // each of b/c/d still keeps its heaviest (to a) so none is orphaned.
    const edges = [
      agg('e1', 'a', 'b', 5),
      agg('e2', 'a', 'c', 3),
      agg('e3', 'a', 'd', 1)
    ]
    const kept = strongEdges(edges, 0) // no fill: only per-node heaviest
    const keptIds = new Set(kept.map((e) => e.id))
    // e1 covers a+b, e2 covers c, e3 covers d → all three needed
    expect(keptIds).toEqual(new Set(['e1', 'e2', 'e3']))
  })

  it('fills up to maxEdges by descending weight after the per-node pass', () => {
    // Two disjoint components so the per-node pass keeps the two heaviest, then
    // fill adds the next heaviest until the cap.
    const edges = [
      agg('e1', 'a', 'b', 10),
      agg('e2', 'a', 'b', 9), // parallel-ish (distinct id) — a,b already covered by e1
      agg('e3', 'c', 'd', 8),
      agg('e4', 'c', 'd', 2)
    ]
    // per-node pass keeps e1 (a,b) and e3 (c,d). Fill to 3 adds the next
    // heaviest overall not yet kept → e2 (9).
    const kept = strongEdges(edges, 3)
    expect(new Set(kept.map((e) => e.id))).toEqual(new Set(['e1', 'e2', 'e3']))
  })

  it('breaks weight ties deterministically by edge id', () => {
    const edges = [agg('zzz', 'a', 'b', 5), agg('aaa', 'c', 'd', 5)]
    // per-node pass covers both a-b and c-d already; fill cap 1 can add nothing
    // new, but the per-node pass itself must be id-stable: process aaa before zzz.
    const kept = strongEdges(edges, 0)
    expect(new Set(kept.map((e) => e.id))).toEqual(new Set(['aaa', 'zzz']))
  })

  it('returns [] for no edges', () => {
    expect(strongEdges([], 100)).toEqual([])
  })

  it('never keeps more than the total edge count even with a huge cap', () => {
    const edges = [agg('e1', 'a', 'b', 1), agg('e2', 'b', 'c', 2)]
    expect(strongEdges(edges, 1000)).toHaveLength(2)
  })

  it('treats a negative/NaN cap as no fill (per-node heaviest only)', () => {
    const edges = [agg('e1', 'a', 'b', 3), agg('e2', 'a', 'c', 1)]
    const keptNeg = strongEdges(edges, -5)
    // e1 covers a+b, e2 needed to cover c → both kept regardless (per-node rule)
    expect(new Set(keptNeg.map((e) => e.id))).toEqual(new Set(['e1', 'e2']))
    expect(strongEdges(edges, NaN)).toHaveLength(2)
  })
})

describe('projectFlow', () => {
  // visibleOf: identity over a small path→node map; null for unknown paths.
  const vmap = new Map<string, string>([
    ['a.go', 'na'],
    ['b.go', 'nb'],
    ['c.go', 'nc']
  ])
  const visibleOf = (p: string) => vmap.get(p) ?? null

  it('lights nodes and matches segments onto existing base edges', () => {
    const steps = [
      step('k0', 'a.go'),
      step('k1', 'b.go', { parentKey: 'k0', depth: 1 })
    ]
    const base = [agg('agg:na->nb', 'na', 'nb', 4)]
    const proj = projectFlow(steps, visibleOf, base)
    expect([...proj.nodeIds].sort()).toEqual(['na', 'nb'])
    expect([...proj.onEdgeIds]).toEqual(['agg:na->nb'])
    expect(proj.extraSegments).toEqual([])
    expect(proj.missing).toBe(0)
  })

  it('emits an extra segment when no base edge coincides', () => {
    const steps = [step('k0', 'a.go'), step('k1', 'c.go', { parentKey: 'k0' })]
    const proj = projectFlow(steps, visibleOf, [])
    expect(proj.onEdgeIds.size).toBe(0)
    expect(proj.extraSegments).toEqual([{ source: 'na', target: 'nc' }])
  })

  it('uses parentKey (not array order) to find the predecessor', () => {
    // Steps are out of adjacency order; k2's parent is k0, not the preceding k1.
    const steps = [
      step('k0', 'a.go'),
      step('k1', 'b.go', { parentKey: 'k0' }),
      step('k2', 'c.go', { parentKey: 'k0' })
    ]
    const proj = projectFlow(steps, visibleOf, [])
    // segments: na->nb (k1), na->nc (k2). NOT nb->nc.
    expect(proj.extraSegments).toEqual([
      { source: 'na', target: 'nb' },
      { source: 'na', target: 'nc' }
    ])
  })

  it('dedups repeated segments and drops self-segments', () => {
    // Two children rolling to the same visible node as their parent → self, drop.
    const vself = (p: string) => (p === 'x.go' || p === 'y.go' ? 'shared' : (vmap.get(p) ?? null))
    const steps = [
      step('k0', 'x.go'),
      step('k1', 'y.go', { parentKey: 'k0' }), // shared->shared self, dropped
      step('k2', 'a.go', { parentKey: 'k0' }),
      step('k3', 'a.go', { parentKey: 'k1' }) // shared->na duplicate of k2's shared->na
    ]
    const proj = projectFlow(steps, vself, [])
    expect(proj.extraSegments).toEqual([{ source: 'shared', target: 'na' }])
  })

  it('counts steps with no filePath or an off-screen path as missing, never crashing', () => {
    const steps = [
      step('k0', 'a.go'),
      step('k1', undefined, { parentKey: 'k0' }), // no path
      step('k2', 'gone.go', { parentKey: 'k0' }) // path not in vmap → null
    ]
    const proj = projectFlow(steps, visibleOf, [])
    expect(proj.missing).toBe(2)
    expect([...proj.nodeIds]).toEqual(['na'])
    expect(proj.extraSegments).toEqual([])
  })

  it('lights a child even when its parent is off-screen (no segment then)', () => {
    const steps = [
      step('k0', 'gone.go'),
      step('k1', 'b.go', { parentKey: 'k0' })
    ]
    const proj = projectFlow(steps, visibleOf, [])
    expect([...proj.nodeIds]).toEqual(['nb'])
    expect(proj.extraSegments).toEqual([])
    expect(proj.missing).toBe(1)
  })

  it('ignores symbol-kind base edges when matching segments', () => {
    const steps = [step('k0', 'a.go'), step('k1', 'b.go', { parentKey: 'k0' })]
    const symEdge: RenderEdge = { id: 'sym:na->nb', source: 'na', target: 'nb', weight: 1, kind: 'symbol' }
    const proj = projectFlow(steps, visibleOf, [symEdge])
    // symbol edge does not count as a base match → extra segment instead
    expect(proj.onEdgeIds.size).toBe(0)
    expect(proj.extraSegments).toEqual([{ source: 'na', target: 'nb' }])
  })
})

describe('usageDepths', () => {
  const pairs = [
    { fromPath: 'a', toPath: 'b', weight: 1 },
    { fromPath: 'b', toPath: 'c', weight: 1 },
    { fromPath: 'x', toPath: 'c', weight: 1 } // x also calls c
  ]

  it('walks downstream (callees) from a seed', () => {
    const d = usageDepths(pairs, new Set(['a']), 'down', 5)
    expect(d.get('a')).toBe(0)
    expect(d.get('b')).toBe(1)
    expect(d.get('c')).toBe(2)
    expect(d.has('x')).toBe(false)
  })

  it('walks upstream (callers) from a seed', () => {
    const d = usageDepths(pairs, new Set(['c']), 'up', 5)
    expect(d.get('c')).toBe(0)
    expect(d.get('b')).toBe(1)
    expect(d.get('x')).toBe(1) // direct caller of c
    expect(d.get('a')).toBe(2) // caller of b
  })

  it('caps at maxDepth (nodes beyond the cap are not visited)', () => {
    const d = usageDepths(pairs, new Set(['a']), 'down', 1)
    expect(d.get('a')).toBe(0)
    expect(d.get('b')).toBe(1)
    expect(d.has('c')).toBe(false)
  })

  it('records the shortest depth when multiple paths reach a node', () => {
    // diamond: a→b, a→c, b→d, c→d ; d reachable at depth 2 either way
    const diamond = [
      { fromPath: 'a', toPath: 'b', weight: 1 },
      { fromPath: 'a', toPath: 'c', weight: 1 },
      { fromPath: 'b', toPath: 'd', weight: 1 },
      { fromPath: 'c', toPath: 'd', weight: 1 }
    ]
    const d = usageDepths(diamond, new Set(['a']), 'down', 9)
    expect(d.get('d')).toBe(2)
  })

  it('seeds land at depth 0 with no incident pairs', () => {
    const d = usageDepths(pairs, new Set(['lonely']), 'down', 5)
    expect(d.get('lonely')).toBe(0)
    expect(d.size).toBe(1)
  })

  it('returns empty for no seeds', () => {
    expect(usageDepths(pairs, new Set(), 'down', 5).size).toBe(0)
  })

  it('handles multiple seeds, all depth 0', () => {
    const d = usageDepths(pairs, new Set(['a', 'x']), 'down', 5)
    expect(d.get('a')).toBe(0)
    expect(d.get('x')).toBe(0)
    expect(d.get('b')).toBe(1)
    expect(d.get('c')).toBe(1) // reached from x (or b), min depth 1
  })
})

describe('heatBuckets', () => {
  it('assigns no entry to zero-in-weight nodes', () => {
    // a→b only: b has in-weight, a has none.
    const b = heatBuckets([agg('e', 'a', 'b', 4)])
    expect(b.has('a')).toBe(false)
    expect(b.get('b')).toBe(4) // single populated node → hottest bucket
  })

  it('spreads a range across buckets 0..4 with the hottest at 4', () => {
    const edges = [
      agg('e1', 's', 'n1', 1),
      agg('e2', 's', 'n2', 2),
      agg('e3', 's', 'n3', 3),
      agg('e4', 's', 'n4', 4),
      agg('e5', 's', 'n5', 5)
    ]
    const b = heatBuckets(edges)
    expect(b.get('n1')).toBe(0)
    expect(b.get('n5')).toBe(4)
    // monotonic non-decreasing with weight
    expect(b.get('n1')! <= b.get('n2')!).toBe(true)
    expect(b.get('n4')! <= b.get('n5')!).toBe(true)
  })

  it('sums incoming weight across multiple edges into a node', () => {
    const edges = [agg('e1', 'a', 't', 2), agg('e2', 'b', 't', 3), agg('e3', 'c', 'u', 1)]
    const b = heatBuckets(edges)
    // t has in-weight 5, u has 1 → t hotter
    expect(b.get('t')!).toBeGreaterThan(b.get('u')!)
  })

  it('gives tied weights the same bucket', () => {
    const edges = [
      agg('e1', 's', 'n1', 5),
      agg('e2', 's', 'n2', 5),
      agg('e3', 's', 'n3', 1)
    ]
    const b = heatBuckets(edges)
    expect(b.get('n1')).toBe(b.get('n2'))
  })

  it('ignores self-edges and non-positive weights', () => {
    const b = heatBuckets([agg('self', 'a', 'a', 9), agg('zero', 'a', 'b', 0)])
    expect(b.size).toBe(0)
  })

  it('returns empty when nothing has in-weight', () => {
    expect(heatBuckets([]).size).toBe(0)
  })
})

describe('aggregateDead', () => {
  const vmap = new Map<string, string>([
    ['pkg/a.go', 'dirA'],
    ['pkg/b.go', 'dirA'], // same visible dir as a.go
    ['other/c.go', 'fileC']
  ])
  const visibleOf = (p: string) => vmap.get(p) ?? null

  it('folds verdicts onto the visible node, counting per class', () => {
    const entries = [
      { name: 'X', filePath: 'pkg/a.go', verdict: 'dead' },
      { name: 'Y', filePath: 'pkg/b.go', verdict: 'dead' },
      { name: 'Z', filePath: 'pkg/a.go', verdict: 'possibly_live' },
      { name: 'W', filePath: 'other/c.go', verdict: 'test_only' }
    ]
    const m = aggregateDead(entries, visibleOf)
    expect(m.get('dirA')).toMatchObject({ dead: 2, possiblyLive: 1, testOnly: 0 })
    expect(m.get('dirA')!.deadNames.sort()).toEqual(['X', 'Y'])
    expect(m.get('fileC')).toMatchObject({ dead: 0, possiblyLive: 0, testOnly: 1 })
  })

  it('ignores entries whose filePath is off-screen or empty', () => {
    const entries = [
      { name: 'X', filePath: 'gone/z.go', verdict: 'dead' },
      { name: 'Y', filePath: '', verdict: 'dead' }
    ]
    expect(aggregateDead(entries, visibleOf).size).toBe(0)
  })

  it('caps deadNames at 50 but keeps counting past the cap', () => {
    const entries = Array.from({ length: 60 }, (_, i) => ({
      name: `fn${i}`,
      filePath: 'pkg/a.go',
      verdict: 'dead'
    }))
    const m = aggregateDead(entries, visibleOf)
    expect(m.get('dirA')!.dead).toBe(60)
    expect(m.get('dirA')!.deadNames).toHaveLength(50)
  })

  it('ignores verdict classes outside the ramp (live/unknown)', () => {
    const entries = [
      { name: 'L', filePath: 'pkg/a.go', verdict: 'live' },
      { name: 'U', filePath: 'pkg/a.go', verdict: 'unknown' }
    ]
    const m = aggregateDead(entries, visibleOf)
    // both ignored → dirA never created
    expect(m.has('dirA')).toBe(false)
  })
})

describe('deadBucket', () => {
  it('buckets dead counts 1 / 2-4 / >=5 into 1 / 2 / 3', () => {
    expect(deadBucket(0)).toBe(0)
    expect(deadBucket(1)).toBe(1)
    expect(deadBucket(2)).toBe(2)
    expect(deadBucket(4)).toBe(2)
    expect(deadBucket(5)).toBe(3)
    expect(deadBucket(100)).toBe(3)
  })
})
