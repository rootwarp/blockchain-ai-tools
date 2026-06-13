#!/usr/bin/env python3
"""RPC companion for eth-signer-mcp: query EOA balance and broadcast signed txs.

Stdlib only. Two subcommands:
  balance    eth_getBalance(address, "latest")
  broadcast  eth_sendRawTransaction(raw), optionally waiting for the receipt

This script only reads chain state and submits already-signed transactions; it
never signs and never builds transactions.
"""

import argparse
import json
import re
import sys
import time
import urllib.request

# network -> (chainId, rpc_url)
NETWORKS = {
    "mainnet": (1, "https://ethereum-rpc.publicnode.com"),
    "hoodi": (560048, "https://ethereum-hoodi-rpc.publicnode.com"),
}

# publicnode rejects the default "Python-urllib/x.y" User-Agent with HTTP 403.
USER_AGENT = "eth-rpc/1.0"

WEI_PER_ETH = 1_000_000_000_000_000_000
DEFAULT_WAIT_TIMEOUT = 120  # seconds to wait for a receipt with --wait
DEFAULT_POLL_INTERVAL = 4   # seconds between receipt polls


def network_config(network):
    """Return (chain_id, rpc_url) for a network name, or raise ValueError."""
    try:
        return NETWORKS[network]
    except KeyError:
        raise ValueError(
            "unknown network %r; expected one of %s" % (network, sorted(NETWORKS))
        )


_ADDR_RE = re.compile(r"^0x[0-9a-fA-F]{40}$")
_HEX_BODY_RE = re.compile(r"^0x[0-9a-fA-F]+$")


def validate_hex_address(addr):
    """Return addr unchanged if it is 0x + 40 hex chars; else raise ValueError.

    Format check only — EIP-55 checksum is not required on the read path.
    """
    if not isinstance(addr, str) or not _ADDR_RE.match(addr):
        raise ValueError("malformed address (expected 0x + 40 hex chars): %r" % (addr,))
    return addr


def validate_raw_tx(raw):
    """Return raw unchanged if it is a non-empty, even-length 0x hex string."""
    if (
        not isinstance(raw, str)
        or not _HEX_BODY_RE.match(raw)
        or len(raw) % 2 != 0
    ):
        raise ValueError(
            "malformed raw transaction (expected 0x-prefixed hex): %r" % (raw,)
        )
    return raw


def parse_hex_int(s):
    """Parse a 0x-prefixed hex quantity string into an int. Raise ValueError otherwise."""
    if not isinstance(s, str) or not s.startswith("0x"):
        raise ValueError("expected 0x-prefixed hex string, got %r" % (s,))
    return int(s, 16)
