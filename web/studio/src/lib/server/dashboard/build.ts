import type {
  ReachabilitySummary,
  CallHub,
  DashboardData,
  DocLinkCounts,
  HealthFlag,
  RecentDocLink,
  SemanticState,
  ServiceCard
} from '$lib/types/dashboard'
import type {
  ApiRoutesPerServiceRow,
  CallHubRow,
  CallsPerServiceRow,
  EdgesByTypeRow,
  EntryCandidatesPerServiceRow,
  FlowsPerServiceRow,
  IndexRunRow,
  MentionsPerServiceFamilyRow,
  ReachabilityRow,
  NodesByLabelRow,
  RecentDocLinkRow,
  SemanticStateRow,
  ServiceRow
} from './queries'

/** Raw row sets collected from the graph, ready for pure aggregation. */
export interface RawDashboardRows {
  services: ServiceRow[]
  nodesByLabel: NodesByLabelRow[]
  edgesByType: EdgesByTypeRow[]
  callsPerService: CallsPerServiceRow[]
  mentionsPerServiceFamily: MentionsPerServiceFamilyRow[]
  semanticState: SemanticStateRow[]
  flowsPerService: FlowsPerServiceRow[]
  entryCandidatesPerService: EntryCandidatesPerServiceRow[]
  apiRoutesPerService: ApiRoutesPerServiceRow[]
  callHubs: CallHubRow[]
  recentDocLinks: RecentDocLinkRow[]
  indexRuns: IndexRunRow[]
  reachability: ReachabilityRow[]
}

/**
 * Mirrors driftThreshold in internal/verify/telemetry/drift.go (the source
 * of truth for drift semantics) — a counter moving more than ±25% between
 * the two most recent IndexRuns of a service is flagged. Zero-baselines
 * (0→N, N→0) always flag.
 */
const DRIFT_THRESHOLD = 0.25

/**
 * Dead-code warn threshold: fraction of classified application functions
 * (live + dead + possibly_live; test_only and unknown excluded) that are
 * dead before the dashboard flags the service. khaata's first
 * classification (264/1791 ≈ 15%) should flag; codegraph (16/1620 ≈ 1%)
 * should not.
 */
const DEAD_CODE_WARN_FRACTION = 0.1

const DRIFT_COUNTERS = [
  'files',
  'functions',
  'methods',
  'callsEdges',
  'implementsEdges',
  'apiRoutes'
] as const

function counterDrifts(prev: IndexRunRow, curr: IndexRunRow): string[] {
  const drifts: string[] = []
  for (const key of DRIFT_COUNTERS) {
    const p = prev[key] ?? 0
    const c = curr[key] ?? 0
    if (p === c) continue
    const crossed = p === 0 || c === 0 ? true : Math.abs(c - p) / p > DRIFT_THRESHOLD
    if (crossed) drifts.push(`${key} ${p}→${c}`)
  }
  return drifts
}

/** Fields collect.ts knows and build.ts doesn't derive from rows. */
export interface DashboardMeta {
  generatedAt: string
  neo4jTarget: string
  warnings: string[]
}

function emptyDocLinks(): DocLinkCounts {
  return { docmine: 0, semlink: 0 }
}

function addDocLink(counts: DocLinkCounts, family: string | null, c: number): void {
  if (family === 'docmine') counts.docmine += c
  else if (family === 'semlink') counts.semlink += c
}

/**
 * Pure aggregation: raw graph rows -> DashboardData (including health
 * flags). No I/O, no Date.now() — `meta` supplies anything time- or
 * environment-dependent so this function is fully deterministic and
 * unit-testable.
 */
export function buildDashboard(raw: RawDashboardRows, meta: DashboardMeta): DashboardData {
  const serviceOrder = [...raw.services].sort((a, b) => a.name.localeCompare(b.name))
  const serviceNames = new Set(serviceOrder.map((s) => s.name))

  // ---- per-service accumulators, keyed by service name ----
  const nodesByLabelPerService = new Map<string, Record<string, number>>()
  const totalNodesByLabel: Record<string, number> = {}
  for (const row of raw.nodesByLabel) {
    if (row.c === 0) continue
    totalNodesByLabel[row.label] = (totalNodesByLabel[row.label] ?? 0) + row.c
    if (row.svc === null || !serviceNames.has(row.svc)) continue
    const bucket = nodesByLabelPerService.get(row.svc) ?? {}
    bucket[row.label] = (bucket[row.label] ?? 0) + row.c
    nodesByLabelPerService.set(row.svc, bucket)
  }

  const totalEdgesByType: Record<string, number> = {}
  for (const row of raw.edgesByType) {
    if (row.c === 0) continue
    totalEdgesByType[row.t] = row.c
  }

  const callsPerService = new Map<string, number>()
  for (const row of raw.callsPerService) {
    if (row.svc === null) continue
    callsPerService.set(row.svc, (callsPerService.get(row.svc) ?? 0) + row.c)
  }

  const docLinksPerService = new Map<string, DocLinkCounts>()
  const totalDocLinks = emptyDocLinks()
  for (const row of raw.mentionsPerServiceFamily) {
    addDocLink(totalDocLinks, row.family, row.c)
    if (row.svc === null) continue
    const counts = docLinksPerService.get(row.svc) ?? emptyDocLinks()
    addDocLink(counts, row.family, row.c)
    docLinksPerService.set(row.svc, counts)
  }

  const semanticPerService = new Map<string, SemanticState>()
  let totalEmbeddedChunks = 0
  let anyEmbeddingModel: string | null = null
  let anyDims: number | null = null
  for (const row of raw.semanticState) {
    totalEmbeddedChunks += row.embedded
    if (row.embedded > 0 && anyEmbeddingModel === null) {
      anyEmbeddingModel = row.embeddingModel
      anyDims = row.dims
    }
    if (row.svc === null) continue
    if (row.embedded > 0) {
      semanticPerService.set(row.svc, {
        embeddingModel: row.embeddingModel,
        dims: row.dims,
        semlinkModel: row.semlinkModel,
        semlinkThreshold: row.threshold,
        embeddedChunks: row.embedded
      })
    }
  }

  const flowsPerService = new Map<string, number>()
  for (const row of raw.flowsPerService) {
    if (row.svc === null) continue
    flowsPerService.set(row.svc, (flowsPerService.get(row.svc) ?? 0) + row.flows)
  }

  const apiRoutesPerService = new Map<string, number>()
  for (const row of raw.apiRoutesPerService) {
    if (row.svc === null) continue
    apiRoutesPerService.set(row.svc, (apiRoutesPerService.get(row.svc) ?? 0) + row.c)
  }

  const entryCandidatesPerService = new Map<string, number>()
  for (const row of raw.entryCandidatesPerService) {
    if (row.svc === null) continue
    entryCandidatesPerService.set(row.svc, (entryCandidatesPerService.get(row.svc) ?? 0) + row.c)
  }

  const reachabilityPerService = new Map<string, ReachabilitySummary>()
  for (const row of raw.reachability) {
    if (row.svc === null || row.verdict === null) continue
    const summary = reachabilityPerService.get(row.svc) ?? {
      live: 0,
      testOnly: 0,
      dead: 0,
      deadCluster: 0,
      possiblyLive: 0,
      unknown: 0
    }
    switch (row.verdict) {
      case 'live':
        summary.live += row.c
        break
      case 'test_only':
        summary.testOnly += row.c
        break
      case 'dead':
        summary.dead += row.c
        summary.deadCluster += row.clusters
        break
      case 'possibly_live':
        summary.possiblyLive += row.c
        break
      case 'unknown':
        summary.unknown += row.c
        break
    }
    reachabilityPerService.set(row.svc, summary)
  }

  // ---- service cards ----
  const services: ServiceCard[] = serviceOrder.map((s) => {
    const nodesByLabel = nodesByLabelPerService.get(s.name) ?? {}
    const docs = nodesByLabel['Document'] ?? 0
    const chunks = nodesByLabel['DocumentChunk'] ?? 0
    return {
      name: s.name,
      scopeId: s.scopeId ?? 'main',
      language: s.language,
      version: s.version,
      repositoryUrl: s.repositoryUrl,
      nodesByLabel,
      calls: callsPerService.get(s.name) ?? 0,
      apiRoutes: apiRoutesPerService.get(s.name) ?? 0,
      flows: flowsPerService.get(s.name) ?? 0,
      docs,
      chunks,
      docLinks: docLinksPerService.get(s.name) ?? emptyDocLinks(),
      semantic: semanticPerService.get(s.name) ?? null,
      reachability: reachabilityPerService.get(s.name) ?? null
    }
  })

  // ---- call hubs ----
  const callHubs: CallHub[] = raw.callHubs.map((row) => ({
    name: row.name,
    service: row.serviceName,
    label: row.label,
    inDegree: row.inDegree
  }))

  // ---- recent doc links ----
  const recentDocLinks: RecentDocLink[] = raw.recentDocLinks.map((row) => {
    const strategy = row.strategy ?? ''
    const family = strategy.split('/')[0] === 'semlink' ? 'semlink' : 'docmine'
    return {
      docPath: row.docPath ?? '(unknown path)',
      headingPath: row.headingPath ?? '',
      family,
      strategy,
      confidence: row.confidence ?? 0,
      createdAt: row.createdAt,
      targetName: row.targetName
    }
  })

  // ---- health flags ----
  const health: HealthFlag[] = []

  // Services with code (Function/Method) but no flows. Two distinct situations:
  //   err  zero-flows — entry-point candidates exist, so flow generation failed
  //        or was starved (fixable: regenerate).
  //   warn no-entry-points — nothing detectable to trace (e.g. a service whose
  //        only functions are test functions); accurate, not actionable.
  for (const s of services) {
    const codeCount = (s.nodesByLabel['Function'] ?? 0) + (s.nodesByLabel['Method'] ?? 0)
    if (codeCount === 0 || s.flows > 0) continue
    const candidates = entryCandidatesPerService.get(s.name) ?? 0
    if (candidates > 0 || s.apiRoutes > 0) {
      const detected = candidates > 0 ? `${candidates} entry-point candidate(s)` : `${s.apiRoutes} API route(s)`
      health.push({
        severity: 'err',
        code: 'zero-flows',
        text: `${s.name}: ${detected} but 0 flows persisted — run codegraph query flows --generate --service="${s.name}"`
      })
    } else {
      health.push({
        severity: 'warn',
        code: 'no-entry-points',
        text: `${s.name}: no non-test entry points detected — nothing to trace into flows`
      })
    }
  }

  // Index-run telemetry (RFC-013 L3): drift between the last two runs of a
  // service is an err (something about what the indexer produced changed by
  // >25% — either a real regression or a legitimately big code change, and a
  // human should decide which); services with code but no recorded IndexRun
  // get one aggregate warn (graph predates run telemetry — re-index to arm
  // drift detection).
  const runsByService = new Map<string, IndexRunRow[]>()
  for (const row of raw.indexRuns) {
    if (row.svc === null) continue
    const runs = runsByService.get(row.svc) ?? []
    runs.push(row)
    runsByService.set(row.svc, runs)
  }
  for (const [svc, runs] of runsByService) {
    if (runs.length < 2) continue
    const drifts = counterDrifts(runs[1], runs[0])
    if (drifts.length > 0) {
      health.push({
        severity: 'err',
        code: 'index-drift',
        text: `${svc}: index drift vs previous run — ${drifts.join(', ')}`
      })
    }
  }
  const untelemetered = services
    .filter((s) => (s.nodesByLabel['Function'] ?? 0) + (s.nodesByLabel['Method'] ?? 0) > 0)
    .filter((s) => !runsByService.has(s.name))
    .map((s) => s.name)
  if (untelemetered.length > 0) {
    health.push({
      severity: 'warn',
      code: 'no-index-telemetry',
      text: `no IndexRun recorded for ${untelemetered.join(', ')} — re-index to arm drift detection`
    })
  }

  // warn: dead-code — RFC-014 verdicts show a significant dead fraction.
  // Threshold: >10% of classified application functions (dead / (live +
  // dead + possiblyLive)) with at least 5 dead — small services would
  // otherwise flag on a single leftover helper.
  for (const s of services) {
    const r = s.reachability
    if (!r) continue
    const appFns = r.live + r.dead + r.possiblyLive
    if (appFns === 0 || r.dead < 5) continue
    const deadPct = r.dead / appFns
    if (deadPct > DEAD_CODE_WARN_FRACTION) {
      health.push({
        severity: 'warn',
        code: 'dead-code',
        text: `${s.name}: ${r.dead} dead functions (${Math.round(deadPct * 100)}% of ${appFns} classified, ${r.deadCluster} in dead clusters) — codegraph query deadcode --service="${s.name}"`
      })
    }
  }
  const unclassified = services
    .filter((s) => (s.nodesByLabel['Function'] ?? 0) + (s.nodesByLabel['Method'] ?? 0) > 0)
    .filter((s) => !reachabilityPerService.has(s.name))
    .map((s) => s.name)
  if (unclassified.length > 0) {
    health.push({
      severity: 'warn',
      code: 'no-reachability',
      text: `no reachability verdicts for ${unclassified.join(', ')} — re-index (or run codegraph query deadcode) to classify dead code`
    })
  }

  // warn: duplicate-repo — services sharing the same non-null repositoryUrl
  const byRepo = new Map<string, string[]>()
  for (const s of services) {
    if (!s.repositoryUrl) continue
    const names = byRepo.get(s.repositoryUrl) ?? []
    names.push(s.name)
    byRepo.set(s.repositoryUrl, names)
  }
  for (const [repositoryUrl, names] of byRepo) {
    if (names.length > 1) {
      health.push({
        severity: 'warn',
        code: 'duplicate-repo',
        text: `${names.join(', ')} share the same repository URL (${repositoryUrl})`
      })
    }
  }

  // ok/warn: embeddings
  if (totalEmbeddedChunks > 0) {
    const dimsPart = anyDims !== null ? ` (dims ${anyDims})` : ''
    const modelPart = anyEmbeddingModel ? ` · ${anyEmbeddingModel}${dimsPart}` : ''
    health.push({
      severity: 'ok',
      code: 'embeddings-online',
      text: `embeddings online · ${totalEmbeddedChunks} chunks${modelPart}`
    })
  } else {
    health.push({
      severity: 'warn',
      code: 'embeddings-missing',
      text: 'no embedded chunks — semantic search unavailable'
    })
  }

  // err before warn before ok, stable within each severity — the drift/
  // telemetry flags are emitted between the flow and repo blocks, so the
  // invariant no longer holds by construction order alone.
  const severityRank = { err: 0, warn: 1, ok: 2 } as const
  health.sort((a, b) => severityRank[a.severity] - severityRank[b.severity])

  return {
    generatedAt: meta.generatedAt,
    neo4jTarget: meta.neo4jTarget,
    warnings: meta.warnings,
    totals: {
      services: services.length,
      nodesByLabel: totalNodesByLabel,
      edgesByType: totalEdgesByType,
      docLinks: totalDocLinks
    },
    services,
    health,
    callHubs,
    recentDocLinks
  }
}
