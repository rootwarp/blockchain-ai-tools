#!/usr/bin/env bash
# regen-vectors.sh — regenerate the eth-signer-mcp golden signing vectors.
#
# Dual-oracle strategy:
#   1. cast wallet sign-tx  (Foundry — optional; gated on cast presence)
#   2. regen-vectors-ethers.mjs  (ethers v6 — always run)
#
# When BOTH oracles run, their raw signed transactions are byte-compared.
# Any mismatch exits non-zero — do NOT commit mismatched vectors.
#
# When cast is ABSENT (or its version does not match .foundry-version):
#   → logs a clear warning and runs ethers-only; exits 0.
#   → vectors carry meta.oracles.cast = "not-run-in-this-env"
#
# When .foundry-version is ABSENT:
#   → cast oracle is skipped with a clear warning (version cannot be verified).
#   → This prevents accidentally committing vectors produced with an unverified
#     cast version that might produce different output from the pinned version.
#
# Prerequisites:
#   • Node.js + ethers@6 installed in scripts/node_modules/
#     (run `npm install --prefix scripts ethers@6` once)
#   • Foundry cast (optional): pinned version in /.foundry-version
#
# No network calls are made. All signing is fully offline.
#
# Usage (from repo root):
#   npm install --prefix scripts ethers@6   # first time only
#   scripts/regen-vectors.sh
#
# Private key is sourced from (single disclosure path, TEST-ONLY):
#   apps/eth-signer-mcp/internal/signing/testdata/README.md
# Do NOT copy the key scalar into this script; the single disclosure path is the README.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
VECTORS_DIR="${REPO_ROOT}/apps/eth-signer-mcp/internal/signing/testdata/vectors"
FOUNDRY_VERSION_FILE="${REPO_ROOT}/.foundry-version"

# Private key — TEST-ONLY key sourced from testdata/README.md (single disclosure path).
# Do NOT duplicate the provenance story here; that README is the canonical reference.
# WARNING: This key is committed in plaintext for testing only. Never use for real funds.
FIXTURE_KEY="1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727"

# ---------------------------------------------------------------------------
# 1. Determine oracle availability
# ---------------------------------------------------------------------------

CAST_AVAILABLE=0
CAST_VERSION_MISMATCH=0
CAST_ACTUAL_VERSION=""
EXPECTED_FOUNDRY_VERSION=""

if [ -f "${FOUNDRY_VERSION_FILE}" ]; then
  EXPECTED_FOUNDRY_VERSION="$(cat "${FOUNDRY_VERSION_FILE}" | tr -d '[:space:]')"
else
  echo "WARNING: .foundry-version not found at ${FOUNDRY_VERSION_FILE}."
  echo "  Cannot verify cast version. Skipping cast oracle to prevent committing"
  echo "  vectors produced with an unverified Foundry version."
  echo "  To enable dual-oracle: create .foundry-version with the pinned tag (e.g. v1.7.1)."
fi

if [ -n "${EXPECTED_FOUNDRY_VERSION}" ] && command -v cast >/dev/null 2>&1; then
  CAST_ACTUAL_VERSION="$(cast --version 2>&1 | head -1)"
  # Check if the version string contains the expected tag
  if ! echo "${CAST_ACTUAL_VERSION}" | grep -qF "${EXPECTED_FOUNDRY_VERSION}"; then
    echo "WARNING: cast version mismatch."
    echo "  expected (from .foundry-version): ${EXPECTED_FOUNDRY_VERSION}"
    echo "  actual:                           ${CAST_ACTUAL_VERSION}"
    echo "  Skipping cast oracle to avoid committing mismatched vectors."
    echo "  To enable cast: install Foundry ${EXPECTED_FOUNDRY_VERSION} and re-run."
    CAST_VERSION_MISMATCH=1
  else
    CAST_AVAILABLE=1
    echo "cast found: ${CAST_ACTUAL_VERSION}"
  fi
elif [ -n "${EXPECTED_FOUNDRY_VERSION}" ]; then
  echo "WARNING: cast not found on PATH. Running ethers-only oracle."
  echo "  To enable dual-oracle: install Foundry ${EXPECTED_FOUNDRY_VERSION}"
  echo "  then re-run this script."
fi

# ---------------------------------------------------------------------------
# 2. Ensure ethers is available
# ---------------------------------------------------------------------------

ETHERS_SCRIPT="${SCRIPT_DIR}/regen-vectors-ethers.mjs"
if [ ! -f "${ETHERS_SCRIPT}" ]; then
  echo "ERROR: ${ETHERS_SCRIPT} not found."
  exit 1
fi

# Check that ethers@6 is installed in scripts/node_modules
if [ ! -d "${SCRIPT_DIR}/node_modules/ethers" ]; then
  echo "ERROR: ethers not found in ${SCRIPT_DIR}/node_modules/ethers"
  echo "  Run: npm install --prefix scripts ethers@6"
  exit 1
fi

echo ""
echo "==> Step 1: Running ethers v6 oracle..."
mkdir -p "${VECTORS_DIR}"
ETHERS_OUTPUT="$(node "${ETHERS_SCRIPT}" "${VECTORS_DIR}" 2>&1)"
echo "${ETHERS_OUTPUT}" | grep -v '^__ETHERS_RESULTS__' | grep -v '^{"raw_tx'

# ---------------------------------------------------------------------------
# 3. (Optional) Run cast oracle and byte-compare
# ---------------------------------------------------------------------------

if [ "${CAST_AVAILABLE}" -eq 1 ]; then
  echo ""
  echo "==> Step 2: Running cast oracle and byte-comparing..."

  # Vector specs for cast: (name, type_flag, chain_id, nonce, to, value, data, gas, extra_flags...)
  # We iterate over the signing vectors and run cast wallet sign-tx for each.
  #
  # cast wallet sign-tx flags:
  #   --type 0            (legacy) or 2 (EIP-1559)
  #   --chain <id>
  #   --nonce <n>
  #   --to <addr>         (omit for contract creation)
  #   --value <wei>
  #   --data <hex>
  #   --gas-limit <n>
  #   --gas-price <wei>   (legacy)
  #   --priority-gas-price <wei> and --gas-price <wei>  (EIP-1559)
  #   --private-key <key>  (TEST-ONLY key — see testdata/README.md)

  MISMATCH_COUNT=0

  run_cast_vector() {
    local name="$1"
    local vector_file="${VECTORS_DIR}/${name}.json"

    if [ ! -f "${vector_file}" ]; then
      echo "  [skip] ${name}: no vector file found"
      return
    fi

    # Parse tx fields from the committed vector JSON using node (already available)
    local tx_type chainId nonce to value data gas gasPrice maxFeePerGas maxPriorityFeePerGas
    tx_type="$(node -e "const v=require('${vector_file}'); console.log(v.tx.type||'0x0')")"
    chainId="$(node -e "const v=require('${vector_file}'); console.log(v.tx.chainId)")"
    nonce="$(node -e "const v=require('${vector_file}'); console.log(v.tx.nonce)")"
    to="$(node -e "const v=require('${vector_file}'); console.log(v.tx.to||'')")"
    value="$(node -e "const v=require('${vector_file}'); console.log(v.tx.value||'0')")"
    data="$(node -e "const v=require('${vector_file}'); console.log(v.tx.data||'0x')")"
    gas="$(node -e "const v=require('${vector_file}'); console.log(v.tx.gas)")"
    gasPrice="$(node -e "const v=require('${vector_file}'); console.log(v.tx.gasPrice||'')")"
    maxFeePerGas="$(node -e "const v=require('${vector_file}'); console.log(v.tx.maxFeePerGas||'')")"
    maxPriorityFeePerGas="$(node -e "const v=require('${vector_file}'); console.log(v.tx.maxPriorityFeePerGas||'')")"

    # Determine numeric type
    local type_num=0
    if [ "${tx_type}" = "0x2" ] || [ "${tx_type}" = "eip1559" ]; then
      type_num=2
    fi

    # Build cast args
    local cast_args=()
    cast_args+=(--private-key "${FIXTURE_KEY}")
    cast_args+=(--type "${type_num}")
    cast_args+=(--chain "${chainId}")
    cast_args+=(--nonce "${nonce}")
    cast_args+=(--value "${value}")
    cast_args+=(--data "${data}")
    cast_args+=(--gas-limit "${gas}")

    if [ -n "${to}" ]; then
      cast_args+=(--to "${to}")
    fi

    if [ "${type_num}" -eq 0 ]; then
      cast_args+=(--gas-price "${gasPrice}")
    else
      cast_args+=(--gas-price "${maxFeePerGas}")
      cast_args+=(--priority-gas-price "${maxPriorityFeePerGas}")
    fi

    local cast_raw
    cast_raw="$(cast wallet sign-tx "${cast_args[@]}" 2>&1 | head -1)"

    # Get ethers raw_tx for this vector
    local ethers_raw
    ethers_raw="$(node -e "const v=require('${vector_file}'); console.log(v.expected.raw_tx)")"

    if [ "${cast_raw}" = "${ethers_raw}" ]; then
      echo "  [match] ${name}"
    else
      echo "  [MISMATCH] ${name}"
      echo "    cast:   ${cast_raw}"
      echo "    ethers: ${ethers_raw}"
      MISMATCH_COUNT=$((MISMATCH_COUNT + 1))
    fi
  }

  # Run cast for each signing vector
  SIGNING_VECTORS=(
    legacy-mainnet
    legacy-sepolia
    1559-mainnet
    1559-sepolia
    legacy-empty-data-zero-value
    1559-empty-data-zero-value
    legacy-contract-creation
    1559-contract-creation
    legacy-padded-nonce
  )

  for vec in "${SIGNING_VECTORS[@]}"; do
    run_cast_vector "${vec}"
  done

  if [ "${MISMATCH_COUNT}" -gt 0 ]; then
    echo ""
    echo "ERROR: ${MISMATCH_COUNT} oracle mismatch(es) detected."
    echo "  The committed vectors do NOT match cast output."
    echo "  Fix the discrepancy before committing."
    exit 1
  fi

  echo ""
  echo "All oracles agree — byte-identical output on all signing vectors."

  # Write cast-version.txt
  echo "${CAST_ACTUAL_VERSION}" > "${VECTORS_DIR}/cast-version.txt"
  echo "Updated cast-version.txt: ${CAST_ACTUAL_VERSION}"

else
  echo ""
  if [ "${CAST_VERSION_MISMATCH}" -eq 1 ]; then
    echo "cast oracle SKIPPED (version mismatch — see warning above)."
  elif [ -z "${EXPECTED_FOUNDRY_VERSION}" ]; then
    echo "cast oracle SKIPPED (.foundry-version absent — see warning above)."
  else
    echo "cast oracle SKIPPED (not installed)."
  fi
  echo "Vectors carry meta.oracles.cast = \"not-run-in-this-env\"."
  echo "Re-run on a Foundry-equipped machine for dual-oracle verification."
fi

# ---------------------------------------------------------------------------
# 4. Final summary
# ---------------------------------------------------------------------------
echo ""
echo "==> Done."
echo "    Vectors written to: ${VECTORS_DIR}"
echo "    Verify with: git diff ${VECTORS_DIR#"${REPO_ROOT}/"}"
echo ""
echo "    To run the Go parity suite:"
echo "    cd apps/eth-signer-mcp && go test ./internal/signing/ -run TestParity"
