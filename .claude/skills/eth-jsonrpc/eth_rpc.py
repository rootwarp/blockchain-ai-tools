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
    "sepolia": (11155111, "https://ethereum-sepolia-rpc.publicnode.com"),
    "holesky": (17000, "https://ethereum-holesky-rpc.publicnode.com"),
}

# publicnode rejects the default "Python-urllib/x.y" User-Agent with HTTP 403.
USER_AGENT = "eth-jsonrpc/1.0"

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

# Allowlist for --read-only-strict: exactly the PRD §Scope "In scope" eth_* read surface.
# TestStrictAllowlistContents enforces literal equality to prevent silent drift (ADR-011).
_STRICT_ALLOWLIST = frozenset({
    "eth_getBalance", "eth_getCode", "eth_getStorageAt",
    "eth_getTransactionCount",
    "eth_getTransactionByHash",
    "eth_getTransactionByBlockHashAndIndex",
    "eth_getTransactionByBlockNumberAndIndex",
    "eth_getTransactionReceipt",
    "eth_getBlockByNumber", "eth_getBlockByHash",
    "eth_getBlockTransactionCountByNumber",
    "eth_getBlockTransactionCountByHash",
    "eth_getLogs", "eth_call", "eth_estimateGas", "eth_gasPrice",
    "eth_feeHistory", "eth_maxPriorityFeePerGas",
    "eth_chainId", "eth_blockNumber", "eth_syncing",
    "eth_accounts", "eth_protocolVersion", "eth_getProof",
})


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


WEI_PER_GWEI = 1_000_000_000


def _wei_to_gwei_str(wei):
    """Exact wei -> gwei decimal string using integer divmod (no float).

    e.g. 30_000_000_000 -> '30', 1_500_000_000 -> '1.5'
    """
    whole, frac = divmod(wei, WEI_PER_GWEI)
    if frac == 0:
        return "%d" % whole
    frac_str = ("%09d" % frac).rstrip("0")
    return "%d.%s" % (whole, frac_str)


class RPCError(Exception):
    """A JSON-RPC transport failure or error response."""


def rpc_call(url, method, params, timeout=15, max_body_bytes=None):
    """POST a JSON-RPC request and return its `result`. Raise RPCError on any failure.

    max_body_bytes: when set, read at most max_body_bytes+1 bytes and raise RPCError
    if the body exceeds the limit (ADR-013). Default None preserves existing behaviour.
    """
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
            if max_body_bytes is None:
                raw = resp.read()
            else:
                raw = resp.read(max_body_bytes + 1)
                if len(raw) > max_body_bytes:
                    raise RPCError(
                        "response exceeds --max-body-bytes (limit was %d bytes)" % max_body_bytes
                    )
            body = json.loads(raw.decode("utf-8"))
    except RPCError:
        raise
    except (OSError, ValueError) as e:  # transport (URLError⊂OSError) / decode (JSON/Unicode⊂ValueError)
        raise RPCError("RPC transport error calling %s: %s" % (method, e))
    if body.get("error") is not None:
        raise RPCError("RPC error for %s: %s" % (method, body["error"]))
    if "result" not in body:
        raise RPCError("RPC response missing result for %s" % method)
    return body["result"]


def rpc_batch(url, payload, timeout=15, max_body_bytes=None):
    """POST a JSON-RPC batch request and return the parsed response array.

    payload is a list of JSON-RPC request objects (already built by do_batch).
    Raises RPCError on transport failure or if the server returns a non-list
    (e.g. a single error object for the whole batch).
    max_body_bytes: same ADR-013 bound as rpc_call; default None is unbounded.
    """
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json", "User-Agent": USER_AGENT},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            if max_body_bytes is None:
                raw = resp.read()
            else:
                raw = resp.read(max_body_bytes + 1)
                if len(raw) > max_body_bytes:
                    raise RPCError(
                        "response exceeds --max-body-bytes (limit was %d bytes)" % max_body_bytes
                    )
            body = json.loads(raw.decode("utf-8"))
    except RPCError:
        raise
    except (OSError, ValueError) as e:
        raise RPCError("RPC batch transport error: %s" % e)
    if not isinstance(body, list):
        raise RPCError("RPC batch response was not a list (server may have rejected the batch): %s" % body)
    return body


def do_balance(*, network=None, rpc_url=None, chain_id=None, address, rpc=rpc_call):
    """Build the balance result dict. `rpc` is injected for testing.

    Exactly one of the two modes must be used:
      - Named network: pass network=<name>; rpc_url and chain_id must be None.
      - Custom endpoint: pass rpc_url + chain_id; network must be None.
    """
    chain_id, url = _resolve_endpoint(network=network, rpc_url=rpc_url, chain_id=chain_id)
    address = validate_hex_address(address)
    wei = parse_hex_int(rpc(url, "eth_getBalance", [address, "latest"]))
    out = {
        "chainId": str(chain_id),
        "address": address,
        "blockTag": "latest",
        "balanceWei": str(wei),
        "balanceEth": wei_to_eth_str(wei),
    }
    if network is not None:
        out = {"network": network, **out}
    return out


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


def do_broadcast(*, network=None, rpc_url=None, chain_id=None, raw_tx,
                 wait=False, wait_timeout=DEFAULT_WAIT_TIMEOUT,
                 poll_interval=DEFAULT_POLL_INTERVAL, rpc=rpc_call,
                 sleep=time.sleep, now=time.monotonic):
    """Submit a signed raw transaction; optionally poll for the receipt.

    `rpc`, `sleep`, and `now` are injected for testing. A submit-time RPC error
    raises RPCError; a successful submit always returns the result dict, even on
    wait timeout (status 'pending').

    Exactly one of the two modes must be used:
      - Named network: pass network=<name>; rpc_url and chain_id must be None.
      - Custom endpoint: pass rpc_url + chain_id; network must be None.
    """
    chain_id, url = _resolve_endpoint(network=network, rpc_url=rpc_url, chain_id=chain_id)
    raw_tx = validate_raw_tx(raw_tx)
    tx_hash = rpc(url, "eth_sendRawTransaction", [raw_tx])

    result = {
        "chainId": str(chain_id),
        "txHash": tx_hash,
        "status": "submitted",
    }
    if network is not None:
        result = {"network": network, **result}
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


# === MODULE: policy ===
# Public: _check_method_policy(method, *, allow_write=False, allowlist=None) -> None

def _check_method_policy(method, *, allow_write=False, allowlist=None):
    """Check method against the allowlist (if set) and denylist. Raise ValueError on refusal.

    Evaluation order:
      1. allow_write=True -> return immediately (bypasses allowlist + denylist).
         Note: allow_write takes precedence even when allowlist is provided.
      2. allowlist is not None -> method must be in the allowlist; raise if not
         (this is the tighter rule, so it fires before the denylist).
      3. denylist -> method must not be in _DENY_METHODS or match _DENY_PREFIXES.

    allow_write=True bypasses all checks (denylist + allowlist).
    allowlist=None means no allowlist is enforced (Phase 2 Task 2.3 contract).
    """
    if not isinstance(method, str) or not method:
        raise ValueError("method must be a non-empty string")
    if allow_write:
        return None
    if allowlist is not None and method not in allowlist:
        raise ValueError(
            "method %s is not in the documented eth_* read surface "
            "(--read-only-strict)" % method
        )
    if method in _DENY_METHODS:
        raise ValueError(
            "method %s refused (use the 'broadcast' op for sends, "
            "or pass --allow-write to override)" % method
        )
    if method.startswith(_DENY_PREFIXES):
        raise ValueError(
            "method %s in a sensitive namespace; pass --allow-write "
            "to override" % method
        )
    return None

# === END MODULE: policy ===


# === MODULE: do_call ===
# Public: do_call(url, *, method, params, allow_write=False,
#                 timeout=15, rpc=rpc_call) -> Any

def do_call(url, *, method, params, allow_write=False,
            strict=False, timeout=15, max_body_bytes=None, rpc=rpc_call):
    """Generic eth_* read passthrough. Returns raw JSON-RPC result.

    Policy enforcement order (most restrictive first):
      1. allow_write=True -> bypass everything (denylist + strict allowlist).
      2. strict=True -> method must be in _STRICT_ALLOWLIST; refusal fires here,
         before the denylist, with a 'read surface' message.
      3. denylist -> _DENY_METHODS and _DENY_PREFIXES are blocked.
    """
    if not isinstance(method, str) or not method:
        raise ValueError("--method is required")
    if not isinstance(params, list):
        raise ValueError("--params must be a JSON array")
    _check_method_policy(
        method,
        allow_write=allow_write,
        allowlist=_STRICT_ALLOWLIST if strict else None,
    )
    return rpc(url, method, params, timeout=timeout, max_body_bytes=max_body_bytes)

# === END MODULE: do_call ===


# === MODULE: do_batch ===
# Public: do_batch(url, *, calls, allow_write=False, timeout=15, rpc=rpc_batch) -> list

def do_batch(url, *, calls, allow_write=False, timeout=15, max_body_bytes=None, rpc=rpc_batch):
    """JSON-RPC batch passthrough. Returns a list of per-entry result/error envelopes.

    calls: list of {"method": str, "params": list} dicts.
    Refused entries (denylist) land as synthetic error envelopes at their original index.
    Transport failures raise RPCError. Partial server-side failures are envelopes, not exceptions.
    Per ADR-012: ids are positional (0..N-1); server response is re-sorted by id.
    """
    if not calls:
        raise ValueError("--calls must be a non-empty JSON array")

    # Validate all entries up-front before any wire egress.
    for i, call in enumerate(calls):
        if not isinstance(call, dict) or not isinstance(call.get("method"), str):
            raise ValueError(
                "--calls entry %d must be an object with a 'method' string" % i
            )
        if not isinstance(call.get("params"), list):
            raise ValueError(
                "--calls entry %d 'params' must be a list" % i
            )

    # Pre-scan: identify refused entries; build wire payload for the allowed ones.
    result = [None] * len(calls)
    wire_payload = []
    for i, call in enumerate(calls):
        method = call["method"]
        params = call["params"]
        try:
            _check_method_policy(method, allow_write=allow_write)
        except ValueError as e:
            result[i] = {"id": i, "error": {"code": -32601, "message": str(e)}}
            continue
        wire_payload.append({"jsonrpc": "2.0", "id": i, "method": method, "params": params})

    # If there are any allowed entries, send the batch.
    if wire_payload:
        wire_response = rpc(url, wire_payload, timeout, max_body_bytes=max_body_bytes)
        # Re-sort by id (servers may return out of order per JSON-RPC spec).
        # Defensive: skip entries whose "id" is absent/None (JSON-RPC permits
        # this on a parse error).  Normalise string ids to int so that gateways
        # that echo ids as "0" instead of 0 still match our int-keyed wire_payload.
        by_id = {}
        for entry in wire_response:
            raw_id = entry.get("id")
            if raw_id is None:
                continue
            try:
                key = int(raw_id)
            except (TypeError, ValueError):
                key = raw_id
            by_id[key] = entry
        for item in wire_payload:
            i = item["id"]
            server_entry = by_id.get(i, {})
            if "result" in server_entry:
                result[i] = {"id": i, "result": server_entry["result"]}
            elif "error" in server_entry:
                result[i] = {"id": i, "error": server_entry["error"]}
            else:
                result[i] = {"id": i, "error": {"code": -32603, "message": "missing result from server"}}

    return result

# === END MODULE: do_batch ===


# === MODULE: do_diagnostics ===
# Public: do_net_version(url, chain_id, *, timeout=15, rpc=rpc_call) -> dict
# Public: do_client_version(url, chain_id, *, timeout=15, rpc=rpc_call) -> dict

def do_net_version(url, chain_id, *, network=None, timeout=15, rpc=rpc_call):
    """Call net_version and return a wrapped dict including chainId (and network if given)."""
    result = rpc(url, "net_version", [], timeout=timeout)
    out = {"chainId": str(chain_id), "netVersion": result}
    if network is not None:
        out["network"] = network
    return out


def do_client_version(url, chain_id, *, network=None, timeout=15, rpc=rpc_call):
    """Call web3_clientVersion and return a wrapped dict including chainId (and network if given)."""
    result = rpc(url, "web3_clientVersion", [], timeout=timeout)
    out = {"chainId": str(chain_id), "clientVersion": result}
    if network is not None:
        out["network"] = network
    return out

# === END MODULE: do_diagnostics ===


# === MODULE: decode ===
# Public:  _decode_result(method, result) -> dict | list | result
#
# Per-method decoders post-process the raw JSON-RPC result. Off by default.
# Called from main after do_call returns when --decode is set.

_HEX_QUANTITY_METHODS = frozenset({
    "eth_blockNumber",
    "eth_gasPrice",
    "eth_chainId",
    "eth_getTransactionCount",
    "eth_estimateGas",
    "eth_maxPriorityFeePerGas",
    "eth_getBalance",
})

# Methods that return gas-price-shaped quantities (wei + gwei).
_GAS_PRICE_METHODS = frozenset({"eth_gasPrice", "eth_maxPriorityFeePerGas"})


def _decode_hex_quantity(method, result):
    """Return a decoded dict for a hex-quantity result.

    For eth_getBalance: {hex, wei, eth}.
    For gas-price methods: {hex, wei, gwei} (integer divmod, no float).
    For all others: {hex, decimal}.

    Defensive: if result is not a 0x-prefixed string, return it unchanged.
    A legitimate uint256 is at most 64 hex chars (66 incl. 0x prefix); any
    longer value is passed through unchanged to avoid Python 3.11+ bignum
    int-to-str limits in json.dumps.
    """
    if not isinstance(result, str) or not result.startswith("0x"):
        return result
    if len(result) > 66:
        return result
    try:
        value = parse_hex_int(result)
    except ValueError:
        return result

    if method == "eth_getBalance":
        return {
            "hex": result,
            "wei": value,
            "eth": wei_to_eth_str(value),
        }
    if method in _GAS_PRICE_METHODS:
        return {
            "hex": result,
            "wei": value,
            "gwei": _wei_to_gwei_str(value),
        }
    return {"hex": result, "decimal": value}


def _decode_block(result):
    """Decode a block object result. Returns {raw: result, <decoded fields>}.

    Decodes: number, gasUsed, gasLimit, baseFeePerGas, timestamp, size,
    difficulty, nonce. Does NOT decode totalDifficulty (legacy under PoS).
    Defensive: missing field -> omit; field not hex-string -> omit.
    """
    if result is None:
        return None
    if not isinstance(result, dict):
        return {"raw": result}

    _BLOCK_INT_FIELDS = (
        "number", "gasUsed", "gasLimit", "baseFeePerGas",
        "timestamp", "size", "difficulty", "nonce",
    )
    out = {"raw": result}
    for field in _BLOCK_INT_FIELDS:
        raw_val = result.get(field)
        if (raw_val is not None and isinstance(raw_val, str)
                and raw_val.startswith("0x") and len(raw_val) <= 66):
            try:
                out[field] = parse_hex_int(raw_val)
            except ValueError:
                pass
    return out


def _decode_tx(result):
    """Decode a transaction object result. Returns {raw: result, <decoded fields>}.

    Decodes numeric fields. value -> {wei, eth}. Gas-price fields -> {wei, gwei}.
    `from` is optional per spec — omitted when missing.
    Defensive: missing or non-hex field -> omit.
    """
    if result is None:
        return None
    if not isinstance(result, dict):
        return {"raw": result}

    _TX_INT_FIELDS = ("blockNumber", "transactionIndex", "nonce", "gas", "chainId", "type")
    _TX_GWEI_FIELDS = ("gasPrice", "maxFeePerGas", "maxPriorityFeePerGas")

    out = {"raw": result}
    for field in _TX_INT_FIELDS:
        raw_val = result.get(field)
        if (raw_val is not None and isinstance(raw_val, str)
                and raw_val.startswith("0x") and len(raw_val) <= 66):
            try:
                out[field] = parse_hex_int(raw_val)
            except ValueError:
                pass
    # value: wei + eth
    raw_value = result.get("value")
    if (raw_value is not None and isinstance(raw_value, str)
            and raw_value.startswith("0x") and len(raw_value) <= 66):
        try:
            wei = parse_hex_int(raw_value)
            out["value"] = {"wei": wei, "eth": wei_to_eth_str(wei)}
        except ValueError:
            pass
    # gas-price-shaped fields
    for field in _TX_GWEI_FIELDS:
        raw_val = result.get(field)
        if (raw_val is not None and isinstance(raw_val, str)
                and raw_val.startswith("0x") and len(raw_val) <= 66):
            try:
                wei = parse_hex_int(raw_val)
                out[field] = {"wei": wei, "gwei": _wei_to_gwei_str(wei)}
            except ValueError:
                pass
    return out


def _decode_receipt(result):
    """Decode a transaction receipt object result. Returns {raw: result, <decoded fields>}.

    Decodes: blockNumber, transactionIndex, gasUsed, cumulativeGasUsed,
    effectiveGasPrice, status, type.
    Defensive: missing or non-hex field -> omit.
    """
    if result is None:
        return None
    if not isinstance(result, dict):
        return {"raw": result}

    _RECEIPT_INT_FIELDS = (
        "blockNumber", "transactionIndex", "gasUsed",
        "cumulativeGasUsed", "effectiveGasPrice", "status", "type",
    )
    out = {"raw": result}
    for field in _RECEIPT_INT_FIELDS:
        raw_val = result.get(field)
        if (raw_val is not None and isinstance(raw_val, str)
                and raw_val.startswith("0x") and len(raw_val) <= 66):
            try:
                out[field] = parse_hex_int(raw_val)
            except ValueError:
                pass
    return out


def _decode_log_entry(entry):
    """Decode a single log entry dict. Returns {raw: entry, blockNumber, logIndex, transactionIndex}.

    topics/data/address/hashes remain in raw only.
    Defensive: missing field -> omit; non-dict entry -> return untouched.
    """
    if not isinstance(entry, dict):
        return entry
    out = {"raw": entry}
    for field in ("blockNumber", "logIndex", "transactionIndex"):
        raw_val = entry.get(field)
        if (raw_val is not None and isinstance(raw_val, str)
                and raw_val.startswith("0x") and len(raw_val) <= 66):
            try:
                out[field] = parse_hex_int(raw_val)
            except ValueError:
                pass
    return out


# Methods that return block objects.
_BLOCK_METHODS = frozenset({
    "eth_getBlockByNumber",
    "eth_getBlockByHash",
})

# Methods that return transaction objects.
_TX_METHODS = frozenset({
    "eth_getTransactionByHash",
    "eth_getTransactionByBlockHashAndIndex",
    "eth_getTransactionByBlockNumberAndIndex",
})

# Methods that return receipt objects.
_RECEIPT_METHODS = frozenset({
    "eth_getTransactionReceipt",
})

# Methods that return log arrays.
_LOG_METHODS = frozenset({
    "eth_getLogs",
})


def _decode_fee_history(result):
    """Decode an eth_feeHistory result dict.

    Returns a new dict with decoded numeric fields alongside raw.
    Decoded additions:
      - oldestBlock: int decimal (hex -> int)
      - baseFeePerGas: list of ints (each hex entry decoded)
      - baseFeePerGasGwei: list of decimal-string gwei values
      - reward: 2D list of ints (present only if result had a non-None reward)
      - raw: the unmodified result dict
    gasUsedRatio is left as-is (already floats per spec).
    DEFENSIVE: missing/None/non-hex/oversized (> 66 chars) field -> omit decoded
    variant; never raise.
    """
    if not isinstance(result, dict):
        return result

    out = {"raw": result}

    # oldestBlock: hex quantity -> decimal int
    raw_ob = result.get("oldestBlock")
    if (raw_ob is not None and isinstance(raw_ob, str)
            and raw_ob.startswith("0x") and len(raw_ob) <= 66):
        try:
            out["oldestBlock"] = parse_hex_int(raw_ob)
        except ValueError:
            pass

    # baseFeePerGas array: decode each entry; produce parallel gwei array.
    raw_bfpg = result.get("baseFeePerGas")
    if isinstance(raw_bfpg, list):
        decoded_bfpg = []
        gwei_bfpg = []
        for entry in raw_bfpg:
            if (entry is not None and isinstance(entry, str)
                    and entry.startswith("0x") and len(entry) <= 66):
                try:
                    wei = parse_hex_int(entry)
                    decoded_bfpg.append(wei)
                    gwei_bfpg.append(_wei_to_gwei_str(wei))
                    continue
                except ValueError:
                    pass
            decoded_bfpg.append(None)
            gwei_bfpg.append(None)
        out["baseFeePerGas"] = decoded_bfpg
        out["baseFeePerGasGwei"] = gwei_bfpg

    # gasUsedRatio: already floats per spec, copy as-is.
    raw_gur = result.get("gasUsedRatio")
    if raw_gur is not None:
        out["gasUsedRatio"] = raw_gur

    # reward: 2D list of hex quantities; omit entirely if absent or None.
    raw_reward = result.get("reward")
    if isinstance(raw_reward, list):
        decoded_reward = []
        for inner in raw_reward:
            if not isinstance(inner, list):
                decoded_reward.append(inner)
                continue
            decoded_inner = []
            for entry in inner:
                if (entry is not None and isinstance(entry, str)
                        and entry.startswith("0x") and len(entry) <= 66):
                    try:
                        decoded_inner.append(parse_hex_int(entry))
                        continue
                    except ValueError:
                        pass
                decoded_inner.append(None)
            decoded_reward.append(decoded_inner)
        out["reward"] = decoded_reward

    return out


def _decode_proof(result):
    """Decode an eth_getProof result dict.

    Returns a new dict with decoded numeric fields alongside raw.
    Decoded additions:
      - balance: int decimal (hex -> int)
      - nonce: int decimal (hex -> int)
      - storageProof[]: list of dicts; each entry's value decoded to int;
        key left as raw hex; proof[] array left as raw hex byte sequences.
      - raw: the unmodified result dict
    codeHash, storageHash: left as raw hex (32-byte digests — not int-parsed).
    DEFENSIVE: missing key/None -> omit decoded variant; never raise;
    result is None -> return None.
    """
    if result is None:
        return None
    if not isinstance(result, dict):
        return {"raw": result}

    out = {"raw": result}

    # balance: hex quantity -> decimal int
    raw_balance = result.get("balance")
    if (raw_balance is not None and isinstance(raw_balance, str)
            and raw_balance.startswith("0x") and len(raw_balance) <= 66):
        try:
            out["balance"] = parse_hex_int(raw_balance)
        except ValueError:
            pass

    # nonce: hex quantity -> decimal int
    raw_nonce = result.get("nonce")
    if (raw_nonce is not None and isinstance(raw_nonce, str)
            and raw_nonce.startswith("0x") and len(raw_nonce) <= 66):
        try:
            out["nonce"] = parse_hex_int(raw_nonce)
        except ValueError:
            pass

    # storageProof: decode value for each slot; key and proof[] stay as raw hex.
    raw_sp = result.get("storageProof")
    if isinstance(raw_sp, list):
        decoded_sp = []
        for slot in raw_sp:
            if not isinstance(slot, dict):
                decoded_sp.append(slot)
                continue
            decoded_slot = {
                "key": slot.get("key"),
                "proof": slot.get("proof") or [],  # null/absent/non-list -> []
            }
            raw_val = slot.get("value")
            if (raw_val is not None and isinstance(raw_val, str)
                    and raw_val.startswith("0x") and len(raw_val) <= 66):
                try:
                    decoded_slot["value"] = parse_hex_int(raw_val)
                except ValueError:
                    pass
            decoded_sp.append(decoded_slot)
        out["storageProof"] = decoded_sp

    return out


def _decode_result(method, result):
    """Post-process raw JSON-RPC result for well-known method shapes.

    Returns decoded representation, or result unchanged if not recognised.
    NEVER raises — decode must not break passthrough.
    On unexpected decode failure, prints a one-line warning to stderr so the
    operator knows decode was skipped, then returns the raw result.
    """
    try:
        if method in _HEX_QUANTITY_METHODS:
            return _decode_hex_quantity(method, result)
        if method in _BLOCK_METHODS:
            return _decode_block(result)
        if method in _TX_METHODS:
            return _decode_tx(result)
        if method in _RECEIPT_METHODS:
            return _decode_receipt(result)
        if method in _LOG_METHODS:
            if not isinstance(result, list):
                return result
            return [_decode_log_entry(entry) for entry in result]
        if method == "eth_feeHistory":
            return _decode_fee_history(result)
        if method == "eth_getProof":
            return _decode_proof(result)
    except Exception as _exc:
        # Defensive: any unexpected error must not break passthrough.
        # Emit a one-line warning so the operator knows decode was skipped.
        print(
            "warning: --decode failed for %s (%s); returning raw result" % (method, _exc),
            file=sys.stderr,
        )
    return result

# === END MODULE: decode ===


# === MODULE: param_ingest ===
# Public: _parse_params(raw, *, stdin=sys.stdin, opener=open) -> list

def _parse_params(raw, *, stdin=sys.stdin, opener=open):
    """Parse --params (inline, stdin, or @file) into a list, or raise ValueError.

    Modes:
      '-'          read from stdin.
      '@<path>'    read from file at <path>. '@-' is a file path, not stdin.
      '<json>'     parse inline JSON.
    """
    if raw == "-":
        raw = stdin.read()
    elif raw.startswith("@"):
        # @<path>: read file contents. @- is intentionally not equivalent
        # to bare "-" — the @ prefix unambiguously means "file path".
        path = raw[1:]
        try:
            with opener(path, "r") as fh:
                raw = fh.read()
        except OSError as e:
            raise ValueError("--params @%s: %s" % (path, e))
    try:
        value = json.loads(raw)
    except ValueError as e:
        raise ValueError("--params must be a JSON array: %s" % e)
    if not isinstance(value, list):
        raise ValueError(
            "--params must be a JSON array (got %s)" % type(value).__name__
        )
    return value

# === END MODULE: param_ingest ===


def _positive_int(s):
    """argparse type validator: accept only integers > 0."""
    try:
        value = int(s)
    except ValueError:
        raise argparse.ArgumentTypeError("%r is not a valid integer" % s)
    if value <= 0:
        raise argparse.ArgumentTypeError(
            "--max-body-bytes must be a positive integer (got %d)" % value
        )
    return value


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="RPC companion for eth-signer-mcp: query balance + broadcast signed txs."
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_bal = sub.add_parser("balance", help="query the ETH balance of an EOA")
    p_bal.add_argument("--network", choices=sorted(NETWORKS),
                       help="named network (use --network OR --rpc-url + --chain-id)")
    p_bal.add_argument("--rpc-url", help="custom RPC endpoint URL")
    p_bal.add_argument("--chain-id", type=int, help="chain ID for custom endpoint")
    p_bal.add_argument("--address", required=True, help="EOA to query (0x + 40 hex)")

    p_bc = sub.add_parser("broadcast", help="submit a signed raw transaction")
    p_bc.add_argument("--network", choices=sorted(NETWORKS),
                      help="named network (use --network OR --rpc-url + --chain-id)")
    p_bc.add_argument("--rpc-url", help="custom RPC endpoint URL")
    p_bc.add_argument("--chain-id", type=int, help="chain ID for custom endpoint")
    p_bc.add_argument("--raw-tx", required=True, help="0x-prefixed signed raw tx hex")
    p_bc.add_argument("--wait", action="store_true", help="poll for the receipt after submit")
    p_bc.add_argument("--wait-timeout", type=int, default=DEFAULT_WAIT_TIMEOUT,
                      help="seconds to wait for a receipt with --wait (default %(default)s)")

    p_call = sub.add_parser("call", help="generic eth_* JSON-RPC read passthrough")
    p_call.add_argument("--network", choices=sorted(NETWORKS))
    p_call.add_argument("--rpc-url")
    p_call.add_argument("--chain-id", type=int)
    p_call.add_argument("--method", required=True)
    p_call.add_argument(
        "--params",
        required=True,
        help="JSON array; pass '-' to read from stdin",
    )
    _call_policy = p_call.add_mutually_exclusive_group()
    _call_policy.add_argument("--allow-write", action="store_true",
                              help="bypass the call denylist (e.g. for dev nodes)")
    _call_policy.add_argument(
        "--read-only-strict", action="store_true",
        help="only allow methods in the documented eth_* read surface (recommended for CI)",
    )
    p_call.add_argument("--timeout", type=int, default=15)
    p_call.add_argument(
        "--max-body-bytes", type=_positive_int, default=None,
        help="cap response body size (bytes); see SKILL.md for eth_getLogs guidance",
    )
    p_call.add_argument(
        "--decode", action="store_true",
        help="post-process well-known result shapes; off by default",
    )

    p_batch = sub.add_parser("batch", help="JSON-RPC batch passthrough (ADR-012)")
    p_batch.add_argument("--network", choices=sorted(NETWORKS))
    p_batch.add_argument("--rpc-url")
    p_batch.add_argument("--chain-id", type=int)
    p_batch.add_argument(
        "--calls",
        required=True,
        help="JSON array of {method, params} objects; pass '-' to read from stdin",
    )
    p_batch.add_argument("--allow-write", action="store_true")
    p_batch.add_argument("--timeout", type=int, default=15)
    p_batch.add_argument(
        "--max-body-bytes", type=_positive_int, default=None,
        help="cap response body size (bytes); see SKILL.md for eth_getLogs guidance",
    )

    p_nv = sub.add_parser(
        "net-version",
        help="diagnostic: call net_version and return chainId + netVersion",
    )
    p_nv.add_argument("--network", choices=sorted(NETWORKS))
    p_nv.add_argument("--rpc-url")
    p_nv.add_argument("--chain-id", type=int)
    p_nv.add_argument("--timeout", type=int, default=15)

    p_cv = sub.add_parser(
        "client-version",
        help="diagnostic: call web3_clientVersion and return chainId + clientVersion",
    )
    p_cv.add_argument("--network", choices=sorted(NETWORKS))
    p_cv.add_argument("--rpc-url")
    p_cv.add_argument("--chain-id", type=int)
    p_cv.add_argument("--timeout", type=int, default=15)

    args = parser.parse_args(argv)

    try:
        if args.command == "balance":
            result = do_balance(
                network=args.network,
                rpc_url=args.rpc_url,
                chain_id=args.chain_id,
                address=args.address,
            )
        elif args.command == "broadcast":
            if args.wait_timeout < 0:
                raise ValueError("--wait-timeout must be non-negative")
            result = do_broadcast(
                network=args.network,
                rpc_url=args.rpc_url,
                chain_id=args.chain_id,
                raw_tx=args.raw_tx,
                wait=args.wait,
                wait_timeout=args.wait_timeout,
            )
        elif args.command == "batch":
            calls = _parse_params(args.calls, stdin=sys.stdin)
            chain_id, url = _resolve_endpoint(
                network=args.network,
                rpc_url=args.rpc_url,
                chain_id=args.chain_id,
            )
            if args.allow_write:
                print(
                    "warning: --allow-write bypasses the call denylist",
                    file=sys.stderr,
                )
            result = do_batch(
                url,
                calls=calls,
                allow_write=args.allow_write,
                timeout=args.timeout,
                max_body_bytes=args.max_body_bytes,
            )
            print(json.dumps(result, indent=2))
            return 0
        elif args.command in ("net-version", "client-version"):
            chain_id, url = _resolve_endpoint(
                network=args.network,
                rpc_url=args.rpc_url,
                chain_id=args.chain_id,
            )
            if args.command == "net-version":
                result = do_net_version(url, chain_id, network=args.network, timeout=args.timeout)
            else:
                result = do_client_version(url, chain_id, network=args.network, timeout=args.timeout)
            print(json.dumps(result, indent=2))
            return 0
        else:
            params = _parse_params(args.params, stdin=sys.stdin)
            chain_id, url = _resolve_endpoint(
                network=args.network,
                rpc_url=args.rpc_url,
                chain_id=args.chain_id,
            )
            if args.allow_write:
                print(
                    "warning: --allow-write bypasses the call denylist",
                    file=sys.stderr,
                )
            result = do_call(
                url,
                method=args.method,
                params=params,
                allow_write=args.allow_write,
                strict=args.read_only_strict,
                timeout=args.timeout,
                max_body_bytes=args.max_body_bytes,
            )
            if args.decode:
                result = _decode_result(args.method, result)
            print(json.dumps(result, indent=2))
            return 0
    except (ValueError, RPCError) as e:
        print("error: %s" % e, file=sys.stderr)
        return 1

    print(json.dumps(result, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
