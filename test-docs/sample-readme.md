# CodeGraph Test Documentation

This is a test document for the CodeGraph project.

## Features

The `NewGeminiEmbeddingService` function creates a new embedding service using Google's Gemini API. It supports generating embeddings for semantic search.

The `VectorSearchManager` handles vector-based semantic search operations. It can create vector indexes and perform similarity searches.

## Usage

To use the document indexing functionality:

1. Call `IndexDocument` to index a markdown file
2. Use `SearchFunctionsByComment` to find functions by their docstrings
3. The `HybridSearchManager` combines multiple search methods

## Code References

The main entry point is in `main.go` where the CLI commands are defined.

Vector search is implemented in the `search` package.