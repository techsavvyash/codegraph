// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import cytoscape from 'cytoscape'
import type { Core } from 'cytoscape'
import type { GraphEdge, GraphNode } from '$lib/types/graph'
import { createCanvasController, type CanvasController } from './elements'

function node(id: string, label = 'Function', name = id): GraphNode {
  return { node_id: id, label, name }
}

function edge(from: string, to: string, type = 'CALLS', strategy?: string, confidence?: number): GraphEdge {
  return { from, to, type, strategy, confidence }
}

describe('createCanvasController', () => {
  let cy: Core
  let controller: CanvasController
  let selectCalls: (string | null)[]
  let pinCalls: string[]
  let expandCalls: string[]

  beforeEach(() => {
    cy = cytoscape({ headless: true, styleEnabled: false })
    selectCalls = []
    pinCalls = []
    expandCalls = []
    controller = createCanvasController(cy, {
      onSelect: (id) => selectCalls.push(id),
      onTogglePin: (id) => pinCalls.push(id),
      onExpandRequest: (id) => expandCalls.push(id)
    })
  })

  afterEach(() => {
    controller.destroy()
    cy.destroy()
  })

  it('adds the initial working set of nodes and edges', () => {
    const nodes = [node('a'), node('b'), node('c')]
    const edges = [edge('a', 'b'), edge('b', 'c')]

    controller.sync(nodes, edges, null, [])

    expect(cy.nodes().length).toBe(3)
    expect(cy.edges().length).toBe(2)
    expect(cy.nodes().map((n) => n.id()).sort()).toEqual(['a', 'b', 'c'])
  })

  it('incrementally adds a node and removes another without touching untouched nodes positions', () => {
    controller.sync([node('a'), node('b'), node('c')], [edge('a', 'b'), edge('b', 'c')], null, [])

    // Grab position object identity for the node that survives the update.
    const bPositionBefore = cy.getElementById('b').position()

    controller.sync([node('a'), node('b'), node('d')], [edge('a', 'b')], null, [])

    expect(cy.nodes().map((n) => n.id()).sort()).toEqual(['a', 'b', 'd'])
    expect(cy.getElementById('c').length).toBe(0)
    expect(cy.getElementById('d').length).toBe(1)

    const bPositionAfter = cy.getElementById('b').position()
    // Cytoscape's position() returns the live backing object for an existing
    // node — identity must survive since 'b' was never removed/re-added.
    expect(bPositionAfter).toBe(bPositionBefore)
  })

  it('removes a node and its incident edges together', () => {
    controller.sync([node('a'), node('b'), node('c')], [edge('a', 'b'), edge('b', 'c')], null, [])

    expect(cy.edges().length).toBe(2)

    // Drop 'b' out of the working set: both incident edges must go with it.
    controller.sync([node('a'), node('c')], [], null, [])

    expect(cy.nodes().length).toBe(2)
    expect(cy.edges().length).toBe(0)
    expect(cy.getElementById('a|CALLS|b').length).toBe(0)
    expect(cy.getElementById('b|CALLS|c').length).toBe(0)
  })

  it('toggles the is-selected class onto exactly the selected node', () => {
    controller.sync([node('a'), node('b')], [], 'a', [])

    expect(cy.getElementById('a').hasClass('is-selected')).toBe(true)
    expect(cy.getElementById('b').hasClass('is-selected')).toBe(false)

    controller.sync([node('a'), node('b')], [], 'b', [])

    expect(cy.getElementById('a').hasClass('is-selected')).toBe(false)
    expect(cy.getElementById('b').hasClass('is-selected')).toBe(true)
  })

  it('toggles the is-selected class off when selection is cleared', () => {
    controller.sync([node('a')], [], 'a', [])
    expect(cy.getElementById('a').hasClass('is-selected')).toBe(true)

    controller.sync([node('a')], [], null, [])
    expect(cy.getElementById('a').hasClass('is-selected')).toBe(false)
  })

  it('applies is-pinned to every pinned node id', () => {
    controller.sync([node('a'), node('b'), node('c')], [], null, ['a', 'c'])

    expect(cy.getElementById('a').hasClass('is-pinned')).toBe(true)
    expect(cy.getElementById('b').hasClass('is-pinned')).toBe(false)
    expect(cy.getElementById('c').hasClass('is-pinned')).toBe(true)
  })

  it('is idempotent: syncing the same working set twice does not change element counts', () => {
    const nodes = [node('a'), node('b')]
    const edges = [edge('a', 'b')]

    controller.sync(nodes, edges, null, [])
    controller.sync(nodes, edges, null, [])

    expect(cy.nodes().length).toBe(2)
    expect(cy.edges().length).toBe(1)
  })

  it('invokes onSelect(id) on node tap', () => {
    controller.sync([node('a')], [], null, [])
    cy.getElementById('a').emit('tap')

    expect(selectCalls).toEqual(['a'])
  })

  it('invokes onSelect(null) on background tap', () => {
    controller.sync([node('a')], [], null, [])
    cy.emit('tap')

    expect(selectCalls).toEqual([null])
  })

  it('invokes onExpandRequest(id) on node double-tap', () => {
    controller.sync([node('a')], [], null, [])
    cy.getElementById('a').emit('dbltap')

    expect(expandCalls).toEqual(['a'])
  })

  it('invokes onTogglePin(id) on alt-click of a node', () => {
    controller.sync([node('a')], [], null, [])
    // Plain-object emit form: cytoscape's Emitter copies `originalEvent`
    // (and other Event fields) straight from this object onto the Event
    // it constructs, unlike the emit(name, extraParamsArray) form which
    // only appends extra positional handler arguments. Not reflected in
    // @types' EventNames = string, so cast past the declared overload.
    type PlainEventEmit = (obj: { type: string; originalEvent: unknown }) => void
    ;(cy.getElementById('a').emit as unknown as PlainEventEmit)({ type: 'tap', originalEvent: { altKey: true } })

    expect(pinCalls).toEqual(['a'])
    expect(selectCalls).toEqual([])
  })

  it('invokes onTogglePin(id) on meta-click of a node', () => {
    controller.sync([node('a')], [], null, [])
    type PlainEventEmit = (obj: { type: string; originalEvent: unknown }) => void
    ;(cy.getElementById('a').emit as unknown as PlainEventEmit)({ type: 'tap', originalEvent: { metaKey: true } })

    expect(pinCalls).toEqual(['a'])
    expect(selectCalls).toEqual([])
  })

  it('does not react to events after destroy()', () => {
    controller.sync([node('a')], [], null, [])
    controller.destroy()
    cy.getElementById('a').emit('tap')

    expect(selectCalls).toEqual([])
  })
})
