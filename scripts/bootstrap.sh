#!/usr/bin/env bash
set -euo pipefail

echo "=== CodeGraph Platform Bootstrap ==="

# Check Go
if ! command -v go &> /dev/null; then
  echo "ERROR: Go not found. Install Go 1.24+ from https://go.dev/dl/"
  exit 1
fi

# Check Docker
if ! command -v docker &> /dev/null; then
  echo "ERROR: Docker not found. Install Docker Desktop or Docker Engine."
  exit 1
fi

# Install pnpm/nx if needed
if ! command -v pnpm &> /dev/null; then
  npm install -g pnpm
fi

# Install node deps
pnpm install

# Start infrastructure
docker compose -f infra/docker/compose.platform.yml up -d

echo "Waiting for services to be healthy..."
sleep 15

# Build CLI
make build

echo ""
echo "=== Bootstrap complete ==="
echo "Run: ./bin/codegraph status"
