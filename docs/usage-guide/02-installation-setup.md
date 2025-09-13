# Installation & Setup

This guide covers detailed installation and configuration of CodeGraph and its dependencies.

## System Requirements

### Minimum Requirements
- **Go**: 1.21 or later
- **Memory**: 4GB RAM (8GB recommended)
- **Storage**: 2GB free space (more for large codebases)
- **OS**: Linux, macOS, or Windows with WSL2

### Recommended for Production
- **Go**: Latest stable version
- **Memory**: 16GB RAM or more
- **Storage**: SSD with 10GB+ free space
- **CPU**: 4+ cores
- **Network**: Reliable internet for dependency downloads

## Dependencies

### Required Dependencies

#### 1. Neo4j Database

**Option A: Docker (Recommended)**
```bash
# Pull and run Neo4j with APOC plugins
docker run -d \
  --name neo4j-codegraph \
  -p 7687:7687 \
  -p 7474:7474 \
  -e NEO4J_AUTH=neo4j/password123 \
  -e NEO4J_PLUGINS='["apoc","apoc-extended"]' \
  -e NEO4J_dbms_security_procedures_unrestricted=apoc.*,gds.* \
  -e NEO4J_dbms_connector_bolt_listen__address=0.0.0.0:7687 \
  -e NEO4J_dbms_connector_http_listen__address=0.0.0.0:7474 \
  -v neo4j-data:/data \
  -v neo4j-logs:/logs \
  neo4j:5.23.0
```

**Option B: Native Installation**

*Ubuntu/Debian:*
```bash
# Add Neo4j repository
wget -O - https://debian.neo4j.com/neotechnology.gpg.key | sudo apt-key add -
echo 'deb https://debian.neo4j.com stable latest' | sudo tee /etc/apt/sources.list.d/neo4j.list

# Install
sudo apt-get update
sudo apt-get install neo4j

# Configure
sudo systemctl enable neo4j
sudo systemctl start neo4j
```

*macOS:*
```bash
# Using Homebrew
brew install neo4j

# Start service
brew services start neo4j
```

*Windows:*
```powershell
# Download and run installer from https://neo4j.com/download/
# Or use Chocolatey
choco install neo4j-community
```

#### 2. Go Language

**Installation:**
```bash
# Linux/macOS
curl -L https://go.dev/dl/go1.21.5.linux-amd64.tar.gz | sudo tar -C /usr/local -xzf -
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# macOS with Homebrew
brew install go

# Windows: Download installer from https://golang.org/dl/
```

**Verify Installation:**
```bash
go version
# Should output: go version go1.21.5 linux/amd64 (or similar)
```

### Optional Dependencies

#### 1. SCIP-Go (for SCIP indexing)

```bash
# Install scip-go for advanced code intelligence
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest

# Verify installation
scip-go --version
```

#### 2. Git (for repository metadata)

```bash
# Usually pre-installed, but if needed:

# Ubuntu/Debian
sudo apt-get install git

# macOS
brew install git

# Windows: Download from https://git-scm.com/
```

## CodeGraph Installation

### Method 1: Build from Source (Recommended)

```bash
# Clone repository
git clone https://github.com/example/context-maximiser.git
cd context-maximiser

# Install Go dependencies
go mod download

# Build CodeGraph binary
go build -o codegraph cmd/codegraph/main.go

# Make executable and add to PATH (Linux/macOS)
chmod +x codegraph
sudo mv codegraph /usr/local/bin/

# Or keep in project directory
./codegraph --help
```

### Method 2: Go Install

```bash
# Install directly from source (when published)
go install github.com/example/context-maximiser/cmd/codegraph@latest

# Verify installation
codegraph --help
```

### Method 3: Pre-built Binaries

```bash
# Download from releases page (when available)
curl -L https://github.com/example/context-maximiser/releases/download/v1.0.0/codegraph-linux-amd64 -o codegraph
chmod +x codegraph
sudo mv codegraph /usr/local/bin/
```

## Configuration

### 1. Database Configuration

**Environment Variables:**
```bash
export NEO4J_URI="bolt://localhost:7687"
export NEO4J_USER="neo4j"
export NEO4J_PASSWORD="password123"
export NEO4J_DATABASE="neo4j"
```

**Configuration File (~/.codegraph.yaml):**
```yaml
neo4j:
  uri: "bolt://localhost:7687"
  username: "neo4j"
  password: "password123"
  database: "neo4j"

# Optional settings
verbose: false
```

**Command Line Flags:**
```bash
codegraph --neo4j-uri="bolt://localhost:7687" \
          --neo4j-user="neo4j" \
          --neo4j-password="password123" \
          status
```

### 2. SCIP Configuration

```bash
# Ensure scip-go is in PATH
export PATH=$PATH:$(go env GOPATH)/bin

# Verify
which scip-go
```

### 3. Docker Compose Setup (Advanced)

Create `docker-compose.yml`:
```yaml
version: '3.8'
services:
  neo4j:
    image: neo4j:5.23.0
    container_name: neo4j-codegraph
    ports:
      - "7687:7687"
      - "7474:7474"
    environment:
      NEO4J_AUTH: neo4j/password123
      NEO4J_PLUGINS: '["apoc","apoc-extended"]'
      NEO4J_dbms_security_procedures_unrestricted: apoc.*,gds.*
      NEO4J_dbms_connector_bolt_listen_address: 0.0.0.0:7687
      NEO4J_dbms_connector_http_listen_address: 0.0.0.0:7474
    volumes:
      - neo4j-data:/data
      - neo4j-logs:/logs
      - neo4j-conf:/var/lib/neo4j/conf
    healthcheck:
      test: ["CMD-SHELL", "cypher-shell -u neo4j -p password123 'RETURN 1'"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  neo4j-data:
  neo4j-logs:
  neo4j-conf:
```

Start services:
```bash
docker-compose up -d
```

## Verification

### 1. Test Database Connection

```bash
# Test connection
codegraph status

# Expected output:
# Database Status: Connected
# Neo4j Version: 5.23.0
# Edition: community
```

### 2. Test Schema Creation

```bash
# Create schema
codegraph schema create

# Verify schema
codegraph schema info
```

### 3. Test Basic Indexing

```bash
# Create test directory
mkdir test-project
cd test-project

# Create simple Go file
cat > main.go << 'EOF'
package main

import "fmt"

func main() {
    fmt.Println("Hello, CodeGraph!")
}

func greet(name string) string {
    return fmt.Sprintf("Hello, %s!", name)
}
EOF

# Index the test project
codegraph index project . --service="test"

# Verify indexing
codegraph query search "main"
```

## Performance Tuning

### Neo4j Configuration

Add to `neo4j.conf` or via environment variables:

```bash
# Memory settings (adjust based on system)
export NEO4J_dbms_memory_heap_initial_size=2G
export NEO4J_dbms_memory_heap_max_size=4G
export NEO4J_dbms_memory_pagecache_size=2G

# Query timeout (increase for large operations)
export NEO4J_dbms_transaction_timeout=60s
```

### Go Configuration

```bash
# Increase memory for large projects
export GOGC=200

# Use more CPU cores
export GOMAXPROCS=8
```

## Security Configuration

### 1. Change Default Credentials

```bash
# Connect to Neo4j browser: http://localhost:7474
# Change password from default 'password123'

# Update configuration
vim ~/.codegraph.yaml
```

### 2. Enable SSL (Production)

```bash
# Generate certificates
mkdir -p certs
openssl req -x509 -newkey rsa:4096 -keyout certs/key.pem -out certs/cert.pem -days 365 -nodes

# Configure Neo4j SSL
export NEO4J_dbms_connector_bolt_tls_level=REQUIRED
export NEO4J_dbms_ssl_policy_bolt_enabled=true
```

### 3. Network Security

```bash
# Bind to specific interface (production)
export NEO4J_dbms_connector_bolt_listen_address=127.0.0.1:7687
export NEO4J_dbms_connector_http_listen_address=127.0.0.1:7474
```

## Troubleshooting Installation

### Common Issues

**1. Neo4j Connection Failed**
```bash
# Check if Neo4j is running
docker ps | grep neo4j
# or
sudo systemctl status neo4j

# Check logs
docker logs neo4j-codegraph
# or
sudo journalctl -u neo4j
```

**2. Permission Denied**
```bash
# Fix binary permissions
chmod +x codegraph

# Or run with Go
go run cmd/codegraph/main.go status
```

**3. SCIP-Go Not Found**
```bash
# Install scip-go
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest

# Add to PATH
export PATH=$PATH:$(go env GOPATH)/bin
```

**4. Port Conflicts**
```bash
# Check port usage
lsof -i :7687
lsof -i :7474

# Use different ports if needed
docker run -p 7688:7687 -p 7475:7474 ... neo4j
```

### Debugging

**Enable Verbose Output:**
```bash
codegraph --verbose status
```

**Check Configuration:**
```bash
codegraph --help | grep -A 10 "Global Flags"
```

**Test Individual Components:**
```bash
# Test Go build
go build cmd/codegraph/main.go

# Test Neo4j directly
docker exec -it neo4j-codegraph cypher-shell -u neo4j -p password123
```

## Next Steps

After successful installation:

1. **Quick Test**: Follow [Quick Start Guide](./01-quick-start.md)
2. **Schema Setup**: Read [Schema Management](./03-schema-management.md)
3. **Index Code**: Explore [Code Indexing](./04-code-indexing.md)
4. **Production Config**: See [Configuration Reference](./10-configuration-reference.md)

## System Monitoring

### Health Checks

```bash
# Create monitoring script
cat > health-check.sh << 'EOF'
#!/bin/bash
echo "=== CodeGraph Health Check ==="
echo "Date: $(date)"
echo

echo "1. Database Connection:"
codegraph status || echo "❌ Database connection failed"

echo -e "\n2. Schema Status:"
codegraph schema info | head -5 || echo "❌ Schema check failed"

echo -e "\n3. Basic Query:"
codegraph query search "test" --limit 1 >/dev/null 2>&1 && echo "✅ Query works" || echo "❌ Query failed"

echo -e "\nHealth check complete."
EOF

chmod +x health-check.sh
```

### Resource Monitoring

```bash
# Monitor Neo4j memory usage
docker stats neo4j-codegraph

# Monitor disk space
df -h

# Monitor system resources
top -p $(pgrep -f codegraph)
```