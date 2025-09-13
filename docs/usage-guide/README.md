# CodeGraph Usage Guide

Welcome to the CodeGraph Usage Guide! This documentation covers all aspects of using the CodeGraph CLI tool for building and querying a comprehensive code intelligence platform.

## Table of Contents

1. [Quick Start Guide](./01-quick-start.md) - Get up and running in minutes
2. [Installation & Setup](./02-installation-setup.md) - Detailed installation and configuration
3. [Schema Management](./03-schema-management.md) - Managing Neo4j schema and database
4. [Code Indexing](./04-code-indexing.md) - Indexing source code with AST and SCIP
5. [Document Indexing](./05-document-indexing.md) - Indexing documentation and requirements
6. [Querying & Search](./06-querying-search.md) - Searching and retrieving code information
7. [Source Code Retrieval](./07-source-code-retrieval.md) - Precise function source code extraction
8. [Advanced Queries](./08-advanced-queries.md) - Complex graph queries and analysis
9. [Integration Guide](./09-integration-guide.md) - Integrating with LLMs and other tools
10. [Configuration Reference](./10-configuration-reference.md) - Complete configuration options
11. [Troubleshooting](./11-troubleshooting.md) - Common issues and solutions

## What is CodeGraph?

CodeGraph is a CLI tool that creates a comprehensive Code Property Graph (CPG) using Neo4j as the backend. It captures:

- **Syntactic Structure**: Classes, functions, variables from AST parsing
- **Semantic Relationships**: Symbol definitions, references, and dependencies
- **Control Flow**: Function calls and data flow patterns
- **Business Context**: Documentation, requirements, and features
- **Precise Location Data**: Exact byte offsets for LLM integration

## Key Features

### 🔍 **Multi-Modal Indexing**
- **AST Indexing**: Deep Go code analysis using go/ast
- **SCIP Indexing**: Standards-based code intelligence protocol
- **Document Indexing**: LLM-powered feature extraction from docs

### 📊 **Rich Graph Model**
- **Code Entities**: Functions, Methods, Classes, Variables
- **Relationships**: Calls, Contains, References, Implements
- **Metadata**: Location data, signatures, complexity metrics

### 🎯 **Precise Code Retrieval**
- **Byte-Level Accuracy**: Exact source code extraction
- **Multiple Access Methods**: By name, signature, or graph traversal
- **LLM-Ready**: Direct integration with language models

### 🚀 **Production Features**
- **Scalable**: Handles large codebases efficiently
- **Reliable**: Comprehensive test coverage
- **Flexible**: Multiple indexing strategies and query patterns

## Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Source Code   │    │   Documentation  │    │   Requirements  │
│                 │    │                  │    │                 │
│ • Go Files      │    │ • Markdown       │    │ • Features      │
│ • Packages      │    │ • READMEs        │    │ • User Stories  │
│ • Dependencies  │    │ • Docs           │    │ • PRDs          │
└─────────────────┘    └──────────────────┘    └─────────────────┘
         │                       │                       │
         ▼                       ▼                       ▼
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  AST Indexer    │    │  SCIP Indexer    │    │ Document Parser │
│                 │    │                  │    │                 │
│ • go/ast        │    │ • scip-go        │    │ • LLM Features  │
│ • Token Sets    │    │ • Protocol Buf   │    │ • Entity Extract│
│ • Precise Loc   │    │ • Cross-Lang     │    │ • Relationships │
└─────────────────┘    └──────────────────┘    └─────────────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 ▼
                    ┌──────────────────┐
                    │   Neo4j Graph    │
                    │                  │
                    │ • Nodes (CPG)    │
                    │ • Relationships  │
                    │ • Properties     │
                    │ • Indexes        │
                    │ • Constraints    │
                    └──────────────────┘
                                 │
                                 ▼
                    ┌──────────────────┐
                    │  Query Interface │
                    │                  │
                    │ • CLI Commands   │
                    │ • Cypher Queries │
                    │ • Source Retrieval│
                    │ • LLM Integration│
                    └──────────────────┘
```

## Getting Started

1. **Install Dependencies**
   ```bash
   # Install Neo4j (Docker recommended)
   docker run -p 7687:7687 -p 7474:7474 -e NEO4J_AUTH=neo4j/password123 neo4j:latest
   
   # Install scip-go for SCIP indexing
   go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
   ```

2. **Build CodeGraph**
   ```bash
   go build -o codegraph cmd/codegraph/main.go
   ```

3. **Set up Schema**
   ```bash
   ./codegraph schema create
   ```

4. **Index Your Code**
   ```bash
   ./codegraph index project . --service="my-project"
   ```

5. **Start Querying**
   ```bash
   ./codegraph query search "function_name"
   ./codegraph query source "function_name"
   ```

## Next Steps

- Read the [Quick Start Guide](./01-quick-start.md) for hands-on examples
- Explore [Code Indexing](./04-code-indexing.md) for different indexing strategies  
- Check out [Source Code Retrieval](./07-source-code-retrieval.md) for LLM integration
- See [Advanced Queries](./08-advanced-queries.md) for powerful analysis patterns

## Support

- 📚 **Documentation**: Complete guides in this usage-guide folder
- 🐛 **Issues**: Report bugs and request features
- 💡 **Integration**: LLM and tool integration examples
- 🚀 **Performance**: Optimization tips and best practices