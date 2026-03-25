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
DEFAULT_HTTP_PORT="8888"

# Installation directory
INSTALL_DIR="$HOME_DIR/.local/bin"

validate_port() {
    local port="$1"

    if [[ ! "$port" =~ ^[0-9]+$ ]]; then
        return 1
    fi

    if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
        return 1
    fi

    return 0
}

# Check if already installed
IS_INSTALLED=false
if [ -f "$INSTALL_DIR/tailhopper" ]; then
    IS_INSTALLED=true
fi

HTTP_PORT="$DEFAULT_HTTP_PORT"

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

while true; do
    read -r -p "Dashboard port [$DEFAULT_HTTP_PORT] (press Enter to use default): " INPUT_PORT
    HTTP_PORT="${INPUT_PORT:-$DEFAULT_HTTP_PORT}"

    if ! validate_port "$HTTP_PORT"; then
        echo -e "${RED}Error: Please enter a valid port number between 1 and 65535.${NC}"
        continue
    fi

    break
done

echo -e "${YELLOW}Dashboard port:${NC} $HTTP_PORT"
echo ""

# Create directories
echo -e "${YELLOW}Creating directories...${NC}"
mkdir -p "$INSTALL_DIR"
mkdir -p "$HOME_DIR/Library/Application Support/Tailhopper"
mkdir -p "$HOME_DIR/Library/Logs/Tailhopper"
echo -e "${GREEN}✓ Directories created${NC}"
echo ""

# Build the binary
echo -e "${YELLOW}Building tailhopper binary...${NC}"
cd "$PROJECT_ROOT"
go build -o "$INSTALL_DIR/tailhopper" ./cmd/tailhopper
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Binary built to $INSTALL_DIR/tailhopper${NC}"
else
    echo -e "${RED}Error: Failed to build binary${NC}"
    exit 1
fi
echo ""

# Copy uninstall script
echo -e "${YELLOW}Copying uninstall script...${NC}"
cp "$SCRIPT_DIR/uninstall.sh" "$INSTALL_DIR/tailhopper-uninstall"
chmod +x "$INSTALL_DIR/tailhopper-uninstall"
echo -e "${GREEN}✓ Uninstall script copied to: ${YELLOW}$INSTALL_DIR/tailhopper-uninstall${NC}"
echo ""

# Create logs viewing script
echo -e "${YELLOW}Creating logs viewing script...${NC}"
cat > "$INSTALL_DIR/tailhopper-logs" <<'LOGS_SCRIPT'
#!/bin/bash

# Colors for output
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

LOG_FILE="$HOME/Library/Logs/Tailhopper/tailhopper.log"

if [ ! -f "$LOG_FILE" ]; then
    echo -e "${YELLOW}Log file not found: $LOG_FILE${NC}"
    exit 1
fi

echo -e "${YELLOW}Viewing TailHopper logs (Ctrl+C to exit)...${NC}"
echo ""
tail -f "$LOG_FILE"
LOGS_SCRIPT
chmod +x "$INSTALL_DIR/tailhopper-logs"
echo -e "${GREEN}✓ Logs script created at: ${YELLOW}$INSTALL_DIR/tailhopper-logs${NC}"
echo ""

# Generate plist file
echo -e "${YELLOW}Generating launchd plist...${NC}"
LAUNCH_AGENTS_DIR="$HOME_DIR/Library/LaunchAgents"
mkdir -p "$LAUNCH_AGENTS_DIR"

# Read the template plist and substitute variables
sed \
    -e "s|{{HOME}}|$HOME_DIR|g" \
    -e "s|{{HTTP_PORT}}|$HTTP_PORT|g" \
    "$SCRIPT_DIR/com.tailhopper.plist" > "$PLIST_FILE"

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
echo -e "${YELLOW}Access the dashboard at: http://localhost:${HTTP_PORT}${NC}"
echo ""
echo -e "${YELLOW}Useful commands:${NC}"
echo "  View logs:      tailhopper-logs"
echo "  Service status: launchctl list | grep tailhopper"
echo "  Restart:        launchctl unload $PLIST_FILE && launchctl load $PLIST_FILE"
echo "  Stop:           launchctl unload $PLIST_FILE"
echo "  Uninstall:      tailhopper-uninstall"
