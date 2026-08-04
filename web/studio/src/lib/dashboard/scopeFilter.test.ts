import { describe, it, expect } from 'vitest'
import { filterServiceCards, filterHealthFlags } from './scopeFilter'
import type { HealthFlag, ServiceCard } from '$lib/types/dashboard'

function card(name: string): ServiceCard {
  return {
    name,
    scopeId: 'main',
    language: 'go',
    version: null,
    repositoryUrl: null,
    nodesByLabel: {},
    calls: 0,
    apiRoutes: 0,
    flows: 0,
    docs: 0,
    chunks: 0,
    docLinks: { docmine: 0, semlink: 0 },
    semantic: null,
    reachability: null
  }
}

function flag(text: string, severity: HealthFlag['severity'] = 'warn'): HealthFlag {
  return { severity, code: 'x', text }
}

const known = ['codegraph', 'khaata/backend', 'codegraph/web/studio', 'dough-core']

describe('filterServiceCards', () => {
  const cards = [card('codegraph'), card('khaata/backend'), card('dough-core')]

  it('returns all cards when scope is null', () => {
    expect(filterServiceCards(cards, null).map((c) => c.name)).toEqual([
      'codegraph',
      'khaata/backend',
      'dough-core'
    ])
  })

  it('returns only the selected service card', () => {
    expect(filterServiceCards(cards, 'khaata/backend').map((c) => c.name)).toEqual(['khaata/backend'])
  })

  it('returns an empty list when the selected service has no card', () => {
    expect(filterServiceCards(cards, 'ghost')).toEqual([])
  })
})

describe('filterHealthFlags', () => {
  it('returns all flags when scope is null', () => {
    const flags = [flag('codegraph has 16 dead functions'), flag('embeddings online')]
    expect(filterHealthFlags(flags, null, known)).toHaveLength(2)
  })

  it('keeps flags mentioning the selected service', () => {
    const flags = [
      flag('codegraph has 16 dead functions'),
      flag('khaata/backend has 264 dead functions')
    ]
    const out = filterHealthFlags(flags, 'codegraph', known)
    expect(out).toHaveLength(1)
    expect(out[0].text).toContain('codegraph has 16')
  })

  it('keeps aggregate flags that mention no known service', () => {
    const flags = [flag('embeddings online'), flag('graph has 9 services indexed')]
    expect(filterHealthFlags(flags, 'codegraph', known)).toHaveLength(2)
  })

  it('drops flags exclusively about other services', () => {
    const flags = [flag('dough-core has zero flows')]
    expect(filterHealthFlags(flags, 'codegraph', known)).toHaveLength(0)
  })

  it('does not treat a nested path as a mention of its prefix', () => {
    // Selecting "codegraph": a flag about "codegraph/web/studio" must NOT
    // survive as if it mentioned "codegraph".
    const flags = [flag('codegraph/web/studio has zero flows')]
    expect(filterHealthFlags(flags, 'codegraph', known)).toHaveLength(0)
    // But it survives when that nested service is the selected scope.
    expect(filterHealthFlags(flags, 'codegraph/web/studio', known)).toHaveLength(1)
  })

  it('matches a service name at the start or end of the text', () => {
    expect(filterHealthFlags([flag('codegraph')], 'codegraph', known)).toHaveLength(1)
    expect(
      filterHealthFlags([flag('duplicate repo detected: codegraph')], 'codegraph', known)
    ).toHaveLength(1)
  })

  it('does not match a service name embedded in a larger identifier', () => {
    // "codegraphs" (trailing letter) is not a mention of "codegraph"
    const flags = [flag('codegraphs is not a service')]
    // no known service is mentioned as a whole token → treated as aggregate,
    // so it stays visible under any scope
    expect(filterHealthFlags(flags, 'codegraph', known)).toHaveLength(1)
    expect(filterHealthFlags(flags, 'dough-core', known)).toHaveLength(1)
  })
})
