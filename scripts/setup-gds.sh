#!/bin/bash
# Downloads the Neo4j Graph Data Science (GDS) plugin jar into ./plugins/
# for use with docker-compose. GDS 2.13.2 is compatible with Neo4j 5.26.

set -euo pipefail

GDS_VERSION="2.13.2"
PLUGINS_DIR="$(cd "$(dirname "$0")/.." && pwd)/plugins"
JAR_NAME="neo4j-graph-data-science-${GDS_VERSION}.jar"
JAR_PATH="${PLUGINS_DIR}/${JAR_NAME}"

if [ -f "$JAR_PATH" ]; then
    echo "GDS ${GDS_VERSION} already present at ${JAR_PATH}"
    exit 0
fi

mkdir -p "$PLUGINS_DIR"

echo "Downloading GDS ${GDS_VERSION}..."
curl -L -o "$JAR_PATH" \
    "https://github.com/neo4j/graph-data-science/releases/download/${GDS_VERSION}/${JAR_NAME}"

echo "Downloaded to ${JAR_PATH} ($(du -h "$JAR_PATH" | cut -f1))"
echo "Restart Neo4j with: docker-compose restart neo4j"
