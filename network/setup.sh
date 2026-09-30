#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# WAPSI network bootstrap: deploys the WAPSI chaincode onto a local
#
#   1. Drunix test network   (default; github.com/npci/drunix)
#   2. Hyperledger Fabric test-network fallback (same chaincode — Drunix is
#      backwards compatible with Fabric v2.5.x)
#
# Usage:
#   ./setup.sh                 # Drunix test network
#   FABRIC_FALLBACK=1 ./setup.sh
#
# Requires: git, docker (running), bash v4+. On Windows use WSL2.
# ---------------------------------------------------------------------------
set -euo pipefail

CHANNEL="${CHANNEL:-wapsich}"
CC_NAME="${CC_NAME:-wapsi}"
CC_VERSION="${CC_VERSION:-1.0}"
CC_PATH="$(cd "$(dirname "$0")/../chaincode" && pwd)"
WORKDIR="${WORKDIR:-$HOME/.wapsi}"
mkdir -p "$WORKDIR"

log() { printf "\n\033[1;36m[wapsi]\033[0m %s\n" "$*"; }

if ! docker info >/dev/null 2>&1; then
  log "ERROR: Docker is not running. Start Docker Desktop and retry."
  exit 1
fi

fetch_drunix() {
  if [ ! -d "$WORKDIR/drunix" ]; then
    log "Cloning npci/drunix ..."
    git clone --depth 1 https://github.com/npci/drunix.git "$WORKDIR/drunix"
  fi
  # The current Drunix installer places binaries under drunix-network/build,
  # while test-network/network.sh resolves them from the repository root.
  # Bridge that layout difference explicitly.
  if [ ! -e "$WORKDIR/drunix/build" ] && [ -d "$WORKDIR/drunix/drunix-network/build" ]; then
    ln -s "$WORKDIR/drunix/drunix-network/build" "$WORKDIR/drunix/build"
  fi
}

fetch_fabric() {
  if [ ! -d "$WORKDIR/fabric-samples" ]; then
    log "Cloning hyperledger/fabric-samples (fallback) ..."
    git clone --depth 1 https://github.com/hyperledger/fabric-samples.git "$WORKDIR/fabric-samples"
    (cd "$WORKDIR/fabric-samples" && ./install-fabric.sh docker fabric-binary 2.5 >/dev/null 2>&1 || true)
  fi
}

up_drunix() {
  fetch_drunix
  local tn="$WORKDIR/drunix/drunix-network/test-network"
  log "Installing Drunix prerequisites (binaries + images) ..."
  (cd "$tn" && ./network.sh prereq)
  log "Starting Drunix network and creating channel $CHANNEL ..."
  # -ca: cryptogen/CA material; -s yugabyte: SQL state store (Drunix feature)
  (cd "$tn" && ./network.sh up createChannel -c "$CHANNEL" -ca -s yugabyte)
}

up_fabric() {
  fetch_fabric
  local tn="$WORKDIR/fabric-samples/test-network"
  log "Starting Fabric test-network and creating channel $CHANNEL ..."
  (cd "$tn" && ./network.sh up createChannel -c "$CHANNEL" -ca)
}

deploy_cc() {
  local tn="$1"
  log "Deploying chaincode '$CC_NAME' v$CC_VERSION from $CC_PATH ..."
  (cd "$tn" && ./network.sh deployCC -ccn "$CC_NAME" -ccp "$CC_PATH" -ccl go -ccv "$CC_VERSION" -c "$CHANNEL")
}

main() {
  local tn
  if [ "${FABRIC_FALLBACK:-0}" = "1" ]; then
    log "Fabric fallback mode (chaincode unchanged; note this in the README)"
    up_fabric
    tn="$WORKDIR/fabric-samples/test-network"
  else
    if up_drunix; then
      tn="$WORKDIR/drunix/drunix-network/test-network"
    else
      log "Drunix bring-up failed — falling back to Fabric test-network"
      up_fabric
      tn="$WORKDIR/fabric-samples/test-network"
    fi
  fi
  deploy_cc "$tn"
  log "Done. Channel: $CHANNEL · Chaincode: $CC_NAME"
  log "WAPSI org layout: Org1MSP (victim bank) · Org2MSP (HDFC) · Org3MSP (Axis) · Org4MSP (LEA)"
  log "Start the gateway in fabric mode:  cd ../gateway && WAPSI_MODE=fabric npm start"
}

main "$@"
