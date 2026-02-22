export interface ToolSource {
  tool: string
  result: string
}

export interface Message {
  id: string
  role: 'user' | 'assistant' | 'system'
  content: string
  sources?: ToolSource[]
}

export interface StreamEvent {
  type: 'text' | 'tool_use' | 'tool_result' | 'done' | 'error' | 'warning'
  delta?: string
  name?: string
  input?: Record<string, unknown>
  result?: string
  message?: string
}
