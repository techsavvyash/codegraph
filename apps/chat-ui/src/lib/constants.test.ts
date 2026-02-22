import { describe, expect, it } from 'vitest'
import { TOOL_ACTIVITY_LABELS, SYSTEM_PROMPT } from './constants'

describe('TOOL_ACTIVITY_LABELS', () => {
  const knownTools = [
    'codegraph_search',
    'codegraph_hybrid_search',
    'codegraph_vector_search',
    'codegraph_get_source',
    'codegraph_find_references',
    'codegraph_analyze_function',
    'codegraph_search_docs',
    'codegraph_search_by_comment',
  ]

  it.each(knownTools)('has a non-empty label for %s', (tool) => {
    expect(TOOL_ACTIVITY_LABELS[tool]).toBeTypeOf('string')
    expect(TOOL_ACTIVITY_LABELS[tool].length).toBeGreaterThan(0)
  })

  it('all labels end with "..."', () => {
    for (const label of Object.values(TOOL_ACTIVITY_LABELS)) {
      expect(label.endsWith('...')).toBe(true)
    }
  })
})

describe('SYSTEM_PROMPT', () => {
  it('is a non-empty string', () => {
    expect(SYSTEM_PROMPT).toBeTypeOf('string')
    expect(SYSTEM_PROMPT.length).toBeGreaterThan(0)
  })

  it('mentions CodeGraph', () => {
    expect(SYSTEM_PROMPT).toContain('CodeGraph')
  })

  it('instructs to cite sources', () => {
    expect(SYSTEM_PROMPT.toLowerCase()).toContain('cite')
  })
})
