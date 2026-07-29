import { describe, it, expect, vi } from 'vitest'
import { expandNode, findNodes, findPath, getSource, ValidationError } from './api'
import type { McpClient } from '$lib/server/mcp/client'

/** Minimal stub matching McpClient.callTool's signature, with a canned resolved value. */
function stubClient(resolved: unknown = { warnings: [], data: {} }) {
  return {
    callTool: vi.fn().mockResolvedValue(resolved)
  } as unknown as McpClient
}

describe('findNodes', () => {
  it('forwards query and defaults', async () => {
    const client = stubClient()
    await findNodes(client, { query: 'Client' })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_find', { query: 'Client' })
  })

  it('forwards label search with service/scope_id/limit/cursor/semantic', async () => {
    const client = stubClient()
    await findNodes(client, {
      label: 'Function',
      service: 'codegraph',
      scope_id: 'main',
      limit: 50,
      cursor: 'abc',
      semantic: true
    })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_find', {
      label: 'Function',
      service: 'codegraph',
      scope_id: 'main',
      limit: 50,
      cursor: 'abc',
      semantic: true
    })
  })

  it('passes both query and label through when both given', async () => {
    const client = stubClient()
    await findNodes(client, { query: 'Client', label: 'Class' })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_find', {
      query: 'Client',
      label: 'Class'
    })
  })

  it('passes envelope through verbatim, including warnings', async () => {
    const envelope = { warnings: ['AllNodesScan on X'], data: { count: 1, results: [] } }
    const client = stubClient(envelope)

    const result = await findNodes(client, { query: 'x' })

    expect(result).toBe(envelope)
    expect(result.warnings).toEqual(['AllNodesScan on X'])
  })

  it('throws ValidationError when neither query nor label provided', async () => {
    const client = stubClient()
    await expect(findNodes(client, {})).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })

  it('throws ValidationError when query and label are only whitespace', async () => {
    const client = stubClient()
    await expect(findNodes(client, { query: '   ', label: '' })).rejects.toThrow(ValidationError)
  })

  it('clamps limit within 1..200 via ValidationError on out-of-range', async () => {
    const client = stubClient()
    await expect(findNodes(client, { query: 'x', limit: 0 })).rejects.toThrow(ValidationError)
    await expect(findNodes(client, { query: 'x', limit: 201 })).rejects.toThrow(ValidationError)
  })

  it('rejects a non-integer limit', async () => {
    const client = stubClient()
    await expect(findNodes(client, { query: 'x', limit: 1.5 })).rejects.toThrow(ValidationError)
  })

  it('rejects a non-boolean semantic value', async () => {
    const client = stubClient()
    // @ts-expect-error deliberately wrong type to test runtime validation
    await expect(findNodes(client, { query: 'x', semantic: 'true' })).rejects.toThrow(ValidationError)
  })
})

describe('expandNode', () => {
  it('forwards required params with defaults applied', async () => {
    const client = stubClient()
    await expandNode(client, { node_id: 'n1', rel_types: ['CALLS'] })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_expand', {
      node_id: 'n1',
      rel_types: ['CALLS'],
      direction: 'out',
      depth: 3,
      max_nodes: 500
    })
  })

  it('forwards explicit direction/depth/max_nodes', async () => {
    const client = stubClient()
    await expandNode(client, {
      node_id: 'n1',
      rel_types: ['CALLS', 'CONTAINS'],
      direction: 'both',
      depth: 5,
      max_nodes: 1000
    })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_expand', {
      node_id: 'n1',
      rel_types: ['CALLS', 'CONTAINS'],
      direction: 'both',
      depth: 5,
      max_nodes: 1000
    })
  })

  it('passes envelope through verbatim', async () => {
    const envelope = { warnings: [], data: { start: {}, nodes: [], edges: [], node_count: 0, edge_count: 0, truncated: false } }
    const client = stubClient(envelope)

    const result = await expandNode(client, { node_id: 'n1', rel_types: ['CALLS'] })

    expect(result).toBe(envelope)
  })

  it('throws ValidationError when node_id is missing', async () => {
    const client = stubClient()
    // @ts-expect-error missing required field
    await expect(expandNode(client, { rel_types: ['CALLS'] })).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })

  it('throws ValidationError when node_id is empty string', async () => {
    const client = stubClient()
    await expect(expandNode(client, { node_id: '', rel_types: ['CALLS'] })).rejects.toThrow(
      ValidationError
    )
  })

  it('throws ValidationError when rel_types is missing', async () => {
    const client = stubClient()
    // @ts-expect-error missing required field
    await expect(expandNode(client, { node_id: 'n1' })).rejects.toThrow(ValidationError)
  })

  it('throws ValidationError when rel_types is empty array', async () => {
    const client = stubClient()
    await expect(expandNode(client, { node_id: 'n1', rel_types: [] })).rejects.toThrow(
      ValidationError
    )
  })

  it('throws ValidationError when rel_types contains a non-string or empty string', async () => {
    const client = stubClient()
    await expect(
      // @ts-expect-error deliberately wrong element type
      expandNode(client, { node_id: 'n1', rel_types: ['CALLS', 42] })
    ).rejects.toThrow(ValidationError)
    await expect(expandNode(client, { node_id: 'n1', rel_types: ['CALLS', ''] })).rejects.toThrow(
      ValidationError
    )
  })

  it('throws ValidationError for an invalid direction', async () => {
    const client = stubClient()
    await expect(
      // @ts-expect-error deliberately wrong enum value
      expandNode(client, { node_id: 'n1', rel_types: ['CALLS'], direction: 'sideways' })
    ).rejects.toThrow(ValidationError)
  })

  it('clamps depth to 1..10', async () => {
    const client = stubClient()
    await expect(
      expandNode(client, { node_id: 'n1', rel_types: ['CALLS'], depth: 0 })
    ).rejects.toThrow(ValidationError)
    await expect(
      expandNode(client, { node_id: 'n1', rel_types: ['CALLS'], depth: 11 })
    ).rejects.toThrow(ValidationError)
  })

  it('clamps max_nodes to 1..2000', async () => {
    const client = stubClient()
    await expect(
      expandNode(client, { node_id: 'n1', rel_types: ['CALLS'], max_nodes: 0 })
    ).rejects.toThrow(ValidationError)
    await expect(
      expandNode(client, { node_id: 'n1', rel_types: ['CALLS'], max_nodes: 2001 })
    ).rejects.toThrow(ValidationError)
  })
})

describe('findPath', () => {
  it('forwards required params with defaults applied', async () => {
    const client = stubClient()
    await findPath(client, { from_id: 'a', to_id: 'b', rel_types: ['CALLS'] })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_path', {
      from_id: 'a',
      to_id: 'b',
      rel_types: ['CALLS'],
      max_hops: 6,
      shortest: true,
      direction: 'out'
    })
  })

  it('forwards explicit max_hops/shortest/direction', async () => {
    const client = stubClient()
    await findPath(client, {
      from_id: 'a',
      to_id: 'b',
      rel_types: ['CALLS'],
      max_hops: 10,
      shortest: false,
      direction: 'both'
    })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_path', {
      from_id: 'a',
      to_id: 'b',
      rel_types: ['CALLS'],
      max_hops: 10,
      shortest: false,
      direction: 'both'
    })
  })

  it('passes envelope through verbatim, including warnings', async () => {
    const envelope = { warnings: ['slow query'], data: { path_count: 0, paths: [] } }
    const client = stubClient(envelope)

    const result = await findPath(client, { from_id: 'a', to_id: 'b', rel_types: ['CALLS'] })

    expect(result).toBe(envelope)
  })

  it('throws ValidationError when from_id or to_id missing', async () => {
    const client = stubClient()
    // @ts-expect-error missing required field
    await expect(findPath(client, { to_id: 'b', rel_types: ['CALLS'] })).rejects.toThrow(
      ValidationError
    )
    // @ts-expect-error missing required field
    await expect(findPath(client, { from_id: 'a', rel_types: ['CALLS'] })).rejects.toThrow(
      ValidationError
    )
    expect(client.callTool).not.toHaveBeenCalled()
  })

  it('throws ValidationError when rel_types missing or empty', async () => {
    const client = stubClient()
    // @ts-expect-error missing required field
    await expect(findPath(client, { from_id: 'a', to_id: 'b' })).rejects.toThrow(ValidationError)
    await expect(
      findPath(client, { from_id: 'a', to_id: 'b', rel_types: [] })
    ).rejects.toThrow(ValidationError)
  })

  it('clamps max_hops to 1..20', async () => {
    const client = stubClient()
    await expect(
      findPath(client, { from_id: 'a', to_id: 'b', rel_types: ['CALLS'], max_hops: 0 })
    ).rejects.toThrow(ValidationError)
    await expect(
      findPath(client, { from_id: 'a', to_id: 'b', rel_types: ['CALLS'], max_hops: 21 })
    ).rejects.toThrow(ValidationError)
  })

  it('throws ValidationError for an invalid direction', async () => {
    const client = stubClient()
    await expect(
      findPath(client, {
        from_id: 'a',
        to_id: 'b',
        rel_types: ['CALLS'],
        // @ts-expect-error deliberately wrong enum value
        direction: 'sideways'
      })
    ).rejects.toThrow(ValidationError)
  })

  it('throws ValidationError for a non-boolean shortest', async () => {
    const client = stubClient()
    await expect(
      findPath(client, {
        from_id: 'a',
        to_id: 'b',
        rel_types: ['CALLS'],
        // @ts-expect-error deliberately wrong type
        shortest: 'yes'
      })
    ).rejects.toThrow(ValidationError)
  })
})

describe('getSource', () => {
  it('forwards node_id with format json', async () => {
    const client = stubClient()
    await getSource(client, { node_id: 'n1' })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_source', {
      node_id: 'n1',
      format: 'json'
    })
  })

  it('passes envelope through verbatim', async () => {
    const envelope = {
      warnings: [],
      data: { kind: 'code', name: 'Foo', lang: 'go', source: 'func Foo() {}' }
    }
    const client = stubClient(envelope)

    const result = await getSource(client, { node_id: 'n1' })

    expect(result).toBe(envelope)
  })

  it('throws ValidationError when node_id is missing or empty', async () => {
    const client = stubClient()
    // @ts-expect-error missing required field
    await expect(getSource(client, {})).rejects.toThrow(ValidationError)
    await expect(getSource(client, { node_id: '' })).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })
})
