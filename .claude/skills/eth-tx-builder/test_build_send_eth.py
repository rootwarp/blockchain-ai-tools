import io
import json
import unittest
from unittest import mock

import build_send_eth as b


class TestNetworkConfig(unittest.TestCase):
    def test_mainnet(self):
        chain_id, url = b.network_config("mainnet")
        self.assertEqual(chain_id, 1)
        self.assertEqual(url, "https://ethereum-rpc.publicnode.com")

    def test_hoodi(self):
        chain_id, url = b.network_config("hoodi")
        self.assertEqual(chain_id, 560048)
        self.assertEqual(url, "https://ethereum-hoodi-rpc.publicnode.com")

    def test_unknown_network_raises(self):
        with self.assertRaises(ValueError):
            b.network_config("goerli")


class TestGweiToWei(unittest.TestCase):
    def test_one_gwei(self):
        self.assertEqual(b.gwei_to_wei(1), 1_000_000_000)

    def test_zero(self):
        self.assertEqual(b.gwei_to_wei(0), 0)

    def test_large_value_no_overflow(self):
        # 1e9 gwei = 1 ETH = 1e18 wei (Python int is arbitrary precision)
        self.assertEqual(b.gwei_to_wei(1_000_000_000), 1_000_000_000_000_000_000)

    def test_negative_raises(self):
        with self.assertRaises(ValueError):
            b.gwei_to_wei(-1)


class TestValidateHexAddress(unittest.TestCase):
    GOOD = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

    def test_valid_returns_same(self):
        self.assertEqual(b.validate_hex_address(self.GOOD), self.GOOD)

    def test_all_lower_ok(self):
        self.assertEqual(
            b.validate_hex_address(self.GOOD.lower()), self.GOOD.lower()
        )

    def test_missing_prefix_raises(self):
        with self.assertRaises(ValueError):
            b.validate_hex_address(self.GOOD[2:])

    def test_too_short_raises(self):
        with self.assertRaises(ValueError):
            b.validate_hex_address("0x1234")

    def test_non_hex_raises(self):
        with self.assertRaises(ValueError):
            b.validate_hex_address("0x" + "z" * 40)


class TestParseHexInt(unittest.TestCase):
    def test_simple(self):
        self.assertEqual(b.parse_hex_int("0x5"), 5)

    def test_two_gwei(self):
        self.assertEqual(b.parse_hex_int("0x77359400"), 2_000_000_000)

    def test_zero(self):
        self.assertEqual(b.parse_hex_int("0x0"), 0)

    def test_missing_prefix_raises(self):
        with self.assertRaises(ValueError):
            b.parse_hex_int("5")

    def test_non_string_raises(self):
        with self.assertRaises(ValueError):
            b.parse_hex_int(None)


class TestComputeMaxFee(unittest.TestCase):
    def test_base_times_two_plus_tip(self):
        # base=2 gwei, tip=1 gwei -> 2*2 + 1 = 5 gwei
        self.assertEqual(
            b.compute_max_fee(2_000_000_000, 1_000_000_000), 5_000_000_000
        )

    def test_zero_base(self):
        self.assertEqual(b.compute_max_fee(0, 1_000_000_000), 1_000_000_000)


class TestRpcCall(unittest.TestCase):
    def _fake_response(self, payload):
        body = json.dumps(payload).encode("utf-8")
        resp = mock.MagicMock()
        resp.read.return_value = body
        # support `with urllib.request.urlopen(...) as resp:`
        resp.__enter__.return_value = resp
        resp.__exit__.return_value = False
        return resp

    def test_returns_result(self):
        with mock.patch(
            "build_send_eth.urllib.request.urlopen",
            return_value=self._fake_response(
                {"jsonrpc": "2.0", "id": 1, "result": "0x5"}
            ),
        ):
            self.assertEqual(
                b.rpc_call("https://x", "eth_getTransactionCount", ["0xabc", "pending"]),
                "0x5",
            )

    def test_jsonrpc_error_raises(self):
        with mock.patch(
            "build_send_eth.urllib.request.urlopen",
            return_value=self._fake_response(
                {"jsonrpc": "2.0", "id": 1, "error": {"code": -32000, "message": "boom"}}
            ),
        ):
            with self.assertRaises(b.RPCError):
                b.rpc_call("https://x", "eth_chainId", [])

    def test_transport_error_raises(self):
        with mock.patch(
            "build_send_eth.urllib.request.urlopen", side_effect=OSError("connection refused")
        ):
            with self.assertRaises(b.RPCError):
                b.rpc_call("https://x", "eth_chainId", [])


if __name__ == "__main__":
    unittest.main()
