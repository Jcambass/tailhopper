#!/usr/bin/env bash

# NOTE: Keep this bootstrap script backward/forward compatible across releases.
# It may install old or new versions, so avoid assumptions about exact archive
# internals and keep extraction/discovery logic resilient.

set -euo pipefail

REPO="jcambass/tailhopper"
VERSION="${VERSION:-latest}"

arch_from_uname() {
  case "$(uname -m)" in
    x86_64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      echo "Unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

resolve_latest_tag() {
  local api_url tag
  api_url="https://api.github.com/repos/${REPO}/releases/latest"

  tag="$(curl -fsSL "${api_url}" | awk -F '"' '/"tag_name"/ {print $4; exit}')"

  if [[ -z "${tag}" ]]; then
    echo "Failed to resolve latest release tag for ${REPO}." >&2
    echo "No published release found. Use VERSION=vX.Y.Z for a specific release." >&2
    exit 1
  fi

  echo "${tag}"
}

if [[ "${VERSION}" == "latest" ]]; then
  VERSION="$(resolve_latest_tag)"
fi

ARCH="$(arch_from_uname)"
ARCHIVE="https://github.com/${REPO}/releases/download/${VERSION}/tailhopper_linux_${ARCH}.tar.gz"
TMPDIR="$(mktemp -d)"

cleanup() {
  rm -rf "${TMPDIR}"
}
trap cleanup EXIT

curl -fsSL "${ARCHIVE}" | tar xz -C "${TMPDIR}"

INSTALLER="$(find "${TMPDIR}" -type f -path '*/linux/install-tar.sh' | head -n1)"
if [[ -z "${INSTALLER}" ]]; then
  echo "Could not find linux/install-tar.sh in release archive." >&2
  exit 1
fi

bash "${INSTALLER}"
