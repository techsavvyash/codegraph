import { get } from 'svelte/store'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { messages, loading, toolActivity, error, clearMessages, sendMessage } from './chat'

// Mock fetch globally for all tests in this file
const mockFetch = vi.fn()
vi.stubGlobal('fetch', mockFetch)

function makeStream(events: object[]): Response {
  const body = events.map(e => JSON.stringify(e)).join('\n') + '\n'
  const stream = new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(body))
      controller.close()
    }
  })
  return new Response(stream, { status: 200 })
}

describe('chat store', () => {
  beforeEach(() => {
    clearMessages()
    vi.clearAllMocks()
  })

  it('starts with empty state', () => {
    expect(get(messages)).toEqual([])
    expect(get(loading)).toBe(false)
    expect(get(toolActivity)).toBeNull()
    expect(get(error)).toBeNull()
  })

  it('clearMessages resets all state', () => {
    messages.update(m => [...m, { id: '1', role: 'user', content: 'hi' }])
    error.set('some error')
    clearMessages()
    expect(get(messages)).toEqual([])
    expect(get(error)).toBeNull()
  })

  it('sendMessage appends user message immediately', async () => {
    mockFetch.mockResolvedValueOnce(makeStream([{ type: 'done' }]))
    const promise = sendMessage('hello world')
    // User bubble is added synchronously before the await
    expect(get(messages)[0]).toMatchObject({ role: 'user', content: 'hello world' })
    await promise
  })

  it('sendMessage ignores empty/whitespace input', async () => {
    await sendMessage('   ')
    expect(get(messages)).toHaveLength(0)
    expect(mockFetch).not.toHaveBeenCalled()
  })

  it('sendMessage sets loading=true during fetch and false after', async () => {
    mockFetch.mockResolvedValueOnce(makeStream([{ type: 'done' }]))
    const promise = sendMessage('hi')
    expect(get(loading)).toBe(true)
    await promise
    expect(get(loading)).toBe(false)
  })

  it('sendMessage streams text deltas into assistant message', async () => {
    mockFetch.mockResolvedValueOnce(makeStream([
      { type: 'text', delta: 'Hello ' },
      { type: 'text', delta: 'world!' },
      { type: 'done' }
    ]))
    await sendMessage('hi')
    const msgs = get(messages)
    const assistant = msgs.find(m => m.role === 'assistant')
    expect(assistant?.content).toBe('Hello world!')
  })

  it('sendMessage accumulates tool_result into sources', async () => {
    mockFetch.mockResolvedValueOnce(makeStream([
      { type: 'tool_use', name: 'codegraph_search', input: { query: 'hybrid' } },
      { type: 'tool_result', name: 'codegraph_search', result: 'HybridSearchManager found' },
      { type: 'text', delta: 'Found it.' },
      { type: 'done' }
    ]))
    await sendMessage('find hybrid search')
    const msgs = get(messages)
    const assistant = msgs.find(m => m.role === 'assistant')
    expect(assistant?.sources).toHaveLength(1)
    expect(assistant?.sources?.[0]).toMatchObject({
      tool: 'codegraph_search',
      result: 'HybridSearchManager found'
    })
    expect(assistant?.content).toBe('Found it.')
  })

  it('sendMessage sets toolActivity during tool_use and clears on tool_result', async () => {
    const activityStates: (string | null)[] = []
    const unsub = toolActivity.subscribe(v => activityStates.push(v))

    mockFetch.mockResolvedValueOnce(makeStream([
      { type: 'tool_use', name: 'codegraph_hybrid_search', input: {} },
      { type: 'tool_result', name: 'codegraph_hybrid_search', result: 'results' },
      { type: 'done' }
    ]))
    await sendMessage('search')
    unsub()

    expect(activityStates).toContain('Running hybrid search...')
    expect(activityStates[activityStates.length - 1]).toBeNull()
  })

  it('sendMessage sets error on non-ok HTTP response', async () => {
    mockFetch.mockResolvedValueOnce(new Response('', { status: 500, statusText: 'Internal Server Error' }))
    await sendMessage('hi')
    expect(get(error)).toContain('500')
  })

  it('sendMessage sets error on network failure', async () => {
    mockFetch.mockRejectedValueOnce(new Error('Failed to fetch'))
    await sendMessage('hi')
    expect(get(error)).toContain('Failed to fetch')
    expect(get(loading)).toBe(false)
  })

  it('sendMessage sets error on error stream event', async () => {
    mockFetch.mockResolvedValueOnce(makeStream([
      { type: 'error', message: 'OPENAI_API_KEY is not set' }
    ]))
    await sendMessage('hi')
    expect(get(error)).toContain('OPENAI_API_KEY')
  })

  it('sendMessage builds correct history payload', async () => {
    mockFetch.mockResolvedValueOnce(makeStream([{ type: 'done' }]))
    await sendMessage('first message')

    mockFetch.mockResolvedValueOnce(makeStream([{ type: 'done' }]))
    await sendMessage('second message')

    const secondCall = mockFetch.mock.calls[1]
    const body = JSON.parse(secondCall[1].body)
    expect(body.messages).toHaveLength(3) // user + assistant + user
    expect(body.messages[0]).toMatchObject({ role: 'user', content: 'first message' })
    expect(body.messages[2]).toMatchObject({ role: 'user', content: 'second message' })
  })
})
