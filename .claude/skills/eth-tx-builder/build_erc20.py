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


def render_summary(ctx):
    """Return the human-readable summary block as a string (pure — no stderr writes).

    PRD §16 fields. All labels are stable so TestSummary can grep them.

    Expected ctx keys:
        operation        str    e.g. "transfer", "approve", "transfer-from"
        network          str    e.g. "mainnet"
        chain_id         int    e.g. 1
        token            str    token contract address
        symbol           Optional[str]  None -> "(unavailable)"
        decimals         int
        human_amount     str    e.g. "1.5"
        base_amount      int    base-unit integer
        is_max_uint      bool   True -> render base_amount as "MAX UINT256"
        from_            str    sender address (transfer / transfer-from)
        to               str    recipient address (transfer / transfer-from)
        -- approve-specific keys (when present) --
        spender          str    (approve)
        holder           str    (approve / transfer-from)
        nonce            int
        gas              int
        max_fee          int    wei
        max_priority_fee int    wei
    """
    op = ctx["operation"]
    symbol_display = ctx["symbol"] if ctx.get("symbol") is not None else "(unavailable)"
    base_amt_display = (
        "MAX UINT256" if ctx.get("is_max_uint") else str(ctx["base_amount"])
    )
    human_display = (
        "MAX UINT256" if ctx.get("is_max_uint") else ctx["human_amount"]
    )

    lines = [
        "--- ERC-20 transaction summary ---",
        "operation         : %s" % op,
        "network           : %s (chainId %s)" % (ctx["network"], ctx["chain_id"]),
        "token             : %s" % ctx["token"],
        "symbol            : %s" % symbol_display,
        "decimals          : %s" % ctx["decimals"],
        "amount (human)    : %s" % human_display,
        "amount (base units): %s" % base_amt_display,
    ]

    # Role-specific address lines per operation
    if op == "transfer":
        lines.append("from (sender)     : %s" % ctx.get("from_", ""))
        lines.append("to (recipient)    : %s" % ctx.get("to", ""))
    elif op == "approve":
        lines.append("holder (sender)   : %s" % ctx.get("from_", ctx.get("holder", "")))
        lines.append("spender           : %s" % ctx.get("spender", ""))
    elif op == "transfer-from":
        lines.append("source (from)     : %s" % ctx.get("from_", ""))
        lines.append("to (recipient)    : %s" % ctx.get("to", ""))
        lines.append("signer / spender  : %s" % ctx.get("sender", ctx.get("signer_spender", "")))

    lines += [
        "nonce             : %s" % ctx["nonce"],
        "gas               : %s" % ctx["gas"],
        "maxFeePerGas      : %s wei" % ctx["max_fee"],
        "maxPriorityFeePerGas: %s wei" % ctx["max_priority_fee"],
        "----------------------------------",
    ]
    return "\n".join(lines) + "\n"


def print_summary(ctx):
    """Write the rendered summary block to stderr."""
    text = render_summary(ctx)
    sys.stderr.write(text)
    if not text.endswith("\n"):
        sys.stderr.write("\n")


def warn_approve_max(symbol, token, spender):
    """Write the --approve-max WARNING block to stderr.

    Prints a multi-line WARNING: block per PRD §7. When symbol is None,
    renders the symbol placeholder as '<unknown>'.
    """
    sym = symbol if symbol is not None else "<unknown>"
    msg = (
        "WARNING: --approve-max grants UNLIMITED transfer authority on"
        " %s (%s) to spender %s.\n"
        "Revoke later with approve(spender, 0) if no longer needed.\n"
        % (sym, token, spender)
    )
    sys.stderr.write(msg)


def warn_low_allowance(holder, spender, current, requested, decimals):
    """Write a low-allowance WARNING line to stderr.

    Uses base_units_to_human for the human-readable figures.
    """
    current_human = base_units_to_human(current, decimals)
    requested_human = base_units_to_human(requested, decimals)
    msg = (
        "WARNING: current allowance is %d (%s); requested transfer is %d (%s)."
        " This transaction will revert unless allowance is increased before broadcast.\n"
        % (current, current_human, requested, requested_human)
    )
    sys.stderr.write(msg)


def warn_allowance_check_skipped(reason):
    """Write an allowance-check-skipped WARNING line to stderr."""
    sys.stderr.write(
        "WARNING: allowance soft-check skipped: %s. Build continues.\n" % reason
    )


def warn_symbol_unavailable():
    """Write a symbol-unavailable WARNING line to stderr (optional, info-only)."""
    sys.stderr.write(
        "WARNING: token symbol() unavailable; summary may be less informative.\n"
    )


def emit_warning(kind, payload):
    """Dispatch a (kind, payload_dict) warning tuple to the matching warn_* emitter.

    kind must be one of: "approve_max", "low_allowance",
    "allowance_check_skipped", "symbol_unavailable".

    Raises ValueError on an unknown kind (defensive — a typo in tx_assembly
    should surface in tests rather than silently dropping a warning).
    """
    if kind == "approve_max":
        warn_approve_max(**payload)
    elif kind == "low_allowance":
        warn_low_allowance(**payload)
    elif kind == "allowance_check_skipped":
        warn_allowance_check_skipped(**payload)
    elif kind == "symbol_unavailable":
        warn_symbol_unavailable()
    else:
        raise ValueError("unknown warning kind: %r" % (kind,))

# === end Layer 2: summary ===

# === Layer 3: tx_assembly ===


def _build_eip1559_envelope(chain_id, nonce, to, data, gas, base_fee, tip):
    """Assemble the v1-shape TxRequest dict with all numeric fields as decimal strings.

    Args:
        chain_id: int network chain id.
        nonce:    int account nonce.
        to:       str token contract address (0x-prefixed).
        data:     str ABI-encoded calldata (0x-prefixed).
        gas:      int gas limit (buffered + capped).
        base_fee: int base fee in wei (from latest block).
        tip:      int max priority fee in wei.

    Returns:
        dict: TxRequest JSON-ready dict matching the eth-signer-mcp contract.
    """
    return {
        "type": "eip1559",
        "chainId": str(chain_id),
        "nonce": str(nonce),
        "to": to,
        "value": "0",
        "data": data,
        "gas": str(gas),
        "maxFeePerGas": str(_core.compute_max_fee(base_fee, tip)),
        "maxPriorityFeePerGas": str(tip),
    }


def do_transfer(network, token, to, amount, sender, *, rpc=_core.rpc_call):
    """Build an ERC-20 transfer(to, amount) TxRequest.

    Follows the architecture eight-step skeleton (ADR-004). Returns
    (tx_dict, summary_ctx, warnings_list). Never writes to stdout/stderr.

    Args:
        network: str network name (e.g. "mainnet", "hoodi").
        token:   str ERC-20 contract address (0x + 40 hex, pre-validated).
        to:      str recipient address (0x + 40 hex, pre-validated).
        amount:  str human-readable amount string (e.g. "1.5").
        sender:  str signing account address (0x + 40 hex, pre-validated).
        rpc:     callable rpc(url, method, params) — injected for testing.

    Returns:
        tuple: (tx_dict, summary_ctx, warnings_list)
    """
    # Step 1: Resolve network.
    chain_id, url = _core.network_config(network)
    # Steps 2–3: Fetch decimals (FATAL) and symbol (best-effort).
    decimals = fetch_decimals(rpc, url, token)
    symbol = fetch_symbol(rpc, url, token)
    # Step 4: Convert human amount to base units.
    amount_base = human_to_base_units(amount, decimals)
    # Step 5: Build calldata.
    calldata = encode_transfer(to, amount_base)
    # Step 6: Estimate gas — FATAL; RPCError propagates (ADR-007, no try/except).
    gas = estimate_gas(rpc, url, sender, token, calldata)
    # Step 7: Fetch nonce and fees.
    nonce    = _core.fetch_nonce(rpc, url, sender)
    base_fee = _core.fetch_base_fee(rpc, url)
    tip      = _core.fetch_tip(rpc, url)
    max_fee  = _core.compute_max_fee(base_fee, tip)
    # Step 8: Assemble tx dict.
    tx_dict = _build_eip1559_envelope(chain_id, nonce, token, calldata, gas, base_fee, tip)
    # Build summary context with stable keys.
    summary_ctx = {
        "operation":      "transfer",
        "network":        network,
        "chain_id":       chain_id,
        "token":          token,
        "symbol":         symbol,
        "decimals":       decimals,
        "human_amount":   amount,
        "base_amount":    amount_base,
        "is_max_uint":    False,
        "from_":          sender,
        "to":             to,
        "nonce":          nonce,
        "gas":            gas,
        "max_fee":        max_fee,
        "max_priority_fee": tip,
    }
    return (tx_dict, summary_ctx, [])


def do_approve(network, token, spender, amount, sender, *,
               approve_max=False, rpc=_core.rpc_call):
    """Build an ERC-20 approve(spender, amount) TxRequest.

    When approve_max=True, amount must be None; MAX_UINT256 is used and an
    "approve_max" warning is queued. Returns (tx_dict, summary_ctx, warnings_list).

    Args:
        network:     str network name.
        token:       str ERC-20 contract address (pre-validated).
        spender:     str spender address (pre-validated).
        amount:      str human-readable amount, or None when approve_max=True.
        sender:      str signing account address (pre-validated).
        approve_max: bool — if True, approve MAX_UINT256 and queue a warning.
        rpc:         callable — injected for testing.

    Returns:
        tuple: (tx_dict, summary_ctx, warnings_list)
    """
    warnings = []
    # Step 1: Resolve network.
    chain_id, url = _core.network_config(network)
    # Steps 2–3: Fetch decimals (FATAL) and symbol (best-effort).
    decimals = fetch_decimals(rpc, url, token)
    symbol = fetch_symbol(rpc, url, token)
    # Step 4: Resolve amount.
    if approve_max:
        amount_base = MAX_UINT256
        warnings.append(("approve_max", {
            "symbol": symbol,
            "token": token,
            "spender": spender,
        }))
    else:
        amount_base = human_to_base_units(amount, decimals)
    # Step 5: Build calldata.
    calldata = encode_approve(spender, amount_base)
    # Step 6: Estimate gas — FATAL; RPCError propagates (ADR-007).
    gas = estimate_gas(rpc, url, sender, token, calldata)
    # Step 7: Fetch nonce and fees.
    nonce    = _core.fetch_nonce(rpc, url, sender)
    base_fee = _core.fetch_base_fee(rpc, url)
    tip      = _core.fetch_tip(rpc, url)
    max_fee  = _core.compute_max_fee(base_fee, tip)
    # Step 8: Assemble tx dict.
    tx_dict = _build_eip1559_envelope(chain_id, nonce, token, calldata, gas, base_fee, tip)
    # Build summary context.
    summary_ctx = {
        "operation":      "approve",
        "network":        network,
        "chain_id":       chain_id,
        "token":          token,
        "symbol":         symbol,
        "decimals":       decimals,
        "human_amount":   "MAX UINT256" if approve_max else amount,
        "base_amount":    amount_base,
        "is_max_uint":    approve_max,
        "from_":          sender,       # holder == sender for approve
        "holder":         sender,
        "spender":        spender,
        "nonce":          nonce,
        "gas":            gas,
        "max_fee":        max_fee,
        "max_priority_fee": tip,
    }
    return (tx_dict, summary_ctx, warnings)


def do_transfer_from(network, token, from_, to, amount, sender, *, rpc=_core.rpc_call):
    """Build an ERC-20 transferFrom(from, to, amount) TxRequest.

    Performs a soft allowance check: if fetch_allowance raises RPCError the
    check is skipped (warning queued); if allowance < amount_base a low-
    allowance warning is queued. In both cases the tx is still built and
    returned. This is the ONLY try/except _core.RPCError outside cli_dispatch
    (architecture ADR-007). Returns (tx_dict, summary_ctx, warnings_list).

    Args:
        network: str network name.
        token:   str ERC-20 contract address (pre-validated).
        from_:   str token holder address (pre-validated).
        to:      str recipient address (pre-validated).
        amount:  str human-readable amount string.
        sender:  str signer / spender address (pre-validated).
        rpc:     callable — injected for testing.

    Returns:
        tuple: (tx_dict, summary_ctx, warnings_list)
    """
    warnings = []
    # Step 1: Resolve network.
    chain_id, url = _core.network_config(network)
    # Steps 2–3: Fetch decimals (FATAL) and symbol (best-effort).
    decimals = fetch_decimals(rpc, url, token)
    symbol = fetch_symbol(rpc, url, token)
    # Step 4: Convert human amount to base units.
    amount_base = human_to_base_units(amount, decimals)
    # Step 5: Build calldata.
    calldata = encode_transfer_from(from_, to, amount_base)
    # Step 6a: Soft allowance check (the ONE try/except RPCError outside main).
    try:
        current = fetch_allowance(rpc, url, token, from_, sender)
    except _core.RPCError as e:
        warnings.append(("allowance_check_skipped", {"reason": str(e)}))
    else:
        if current < amount_base:
            warnings.append(("low_allowance", {
                "holder":    from_,
                "spender":   sender,
                "current":   current,
                "requested": amount_base,
                "decimals":  decimals,
            }))
    # Step 6b: Estimate gas — FATAL; RPCError propagates (ADR-007, no try/except).
    gas = estimate_gas(rpc, url, sender, token, calldata)
    # Step 7: Fetch nonce and fees.
    nonce    = _core.fetch_nonce(rpc, url, sender)
    base_fee = _core.fetch_base_fee(rpc, url)
    tip      = _core.fetch_tip(rpc, url)
    max_fee  = _core.compute_max_fee(base_fee, tip)
    # Step 8: Assemble tx dict.
    tx_dict = _build_eip1559_envelope(chain_id, nonce, token, calldata, gas, base_fee, tip)
    # Build summary context.
    summary_ctx = {
        "operation":        "transfer-from",
        "network":          network,
        "chain_id":         chain_id,
        "token":            token,
        "symbol":           symbol,
        "decimals":         decimals,
        "human_amount":     amount,
        "base_amount":      amount_base,
        "is_max_uint":      False,
        "from_":            from_,
        "to":               to,
        "sender":           sender,
        "signer_spender":   sender,
        "nonce":            nonce,
        "gas":              gas,
        "max_fee":          max_fee,
        "max_priority_fee": tip,
    }
    return (tx_dict, summary_ctx, warnings)

# === end Layer 3: tx_assembly ===

# === Layer 4: cli_dispatch ===


def _build_parser():
    """Build the top-level argparse parser with three ERC-20 subcommands.

    Subcommands:
        transfer       — transfer(to, amount)
        approve        — approve(spender, amount | --approve-max)
        transfer-from  — transferFrom(from, to, amount)

    Network choices come from sorted(_core.NETWORKS) so Phase 2 additions
    propagate automatically (architecture Assumption 15).

    Returns:
        argparse.ArgumentParser
    """
    parser = argparse.ArgumentParser(
        description="Build a ready-to-sign EIP-1559 ERC-20 TxRequest JSON for eth-signer-mcp."
    )
    sub = parser.add_subparsers(dest="op", required=True)

    # --- transfer ---
    p_transfer = sub.add_parser("transfer", help="ERC-20 transfer(to, amount)")
    p_transfer.add_argument("--network", required=True,
                            choices=sorted(_core.NETWORKS),
                            help="network name")
    p_transfer.add_argument("--token", required=True,
                            help="ERC-20 contract address (0x + 40 hex)")
    p_transfer.add_argument("--to", required=True,
                            help="recipient address (0x + 40 hex)")
    p_transfer.add_argument("--amount", required=True,
                            help="human-readable token amount (e.g. 1.5)")
    p_transfer.add_argument("--sender", required=True,
                            help="signing account address (0x + 40 hex)")

    # --- approve ---
    p_approve = sub.add_parser("approve", help="ERC-20 approve(spender, amount)")
    p_approve.add_argument("--network", required=True,
                           choices=sorted(_core.NETWORKS),
                           help="network name")
    p_approve.add_argument("--token", required=True,
                           help="ERC-20 contract address (0x + 40 hex)")
    p_approve.add_argument("--spender", required=True,
                           help="spender address (0x + 40 hex)")
    p_approve.add_argument("--sender", required=True,
                           help="signing account address (0x + 40 hex)")
    # --amount XOR --approve-max (architecture Assumption 13 / A14; PRD §7)
    amt_group = p_approve.add_mutually_exclusive_group(required=True)
    amt_group.add_argument("--amount", default=None,
                           help="human-readable token amount to approve (e.g. 1.5)")
    amt_group.add_argument("--approve-max", action="store_true",
                           dest="approve_max",
                           help="approve MAX_UINT256 (unlimited); prints loud WARNING:")

    # --- transfer-from ---
    p_tf = sub.add_parser("transfer-from",
                           help="ERC-20 transferFrom(from, to, amount)")
    p_tf.add_argument("--network", required=True,
                      choices=sorted(_core.NETWORKS),
                      help="network name")
    p_tf.add_argument("--token", required=True,
                      help="ERC-20 contract address (0x + 40 hex)")
    p_tf.add_argument("--from", dest="from_", required=True,
                      help="token holder address (0x + 40 hex)")
    p_tf.add_argument("--to", required=True,
                      help="recipient address (0x + 40 hex)")
    p_tf.add_argument("--amount", required=True,
                      help="human-readable token amount (e.g. 1.5)")
    p_tf.add_argument("--sender", required=True,
                      help="signing / spender account address (0x + 40 hex)")

    return parser


def _validate_addresses(args):
    """Validate every address argument present on the parsed args namespace.

    Calls _core.validate_hex_address on each attribute that could hold an
    address. Raises ValueError (caught by main) on any malformed value.
    Address validation happens exactly once, here, so do_* can accept
    pre-validated hex (architecture ADR-010).
    """
    for attr in ("token", "to", "spender", "from_", "sender"):
        value = getattr(args, attr, None)
        if value is not None:
            _core.validate_hex_address(value)


def main(argv=None):
    """Parse CLI args, dispatch to the appropriate do_* function, print results.

    This is the ONLY try/except (ValueError, _core.RPCError) in build_erc20.py
    (architecture ADR-007). Lower layers raise; this function catches and
    formats error: messages to stderr, returning exit code 1.

    Stdout = JSON only (architecture ADR-009).
    Stderr = summary block + WARNING: lines + error: messages.

    Args:
        argv: list of str CLI arguments (defaults to sys.argv[1:]).

    Returns:
        int: 0 on success, 1 on any ValueError or RPCError.
    """
    parser = _build_parser()
    args = parser.parse_args(argv)
    try:
        _validate_addresses(args)
        if args.op == "transfer":
            tx, ctx, warns = do_transfer(
                args.network, args.token, args.to, args.amount, args.sender,
            )
        elif args.op == "approve":
            tx, ctx, warns = do_approve(
                args.network, args.token, args.spender, args.amount,
                args.sender,
                approve_max=args.approve_max,
            )
        elif args.op == "transfer-from":
            tx, ctx, warns = do_transfer_from(
                args.network, args.token, args.from_, args.to,
                args.amount, args.sender,
            )
        for w_kind, w_payload in warns:
            emit_warning(w_kind, w_payload)
        print_summary(ctx)
        print(json.dumps(tx, indent=2))
        return 0
    except (ValueError, _core.RPCError) as e:
        print("error: %s" % e, file=sys.stderr)
        return 1

# === end Layer 4: cli_dispatch ===


if __name__ == "__main__":
    sys.exit(main())
