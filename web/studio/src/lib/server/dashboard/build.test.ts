import { describe, it, expect } from 'vitest'
import { buildDashboard, type RawDashboardRows } from './build'
import type {
  ApiRoutesPerServiceRow,
  CallHubRow,
  CallsPerServiceRow,
  EdgesByTypeRow,
  FlowsPerServiceRow,
  IndexRunRow,
  MentionsPerServiceFamilyRow,
  NodesByLabelRow,
  RecentDocLinkRow,
  SemanticStateRow,
  ServiceRow
} from './queries'

const META = {
  generatedAt: '2026-07-23T00:00:00.000Z',
  neo4jTarget: 'bolt://localhost:7687',
  warnings: [] as string[]
}

/** Builds a full RawDashboardRows with sensible empty defaults, overridable per-field. */
function fixture(overrides: Partial<RawDashboardRows> = {}): RawDashboardRows {
  return {
    services: [],
    nodesByLabel: [],
    edgesByType: [],
    callsPerService: [],
    mentionsPerServiceFamily: [],
    semanticState: [],
    flowsPerService: [],
    entryCandidatesPerService: [],
    apiRoutesPerService: [],
    callHubs: [],
    recentDocLinks: [],
    indexRuns: [],
    reachability: [],
    ...overrides
  }
}

/** IndexRunRow with sensible defaults, overridable per-field. */
function run(svc: string, finishedAt: string, overrides: Partial<IndexRunRow> = {}): IndexRunRow {
  return {
    svc,
    finishedAt,
    files: 100,
    functions: 500,
    methods: 200,
    callsEdges: 1000,
    implementsEdges: 50,
    apiRoutes: 20,
    ...overrides
  }
}

describe('buildDashboard', () => {
  it('aggregates raw rows into totals and per-service cards', () => {
    const services: ServiceRow[] = [
      {
        name: 'dough-core',
        scopeId: 'main',
        language: 'go',
        version: 'v1.2.0',
        repositoryUrl: 'https://github.com/example/dough',
        packageName: null
      },
      {
        name: 'codegraph',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: 'https://github.com/example/codegraph',
        packageName: null
      }
    ]
    const nodesByLabel: NodesByLabelRow[] = [
      { label: 'Function', svc: 'dough-core', c: 100 },
      { label: 'Method', svc: 'dough-core', c: 50 },
      { label: 'Function', svc: 'codegraph', c: 200 },
      { label: 'File', svc: 'codegraph', c: 30 }
    ]
    const edgesByType: EdgesByTypeRow[] = [
      { t: 'CALLS', c: 500 },
      { t: 'CONTAINS', c: 900 }
    ]
    const callsPerService: CallsPerServiceRow[] = [
      { svc: 'dough-core', c: 300 },
      { svc: 'codegraph', c: 200 }
    ]
    const flowsPerService: FlowsPerServiceRow[] = [
      { svc: 'dough-core', flows: 74 },
      { svc: 'codegraph', flows: 36 }
    ]
    const apiRoutesPerService: ApiRoutesPerServiceRow[] = [
      { svc: 'dough-core', c: 367 },
      { svc: 'codegraph', c: 155 }
    ]

    const result = buildDashboard(
      fixture({ services, nodesByLabel, edgesByType, callsPerService, flowsPerService, apiRoutesPerService }),
      META
    )

    expect(result.totals.services).toBe(2)
    expect(result.totals.nodesByLabel).toEqual({ Function: 300, Method: 50, File: 30 })
    expect(result.totals.edgesByType).toEqual({ CALLS: 500, CONTAINS: 900 })

    // sorted by name: codegraph before dough-core
    expect(result.services.map((s) => s.name)).toEqual(['codegraph', 'dough-core'])

    const doughCore = result.services.find((s) => s.name === 'dough-core')!
    expect(doughCore.nodesByLabel).toEqual({ Function: 100, Method: 50 })
    expect(doughCore.calls).toBe(300)
    expect(doughCore.flows).toBe(74)
    expect(doughCore.apiRoutes).toBe(367)
    expect(doughCore.language).toBe('go')
    expect(doughCore.version).toBe('v1.2.0')

    const codegraphCard = result.services.find((s) => s.name === 'codegraph')!
    expect(codegraphCard.nodesByLabel).toEqual({ Function: 200, File: 30 })
    expect(codegraphCard.calls).toBe(200)
    expect(codegraphCard.version).toBeNull()
  })

  it('handles null rows arrays as empty (no throw, zeroed aggregates)', () => {
    const services: ServiceRow[] = [
      { name: 'lonely', scopeId: 'main', language: null, version: null, repositoryUrl: null, packageName: null }
    ]
    const result = buildDashboard(fixture({ services }), META)

    expect(result.totals.nodesByLabel).toEqual({})
    expect(result.totals.edgesByType).toEqual({})
    expect(result.services).toHaveLength(1)
    expect(result.services[0].nodesByLabel).toEqual({})
    expect(result.services[0].calls).toBe(0)
    expect(result.services[0].flows).toBe(0)
    expect(result.services[0].semantic).toBeNull()
  })

  it('surfaces a docs-only service (no code, no flows, no zero-flows flag)', () => {
    const services: ServiceRow[] = [
      { name: 'docs-only', scopeId: 'main', language: null, version: null, repositoryUrl: null, packageName: null }
    ]
    const nodesByLabel: NodesByLabelRow[] = [
      { label: 'Document', svc: 'docs-only', c: 5 },
      { label: 'DocumentChunk', svc: 'docs-only', c: 42 }
    ]
    const result = buildDashboard(fixture({ services, nodesByLabel }), META)

    const card = result.services[0]
    expect(card.docs).toBe(5)
    expect(card.chunks).toBe(42)
    expect(card.calls).toBe(0)
    expect(card.flows).toBe(0)

    // no code (Function/Method) present, so no zero-flows error for this service
    expect(result.health.find((h) => h.code === 'zero-flows')).toBeUndefined()
  })

  it('flags zero flows as err only when entry-point candidates exist', () => {
    const services: ServiceRow[] = [
      { name: 'starved', scopeId: 'main', language: 'typescript', version: null, repositoryUrl: null, packageName: null }
    ]
    const nodesByLabel: NodesByLabelRow[] = [{ label: 'Function', svc: 'starved', c: 12 }]
    const entryCandidatesPerService = [{ svc: 'starved', c: 3 }]
    const result = buildDashboard(fixture({ services, nodesByLabel, entryCandidatesPerService }), META)

    const flag = result.health.find((h) => h.code === 'zero-flows')
    expect(flag).toBeDefined()
    expect(flag!.severity).toBe('err')
    expect(flag!.text).toContain('starved')
    expect(flag!.text).toContain('3 entry-point candidate(s)')
    expect(flag!.text).toContain('query flows --generate --service="starved"')
    expect(result.health.find((h) => h.code === 'no-entry-points')).toBeUndefined()
  })

  it('flags zero flows as err when API routes exist but no root candidates', () => {
    const services: ServiceRow[] = [
      { name: 'api-svc', scopeId: 'main', language: 'go', version: null, repositoryUrl: null, packageName: null }
    ]
    const nodesByLabel: NodesByLabelRow[] = [{ label: 'Method', svc: 'api-svc', c: 8 }]
    const apiRoutesPerService: ApiRoutesPerServiceRow[] = [{ svc: 'api-svc', c: 4 }]
    const result = buildDashboard(fixture({ services, nodesByLabel, apiRoutesPerService }), META)

    const flag = result.health.find((h) => h.code === 'zero-flows')
    expect(flag).toBeDefined()
    expect(flag!.severity).toBe('err')
    expect(flag!.text).toContain('4 API route(s)')
  })

  it('downgrades zero flows to a warn when nothing is traceable', () => {
    const services: ServiceRow[] = [
      { name: 'test-only', scopeId: 'main', language: 'typescript', version: null, repositoryUrl: null, packageName: null }
    ]
    // Functions exist but none qualify as entry candidates (e.g. all test functions).
    const nodesByLabel: NodesByLabelRow[] = [{ label: 'Function', svc: 'test-only', c: 5 }]
    const result = buildDashboard(fixture({ services, nodesByLabel }), META)

    expect(result.health.find((h) => h.code === 'zero-flows')).toBeUndefined()
    const flag = result.health.find((h) => h.code === 'no-entry-points')
    expect(flag).toBeDefined()
    expect(flag!.severity).toBe('warn')
    expect(flag!.text).toContain('test-only')
    expect(flag!.text).toContain('no non-test entry points')
  })

  it('flags services sharing a repositoryUrl as a duplicate-repo warning', () => {
    const services: ServiceRow[] = [
      {
        name: 'codegraph',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: 'https://github.com/example/repo',
        packageName: null
      },
      {
        name: 'context-maximiser',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: 'https://github.com/example/repo',
        packageName: null
      },
      {
        name: 'unrelated',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: 'https://github.com/example/other',
        packageName: null
      }
    ]
    const result = buildDashboard(fixture({ services }), META)

    const flag = result.health.find((h) => h.code === 'duplicate-repo')
    expect(flag).toBeDefined()
    expect(flag!.severity).toBe('warn')
    expect(flag!.text).toContain('codegraph')
    expect(flag!.text).toContain('context-maximiser')
    expect(flag!.text).not.toContain('unrelated')
  })

  it('flags embeddings online when at least one service has embedded chunks', () => {
    const services: ServiceRow[] = [
      { name: 'svc-a', scopeId: 'main', language: 'go', version: null, repositoryUrl: null, packageName: null }
    ]
    const semanticState: SemanticStateRow[] = [
      {
        svc: 'svc-a',
        chunks: 100,
        embedded: 80,
        dims: 1536,
        embeddingModel: 'text-embedding-3-small',
        semlinkModel: 'text-embedding-3-small',
        threshold: 0.82
      }
    ]
    const result = buildDashboard(fixture({ services, semanticState }), META)

    const flag = result.health.find((h) => h.code === 'embeddings-online')
    expect(flag).toBeDefined()
    expect(flag!.severity).toBe('ok')
    expect(flag!.text).toContain('80 chunks')
    expect(flag!.text).toContain('text-embedding-3-small')
    expect(flag!.text).toContain('1536')
    expect(result.health.find((h) => h.code === 'embeddings-missing')).toBeUndefined()

    const card = result.services[0]
    expect(card.semantic).toEqual({
      embeddingModel: 'text-embedding-3-small',
      dims: 1536,
      semlinkModel: 'text-embedding-3-small',
      semlinkThreshold: 0.82,
      embeddedChunks: 80
    })
  })

  it('flags embeddings missing as a warn when no service has embedded chunks', () => {
    const services: ServiceRow[] = [
      { name: 'svc-a', scopeId: 'main', language: 'go', version: null, repositoryUrl: null, packageName: null }
    ]
    const semanticState: SemanticStateRow[] = [
      { svc: 'svc-a', chunks: 10, embedded: 0, dims: null, embeddingModel: null, semlinkModel: null, threshold: null }
    ]
    const result = buildDashboard(fixture({ services, semanticState }), META)

    const flag = result.health.find((h) => h.code === 'embeddings-missing')
    expect(flag).toBeDefined()
    expect(flag!.severity).toBe('warn')
    expect(flag!.text).toContain('no embedded chunks')
    expect(result.health.find((h) => h.code === 'embeddings-online')).toBeUndefined()
    expect(result.services[0].semantic).toBeNull()
  })

  it('orders health flags err before warn before ok', () => {
    const services: ServiceRow[] = [
      {
        name: 'svc-a',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: 'https://github.com/example/shared',
        packageName: null
      },
      {
        name: 'svc-b',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: 'https://github.com/example/shared',
        packageName: null
      }
    ]
    const nodesByLabel: NodesByLabelRow[] = [{ label: 'Function', svc: 'svc-a', c: 3 }]
    const entryCandidatesPerService = [{ svc: 'svc-a', c: 1 }]
    const semanticState: SemanticStateRow[] = [
      {
        svc: 'svc-a',
        chunks: 10,
        embedded: 5,
        dims: 768,
        embeddingModel: 'model-x',
        semlinkModel: null,
        threshold: null
      }
    ]
    const result = buildDashboard(
      fixture({ services, nodesByLabel, entryCandidatesPerService, semanticState }),
      META
    )

    const severities = result.health.map((h) => h.severity)
    const errIdx = severities.indexOf('err')
    const warnIdx = severities.indexOf('warn')
    const okIdx = severities.indexOf('ok')
    expect(errIdx).toBeGreaterThanOrEqual(0)
    expect(warnIdx).toBeGreaterThanOrEqual(0)
    expect(okIdx).toBeGreaterThanOrEqual(0)
    expect(errIdx).toBeLessThan(warnIdx)
    expect(warnIdx).toBeLessThan(okIdx)
  })

  it('deduplicates warnings passed through meta', () => {
    const result = buildDashboard(fixture(), {
      ...META,
      warnings: ['AllNodesScan on X', 'AllNodesScan on X', 'slow query Y']
    })
    // build.ts passes meta.warnings through as-is; dedup happens in collect.ts
    // via a Set, so verify build.ts preserves whatever it's given untouched.
    expect(result.warnings).toEqual(['AllNodesScan on X', 'AllNodesScan on X', 'slow query Y'])
  })

  it('omits zero-count labels and edge types from totals and service cards', () => {
    const services: ServiceRow[] = [
      { name: 'svc-a', scopeId: 'main', language: 'go', version: null, repositoryUrl: null, packageName: null }
    ]
    const nodesByLabel: NodesByLabelRow[] = [
      { label: 'Function', svc: 'svc-a', c: 5 },
      { label: 'Class', svc: 'svc-a', c: 0 },
      { label: 'Interface', svc: null, c: 0 }
    ]
    const edgesByType: EdgesByTypeRow[] = [
      { t: 'CALLS', c: 5 },
      { t: 'IMPLEMENTS', c: 0 }
    ]
    const result = buildDashboard(fixture({ services, nodesByLabel, edgesByType }), META)

    expect(result.totals.nodesByLabel).toEqual({ Function: 5 })
    expect(result.totals.edgesByType).toEqual({ CALLS: 5 })
    expect(result.services[0].nodesByLabel).toEqual({ Function: 5 })
  })

  it('aggregates doc link family counts per service and in totals', () => {
    const services: ServiceRow[] = [
      { name: 'svc-a', scopeId: 'main', language: 'go', version: null, repositoryUrl: null, packageName: null }
    ]
    const mentionsPerServiceFamily: MentionsPerServiceFamilyRow[] = [
      { svc: 'svc-a', family: 'docmine', c: 12 },
      { svc: 'svc-a', family: 'semlink', c: 7 }
    ]
    const result = buildDashboard(fixture({ services, mentionsPerServiceFamily }), META)

    expect(result.services[0].docLinks).toEqual({ docmine: 12, semlink: 7 })
    expect(result.totals.docLinks).toEqual({ docmine: 12, semlink: 7 })
  })

  it('maps call hub and recent doc link rows through unchanged in shape', () => {
    const callHubs: CallHubRow[] = [
      { name: 'HandleRequest', serviceName: 'dough-core', label: 'Function', inDegree: 42 }
    ]
    const recentDocLinks: RecentDocLinkRow[] = [
      {
        docPath: '/docs/architecture.md',
        headingPath: 'Architecture > Overview',
        strategy: 'semlink/text-embedding-3-small',
        confidence: 0.91,
        createdAt: '2026-07-20T10:00:00.000Z',
        targetName: 'HandleRequest'
      }
    ]
    const result = buildDashboard(fixture({ callHubs, recentDocLinks }), META)

    expect(result.callHubs).toEqual([
      { name: 'HandleRequest', service: 'dough-core', label: 'Function', inDegree: 42 }
    ])
    expect(result.recentDocLinks).toEqual([
      {
        docPath: '/docs/architecture.md',
        headingPath: 'Architecture > Overview',
        family: 'semlink',
        strategy: 'semlink/text-embedding-3-small',
        confidence: 0.91,
        createdAt: '2026-07-20T10:00:00.000Z',
        targetName: 'HandleRequest'
      }
    ])
  })

  it('flags index drift when a counter moves more than 25% between the last two runs', () => {
    const indexRuns: IndexRunRow[] = [
      run('svc-a', '2026-07-31T12:00:00Z', { callsEdges: 2924, apiRoutes: 169 }),
      run('svc-a', '2026-07-30T12:00:00Z', { callsEdges: 2000, apiRoutes: 169 })
    ]
    const result = buildDashboard(fixture({ indexRuns }), META)

    const flag = result.health.find((h) => h.code === 'index-drift')
    expect(flag).toBeDefined()
    expect(flag?.severity).toBe('err')
    expect(flag?.text).toContain('svc-a')
    expect(flag?.text).toContain('callsEdges 2000→2924')
    expect(flag?.text).not.toContain('apiRoutes')
  })

  it('flags zero-baseline drift (N→0) even though percent math would divide by zero', () => {
    const indexRuns: IndexRunRow[] = [
      run('svc-a', '2026-07-31T12:00:00Z', { apiRoutes: 0 }),
      run('svc-a', '2026-07-30T12:00:00Z', { apiRoutes: 424 })
    ]
    const result = buildDashboard(fixture({ indexRuns }), META)

    const flag = result.health.find((h) => h.code === 'index-drift')
    expect(flag?.text).toContain('apiRoutes 424→0')
  })

  it('does not flag drift for small deltas or single-run services', () => {
    const indexRuns: IndexRunRow[] = [
      run('svc-a', '2026-07-31T12:00:00Z', { callsEdges: 1100 }),
      run('svc-a', '2026-07-30T12:00:00Z', { callsEdges: 1000 }),
      run('svc-b', '2026-07-31T12:00:00Z')
    ]
    const result = buildDashboard(fixture({ indexRuns }), META)

    expect(result.health.find((h) => h.code === 'index-drift')).toBeUndefined()
  })

  it('warns once, aggregated, for services with code but no recorded IndexRun', () => {
    const services: ServiceRow[] = [
      {
        name: 'svc-a',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: null,
        packageName: null
      },
      {
        name: 'svc-b',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: null,
        packageName: null
      }
    ]
    const nodesByLabel: NodesByLabelRow[] = [
      { label: 'Function', svc: 'svc-a', c: 3 },
      { label: 'Function', svc: 'svc-b', c: 3 }
    ]
    const indexRuns: IndexRunRow[] = [run('svc-b', '2026-07-31T12:00:00Z')]
    const result = buildDashboard(fixture({ services, nodesByLabel, indexRuns }), META)

    const flags = result.health.filter((h) => h.code === 'no-index-telemetry')
    expect(flags).toHaveLength(1)
    expect(flags[0].severity).toBe('warn')
    expect(flags[0].text).toContain('svc-a')
    expect(flags[0].text).not.toContain('svc-b')
  })

  it('flags dead-code when the dead fraction crosses the threshold', () => {
    const services: ServiceRow[] = [
      {
        name: 'khaata/backend',
        scopeId: 'main',
        language: 'typescript',
        version: null,
        repositoryUrl: null,
        packageName: null
      },
      {
        name: 'codegraph',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: null,
        packageName: null
      }
    ]
    const reachability = [
      // khaata: 264/1791 classified app fns ≈ 15% dead → flag
      { svc: 'khaata/backend', verdict: 'live', c: 1527, clusters: 0 },
      { svc: 'khaata/backend', verdict: 'dead', c: 264, clusters: 48 },
      // codegraph: 16 dead of ~793 ≈ 2% → no flag
      { svc: 'codegraph', verdict: 'live', c: 773, clusters: 0 },
      { svc: 'codegraph', verdict: 'test_only', c: 827, clusters: 0 },
      { svc: 'codegraph', verdict: 'dead', c: 16, clusters: 0 },
      { svc: 'codegraph', verdict: 'possibly_live', c: 4, clusters: 0 }
    ]
    const result = buildDashboard(fixture({ services, reachability }), META)

    const flags = result.health.filter((h) => h.code === 'dead-code')
    expect(flags).toHaveLength(1)
    expect(flags[0].severity).toBe('warn')
    expect(flags[0].text).toContain('khaata/backend')
    expect(flags[0].text).toContain('264 dead')
    expect(flags[0].text).toContain('48 in dead clusters')

    // Per-service summary lands on the cards.
    const khaata = result.services.find((s) => s.name === 'khaata/backend')
    expect(khaata?.reachability).toEqual({
      live: 1527,
      testOnly: 0,
      dead: 264,
      deadCluster: 48,
      possiblyLive: 0,
      unknown: 0
    })
    const cg = result.services.find((s) => s.name === 'codegraph')
    expect(cg?.reachability?.testOnly).toBe(827)
  })

  it('small dead counts never flag, and unclassified coded services get the aggregated warn', () => {
    const services: ServiceRow[] = [
      {
        name: 'tiny',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: null,
        packageName: null
      },
      {
        name: 'unclassified',
        scopeId: 'main',
        language: 'go',
        version: null,
        repositoryUrl: null,
        packageName: null
      }
    ]
    const nodesByLabel: NodesByLabelRow[] = [
      { label: 'Function', svc: 'tiny', c: 10 },
      { label: 'Function', svc: 'unclassified', c: 10 }
    ]
    const reachability = [
      // 4 dead of 10 = 40%, but below the 5-dead floor → no dead-code flag.
      { svc: 'tiny', verdict: 'live', c: 6, clusters: 0 },
      { svc: 'tiny', verdict: 'dead', c: 4, clusters: 0 }
    ]
    const result = buildDashboard(fixture({ services, nodesByLabel, reachability }), META)

    expect(result.health.filter((h) => h.code === 'dead-code')).toHaveLength(0)

    const noReach = result.health.filter((h) => h.code === 'no-reachability')
    expect(noReach).toHaveLength(1)
    expect(noReach[0].text).toContain('unclassified')
    expect(noReach[0].text).not.toContain('tiny')
    expect(result.services.find((s) => s.name === 'unclassified')?.reachability).toBeNull()
  })
})
