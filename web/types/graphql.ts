// GraphQL Types

export interface Service {
  name: string
  packageName?: string
  version?: string
  language: string
  repositoryURL?: string
  indexedAt: string
  fileCount: number
  symbolCount: number
}

export interface File {
  path: string
  serviceName: string
  language: string
  lines?: number
  size?: number
  hash?: string
}

export interface Symbol {
  scipSymbol: string
  name: string
  kind: string
  displayName?: string
  filePath: string
  serviceName?: string
  startLine?: number
  endLine?: number
  signature?: string
  documentation?: string
}

export interface GraphNode {
  id: string
  type: string
  label: string
  properties?: string
}

export interface GraphEdge {
  id: string
  source: string
  target: string
  type: string
  properties?: string
}

export interface Graph {
  nodes: GraphNode[]
  edges: GraphEdge[]
  totalNodes: number
  totalEdges: number
  hasMore: boolean
}

export interface Metadata {
  key: string
  value: string
  category?: string
  createdAt: string
  updatedAt?: string
  createdBy?: string
}

// Query Variables
export interface GetServiceVariables {
  name: string
}

export interface GetFilesVariables {
  serviceName: string
  limit?: number
  offset?: number
}

export interface GetFileVariables {
  path: string
  serviceName: string
}

export interface SearchSymbolsVariables {
  query: string
  limit?: number
}

export interface GetGraphVariables {
  serviceName: string
  limit?: number
  offset?: number
}

export interface AddMetadataVariables {
  targetId: string
  targetType: string
  key: string
  value: string
  category?: string
}

// Response Types
export interface GetServicesResponse {
  services: Service[]
}

export interface GetServiceResponse {
  service: Service | null
}

export interface GetFilesResponse {
  files: File[]
}

export interface GetFileResponse {
  file: File | null
}

export interface SearchSymbolsResponse {
  symbols: Symbol[]
}

export interface GetGraphResponse {
  graph: Graph
}

export interface AddMetadataResponse {
  addMetadata: Metadata
}
