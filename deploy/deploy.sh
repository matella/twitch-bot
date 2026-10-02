#!/bin/bash
# Twitch Bot Deployment Script for matelab

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
REPO_URL="https://github.com/matella/twitch-bot.git"
DEPLOY_DIR="/opt/twitch-bot"
DATA_DIR="/var/lib/twitch-bot"
SERVICE_NAME="twitch-bot"
ENV_FILE="$DEPLOY_DIR/.env"

echo -e "${GREEN}=== Twitch Bot Deployment Script ===${NC}"
echo ""

# Check if running as root
if [[ $EUID -ne 0 ]]; then
   echo -e "${RED}This script must be run as root${NC}"
   exit 1
fi

# Function to print status
print_status() {
    echo -e "${YELLOW}>>> $1${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

# 1. Create user if it doesn't exist
print_status "Setting up twitch-bot user..."
if id "twitch-bot" &>/dev/null; then
    print_success "User 'twitch-bot' already exists"
else
    useradd -r -s /bin/false -d /var/lib/twitch-bot twitch-bot
    print_success "Created user 'twitch-bot'"
fi

# 2. Create directories
print_status "Creating deployment directories..."
mkdir -p "$DEPLOY_DIR"
mkdir -p "$DATA_DIR"
chown -R twitch-bot:twitch-bot "$DATA_DIR"
chmod 755 "$DEPLOY_DIR"
chmod 755 "$DATA_DIR"
print_success "Directories created"

# 3. Clone or update repository
print_status "Cloning/updating repository..."
if [ -d "$DEPLOY_DIR/.git" ]; then
    cd "$DEPLOY_DIR"
    git fetch origin
    git reset --hard origin/main
    print_success "Repository updated"
else
    rm -rf "$DEPLOY_DIR"
    git clone "$REPO_URL" "$DEPLOY_DIR"
    cd "$DEPLOY_DIR"
    print_success "Repository cloned"
fi

# 4. Check for .env file
print_status "Checking configuration..."
if [ ! -f "$ENV_FILE" ]; then
    print_error "Configuration file not found: $ENV_FILE"
    echo ""
    echo "Please create your configuration file:"
    echo ""
    cat deploy/.env.example
    echo ""
    echo "Save it as: $ENV_FILE"
    exit 1
fi
print_success "Configuration file found"

# 5. Build the binary
print_status "Building the binary..."
cd "$DEPLOY_DIR"
if command -v docker &> /dev/null; then
    print_status "Docker detected, building with Docker..."
    docker build -t twitch-bot:latest -f deploy/Dockerfile .
    print_success "Docker image built"
else
    print_status "Docker not found, building with Go..."
    if ! command -v go &> /dev/null; then
        print_error "Go is not installed. Please install Go 1.21+ or Docker"
        exit 1
    fi
    go build -o "$DEPLOY_DIR/twitch-bot" ./cmd
    print_success "Binary built"
fi

# 6. Set permissions
print_status "Setting permissions..."
if [ -f "$DEPLOY_DIR/twitch-bot" ]; then
    chmod 755 "$DEPLOY_DIR/twitch-bot"
fi
print_success "Permissions set"

# 7. Install systemd service
print_status "Installing systemd service..."
cp "$DEPLOY_DIR/deploy/twitch-bot.service" "/etc/systemd/system/$SERVICE_NAME.service"

# Load environment from .env file into systemd
sed -i "1a EnvironmentFile=$ENV_FILE" "/etc/systemd/system/$SERVICE_NAME.service"

systemctl daemon-reload
print_success "Systemd service installed"

# 8. Enable and start service
print_status "Starting service..."
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
print_success "Service started and enabled"

# 9. Wait for service to be ready
print_status "Waiting for service to be ready..."
sleep 5

if systemctl is-active --quiet "$SERVICE_NAME"; then
    print_success "Service is running"
else
    print_error "Service failed to start"
    echo "Check logs with: journalctl -u $SERVICE_NAME -n 50"
    exit 1
fi

# 10. Check health
print_status "Checking health endpoint..."
if curl -f http://localhost:9090/api/health &>/dev/null; then
    print_success "Health check passed"
else
    echo -e "${YELLOW}Health check failed (service might still be starting)${NC}"
    echo "Check status with: systemctl status $SERVICE_NAME"
fi

echo ""
echo -e "${GREEN}=== Deployment Complete ===${NC}"
echo ""
echo "Service Information:"
echo "  Name: $SERVICE_NAME"
echo "  Status: $(systemctl is-active $SERVICE_NAME)"
echo "  Admin Dashboard: http://localhost:9090"
echo ""
echo "Useful Commands:"
echo "  View logs: journalctl -u $SERVICE_NAME -f"
echo "  Check status: systemctl status $SERVICE_NAME"
echo "  Restart: systemctl restart $SERVICE_NAME"
echo "  Stop: systemctl stop $SERVICE_NAME"
echo ""
