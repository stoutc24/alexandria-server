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

# A secrets key for Alexandria accounts: 32 random bytes, base64 as the
# README's `openssl rand -base64 32` (the server takes hex too).
generate_secrets_key() {
    if command -v openssl &> /dev/null; then
        openssl rand -base64 32
    else
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    fi
}

# Alexandria accounts keep people's service logins sealed with
# ALEXANDRIA_SECRETS_KEY. A server installed before accounts has none:
# add one, once, and print it. A key already set is never touched.
ensure_secrets_key() {
    [ -f .env ] || return 0
    if grep -Eq '^ALEXANDRIA_SECRETS_KEY=.+' .env; then
        return 0
    fi
    local key
    key=$(generate_secrets_key)
    cp .env .env.backup
    if grep -Eq '^ALEXANDRIA_SECRETS_KEY=$' .env; then
        awk -v k="$key" '{ if ($0 ~ /^ALEXANDRIA_SECRETS_KEY=$/) print "ALEXANDRIA_SECRETS_KEY=" k; else print }' \
            .env > .env.tmp && mv .env.tmp .env
    else
        {
            echo ""
            echo "# Secrets key for Alexandria accounts (added $(date))."
            echo "# KEEP A COPY SOMEWHERE SAFE, apart from your backups: it seals the"
            echo "# service logins accounts keep, which cannot be opened without it."
            echo "# Never the same as the admin token."
            echo "ALEXANDRIA_SECRETS_KEY=${key}"
        } >> .env
    fi
    chmod 600 .env
    echo -e "${GREEN}✓${NC} Added a secrets key for Alexandria accounts to .env."
    echo "    Copy it somewhere safe, apart from your backups:"
    echo -e "    ${key}"
}
ensure_secrets_key

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
