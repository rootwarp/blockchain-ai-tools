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
import re
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

MAX_UINT256 = (1 << 256) - 1  # for --approve-max


def human_to_base_units(amount_str, decimals):
    """Convert a human-readable decimal string to base units (integer).

    Conversion path is str -> str -> int. No floating-point arithmetic,
    no decimal.Decimal, no fractions.Fraction (architecture ADR-008).

    Rules:
    - amount_str must be a non-empty string.
    - Negatives are rejected.
    - At most one decimal point is allowed.
    - All characters must be digits (except the optional single dot).
    - The fractional part may not have more digits than `decimals`.
    - Returns 0 for zero-value inputs ("0", "0.0", etc.) — PRD §6 allows this.
    """
    if not isinstance(amount_str, str) or amount_str == "":
        raise ValueError("amount must be a non-empty string, got %r" % (amount_str,))
    if amount_str.startswith("-"):
        raise ValueError("negative amounts are not allowed: %r" % (amount_str,))
    parts = amount_str.split(".")
    if len(parts) > 2:
        raise ValueError("amount has multiple decimal points: %r" % (amount_str,))
    int_part = parts[0]
    frac_part = parts[1] if len(parts) == 2 else ""
    # Validate that both halves contain only digits
    if not re.fullmatch(r"\d*", int_part):
        raise ValueError("non-digit characters in integer part of amount: %r" % (amount_str,))
    if not re.fullmatch(r"\d*", frac_part):
        raise ValueError("non-digit characters in fractional part of amount: %r" % (amount_str,))
    # At least one of int_part / frac_part must be non-empty
    if int_part == "" and frac_part == "":
        raise ValueError("amount has no digits: %r" % (amount_str,))
    if len(frac_part) > decimals:
        raise ValueError(
            "amount has more fractional digits (%d) than token decimals (%d)"
            % (len(frac_part), decimals)
        )
    # Right-pad fractional part to exactly `decimals` digits
    frac_padded = frac_part.ljust(decimals, "0")
    # Concatenate integer and padded fractional parts and parse as base-10 integer
    int_part_normalized = int_part if int_part != "" else "0"
    combined = int_part_normalized + frac_padded
    return int(combined, 10)


def base_units_to_human(amount, decimals):
    """Render a base-unit integer as a human-readable decimal string.

    Uses string manipulation only; no float arithmetic.
    decimals == 0 returns str(amount).
    """
    if decimals == 0:
        return str(amount)
    # Left-zero-pad to at least decimals+1 chars so we can insert the dot
    s = str(amount).zfill(decimals + 1)
    # Insert decimal point decimals places from the right
    int_portion = s[:-decimals]
    frac_portion = s[-decimals:]
    # Strip trailing zeros and trailing dot
    result = int_portion + "." + frac_portion
    result = result.rstrip("0").rstrip(".")
    return result

# === end Layer 1: amount_codec ===

# === Layer 2: contract_reads ===


def fetch_decimals(rpc, url, token):
    """Fetch the token's decimals() value from the chain. FATAL on failure.

    No try/except — RPCError propagates by design (architecture ADR-006).
    Raises ValueError if the returned value exceeds MAX_DECIMALS.

    Args:
        rpc: callable with signature rpc(url, method, params) -> result.
             Defaults to _core.rpc_call when called from tx_assembly.
        url: RPC endpoint URL string.
        token: token contract address (hex string with 0x prefix).

    Returns:
        int: decimals (0 .. MAX_DECIMALS inclusive).
    """
    call_obj = {"to": token, "data": encode_decimals_call()}
    hex_result = rpc(url, "eth_call", [call_obj, "latest"])
    return decode_decimals(hex_result)


def fetch_symbol(rpc, url, token):
    """Fetch the token's symbol() value from the chain. Best-effort.

    Swallows ALL exceptions and returns None on any failure (architecture
    ADR-006: enrichment read — degrading gracefully is correct here).

    Args:
        rpc: callable with signature rpc(url, method, params) -> result.
        url: RPC endpoint URL string.
        token: token contract address (hex string with 0x prefix).

    Returns:
        Optional[str]: the token symbol, or None if unavailable.
    """
    try:
        call_obj = {"to": token, "data": encode_symbol_call()}
        hex_result = rpc(url, "eth_call", [call_obj, "latest"])
        return decode_symbol(hex_result)
    except Exception:
        return None


def fetch_allowance(rpc, url, token, holder, spender):
    """Fetch the current ERC-20 allowance(holder, spender). FATAL on failure.

    No try/except — RPCError propagates by design. The soft-check
    try/except is the caller's responsibility (architecture ADR-006,
    see tx_assembly.do_transfer_from).

    Args:
        rpc: callable with signature rpc(url, method, params) -> result.
        url: RPC endpoint URL string.
        token: token contract address (hex string with 0x prefix).
        holder: token holder address (hex string with 0x prefix).
        spender: spender address (hex string with 0x prefix).

    Returns:
        int: current allowance in base units (uint256).
    """
    call_obj = {"to": token, "data": encode_allowance_call(holder, spender)}
    hex_result = rpc(url, "eth_call", [call_obj, "latest"])
    return decode_allowance(hex_result)

# === end Layer 2: contract_reads ===

# === Layer 2: gas_estimator ===

# Gas policy constants (PRD §9):
GAS_BUFFER_NUM = 12   # Buffer multiplier numerator  (+20% = ×12/10)
GAS_BUFFER_DEN = 10   # Buffer multiplier denominator
GAS_CAP        = 300_000  # Hard ceiling on buffered gas estimate


def _apply_buffer_cap(est):
    """Apply the +20% buffer and 300_000 cap using integer arithmetic.

    Returns min((est * GAS_BUFFER_NUM) // GAS_BUFFER_DEN, GAS_CAP).
    """
    return min((est * GAS_BUFFER_NUM) // GAS_BUFFER_DEN, GAS_CAP)


# Why there is no try/except in estimate_gas:
#
# A silent fallback to a hardcoded gas number would let a transaction that
# will DEFINITELY REVERT on-chain get signed and broadcast, burning its full
# gas budget with no recourse for the operator. The right behaviour on an
# eth_estimateGas failure is to surface the node's error message and stop
# immediately so the operator can investigate (wrong amount, insufficient
# allowance, contract paused, etc.) before spending real gas.
#
# See architecture ADR-007 and research §03 for the full rationale. This
# RPCError propagation path is LOAD-BEARING. Do NOT add a try/except around
# the rpc() call below — doing so would silently break the no-fallback
# guarantee and require a new ADR to justify the exception.
def estimate_gas(rpc, url, sender, token, data):
    """Estimate gas for an ERC-20 call and return the buffered+capped value.

    Builds a {from, to, data, value:"0x0"} call object, queries
    eth_estimateGas against "latest", parses the hex result via
    _core.parse_hex_int, and returns _apply_buffer_cap(estimate).

    NO try/except — RPCError propagates by design (architecture ADR-007).

    Args:
        rpc: callable rpc(url, method, params) -> result (or raises RPCError).
        url: RPC endpoint URL string.
        sender: the `from` address (hex string with 0x prefix).
        token: the token contract address (`to` field).
        data: ABI-encoded calldata (hex string with 0x prefix).

    Returns:
        int: buffered and capped gas limit.
    """
    call_obj = {
        "from": sender,
        "to": token,
        "data": data,
        "value": "0x0",
    }
    hex_result = rpc(url, "eth_estimateGas", [call_obj, "latest"])
    est = _core.parse_hex_int(hex_result)
    return _apply_buffer_cap(est)

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
