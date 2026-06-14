#!/usr/bin/env python3
"""Build a ready-to-sign EIP-1559 ERC-20 TxRequest JSON for eth-signer-mcp.

Stdlib only. Supports three ERC-20 operations: transfer, approve,
transferFrom. Queries a public RPC for token metadata (decimals, symbol),
calldata-encodes the op, estimates gas, fetches nonce and fees, and prints
the sign_transaction request JSON to stdout. This script never signs.

Imported symbols from build_send_eth (_core contract):
    NETWORKS            — dict of network name -> (chain_id, rpc_url)
    network_config      — fn(network) -> (chain_id, url) or raise ValueError
    rpc_call            — fn(url, method, params) -> result or raise RPCError
    validate_hex_address — fn(addr) -> addr or raise ValueError
    parse_hex_int       — fn(s) -> int or raise ValueError
    compute_max_fee     — fn(base_fee, tip) -> int
    fetch_nonce         — fn(rpc, url, sender) -> int
    fetch_base_fee      — fn(rpc, url) -> int
    fetch_tip           — fn(rpc, url) -> int
    RPCError            — exception class for JSON-RPC transport failures

If any of these disappear from build_send_eth, this module fails at import
with a clear AttributeError, caught by CI and attributable to the v1 change.
"""

import argparse
import json
import sys

import build_send_eth as _core

# === Layer 1: abi_codec ===

# === end Layer 1: abi_codec ===

# === Layer 1: amount_codec ===

# === end Layer 1: amount_codec ===

# === Layer 2: contract_reads ===

# === end Layer 2: contract_reads ===

# === Layer 2: gas_estimator ===

# === end Layer 2: gas_estimator ===

# === Layer 2: summary ===

# === end Layer 2: summary ===

# === Layer 3: tx_assembly ===

# === end Layer 3: tx_assembly ===

# === Layer 4: cli_dispatch ===


def main(argv=None):
    """Stub entry point — returns 0."""
    return 0

# === end Layer 4: cli_dispatch ===


if __name__ == "__main__":
    sys.exit(main())
