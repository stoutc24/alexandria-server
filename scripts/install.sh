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

echo ""
echo -e "${BLUE}Alexandria Companion Server${NC}"
echo "A small helper for the Alexandria app, run on your own machine."
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

# Generate a secure random secret (64 hex characters, as the README's
# `openssl rand -hex 32`)
generate_secret() {
    if command -v openssl &> /dev/null; then
        openssl rand -hex 32
    else
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    fi
}

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
# ALEXANDRIA_SECRETS_KEY. An .env from before accounts has none: add one,
# once, and print it. A key already set is never touched.
ensure_secrets_key() {
    [ -f .env ] || return 0
    if grep -Eq '^ALEXANDRIA_SECRETS_KEY=.+' .env; then
        return 0
    fi
    local key
    key=$(generate_secrets_key)
    cp .env .env.backup
    chmod 600 .env.backup
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

# The published docker-compose.yml, without ever replacing one you changed.
#
# This script remembers the file it wrote (.compose-checksum). Next time:
#   - yours is still that file: it is replaced by the current one;
#   - yours differs (you edited it, or it was installed before the checksum
#     was kept): YOURS IS KEPT and used, and the current one is saved beside
#     it as docker-compose.yml.new. Replacing an edited file could point the
#     server at a different data volume, which starts it empty.
# Keep your own changes in docker-compose.override.yml, which Docker Compose
# reads by itself and no update touches.
COMPOSE_URL="https://raw.githubusercontent.com/stoutc24/alexandria-server/main/docker-compose.yml"
compose_checksum() { cksum < "$1" | awk '{print $1 "-" $2}'; }
fetch_compose() {
    curl -fsSL "$COMPOSE_URL" -o docker-compose.yml.new
    if [ ! -f docker-compose.yml ]; then
        mv docker-compose.yml.new docker-compose.yml
        compose_checksum docker-compose.yml > .compose-checksum
        echo -e "${GREEN}✓${NC} docker-compose.yml downloaded"
    elif cmp -s docker-compose.yml docker-compose.yml.new; then
        rm -f docker-compose.yml.new
        compose_checksum docker-compose.yml > .compose-checksum
        echo -e "${GREEN}✓${NC} docker-compose.yml is current"
    elif [ -f .compose-checksum ] && [ "$(compose_checksum docker-compose.yml)" = "$(cat .compose-checksum)" ]; then
        cp docker-compose.yml docker-compose.yml.backup
        mv docker-compose.yml.new docker-compose.yml
        compose_checksum docker-compose.yml > .compose-checksum
        echo -e "${GREEN}✓${NC} docker-compose.yml updated (the previous one is docker-compose.yml.backup)"
    else
        echo -e "${YELLOW}!${NC} Your docker-compose.yml differs from the current one, so it was KEPT"
        echo "    and is still the one in use. The current one is saved beside it as"
        echo "    docker-compose.yml.new. To move to it: put your own changes in"
        echo "    docker-compose.override.yml, then rename docker-compose.yml.new to"
        echo "    docker-compose.yml and run this again."
    fi
}

# This machine's address on the home network, for the admin page's link.
# Best effort: empty when it cannot be told.
lan_address() {
    if command -v hostname &> /dev/null && hostname -I &> /dev/null; then
        hostname -I | awk '{print $1}'
    elif command -v ipconfig &> /dev/null; then
        ipconfig getifaddr en0 2> /dev/null || ipconfig getifaddr en1 2> /dev/null || true
    fi
}

# Waits up to 30 seconds for the server to answer on this machine.
wait_until_healthy() {
    local port="$1" i
    for i in $(seq 1 30); do
        if curl -fsS -m 2 "http://localhost:${port}/health" &> /dev/null; then
            return 0
        fi
        sleep 1
    done
    return 1
}

# Main installation
main() {
    # The .env holds the admin token and the secrets key: no file made here
    # is readable by another user of this machine, from its first byte.
    umask 077

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

    # Download docker-compose.yml (never over one you changed: fetch_compose)
    echo -e "${YELLOW}Downloading configuration...${NC}"
    fetch_compose

    # Create .env file if it doesn't exist
    if [ ! -f .env ]; then
        echo -e "${YELLOW}Generating configuration...${NC}"
        ALEXANDRIA_API_TOKEN=$(generate_secret)
        ALEXANDRIA_SECRETS_KEY=$(generate_secrets_key)
        SERVER_NAME=$(hostname -s 2>/dev/null || hostname)
        cat > .env << EOF
# Alexandria Server Configuration
# Generated on $(date)

# Port on this machine. Keep 4000: the apps look for the server on port
# 4000 when they search your network.
PORT=4000

# Admin token - KEEP THIS SAFE. It sets up the admin login on the
# server's web page (http://<this machine>:4000/admin). Phones, TVs and
# watches never use it: they link with a one-time code instead.
# The server refuses to start without it (16 characters or more).
ALEXANDRIA_API_TOKEN=${ALEXANDRIA_API_TOKEN}

# Secrets key for Alexandria accounts - KEEP A COPY SOMEWHERE SAFE, apart
# from your backups. It seals the service logins accounts keep for the
# people you invite; without it they cannot be opened. Never the same as
# the admin token.
ALEXANDRIA_SECRETS_KEY=${ALEXANDRIA_SECRETS_KEY}

# The name devices see when they find this server on the network.
ALEXANDRIA_SERVER_NAME=${SERVER_NAME:-Alexandria Companion}

# Optional: the https address this server is reached at from outside,
# behind a reverse proxy (https://alexandria.example.com). Leave empty
# if unused.
ALEXANDRIA_PUBLIC_URL=

# Optional: the address that reverse proxy connects from, so sign-in and
# linking limits count each visitor separately. Leave empty if unused.
ALEXANDRIA_TRUSTED_PROXIES=

# Timezone
TZ=UTC
EOF
        chmod 600 .env
        echo -e "${GREEN}✓${NC} .env created with a new admin token (also saved in $INSTALL_DIR/.env):"
        echo -e "    ${ALEXANDRIA_API_TOKEN}"
        echo -e "${GREEN}✓${NC} and a secrets key for Alexandria accounts. Copy it somewhere safe,"
        echo "    apart from your backups:"
        echo -e "    ${ALEXANDRIA_SECRETS_KEY}"
    else
        echo -e "${YELLOW}!${NC} .env file already exists, keeping it"
        ensure_secrets_key
    fi
    echo ""

    # Pull the published image and start it. Never build here: this
    # directory holds only the compose file, not the source.
    echo -e "${YELLOW}Downloading the server image...${NC}"
    if ! $COMPOSE_CMD pull; then
        echo -e "${RED}Error: could not download ghcr.io/stoutc24/alexandria-server.${NC}"
        echo "Check this machine's internet connection, then run this script again."
        exit 1
    fi
    echo -e "${YELLOW}Starting Alexandria Server...${NC}"
    $COMPOSE_CMD up -d --no-build

    # The port on this machine (PORT in .env; 4000 unless changed).
    local port address
    port=$(grep -E '^PORT=[0-9]+$' .env | tail -1 | cut -d= -f2)
    port="${port:-4000}"
    address=$(lan_address)
    if [ -z "$address" ]; then
        address="<this machine's address>"
    fi

    echo ""
    if wait_until_healthy "$port"; then
        echo -e "${GREEN}======================================================${NC}"
        echo -e "${GREEN}  Alexandria Server is running.${NC}"
        echo -e "${GREEN}======================================================${NC}"
    else
        echo -e "${YELLOW}======================================================${NC}"
        echo -e "${YELLOW}  The server was started but has not answered yet.${NC}"
        echo -e "${YELLOW}  See why with:  cd $INSTALL_DIR && $COMPOSE_CMD logs${NC}"
        echo -e "${YELLOW}======================================================${NC}"
    fi
    echo ""
    echo "  Health:      http://localhost:${port}/health"
    echo "  Admin page:  http://${address}:${port}/admin"
    echo ""
    echo "  Next:"
    echo "  1. Open the admin page and sign in with the admin token above."
    echo "     Set an admin password and add the login to your"
    echo "     authenticator app; keep the recovery codes it shows."
    echo "  2. In Alexandria: Settings > Companion Server > Find my server"
    echo "     on this network > Get a link code. Approve the code on the"
    echo "     admin page. A TV shows its code the same way: approve it there."
    echo "  3. Optional: give someone one sign-in for every phone. On the"
    echo "     admin page, open a person's Sign-in & services, add their"
    echo "     servers, then Invite to sign in."
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
