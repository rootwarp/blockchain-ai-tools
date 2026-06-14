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


class TestContractReads(unittest.TestCase):
    """Tests for the Layer 2 contract_reads section.

    All network I/O is mocked via the injected `rpc` kwarg.
    """

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


if __name__ == "__main__":
    unittest.main()
