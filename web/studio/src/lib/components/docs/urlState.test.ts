import { describe, it, expect } from 'vitest'
import { parseDocsSelection, serializeDocsSelection, EMPTY_SELECTION } from './urlState'

describe('parseDocsSelection', () => {
  it('returns an empty selection for empty params', () => {
    expect(parseDocsSelection(new URLSearchParams())).toEqual(EMPTY_SELECTION)
  })
  it('reads doc, chunk, and q', () => {
    const p = new URLSearchParams('doc=d1&chunk=c1&q=rfc')
    expect(parseDocsSelection(p)).toEqual({ doc: 'd1', chunk: 'c1', query: 'rfc' })
  })
  it('drops a chunk that has no doc', () => {
    const p = new URLSearchParams('chunk=c1')
    expect(parseDocsSelection(p)).toEqual({ doc: null, chunk: null, query: '' })
  })
  it('treats empty-string params as absent', () => {
    const p = new URLSearchParams('doc=&chunk=&q=')
    expect(parseDocsSelection(p)).toEqual(EMPTY_SELECTION)
  })
})

describe('serializeDocsSelection', () => {
  it('returns bare /docs for an empty selection', () => {
    expect(serializeDocsSelection(EMPTY_SELECTION)).toBe('/docs')
  })
  it('writes doc, chunk, q in stable order', () => {
    expect(serializeDocsSelection({ doc: 'd1', chunk: 'c1', query: 'rfc' })).toBe('/docs?doc=d1&chunk=c1&q=rfc')
  })
  it('omits chunk when there is no doc', () => {
    expect(serializeDocsSelection({ doc: null, chunk: 'c1', query: '' })).toBe('/docs')
  })
  it('encodes special characters', () => {
    expect(serializeDocsSelection({ doc: '4:abc:1', chunk: null, query: 'a b' })).toBe('/docs?doc=4%3Aabc%3A1&q=a+b')
  })
  it('round-trips through parse', () => {
    const sel = { doc: 'd1', chunk: 'c1', query: 'linking' }
    const path = serializeDocsSelection(sel)
    const params = new URLSearchParams(path.split('?')[1])
    expect(parseDocsSelection(params)).toEqual(sel)
  })
})
