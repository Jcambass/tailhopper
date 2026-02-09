#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}TailHopper Install Script${NC}"
echo ""

# Get the script directory and navigate to project root
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# Get home directory
HOME_DIR="$HOME"
PLIST_FILE="$HOME_DIR/Library/LaunchAgents/com.tailhopper.plist"

# Check if already installed
IS_INSTALLED=false
if [ -f "$HOME_DIR/bin/tailhopper" ]; then
    IS_INSTALLED=true
fi

if [ "$IS_INSTALLED" = true ]; then
    echo -e "${YELLOW}⚠ Existing installation found. Updating...${NC}"
    echo ""
    
    # Unload service before updating
    if launchctl list com.tailhopper &>/dev/null; then
        echo -e "${YELLOW}Stopping existing service...${NC}"
        launchctl unload "$PLIST_FILE" || true
    fi
    echo ""
else
    echo -e "${YELLOW}Fresh installation${NC}"
    echo ""
fi

# Create directories
echo -e "${YELLOW}Creating directories...${NC}"
mkdir -p "$HOME_DIR/bin"
mkdir -p "$HOME_DIR/Library/Application Support/Tailhopper"
mkdir -p "$HOME_DIR/Library/Logs/Tailhopper"
echo -e "${GREEN}✓ Directories created${NC}"
echo ""

# Build the binary
echo -e "${YELLOW}Building tailhopper binary...${NC}"
cd "$PROJECT_ROOT"
go build -o "$HOME_DIR/bin/tailhopper" ./cmd/tailhopper
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Binary built successfully${NC}"
else
    echo -e "${RED}Error: Failed to build binary${NC}"
    exit 1
fi
echo ""

# Copy uninstall script
echo -e "${YELLOW}Copying uninstall script...${NC}"
cp "$SCRIPT_DIR/uninstall.sh" "$HOME_DIR/Library/Application Support/Tailhopper/uninstall.sh"
chmod +x "$HOME_DIR/Library/Application Support/Tailhopper/uninstall.sh"
echo -e "${GREEN}✓ Uninstall script copied to: ${YELLOW}$HOME_DIR/Library/Application Support/Tailhopper/uninstall.sh${NC}"
echo ""

# Generate plist file
echo -e "${YELLOW}Generating launchd plist...${NC}"
LAUNCH_AGENTS_DIR="$HOME_DIR/Library/LaunchAgents"
mkdir -p "$LAUNCH_AGENTS_DIR"

# Read the template plist and substitute variables
sed "s|{{HOME}}|$HOME_DIR|g" "$SCRIPT_DIR/com.tailhopper.plist" > "$PLIST_FILE"

if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Plist installed to: ${YELLOW}$PLIST_FILE${NC}"
else
    echo -e "${RED}Error: Failed to create plist${NC}"
    exit 1
fi
echo ""

# Load the service
echo -e "${YELLOW}Loading service...${NC}"
launchctl load "$PLIST_FILE"

if launchctl list com.tailhopper &>/dev/null; then
    echo -e "${GREEN}✓ Service loaded successfully${NC}"
else
    echo -e "${RED}Error: Failed to load service${NC}"
    exit 1
fi
echo ""

if [ "$IS_INSTALLED" = true ]; then
    echo -e "${GREEN}Update complete!${NC}"
else
    echo -e "${GREEN}Installation complete!${NC}"
fi
echo ""
echo -e "${YELLOW}Useful commands:${NC}"
echo "  View logs:    tail -f $HOME_DIR/Library/Logs/Tailhopper/tailhopper.log"
echo "  Service status: launchctl list | grep tailhopper"
echo "  Restart:      launchctl unload $PLIST_FILE && launchctl load $PLIST_FILE"
echo "  Stop:         launchctl unload $PLIST_FILE"
echo "  Uninstall:    $HOME_DIR/Library/Application Support/Tailhopper/uninstall.sh"
echo ""
echo -e "${YELLOW}TailHopper is now running as a SOCKS5 proxy!${NC}"
echo "  UI:     http://localhost:8888"
echo "  SOCKS5:       127.0.0.1:1080"
