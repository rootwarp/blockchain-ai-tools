import io
import inspect
import json
import unittest
import unittest.mock as mock

import build_erc20 as b


# Duplicated from test_build_send_eth.py because the v1 test file is read-only
# for Phase 1 (architecture A14).
def make_fake_rpc(results, errors=()):
    """Return a fake rpc(url, method, params). `results` maps method->value;
    methods in `errors` raise RPCError."""
    def _rpc(url, method, params):
        if method in errors:
            raise b._core.RPCError("simulated failure for %s" % method)
        return results[method]
    return _rpc


class TestAmountCodec(unittest.TestCase):
    # --- MAX_UINT256 ---

    def test_max_uint256_value(self):
        self.assertEqual(b.MAX_UINT256, (1 << 256) - 1)

    # --- human_to_base_units positive vectors ---

    def test_zero_6_decimals(self):
        self.assertEqual(b.human_to_base_units("0", 6), 0)

    def test_zero_point_zero_6_decimals(self):
        self.assertEqual(b.human_to_base_units("0.0", 6), 0)

    def test_one_18_decimals(self):
        self.assertEqual(b.human_to_base_units("1", 18), 10 ** 18)

    def test_one_point_five_6_decimals(self):
        self.assertEqual(b.human_to_base_units("1.5", 6), 1_500_000)

    def test_small_fractional_18_decimals(self):
        self.assertEqual(b.human_to_base_units("0.000001", 18), 10 ** 12)

    def test_large_with_fraction_6_decimals(self):
        self.assertEqual(b.human_to_base_units("1000000.5", 6), 1_000_000_500_000)

    def test_high_precision_18_decimals(self):
        self.assertEqual(
            b.human_to_base_units("0.123456789012345678", 18),
            123_456_789_012_345_678,
        )

    def test_zero_decimals(self):
        self.assertEqual(b.human_to_base_units("42", 0), 42)

    # --- human_to_base_units rejection vectors ---

    def test_empty_string_raises(self):
        with self.assertRaises(ValueError):
            b.human_to_base_units("", 6)

    def test_negative_raises(self):
        with self.assertRaises(ValueError):
            b.human_to_base_units("-1", 6)

    def test_multi_dot_two_dots_raises(self):
        with self.assertRaises(ValueError):
            b.human_to_base_units("1..5", 6)

    def test_multi_dot_three_parts_raises(self):
        with self.assertRaises(ValueError):
            b.human_to_base_units("1.5.0", 6)

    def test_non_digit_raises(self):
        with self.assertRaises(ValueError):
            b.human_to_base_units("abc", 6)

    def test_too_many_fractional_digits_raises(self):
        # "1.0000001" has 7 fractional digits but decimals=6
        with self.assertRaises(ValueError):
            b.human_to_base_units("1.0000001", 6)

    def test_dot_only_raises(self):
        with self.assertRaises(ValueError):
            b.human_to_base_units(".", 6)

    # --- base_units_to_human round-trip ---

    def test_round_trip_zero_6_decimals(self):
        self.assertEqual(b.base_units_to_human(0, 6), "0")

    def test_round_trip_one_point_five(self):
        self.assertEqual(b.base_units_to_human(1_500_000, 6), "1.5")

    def test_round_trip_18_decimals(self):
        self.assertEqual(b.base_units_to_human(10 ** 18, 18), "1")

    def test_round_trip_small_fractional(self):
        self.assertEqual(b.base_units_to_human(10 ** 12, 18), "0.000001")

    def test_round_trip_large_with_fraction(self):
        self.assertEqual(b.base_units_to_human(1_000_000_500_000, 6), "1000000.5")

    def test_round_trip_zero_decimals(self):
        self.assertEqual(b.base_units_to_human(42, 0), "42")

    # --- No-float invariant (ADR-008) ---

    def test_no_float_in_human_to_base_units(self):
        """The substring 'float(' must not appear in human_to_base_units source."""
        src = inspect.getsource(b.human_to_base_units)
        self.assertNotIn("float(", src)


class TestAbiCodec(unittest.TestCase):
    # --- Selector constants ---

    def test_sel_transfer(self):
        self.assertEqual(b.SEL_TRANSFER, "0xa9059cbb")

    def test_sel_approve(self):
        self.assertEqual(b.SEL_APPROVE, "0x095ea7b3")

    def test_sel_transfer_from(self):
        self.assertEqual(b.SEL_TRANSFER_FROM, "0x23b872dd")

    def test_sel_decimals(self):
        self.assertEqual(b.SEL_DECIMALS, "0x313ce567")

    def test_sel_symbol(self):
        self.assertEqual(b.SEL_SYMBOL, "0x95d89b41")

    def test_sel_allowance(self):
        self.assertEqual(b.SEL_ALLOWANCE, "0xdd62ed3e")

    # --- _encode_address ---

    def test_encode_address_lowercase_pads(self):
        # Mixed-case input -> lowercase, left-padded to 64 hex chars
        addr = "0x890e560a6012bFA5d0d71a4a107dBa4Aed698f38"
        result = b._encode_address(addr)
        self.assertEqual(len(result), 64)
        self.assertEqual(result, "000000000000000000000000890e560a6012bfa5d0d71a4a107dba4aed698f38")

    def test_encode_address_all_zeros(self):
        result = b._encode_address("0x" + "0" * 40)
        self.assertEqual(result, "0" * 64)

    # --- _encode_uint256 ---

    def test_encode_uint256_zero(self):
        self.assertEqual(b._encode_uint256(0), "0" * 64)

    def test_encode_uint256_one(self):
        self.assertEqual(b._encode_uint256(1), "0" * 63 + "1")

    def test_encode_uint256_max(self):
        self.assertEqual(b._encode_uint256((1 << 256) - 1), "f" * 64)

    def test_encode_uint256_negative_raises(self):
        with self.assertRaises(ValueError):
            b._encode_uint256(-1)

    def test_encode_uint256_overflow_raises(self):
        with self.assertRaises(ValueError):
            b._encode_uint256(1 << 256)

    # --- _pack_call ---

    def test_pack_call_usdc_transfer_vector_a(self):
        # Vector A from research §01: transfer to 0x890e...698f38, amount 3,383,250,000
        # Etherscan tx 0x1c6e8fe429...
        to_addr = "0x890e560a6012bfa5d0d71a4a107dba4aed698f38"
        amount = 3_383_250_000  # 0xc9a84c50
        result = b._pack_call(
            b.SEL_TRANSFER,
            b._encode_address(to_addr),
            b._encode_uint256(amount),
        )
        expected = (
            "0xa9059cbb"
            "000000000000000000000000890e560a6012bfa5d0d71a4a107dba4aed698f38"
            "00000000000000000000000000000000000000000000000000000000c9a84c50"
        )
        self.assertEqual(result, expected)

    # --- encode_transfer ---

    def test_encode_transfer_vector_a(self):
        to_addr = "0x890e560a6012bfa5d0d71a4a107dba4aed698f38"
        amount = 3_383_250_000
        result = b.encode_transfer(to_addr, amount)
        expected = (
            "0xa9059cbb"
            "000000000000000000000000890e560a6012bfa5d0d71a4a107dba4aed698f38"
            "00000000000000000000000000000000000000000000000000000000c9a84c50"
        )
        self.assertEqual(result, expected)

    def test_encode_transfer_vector_b(self):
        # Vector B: transfer to 0xa490...f98a, amount 2,500,000,000
        to_addr = "0xa49053f705a560f0717bc2e96dddcfe7edb7f98a"
        amount = 2_500_000_000  # 0x9502f900
        result = b.encode_transfer(to_addr, amount)
        expected = (
            "0xa9059cbb"
            "000000000000000000000000a49053f705a560f0717bc2e96dddcfe7edb7f98a"
            "000000000000000000000000000000000000000000000000000000009502f900"
        )
        self.assertEqual(result, expected)

    # --- encode_approve ---

    def test_encode_approve_bit_pattern(self):
        # Independent expected: selector || zero-pad addr || zero-pad uint256
        # Uses format(int(addr,16),"064x") — a different code path from the
        # production string-slicing _encode_address helper (Fix 1).
        spender = "0xa49053f705a560f0717bc2e96dddcfe7edb7f98a"
        amount = 1_000_000  # 1 USDC at 6 decimals
        result = b.encode_approve(spender, amount)
        expected = (
            "0x095ea7b3"
            + format(int(spender, 16), "064x")
            + format(amount, "064x")
        )
        self.assertEqual(result, expected)

    # --- encode_transfer_from ---

    def test_encode_transfer_from_bit_pattern(self):
        # Independent expected: selector || addr || addr || uint256
        # Uses format(int(addr,16),"064x") — differs from production helper (Fix 1).
        from_ = "0x890e560a6012bfa5d0d71a4a107dba4aed698f38"
        to = "0xa49053f705a560f0717bc2e96dddcfe7edb7f98a"
        amount = 500_000
        result = b.encode_transfer_from(from_, to, amount)
        expected = (
            "0x23b872dd"
            + format(int(from_, 16), "064x")
            + format(int(to, 16), "064x")
            + format(amount, "064x")
        )
        self.assertEqual(result, expected)

    # --- encode_decimals_call / encode_symbol_call ---

    def test_encode_decimals_call_equals_selector(self):
        self.assertEqual(b.encode_decimals_call(), b.SEL_DECIMALS)

    def test_encode_symbol_call_equals_selector(self):
        self.assertEqual(b.encode_symbol_call(), b.SEL_SYMBOL)

    # --- encode_allowance_call ---

    def test_encode_allowance_call_bit_pattern(self):
        # Independent expected: selector || addr || addr
        # Uses format(int(addr,16),"064x") — different code path from production (Fix 1).
        holder = "0x890e560a6012bfa5d0d71a4a107dba4aed698f38"
        spender = "0xa49053f705a560f0717bc2e96dddcfe7edb7f98a"
        result = b.encode_allowance_call(holder, spender)
        expected = (
            "0xdd62ed3e"
            + format(int(holder, 16), "064x")
            + format(int(spender, 16), "064x")
        )
        self.assertEqual(result, expected)

    # --- decode_decimals ---

    def test_decode_decimals_zero(self):
        self.assertEqual(b.decode_decimals("0x" + "0" * 64), 0)

    def test_decode_decimals_six(self):
        self.assertEqual(b.decode_decimals("0x" + "0" * 62 + "06"), 6)

    def test_decode_decimals_18(self):
        self.assertEqual(b.decode_decimals("0x" + "0" * 62 + "12"), 18)

    def test_decode_decimals_24(self):
        self.assertEqual(b.decode_decimals("0x" + "0" * 62 + "18"), 24)

    def test_decode_decimals_36_ok(self):
        self.assertEqual(b.decode_decimals("0x" + "0" * 62 + "24"), 36)

    def test_decode_decimals_37_raises(self):
        with self.assertRaises(ValueError):
            b.decode_decimals("0x" + "0" * 62 + "25")

    def test_decode_decimals_255_hostile_raises(self):
        # 255 > MAX_DECIMALS; the low-byte mask gives 255 which exceeds 36
        with self.assertRaises(ValueError):
            b.decode_decimals("0x" + "0" * 62 + "ff")

    # --- decode_symbol ---

    def test_decode_symbol_standard_abi_usdc(self):
        # Standard ABI encoding of "USDC":
        # word 0 = offset 0x20 (32)
        # word 1 = length 4
        # word 2 = b"USDC" right-padded to 32 bytes
        offset_word = "0020".zfill(64)
        length_word = "0004".zfill(64)
        payload = b"USDC" + b"\x00" * 28
        data_hex = offset_word + length_word + payload.hex()
        result = b.decode_symbol("0x" + data_hex)
        self.assertEqual(result, "USDC")

    def test_decode_symbol_bytes32_mkr_style(self):
        # Legacy bytes32: b"MKR" + 29 null bytes
        data = b"MKR" + b"\x00" * 29
        result = b.decode_symbol("0x" + data.hex())
        self.assertEqual(result, "MKR")

    def test_decode_symbol_malformed_returns_none(self):
        # Completely malformed: should return None, not raise
        result = b.decode_symbol("0xdeadbeef")
        self.assertIsNone(result)

    def test_decode_symbol_empty_data_returns_none(self):
        result = b.decode_symbol("0x")
        self.assertIsNone(result)

    # --- decode_decimals non-str guard (Fix 4) ---

    def test_decode_decimals_non_str_int_raises_value_error(self):
        """Non-string input must raise ValueError, not TypeError (Fix 4)."""
        with self.assertRaises(ValueError):
            b.decode_decimals(6)

    def test_decode_decimals_non_str_dict_raises_value_error(self):
        """Dict input must raise ValueError, not TypeError (Fix 4)."""
        with self.assertRaises(ValueError):
            b.decode_decimals({"result": "0x06"})

    # --- decode_symbol empty ABI string -> None (Fix 3) ---

    def test_decode_symbol_empty_abi_string_returns_none(self):
        """Valid ABI string with length=0 must return None, not '' (Fix 3).

        ''.isprintable() is True, so without the `text and` guard the standard-ABI
        path would return '' instead of falling through to None.
        """
        offset_word = "0020".zfill(64)
        length_word = "0000".zfill(64)  # length = 0
        padding = "00" * 32             # minimum one word of padding
        data_hex = offset_word + length_word + padding
        result = b.decode_symbol("0x" + data_hex)
        self.assertIsNone(result)

    # --- decode_allowance ---

    def test_decode_allowance_zero(self):
        self.assertEqual(b.decode_allowance("0x" + "0" * 64), 0)

    def test_decode_allowance_max_uint256(self):
        self.assertEqual(b.decode_allowance("0x" + "f" * 64), (1 << 256) - 1)

    # --- decode_allowance non-str guard (Fix 4) ---

    def test_decode_allowance_non_str_int_raises_value_error(self):
        """Non-string input must raise ValueError, not TypeError (Fix 4)."""
        with self.assertRaises(ValueError):
            b.decode_allowance(42)

    def test_decode_allowance_non_str_dict_raises_value_error(self):
        """Dict input must raise ValueError, not TypeError (Fix 4)."""
        with self.assertRaises(ValueError):
            b.decode_allowance({"result": "0x0a"})

    # --- MAX_DECIMALS constant ---

    def test_max_decimals_value(self):
        self.assertEqual(b.MAX_DECIMALS, 36)


class TestDecodeSymbolPolished(unittest.TestCase):
    """Issue 3.4: Per-variant decode_symbol tests (ADR-013 bounded catalog).

    Fixtures are lifted verbatim from ADR-013's worked examples. Each variant
    has a happy-path fixture + a malformed-input fixture that returns None.
    The Phase 1 regression tests (USDC + MKR) are pinned here to confirm the
    refactor does not break existing behaviour. The "outside-the-catalog"
    test guards against scope creep.
    """

    # -----------------------------------------------------------------------
    # Regression: Phase 1 helpers still work via decode_symbol (after refactor)
    # -----------------------------------------------------------------------

    def test_phase1_usdc_standard_abi_string(self):
        """Phase 1 regression: standard ABI 'string' for USDC still decodes to 'USDC'."""
        # Standard ABI encoding of "USDC": offset=32, length=4, payload right-padded
        offset_word = "0020".zfill(64)
        length_word = "0004".zfill(64)
        payload = b"USDC" + b"\x00" * 28
        hex_result = "0x" + offset_word + length_word + payload.hex()
        self.assertEqual(b.decode_symbol(hex_result), "USDC")

    def test_phase1_mkr_null_trimmed_bytes32(self):
        """Phase 1 regression: null-padded bytes32 for MKR still decodes to 'MKR'."""
        # MKR: 3 bytes "MKR" + 29 null bytes (ADR-013 Format A example)
        data = b"MKR" + b"\x00" * 29
        hex_result = "0x" + data.hex()
        self.assertEqual(b.decode_symbol(hex_result), "MKR")

    # -----------------------------------------------------------------------
    # _try_decode_abi_string helper (extracted Phase 1 logic)
    # -----------------------------------------------------------------------

    def test_try_decode_abi_string_usdc_happy(self):
        """_try_decode_abi_string decodes a standard ABI string correctly."""
        offset_word = "0020".zfill(64)
        length_word = "0004".zfill(64)
        payload = b"USDC" + b"\x00" * 28
        hex_result = "0x" + offset_word + length_word + payload.hex()
        self.assertEqual(b._try_decode_abi_string(hex_result), "USDC")

    def test_try_decode_abi_string_empty_returns_none(self):
        """_try_decode_abi_string: ABI string with length=0 returns None (not '')."""
        offset_word = "0020".zfill(64)
        length_word = "0000".zfill(64)
        padding = "00" * 32
        hex_result = "0x" + offset_word + length_word + padding
        self.assertIsNone(b._try_decode_abi_string(hex_result))

    def test_try_decode_abi_string_malformed_wrong_offset_returns_none(self):
        """_try_decode_abi_string: wrong offset word (not 0x20) returns None."""
        # offset = 0x40 (64) instead of 0x20 (32) — not standard ABI string
        offset_word = "0040".zfill(64)
        length_word = "0004".zfill(64)
        payload = b"USDC" + b"\x00" * 28
        hex_result = "0x" + offset_word + length_word + payload.hex()
        self.assertIsNone(b._try_decode_abi_string(hex_result))

    def test_try_decode_abi_string_truncated_returns_none(self):
        """_try_decode_abi_string: truncated response (no length word) returns None."""
        # Only 32 bytes — too short for offset + length + data
        data = b"\x00" * 31 + b"\x20"  # just the offset word
        self.assertIsNone(b._try_decode_abi_string("0x" + data.hex()))

    def test_try_decode_abi_string_empty_hex_returns_none(self):
        """_try_decode_abi_string: '0x' (empty) returns None."""
        self.assertIsNone(b._try_decode_abi_string("0x"))

    # -----------------------------------------------------------------------
    # _try_decode_bytes32_null_trimmed helper (extracted Phase 1 logic)
    # -----------------------------------------------------------------------

    def test_try_decode_bytes32_null_trimmed_mkr_happy(self):
        """_try_decode_bytes32_null_trimmed decodes MKR (Format A) correctly."""
        # ADR-013 Format A: MKR = 4d4b52 + 29 null bytes
        data = b"MKR" + b"\x00" * 29
        self.assertEqual(b._try_decode_bytes32_null_trimmed("0x" + data.hex()), "MKR")

    def test_try_decode_bytes32_null_trimmed_short_returns_none(self):
        """_try_decode_bytes32_null_trimmed: response < 32 bytes returns None."""
        # Only 4 bytes — too short for a bytes32 word
        data = b"MKR\x00"
        self.assertIsNone(b._try_decode_bytes32_null_trimmed("0x" + data.hex()))

    def test_try_decode_bytes32_null_trimmed_nonprintable_tail_returns_none(self):
        """_try_decode_bytes32_null_trimmed: non-printable bytes after ticker -> None."""
        # Ticker "MKR" followed by a non-printable non-null byte (not a clean null-pad)
        data = b"MKR" + b"\x01" + b"\x00" * 28
        self.assertIsNone(b._try_decode_bytes32_null_trimmed("0x" + data.hex()))

    def test_try_decode_bytes32_null_trimmed_all_zeros_returns_none(self):
        """_try_decode_bytes32_null_trimmed: all-zero bytes32 returns None (empty ticker)."""
        data = b"\x00" * 32
        self.assertIsNone(b._try_decode_bytes32_null_trimmed("0x" + data.hex()))

    # -----------------------------------------------------------------------
    # _try_decode_bytes32_length_prefixed helper (NEW — ADR-013 Format B / DGD-style)
    # -----------------------------------------------------------------------

    def test_try_decode_bytes32_length_prefixed_dgd_happy(self):
        """_try_decode_bytes32_length_prefixed decodes DGD (Format B) correctly.

        ADR-013 Format B hex example:
        0344474400000000000000000000000000000000000000000000000000000000
        byte 0 = 0x03 (length 3), bytes 1-3 = 444744 = 'DGD', rest = nulls.
        Expected ticker: 'DGD'.
        """
        # DGD: length=3, ticker=b"DGD" + 28 null bytes
        raw = bytes([3]) + b"DGD" + b"\x00" * 28
        self.assertEqual(len(raw), 32)
        hex_result = "0x" + raw.hex()
        self.assertEqual(b._try_decode_bytes32_length_prefixed(hex_result), "DGD")

    def test_try_decode_bytes32_length_prefixed_single_char_happy(self):
        """_try_decode_bytes32_length_prefixed handles a 1-char ticker."""
        raw = bytes([1]) + b"X" + b"\x00" * 30
        self.assertEqual(len(raw), 32)
        self.assertEqual(b._try_decode_bytes32_length_prefixed("0x" + raw.hex()), "X")

    def test_try_decode_bytes32_length_prefixed_max_len_happy(self):
        """_try_decode_bytes32_length_prefixed handles length=31 (max valid)."""
        ticker = b"A" * 31
        raw = bytes([31]) + ticker
        self.assertEqual(len(raw), 32)
        self.assertEqual(b._try_decode_bytes32_length_prefixed("0x" + raw.hex()), "A" * 31)

    def test_try_decode_bytes32_length_prefixed_zero_length_returns_none(self):
        """_try_decode_bytes32_length_prefixed: length byte = 0 returns None (empty ticker)."""
        raw = bytes([0]) + b"\x00" * 31
        self.assertIsNone(b._try_decode_bytes32_length_prefixed("0x" + raw.hex()))

    def test_try_decode_bytes32_length_prefixed_length_overflow_returns_none(self):
        """_try_decode_bytes32_length_prefixed: length byte >= 32 returns None (overflow)."""
        # length byte = 32 means the ticker would need 32 bytes, overflowing the word
        raw = bytes([32]) + b"A" * 31
        self.assertIsNone(b._try_decode_bytes32_length_prefixed("0x" + raw.hex()))

    def test_try_decode_bytes32_length_prefixed_length_exceeds_data_returns_none(self):
        """_try_decode_bytes32_length_prefixed: length byte claims more data than available."""
        # A 4-byte response where byte 0 claims length=10
        raw = bytes([10]) + b"DGD"
        self.assertIsNone(b._try_decode_bytes32_length_prefixed("0x" + raw.hex()))

    def test_try_decode_bytes32_length_prefixed_nonprintable_ticker_returns_none(self):
        """_try_decode_bytes32_length_prefixed: non-printable ticker bytes return None."""
        # length=3, ticker=b"\x01\x02\x03" (non-printable)
        raw = bytes([3]) + b"\x01\x02\x03" + b"\x00" * 28
        self.assertIsNone(b._try_decode_bytes32_length_prefixed("0x" + raw.hex()))

    def test_try_decode_bytes32_length_prefixed_empty_hex_returns_none(self):
        """_try_decode_bytes32_length_prefixed: '0x' (empty) returns None."""
        self.assertIsNone(b._try_decode_bytes32_length_prefixed("0x"))

    def test_try_decode_bytes32_length_prefixed_short_response_returns_none(self):
        """_try_decode_bytes32_length_prefixed: response < 1 byte returns None."""
        # Less than 32 bytes but also less than 1 byte — empty after strip
        self.assertIsNone(b._try_decode_bytes32_length_prefixed("0x"))

    # -----------------------------------------------------------------------
    # decode_symbol integration: DGD-style via the full ladder
    # -----------------------------------------------------------------------

    def test_decode_symbol_dgd_via_ladder(self):
        """decode_symbol correctly decodes a DGD-style length-prefixed response."""
        raw = bytes([3]) + b"DGD" + b"\x00" * 28
        hex_result = "0x" + raw.hex()
        self.assertEqual(b.decode_symbol(hex_result), "DGD")

    # -----------------------------------------------------------------------
    # "Outside the catalog" scope-creep guard
    # -----------------------------------------------------------------------

    def test_decode_symbol_outside_catalog_returns_none(self):
        """A hex response that matches no variant in the bounded catalog returns None.

        This is the scope-creep guard (ADR-013): without it, future contributors
        could silently add variants without updating the ADR.

        Crafted hex: 32 bytes where byte 0 = 0x00 (null-prefixed, not DGD-style),
        rest are non-printable non-null bytes — cannot be decoded by any variant.
        """
        # All non-null non-printable bytes; none of the three variants matches
        raw = b"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f" * 2
        self.assertIsNone(b.decode_symbol("0x" + raw.hex()))

    # -----------------------------------------------------------------------
    # never-raises guarantee (ADR-006)
    # -----------------------------------------------------------------------

    def test_decode_symbol_never_raises_on_junk(self):
        """decode_symbol must never raise, even on bizarre/malformed input."""
        # Various junk inputs
        for junk in ("0xzzzz", "0x", "", "not hex at all", "0x" + "ff" * 100):
            try:
                result = b.decode_symbol(junk)
                # May return None or a string — both are acceptable
                self.assertTrue(result is None or isinstance(result, str),
                                msg="Expected None or str for input %r; got %r" % (junk, result))
            except Exception as exc:
                self.fail("decode_symbol raised %r on input %r" % (exc, junk))

    def test_decode_symbol_never_raises_on_empty(self):
        """decode_symbol('0x') must return None, not raise."""
        self.assertIsNone(b.decode_symbol("0x"))

    def test_decode_symbol_never_raises_on_non_string(self):
        """decode_symbol with a non-string input must return None, not raise."""
        # Non-string input — should be absorbed by the outer try/except
        for junk in (None, 42, b"\x00", [], {}):
            try:
                result = b.decode_symbol(junk)
                self.assertTrue(result is None or isinstance(result, str),
                                msg="Expected None or str for input %r; got %r" % (junk, result))
            except Exception as exc:
                self.fail("decode_symbol raised %r on non-string input %r" % (exc, junk))


class TestContractReads(unittest.TestCase):
    """Tests for the Layer 2 contract_reads section.

    All network I/O is mocked via the injected `rpc` kwarg.
    """

    # --- make_fake_rpc integration (Fix 2: make helper non-dead, A14 comment) ---

    def test_fetch_decimals_via_make_fake_rpc(self):
        """fetch_decimals works correctly when driven through make_fake_rpc."""
        hex_6 = "0x" + "0" * 62 + "06"
        rpc = make_fake_rpc({"eth_call": hex_6})
        result = b.fetch_decimals(rpc=rpc, url="https://x",
                                  token="0x" + "a" * 40)
        self.assertEqual(result, 6)

    def test_fetch_allowance_via_make_fake_rpc(self):
        """fetch_allowance works correctly when driven through make_fake_rpc."""
        hex_10 = "0x" + "0" * 62 + "0a"
        rpc = make_fake_rpc({"eth_call": hex_10})
        result = b.fetch_allowance(rpc=rpc, url="https://x",
                                   token="0x" + "a" * 40,
                                   holder="0x" + "1" * 40,
                                   spender="0x" + "2" * 40)
        self.assertEqual(result, 10)

    def test_fetch_decimals_via_make_fake_rpc_propagates_rpc_error(self):
        """make_fake_rpc `errors` set causes RPCError to propagate from fetch_decimals."""
        rpc = make_fake_rpc({}, errors=("eth_call",))
        with self.assertRaises(b._core.RPCError):
            b.fetch_decimals(rpc=rpc, url="https://x",
                             token="0x" + "a" * 40)

    # --- fetch_decimals ---

    def test_fetch_decimals_happy_path(self):
        """Returns 6 when rpc returns a 32-byte hex word with low byte 0x06."""
        hex_6 = "0x" + "0" * 62 + "06"
        mock_rpc = mock.Mock(return_value=hex_6)
        token = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
        url = "https://ethereum-rpc.publicnode.com"

        result = b.fetch_decimals(rpc=mock_rpc, url=url, token=token)

        self.assertEqual(result, 6)
        mock_rpc.assert_called_once()
        args = mock_rpc.call_args
        self.assertEqual(args[0][1], "eth_call")   # method
        self.assertEqual(args[0][2][1], "latest")  # block tag

    def test_fetch_decimals_propagates_rpc_error(self):
        """RPCError propagates out (FATAL — no catch inside fetch_decimals)."""
        mock_rpc = mock.Mock(side_effect=b._core.RPCError("boom"))
        with self.assertRaises(b._core.RPCError):
            b.fetch_decimals(rpc=mock_rpc, url="https://x", token="0x" + "a" * 40)

    def test_fetch_decimals_rejects_suspicious_value(self):
        """If decimals > MAX_DECIMALS the decode raises ValueError (propagated)."""
        # 37 = 0x25 which exceeds MAX_DECIMALS=36
        hex_37 = "0x" + "0" * 62 + "25"
        mock_rpc = mock.Mock(return_value=hex_37)
        with self.assertRaises(ValueError):
            b.fetch_decimals(rpc=mock_rpc, url="https://x", token="0x" + "a" * 40)

    # --- fetch_symbol ---

    def test_fetch_symbol_happy_path_usdc(self):
        """Returns 'USDC' when rpc returns a standard ABI-encoded string."""
        # Standard ABI: offset=32, length=4, payload "USDC" right-padded to 32 bytes
        offset_word = "0020".zfill(64)
        length_word = "0004".zfill(64)
        payload = "55534443" + "00" * 28  # "USDC" in hex + 28 zero bytes
        hex_result = "0x" + offset_word + length_word + payload
        mock_rpc = mock.Mock(return_value=hex_result)
        token = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"

        result = b.fetch_symbol(rpc=mock_rpc, url="https://x", token=token)

        self.assertEqual(result, "USDC")
        args = mock_rpc.call_args
        self.assertEqual(args[0][1], "eth_call")
        self.assertEqual(args[0][2][1], "latest")

    def test_fetch_symbol_returns_none_on_rpc_error(self):
        """Returns None (not raises) when rpc raises RPCError (best-effort)."""
        mock_rpc = mock.Mock(side_effect=b._core.RPCError("gone"))
        result = b.fetch_symbol(rpc=mock_rpc, url="https://x", token="0x" + "a" * 40)
        self.assertIsNone(result)

    def test_fetch_symbol_returns_none_when_decode_returns_none(self):
        """Returns None when decode_symbol returns None (malformed response)."""
        mock_rpc = mock.Mock(return_value="0xdeadbeef")  # too short for any decode
        result = b.fetch_symbol(rpc=mock_rpc, url="https://x", token="0x" + "a" * 40)
        self.assertIsNone(result)

    def test_fetch_symbol_never_raises(self):
        """Any exception from rpc must be swallowed — never re-raised."""
        mock_rpc = mock.Mock(side_effect=RuntimeError("unexpected error"))
        # Should not raise
        result = b.fetch_symbol(rpc=mock_rpc, url="https://x", token="0x" + "a" * 40)
        self.assertIsNone(result)

    # --- fetch_allowance ---

    def test_fetch_allowance_happy_path(self):
        """Returns 10 when rpc returns a 32-byte word with low byte 0x0a."""
        hex_10 = "0x" + "0" * 62 + "0a"
        mock_rpc = mock.Mock(return_value=hex_10)
        token = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
        holder = "0x" + "1" * 40
        spender = "0x" + "2" * 40

        result = b.fetch_allowance(rpc=mock_rpc, url="https://x", token=token,
                                   holder=holder, spender=spender)

        self.assertEqual(result, 10)
        args = mock_rpc.call_args
        self.assertEqual(args[0][1], "eth_call")
        self.assertEqual(args[0][2][1], "latest")

    def test_fetch_allowance_propagates_rpc_error(self):
        """RPCError propagates out (soft-check is caller's job per ADR-006)."""
        mock_rpc = mock.Mock(side_effect=b._core.RPCError("timeout"))
        with self.assertRaises(b._core.RPCError):
            b.fetch_allowance(rpc=mock_rpc, url="https://x",
                              token="0x" + "a" * 40,
                              holder="0x" + "1" * 40,
                              spender="0x" + "2" * 40)

    # --- SEL_BALANCE_OF constant ---

    def test_sel_balance_of_value(self):
        """SEL_BALANCE_OF == '0x70a08231' (keccak256('balanceOf(address)')[:4])."""
        self.assertEqual(b.SEL_BALANCE_OF, "0x70a08231")

    # --- encode_balance_of_call ---

    def test_encode_balance_of_call_zero_address(self):
        """encode_balance_of_call('0x'+'00'*20) returns selector + zero-padded word."""
        result = b.encode_balance_of_call("0x" + "00" * 20)
        self.assertEqual(result, "0x70a08231" + "00" * 32)

    def test_encode_balance_of_call_bit_pattern(self):
        """encode_balance_of_call round-trips against a mixed-case address."""
        holder = "0x890e560a6012bFA5d0d71a4a107dBa4Aed698f38"
        result = b.encode_balance_of_call(holder)
        expected = (
            "0x70a08231"
            + format(int(holder, 16), "064x")
        )
        self.assertEqual(result, expected)

    # --- decode_balance ---

    def test_decode_balance_zero(self):
        self.assertEqual(b.decode_balance("0x" + "0" * 64), 0)

    def test_decode_balance_ten(self):
        self.assertEqual(b.decode_balance("0x" + "0" * 63 + "a"), 10)

    def test_decode_balance_max_uint256(self):
        self.assertEqual(b.decode_balance("0x" + "f" * 64), (1 << 256) - 1)

    # --- decode_balance non-str guard (Fix 5) ---

    def test_decode_balance_non_str_int_raises_value_error(self):
        """Non-string input must raise ValueError (Fix 5 — pin decode_balance contract)."""
        with self.assertRaises(ValueError):
            b.decode_balance(42)

    def test_decode_balance_non_str_dict_raises_value_error(self):
        """Dict input must raise ValueError (Fix 5 — pin decode_balance contract)."""
        with self.assertRaises(ValueError):
            b.decode_balance({"result": "0x06"})

    # --- fetch_balance_of ---

    def test_fetch_balance_of_happy_path(self):
        """Returns 6 when rpc returns a 32-byte word with low byte 0x06."""
        hex_6 = "0x" + "0" * 62 + "06"
        mock_rpc = mock.Mock(return_value=hex_6)
        token = "0x" + "a" * 40
        holder = "0x" + "1" * 40
        result = b.fetch_balance_of(rpc=mock_rpc, url="https://x",
                                    token=token, holder=holder)
        self.assertEqual(result, 6)
        args = mock_rpc.call_args
        self.assertEqual(args[0][1], "eth_call")
        call_obj = args[0][2][0]
        self.assertEqual(call_obj["to"], token)
        self.assertTrue(call_obj["data"].startswith("0x70a08231"))
        self.assertEqual(args[0][2][1], "latest")

    def test_fetch_balance_of_propagates_rpc_error(self):
        """RPCError propagates (soft-check posture is caller's job per ADR-006)."""
        mock_rpc = mock.Mock(side_effect=b._core.RPCError("down"))
        with self.assertRaises(b._core.RPCError):
            b.fetch_balance_of(rpc=mock_rpc, url="https://x",
                               token="0x" + "a" * 40,
                               holder="0x" + "1" * 40)


class TestGasEstimator(unittest.TestCase):
    """Tests for the Layer 2 gas_estimator section."""

    # --- _apply_buffer_cap table ---

    def test_apply_buffer_cap_zero(self):
        """0 * 12 // 10 = 0, well below cap."""
        self.assertEqual(b._apply_buffer_cap(0), 0)

    def test_apply_buffer_cap_one(self):
        """1 * 12 // 10 = 1 (integer division floors)."""
        self.assertEqual(b._apply_buffer_cap(1), 1)

    def test_apply_buffer_cap_100k(self):
        """100_000 * 12 // 10 = 120_000 (below 300_000 cap)."""
        self.assertEqual(b._apply_buffer_cap(100_000), 120_000)

    def test_apply_buffer_cap_250k_hit_cap(self):
        """250_000 * 12 // 10 = 300_000 exactly (at cap)."""
        self.assertEqual(b._apply_buffer_cap(250_000), 300_000)

    def test_apply_buffer_cap_1m_capped(self):
        """1_000_000 is well above cap — should return 300_000."""
        self.assertEqual(b._apply_buffer_cap(1_000_000), 300_000)

    # --- estimate_gas happy paths ---

    def test_estimate_gas_buffered(self):
        """65055 (0xfe1f) buffered: (65055 * 12) // 10 = 78066, below cap."""
        mock_rpc = mock.Mock(return_value="0xfe1f")
        sender = "0x" + "a" * 40
        token = "0x" + "b" * 40
        data = "0xa9059cbb" + "0" * 120
        url = "https://ethereum-rpc.publicnode.com"

        result = b.estimate_gas(rpc=mock_rpc, url=url, sender=sender,
                                token=token, data=data)

        self.assertEqual(result, 78066)
        # Verify the call to the rpc
        args = mock_rpc.call_args
        self.assertEqual(args[0][1], "eth_estimateGas")
        call_obj = args[0][2][0]
        self.assertIn("from", call_obj)
        self.assertIn("to", call_obj)
        self.assertIn("data", call_obj)
        self.assertEqual(call_obj["value"], "0x0")

    def test_estimate_gas_capped(self):
        """250_000 (0x3d090) buffered = 300_000, exactly at cap."""
        mock_rpc = mock.Mock(return_value="0x3d090")
        result = b.estimate_gas(rpc=mock_rpc, url="https://x",
                                sender="0x" + "a" * 40,
                                token="0x" + "b" * 40,
                                data="0x00")
        self.assertEqual(result, 300_000)

    # --- estimate_gas no-fallback regression ---

    def test_estimate_gas_propagates_rpc_error(self):
        """RPCError must propagate — no internal catch (architecture ADR-007)."""
        mock_rpc = mock.Mock(side_effect=b._core.RPCError(
            "execution reverted: ERC20: transfer amount exceeds balance"
        ))
        with self.assertRaises(b._core.RPCError):
            b.estimate_gas(rpc=mock_rpc, url="https://x",
                           sender="0x" + "a" * 40,
                           token="0x" + "b" * 40,
                           data="0x00")

    # --- Constants ---

    def test_gas_buffer_num(self):
        self.assertEqual(b.GAS_BUFFER_NUM, 12)

    def test_gas_buffer_den(self):
        self.assertEqual(b.GAS_BUFFER_DEN, 10)

    def test_gas_cap(self):
        self.assertEqual(b.GAS_CAP, 300_000)


class TestSummary(unittest.TestCase):
    """Tests for the Layer 2 summary section."""

    # Synthetic ctx for a 'transfer' operation used by most render tests.
    _TRANSFER_CTX = {
        "operation": "transfer",
        "network": "mainnet",
        "chain_id": 1,
        "token": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
        "symbol": "USDC",
        "decimals": 6,
        "human_amount": "1.5",
        "base_amount": 1_500_000,
        "is_max_uint": False,
        "from_": "0xSender0000000000000000000000000000000000",
        "to": "0xRecipient00000000000000000000000000000000",
        "nonce": 42,
        "gas": 78066,
        "max_fee": 25_000_000_000,
        "max_priority_fee": 1_500_000_000,
    }

    # --- render_summary label checks ---

    def test_render_summary_contains_operation(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("operation", text)
        self.assertIn("transfer", text)

    def test_render_summary_contains_network(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("network", text)
        self.assertIn("mainnet", text)

    def test_render_summary_contains_token(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("token", text)

    def test_render_summary_contains_symbol(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("USDC", text)

    def test_render_summary_contains_decimals(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("decimals", text)

    def test_render_summary_contains_amount_base_units(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("amount (base units)", text)

    def test_render_summary_contains_nonce(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("nonce", text)

    def test_render_summary_contains_gas(self):
        text = b.render_summary(self._TRANSFER_CTX)
        self.assertIn("gas", text)

    def test_render_summary_is_pure_no_stderr(self):
        """render_summary must not write to stderr (pure function)."""
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.render_summary(self._TRANSFER_CTX)
            self.assertEqual(fake_err.getvalue(), "")

    # --- render_summary with symbol=None ---

    def test_render_summary_unavailable_when_symbol_none(self):
        ctx = dict(self._TRANSFER_CTX, symbol=None)
        text = b.render_summary(ctx)
        self.assertIn("(unavailable)", text)

    # --- render_summary with is_max_uint=True ---

    def test_render_summary_max_uint256_label(self):
        ctx = dict(self._TRANSFER_CTX, is_max_uint=True)
        text = b.render_summary(ctx)
        self.assertIn("MAX UINT256", text)

    # --- print_summary writes to stderr ---

    def test_print_summary_writes_to_stderr(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.print_summary(self._TRANSFER_CTX)
            output = fake_err.getvalue()
        self.assertIn("operation", output)
        self.assertGreater(len(output), 0)

    # --- warn_approve_max ---

    def test_warn_approve_max_writes_warning_prefix(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_max(symbol="USDC",
                               token="0xA0b8" + "0" * 36,
                               spender="0xSpnd" + "0" * 35)
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("UNLIMITED", output)
        self.assertIn("USDC", output)

    def test_warn_approve_max_unknown_symbol(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_max(symbol=None,
                               token="0x" + "a" * 40,
                               spender="0x" + "b" * 40)
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("<unknown>", output)

    def test_warn_approve_max_contains_revoke_hint(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_max(symbol="USDC",
                               token="0x" + "a" * 40,
                               spender="0x" + "b" * 40)
            output = fake_err.getvalue()
        self.assertIn("Revoke", output)

    # --- warn_low_allowance ---

    def test_warn_low_allowance_writes_warning_prefix(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_low_allowance(holder="0x" + "1" * 40,
                                 spender="0x" + "2" * 40,
                                 current=500_000,
                                 requested=1_500_000,
                                 decimals=6)
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        # base_units_to_human(500_000, 6) = "0.5", base_units_to_human(1_500_000, 6) = "1.5"
        self.assertIn("0.5", output)
        self.assertIn("1.5", output)

    def test_warn_low_allowance_contains_revert_hint(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_low_allowance(holder="0x" + "1" * 40,
                                 spender="0x" + "2" * 40,
                                 current=0,
                                 requested=1_000_000,
                                 decimals=6)
            output = fake_err.getvalue()
        self.assertIn("revert", output)

    # --- warn_allowance_check_skipped ---

    def test_warn_allowance_check_skipped_contains_reason(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_allowance_check_skipped(reason="transport error")
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("transport error", output)

    def test_warn_allowance_check_skipped_build_continues(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_allowance_check_skipped(reason="timeout")
            output = fake_err.getvalue()
        self.assertIn("Build continues", output)

    # --- emit_warning dispatcher ---

    def test_emit_warning_approve_max_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("approve_max", {
                "symbol": "USDC",
                "token": "0x" + "a" * 40,
                "spender": "0x" + "b" * 40,
            })
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("UNLIMITED", output)

    def test_emit_warning_low_allowance_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("low_allowance", {
                "holder": "0x" + "1" * 40,
                "spender": "0x" + "2" * 40,
                "current": 0,
                "requested": 1_000_000,
                "decimals": 6,
            })
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)

    def test_emit_warning_allowance_check_skipped_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("allowance_check_skipped", {"reason": "rpc down"})
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("rpc down", output)

    def test_emit_warning_symbol_unavailable_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("symbol_unavailable", {})
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)

    def test_emit_warning_unknown_kind_raises_value_error(self):
        with self.assertRaises(ValueError):
            b.emit_warning("unknown_kind", {})

    # --- emit_warning symbol_unavailable payload guard (Fix 5) ---

    def test_emit_warning_symbol_unavailable_with_payload_raises_value_error(self):
        """symbol_unavailable must raise ValueError when a non-empty payload is passed (Fix 5)."""
        with self.assertRaises(ValueError):
            b.emit_warning("symbol_unavailable", {"x": 1})

    def test_emit_warning_symbol_unavailable_empty_payload_succeeds(self):
        """symbol_unavailable with an empty payload must succeed (Fix 5)."""
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("symbol_unavailable", {})
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)

    # --- warn_low_balance ---

    def test_warn_low_balance_writes_warning_prefix(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_low_balance(
                holder="0x" + "1" * 40,
                current=500_000,
                requested=1_500_000,
                decimals=6,
                symbol="USDC",
            )
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("0x" + "1" * 40, output)

    def test_warn_low_balance_writes_to_stderr(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                b.warn_low_balance(
                    holder="0x" + "1" * 40,
                    current=0,
                    requested=1_000_000,
                    decimals=6,
                    symbol="USDC",
                )
        self.assertGreater(len(fake_err.getvalue()), 0)
        self.assertEqual(fake_out.getvalue(), "")

    def test_warn_low_balance_contains_revert_hint(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_low_balance(
                holder="0x" + "1" * 40,
                current=0,
                requested=1_000_000,
                decimals=6,
                symbol="USDC",
            )
            output = fake_err.getvalue()
        self.assertIn("revert", output)

    # --- warn_balance_check_skipped ---

    def test_warn_balance_check_skipped_contains_reason(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_balance_check_skipped(reason="transport timeout")
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("transport timeout", output)

    def test_warn_balance_check_skipped_build_continues(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_balance_check_skipped(reason="down")
            output = fake_err.getvalue()
        self.assertIn("Build continues", output)

    def test_warn_balance_check_skipped_writes_to_stderr(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                b.warn_balance_check_skipped(reason="x")
        self.assertGreater(len(fake_err.getvalue()), 0)
        self.assertEqual(fake_out.getvalue(), "")

    # --- emit_warning: low_balance and balance_check_skipped ---

    def test_emit_warning_low_balance_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("low_balance", {
                "holder": "0x" + "1" * 40,
                "current": 0,
                "requested": 1_000_000,
                "decimals": 6,
                "symbol": "USDC",
            })
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)

    def test_emit_warning_balance_check_skipped_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("balance_check_skipped", {"reason": "rpc down"})
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("rpc down", output)

    # --- warn_approve_race ---

    def test_warn_approve_race_writes_warning_prefix(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_race(
                holder="0x" + "1" * 40,
                spender="0x" + "2" * 40,
                current=5_000_000,
                requested=1_000_000,
                decimals=6,
                symbol="USDC",
            )
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("0x" + "2" * 40, output)  # spender must appear

    def test_warn_approve_race_contains_swc114_reference(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_race(
                holder="0x" + "1" * 40,
                spender="0x" + "2" * 40,
                current=5_000_000,
                requested=1_000_000,
                decimals=6,
                symbol="USDC",
            )
            output = fake_err.getvalue()
        self.assertIn("SWC-114", output)

    def test_warn_approve_race_writes_to_stderr(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                b.warn_approve_race(
                    holder="0x" + "1" * 40,
                    spender="0x" + "2" * 40,
                    current=5_000_000,
                    requested=1_000_000,
                    decimals=6,
                    symbol="USDC",
                )
        self.assertGreater(len(fake_err.getvalue()), 0)
        self.assertEqual(fake_out.getvalue(), "")

    # --- warn_approve_race_check_skipped ---

    def test_warn_approve_race_check_skipped_contains_reason(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_race_check_skipped(reason="rpc timeout")
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("rpc timeout", output)

    def test_warn_approve_race_check_skipped_build_continues(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_race_check_skipped(reason="down")
            output = fake_err.getvalue()
        self.assertIn("Build continues", output)

    # --- emit_warning: approve_race and approve_race_check_skipped ---

    def test_emit_warning_approve_race_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("approve_race", {
                "holder": "0x" + "1" * 40,
                "spender": "0x" + "2" * 40,
                "current": 5_000_000,
                "requested": 1_000_000,
                "decimals": 6,
                "symbol": "USDC",
            })
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)

    def test_emit_warning_approve_race_check_skipped_dispatches(self):
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("approve_race_check_skipped", {"reason": "rpc down"})
            output = fake_err.getvalue()
        self.assertIn("WARNING:", output)
        self.assertIn("rpc down", output)


    # -----------------------------------------------------------------------
    # Issue 3.2 Sub-step 0: byte-identical Phase-1 regression pins for op_label
    # These verify that adding op_label to summary_ctx and reading it in
    # render_summary produces output byte-identical to what Phase 1 produced.
    # -----------------------------------------------------------------------

    def test_render_summary_transfer_byte_identical_with_op_label(self):
        """render_summary with op_label='transfer' is byte-identical to Phase 1 output.

        Phase 1 set ctx['operation']='transfer'; now both 'op_label' and
        'operation' are set to 'transfer'. The rendered text must be identical
        character-for-character.
        """
        ctx = dict(self._TRANSFER_CTX, op_label="transfer")
        text = b.render_summary(ctx)
        # Pin the exact operation line.
        self.assertIn("operation         : transfer", text)
        # Ensure no OTHER op label leaks in via the op_label path.
        self.assertNotIn("operation         : approve", text)
        self.assertNotIn("operation         : revoke", text)
        self.assertNotIn("operation         : transfer-from", text)

    def test_render_summary_approve_byte_identical_with_op_label(self):
        """render_summary with op_label='approve' is byte-identical to Phase 1 output."""
        ctx = {
            "op_label":       "approve",
            "operation":      "approve",
            "network":        "mainnet",
            "chain_id":       1,
            "token":          "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
            "symbol":         "USDC",
            "decimals":       6,
            "human_amount":   "1.5",
            "base_amount":    1_500_000,
            "is_max_uint":    False,
            "from_":          "0xSender0000000000000000000000000000000000",
            "holder":         "0xSender0000000000000000000000000000000000",
            "spender":        "0xSpender000000000000000000000000000000000",
            "nonce":          42,
            "gas":            78066,
            "max_fee":        25_000_000_000,
            "max_priority_fee": 1_500_000_000,
        }
        text = b.render_summary(ctx)
        self.assertIn("operation         : approve", text)
        self.assertIn("spender", text)
        self.assertNotIn("operation         : revoke", text)

    def test_render_summary_transfer_from_byte_identical_with_op_label(self):
        """render_summary with op_label='transfer-from' is byte-identical to Phase 1 output."""
        ctx = {
            "op_label":         "transfer-from",
            "operation":        "transfer-from",
            "network":          "mainnet",
            "chain_id":         1,
            "token":            "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
            "symbol":           "USDC",
            "decimals":         6,
            "human_amount":     "1.5",
            "base_amount":      1_500_000,
            "is_max_uint":      False,
            "from_":            "0xHolder0000000000000000000000000000000000",
            "to":               "0xRecipient00000000000000000000000000000000",
            "sender":           "0xSender0000000000000000000000000000000000",
            "signer_spender":   "0xSender0000000000000000000000000000000000",
            "nonce":            42,
            "gas":              78066,
            "max_fee":          25_000_000_000,
            "max_priority_fee": 1_500_000_000,
        }
        text = b.render_summary(ctx)
        self.assertIn("operation         : transfer-from", text)
        self.assertIn("signer / spender", text)
        self.assertNotIn("operation         : revoke", text)

    # -----------------------------------------------------------------------
    # Issue 3.2: render_summary with op_label="revoke"
    # -----------------------------------------------------------------------

    def test_render_summary_revoke_op_label(self):
        """render_summary with op_label='revoke' must show 'operation: revoke'."""
        ctx = {
            "op_label":       "revoke",
            "operation":      "revoke",
            "network":        "mainnet",
            "chain_id":       1,
            "token":          "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
            "symbol":         "USDC",
            "decimals":       6,
            "human_amount":   "0",
            "base_amount":    0,
            "is_max_uint":    False,
            "from_":          "0xSender0000000000000000000000000000000000",
            "holder":         "0xSender0000000000000000000000000000000000",
            "spender":        "0xSpender000000000000000000000000000000000",
            "nonce":          7,
            "gas":            78066,
            "max_fee":        25_000_000_000,
            "max_priority_fee": 1_500_000_000,
        }
        text = b.render_summary(ctx)
        self.assertIn("operation         : revoke", text)
        # Address layout: holder + spender (same as approve)
        self.assertIn("spender", text)
        # Must NOT say 'operation: approve'
        self.assertNotIn("operation         : approve", text)

    # -----------------------------------------------------------------------
    # Issue 3.2: warn_approve_revoke
    # -----------------------------------------------------------------------

    def test_warn_approve_revoke_writes_to_stderr(self):
        """warn_approve_revoke writes a multi-line confirmation block to stderr."""
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_revoke(
                symbol="USDC",
                token="0x" + "a" * 40,
                spender="0x" + "b" * 40,
            )
            output = fake_err.getvalue()
        self.assertGreater(len(output), 0)
        # Names symbol
        self.assertIn("USDC", output)
        # Names token
        self.assertIn("0x" + "a" * 40, output)
        # Names spender
        self.assertIn("0x" + "b" * 40, output)

    def test_warn_approve_revoke_no_warning_prefix(self):
        """warn_approve_revoke must NOT start with 'WARNING:' (informational, not alarming)."""
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_revoke(
                symbol="USDC",
                token="0x" + "a" * 40,
                spender="0x" + "b" * 40,
            )
            output = fake_err.getvalue()
        self.assertNotIn("WARNING:", output)

    def test_warn_approve_revoke_unknown_symbol(self):
        """warn_approve_revoke with symbol=None renders '<unknown>'."""
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.warn_approve_revoke(
                symbol=None,
                token="0x" + "a" * 40,
                spender="0x" + "b" * 40,
            )
            output = fake_err.getvalue()
        self.assertIn("<unknown>", output)

    def test_warn_approve_revoke_writes_to_stderr_not_stdout(self):
        """warn_approve_revoke must write to stderr only (not stdout)."""
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                b.warn_approve_revoke(
                    symbol="USDC",
                    token="0x" + "a" * 40,
                    spender="0x" + "b" * 40,
                )
        self.assertGreater(len(fake_err.getvalue()), 0)
        self.assertEqual(fake_out.getvalue(), "")

    def test_emit_warning_approve_revoke_dispatches(self):
        """emit_warning('approve_revoke', {...}) must dispatch to warn_approve_revoke."""
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            b.emit_warning("approve_revoke", {
                "symbol": "USDC",
                "token": "0x" + "a" * 40,
                "spender": "0x" + "b" * 40,
            })
            output = fake_err.getvalue()
        self.assertGreater(len(output), 0)
        self.assertIn("USDC", output)
        self.assertIn("0x" + "b" * 40, output)


class TestTxAssembly(unittest.TestCase):
    """Tests for the Layer 3 tx_assembly section.

    Uses _make_rpc_for_transfer (an instance helper), a selector-aware
    dispatcher that distinguishes decimals/symbol/allowance eth_call reads
    by their 4-byte selector prefix. Tests cover happy paths, approve_max,
    transfer-from soft-checks, and no-fallback regressions (ADR-007).
    """

    # Addresses reused across tests — 0x-prefixed, 40 hex chars each.
    TOKEN   = "0x" + "a" * 40
    TO      = "0x" + "b" * 40
    SENDER  = "0x" + "c" * 40
    FROM_   = "0x" + "d" * 40
    SPENDER = "0x" + "e" * 40

    # Hex constants returned by the fake RPC.
    HEX_DECIMALS_6   = "0x" + "0" * 62 + "06"
    # Standard ABI encoding of "USDC" symbol.
    _offset = "0020".zfill(64)
    _length = "0004".zfill(64)
    _payload = "55534443" + "00" * 28  # "USDC" + 28 zero bytes
    HEX_SYMBOL_USDC  = "0x" + _offset + _length + _payload
    HEX_GAS          = "0xfe1f"   # 65055 -> buffered: 78066
    HEX_NONCE        = "0x05"     # nonce=5
    HEX_BASE_FEE     = "0x2540be400"  # 10_000_000_000 (10 gwei)
    HEX_TIP          = "0x3b9aca00"   # 1_000_000_000 (1 gwei)
    # Allowance >= requested (10 USDC worth in base units 10_000_000)
    HEX_ALLOWANCE_HIGH = "0x" + format(10_000_000, "064x")
    # Allowance < requested (1 base unit)
    HEX_ALLOWANCE_LOW  = "0x" + format(1, "064x")
    # Balance >= requested (10 USDC worth in base units 10_000_000)
    HEX_BALANCE_HIGH   = "0x" + format(10_000_000, "064x")
    # Balance < requested (1 base unit, less than 1.5 USDC requested)
    HEX_BALANCE_LOW    = "0x" + format(1, "064x")

    def _make_block(self):
        """Return a minimal fake eth_getBlockByNumber result."""
        return {"baseFeePerGas": self.HEX_BASE_FEE}

    def _make_rpc_for_transfer(self, *, gas_hex=None, allowance_hex=None,
                               balance_hex=None):
        """Build a make_fake_rpc that supplies correct responses for do_transfer.

        The fake RPC dispatches eth_call responses by inspecting the selector
        in the data field to distinguish decimals / symbol / allowance / balanceOf
        reads (architecture A14 comment).
        """
        gas_hex = gas_hex or self.HEX_GAS

        def _rpc(url, method, params):
            if method == "eth_estimateGas":
                return gas_hex
            if method == "eth_getTransactionCount":
                return self.HEX_NONCE
            if method == "eth_getBlockByNumber":
                return self._make_block()
            if method == "eth_maxPriorityFeePerGas":
                return self.HEX_TIP
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                # Distinguish by 4-byte selector prefix
                if data.startswith(b.SEL_DECIMALS):
                    return self.HEX_DECIMALS_6
                if data.startswith(b.SEL_SYMBOL):
                    return self.HEX_SYMBOL_USDC
                if data.startswith(b.SEL_ALLOWANCE):
                    return allowance_hex or self.HEX_ALLOWANCE_HIGH
                if data.startswith(b.SEL_BALANCE_OF):
                    return balance_hex or self.HEX_BALANCE_HIGH
                raise AssertionError("unexpected eth_call data: %r" % data)
            raise AssertionError("unexpected RPC method: %r" % method)

        return _rpc

    # -----------------------------------------------------------------------
    # _soft_check_allowance helper (Issue 2.3)
    # -----------------------------------------------------------------------

    def _make_rpc_allowance(self, value):
        """Return a mock rpc that returns a hex-encoded allowance value."""
        hex_val = "0x" + format(value, "064x")
        return mock.Mock(return_value=hex_val)

    def test_soft_check_allowance_ok_returns_empty(self):
        """Default trigger: allowance >= requested → returns []."""
        rpc = self._make_rpc_allowance(10_000_000)  # high allowance
        result = b._soft_check_allowance(
            rpc=rpc, url="https://x",
            token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
            requested=1_500_000,
            skipped_kind="allowance_check_skipped",
            low_kind="low_allowance",
        )
        self.assertEqual(result, [])

    def test_soft_check_allowance_low_returns_warning(self):
        """Default trigger: allowance < requested → returns [(low_kind, payload)]."""
        rpc = self._make_rpc_allowance(1)  # low allowance
        result = b._soft_check_allowance(
            rpc=rpc, url="https://x",
            token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
            requested=1_500_000,
            skipped_kind="allowance_check_skipped",
            low_kind="low_allowance",
            low_payload_extra={"holder": self.FROM_, "spender": self.SENDER,
                               "decimals": 6, "symbol": "USDC"},
        )
        self.assertEqual(len(result), 1)
        kind, payload = result[0]
        self.assertEqual(kind, "low_allowance")
        self.assertIn("current", payload)
        self.assertIn("requested", payload)
        self.assertIn("holder", payload)
        self.assertIn("spender", payload)
        self.assertIn("decimals", payload)
        self.assertIn("symbol", payload)

    def test_soft_check_allowance_skipped_on_rpc_error(self):
        """RPCError → returns [(skipped_kind, {'reason': str(e)})]."""
        rpc = mock.Mock(side_effect=b._core.RPCError("timeout"))
        result = b._soft_check_allowance(
            rpc=rpc, url="https://x",
            token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
            requested=1_500_000,
            skipped_kind="allowance_check_skipped",
            low_kind="low_allowance",
        )
        self.assertEqual(len(result), 1)
        kind, payload = result[0]
        self.assertEqual(kind, "allowance_check_skipped")
        self.assertIn("reason", payload)
        self.assertIn("timeout", payload["reason"])

    def test_soft_check_allowance_custom_trigger_fires(self):
        """Custom trigger lambda cur, req: cur != 0 and cur != req fires for cur=5, req=10."""
        rpc = self._make_rpc_allowance(5)  # 5 != 0 and 5 != 10 → should fire
        result = b._soft_check_allowance(
            rpc=rpc, url="https://x",
            token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
            requested=10,
            skipped_kind="approve_race_check_skipped",
            low_kind="approve_race",
            trigger=lambda cur, req: cur != 0 and cur != req,
        )
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0][0], "approve_race")

    def test_soft_check_allowance_custom_trigger_zero_skips(self):
        """Custom trigger: cur=0, req=10 → cur==0, so trigger is False → returns []."""
        rpc = self._make_rpc_allowance(0)  # 0 != 0 is False → trigger False
        result = b._soft_check_allowance(
            rpc=rpc, url="https://x",
            token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
            requested=10,
            skipped_kind="approve_race_check_skipped",
            low_kind="approve_race",
            trigger=lambda cur, req: cur != 0 and cur != req,
        )
        self.assertEqual(result, [])

    def test_soft_check_allowance_no_low_payload_extra(self):
        """With no low_payload_extra, payload has only current+requested keys."""
        rpc = self._make_rpc_allowance(1)  # trigger fires (1 < 100)
        result = b._soft_check_allowance(
            rpc=rpc, url="https://x",
            token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
            requested=100,
            skipped_kind="allowance_check_skipped",
            low_kind="low_allowance",
        )
        self.assertEqual(len(result), 1)
        kind, payload = result[0]
        self.assertIn("current", payload)
        self.assertIn("requested", payload)

    def test_soft_check_allowance_reserved_key_current_raises(self):
        """low_payload_extra containing 'current' must raise ValueError (Fix 3)."""
        rpc = self._make_rpc_allowance(1)  # trigger fires
        with self.assertRaises(ValueError) as ctx:
            b._soft_check_allowance(
                rpc=rpc, url="https://x",
                token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
                requested=100,
                skipped_kind="allowance_check_skipped",
                low_kind="low_allowance",
                low_payload_extra={"current": 999, "holder": self.FROM_},
            )
        self.assertIn("reserved", str(ctx.exception))

    def test_soft_check_allowance_reserved_key_requested_raises(self):
        """low_payload_extra containing 'requested' must raise ValueError (Fix 3)."""
        rpc = self._make_rpc_allowance(1)  # trigger fires
        with self.assertRaises(ValueError) as ctx:
            b._soft_check_allowance(
                rpc=rpc, url="https://x",
                token=self.TOKEN, holder=self.FROM_, spender=self.SENDER,
                requested=100,
                skipped_kind="allowance_check_skipped",
                low_kind="low_allowance",
                low_payload_extra={"requested": 50, "holder": self.FROM_},
            )
        self.assertIn("reserved", str(ctx.exception))

    # -----------------------------------------------------------------------
    # _build_eip1559_envelope
    # -----------------------------------------------------------------------

    def test_build_envelope_shape(self):
        """_build_eip1559_envelope returns the v1 TxRequest shape with decimal strings."""
        base_fee = 10_000_000_000
        tip      = 1_000_000_000
        tx = b._build_eip1559_envelope(
            chain_id=1,
            nonce=5,
            to=self.TOKEN,
            data="0xdeadbeef",
            gas=78066,
            base_fee=base_fee,
            tip=tip,
        )
        self.assertEqual(tx["type"], "eip1559")
        self.assertEqual(tx["chainId"], "1")
        self.assertEqual(tx["nonce"], "5")
        self.assertEqual(tx["to"], self.TOKEN)
        self.assertEqual(tx["value"], "0")
        self.assertEqual(tx["data"], "0xdeadbeef")
        self.assertEqual(tx["gas"], "78066")
        # maxFeePerGas = compute_max_fee(base_fee, tip) = base_fee*2 + tip
        expected_max_fee = str(b._core.compute_max_fee(base_fee, tip))
        self.assertEqual(tx["maxFeePerGas"], expected_max_fee)
        self.assertEqual(tx["maxPriorityFeePerGas"], str(tip))

    def test_build_envelope_all_numeric_fields_are_strings(self):
        """All numeric tx fields must be decimal strings, not ints."""
        tx = b._build_eip1559_envelope(1, 0, self.TOKEN, "0x", 21000,
                                       10_000_000_000, 1_000_000_000)
        for key in ("chainId", "nonce", "gas", "maxFeePerGas", "maxPriorityFeePerGas"):
            self.assertIsInstance(tx[key], str, msg="field %r should be str" % key)

    # -----------------------------------------------------------------------
    # do_transfer — happy path
    # -----------------------------------------------------------------------

    def test_do_transfer_happy_path_tx_shape(self):
        rpc = self._make_rpc_for_transfer()
        tx, ctx, warns = b.do_transfer(
            network="mainnet",
            token=self.TOKEN,
            to=self.TO,
            amount="1.5",
            sender=self.SENDER,
            rpc=rpc,
        )
        # tx is the token contract address (not the recipient)
        self.assertEqual(tx["to"], self.TOKEN)
        self.assertEqual(tx["value"], "0")
        self.assertTrue(tx["data"].startswith(b.SEL_TRANSFER))
        self.assertEqual(tx["type"], "eip1559")
        # All numeric fields are strings
        for key in ("chainId", "nonce", "gas", "maxFeePerGas", "maxPriorityFeePerGas"):
            self.assertIsInstance(tx[key], str)

    def test_do_transfer_happy_path_warnings_empty(self):
        rpc = self._make_rpc_for_transfer()
        _, _, warns = b.do_transfer(
            network="mainnet", token=self.TOKEN, to=self.TO,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(warns, [])

    def test_do_transfer_happy_path_summary_ctx(self):
        rpc = self._make_rpc_for_transfer()
        _, ctx, _ = b.do_transfer(
            network="mainnet", token=self.TOKEN, to=self.TO,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(ctx["operation"], "transfer")
        self.assertEqual(ctx["network"], "mainnet")
        self.assertEqual(ctx["token"], self.TOKEN)
        self.assertEqual(ctx["symbol"], "USDC")
        self.assertEqual(ctx["decimals"], 6)
        self.assertEqual(ctx["human_amount"], "1.5")
        self.assertEqual(ctx["base_amount"], 1_500_000)
        self.assertFalse(ctx["is_max_uint"])
        self.assertIn("nonce", ctx)
        self.assertIn("gas", ctx)
        self.assertIn("max_fee", ctx)
        self.assertIn("max_priority_fee", ctx)

    # -----------------------------------------------------------------------
    # do_transfer — balanceOf soft-check (Issue 2.2)
    # -----------------------------------------------------------------------

    def test_do_transfer_balance_sufficient_no_warning(self):
        """Balance >= requested → no low_balance warning."""
        rpc = self._make_rpc_for_transfer(balance_hex=self.HEX_BALANCE_HIGH)
        _, _, warns = b.do_transfer(
            network="mainnet", token=self.TOKEN, to=self.TO,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(warns, [])

    def test_do_transfer_low_balance_queues_warning(self):
        """Balance < requested → low_balance warning; tx_dict still built."""
        rpc = self._make_rpc_for_transfer(balance_hex=self.HEX_BALANCE_LOW)
        tx, _, warns = b.do_transfer(
            network="mainnet", token=self.TOKEN, to=self.TO,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(len(warns), 1)
        kind, payload = warns[0]
        self.assertEqual(kind, "low_balance")
        self.assertIn("holder", payload)
        self.assertIn("current", payload)
        self.assertIn("requested", payload)
        self.assertIn("decimals", payload)
        self.assertIn("symbol", payload)
        # tx is still built
        self.assertIn("data", tx)

    def test_do_transfer_balance_rpc_error_queues_skipped_warning(self):
        """fetch_balance_of RPCError → balance_check_skipped warning; tx still built."""
        base_rpc = self._make_rpc_for_transfer()

        def _rpc_balance_fails(url, method, params):
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                if data.startswith(b.SEL_BALANCE_OF):
                    raise b._core.RPCError("balance rpc down")
            return base_rpc(url, method, params)

        tx, _, warns = b.do_transfer(
            network="mainnet", token=self.TOKEN, to=self.TO,
            amount="1.5", sender=self.SENDER, rpc=_rpc_balance_fails,
        )
        self.assertEqual(len(warns), 1)
        kind, payload = warns[0]
        self.assertEqual(kind, "balance_check_skipped")
        self.assertIn("reason", payload)
        # tx still built
        self.assertIn("data", tx)

    def test_do_transfer_estimate_gas_error_not_caught_by_balance_check(self):
        """estimate_gas RPCError must NOT be caught by the balance try/except (ADR-007)."""
        def _rpc_gas_fails(url, method, params):
            if method == "eth_estimateGas":
                raise b._core.RPCError("gas reverted")
            return self._make_rpc_for_transfer()(url, method, params)

        with self.assertRaises(b._core.RPCError):
            b.do_transfer(
                network="mainnet", token=self.TOKEN, to=self.TO,
                amount="1.5", sender=self.SENDER, rpc=_rpc_gas_fails,
            )

    def test_do_transfer_decimals_fatal_balance_not_called(self):
        """fetch_decimals RPCError propagates; fetch_balance_of must not be called."""
        calls = []

        def _rpc_no_decimals(url, method, params):
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                if data.startswith(b.SEL_DECIMALS):
                    raise b._core.RPCError("decimals down")
                if data.startswith(b.SEL_BALANCE_OF):
                    calls.append("balanceOf")
            return self._make_rpc_for_transfer()(url, method, params)

        with self.assertRaises(b._core.RPCError):
            b.do_transfer(
                network="mainnet", token=self.TOKEN, to=self.TO,
                amount="1.5", sender=self.SENDER, rpc=_rpc_no_decimals,
            )
        self.assertEqual(calls, [], "fetch_balance_of must not be called when decimals fails")

    # -----------------------------------------------------------------------
    # do_approve — bounded amount
    # -----------------------------------------------------------------------

    def test_do_approve_happy_path_tx_shape(self):
        # Use allowance_hex=0 so no approve_race warning fires (common first-approval case)
        rpc = self._make_rpc_for_transfer(allowance_hex="0x" + "0" * 64)
        tx, ctx, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(tx["to"], self.TOKEN)
        self.assertEqual(tx["value"], "0")
        self.assertTrue(tx["data"].startswith(b.SEL_APPROVE))
        self.assertEqual(warns, [])
        self.assertEqual(ctx["operation"], "approve")
        self.assertFalse(ctx["is_max_uint"])

    # -----------------------------------------------------------------------
    # do_approve — approve_max=True
    # -----------------------------------------------------------------------

    def test_do_approve_max_data_ends_with_all_fs(self):
        """When approve_max=True, the calldata amount word must be all-F's."""
        rpc = self._make_rpc_for_transfer()
        tx, ctx, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, approve_max=True, rpc=rpc,
        )
        # The last 64 hex chars of tx["data"] are the uint256 encoding of MAX_UINT256
        self.assertTrue(tx["data"].endswith("f" * 64))
        self.assertTrue(ctx["is_max_uint"])

    def test_do_approve_max_queues_approve_max_warning(self):
        rpc = self._make_rpc_for_transfer()
        _, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, approve_max=True, rpc=rpc,
        )
        self.assertEqual(len(warns), 1)
        kind, payload = warns[0]
        self.assertEqual(kind, "approve_max")
        self.assertIn("token", payload)
        self.assertIn("spender", payload)

    # -----------------------------------------------------------------------
    # do_approve — approve-race guard (Issue 2.4)
    # -----------------------------------------------------------------------

    def test_do_approve_race_zero_allowance_no_warning(self):
        """Allowance == 0 → no approve_race warning (most common modern case)."""
        rpc = self._make_rpc_for_transfer(allowance_hex="0x" + "0" * 64)
        _, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        race_warns = [w for w in warns if w[0] == "approve_race"]
        self.assertEqual(race_warns, [])

    def test_do_approve_race_allowance_equals_requested_no_warning(self):
        """Allowance == requested → no race (no-op approve, same amount)."""
        # amount="1.5" with decimals=6 → amount_base=1_500_000
        rpc = self._make_rpc_for_transfer(
            allowance_hex="0x" + format(1_500_000, "064x")
        )
        _, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        race_warns = [w for w in warns if w[0] == "approve_race"]
        self.assertEqual(race_warns, [])

    def test_do_approve_race_nonzero_allowance_different_amount_queues_warning(self):
        """Allowance != 0 AND != requested → approve_race warning; tx still built."""
        rpc = self._make_rpc_for_transfer(
            allowance_hex="0x" + format(5_000_000, "064x")  # 5 USDC, != 1.5 USDC requested
        )
        tx, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount="1.5", sender=self.SENDER, rpc=rpc,
        )
        race_warns = [w for w in warns if w[0] == "approve_race"]
        self.assertEqual(len(race_warns), 1)
        kind, payload = race_warns[0]
        self.assertEqual(kind, "approve_race")
        self.assertIn("holder", payload)
        self.assertIn("spender", payload)
        self.assertIn("current", payload)
        self.assertIn("requested", payload)
        self.assertIn("decimals", payload)
        self.assertIn("symbol", payload)
        # tx is still built
        self.assertIn("data", tx)

    def test_do_approve_max_no_race_check(self):
        """approve_max=True → only approve_max warning; no approve_race."""
        rpc = self._make_rpc_for_transfer(
            allowance_hex="0x" + format(5_000_000, "064x")
        )
        _, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, approve_max=True, rpc=rpc,
        )
        kinds = [w[0] for w in warns]
        self.assertIn("approve_max", kinds)
        self.assertNotIn("approve_race", kinds)

    def test_do_approve_zero_amount_no_race_check(self):
        """amount=0 (revocation) → no approve_race warning."""
        rpc = self._make_rpc_for_transfer(
            allowance_hex="0x" + format(5_000_000, "064x")
        )
        _, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount="0", sender=self.SENDER, rpc=rpc,
        )
        race_warns = [w for w in warns if w[0] == "approve_race"]
        self.assertEqual(race_warns, [])

    def test_do_approve_race_rpc_error_queues_skipped_warning(self):
        """Allowance RPC error → approve_race_check_skipped warning; tx still built."""
        base_rpc = self._make_rpc_for_transfer()

        def _rpc_allowance_fails(url, method, params):
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                if data.startswith(b.SEL_ALLOWANCE):
                    raise b._core.RPCError("allowance rpc down")
            return base_rpc(url, method, params)

        tx, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount="1.5", sender=self.SENDER, rpc=_rpc_allowance_fails,
        )
        race_warns = [w for w in warns if w[0] == "approve_race_check_skipped"]
        self.assertEqual(len(race_warns), 1)
        kind, payload = race_warns[0]
        self.assertIn("reason", payload)
        self.assertIn("data", tx)

    def test_do_approve_estimate_gas_error_propagates(self):
        """estimate_gas RPCError propagates; not caught by race check (ADR-007)."""
        def _rpc_gas_fails(url, method, params):
            if method == "eth_estimateGas":
                raise b._core.RPCError("gas reverted")
            return self._make_rpc_for_transfer()(url, method, params)

        with self.assertRaises(b._core.RPCError):
            b.do_approve(
                network="mainnet", token=self.TOKEN, spender=self.SPENDER,
                amount="1.5", sender=self.SENDER, rpc=_rpc_gas_fails,
            )

    # -----------------------------------------------------------------------
    # do_approve — revoke=True (Issue 3.2)
    # -----------------------------------------------------------------------

    def test_do_approve_revoke_calldata_amount_word_all_zeros(self):
        """revoke=True: the 32-byte amount word in calldata must be all-zeros.

        Bit-pattern golden vector: the last 64 hex chars of tx['data'] (the
        uint256 amount word) must be '0' * 64. This is the highest-leverage
        regression guard — catches accidental use of MAX_UINT256 (approve_max)
        or any other non-zero amount on the revoke path.
        """
        rpc = self._make_rpc_for_transfer()
        tx, ctx, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, revoke=True, rpc=rpc,
        )
        # The last 64 hex chars of tx["data"] are the uint256 encoding of 0.
        self.assertTrue(tx["data"].endswith("0" * 64),
                        msg="amount word must be all-zeros for revoke; got: %r"
                            % tx["data"][-64:])

    def test_do_approve_revoke_op_label_is_revoke(self):
        """revoke=True: summary_ctx['op_label'] must be 'revoke'."""
        rpc = self._make_rpc_for_transfer()
        _, ctx, _ = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, revoke=True, rpc=rpc,
        )
        self.assertEqual(ctx["op_label"], "revoke")
        self.assertEqual(ctx["operation"], "revoke")

    def test_do_approve_revoke_queues_approve_revoke_warning(self):
        """revoke=True: warnings_list must contain exactly one ('approve_revoke', {...}) entry."""
        rpc = self._make_rpc_for_transfer()
        _, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, revoke=True, rpc=rpc,
        )
        revoke_warns = [w for w in warns if w[0] == "approve_revoke"]
        self.assertEqual(len(revoke_warns), 1,
                         msg="Expected exactly one approve_revoke warning; got: %r" % warns)
        kind, payload = revoke_warns[0]
        self.assertIn("symbol", payload)
        self.assertIn("token", payload)
        self.assertIn("spender", payload)

    def test_do_approve_revoke_human_to_base_units_not_called(self):
        """revoke=True: human_to_base_units must NOT be called (amount is hardcoded 0)."""
        rpc = self._make_rpc_for_transfer()
        with mock.patch("build_erc20.human_to_base_units") as mock_h2b:
            b.do_approve(
                network="mainnet", token=self.TOKEN, spender=self.SPENDER,
                amount=None, sender=self.SENDER, revoke=True, rpc=rpc,
            )
        mock_h2b.assert_not_called()

    def test_do_approve_revoke_fetch_decimals_still_called(self):
        """revoke=True: fetch_decimals must still be called (preserves ADR-006 contract).

        Even though decimals is not needed for amount conversion on the revoke
        path, it is still fetched so the summary block shows decimals for the
        operator's review and symmetry with approve_max=True is preserved.
        """
        decimals_called = []

        def _rpc_track_decimals(url, method, params):
            if method == "eth_call":
                data = params[0].get("data", "")
                if data.startswith(b.SEL_DECIMALS):
                    decimals_called.append(True)
            return self._make_rpc_for_transfer()(url, method, params)

        b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, revoke=True, rpc=_rpc_track_decimals,
        )
        self.assertTrue(decimals_called, msg="fetch_decimals must be called on the revoke path")

    def test_do_approve_revoke_fetch_decimals_rpc_error_propagates(self):
        """revoke=True: RPCError from fetch_decimals must propagate (FATAL — ADR-006)."""
        def _rpc_no_decimals(url, method, params):
            if method == "eth_call":
                data = params[0].get("data", "")
                if data.startswith(b.SEL_DECIMALS):
                    raise b._core.RPCError("decimals rpc down")
            return self._make_rpc_for_transfer()(url, method, params)

        with self.assertRaises(b._core.RPCError):
            b.do_approve(
                network="mainnet", token=self.TOKEN, spender=self.SPENDER,
                amount=None, sender=self.SENDER, revoke=True, rpc=_rpc_no_decimals,
            )

    def test_do_approve_revoke_and_approve_max_raises_value_error(self):
        """revoke=True + approve_max=True: ValueError raised (defense-in-depth for direct callers).

        Argparse prevents this at the CLI layer via the three-way mutex;
        this check guards callers that invoke do_approve directly (ADR-012).
        """
        rpc = self._make_rpc_for_transfer()
        with self.assertRaises(ValueError) as cm:
            b.do_approve(
                network="mainnet", token=self.TOKEN, spender=self.SPENDER,
                amount=None, sender=self.SENDER,
                revoke=True, approve_max=True, rpc=rpc,
            )
        self.assertIn("mutually exclusive", str(cm.exception))

    def test_do_approve_revoke_no_approve_race_check(self):
        """revoke=True: the approve-race soft-check must NOT be performed.

        Revocations (amount=0) have no race window; the soft-check is
        explicitly skipped for revoke just as it is for amount==0 and
        approve_max paths (Issue 3.2 implementation note).
        """
        rpc = self._make_rpc_for_transfer(
            allowance_hex="0x" + format(5_000_000, "064x")  # non-zero allowance
        )
        _, _, warns = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, revoke=True, rpc=rpc,
        )
        race_warns = [w for w in warns if w[0] in ("approve_race", "approve_race_check_skipped")]
        self.assertEqual(race_warns, [],
                         msg="revoke must not trigger approve_race check; warns=%r" % warns)

    def test_do_approve_revoke_tx_uses_approve_selector(self):
        """revoke=True: calldata must start with SEL_APPROVE (no new selector — ADR-005)."""
        rpc = self._make_rpc_for_transfer()
        tx, _, _ = b.do_approve(
            network="mainnet", token=self.TOKEN, spender=self.SPENDER,
            amount=None, sender=self.SENDER, revoke=True, rpc=rpc,
        )
        self.assertTrue(tx["data"].startswith(b.SEL_APPROVE),
                        msg="revoke must use SEL_APPROVE selector; got: %r" % tx["data"][:10])

    # -----------------------------------------------------------------------
    # do_transfer_from — happy path (sufficient allowance)
    # -----------------------------------------------------------------------

    def test_do_transfer_from_happy_path_no_warnings(self):
        # Allowance HIGH (10_000_000 >= 1_500_000 requested)
        rpc = self._make_rpc_for_transfer(allowance_hex=self.HEX_ALLOWANCE_HIGH)
        _, _, warns = b.do_transfer_from(
            network="mainnet", token=self.TOKEN,
            from_=self.FROM_, to=self.TO, amount="1.5",
            sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(warns, [])

    def test_do_transfer_from_happy_path_tx_shape(self):
        rpc = self._make_rpc_for_transfer(allowance_hex=self.HEX_ALLOWANCE_HIGH)
        tx, ctx, _ = b.do_transfer_from(
            network="mainnet", token=self.TOKEN,
            from_=self.FROM_, to=self.TO, amount="1.5",
            sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(tx["to"], self.TOKEN)
        self.assertTrue(tx["data"].startswith(b.SEL_TRANSFER_FROM))
        self.assertEqual(ctx["operation"], "transfer-from")

    # -----------------------------------------------------------------------
    # do_transfer_from — low allowance (soft-check warns, tx still built)
    # -----------------------------------------------------------------------

    def test_do_transfer_from_low_allowance_queues_warning(self):
        rpc = self._make_rpc_for_transfer(allowance_hex=self.HEX_ALLOWANCE_LOW)
        tx, ctx, warns = b.do_transfer_from(
            network="mainnet", token=self.TOKEN,
            from_=self.FROM_, to=self.TO, amount="1.5",
            sender=self.SENDER, rpc=rpc,
        )
        self.assertEqual(len(warns), 1)
        kind, payload = warns[0]
        self.assertEqual(kind, "low_allowance")
        self.assertIn("current", payload)
        self.assertIn("requested", payload)
        # tx is still built despite the low allowance
        self.assertIn("data", tx)

    # -----------------------------------------------------------------------
    # do_transfer_from — allowance RPC error (soft-check skipped, tx still built)
    # -----------------------------------------------------------------------

    def test_do_transfer_from_allowance_rpc_error_queues_skipped_warning(self):
        """When fetch_allowance raises RPCError the soft-check is skipped; tx is still built."""
        base_rpc = self._make_rpc_for_transfer()

        def _rpc_allowance_fails(url, method, params):
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                if data.startswith(b.SEL_ALLOWANCE):
                    raise b._core.RPCError("allowance rpc down")
            return base_rpc(url, method, params)

        tx, _, warns = b.do_transfer_from(
            network="mainnet", token=self.TOKEN,
            from_=self.FROM_, to=self.TO, amount="1.5",
            sender=self.SENDER, rpc=_rpc_allowance_fails,
        )
        self.assertEqual(len(warns), 1)
        kind, payload = warns[0]
        self.assertEqual(kind, "allowance_check_skipped")
        self.assertIn("reason", payload)
        # tx was still built
        self.assertIn("data", tx)

    # -----------------------------------------------------------------------
    # No-fallback regressions (ADR-007)
    # -----------------------------------------------------------------------

    def test_do_transfer_fetch_decimals_raises_propagates(self):
        """When fetch_decimals raises RPCError, do_transfer must propagate it (no tx)."""
        def _rpc_no_decimals(url, method, params):
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                if data.startswith(b.SEL_DECIMALS):
                    raise b._core.RPCError("decimals rpc down")
            return self._make_rpc_for_transfer()(url, method, params)

        with self.assertRaises(b._core.RPCError):
            b.do_transfer(
                network="mainnet", token=self.TOKEN, to=self.TO,
                amount="1.5", sender=self.SENDER, rpc=_rpc_no_decimals,
            )

    def test_do_transfer_estimate_gas_raises_propagates(self):
        """When estimate_gas raises RPCError, do_transfer must propagate it (ADR-007)."""
        def _rpc_no_gas(url, method, params):
            if method == "eth_estimateGas":
                raise b._core.RPCError("execution reverted")
            return self._make_rpc_for_transfer()(url, method, params)

        with self.assertRaises(b._core.RPCError):
            b.do_transfer(
                network="mainnet", token=self.TOKEN, to=self.TO,
                amount="1.5", sender=self.SENDER, rpc=_rpc_no_gas,
            )


class TestCliDispatch(unittest.TestCase):
    """Tests for the Layer 4 cli_dispatch section.

    Tests argparse smoke, address validation, happy paths, warning paths, and
    the no-fallback regression (ADR-007) at the CLI layer.
    """

    # Pre-validated 40-hex-char addresses used across all CLI tests.
    TOKEN   = "0x" + "a" * 40
    TO      = "0x" + "b" * 40
    SENDER  = "0x" + "c" * 40
    FROM_   = "0x" + "d" * 40
    SPENDER = "0x" + "e" * 40

    # The fake TX dict that mocked do_* functions return.
    FAKE_TX = {
        "type": "eip1559",
        "chainId": "1",
        "nonce": "5",
        "to": "0x" + "a" * 40,
        "value": "0",
        "data": "0xdeadbeef",
        "gas": "78066",
        "maxFeePerGas": "21000000000",
        "maxPriorityFeePerGas": "1000000000",
    }

    FAKE_CTX = {
        "operation": "transfer",
        "network": "mainnet",
        "chain_id": 1,
        "token": "0x" + "a" * 40,
        "symbol": "USDC",
        "decimals": 6,
        "human_amount": "1.5",
        "base_amount": 1_500_000,
        "is_max_uint": False,
        "from_": "0x" + "c" * 40,
        "to": "0x" + "b" * 40,
        "nonce": 5,
        "gas": 78066,
        "max_fee": 21_000_000_000,
        "max_priority_fee": 1_000_000_000,
    }

    # -----------------------------------------------------------------------
    # Argparse smoke tests
    # -----------------------------------------------------------------------

    def test_top_level_help_lists_subcommands(self):
        """main(["--help"]) exits via SystemExit; stdout lists all three subcommands."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit) as cm:
                b.main(["--help"])
        # argparse exits 0 on --help
        self.assertEqual(cm.exception.code, 0)
        output = fake_out.getvalue()
        self.assertIn("transfer", output)
        self.assertIn("approve", output)
        self.assertIn("transfer-from", output)

    def test_transfer_help_lists_required_flags(self):
        """transfer --help exits 0; stdout mentions required flags."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit) as cm:
                b.main(["transfer", "--help"])
        self.assertEqual(cm.exception.code, 0)
        output = fake_out.getvalue()
        self.assertIn("--token", output)
        self.assertIn("--to", output)
        self.assertIn("--amount", output)
        self.assertIn("--sender", output)

    def test_approve_help_lists_mutex_flags(self):
        """approve --help exits 0; stdout mentions --amount and --approve-max."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit) as cm:
                b.main(["approve", "--help"])
        self.assertEqual(cm.exception.code, 0)
        output = fake_out.getvalue()
        self.assertIn("--amount", output)
        self.assertIn("--approve-max", output)

    def test_transfer_from_help_lists_required_flags(self):
        """transfer-from --help exits 0; stdout mentions --from and required flags."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit) as cm:
                b.main(["transfer-from", "--help"])
        self.assertEqual(cm.exception.code, 0)
        output = fake_out.getvalue()
        self.assertIn("--from", output)
        self.assertIn("--to", output)
        self.assertIn("--amount", output)

    # -----------------------------------------------------------------------
    # Mutex enforcement for approve (--amount XOR --approve-max)
    # -----------------------------------------------------------------------

    def _approve_base_args(self):
        return [
            "approve", "--network", "hoodi",
            "--token", self.TOKEN,
            "--spender", self.SPENDER,
            "--sender", self.SENDER,
        ]

    def test_approve_both_amount_and_approve_max_raises(self):
        """argparse must reject both --amount and --approve-max together."""
        args = self._approve_base_args() + ["--amount", "1", "--approve-max"]
        with self.assertRaises(SystemExit) as cm:
            b.main(args)
        self.assertNotEqual(cm.exception.code, 0)

    def test_approve_neither_amount_nor_approve_max_raises(self):
        """argparse must reject neither --amount nor --approve-max being set."""
        args = self._approve_base_args()
        with self.assertRaises(SystemExit) as cm:
            b.main(args)
        self.assertNotEqual(cm.exception.code, 0)

    # -----------------------------------------------------------------------
    # Issue 3.2: --revoke CLI tests
    # -----------------------------------------------------------------------

    def test_approve_revoke_help_shows_revoke_flag(self):
        """approve --help must list --revoke in the mutex group."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit) as cm:
                b.main(["approve", "--help"])
        self.assertEqual(cm.exception.code, 0)
        self.assertIn("--revoke", fake_out.getvalue())

    def test_approve_revoke_happy_path_exit_0(self):
        """approve --revoke: exit 0, JSON on stdout, summary on stderr with 'operation: revoke'."""
        revoke_ctx = {
            "op_label":       "revoke",
            "operation":      "revoke",
            "network":        "mainnet",
            "chain_id":       1,
            "token":          self.TOKEN,
            "symbol":         "USDC",
            "decimals":       6,
            "human_amount":   "0",
            "base_amount":    0,
            "is_max_uint":    False,
            "from_":          self.SENDER,
            "holder":         self.SENDER,
            "spender":        self.SPENDER,
            "nonce":          5,
            "gas":            78066,
            "max_fee":        21_000_000_000,
            "max_priority_fee": 1_000_000_000,
        }
        warns = [("approve_revoke", {
            "symbol": "USDC",
            "token": self.TOKEN,
            "spender": self.SPENDER,
        })]
        with mock.patch("build_erc20.do_approve",
                        return_value=(self.FAKE_TX, revoke_ctx, warns)):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "approve", "--network", "mainnet",
                        "--token", self.TOKEN, "--spender", self.SPENDER,
                        "--revoke", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        # JSON on stdout
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)
        # Summary on stderr contains 'operation: revoke'
        stderr = fake_err.getvalue()
        self.assertIn("operation", stderr)
        self.assertIn("revoke", stderr)
        # Revoke confirmation block on stderr
        self.assertIn("USDC", stderr)

    def test_approve_revoke_and_amount_rejected_by_argparse(self):
        """--revoke and --amount together: argparse exits 2 (mutex violation)."""
        args = self._approve_base_args() + ["--amount", "1.5", "--revoke"]
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            with self.assertRaises(SystemExit) as cm:
                b.main(args)
        self.assertEqual(cm.exception.code, 2)
        # argparse default mutex error message contains 'not allowed with'
        self.assertIn("not allowed with", fake_err.getvalue())

    def test_approve_revoke_and_approve_max_rejected_by_argparse(self):
        """--revoke and --approve-max together: argparse exits 2 (mutex violation)."""
        args = self._approve_base_args() + ["--approve-max", "--revoke"]
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            with self.assertRaises(SystemExit) as cm:
                b.main(args)
        self.assertEqual(cm.exception.code, 2)
        self.assertIn("not allowed with", fake_err.getvalue())

    def test_approve_none_of_three_rejected_by_argparse(self):
        """No --amount / --approve-max / --revoke: argparse exits 2 (required mutex)."""
        args = self._approve_base_args()  # no mutex argument
        with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
            with self.assertRaises(SystemExit) as cm:
                b.main(args)
        self.assertEqual(cm.exception.code, 2)
        # argparse's "one of the arguments ... is required" message
        stderr = fake_err.getvalue()
        self.assertTrue(
            "one of the arguments" in stderr or "required" in stderr,
            msg="expected 'required' error message; got: %r" % stderr,
        )

    # -----------------------------------------------------------------------
    # Address validation failure → exit 1, error: on stderr, empty stdout
    # -----------------------------------------------------------------------

    def test_address_validation_failure_returns_1(self):
        """Bad --token address → main returns 1."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                result = b.main([
                    "transfer", "--network", "mainnet",
                    "--token", "not-an-address",
                    "--to", self.TO,
                    "--amount", "1.5",
                    "--sender", self.SENDER,
                ])
        self.assertEqual(result, 1)
        self.assertIn("error:", fake_err.getvalue())
        self.assertEqual(fake_out.getvalue(), "")

    def test_address_validation_bad_to_returns_1(self):
        """Bad --to address → main returns 1, error: on stderr."""
        with mock.patch("sys.stdout", new_callable=io.StringIO):
            with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                result = b.main([
                    "transfer", "--network", "mainnet",
                    "--token", self.TOKEN,
                    "--to", "bad",
                    "--amount", "1.5",
                    "--sender", self.SENDER,
                ])
        self.assertEqual(result, 1)
        self.assertIn("error:", fake_err.getvalue())

    # -----------------------------------------------------------------------
    # Happy path — transfer (exit 0, JSON on stdout, summary on stderr)
    # -----------------------------------------------------------------------

    def test_transfer_happy_path_exit_0(self):
        """transfer happy path returns 0."""
        with mock.patch("build_erc20.do_transfer",
                        return_value=(self.FAKE_TX, self.FAKE_CTX, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO):
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    result = b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)

    def test_transfer_happy_path_stdout_is_valid_json(self):
        """transfer happy path: stdout contains exactly the tx JSON."""
        with mock.patch("build_erc20.do_transfer",
                        return_value=(self.FAKE_TX, self.FAKE_CTX, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

    def test_transfer_happy_path_stderr_contains_summary(self):
        """transfer happy path: stderr contains summary labels."""
        with mock.patch("build_erc20.do_transfer",
                        return_value=(self.FAKE_TX, self.FAKE_CTX, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO):
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        stderr = fake_err.getvalue()
        self.assertIn("operation", stderr)
        self.assertIn("transfer", stderr)

    # -----------------------------------------------------------------------
    # Happy path — approve (bounded amount)
    # -----------------------------------------------------------------------

    def test_approve_happy_path_exit_0(self):
        """approve with --amount returns 0 and valid JSON on stdout."""
        approve_ctx = dict(self.FAKE_CTX, operation="approve",
                           holder=self.SENDER, spender=self.SPENDER, is_max_uint=False)
        with mock.patch("build_erc20.do_approve",
                        return_value=(self.FAKE_TX, approve_ctx, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    result = b.main([
                        "approve", "--network", "mainnet",
                        "--token", self.TOKEN, "--spender", self.SPENDER,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

    # -----------------------------------------------------------------------
    # Happy path — transfer-from (sufficient allowance)
    # -----------------------------------------------------------------------

    def test_transfer_from_happy_path_exit_0(self):
        """transfer-from happy path returns 0 and valid JSON on stdout."""
        tf_ctx = dict(self.FAKE_CTX, operation="transfer-from",
                      from_=self.FROM_, sender=self.SENDER)
        with mock.patch("build_erc20.do_transfer_from",
                        return_value=(self.FAKE_TX, tf_ctx, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    result = b.main([
                        "transfer-from", "--network", "mainnet",
                        "--token", self.TOKEN,
                        "--from", self.FROM_,
                        "--to", self.TO,
                        "--amount", "1.5",
                        "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

    # -----------------------------------------------------------------------
    # --approve-max warning path
    # -----------------------------------------------------------------------

    def test_approve_max_path_returns_0_with_warning_on_stderr(self):
        """--approve-max path: exit 0; WARNING: on stderr; JSON on stdout."""
        approve_ctx = dict(self.FAKE_CTX, operation="approve",
                           is_max_uint=True,
                           holder=self.SENDER, spender=self.SPENDER)
        warns = [("approve_max", {
            "symbol": "USDC",
            "token": self.TOKEN,
            "spender": self.SPENDER,
        })]
        with mock.patch("build_erc20.do_approve",
                        return_value=(self.FAKE_TX, approve_ctx, warns)):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "approve", "--network", "mainnet",
                        "--token", self.TOKEN, "--spender", self.SPENDER,
                        "--approve-max", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        self.assertIn("WARNING:", fake_err.getvalue())
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

    # -----------------------------------------------------------------------
    # transfer-from low-allowance path
    # -----------------------------------------------------------------------

    def test_transfer_from_low_allowance_returns_0_with_warning(self):
        """Low allowance warning path: exit 0; WARNING: on stderr; JSON on stdout.

        Uses the real do_transfer_from via a thin wrapper that injects a low-
        allowance fake rpc. The real _soft_check_allowance payload (which includes
        'symbol' after the 2.3 refactor) flows through the real emit_warning
        (**payload), exercising the true contract and catching the Fix-1 TypeError.
        """
        ta = TestTxAssembly()
        rpc = ta._make_rpc_for_transfer(allowance_hex=ta.HEX_ALLOWANCE_LOW)
        # Capture original before patching to avoid recursion when the wrapper
        # calls do_transfer_from while it is still patched.
        _real_do_transfer_from = b.do_transfer_from

        def wrapper(*args, **kw):
            kw["rpc"] = rpc
            return _real_do_transfer_from(*args, **kw)

        with mock.patch("build_erc20.do_transfer_from", side_effect=wrapper):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer-from", "--network", "mainnet",
                        "--token", ta.TOKEN,
                        "--from", ta.FROM_,
                        "--to", ta.TO,
                        "--amount", "1.5",
                        "--sender", ta.SENDER,
                    ])
        self.assertEqual(result, 0)
        self.assertIn("WARNING:", fake_err.getvalue())
        self.assertIn("allowance", fake_err.getvalue())
        parsed = json.loads(fake_out.getvalue())
        self.assertIn("type", parsed)

    # -----------------------------------------------------------------------
    # balanceOf soft-check at the CLI layer (Issue 2.2)
    # -----------------------------------------------------------------------

    def test_approve_race_warning_returns_0_warning_on_stderr_json_on_stdout(self):
        """Non-zero current allowance → exit 0, WARNING: on stderr, valid JSON on stdout."""
        approve_ctx = dict(self.FAKE_CTX, operation="approve",
                           holder=self.SENDER, spender=self.SPENDER, is_max_uint=False)
        warns = [("approve_race", {
            "holder":    self.SENDER,
            "spender":   self.SPENDER,
            "current":   5_000_000,
            "requested": 1_500_000,
            "decimals":  6,
            "symbol":    "USDC",
        })]
        with mock.patch("build_erc20.do_approve",
                        return_value=(self.FAKE_TX, approve_ctx, warns)):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "approve", "--network", "mainnet",
                        "--token", self.TOKEN, "--spender", self.SPENDER,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        self.assertIn("WARNING:", fake_err.getvalue())
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

    def test_transfer_low_balance_returns_0_warning_on_stderr_json_on_stdout(self):
        """Low balance: exit 0, WARNING: on stderr, valid JSON on stdout."""
        warns = [("low_balance", {
            "holder":    self.SENDER,
            "current":   1,
            "requested": 1_500_000,
            "decimals":  6,
            "symbol":    "USDC",
        })]
        with mock.patch("build_erc20.do_transfer",
                        return_value=(self.FAKE_TX, self.FAKE_CTX, warns)):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        self.assertIn("WARNING:", fake_err.getvalue())
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

    # -----------------------------------------------------------------------
    # Issue 2.6: verify ERC-20 helper picks up new networks (test-only)
    # -----------------------------------------------------------------------

    def test_network_choices_includes_all_four(self):
        """--network choices on each subparser == ['holesky','hoodi','mainnet','sepolia']."""
        parser = b._build_parser()
        expected = ["holesky", "hoodi", "mainnet", "sepolia"]
        for sp_name in ("transfer", "approve", "transfer-from"):
            # Walk the subparser actions to find the --network argument
            subparsers_action = None
            for action in parser._actions:
                if hasattr(action, "_name_parser_map"):
                    subparsers_action = action
                    break
            sp = subparsers_action._name_parser_map[sp_name]
            network_action = next(
                a for a in sp._actions if "--network" in getattr(a, "option_strings", [])
            )
            self.assertEqual(sorted(network_action.choices), expected,
                             msg="subparser '%s' choices mismatch" % sp_name)

    def test_main_transfer_sepolia(self):
        """transfer --network sepolia with mocked do_transfer → exit 0, chainId=='11155111'."""
        sepolia_tx = dict(self.FAKE_TX, chainId="11155111")
        captured = {}

        def fake_do_transfer(network, token, to, amount, sender, **kw):
            captured["network"] = network
            return (sepolia_tx, self.FAKE_CTX, [])

        with mock.patch("build_erc20.do_transfer", side_effect=fake_do_transfer):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    result = b.main([
                        "transfer", "--network", "sepolia",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.0", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        self.assertEqual(captured["network"], "sepolia")
        tx = json.loads(fake_out.getvalue())
        self.assertEqual(tx["chainId"], "11155111")

    def test_main_transfer_holesky(self):
        """transfer --network holesky with mocked do_transfer → exit 0, chainId=='17000'."""
        holesky_tx = dict(self.FAKE_TX, chainId="17000")
        captured = {}

        def fake_do_transfer(network, token, to, amount, sender, **kw):
            captured["network"] = network
            return (holesky_tx, self.FAKE_CTX, [])

        with mock.patch("build_erc20.do_transfer", side_effect=fake_do_transfer):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    result = b.main([
                        "transfer", "--network", "holesky",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.0", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        self.assertEqual(captured["network"], "holesky")
        tx = json.loads(fake_out.getvalue())
        self.assertEqual(tx["chainId"], "17000")

    def test_network_nope_rejected_by_argparse(self):
        """--network nope is rejected by argparse with exit code 2."""
        with self.assertRaises(SystemExit) as cm:
            with mock.patch("sys.stderr", new_callable=io.StringIO):
                b.main([
                    "transfer", "--network", "nope",
                    "--token", self.TOKEN, "--to", self.TO,
                    "--amount", "1.0", "--sender", self.SENDER,
                ])
        self.assertEqual(cm.exception.code, 2)

    # -----------------------------------------------------------------------
    # No-fallback regression at the CLI layer (ADR-007)
    # -----------------------------------------------------------------------

    def test_cli_no_fallback_estimate_gas_rpc_error_returns_1_empty_stdout(self):
        """When estimate_gas raises RPCError, main returns 1 and stdout is empty (ADR-007)."""
        with mock.patch("build_erc20.do_transfer",
                        side_effect=b._core.RPCError("execution reverted")):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 1)
        self.assertEqual(fake_out.getvalue(), "")
        self.assertIn("error:", fake_err.getvalue())
        self.assertIn("execution reverted", fake_err.getvalue())

    # -----------------------------------------------------------------------
    # Issue 2.7: --summary-only flag
    # -----------------------------------------------------------------------

    def test_transfer_help_lists_summary_only(self):
        """transfer --help includes --summary-only."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit):
                b.main(["transfer", "--help"])
        self.assertIn("--summary-only", fake_out.getvalue())

    def test_approve_help_lists_summary_only(self):
        """approve --help includes --summary-only."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit):
                b.main(["approve", "--help"])
        self.assertIn("--summary-only", fake_out.getvalue())

    def test_transfer_from_help_lists_summary_only(self):
        """transfer-from --help includes --summary-only."""
        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
            with self.assertRaises(SystemExit):
                b.main(["transfer-from", "--help"])
        self.assertIn("--summary-only", fake_out.getvalue())

    def test_transfer_summary_only_exit_0_empty_stdout_summary_on_stderr(self):
        """transfer --summary-only → exit 0, stdout empty, summary on stderr."""
        with mock.patch("build_erc20.do_transfer",
                        return_value=(self.FAKE_TX, self.FAKE_CTX, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                        "--summary-only",
                    ])
        self.assertEqual(result, 0)
        self.assertEqual(fake_out.getvalue(), "")
        self.assertIn("operation", fake_err.getvalue())

    def test_approve_max_summary_only_exit_0_empty_stdout_warning_and_summary_on_stderr(self):
        """approve --approve-max --summary-only → exit 0, stdout empty,
        stderr contains approve_max WARNING and summary block."""
        approve_ctx = dict(self.FAKE_CTX, operation="approve",
                           is_max_uint=True,
                           holder=self.SENDER, spender=self.SPENDER)
        warns = [("approve_max", {
            "symbol": "USDC",
            "token": self.TOKEN,
            "spender": self.SPENDER,
        })]
        with mock.patch("build_erc20.do_approve",
                        return_value=(self.FAKE_TX, approve_ctx, warns)):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "approve", "--network", "mainnet",
                        "--token", self.TOKEN, "--spender", self.SPENDER,
                        "--approve-max", "--sender", self.SENDER,
                        "--summary-only",
                    ])
        self.assertEqual(result, 0)
        self.assertEqual(fake_out.getvalue(), "")
        stderr = fake_err.getvalue()
        self.assertIn("WARNING:", stderr)
        self.assertIn("operation", stderr)

    def test_summary_only_estimate_gas_raises_exit_1_empty_stdout_error_on_stderr(self):
        """--summary-only does NOT mask fatal errors: estimate_gas raises → exit 1,
        stdout empty, error: on stderr."""
        with mock.patch("build_erc20.do_transfer",
                        side_effect=b._core.RPCError("execution reverted")):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                        "--summary-only",
                    ])
        self.assertEqual(result, 1)
        self.assertEqual(fake_out.getvalue(), "")
        self.assertIn("error:", fake_err.getvalue())

    def test_transfer_without_summary_only_still_prints_json(self):
        """Without --summary-only, happy-path JSON still goes to stdout (unchanged)."""
        with mock.patch("build_erc20.do_transfer",
                        return_value=(self.FAKE_TX, self.FAKE_CTX, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    result = b.main([
                        "transfer", "--network", "mainnet",
                        "--token", self.TOKEN, "--to", self.TO,
                        "--amount", "1.5", "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

    def test_transfer_from_summary_only_exit_0_empty_stdout(self):
        """transfer-from --summary-only → exit 0, stdout empty."""
        tf_ctx = dict(self.FAKE_CTX, operation="transfer-from",
                      from_=self.FROM_, sender=self.SENDER)
        with mock.patch("build_erc20.do_transfer_from",
                        return_value=(self.FAKE_TX, tf_ctx, [])):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO):
                    result = b.main([
                        "transfer-from", "--network", "mainnet",
                        "--token", self.TOKEN,
                        "--from", self.FROM_,
                        "--to", self.TO,
                        "--amount", "1.5",
                        "--sender", self.SENDER,
                        "--summary-only",
                    ])
        self.assertEqual(result, 0)
        self.assertEqual(fake_out.getvalue(), "")

    # -----------------------------------------------------------------------
    # Issue 2.8b: cross-feature regression matrix
    # -----------------------------------------------------------------------

    def test_regression_matrix_warnings_x_ops_summary_only(self):
        """4-warning × ops × --summary-only matrix.

        Each subTest sets up a mocked-rpc scenario that fires one warning family,
        runs main() with --summary-only, and asserts:
          - exit 0
          - stdout is empty
          - stderr contains the WARNING: text + the summary block

        Cells (relocated from 2.7 and 2.8 AC lists):
          A: approve --approve-max --summary-only          → approve_max warning
          B: transfer-from low-allowance --summary-only    → low_allowance warning
          C: transfer low-balance --summary-only           → low_balance warning
          D: approve non-zero current-allowance --summary-only → approve_race warning
        """
        # ---- Cell A: approve --approve-max → approve_max warning ----
        with self.subTest(op="approve", warning="approve_max", summary_only=True):
            approve_ctx = dict(self.FAKE_CTX, operation="approve",
                               is_max_uint=True,
                               holder=self.SENDER, spender=self.SPENDER)
            warns = [("approve_max", {
                "symbol": "USDC",
                "token": self.TOKEN,
                "spender": self.SPENDER,
            })]
            with mock.patch("build_erc20.do_approve",
                            return_value=(self.FAKE_TX, approve_ctx, warns)):
                with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                    with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                        result = b.main([
                            "approve", "--network", "mainnet",
                            "--token", self.TOKEN, "--spender", self.SPENDER,
                            "--approve-max", "--sender", self.SENDER,
                            "--summary-only",
                        ])
            self.assertEqual(result, 0)
            self.assertEqual(fake_out.getvalue(), "")
            stderr = fake_err.getvalue()
            self.assertIn("WARNING:", stderr)
            self.assertIn("UNLIMITED", stderr)      # approve_max wording
            self.assertIn("operation", stderr)      # summary block present

        # ---- Cell B: transfer-from low-allowance + --summary-only ----
        # Uses real do_transfer_from via wrapper injection so the real
        # _soft_check_allowance payload (with 'symbol') flows through emit_warning.
        with self.subTest(op="transfer-from", warning="low_allowance", summary_only=True):
            ta = TestTxAssembly()
            rpc_b = ta._make_rpc_for_transfer(allowance_hex=ta.HEX_ALLOWANCE_LOW)
            # Capture original before patching to avoid recursion.
            _real_dtf = b.do_transfer_from

            def _wrapper_b(*args, **kw):
                kw["rpc"] = rpc_b
                return _real_dtf(*args, **kw)

            with mock.patch("build_erc20.do_transfer_from", side_effect=_wrapper_b):
                with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                    with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                        result = b.main([
                            "transfer-from", "--network", "mainnet",
                            "--token", ta.TOKEN,
                            "--from", ta.FROM_,
                            "--to", ta.TO,
                            "--amount", "1.5",
                            "--sender", ta.SENDER,
                            "--summary-only",
                        ])
            self.assertEqual(result, 0)
            self.assertEqual(fake_out.getvalue(), "")
            stderr = fake_err.getvalue()
            self.assertIn("WARNING:", stderr)
            self.assertIn("allowance", stderr)      # low_allowance wording
            self.assertIn("operation", stderr)      # summary block present

        # ---- Cell C: transfer low-balance + --summary-only ----
        with self.subTest(op="transfer", warning="low_balance", summary_only=True):
            warns = [("low_balance", {
                "holder":    self.SENDER,
                "current":   1,
                "requested": 1_500_000,
                "decimals":  6,
                "symbol":    "USDC",
            })]
            with mock.patch("build_erc20.do_transfer",
                            return_value=(self.FAKE_TX, self.FAKE_CTX, warns)):
                with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                    with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                        result = b.main([
                            "transfer", "--network", "mainnet",
                            "--token", self.TOKEN, "--to", self.TO,
                            "--amount", "1.5", "--sender", self.SENDER,
                            "--summary-only",
                        ])
            self.assertEqual(result, 0)
            self.assertEqual(fake_out.getvalue(), "")
            stderr = fake_err.getvalue()
            self.assertIn("WARNING:", stderr)
            self.assertIn("balance", stderr)        # low_balance wording
            self.assertIn("operation", stderr)      # summary block present

        # ---- Cell D: approve non-zero current-allowance + --summary-only ----
        with self.subTest(op="approve", warning="approve_race", summary_only=True):
            approve_ctx = dict(self.FAKE_CTX, operation="approve",
                               is_max_uint=False,
                               holder=self.SENDER, spender=self.SPENDER)
            warns = [("approve_race", {
                "holder":    self.SENDER,
                "spender":   self.SPENDER,
                "current":   5_000_000,
                "requested": 1_500_000,
                "decimals":  6,
                "symbol":    "USDC",
            })]
            with mock.patch("build_erc20.do_approve",
                            return_value=(self.FAKE_TX, approve_ctx, warns)):
                with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                    with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                        result = b.main([
                            "approve", "--network", "mainnet",
                            "--token", self.TOKEN, "--spender", self.SPENDER,
                            "--amount", "1.5", "--sender", self.SENDER,
                            "--summary-only",
                        ])
            self.assertEqual(result, 0)
            self.assertEqual(fake_out.getvalue(), "")
            stderr = fake_err.getvalue()
            self.assertIn("WARNING:", stderr)
            self.assertIn("SWC-114", stderr)        # approve_race wording
            self.assertIn("operation", stderr)      # summary block present

    def test_regression_matrix_networks_x_ops_summary_only(self):
        """4-networks × 3-ops × --summary-only matrix (12 cells).

        For each (network, op) pair, wraps the real do_* function with a
        side_effect that calls through but captures the returned tx_dict.
        Asserts:
          - exit 0
          - stdout empty (--summary-only suppresses JSON)
          - the captured tx_dict["chainId"] matches the expected chain ID

        Expected chain IDs per network:
          mainnet  → "1"
          hoodi    → "560048"
          sepolia  → "11155111"
          holesky  → "17000"
        """
        # Build a real-but-mocked RPC for do_* calls (from TestTxAssembly harness).
        # We can't use self._make_rpc_for_transfer() directly here because that
        # belongs to TestTxAssembly — reuse the same pattern inline.
        tx_assembly = TestTxAssembly()  # re-use the _make_rpc_for_transfer helper

        NETWORK_CHAIN_IDS = {
            "mainnet": "1",
            "hoodi":   "560048",
            "sepolia": "11155111",
            "holesky": "17000",
        }
        OPS = ["transfer", "approve", "transfer-from"]

        for network, expected_chain_id in NETWORK_CHAIN_IDS.items():
            for op in OPS:
                with self.subTest(network=network, op=op, summary_only=True):
                    # Build a fresh mocked RPC for this cell.
                    # For approve: zero allowance avoids approve_race warning.
                    # For transfer-from: high allowance avoids low_allowance warning.
                    # (Both > requested amount so no soft-check warning fires.)
                    allowance_hex = (
                        "0x" + "0" * 64
                        if op == "approve"
                        else tx_assembly.HEX_ALLOWANCE_HIGH
                    )
                    rpc = tx_assembly._make_rpc_for_transfer(
                        allowance_hex=allowance_hex,
                        balance_hex=tx_assembly.HEX_BALANCE_HIGH,
                    )
                    captured = {}

                    def make_wrapper(fn, cap, mock_rpc):
                        """Wrap a do_* function: inject mock_rpc and capture tx_dict."""
                        def wrapper(*args, **kw):
                            # Inject the mock rpc so no live network calls happen.
                            kw["rpc"] = mock_rpc
                            result = fn(*args, **kw)
                            cap["tx_dict"] = result[0]
                            return result
                        return wrapper

                    if op == "transfer":
                        side_effect = make_wrapper(b.do_transfer, captured, rpc)
                        patch_target = "build_erc20.do_transfer"
                        cli_args = [
                            "transfer", "--network", network,
                            "--token", tx_assembly.TOKEN,
                            "--to",    tx_assembly.TO,
                            "--amount", "1.0",
                            "--sender", tx_assembly.SENDER,
                            "--summary-only",
                        ]
                    elif op == "approve":
                        side_effect = make_wrapper(b.do_approve, captured, rpc)
                        patch_target = "build_erc20.do_approve"
                        cli_args = [
                            "approve", "--network", network,
                            "--token",   tx_assembly.TOKEN,
                            "--spender", tx_assembly.SPENDER,
                            "--amount",  "1.0",
                            "--sender",  tx_assembly.SENDER,
                            "--summary-only",
                        ]
                    else:  # transfer-from
                        side_effect = make_wrapper(b.do_transfer_from, captured, rpc)
                        patch_target = "build_erc20.do_transfer_from"
                        cli_args = [
                            "transfer-from", "--network", network,
                            "--token",  tx_assembly.TOKEN,
                            "--from",   tx_assembly.FROM_,
                            "--to",     tx_assembly.TO,
                            "--amount", "1.0",
                            "--sender", tx_assembly.SENDER,
                            "--summary-only",
                        ]

                    with mock.patch(patch_target, side_effect=side_effect):
                        with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                            with mock.patch("sys.stderr", new_callable=io.StringIO):
                                exit_code = b.main(cli_args)

                    self.assertEqual(exit_code, 0,
                                     msg="expected exit 0 for %s %s" % (network, op))
                    self.assertEqual(fake_out.getvalue(), "",
                                     msg="stdout should be empty for %s %s" % (network, op))
                    self.assertIn("tx_dict", captured,
                                  msg="do_* was not called for %s %s" % (network, op))
                    self.assertEqual(
                        captured["tx_dict"]["chainId"],
                        expected_chain_id,
                        msg="chainId mismatch for network=%s op=%s" % (network, op),
                    )


class TestWarningE2E(unittest.TestCase):
    """End-to-end regression tests that drive main() against a real mocked RPC.

    These tests exercise the real do_* functions by injecting a fake rpc via a
    thin wrapper patch (same technique as test_regression_matrix_networks_x_ops
    in TestCliDispatch). The real _soft_check_allowance payload flows through
    the real emit_warning(**payload), catching any warn_* signature mismatches.

    This is the class of bug Fix 1 addresses: warn_low_allowance lacked the
    'symbol' param that _soft_check_allowance started including after the 2.3
    refactor, causing a TypeError that escaped main()'s except handler.

    Re-uses _make_rpc_for_transfer from TestTxAssembly to share the selector-aware
    dispatcher (architecture A14 comment).
    """

    # -----------------------------------------------------------------------
    # Fix 2, Part 1 — low_allowance e2e (this is the regression guard for Fix 1)
    # -----------------------------------------------------------------------

    def test_e2e_transfer_from_low_allowance_exit_0_warning_on_stderr(self):
        """E2E: transfer-from with real low-allowance RPC → exit 0, WARNING with
        'allowance' on stderr, valid JSON on stdout.

        This test FAILS before Fix 1 (TypeError in warn_low_allowance missing
        'symbol' param) and PASSES after Fix 1 (symbol=None param added).

        Uses the real do_transfer_from via a thin wrapper that injects rpc (captured
        before patching to avoid recursion) — so the real _soft_check_allowance
        payload (which includes 'symbol') flows through the real emit_warning(**payload).
        """
        ta = TestTxAssembly()
        rpc = ta._make_rpc_for_transfer(allowance_hex=ta.HEX_ALLOWANCE_LOW)
        # Capture original before patching to prevent recursive mock calls.
        _real = b.do_transfer_from

        def _wrapper(*args, **kw):
            kw["rpc"] = rpc
            return _real(*args, **kw)

        with mock.patch("build_erc20.do_transfer_from", side_effect=_wrapper):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer-from", "--network", "mainnet",
                        "--token", ta.TOKEN,
                        "--from", ta.FROM_,
                        "--to", ta.TO,
                        "--amount", "1.5",
                        "--sender", ta.SENDER,
                    ])

        self.assertEqual(result, 0, msg="exit code must be 0 (warn-don't-block)")
        stderr = fake_err.getvalue()
        self.assertIn("WARNING:", stderr, msg="low_allowance WARNING must appear on stderr")
        self.assertIn("allowance", stderr, msg="'allowance' must appear in WARNING text")
        # stdout must contain valid JSON (the tx_dict)
        stdout = fake_out.getvalue().strip()
        self.assertTrue(stdout, msg="stdout must not be empty")
        parsed = json.loads(stdout)
        self.assertIn("type", parsed)
        self.assertEqual(parsed["type"], "eip1559")

    def test_e2e_transfer_from_low_allowance_warning_contains_symbol(self):
        """E2E: low_allowance WARNING must include the symbol (USDC from fake RPC).

        Specifically guards against the pre-Fix-1 crash: warn_low_allowance had
        no 'symbol' param but the payload from _soft_check_allowance included one
        after the 2.3 refactor.
        """
        ta = TestTxAssembly()
        rpc = ta._make_rpc_for_transfer(allowance_hex=ta.HEX_ALLOWANCE_LOW)
        _real = b.do_transfer_from

        def _wrapper(*args, **kw):
            kw["rpc"] = rpc
            return _real(*args, **kw)

        with mock.patch("build_erc20.do_transfer_from", side_effect=_wrapper):
            with mock.patch("sys.stdout", new_callable=io.StringIO):
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer-from", "--network", "mainnet",
                        "--token", ta.TOKEN,
                        "--from", ta.FROM_,
                        "--to", ta.TO,
                        "--amount", "1.5",
                        "--sender", ta.SENDER,
                    ])

        self.assertEqual(result, 0)
        stderr = fake_err.getvalue()
        # After Fix 1, the symbol "USDC" (returned by fake RPC) must appear in the warning.
        self.assertIn("USDC", stderr, msg="token symbol must appear in low_allowance WARNING")

    # -----------------------------------------------------------------------
    # Fix 2, Part 2 — allowance_check_skipped e2e
    # -----------------------------------------------------------------------

    def test_e2e_transfer_from_allowance_rpc_error_exit_0_warning_on_stderr(self):
        """E2E: transfer-from with allowance RPC error → exit 0, WARNING on stderr,
        valid JSON on stdout.

        Uses the real do_transfer_from via wrapper injection — the real soft-check
        catch flows through emit_warning("allowance_check_skipped", ...).
        """
        ta = TestTxAssembly()
        base_rpc = ta._make_rpc_for_transfer()

        def _rpc_allowance_fails(url, method, params):
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                if data.startswith(b.SEL_ALLOWANCE):
                    raise b._core.RPCError("simulated allowance rpc failure")
            return base_rpc(url, method, params)

        _real = b.do_transfer_from

        def _wrapper(*args, **kw):
            kw["rpc"] = _rpc_allowance_fails
            return _real(*args, **kw)

        with mock.patch("build_erc20.do_transfer_from", side_effect=_wrapper):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer-from", "--network", "mainnet",
                        "--token", ta.TOKEN,
                        "--from", ta.FROM_,
                        "--to", ta.TO,
                        "--amount", "1.5",
                        "--sender", ta.SENDER,
                    ])

        self.assertEqual(result, 0, msg="allowance_check_skipped must not block build (exit 0)")
        stderr = fake_err.getvalue()
        self.assertIn("WARNING:", stderr)
        self.assertIn("allowance", stderr)
        stdout = fake_out.getvalue().strip()
        self.assertTrue(stdout, msg="tx JSON must still appear on stdout")
        parsed = json.loads(stdout)
        self.assertEqual(parsed["type"], "eip1559")

    # -----------------------------------------------------------------------
    # Fix 2, Part 3 — balance_check_skipped e2e
    # -----------------------------------------------------------------------

    def test_e2e_transfer_balance_rpc_error_exit_0_warning_on_stderr(self):
        """E2E: transfer with balanceOf RPC error → exit 0, WARNING on stderr,
        valid JSON on stdout.

        Uses the real do_transfer via wrapper injection — the real balance soft-check
        catch flows through emit_warning("balance_check_skipped", ...).
        """
        ta = TestTxAssembly()
        base_rpc = ta._make_rpc_for_transfer()

        def _rpc_balance_fails(url, method, params):
            if method == "eth_call":
                call_obj = params[0]
                data = call_obj.get("data", "")
                if data.startswith(b.SEL_BALANCE_OF):
                    raise b._core.RPCError("simulated balanceOf rpc failure")
            return base_rpc(url, method, params)

        _real = b.do_transfer

        def _wrapper(*args, **kw):
            kw["rpc"] = _rpc_balance_fails
            return _real(*args, **kw)

        with mock.patch("build_erc20.do_transfer", side_effect=_wrapper):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer", "--network", "mainnet",
                        "--token", ta.TOKEN,
                        "--to", ta.TO,
                        "--amount", "1.5",
                        "--sender", ta.SENDER,
                    ])

        self.assertEqual(result, 0, msg="balance_check_skipped must not block build (exit 0)")
        stderr = fake_err.getvalue()
        self.assertIn("WARNING:", stderr)
        # The balance_check_skipped warning text includes "balanceOf"
        self.assertIn("balanceOf", stderr)
        stdout = fake_out.getvalue().strip()
        self.assertTrue(stdout, msg="tx JSON must still appear on stdout")
        parsed = json.loads(stdout)
        self.assertEqual(parsed["type"], "eip1559")


if __name__ == "__main__":
    unittest.main()
