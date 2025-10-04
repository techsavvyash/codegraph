// Test script to verify embedding persistence implementation
// This script can be run via Neo4j browser or cypher-shell

// 1. Check if any Function nodes have embeddings
MATCH (f:Function)
WHERE f.embedding IS NOT NULL
RETURN f.id, f.name, size(f.embedding) as embedding_dims
LIMIT 10;

// 2. Count total functions with and without embeddings
MATCH (f:Function)
RETURN
  count(f) as total_functions,
  count(f.embedding) as functions_with_embeddings,
  count(f) - count(f.embedding) as functions_without_embeddings;

// 3. Check IMPLEMENTS relationships with their metadata
MATCH (f:Function)-[r:IMPLEMENTS]->(feature:Feature)
RETURN
  f.name as function_name,
  feature.name as feature_name,
  r.confidence as confidence,
  r.validationMethod as validation_method,
  r.subgraphSize as subgraph_size,
  size(f.embedding) as function_embedding_dims
LIMIT 10;

// 4. Verify embedding vector properties
MATCH (f:Function)
WHERE f.embedding IS NOT NULL
WITH f, size(f.embedding) as dims
RETURN
  dims,
  count(f) as functions_count
ORDER BY dims;
