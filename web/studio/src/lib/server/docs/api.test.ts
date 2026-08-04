import { describe, it, expect, vi } from 'vitest'
import {
  assembleChunks,
  bandOf,
  buildDocListQuery,
  buildDocSearchFallbackQuery,
  buildDocSearchQuery,
  escapeLuceneQuery,
  familyOf,
  getDocumentDetail,
  getReverseMentions,
  isMissingIndexError,
  listDocuments,
  searchDocs,
  ValidationError
} from './api'
import { groupDocumentsByService } from '$lib/components/docs/grouping'
import type { McpClient } from '$lib/server/mcp/client'
import { McpRequestError } from '$lib/server/mcp/client'
import type { DocSummary } from '$lib/types/docs'

/** Stub matching McpClient.callTool with a per-call queue of resolved values. */
function stubClient(...resolves: unknown[]) {
  const fn = vi.fn()
  for (const r of resolves) fn.mockResolvedValueOnce(r)
  return { callTool: fn } as unknown as McpClient
}

function envelope<T>(data: T, warnings: string[] = []) {
  return { warnings, data }
}

// ── family / band derivation ────────────────────────────────────────────
describe('familyOf', () => {
  it('classifies semlink strategies as semlink', () => {
    expect(familyOf('semlink/text-embedding-3-small')).toBe('semlink')
  })
  it('classifies docmine strategies as docmine', () => {
    expect(familyOf('docmine/codespan')).toBe('docmine')
    expect(familyOf('docmine/fence')).toBe('docmine')
  })
  it('treats unknown/empty/null as docmine (higher-trust default)', () => {
    expect(familyOf('mystery/thing')).toBe('docmine')
    expect(familyOf('')).toBe('docmine')
    expect(familyOf(null)).toBe('docmine')
    expect(familyOf(undefined)).toBe('docmine')
  })
})

describe('bandOf', () => {
  it('docmine ≥0.7 is high, below is medium', () => {
    expect(bandOf('docmine', 0.7)).toBe('high')
    expect(bandOf('docmine', 0.95)).toBe('high')
    expect(bandOf('docmine', 0.5)).toBe('medium')
  })
  it('semlink caps at medium, low below 0.6', () => {
    expect(bandOf('semlink', 0.9)).toBe('medium')
    expect(bandOf('semlink', 0.6)).toBe('medium')
    expect(bandOf('semlink', 0.474)).toBe('low')
    expect(bandOf('semlink', 0)).toBe('low')
  })
})

// ── query builders ──────────────────────────────────────────────────────
describe('buildDocListQuery', () => {
  it('omits the service filter when service is null', () => {
    const q = buildDocListQuery(null)
    expect(q).not.toContain('$service')
    expect(q).toContain('MATCH (d:Document)')
    expect(q).toContain('ORDER BY toLower')
  })
  it('adds a parameterized service filter when service is given', () => {
    const q = buildDocListQuery('codegraph')
    expect(q).toContain('WHERE d.serviceName = $service')
    // never interpolate the raw value
    expect(q).not.toContain('codegraph')
  })
})

describe('buildDocSearchQuery', () => {
  it('unions both fulltext indexes and de-dupes to one hit per doc', () => {
    const q = buildDocSearchQuery(null)
    expect(q).toContain("db.index.fulltext.queryNodes('document_fulltext', $q)")
    expect(q).toContain("db.index.fulltext.queryNodes('documentchunk_fulltext', $q)")
    expect(q).toContain('max(score) AS score')
    expect(q).toContain('LIMIT toInteger($limit)')
  })
  it('applies the service filter to both arms when scoped', () => {
    const q = buildDocSearchQuery('dough-core')
    const occurrences = q.match(/WHERE d\.serviceName = \$service/g) ?? []
    expect(occurrences.length).toBe(2)
    expect(q).not.toContain('dough-core')
  })
})

describe('buildDocSearchFallbackQuery', () => {
  it('does case-insensitive CONTAINS over title and content', () => {
    const q = buildDocSearchFallbackQuery(null)
    expect(q).toContain('t CONTAINS $needle')
    expect(q).toContain('body CONTAINS $needle')
    expect(q).not.toContain('$service')
  })
  it('appends the service filter when scoped', () => {
    const q = buildDocSearchFallbackQuery('codegraph')
    expect(q).toContain('AND d.serviceName = $service')
  })
})

// ── lucene escaping ─────────────────────────────────────────────────────
describe('escapeLuceneQuery', () => {
  it('escapes lucene special characters', () => {
    expect(escapeLuceneQuery('a+b')).toBe('a\\+b')
    expect(escapeLuceneQuery('foo(bar)')).toBe('foo\\(bar\\)')
    expect(escapeLuceneQuery('a:b')).toBe('a\\:b')
    expect(escapeLuceneQuery('c/d')).toBe('c\\/d')
  })
  it('leaves plain alphanumerics and spaces untouched', () => {
    expect(escapeLuceneQuery('rfc 011 linking')).toBe('rfc 011 linking')
  })
})

// ── missing-index detection ─────────────────────────────────────────────
describe('isMissingIndexError', () => {
  it('matches the neo4j missing-fulltext messages', () => {
    expect(isMissingIndexError(new Error('There is no such fulltext schema index: document_fulltext'))).toBe(true)
    expect(isMissingIndexError(new Error('no such fulltext schema index'))).toBe(true)
    expect(isMissingIndexError(new Error('Unable to find valid index for the query'))).toBe(true)
  })
  it('does not match unrelated errors', () => {
    expect(isMissingIndexError(new Error('query timed out after 30000ms'))).toBe(false)
    expect(isMissingIndexError(new Error('connection refused'))).toBe(false)
  })
})

// ── grouping ────────────────────────────────────────────────────────────
describe('groupDocumentsByService', () => {
  const doc = (nodeId: string, service: string | null, title: string): DocSummary => ({
    nodeId,
    nodeKey: `key:${nodeId}`,
    title,
    filePath: `${title}.md`,
    service,
    type: 'markdown',
    chunkCount: 1
  })

  it('groups by service and sorts services alphabetically', () => {
    const groups = groupDocumentsByService([
      doc('1', 'dough-core', 'z'),
      doc('2', 'codegraph', 'a'),
      doc('3', 'codegraph', 'b')
    ])
    expect(groups.map((g) => g.service)).toEqual(['codegraph', 'dough-core'])
    expect(groups[0].documents.map((d) => d.nodeId)).toEqual(['2', '3'])
  })
  it('buckets null-service docs under (unassigned)', () => {
    const groups = groupDocumentsByService([doc('1', null, 'orphan')])
    expect(groups[0].service).toBe('(unassigned)')
  })
  it('preserves incoming doc order within a group', () => {
    const groups = groupDocumentsByService([
      doc('1', 'codegraph', 'first'),
      doc('2', 'codegraph', 'second')
    ])
    expect(groups[0].documents.map((d) => d.title)).toEqual(['first', 'second'])
  })
})

// ── chunk assembly ──────────────────────────────────────────────────────
describe('assembleChunks', () => {
  it('attaches mentions to their chunk and derives family/band', () => {
    const chunks = assembleChunks(
      [
        { nodeId: 'c1', nodeKey: 'k1', headingPath: 'Doc > A', chunkIndex: 0, content: 'text a' },
        { nodeId: 'c2', nodeKey: 'k2', headingPath: 'Doc > B', chunkIndex: 1, content: 'text b' }
      ],
      [
        {
          chunkId: 'c1',
          targetId: 't1',
          targetName: 'Foo',
          targetLabel: 'Class',
          targetFile: 'foo.go',
          strategy: 'docmine/fence',
          confidence: 0.7
        },
        {
          chunkId: 'c1',
          targetId: 't2',
          targetName: 'Bar',
          targetLabel: 'Function',
          targetFile: 'bar.go',
          strategy: 'semlink/text-embedding-3-small',
          confidence: 0.48
        }
      ]
    )
    expect(chunks[0].mentions).toHaveLength(2)
    expect(chunks[0].mentions[0]).toMatchObject({
      nodeId: 't1',
      name: 'Foo',
      family: 'docmine',
      band: 'high'
    })
    expect(chunks[0].mentions[1]).toMatchObject({
      nodeId: 't2',
      family: 'semlink',
      band: 'low'
    })
    expect(chunks[1].mentions).toEqual([])
  })
  it('defaults null content/index/confidence safely', () => {
    const chunks = assembleChunks(
      [{ nodeId: 'c1', nodeKey: 'k1', headingPath: null, chunkIndex: null, content: null }],
      [
        {
          chunkId: 'c1',
          targetId: 't1',
          targetName: null,
          targetLabel: null,
          targetFile: null,
          strategy: null,
          confidence: null
        }
      ]
    )
    expect(chunks[0].content).toBe('')
    expect(chunks[0].chunkIndex).toBe(0)
    expect(chunks[0].mentions[0].confidence).toBe(0)
    expect(chunks[0].mentions[0].family).toBe('docmine')
  })
})

// ── listDocuments ───────────────────────────────────────────────────────
describe('listDocuments', () => {
  it('sends the unscoped query with no params when service is null', async () => {
    const client = stubClient(envelope({ columns: [], row_count: 0, rows: [] }))
    await listDocuments(client, {})
    const [tool, args] = (client.callTool as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(tool).toBe('codegraph_cypher')
    expect(args.query).not.toContain('$service')
    expect(args.params).toBeUndefined()
    expect(args.format).toBe('json')
  })
  it('passes the service param when scoped', async () => {
    const client = stubClient(envelope({ columns: [], row_count: 0, rows: [] }))
    await listDocuments(client, { service: 'codegraph' })
    const [, args] = (client.callTool as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(args.params).toEqual({ service: 'codegraph' })
    expect(args.query).toContain('$service')
  })
  it('maps rows and falls back title→filePath→(untitled)', async () => {
    const client = stubClient(
      envelope({
        columns: [],
        row_count: 3,
        rows: [
          { nodeId: 'a', nodeKey: 'ka', title: 'Titled', filePath: 'a.md', service: 'codegraph', type: 'markdown', chunkCount: 5 },
          { nodeId: 'b', nodeKey: 'kb', title: null, filePath: 'b.md', service: 'codegraph', type: 'markdown', chunkCount: null },
          { nodeId: 'c', nodeKey: 'kc', title: null, filePath: null, service: null, type: null, chunkCount: 0 }
        ]
      })
    )
    const res = await listDocuments(client, {})
    expect(res.data.documents[0].title).toBe('Titled')
    expect(res.data.documents[1].title).toBe('b.md')
    expect(res.data.documents[1].chunkCount).toBe(0)
    expect(res.data.documents[2].title).toBe('(untitled)')
  })
  it('tolerates rows:null', async () => {
    const client = stubClient(envelope({ columns: [], row_count: 0, rows: null }))
    const res = await listDocuments(client, {})
    expect(res.data.documents).toEqual([])
  })
  it('propagates warnings', async () => {
    const client = stubClient(envelope({ columns: [], row_count: 0, rows: null }, ['AllNodesScan on Document']))
    const res = await listDocuments(client, {})
    expect(res.warnings).toEqual(['AllNodesScan on Document'])
  })
})

// ── getDocumentDetail ───────────────────────────────────────────────────
describe('getDocumentDetail', () => {
  it('throws ValidationError on empty docId without calling the tool', async () => {
    const client = stubClient()
    await expect(getDocumentDetail(client, { docId: '' })).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })
  it('throws ValidationError when the document is not found', async () => {
    const client = stubClient(
      envelope({ columns: [], row_count: 0, rows: [] }),
      envelope({ columns: [], row_count: 0, rows: [] }),
      envelope({ columns: [], row_count: 0, rows: [] })
    )
    await expect(getDocumentDetail(client, { docId: 'missing' })).rejects.toThrow(ValidationError)
  })
  it('assembles document + chunks + mentions and unions warnings', async () => {
    const client = stubClient(
      envelope(
        {
          columns: [],
          row_count: 1,
          rows: [{ nodeId: 'd1', nodeKey: 'kd1', title: 'Doc', filePath: 'd.md', service: 'codegraph', type: 'markdown', chunkCount: 2 }]
        },
        ['w-summary']
      ),
      envelope(
        {
          columns: [],
          row_count: 2,
          rows: [
            { nodeId: 'c1', nodeKey: 'kc1', headingPath: 'Doc > A', chunkIndex: 0, content: 'a' },
            { nodeId: 'c2', nodeKey: 'kc2', headingPath: 'Doc > B', chunkIndex: 1, content: 'b' }
          ]
        },
        ['w-chunks']
      ),
      envelope(
        {
          columns: [],
          row_count: 1,
          rows: [
            { chunkId: 'c1', targetId: 't1', targetName: 'Foo', targetLabel: 'Class', targetFile: 'foo.go', strategy: 'docmine/fence', confidence: 0.7 }
          ]
        },
        ['w-summary'] // duplicate, should be de-duped
      )
    )
    const res = await getDocumentDetail(client, { docId: 'd1' })
    expect(res.data.document.title).toBe('Doc')
    expect(res.data.chunks).toHaveLength(2)
    expect(res.data.chunks[0].mentions[0].name).toBe('Foo')
    expect(res.data.chunks[1].mentions).toEqual([])
    expect(res.warnings.sort()).toEqual(['w-chunks', 'w-summary'])
  })
})

// ── getReverseMentions ──────────────────────────────────────────────────
describe('getReverseMentions', () => {
  it('throws ValidationError on empty nodeId', async () => {
    const client = stubClient()
    await expect(getReverseMentions(client, { nodeId: '' })).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })
  it('maps reverse mention rows with derived family/band', async () => {
    const client = stubClient(
      envelope({
        columns: [],
        row_count: 1,
        rows: [
          {
            documentNodeId: 'd1',
            documentTitle: 'Doc',
            documentService: 'codegraph',
            chunkNodeId: 'c1',
            headingPath: 'Doc > A',
            chunkIndex: 0,
            strategy: 'semlink/text-embedding-3-small',
            confidence: 0.62
          }
        ]
      })
    )
    const res = await getReverseMentions(client, { nodeId: 't1' })
    expect(res.data.mentions[0]).toMatchObject({
      documentTitle: 'Doc',
      family: 'semlink',
      band: 'medium'
    })
  })
})

// ── searchDocs ──────────────────────────────────────────────────────────
describe('searchDocs', () => {
  it('rejects a blank query', async () => {
    const client = stubClient()
    await expect(searchDocs(client, { query: '   ' })).rejects.toThrow(ValidationError)
    expect(client.callTool).not.toHaveBeenCalled()
  })
  it('wildcard-suffixes the escaped term and clamps the limit', async () => {
    const client = stubClient(envelope({ columns: [], row_count: 0, rows: [] }))
    await searchDocs(client, { query: 'rfc', limit: 999 })
    const [, args] = (client.callTool as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(args.params.q).toBe('rfc*')
    expect(args.params.limit).toBe(100)
  })
  it('escapes special chars before wildcarding', async () => {
    const client = stubClient(envelope({ columns: [], row_count: 0, rows: [] }))
    await searchDocs(client, { query: 'a+b' })
    const [, args] = (client.callTool as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(args.params.q).toBe('a\\+b*')
  })
  it('passes the service param when scoped', async () => {
    const client = stubClient(envelope({ columns: [], row_count: 0, rows: [] }))
    await searchDocs(client, { query: 'rfc', service: 'codegraph' })
    const [, args] = (client.callTool as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(args.params.service).toBe('codegraph')
  })
  it('maps hits with fallback:false on the fulltext path', async () => {
    const client = stubClient(
      envelope({
        columns: [],
        row_count: 1,
        rows: [{ nodeId: 'd1', nodeKey: 'kd1', title: 'RFC-011', filePath: 'rfc.md', service: 'codegraph', matchedIn: 'title', score: 2.1 }]
      })
    )
    const res = await searchDocs(client, { query: 'rfc' })
    expect(res.data.fallback).toBe(false)
    expect(res.data.hits[0]).toMatchObject({ title: 'RFC-011', matchedIn: 'title', score: 2.1 })
  })
  it('falls back to CONTAINS when the fulltext index is missing', async () => {
    const client = {
      callTool: vi
        .fn()
        .mockRejectedValueOnce(new McpRequestError('There is no such fulltext schema index: document_fulltext', 'tool-error'))
        .mockResolvedValueOnce(
          envelope({
            columns: [],
            row_count: 1,
            rows: [{ nodeId: 'd1', nodeKey: 'kd1', title: 'RFC-011', filePath: 'rfc.md', service: 'codegraph', matchedIn: 'content', score: null }]
          })
        )
    } as unknown as McpClient
    const res = await searchDocs(client, { query: 'rfc' })
    expect(res.data.fallback).toBe(true)
    expect(res.data.hits[0].score).toBeNull()
    // second call used the lowercased raw needle, not the wildcarded lucene term
    const [, fbArgs] = (client.callTool as ReturnType<typeof vi.fn>).mock.calls[1]
    expect(fbArgs.params.needle).toBe('rfc')
  })
  it('re-throws non-index tool errors instead of falling back', async () => {
    const client = {
      callTool: vi.fn().mockRejectedValueOnce(new McpRequestError('query timed out after 30000ms', 'timeout'))
    } as unknown as McpClient
    await expect(searchDocs(client, { query: 'rfc' })).rejects.toThrow('timed out')
    expect(client.callTool).toHaveBeenCalledTimes(1)
  })
})
