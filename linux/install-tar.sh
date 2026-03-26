#!/usr/bin/env bash

# NOTE: This script is bundled in each release archive and is versioned with
# that release, so behavior here may evolve between releases.
#
# RISK: putting cross-version migration/cleanup logic in this file can break
# upgrades from older installs. Keep cross-version compatibility and migration
# policy in linux/install.sh (bootstrap), and keep this script focused on
# installing files from the current archive.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCHIVE_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BIN_SRC="${ARCHIVE_ROOT}/tailhopper"
SERVICE_SRC="${ARCHIVE_ROOT}/tailhopper.service"

if [[ ! -f "${BIN_SRC}" || ! -f "${SERVICE_SRC}" ]]; then
  echo "Expected tailhopper and tailhopper.service next to this script." >&2
  echo "Run this script from extracted release contents." >&2
  exit 1
fi

mkdir -p "${HOME}/.local/bin" "${HOME}/.local/share/tailhopper" "${HOME}/.config/systemd/user"

install -m 0755 "${BIN_SRC}" "${HOME}/.local/bin/tailhopper"
install -m 0644 "${SERVICE_SRC}" "${HOME}/.config/systemd/user/tailhopper.service"

if command -v systemctl >/dev/null 2>&1; then
  systemctl --user daemon-reload
  systemctl --user enable --now tailhopper
else
  echo "systemctl not found; service file installed but not enabled." >&2
fi

echo "Installed tailhopper to ${HOME}/.local/bin/tailhopper"
