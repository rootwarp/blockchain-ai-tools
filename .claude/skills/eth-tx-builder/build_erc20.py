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

# Selectors are keccak256(canonical_signature)[:4]. Hardcoded because the
# Python stdlib does not ship Keccak (hashlib.sha3_256 is NOT keccak;
# SHA-3 finalisation differs). Verified against the USDC mainnet test
# vectors in research/01-abi-encoding.
SEL_TRANSFER       = "0xa9059cbb"   # keccak256("transfer(address,uint256)")[:4]
SEL_APPROVE        = "0x095ea7b3"   # keccak256("approve(address,uint256)")[:4]
SEL_TRANSFER_FROM  = "0x23b872dd"   # keccak256("transferFrom(address,address,uint256)")[:4]
SEL_DECIMALS       = "0x313ce567"   # keccak256("decimals()")[:4]
SEL_SYMBOL         = "0x95d89b41"   # keccak256("symbol()")[:4]
SEL_ALLOWANCE      = "0xdd62ed3e"   # keccak256("allowance(address,address)")[:4]

MAX_DECIMALS = 36  # research §1.4; rejects hostile values above this ceiling


def _encode_address(addr_hex):
    """Encode an Ethereum address as a 64-hex-char ABI word (left-padded, lowercase)."""
    # Strip 0x prefix, lowercase, left-pad to 64 hex chars (32 bytes = 12 zero bytes + 20 addr)
    raw = addr_hex.lower()
    if raw.startswith("0x"):
        raw = raw[2:]
    return raw.zfill(64)


def _encode_uint256(n):
    """Encode an integer as a 64-hex-char ABI word (big-endian, left zero-padded).

    Raises ValueError if n < 0 or n >= 2**256.
    """
    if n < 0:
        raise ValueError("cannot encode negative integer as uint256: %d" % n)
    if n >= (1 << 256):
        raise ValueError("integer %d exceeds uint256 max" % n)
    return format(n, "064x")


def _pack_call(selector_hex, *args_hex):
    """Concatenate selector + ABI-encoded argument words into a 0x-prefixed calldata string."""
    return "0x" + selector_hex[2:] + "".join(args_hex)


def encode_transfer(to, amount_base):
    """ABI-encode a transfer(address,uint256) calldata."""
    return _pack_call(SEL_TRANSFER, _encode_address(to), _encode_uint256(amount_base))


def encode_approve(spender, amount_base):
    """ABI-encode an approve(address,uint256) calldata."""
    return _pack_call(SEL_APPROVE, _encode_address(spender), _encode_uint256(amount_base))


def encode_transfer_from(from_, to, amount_base):
    """ABI-encode a transferFrom(address,address,uint256) calldata."""
    return _pack_call(
        SEL_TRANSFER_FROM,
        _encode_address(from_),
        _encode_address(to),
        _encode_uint256(amount_base),
    )


def encode_decimals_call():
    """Return the calldata for a decimals() read (selector only, no args)."""
    return SEL_DECIMALS


def encode_symbol_call():
    """Return the calldata for a symbol() read (selector only, no args)."""
    return SEL_SYMBOL


def encode_allowance_call(holder, spender):
    """ABI-encode an allowance(address,address) read calldata."""
    return _pack_call(SEL_ALLOWANCE, _encode_address(holder), _encode_address(spender))


def decode_decimals(hex_result):
    """Decode the uint8 return from decimals().

    Takes the low byte of the 32-byte word. Raises ValueError if the value
    exceeds MAX_DECIMALS (hostile-value sanity cap from research §1.4).
    """
    value = int(hex_result, 16) & 0xff
    if value > MAX_DECIMALS:
        raise ValueError(
            "token decimals() returned suspicious value %d (cap %d)" % (value, MAX_DECIMALS)
        )
    return value


def decode_symbol(hex_result):
    """Decode the string return from symbol(). Returns Optional[str].

    Tries standard ABI dynamic-string layout (offset word + length word + UTF-8 bytes).
    Falls back to null-trimmed first-32-byte read (legacy bytes32, e.g. MKR).
    Returns None on any failure rather than raising (architecture ADR-006).
    """
    try:
        raw = hex_result
        if raw.startswith("0x"):
            raw = raw[2:]
        data = bytes.fromhex(raw)
        # Standard ABI: word 0 = offset (should be 0x20 = 32), word 1 = length, then bytes
        if len(data) >= 64:
            offset = int.from_bytes(data[0:32], "big")
            if offset == 32:
                length = int.from_bytes(data[32:64], "big")
                if len(data) >= 64 + length:
                    text = data[64 : 64 + length].decode("utf-8")
                    if text.isprintable():
                        return text
        # Fallback: bytes32 null-trimmed (MKR pattern)
        if len(data) >= 32:
            text = data[:32].rstrip(b"\x00").decode("utf-8", errors="replace")
            if text and text.isprintable():
                return text
        return None
    except Exception:
        return None


def decode_allowance(hex_result):
    """Decode the uint256 return from allowance()."""
    return int(hex_result, 16)

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
