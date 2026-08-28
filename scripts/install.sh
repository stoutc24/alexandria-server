#!/bin/bash

# =============================================================================
# Alexandria Server - Quick Install Script
# =============================================================================
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/stoutc24/alexandria-server/main/scripts/install.sh | bash
#
# Or manually:
#   ./scripts/install.sh
#
# =============================================================================

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default installation directory
INSTALL_DIR="${INSTALL_DIR:-$HOME/alexandria-server}"

echo -e "${BLUE}"
echo "  ___   _                           _      _       "
echo " / _ \ | | _____  ____ _ _ __   __| |_ __(_) __ _ "
echo "| | | || |/ _ \ \/ / _\` | '_ \ / _\` | '__| |/ _\` |"
echo "| |_| ||  __/>  < (_| | | | | (_| | |  | | (_| |"
echo " \___(_)___/_/\_\__,_|_| |_|\__,_|_|  |_|\__,_|"
echo -e "${NC}"
echo "Alexandria Server - Self-Hosted Family Media Backend"
echo "======================================================"
echo ""

# Check for Docker
check_docker() {
    if ! command -v docker &> /dev/null; then
        echo -e "${RED}Error: Docker is not installed.${NC}"
        echo ""
        echo "Please install Docker first:"
        echo "  - Linux: https://docs.docker.com/engine/install/"
        echo "  - macOS: https://docs.docker.com/desktop/install/mac-install/"
        echo "  - Windows: https://docs.docker.com/desktop/install/windows-install/"
        exit 1
    fi
    echo -e "${GREEN}✓${NC} Docker found"
}

# Check for Docker Compose
check_docker_compose() {
    if docker compose version &> /dev/null; then
        COMPOSE_CMD="docker compose"
    elif command -v docker-compose &> /dev/null; then
        COMPOSE_CMD="docker-compose"
    else
        echo -e "${RED}Error: Docker Compose is not installed.${NC}"
        echo "Please install Docker Compose: https://docs.docker.com/compose/install/"
        exit 1
    fi
    echo -e "${GREEN}✓${NC} Docker Compose found"
}

# Generate a secure random secret
generate_secret() {
    if command -v openssl &> /dev/null; then
        openssl rand -base64 32
    else
        head -c 32 /dev/urandom | base64
    fi
}

# Main installation
main() {
    echo -e "${YELLOW}Checking requirements...${NC}"
    check_docker
    check_docker_compose
    echo ""

    # Create installation directory
    echo -e "${YELLOW}Creating installation directory...${NC}"
    mkdir -p "$INSTALL_DIR"
    cd "$INSTALL_DIR"
    echo -e "${GREEN}✓${NC} Directory: $INSTALL_DIR"
    echo ""

    # Download docker-compose.yml
    echo -e "${YELLOW}Downloading configuration...${NC}"
    curl -fsSL "https://raw.githubusercontent.com/stoutc24/alexandria-server/main/docker-compose.yml" -o docker-compose.yml
    echo -e "${GREEN}✓${NC} docker-compose.yml downloaded"

    # Create .env file if it doesn't exist
    if [ ! -f .env ]; then
        echo -e "${YELLOW}Generating configuration...${NC}"
        ALEXANDRIA_API_TOKEN=$(generate_secret)
        cat > .env << EOF
# Alexandria Server Configuration
# Generated on $(date)

# Server port (default: 3000)
PORT=3000

# Shared API token - KEEP THIS SAFE!
# The app sends it as: Authorization: Bearer <token>
# The server refuses to start without it.
ALEXANDRIA_API_TOKEN=${ALEXANDRIA_API_TOKEN}

# Timezone
TZ=UTC
EOF
        echo -e "${GREEN}✓${NC} .env file created with a generated API token"
        echo -e "${YELLOW}!${NC} Enter this token in the app under Settings:"
        echo -e "    ${ALEXANDRIA_API_TOKEN}"
    else
        echo -e "${YELLOW}!${NC} .env file already exists, skipping..."
    fi
    echo ""

    # Pull and start
    echo -e "${YELLOW}Starting Alexandria Server...${NC}"
    $COMPOSE_CMD pull 2>/dev/null || true
    $COMPOSE_CMD up -d --build

    echo ""
    echo -e "${GREEN}======================================================"
    echo "  Alexandria Server is now running!"
    echo "======================================================${NC}"
    echo ""
    echo "  URL:        http://localhost:3000"
    echo "  Health:     http://localhost:3000/health"
    echo "  API Docs:   http://localhost:3000/api/v1"
    echo ""
    echo "  Installation directory: $INSTALL_DIR"
    echo ""
    echo -e "${YELLOW}Useful commands:${NC}"
    echo "  cd $INSTALL_DIR"
    echo "  $COMPOSE_CMD logs -f        # View logs"
    echo "  $COMPOSE_CMD restart        # Restart server"
    echo "  $COMPOSE_CMD down           # Stop server"
    echo "  $COMPOSE_CMD pull && $COMPOSE_CMD up -d  # Update"
    echo ""
}

main "$@"
