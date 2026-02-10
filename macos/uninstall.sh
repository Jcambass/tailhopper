#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}TailHopper Uninstall Script${NC}"
echo ""

# Get home directory
HOME_DIR="$HOME"
INSTALL_DIR="$HOME_DIR/.local/bin"
PLIST_FILE="$HOME_DIR/Library/LaunchAgents/com.tailhopper.plist"
BINARY_PATH="$INSTALL_DIR/tailhopper"
UNINSTALL_SCRIPT="$INSTALL_DIR/tailhopper-uninstall"
LOGS_SCRIPT="$INSTALL_DIR/tailhopper-logs"
STATE_DIR="$HOME_DIR/Library/Application Support/Tailhopper"
LOG_DIR="$HOME_DIR/Library/Logs/Tailhopper"

# Unload the service if running
if launchctl list com.tailhopper &>/dev/null; then
    echo -e "${YELLOW}Unloading service...${NC}"
    launchctl unload "$PLIST_FILE" || true
    echo -e "${GREEN}✓ Service unloaded${NC}"
fi
echo ""

# Remove plist
if [ -f "$PLIST_FILE" ]; then
    echo -e "${YELLOW}Removing plist...${NC}"
    rm "$PLIST_FILE"
    echo -e "${GREEN}✓ Plist removed${NC}"
fi
echo ""

# Remove binary
if [ -f "$BINARY_PATH" ]; then
    echo -e "${YELLOW}Removing binary...${NC}"
    rm "$BINARY_PATH"
    echo -e "${GREEN}✓ Binary removed${NC}"
fi
echo ""

# Remove uninstall script
if [ -f "$UNINSTALL_SCRIPT" ]; then
    echo -e "${YELLOW}Removing uninstall script...${NC}"
    rm "$UNINSTALL_SCRIPT"
    echo -e "${GREEN}✓ Uninstall script removed${NC}"
fi
echo ""

# Remove logs script
if [ -f "$LOGS_SCRIPT" ]; then
    echo -e "${YELLOW}Removing logs script...${NC}"
    rm "$LOGS_SCRIPT"
    echo -e "${GREEN}✓ Logs script removed${NC}"
fi
echo ""

# Ask about state directory
if [ -d "$STATE_DIR" ]; then
    read -p "Remove state and configuration files ($STATE_DIR)? [y/N]: " -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        rm -rf "$STATE_DIR"
        echo -e "${GREEN}✓ State and configuration files removed${NC}"
    else
        echo -e "${YELLOW}State and configuration files preserved${NC}"
    fi
    echo ""
fi

# Ask about log directory
if [ -d "$LOG_DIR" ]; then
    read -p "Remove log directory ($LOG_DIR)? [y/N]: " -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        rm -rf "$LOG_DIR"
        echo -e "${GREEN}✓ Log directory removed${NC}"
    else
        echo -e "${YELLOW}Log directory preserved${NC}"
    fi
    echo ""
fi

echo -e "${GREEN}Uninstallation complete!${NC}"
