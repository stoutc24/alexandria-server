#!/bin/bash

# =============================================================================
# Alexandria Server - Update Script
# =============================================================================

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

INSTALL_DIR="${INSTALL_DIR:-$HOME/alexandria-server}"

echo -e "${YELLOW}Updating Alexandria Server...${NC}"
echo ""

cd "$INSTALL_DIR" || { echo -e "${RED}Error: Installation directory not found at $INSTALL_DIR${NC}"; exit 1; }

# Detect compose command
if docker compose version &> /dev/null; then
    COMPOSE_CMD="docker compose"
else
    COMPOSE_CMD="docker-compose"
fi

# Backup current compose file
cp docker-compose.yml docker-compose.yml.backup

# Download latest compose file
echo -e "${YELLOW}Downloading latest configuration...${NC}"
curl -fsSL "https://raw.githubusercontent.com/stoutc24/alexandria-server/main/docker-compose.yml" -o docker-compose.yml
echo -e "${GREEN}✓${NC} Configuration updated"

# Pull latest image and restart
echo -e "${YELLOW}Pulling latest image...${NC}"
$COMPOSE_CMD pull

echo -e "${YELLOW}Restarting server...${NC}"
# The image just pulled; this directory holds no source to build from.
$COMPOSE_CMD up -d --no-build

echo ""
echo -e "${GREEN}✓ Alexandria Server updated successfully!${NC}"
echo ""
echo "Run '$COMPOSE_CMD logs -f' to view logs"
