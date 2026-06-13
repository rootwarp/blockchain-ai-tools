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

    def test_float_raises(self):
        with self.assertRaises(ValueError):
            b.gwei_to_wei(1.5)


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

    def test_sets_user_agent_header(self):
        # Regression: publicnode rejects the default "Python-urllib/x.y" UA with HTTP 403.
        with mock.patch(
            "build_send_eth.urllib.request.urlopen",
            return_value=self._fake_response({"jsonrpc": "2.0", "id": 1, "result": "0x1"}),
        ) as urlopen:
            b.rpc_call("https://x", "eth_chainId", [])
        sent_request = urlopen.call_args[0][0]
        self.assertEqual(sent_request.get_header("User-agent"), b.USER_AGENT)
        self.assertNotIn("Python-urllib", sent_request.get_header("User-agent"))


def make_fake_rpc(results, errors=()):
    """Return a fake rpc(url, method, params). `results` maps method->value;
    methods in `errors` raise RPCError."""
    def _rpc(url, method, params):
        if method in errors:
            raise b.RPCError("simulated failure for %s" % method)
        return results[method]
    return _rpc


class TestFetchHelpers(unittest.TestCase):
    def test_fetch_nonce(self):
        rpc = make_fake_rpc({"eth_getTransactionCount": "0x5"})
        self.assertEqual(b.fetch_nonce(rpc, "https://x", "0xabc"), 5)

    def test_fetch_base_fee(self):
        rpc = make_fake_rpc(
            {"eth_getBlockByNumber": {"baseFeePerGas": "0x77359400"}}
        )
        self.assertEqual(b.fetch_base_fee(rpc, "https://x"), 2_000_000_000)

    def test_fetch_base_fee_missing_raises(self):
        rpc = make_fake_rpc({"eth_getBlockByNumber": {"number": "0x1"}})
        with self.assertRaises(b.RPCError):
            b.fetch_base_fee(rpc, "https://x")

    def test_fetch_tip(self):
        rpc = make_fake_rpc({"eth_maxPriorityFeePerGas": "0x3b9aca00"})
        self.assertEqual(b.fetch_tip(rpc, "https://x"), 1_000_000_000)

    def test_fetch_tip_fallback_on_error(self):
        rpc = make_fake_rpc({}, errors={"eth_maxPriorityFeePerGas"})
        self.assertEqual(b.fetch_tip(rpc, "https://x"), b.DEFAULT_TIP_WEI)


class TestBuildTxRequest(unittest.TestCase):
    TO = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    SENDER = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

    def _rpc(self):
        return make_fake_rpc(
            {
                "eth_getTransactionCount": "0x5",
                "eth_getBlockByNumber": {"baseFeePerGas": "0x77359400"},  # 2 gwei
                "eth_maxPriorityFeePerGas": "0x3b9aca00",  # 1 gwei
            }
        )

    def test_full_request(self):
        tx = b.build_tx_request("mainnet", self.TO, 1000, self.SENDER, rpc=self._rpc())
        self.assertEqual(
            tx,
            {
                "type": "eip1559",
                "chainId": "1",
                "nonce": "5",
                "to": self.TO,
                "value": "1000000000000",  # 1000 gwei = 1e12 wei
                "data": "0x",
                "gas": "21000",
                "maxFeePerGas": "5000000000",  # 2*2e9 + 1e9
                "maxPriorityFeePerGas": "1000000000",
            },
        )

    def test_all_numeric_fields_are_decimal_strings(self):
        tx = b.build_tx_request("hoodi", self.TO, 0, self.SENDER, rpc=self._rpc())
        for k in ("chainId", "nonce", "value", "gas", "maxFeePerGas", "maxPriorityFeePerGas"):
            self.assertIsInstance(tx[k], str)
            self.assertTrue(tx[k].isdigit(), "%s not a decimal string: %r" % (k, tx[k]))
        self.assertEqual(tx["chainId"], "560048")
        self.assertEqual(tx["value"], "0")

    def test_malformed_to_raises(self):
        with self.assertRaises(ValueError):
            b.build_tx_request("mainnet", "0xnope", 1, self.SENDER, rpc=self._rpc())

    def test_malformed_sender_raises(self):
        with self.assertRaises(ValueError):
            b.build_tx_request("mainnet", self.TO, 1, "0xnope", rpc=self._rpc())


class TestMain(unittest.TestCase):
    TO = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    SENDER = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

    def _patched_build(self):
        # main() calls build_tx_request with the real rpc_call; patch it out.
        return mock.patch(
            "build_send_eth.build_tx_request",
            return_value={"type": "eip1559", "chainId": "1", "to": self.TO},
        )

    def test_success_prints_json_and_returns_zero(self):
        out = io.StringIO()
        with self._patched_build(), mock.patch("sys.stdout", out):
            rc = b.main(
                ["--network", "mainnet", "--to", self.TO,
                 "--amount-gwei", "1000", "--sender", self.SENDER]
            )
        self.assertEqual(rc, 0)
        parsed = json.loads(out.getvalue())
        self.assertEqual(parsed["type"], "eip1559")

    def test_build_error_prints_stderr_and_returns_one(self):
        err = io.StringIO()
        with mock.patch(
            "build_send_eth.build_tx_request", side_effect=b.RPCError("rpc down")
        ), mock.patch("sys.stderr", err):
            rc = b.main(
                ["--network", "mainnet", "--to", self.TO,
                 "--amount-gwei", "1000", "--sender", self.SENDER]
            )
        self.assertEqual(rc, 1)
        self.assertIn("rpc down", err.getvalue())

    def test_value_error_returns_one(self):
        err = io.StringIO()
        with mock.patch(
            "build_send_eth.build_tx_request", side_effect=ValueError("bad address")
        ), mock.patch("sys.stderr", err):
            rc = b.main(
                ["--network", "mainnet", "--to", "0xbad",
                 "--amount-gwei", "1", "--sender", self.SENDER]
            )
        self.assertEqual(rc, 1)
        self.assertIn("bad address", err.getvalue())


if __name__ == "__main__":
    unittest.main()
