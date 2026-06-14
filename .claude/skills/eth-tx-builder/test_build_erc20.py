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
        spender = "0xa49053f705a560f0717bc2e96dddcfe7edb7f98a"
        amount = 1_000_000  # 1 USDC at 6 decimals
        result = b.encode_approve(spender, amount)
        expected = (
            "0x095ea7b3"
            + b._encode_address(spender)
            + b._encode_uint256(amount)
        )
        self.assertEqual(result, expected)

    # --- encode_transfer_from ---

    def test_encode_transfer_from_bit_pattern(self):
        from_ = "0x890e560a6012bfa5d0d71a4a107dba4aed698f38"
        to = "0xa49053f705a560f0717bc2e96dddcfe7edb7f98a"
        amount = 500_000
        result = b.encode_transfer_from(from_, to, amount)
        expected = (
            "0x23b872dd"
            + b._encode_address(from_)
            + b._encode_address(to)
            + b._encode_uint256(amount)
        )
        self.assertEqual(result, expected)

    # --- encode_decimals_call / encode_symbol_call ---

    def test_encode_decimals_call_equals_selector(self):
        self.assertEqual(b.encode_decimals_call(), b.SEL_DECIMALS)

    def test_encode_symbol_call_equals_selector(self):
        self.assertEqual(b.encode_symbol_call(), b.SEL_SYMBOL)

    # --- encode_allowance_call ---

    def test_encode_allowance_call_bit_pattern(self):
        holder = "0x890e560a6012bfa5d0d71a4a107dba4aed698f38"
        spender = "0xa49053f705a560f0717bc2e96dddcfe7edb7f98a"
        result = b.encode_allowance_call(holder, spender)
        expected = (
            "0xdd62ed3e"
            + b._encode_address(holder)
            + b._encode_address(spender)
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

    # --- decode_allowance ---

    def test_decode_allowance_zero(self):
        self.assertEqual(b.decode_allowance("0x" + "0" * 64), 0)

    def test_decode_allowance_max_uint256(self):
        self.assertEqual(b.decode_allowance("0x" + "f" * 64), (1 << 256) - 1)

    # --- MAX_DECIMALS constant ---

    def test_max_decimals_value(self):
        self.assertEqual(b.MAX_DECIMALS, 36)


if __name__ == "__main__":
    unittest.main()
