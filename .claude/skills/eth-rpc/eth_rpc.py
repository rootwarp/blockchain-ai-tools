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
import urllib.parse
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

# --- passthrough safety (call op) ---
_DENY_METHODS = frozenset({
    "eth_sendRawTransaction", "eth_sendTransaction",
    "eth_sign", "eth_signTransaction",
    "eth_signTypedData", "eth_signTypedData_v3", "eth_signTypedData_v4",
})
_DENY_PREFIXES = ("personal_", "admin_", "miner_", "engine_", "clique_")
_LOOPBACK_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})


def network_config(network):
    """Return (chain_id, rpc_url) for a network name, or raise ValueError."""
    try:
        return NETWORKS[network]
    except KeyError:
        raise ValueError(
            "unknown network %r; expected one of %s" % (network, sorted(NETWORKS))
        )


# === MODULE: endpoint_resolution ===
# Public: _validate_rpc_url(url) -> str
# Public: _resolve_endpoint(network, rpc_url, chain_id) -> (int, str)

def _validate_rpc_url(url):
    """Return url unchanged if it is a safe RPC endpoint; else raise ValueError.

    HTTPS is always accepted. HTTP is accepted only for loopback hosts
    (127.0.0.1, localhost, ::1) — relies on documented
    SplitResult.hostname semantics: bracket-stripping + lowercasing.
    """
    parts = urllib.parse.urlsplit(url)
    if parts.scheme not in ("http", "https"):
        raise ValueError(
            "unsupported scheme %r in RPC URL (expected http or https)" % parts.scheme
        )
    if parts.scheme == "http":
        if parts.hostname not in _LOOPBACK_HOSTS:
            raise ValueError(
                "http:// RPC URL is only allowed for loopback hosts "
                "(%s); got %r" % (sorted(_LOOPBACK_HOSTS), parts.hostname)
            )
    return url


def _resolve_endpoint(network=None, rpc_url=None, chain_id=None):
    """Return (chain_id_int, url_str) for the given endpoint selection.

    Exactly one of the two modes must be used:
      - Named network: pass network=<name>; rpc_url and chain_id must be None.
      - Custom endpoint: pass rpc_url + chain_id; network must be None.
    """
    if network is not None and (rpc_url is not None or chain_id is not None):
        raise ValueError("use --network OR (--rpc-url + --chain-id), not both")
    if network is not None:
        return network_config(network)
    if rpc_url is None or chain_id is None:
        raise ValueError("--rpc-url and --chain-id are required together")
    return (int(chain_id), _validate_rpc_url(rpc_url))

# === END MODULE: endpoint_resolution ===


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
    """Return raw unchanged if it is a non-empty 0x hex string with an even number of nibbles after the prefix (complete bytes)."""
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


def wei_to_eth_str(wei):
    """Exact wei -> ETH decimal string (no float). e.g. 10**17 -> '0.1', 0 -> '0'."""
    if not isinstance(wei, int):
        raise ValueError("wei must be an integer")
    sign = "-" if wei < 0 else ""
    whole, frac = divmod(abs(wei), WEI_PER_ETH)
    if frac == 0:
        return "%s%d" % (sign, whole)
    frac_str = ("%018d" % frac).rstrip("0")
    return "%s%d.%s" % (sign, whole, frac_str)


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
        headers={"Content-Type": "application/json", "User-Agent": USER_AGENT},
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


def do_balance(network, address, rpc=rpc_call):
    """Build the balance result dict. `rpc` is injected for testing."""
    chain_id, url = network_config(network)
    address = validate_hex_address(address)
    wei = parse_hex_int(rpc(url, "eth_getBalance", [address, "latest"]))
    return {
        "network": network,
        "chainId": str(chain_id),
        "address": address,
        "blockTag": "latest",
        "balanceWei": str(wei),
        "balanceEth": wei_to_eth_str(wei),
    }


def _receipt_summary(receipt):
    """Map a non-null receipt dict to status + block/gas fields."""
    receipt_status = receipt.get("status")
    status = "mined" if receipt_status == "0x1" else "failed"
    out = {"status": status, "receiptStatus": receipt_status}
    if receipt.get("blockNumber") is not None:
        out["blockNumber"] = str(parse_hex_int(receipt["blockNumber"]))
    if receipt.get("gasUsed") is not None:
        out["gasUsed"] = str(parse_hex_int(receipt["gasUsed"]))
    if receipt.get("effectiveGasPrice") is not None:
        out["effectiveGasPrice"] = str(parse_hex_int(receipt["effectiveGasPrice"]))
    return out


def do_broadcast(network, raw_tx, wait=False, wait_timeout=DEFAULT_WAIT_TIMEOUT,
                 poll_interval=DEFAULT_POLL_INTERVAL, rpc=rpc_call,
                 sleep=time.sleep, now=time.monotonic):
    """Submit a signed raw transaction; optionally poll for the receipt.

    `rpc`, `sleep`, and `now` are injected for testing. A submit-time RPC error
    raises RPCError; a successful submit always returns the result dict, even on
    wait timeout (status 'pending').
    """
    chain_id, url = network_config(network)
    raw_tx = validate_raw_tx(raw_tx)
    tx_hash = rpc(url, "eth_sendRawTransaction", [raw_tx])

    result = {
        "network": network,
        "chainId": str(chain_id),
        "txHash": tx_hash,
        "status": "submitted",
    }
    if not wait:
        return result

    deadline = now() + wait_timeout
    while True:
        try:
            receipt = rpc(url, "eth_getTransactionReceipt", [tx_hash])
        except RPCError as e:
            raise RPCError(
                "receipt poll failed after submit (txHash=%s): %s" % (tx_hash, e)
            )
        if receipt is not None:
            result.update(_receipt_summary(receipt))
            return result
        if now() >= deadline:
            result["status"] = "pending"
            return result
        sleep(poll_interval)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="RPC companion for eth-signer-mcp: query balance + broadcast signed txs."
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_bal = sub.add_parser("balance", help="query the ETH balance of an EOA")
    p_bal.add_argument("--network", required=True, choices=sorted(NETWORKS))
    p_bal.add_argument("--address", required=True, help="EOA to query (0x + 40 hex)")

    p_bc = sub.add_parser("broadcast", help="submit a signed raw transaction")
    p_bc.add_argument("--network", required=True, choices=sorted(NETWORKS))
    p_bc.add_argument("--raw-tx", required=True, help="0x-prefixed signed raw tx hex")
    p_bc.add_argument("--wait", action="store_true", help="poll for the receipt after submit")
    p_bc.add_argument("--wait-timeout", type=int, default=DEFAULT_WAIT_TIMEOUT,
                      help="seconds to wait for a receipt with --wait (default %(default)s)")

    args = parser.parse_args(argv)

    try:
        if args.command == "balance":
            result = do_balance(args.network, args.address)
        else:
            if args.wait_timeout < 0:
                raise ValueError("--wait-timeout must be non-negative")
            result = do_broadcast(
                args.network, args.raw_tx, wait=args.wait, wait_timeout=args.wait_timeout
            )
    except (ValueError, RPCError) as e:
        print("error: %s" % e, file=sys.stderr)
        return 1

    print(json.dumps(result, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
