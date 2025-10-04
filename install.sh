#!/bin/bash
set -e

# CodeGraph Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/.../install.sh | bash

VERSION="${CODEGRAPH_VERSION:-latest}"
INSTALL_DIR="${CODEGRAPH_INSTALL_DIR:-$HOME/.codegraph}"
BIN_DIR="${CODEGRAPH_BIN_DIR:-/usr/local/bin}"
NEO4J_PASSWORD="${CODEGRAPH_NEO4J_PASSWORD:-password123}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Ask for user confirmation
ask_permission() {
    local prompt="$1"
    local response

    while true; do
        read -p "$(echo -e ${YELLOW}[PERMISSION REQUIRED]${NC} $prompt [y/N]: )" response
        case "$response" in
            [yY][eE][sS]|[yY])
                return 0
                ;;
            [nN][oO]|[nN]|"")
                return 1
                ;;
            *)
                echo "Please answer yes or no."
                ;;
        esac
    done
}

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Darwin*)
            OS="macos"
            ;;
        Linux*)
            OS="linux"
            ;;
        *)
            log_error "Unsupported operating system: $(uname -s)"
            exit 1
            ;;
    esac
    log_info "Detected OS: $OS"
}

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."

    # Check Go
    if ! command_exists go; then
        log_error "Go is not installed. Please install Go 1.24+ from https://go.dev/dl/"
        exit 1
    fi

    local go_version=$(go version | awk '{print $3}' | sed 's/go//')
    log_success "Go $go_version installed"

    # Check Docker
    if ! command_exists docker; then
        log_error "Docker is not installed. Please install Docker from https://docker.com/get-started"
        exit 1
    fi
    log_success "Docker installed"

    # Check Docker Compose
    if ! command_exists docker-compose && ! docker compose version >/dev/null 2>&1; then
        log_error "Docker Compose is not installed"
        exit 1
    fi
    log_success "Docker Compose installed"

    # Check if Docker daemon is running
    if ! docker info >/dev/null 2>&1; then
        log_warn "Docker daemon is not running"

        if ask_permission "Docker daemon is not running. Start it now?"; then
            log_info "Starting Docker daemon..."

            if [ "$OS" = "macos" ]; then
                open -a Docker
                log_info "Waiting for Docker to start (this may take 30-60 seconds)..."

                # Wait for Docker to start (max 2 minutes)
                local count=0
                while ! docker info >/dev/null 2>&1; do
                    sleep 5
                    count=$((count + 1))
                    if [ $count -gt 24 ]; then
                        log_error "Docker failed to start after 2 minutes"
                        exit 1
                    fi
                    echo -n "."
                done
                echo ""
                log_success "Docker is now running"
            else
                sudo systemctl start docker
                sleep 5
                log_success "Docker daemon started"
            fi
        else
            log_error "Docker daemon is required. Please start it manually and re-run the installer."
            exit 1
        fi
    fi
}

# Clone or download repository
setup_repository() {
    log_info "Setting up CodeGraph in $INSTALL_DIR..."

    if [ -d "$INSTALL_DIR" ]; then
        log_warn "CodeGraph is already installed at $INSTALL_DIR"

        if ask_permission "Reinstall CodeGraph?"; then
            log_info "Removing existing installation..."
            rm -rf "$INSTALL_DIR"
        else
            log_info "Using existing installation"
            cd "$INSTALL_DIR"
            return 0
        fi
    fi

    mkdir -p "$INSTALL_DIR"

    # Try to clone from git (update with actual repo URL)
    if command_exists git; then
        log_info "Cloning repository..."
        # TODO: Update with actual repository URL
        # git clone https://github.com/yourusername/context-maximiser.git "$INSTALL_DIR"

        # For now, assume we're running from the repo
        if [ -f "$(dirname "$0")/go.mod" ]; then
            log_info "Running from source repository, copying files..."
            cp -r "$(dirname "$0")"/* "$INSTALL_DIR/"
        else
            log_error "Repository URL not configured. Please clone manually."
            exit 1
        fi
    fi

    cd "$INSTALL_DIR"
    log_success "Repository ready at $INSTALL_DIR"
}

# Build CLI
build_cli() {
    log_info "Building CodeGraph CLI..."

    cd "$INSTALL_DIR"
    make build

    log_success "CLI built successfully"

    # Install CLI to bin directory
    if ask_permission "Install codegraph to $BIN_DIR (may require sudo)?"; then
        if [ -w "$BIN_DIR" ]; then
            cp bin/codegraph "$BIN_DIR/codegraph"
        else
            sudo cp bin/codegraph "$BIN_DIR/codegraph"
        fi
        log_success "codegraph installed to $BIN_DIR"
    else
        log_warn "CLI not installed to $BIN_DIR. Add $INSTALL_DIR/bin to your PATH:"
        echo "    export PATH=\"$INSTALL_DIR/bin:\$PATH\""
    fi
}

# Setup Neo4j
setup_neo4j() {
    log_info "Setting up Neo4j database..."

    # Ensure docker-compose.yml is in the install directory
    if [ ! -f "$INSTALL_DIR/docker-compose.yml" ]; then
        log_error "docker-compose.yml not found in $INSTALL_DIR"
        exit 1
    fi

    cd "$INSTALL_DIR"

    # Check if Neo4j is already running
    if docker ps | grep -q neo4j; then
        log_warn "Neo4j container is already running"

        if ! ask_permission "Restart Neo4j container?"; then
            log_info "Keeping existing Neo4j container"
            return 0
        fi

        log_info "Stopping existing Neo4j container..."
        docker-compose down
    fi

    log_info "Starting Neo4j with Docker Compose..."
    log_info "Pulling Neo4j image if needed (this may take a few minutes on first run)..."
    docker-compose up -d

    log_info "Waiting for Neo4j to be ready (this may take 30 seconds)..."
    sleep 10

    local count=0
    while ! docker exec $(docker ps -qf "name=neo4j") cypher-shell -u neo4j -p "$NEO4J_PASSWORD" "RETURN 1;" >/dev/null 2>&1; do
        sleep 5
        count=$((count + 1))
        if [ $count -gt 12 ]; then
            log_error "Neo4j failed to start after 60 seconds"
            log_info "Check logs with: docker-compose logs neo4j"
            exit 1
        fi
        echo -n "."
    done
    echo ""

    log_success "Neo4j is running at bolt://localhost:7687"
    log_info "Neo4j Browser: http://localhost:7474 (username: neo4j, password: $NEO4J_PASSWORD)"

    # Create schema
    log_info "Creating database schema..."
    if command_exists codegraph; then
        codegraph schema create
    else
        "$INSTALL_DIR/bin/codegraph" schema create
    fi
    log_success "Database schema created"
}

# Create configuration file
create_config() {
    log_info "Creating configuration file..."

    local config_file="$HOME/.codegraph.yaml"

    if [ -f "$config_file" ]; then
        log_warn "Configuration file already exists at $config_file"
        if ! ask_permission "Overwrite existing configuration?"; then
            log_info "Keeping existing configuration"
            return 0
        fi
    fi

    cat > "$config_file" << EOF
# CodeGraph Configuration
neo4j:
  uri: "bolt://localhost:7687"
  username: "neo4j"
  password: "$NEO4J_PASSWORD"
  database: "neo4j"

verbose: false

# LLM Provider Configuration (optional)
# llm:
#   provider: "litellm"  # or "openai", "gemini"
#   base_url: "http://localhost:4000"
#   api_key: "your-api-key"
#   text_model: "openai/gpt-4"
#   embedding_model: "openai/text-embedding-3-small"
EOF

    log_success "Configuration created at $config_file"
}

# Build MCP server
build_mcp_server() {
    log_info "Building MCP server..."

    cd "$INSTALL_DIR/mcp-server"
    go build -o codegraph-mcp .

    log_success "MCP server built at $INSTALL_DIR/mcp-server/codegraph-mcp"
}

# Setup MCP server with Claude Code
setup_mcp_server() {
    log_info "Setting up MCP server for Claude Code..."

    # Check if claude CLI is available
    if ! command_exists claude; then
        log_warn "Claude CLI not found. MCP server will not be configured automatically."
        log_info "To add the MCP server manually, run:"
        echo ""
        echo "    claude mcp add codegraph $INSTALL_DIR/mcp-server/codegraph-mcp NEO4J_URI=bolt://localhost:7687 NEO4J_USERNAME=neo4j NEO4J_PASSWORD=$NEO4J_PASSWORD"
        echo ""
        return 0
    fi

    if ask_permission "Add CodeGraph MCP server to Claude Code?"; then
        log_info "Adding MCP server to Claude Code..."

        claude mcp add codegraph \
            "$INSTALL_DIR/mcp-server/codegraph-mcp" \
            NEO4J_URI=bolt://localhost:7687 \
            NEO4J_USERNAME=neo4j \
            NEO4J_PASSWORD="$NEO4J_PASSWORD"

        log_success "MCP server added to Claude Code"
        log_warn "Please restart Claude Code for changes to take effect"
    else
        log_info "Skipping MCP server setup. You can add it later with:"
        echo ""
        echo "    claude mcp add codegraph $INSTALL_DIR/mcp-server/codegraph-mcp NEO4J_URI=bolt://localhost:7687 NEO4J_USERNAME=neo4j NEO4J_PASSWORD=$NEO4J_PASSWORD"
        echo ""
    fi
}

# Print next steps
print_next_steps() {
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    log_success "CodeGraph installation completed successfully!"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo -e "${GREEN}Installation Summary:${NC}"
    echo "  • CodeGraph CLI: $(which codegraph 2>/dev/null || echo "$INSTALL_DIR/bin/codegraph")"
    echo "  • Neo4j Database: bolt://localhost:7687"
    echo "  • Neo4j Browser: http://localhost:7474"
    echo "  • MCP Server: $INSTALL_DIR/mcp-server/codegraph-mcp"
    echo "  • Configuration: $HOME/.codegraph.yaml"
    echo ""
    echo -e "${BLUE}Quick Start:${NC}"
    echo ""
    echo "1. Index your first project:"
    echo "   $ codegraph index scip /path/to/your/project --service=\"my-service\""
    echo ""
    echo "2. Search for code:"
    echo "   $ codegraph query search \"functionName\""
    echo ""
    echo "3. Check database status:"
    echo "   $ codegraph status"
    echo ""
    echo "4. View Neo4j graph in browser:"
    echo "   Open http://localhost:7474 (username: neo4j, password: $NEO4J_PASSWORD)"
    echo ""

    if command_exists claude; then
        echo -e "${BLUE}Claude Code Integration:${NC}"
        echo "  • MCP server is configured and ready to use"
        echo "  • Restart Claude Code to activate the MCP tools"
        echo "  • Available tools: codegraph_search, codegraph_list_services, and more"
        echo ""
    fi

    echo -e "${BLUE}Install SCIP Indexers (language support):${NC}"
    echo "  • Go:         go install github.com/sourcegraph/scip-go/cmd/scip-go@latest"
    echo "  • TypeScript: npm install -g @sourcegraph/scip-typescript"
    echo "  • Python:     pip install scip-python"
    echo "  • Java:       See https://sourcegraph.github.io/scip-java/"
    echo ""
    echo -e "${BLUE}Resources:${NC}"
    echo "  • Installation directory: $INSTALL_DIR"
    echo "  • Documentation: $INSTALL_DIR/README.md"
    echo "  • CLAUDE.md (for Claude): $INSTALL_DIR/CLAUDE.md"
    echo "  • Configuration: $HOME/.codegraph.yaml"
    echo ""
    echo -e "${BLUE}Managing Neo4j:${NC}"
    echo "  • Stop:  cd $INSTALL_DIR && docker-compose down"
    echo "  • Start: cd $INSTALL_DIR && docker-compose up -d"
    echo "  • Logs:  cd $INSTALL_DIR && docker-compose logs -f"
    echo ""
    echo -e "${YELLOW}Note:${NC} All CodeGraph files are in $INSTALL_DIR for easy management"
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
}

# Main installation flow
main() {
    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                                                              ║"
    echo "║              CodeGraph Installation Script                   ║"
    echo "║         Neo4j-Based Code Intelligence Platform               ║"
    echo "║                                                              ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""

    detect_os
    check_prerequisites
    setup_repository
    build_cli
    create_config
    setup_neo4j
    build_mcp_server
    setup_mcp_server
    print_next_steps
}

# Run main installation
main
