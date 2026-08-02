import { describe, it, expect } from 'vitest'
import { injectScope, type ToolInputSchema } from './scope'
import type { ChatScope } from '$lib/types/chat'

const scoped: ChatScope = { service: 'codegraph', scopeId: 'main' }
const unscoped: ChatScope = { service: null, scopeId: 'main' }
const altScope: ChatScope = { service: 'khaata/backend', scopeId: 'feature-x' }

const schemaBoth: ToolInputSchema = {
  properties: { query: {}, service_name: {}, scope_id: {} }
}
const schemaServiceOnly: ToolInputSchema = { properties: { service_name: {} } }
const schemaScopeOnly: ToolInputSchema = { properties: { scope_id: {} } }
const schemaNeither: ToolInputSchema = { properties: { query: {} } }

describe('injectScope', () => {
  it('injects service_name and scope_id when schema declares them, model unset, service non-null', () => {
    const out = injectScope({ query: 'x' }, schemaBoth, scoped)
    expect(out).toEqual({ query: 'x', service_name: 'codegraph', scope_id: 'main' })
  })

  it('never mutates the input args object', () => {
    const args = { query: 'x' }
    injectScope(args, schemaBoth, scoped)
    expect(args).toEqual({ query: 'x' })
  })

  it('always returns a fresh object even when nothing is injected', () => {
    const args = { query: 'x' }
    const out = injectScope(args, schemaNeither, scoped)
    expect(out).not.toBe(args)
    expect(out).toEqual({ query: 'x' })
  })

  it('does NOT override an explicit model-set service_name', () => {
    const out = injectScope({ service_name: 'other-svc' }, schemaBoth, scoped)
    expect(out.service_name).toBe('other-svc')
    // scope_id is still injected since the model left it unset
    expect(out.scope_id).toBe('main')
  })

  it('does NOT override an explicit model-set scope_id', () => {
    const out = injectScope({ scope_id: 'custom' }, schemaBoth, scoped)
    expect(out.scope_id).toBe('custom')
    expect(out.service_name).toBe('codegraph')
  })

  it('treats an empty-string service_name from the model as unset and injects', () => {
    const out = injectScope({ service_name: '   ' }, schemaBoth, scoped)
    expect(out.service_name).toBe('codegraph')
  })

  it('does not inject service_name when scope.service is null (All services)', () => {
    const out = injectScope({ query: 'x' }, schemaBoth, unscoped)
    expect(out).not.toHaveProperty('service_name')
    // scope_id still injected — it always has a value
    expect(out.scope_id).toBe('main')
  })

  it('does not inject service_name when the schema lacks that property', () => {
    const out = injectScope({ query: 'x' }, schemaScopeOnly, scoped)
    expect(out).not.toHaveProperty('service_name')
    expect(out.scope_id).toBe('main')
  })

  it('does not inject scope_id when the schema lacks that property', () => {
    const out = injectScope({ query: 'x' }, schemaServiceOnly, scoped)
    expect(out.service_name).toBe('codegraph')
    expect(out).not.toHaveProperty('scope_id')
  })

  it('injects nothing when the schema declares neither property', () => {
    const out = injectScope({ query: 'x' }, schemaNeither, scoped)
    expect(out).toEqual({ query: 'x' })
  })

  it('tolerates an undefined schema (no injection, fresh copy)', () => {
    const out = injectScope({ query: 'x' }, undefined, scoped)
    expect(out).toEqual({ query: 'x' })
  })

  it('carries a non-default scope_id and alternate service through', () => {
    const out = injectScope({ query: 'x' }, schemaBoth, altScope)
    expect(out).toEqual({ query: 'x', service_name: 'khaata/backend', scope_id: 'feature-x' })
  })

  it('honors a model service_name even when scope is unscoped', () => {
    const out = injectScope({ service_name: 'pinned' }, schemaBoth, unscoped)
    expect(out.service_name).toBe('pinned')
  })
})
