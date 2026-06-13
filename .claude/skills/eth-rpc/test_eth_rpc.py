import io
import json
import unittest
from unittest import mock

import eth_rpc as r


class TestNetworkConfig(unittest.TestCase):
    def test_mainnet(self):
        chain_id, url = r.network_config("mainnet")
        self.assertEqual(chain_id, 1)
        self.assertEqual(url, "https://ethereum-rpc.publicnode.com")

    def test_hoodi(self):
        chain_id, url = r.network_config("hoodi")
        self.assertEqual(chain_id, 560048)
        self.assertEqual(url, "https://ethereum-hoodi-rpc.publicnode.com")

    def test_unknown_network_raises(self):
        with self.assertRaises(ValueError):
            r.network_config("goerli")


class TestValidateHexAddress(unittest.TestCase):
    GOOD = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

    def test_valid_returns_same(self):
        self.assertEqual(r.validate_hex_address(self.GOOD), self.GOOD)

    def test_all_lower_ok(self):
        self.assertEqual(r.validate_hex_address(self.GOOD.lower()), self.GOOD.lower())

    def test_missing_prefix_raises(self):
        with self.assertRaises(ValueError):
            r.validate_hex_address(self.GOOD[2:])

    def test_too_short_raises(self):
        with self.assertRaises(ValueError):
            r.validate_hex_address("0x1234")

    def test_non_hex_raises(self):
        with self.assertRaises(ValueError):
            r.validate_hex_address("0x" + "z" * 40)


class TestValidateRawTx(unittest.TestCase):
    def test_valid_returns_same(self):
        self.assertEqual(r.validate_raw_tx("0x02f8ab"), "0x02f8ab")

    def test_missing_prefix_raises(self):
        with self.assertRaises(ValueError):
            r.validate_raw_tx("02f8ab")

    def test_odd_length_raises(self):
        with self.assertRaises(ValueError):
            r.validate_raw_tx("0x02f8a")

    def test_non_hex_raises(self):
        with self.assertRaises(ValueError):
            r.validate_raw_tx("0xzz")

    def test_empty_body_raises(self):
        with self.assertRaises(ValueError):
            r.validate_raw_tx("0x")


class TestParseHexInt(unittest.TestCase):
    def test_simple(self):
        self.assertEqual(r.parse_hex_int("0x5"), 5)

    def test_zero(self):
        self.assertEqual(r.parse_hex_int("0x0"), 0)

    def test_block_number(self):
        self.assertEqual(r.parse_hex_int("0x2dea62"), 3009122)

    def test_missing_prefix_raises(self):
        with self.assertRaises(ValueError):
            r.parse_hex_int("5")

    def test_non_string_raises(self):
        with self.assertRaises(ValueError):
            r.parse_hex_int(None)


class TestRpcCall(unittest.TestCase):
    def _fake_response(self, payload):
        body = json.dumps(payload).encode("utf-8")
        resp = mock.MagicMock()
        resp.read.return_value = body
        resp.__enter__.return_value = resp
        resp.__exit__.return_value = False
        return resp

    def test_returns_result(self):
        with mock.patch(
            "eth_rpc.urllib.request.urlopen",
            return_value=self._fake_response({"jsonrpc": "2.0", "id": 1, "result": "0x5"}),
        ):
            self.assertEqual(r.rpc_call("https://x", "eth_getBalance", ["0xabc", "latest"]), "0x5")

    def test_jsonrpc_error_raises(self):
        with mock.patch(
            "eth_rpc.urllib.request.urlopen",
            return_value=self._fake_response(
                {"jsonrpc": "2.0", "id": 1, "error": {"code": -32000, "message": "boom"}}
            ),
        ):
            with self.assertRaises(r.RPCError):
                r.rpc_call("https://x", "eth_chainId", [])

    def test_transport_error_raises(self):
        with mock.patch(
            "eth_rpc.urllib.request.urlopen", side_effect=OSError("connection refused")
        ):
            with self.assertRaises(r.RPCError):
                r.rpc_call("https://x", "eth_chainId", [])

    def test_missing_result_raises(self):
        with mock.patch(
            "eth_rpc.urllib.request.urlopen",
            return_value=self._fake_response({"jsonrpc": "2.0", "id": 1}),
        ):
            with self.assertRaises(r.RPCError):
                r.rpc_call("https://x", "eth_chainId", [])

    def test_sets_user_agent_header(self):
        # Regression: publicnode rejects the default "Python-urllib/x.y" UA with HTTP 403.
        with mock.patch(
            "eth_rpc.urllib.request.urlopen",
            return_value=self._fake_response({"jsonrpc": "2.0", "id": 1, "result": "0x1"}),
        ) as urlopen:
            r.rpc_call("https://x", "eth_chainId", [])
        sent_request = urlopen.call_args[0][0]
        self.assertEqual(sent_request.get_header("User-agent"), r.USER_AGENT)
        self.assertNotIn("Python-urllib", sent_request.get_header("User-agent"))


def make_fake_rpc(results, errors=()):
    """Return a fake rpc(url, method, params). `results` maps method->value;
    methods in `errors` raise RPCError."""
    def _rpc(url, method, params):
        if method in errors:
            raise r.RPCError("simulated failure for %s" % method)
        return results[method]
    return _rpc


class TestDoBalance(unittest.TestCase):
    ADDR = "0x6302794A4F2487a2540c40E2dbB211Ff6AF1CD20"

    def test_normal_balance(self):
        rpc = make_fake_rpc({"eth_getBalance": "0x0c7d713b49da0000"})  # 0.9 ETH
        out = r.do_balance("hoodi", self.ADDR, rpc=rpc)
        self.assertEqual(
            out,
            {
                "network": "hoodi",
                "chainId": "560048",
                "address": self.ADDR,
                "blockTag": "latest",
                "balanceWei": "900000000000000000",
                "balanceEth": "0.9",
            },
        )

    def test_zero_balance(self):
        rpc = make_fake_rpc({"eth_getBalance": "0x0"})
        out = r.do_balance("mainnet", self.ADDR, rpc=rpc)
        self.assertEqual(out["chainId"], "1")
        self.assertEqual(out["balanceWei"], "0")
        self.assertEqual(out["balanceEth"], "0")

    def test_one_eth(self):
        rpc = make_fake_rpc({"eth_getBalance": "0x0de0b6b3a7640000"})  # 1 ETH
        out = r.do_balance("hoodi", self.ADDR, rpc=rpc)
        self.assertEqual(out["balanceWei"], "1000000000000000000")
        self.assertEqual(out["balanceEth"], "1")

    def test_queries_latest(self):
        seen = {}

        def rpc(url, method, params):
            seen["params"] = params
            return "0x0"

        r.do_balance("hoodi", self.ADDR, rpc=rpc)
        self.assertEqual(seen["params"], [self.ADDR, "latest"])

    def test_malformed_address_raises(self):
        with self.assertRaises(ValueError):
            r.do_balance("hoodi", "0xnope", rpc=make_fake_rpc({"eth_getBalance": "0x0"}))


class TestWeiToEthStr(unittest.TestCase):
    def test_zero(self):
        self.assertEqual(r.wei_to_eth_str(0), "0")

    def test_one_eth(self):
        self.assertEqual(r.wei_to_eth_str(1_000_000_000_000_000_000), "1")

    def test_tenth_eth(self):
        self.assertEqual(r.wei_to_eth_str(100_000_000_000_000_000), "0.1")

    def test_one_and_a_half(self):
        self.assertEqual(r.wei_to_eth_str(1_500_000_000_000_000_000), "1.5")

    def test_sub_eth_no_float_drift(self):
        # 0.899976476745084 ETH expressed exactly
        self.assertEqual(r.wei_to_eth_str(899_976_476_745_084_000), "0.899976476745084")

    def test_one_wei(self):
        self.assertEqual(r.wei_to_eth_str(1), "0.000000000000000001")

    def test_non_int_raises(self):
        with self.assertRaises(ValueError):
            r.wei_to_eth_str("1")


if __name__ == "__main__":
    unittest.main()
