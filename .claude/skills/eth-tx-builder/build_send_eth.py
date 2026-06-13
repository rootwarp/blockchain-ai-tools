#!/usr/bin/env python3
"""Build a ready-to-sign EIP-1559 send-ETH TxRequest JSON for eth-signer-mcp.

Stdlib only. Queries a hardcoded public RPC for nonce + fees, converts the
gwei amount to wei, and prints the sign_transaction request JSON to stdout.
This script never signs.
"""

import argparse
import json
import re
import sys
import urllib.request

# network -> (chainId, rpc_url)
NETWORKS = {
    "mainnet": (1, "https://ethereum-rpc.publicnode.com"),
    "hoodi": (560048, "https://ethereum-hoodi-rpc.publicnode.com"),
}

DEFAULT_TIP_WEI = 1_000_000_000  # 1 gwei, fallback when eth_maxPriorityFeePerGas is unavailable
GAS_LIMIT_ETH_TRANSFER = 21000  # fixed intrinsic cost of a plain value transfer


def network_config(network):
    """Return (chain_id, rpc_url) for a network name, or raise ValueError."""
    try:
        return NETWORKS[network]
    except KeyError:
        raise ValueError(
            "unknown network %r; expected one of %s" % (network, sorted(NETWORKS))
        )


def gwei_to_wei(amount_gwei):
    """Convert an integer gwei amount to wei. Raises ValueError if non-int or negative."""
    if not isinstance(amount_gwei, int):
        raise ValueError("amount-gwei must be an integer")
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


def parse_hex_int(s):
    """Parse a 0x-prefixed hex quantity string into an int. Raise ValueError otherwise."""
    if not isinstance(s, str) or not s.startswith("0x"):
        raise ValueError("expected 0x-prefixed hex string, got %r" % (s,))
    return int(s, 16)


def compute_max_fee(base_fee, tip):
    """maxFeePerGas heuristic: baseFee*2 + tip (absorbs ~6 full blocks of base-fee rise)."""
    return base_fee * 2 + tip


class RPCError(Exception):
    """A JSON-RPC transport failure or error response."""


def rpc_call(url, method, params, timeout=15):
    """POST a JSON-RPC request and return its `result`. Raise RPCError on any failure."""
    payload = json.dumps(
        {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
    ).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = json.loads(resp.read().decode("utf-8"))
    except (OSError, ValueError) as e:  # transport (URLError⊂OSError) / decode (JSON/Unicode⊂ValueError)
        raise RPCError("RPC transport error calling %s: %s" % (method, e))
    if body.get("error") is not None:
        raise RPCError("RPC error for %s: %s" % (method, body["error"]))
    if "result" not in body:
        raise RPCError("RPC response missing result for %s" % method)
    return body["result"]


def fetch_nonce(rpc, url, sender):
    """Nonce = eth_getTransactionCount(sender, "pending")."""
    return parse_hex_int(rpc(url, "eth_getTransactionCount", [sender, "pending"]))


def fetch_base_fee(rpc, url):
    """baseFeePerGas of the latest block. Raise RPCError if absent (pre-EIP-1559 chain)."""
    block = rpc(url, "eth_getBlockByNumber", ["latest", False])
    if not isinstance(block, dict) or block.get("baseFeePerGas") is None:
        raise RPCError("latest block has no baseFeePerGas (not an EIP-1559 chain?)")
    return parse_hex_int(block["baseFeePerGas"])


def fetch_tip(rpc, url):
    """Suggested priority fee from eth_maxPriorityFeePerGas; fall back to DEFAULT_TIP_WEI."""
    try:
        return parse_hex_int(rpc(url, "eth_maxPriorityFeePerGas", []))
    except (RPCError, ValueError):
        return DEFAULT_TIP_WEI


def build_tx_request(network, to, amount_gwei, sender, rpc=rpc_call):
    """Build the sign_transaction request dict. `rpc` is injected for testing.

    Numeric fields are decimal strings (the canonical form validate.go accepts).
    """
    chain_id, url = network_config(network)
    to = validate_hex_address(to)
    sender = validate_hex_address(sender)
    value_wei = gwei_to_wei(amount_gwei)

    nonce = fetch_nonce(rpc, url, sender)
    base_fee = fetch_base_fee(rpc, url)
    tip = fetch_tip(rpc, url)
    max_fee = compute_max_fee(base_fee, tip)

    return {
        "type": "eip1559",
        "chainId": str(chain_id),
        "nonce": str(nonce),
        "to": to,
        "value": str(value_wei),
        "data": "0x",
        "gas": str(GAS_LIMIT_ETH_TRANSFER),
        "maxFeePerGas": str(max_fee),
        "maxPriorityFeePerGas": str(tip),
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Build a ready-to-sign EIP-1559 send-ETH TxRequest JSON for eth-signer-mcp."
    )
    parser.add_argument("--network", required=True, choices=sorted(NETWORKS))
    parser.add_argument("--to", required=True, help="recipient EOA (0x + 40 hex)")
    parser.add_argument("--amount-gwei", required=True, type=int, help="amount to send, in gwei")
    parser.add_argument("--sender", required=True, help="signing account (0x + 40 hex)")
    args = parser.parse_args(argv)

    try:
        tx = build_tx_request(args.network, args.to, args.amount_gwei, args.sender)
    except (ValueError, RPCError) as e:
        print("error: %s" % e, file=sys.stderr)
        return 1

    print(json.dumps(tx, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
