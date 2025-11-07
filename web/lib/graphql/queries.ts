import { gql } from '@apollo/client'

// Service Queries
export const GET_SERVICES = gql`
  query GetServices {
    services {
      name
      packageName
      version
      language
      repositoryURL
      indexedAt
      fileCount
      symbolCount
    }
  }
`

export const GET_SERVICE = gql`
  query GetService($name: String!) {
    service(name: $name) {
      name
      packageName
      version
      language
      repositoryURL
      indexedAt
      fileCount
      symbolCount
    }
  }
`

// File Queries
export const GET_FILES = gql`
  query GetFiles($serviceName: String!, $limit: Int, $offset: Int) {
    files(serviceName: $serviceName, limit: $limit, offset: $offset) {
      path
      serviceName
      language
      lines
      size
      hash
    }
  }
`

export const GET_FILE = gql`
  query GetFile($path: String!, $serviceName: String!) {
    file(path: $path, serviceName: $serviceName) {
      path
      serviceName
      language
      lines
      size
      hash
    }
  }
`

// Symbol Queries
export const SEARCH_SYMBOLS = gql`
  query SearchSymbols($query: String!, $limit: Int) {
    symbols(query: $query, limit: $limit) {
      scipSymbol
      name
      kind
      displayName
      filePath
      serviceName
      startLine
      endLine
      signature
      documentation
    }
  }
`

// Graph Queries
export const GET_GRAPH = gql`
  query GetGraph($serviceName: String!, $limit: Int, $offset: Int) {
    graph(serviceName: $serviceName, limit: $limit, offset: $offset) {
      nodes {
        id
        type
        label
        properties
      }
      edges {
        id
        source
        target
        type
        properties
      }
      totalNodes
      totalEdges
      hasMore
    }
  }
`

// Metadata Mutations
export const ADD_METADATA = gql`
  mutation AddMetadata(
    $targetId: String!
    $targetType: String!
    $key: String!
    $value: String!
    $category: String
  ) {
    addMetadata(
      targetId: $targetId
      targetType: $targetType
      key: $key
      value: $value
      category: $category
    ) {
      key
      value
      category
      createdAt
      createdBy
    }
  }
`
