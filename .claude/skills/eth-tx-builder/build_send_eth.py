#!/usr/bin/env python3
"""Build a ready-to-sign EIP-1559 send-ETH TxRequest JSON for eth-signer-mcp.

Stdlib only. Queries a hardcoded public RPC for nonce + fees, converts the
gwei amount to wei, and prints the sign_transaction request JSON to stdout.
This script never signs.
"""

import re

# network -> (chainId, rpc_url)
NETWORKS = {
    "mainnet": (1, "https://ethereum-rpc.publicnode.com"),
    "hoodi": (560048, "https://ethereum-hoodi-rpc.publicnode.com"),
}


def network_config(network):
    """Return (chain_id, rpc_url) for a network name, or raise ValueError."""
    try:
        return NETWORKS[network]
    except KeyError:
        raise ValueError(
            "unknown network %r; expected one of %s" % (network, sorted(NETWORKS))
        )


def gwei_to_wei(amount_gwei):
    """Convert an integer gwei amount to wei. Raises ValueError if negative."""
    if amount_gwei < 0:
        raise ValueError("amount-gwei must be non-negative")
    return amount_gwei * 1_000_000_000


_ADDR_RE = re.compile(r"^0x[0-9a-fA-F]{40}$")


def validate_hex_address(addr):
    """Return addr unchanged if it is 0x + 40 hex chars; else raise ValueError.

    Format check only — EIP-55 checksum is enforced by the signer.
    """
    if not isinstance(addr, str) or not _ADDR_RE.match(addr):
        raise ValueError(
            "malformed address (expected 0x + 40 hex chars): %r" % (addr,)
        )
    return addr
