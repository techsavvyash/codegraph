#!/bin/bash

# CodeGraph Uninstaller
# Removes CodeGraph installation, stops Neo4j, and cleans up configuration

INSTALL_DIR="${CODEGRAPH_INSTALL_DIR:-$HOME/.codegraph}"
BIN_DIR="${CODEGRAPH_BIN_DIR:-/usr/local/bin}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

ask_permission() {
    local prompt="$1"
    local response
    while true; do
        read -p "$(echo -e ${YELLOW}[CONFIRM]${NC} $prompt [y/N]: )" response
        case "$response" in
            [yY][eE][sS]|[yY]) return 0 ;;
            [nN][oO]|[nN]|"") return 1 ;;
            *) echo "Please answer yes or no." ;;
        esac
    done
}

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                                                              ║"
echo "║            CodeGraph Uninstallation Script                   ║"
echo "║                                                              ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

log_warn "This will remove CodeGraph from your system"
echo ""
echo "The following will be removed:"
echo "  • CodeGraph installation: $INSTALL_DIR"
echo "  • CLI binary: $BIN_DIR/codegraph"
echo "  • Configuration: $HOME/.codegraph.yaml"
echo "  • Neo4j Docker containers and volumes"
echo ""

if ! ask_permission "Continue with uninstallation?"; then
    echo "Uninstallation cancelled."
    exit 0
fi

# Stop and remove Neo4j containers
if [ -f "$INSTALL_DIR/docker-compose.yml" ]; then
    log_info "Stopping Neo4j containers..."
    cd "$INSTALL_DIR"
    docker-compose down -v 2>/dev/null || true
    log_success "Neo4j containers stopped and removed"
fi

# Remove MCP server from Claude Code
if command -v claude >/dev/null 2>&1; then
    if ask_permission "Remove CodeGraph MCP server from Claude Code?"; then
        log_info "Removing MCP server configuration..."
        claude mcp remove codegraph 2>/dev/null || log_warn "MCP server not found in Claude Code config"
        log_success "MCP server removed (restart Claude Code to apply)"
    fi
fi

# Remove installation directory
if [ -d "$INSTALL_DIR" ]; then
    log_info "Removing installation directory..."
    rm -rf "$INSTALL_DIR"
    log_success "Installation directory removed"
fi

# Remove CLI binary
if [ -f "$BIN_DIR/codegraph" ]; then
    log_info "Removing CLI binary..."
    if [ -w "$BIN_DIR" ]; then
        rm "$BIN_DIR/codegraph"
    else
        sudo rm "$BIN_DIR/codegraph"
    fi
    log_success "CLI binary removed"
fi

# Remove configuration
if [ -f "$HOME/.codegraph.yaml" ]; then
    if ask_permission "Remove configuration file (~/.codegraph.yaml)?"; then
        rm "$HOME/.codegraph.yaml"
        log_success "Configuration file removed"
    else
        log_info "Configuration file kept"
    fi
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log_success "CodeGraph has been uninstalled"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Thank you for using CodeGraph!"
echo ""
