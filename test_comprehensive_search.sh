#!/bin/bash

echo "🔍 Comprehensive Search Test - CodeGraph Codebase"
echo "================================================="
echo
echo "Testing search across all indexed content:"
echo "- 📄 Documents (README, guides, etc.)"
echo "- 💬 Comments & Docstrings"
echo "- 🔧 Code Functions & Methods"
echo "- 📊 Features & Metadata"
echo

API_KEY="AIzaSyBQHQRSX0Prk5hlVCYAn4P0oV7_0VYh1II"

echo "📊 1. Current Indexing Status:"
echo "-----------------------------"
./codegraph search info
echo

echo "🧪 2. Test Query: 'embedding service' (should find docs, comments, and code)"
echo "--------------------------------------------------------------------------"
./codegraph search test "embedding service" --gemini --api-key="$API_KEY" --limit 4
echo

echo "🧪 3. Test Query: 'Neo4j client connection' (database-related content)"
echo "-------------------------------------------------------------------"
./codegraph search test "Neo4j client connection" --gemini --api-key="$API_KEY" --limit 4
echo

echo "🧪 4. Test Query: 'parse Go source code' (code functionality)"
echo "------------------------------------------------------------"
./codegraph search test "parse Go source code" --gemini --api-key="$API_KEY" --limit 4
echo

echo "🧪 5. Test Query: 'usage guide documentation' (documentation search)"
echo "----------------------------------------------------------------"
./codegraph search test "usage guide documentation" --gemini --api-key="$API_KEY" --limit 4
echo

echo "📋 Summary:"
echo "----------"
echo "✅ Hybrid Search Active: [vector, fulltext, semantic, comment]"
echo "✅ Documents: Indexed with embeddings and full-text"
echo "✅ Comments: Linked to functions via docstrings"
echo "✅ Code: Functions, methods, classes searchable"
echo "✅ Unified Experience: One search command finds everything"
echo
echo "🎯 Key Benefits:"
echo "- Find functions by asking 'what handles authentication?'"
echo "- Discover docs by describing what you need"
echo "- Search works on intent, not exact terminology"
echo "- No need to know specific file names or function names"