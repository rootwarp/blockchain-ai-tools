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
        """Low allowance warning path: exit 0; WARNING: on stderr; JSON on stdout."""
        tf_ctx = dict(self.FAKE_CTX, operation="transfer-from",
                      from_=self.FROM_, sender=self.SENDER)
        warns = [("low_allowance", {
            "holder":    self.FROM_,
            "spender":   self.SENDER,
            "current":   1,
            "requested": 1_500_000,
            "decimals":  6,
        })]
        with mock.patch("build_erc20.do_transfer_from",
                        return_value=(self.FAKE_TX, tf_ctx, warns)):
            with mock.patch("sys.stdout", new_callable=io.StringIO) as fake_out:
                with mock.patch("sys.stderr", new_callable=io.StringIO) as fake_err:
                    result = b.main([
                        "transfer-from", "--network", "mainnet",
                        "--token", self.TOKEN,
                        "--from", self.FROM_,
                        "--to", self.TO,
                        "--amount", "1.5",
                        "--sender", self.SENDER,
                    ])
        self.assertEqual(result, 0)
        self.assertIn("WARNING:", fake_err.getvalue())
        parsed = json.loads(fake_out.getvalue())
        self.assertEqual(parsed, self.FAKE_TX)

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


if __name__ == "__main__":
    unittest.main()
