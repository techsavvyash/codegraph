import { describe, it, expect, vi } from 'vitest'
import { getEntryPoints, listServices, traceFlow, ValidationError } from './api'
import type { McpClient } from '$lib/server/mcp/client'

/** Minimal stub matching McpClient.callTool's signature, with a canned resolved value. */
function stubClient(resolved: unknown = { warnings: [], data: {} }) {
  return {
    callTool: vi.fn().mockResolvedValue(resolved)
  } as unknown as McpClient
}

describe('listServices', () => {
  it('forwards the expected cypher query and format', async () => {
    const client = stubClient({
      warnings: [],
      data: { columns: ['name'], row_count: 2, rows: [{ name: 'a' }, { name: 'b' }] }
    })

    await listServices(client)

    expect(client.callTool).toHaveBeenCalledWith('codegraph_cypher', {
      query: 'MATCH (s:Service) RETURN s.name AS name ORDER BY name',
      format: 'json'
    })
  })

  it('maps rows to a flat services array', async () => {
    const client = stubClient({
      warnings: [],
      data: { columns: ['name'], row_count: 2, rows: [{ name: 'codegraph' }, { name: 'khaata' }] }
    })

    const result = await listServices(client)

    expect(result.data).toEqual({ services: ['codegraph', 'khaata'] })
  })

  it('tolerates rows:null and returns an empty services array', async () => {
    const client = stubClient({
      warnings: [],
      data: { columns: ['name'], row_count: 0, rows: null }
    })

    const result = await listServices(client)

    expect(result.data).toEqual({ services: [] })
  })

  it('preserves warnings from the envelope', async () => {
    const client = stubClient({
      warnings: ['AllNodesScan on Service'],
      data: { columns: ['name'], row_count: 0, rows: null }
    })

    const result = await listServices(client)

    expect(result.warnings).toEqual(['AllNodesScan on Service'])
  })

  it('propagates a tool-error rejection', async () => {
    const client = {
      callTool: vi.fn().mockRejectedValue(new Error('tool exploded'))
    } as unknown as McpClient

    await expect(listServices(client)).rejects.toThrow('tool exploded')
  })
})

describe('getEntryPoints', () => {
  it('forwards service with defaults applied', async () => {
    const client = stubClient()
    await getEntryPoints(client, { service: 'codegraph' })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_entry_points', {
      service_name: 'codegraph',
      limit: 100,
      format: 'json'
    })
  })

  it('forwards explicit tier and limit', async () => {
    const client = stubClient()
    await getEntryPoints(client, { service: 'codegraph', tier: 2, limit: 50 })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_entry_points', {
      service_name: 'codegraph',
      limit: 50,
      format: 'json',
      tier: 2
    })
  })

  it('returns the envelope as-is, including tier_errors passthrough', async () => {
    const envelope = {
      warnings: [],
      data: { count: 0, entries: [], tier_errors: ['tier 3 query timed out'] }
    }
    const client = stubClient(envelope)

    const result = await getEntryPoints(client, { service: 'codegraph' })

    expect(result).toBe(envelope)
  })

  it('throws ValidationError when service is missing or empty', async () => {
    const client = stubClient()
    // @ts-expect-error missing required field
    await expect(getEntryPoints(client, {})).rejects.toThrow(ValidationError)
    await expect(getEntryPoints(client, { service: '' })).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })

  it('rejects tier outside 1..4', async () => {
    const client = stubClient()
    await expect(getEntryPoints(client, { service: 'codegraph', tier: 0 })).rejects.toThrow(
      ValidationError
    )
    await expect(getEntryPoints(client, { service: 'codegraph', tier: 5 })).rejects.toThrow(
      ValidationError
    )
    expect(client.callTool).not.toHaveBeenCalled()
  })

  it('rejects a non-integer tier', async () => {
    const client = stubClient()
    await expect(getEntryPoints(client, { service: 'codegraph', tier: 1.5 })).rejects.toThrow(
      ValidationError
    )
  })

  it('clamps limit at the low end', async () => {
    const client = stubClient()
    await getEntryPoints(client, { service: 'codegraph', limit: 0 })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_entry_points', {
      service_name: 'codegraph',
      limit: 1,
      format: 'json'
    })
  })

  it('clamps limit at the high end', async () => {
    const client = stubClient()
    await getEntryPoints(client, { service: 'codegraph', limit: 500 })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_entry_points', {
      service_name: 'codegraph',
      limit: 200,
      format: 'json'
    })
  })
})

describe('traceFlow', () => {
  it('forwards node_id with defaults applied', async () => {
    const client = stubClient()
    await traceFlow(client, { node_id: 'n1' })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_flows', {
      from: 'n1',
      max_depth: 4,
      persist: false,
      format: 'json'
    })
  })

  it('forwards explicit max_depth', async () => {
    const client = stubClient()
    await traceFlow(client, { node_id: 'n1', max_depth: 7 })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_flows', {
      from: 'n1',
      max_depth: 7,
      persist: false,
      format: 'json'
    })
  })

  it('clamps max_depth at the low end', async () => {
    const client = stubClient()
    await traceFlow(client, { node_id: 'n1', max_depth: 0 })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_flows', {
      from: 'n1',
      max_depth: 1,
      persist: false,
      format: 'json'
    })
  })

  it('clamps max_depth at the high end', async () => {
    const client = stubClient()
    await traceFlow(client, { node_id: 'n1', max_depth: 99 })

    expect(client.callTool).toHaveBeenCalledWith('codegraph_flows', {
      from: 'n1',
      max_depth: 10,
      persist: false,
      format: 'json'
    })
  })

  it('passes an empty flows envelope through verbatim', async () => {
    const envelope = { warnings: [], data: { flow_count: 0, flows: [] } }
    const client = stubClient(envelope)

    const result = await traceFlow(client, { node_id: 'n1' })

    expect(result).toBe(envelope)
  })

  it('preserves warnings from the envelope', async () => {
    const envelope = {
      warnings: ['flow trace hit max_depth before reaching a leaf'],
      data: { flow_count: 1, flows: [] }
    }
    const client = stubClient(envelope)

    const result = await traceFlow(client, { node_id: 'n1' })

    expect(result.warnings).toEqual(['flow trace hit max_depth before reaching a leaf'])
  })

  it('throws ValidationError when node_id is missing or empty', async () => {
    const client = stubClient()
    // @ts-expect-error missing required field
    await expect(traceFlow(client, {})).rejects.toThrow(ValidationError)
    await expect(traceFlow(client, { node_id: '' })).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })

  it('rejects a non-integer max_depth', async () => {
    const client = stubClient()
    await expect(traceFlow(client, { node_id: 'n1', max_depth: 2.5 })).rejects.toThrow(
      ValidationError
    )
  })

  it('propagates a tool-error rejection', async () => {
    const client = {
      callTool: vi.fn().mockRejectedValue(new Error('tool exploded'))
    } as unknown as McpClient

    await expect(traceFlow(client, { node_id: 'n1' })).rejects.toThrow('tool exploded')
  })
})
