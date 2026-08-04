import { describe, expect, it } from 'vitest'
import { buildSpineTree, layoutSpine } from './layout'
import type { FlowStep } from '$lib/types/flows'

function step(partial: Partial<FlowStep> & Pick<FlowStep, 'nodeKey' | 'name' | 'order' | 'depth'>): FlowStep {
  return { label: 'Function', ...partial }
}

describe('buildSpineTree', () => {
  it('places the depth-0 step as the sole root row', () => {
    const steps = [step({ nodeKey: 'root', name: 'handleExpandTool', order: 0, depth: 0 })]
    const rows = buildSpineTree(steps)
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ depth: 0, row: 0, orphan: false })
    expect(rows[0].step.nodeKey).toBe('root')
  })

  it('orders a linear chain by depth', () => {
    const steps = [
      step({ nodeKey: 'a', name: 'a', order: 0, depth: 0 }),
      step({ nodeKey: 'b', name: 'b', order: 0, depth: 1, parentKey: 'a' }),
      step({ nodeKey: 'c', name: 'c', order: 0, depth: 2, parentKey: 'b' })
    ]
    const rows = buildSpineTree(steps)
    expect(rows.map((r) => r.step.nodeKey)).toEqual(['a', 'b', 'c'])
    expect(rows.map((r) => r.depth)).toEqual([0, 1, 2])
    expect(rows.map((r) => r.row)).toEqual([0, 1, 2])
  })

  it('interleaves multi-branch subtrees in DFS order, not breadth-first', () => {
    // root
    //  ├─ b1 (order 0)
    //  │   └─ c1
    //  └─ b2 (order 1)
    //      └─ c2
    const steps = [
      step({ nodeKey: 'root', name: 'root', order: 0, depth: 0 }),
      step({ nodeKey: 'b1', name: 'b1', order: 0, depth: 1, parentKey: 'root' }),
      step({ nodeKey: 'b2', name: 'b2', order: 1, depth: 1, parentKey: 'root' }),
      step({ nodeKey: 'c1', name: 'c1', order: 0, depth: 2, parentKey: 'b1' }),
      step({ nodeKey: 'c2', name: 'c2', order: 0, depth: 2, parentKey: 'b2' })
    ]
    const rows = buildSpineTree(steps)
    // DFS: root, b1, c1 (b1's child before b2), b2, c2
    expect(rows.map((r) => r.step.nodeKey)).toEqual(['root', 'b1', 'c1', 'b2', 'c2'])
    expect(rows.map((r) => r.row)).toEqual([0, 1, 2, 3, 4])
  })

  it('respects sibling `order` even when steps arrive out of order', () => {
    const steps = [
      step({ nodeKey: 'root', name: 'root', order: 0, depth: 0 }),
      step({ nodeKey: 'second', name: 'second', order: 1, depth: 1, parentKey: 'root' }),
      step({ nodeKey: 'first', name: 'first', order: 0, depth: 1, parentKey: 'root' })
    ]
    const rows = buildSpineTree(steps)
    expect(rows.map((r) => r.step.nodeKey)).toEqual(['root', 'first', 'second'])
  })

  it('flags a step whose parentKey has no matching step as an orphan, appended after reachable rows', () => {
    const steps = [
      step({ nodeKey: 'root', name: 'root', order: 0, depth: 0 }),
      step({ nodeKey: 'child', name: 'child', order: 0, depth: 1, parentKey: 'root' }),
      step({ nodeKey: 'lost', name: 'lost', order: 1, depth: 3, parentKey: 'missing-parent' })
    ]
    const rows = buildSpineTree(steps)
    expect(rows.map((r) => r.step.nodeKey)).toEqual(['root', 'child', 'lost'])
    expect(rows.find((r) => r.step.nodeKey === 'lost')).toMatchObject({ orphan: true, depth: 3 })
    expect(rows.find((r) => r.step.nodeKey === 'root')?.orphan).toBe(false)
    expect(rows.find((r) => r.step.nodeKey === 'child')?.orphan).toBe(false)
  })

  it('produces stable DFS order across repeated calls on the same input', () => {
    const steps = [
      step({ nodeKey: 'root', name: 'root', order: 0, depth: 0 }),
      step({ nodeKey: 'b2', name: 'b2', order: 1, depth: 1, parentKey: 'root' }),
      step({ nodeKey: 'b1', name: 'b1', order: 0, depth: 1, parentKey: 'root' }),
      step({ nodeKey: 'c1', name: 'c1', order: 0, depth: 2, parentKey: 'b1' })
    ]
    const first = buildSpineTree(steps).map((r) => r.step.nodeKey)
    const second = buildSpineTree(steps).map((r) => r.step.nodeKey)
    expect(second).toEqual(first)
    expect(first).toEqual(['root', 'b1', 'c1', 'b2'])
  })

  it('returns an empty row list for an empty step array', () => {
    expect(buildSpineTree([])).toEqual([])
  })
})

describe('layoutSpine', () => {
  const opts = { originX: 48, originY: 56, colWidth: 168, rowHeight: 84, chipWidth: 140, chipHeight: 26, padding: 40 }

  it('positions chips on a column/row grid derived from depth and DFS row index', () => {
    const steps = [
      step({ nodeKey: 'a', name: 'a', order: 0, depth: 0 }),
      step({ nodeKey: 'b', name: 'b', order: 0, depth: 1, parentKey: 'a' })
    ]
    const rows = buildSpineTree(steps)
    const layout = layoutSpine(rows, opts)

    const chipA = layout.chips.find((c) => c.step.nodeKey === 'a')!
    const chipB = layout.chips.find((c) => c.step.nodeKey === 'b')!
    expect(chipA.x).toBe(48)
    expect(chipA.y).toBe(56)
    expect(chipB.x).toBe(48 + 168)
    expect(chipB.y).toBe(56 + 84)
  })

  it('emits one depth guide per distinct depth level present, sorted ascending', () => {
    // depth guides reflect tree structure (parent depth + 1), not the raw
    // FlowStep.depth field — a orphan can carry a stale/unreachable depth,
    // but reachable steps' positions must follow the spanning tree.
    const steps = [
      step({ nodeKey: 'a', name: 'a', order: 0, depth: 0 }),
      step({ nodeKey: 'b', name: 'b', order: 0, depth: 1, parentKey: 'a' }),
      step({ nodeKey: 'c', name: 'c', order: 0, depth: 1, parentKey: 'a' }),
      step({ nodeKey: 'd', name: 'd', order: 0, depth: 2, parentKey: 'b' }),
      step({ nodeKey: 'lost', name: 'lost', order: 0, depth: 5, parentKey: 'missing' })
    ]
    const rows = buildSpineTree(steps)
    const layout = layoutSpine(rows, opts)
    expect(layout.depthGuides.map((g) => g.depth)).toEqual([0, 1, 2, 5])
    expect(layout.depthGuides.map((g) => g.x)).toEqual([48, 48 + 168, 48 + 2 * 168, 48 + 5 * 168])
  })

  it('builds one connector per parent→child edge, anchored to the parent bottom and child left', () => {
    const steps = [
      step({ nodeKey: 'a', name: 'a', order: 0, depth: 0 }),
      step({ nodeKey: 'b', name: 'b', order: 0, depth: 1, parentKey: 'a' }),
      step({ nodeKey: 'c', name: 'c', order: 1, depth: 1, parentKey: 'a' })
    ]
    const rows = buildSpineTree(steps)
    const layout = layoutSpine(rows, opts)
    expect(layout.connectors).toHaveLength(2)

    const chipA = layout.chips.find((c) => c.step.nodeKey === 'a')!
    const chipB = layout.chips.find((c) => c.step.nodeKey === 'b')!
    const connAB = layout.connectors.find((c) => c.childKey === 'b')!

    // path starts near the parent's bottom edge and ends at the child's left edge/vertical-center
    expect(connAB.path).toContain(`${chipA.y + chipA.height}`)
    expect(connAB.path.trim().endsWith(`${chipB.x}`)).toBe(true)
    expect(connAB.labelY).toBe(chipB.y + chipB.height / 2)
  })

  it('does not emit a connector for an orphan step', () => {
    const steps = [
      step({ nodeKey: 'a', name: 'a', order: 0, depth: 0 }),
      step({ nodeKey: 'lost', name: 'lost', order: 0, depth: 2, parentKey: 'missing' })
    ]
    const rows = buildSpineTree(steps)
    const layout = layoutSpine(rows, opts)
    expect(layout.connectors).toHaveLength(0)
  })

  it('sizes the stage to cover the deepest column and last row, plus padding', () => {
    const steps = [
      step({ nodeKey: 'a', name: 'a', order: 0, depth: 0 }),
      step({ nodeKey: 'b', name: 'b', order: 0, depth: 1, parentKey: 'a' }),
      step({ nodeKey: 'c', name: 'c', order: 0, depth: 2, parentKey: 'b' })
    ]
    const rows = buildSpineTree(steps)
    const layout = layoutSpine(rows, opts)

    const lastChip = layout.chips.find((c) => c.step.nodeKey === 'c')!
    expect(layout.width).toBe(lastChip.x + opts.chipWidth + opts.padding)
    expect(layout.height).toBe(lastChip.y + opts.chipHeight + opts.padding)
    // every chip must fit inside the reported stage bounds
    for (const chip of layout.chips) {
      expect(chip.x + chip.width).toBeLessThanOrEqual(layout.width)
      expect(chip.y + chip.height).toBeLessThanOrEqual(layout.height)
    }
  })

  it('handles an empty row list without throwing, producing an empty layout', () => {
    const layout = layoutSpine([], opts)
    expect(layout.chips).toEqual([])
    expect(layout.connectors).toEqual([])
    expect(layout.depthGuides).toEqual([])
    expect(layout.width).toBeGreaterThan(0)
    expect(layout.height).toBeGreaterThan(0)
  })
})
