export const TOOL_ACTIVITY_LABELS: Record<string, string> = {
  codegraph_search:              'Searching codebase...',
  codegraph_hybrid_search:       'Running hybrid search...',
  codegraph_vector_search:       'Running semantic search...',
  codegraph_get_source:          'Fetching source code...',
  codegraph_find_references:     'Finding references...',
  codegraph_analyze_function:    'Analyzing function...',
  codegraph_search_docs:         'Searching documentation...',
  codegraph_search_by_comment:   'Searching by comment...',
}

export const SYSTEM_PROMPT = `You are a code intelligence assistant for the CodeGraph project.
You have access to a graph database of code entities indexed from this repository.
Use the available tools to search and retrieve code before answering questions.
Always cite which functions/files you used to derive your answer.`
