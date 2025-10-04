#!/bin/bash

echo "🔍 Testing Comment-Based Search Functionality"
echo "============================================="
echo

# Test if we can search for functions through comments
echo "1. Testing hybrid search with 'execute' (should include comment results):"
./codegraph search test "execute" --limit 3
echo

echo "2. Testing hybrid search with 'database' (should include comment results):"
./codegraph search test "database" --limit 3
echo

echo "3. Let's check what Comment nodes exist in Neo4j:"
# We'll use the search command to look for anything with "Comment" in it
./codegraph query search "Comment" --limit 5
echo

echo "4. Let's force recreate comment embeddings with smaller batch to see progress:"
./codegraph search comments --gemini --api-key="AIzaSyBQHQRSX0Prk5hlVCYAn4P0oV7_0VYh1II" --dimensions 768 --batch-size 5 --dry-run
echo

echo "Note: If no new comment embeddings are being created, it means they already exist from previous runs."
echo "The hybrid search should still work and include comment-based results automatically."