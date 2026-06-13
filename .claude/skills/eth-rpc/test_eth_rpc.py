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


class TestDoBroadcastWait(unittest.TestCase):
    RAW = "0x02f8ab83088bb0"
    HASH = "0xd6133a2b2713dd86f4abe32421aed32f9945aed046dbc80751f5a03871799e85"

    def _seq_rpc(self, receipts):
        """rpc that returns HASH for send, then pops receipts for each getReceipt."""
        queue = list(receipts)

        def rpc(url, method, params):
            if method == "eth_sendRawTransaction":
                return self.HASH
            if method == "eth_getTransactionReceipt":
                return queue.pop(0)
            raise AssertionError("unexpected method %s" % method)

        return rpc

    def test_mined_after_pending(self):
        receipts = [
            None,
            {
                "status": "0x1",
                "blockNumber": "0x2dea62",
                "gasUsed": "0x5208",
                "effectiveGasPrice": "0x42c5f174",
            },
        ]
        slept = []
        out = r.do_broadcast(
            "hoodi", self.RAW, wait=True, wait_timeout=120, poll_interval=4,
            rpc=self._seq_rpc(receipts), sleep=lambda s: slept.append(s), now=lambda: 0.0,
        )
        self.assertEqual(out["txHash"], self.HASH)
        self.assertEqual(out["status"], "mined")
        self.assertEqual(out["receiptStatus"], "0x1")
        self.assertEqual(out["blockNumber"], "3009122")
        self.assertEqual(out["gasUsed"], "21000")
        self.assertEqual(out["effectiveGasPrice"], "1120268660")
        self.assertEqual(slept, [4])  # slept once between the two polls

    def test_reverted_is_failed(self):
        receipts = [{"status": "0x0", "blockNumber": "0x10", "gasUsed": "0x5208"}]
        out = r.do_broadcast(
            "hoodi", self.RAW, wait=True, wait_timeout=120, poll_interval=4,
            rpc=self._seq_rpc(receipts), sleep=lambda s: None, now=lambda: 0.0,
        )
        self.assertEqual(out["status"], "failed")
        self.assertEqual(out["receiptStatus"], "0x0")
        self.assertEqual(out["blockNumber"], "16")
        self.assertNotIn("effectiveGasPrice", out)  # absent field omitted

    def test_timeout_is_pending(self):
        clock = [0.0]

        def now():
            return clock[0]

        def sleep(s):
            clock[0] += s

        def rpc(url, method, params):
            if method == "eth_sendRawTransaction":
                return self.HASH
            return None  # receipt never appears

        out = r.do_broadcast(
            "hoodi", self.RAW, wait=True, wait_timeout=10, poll_interval=4,
            rpc=rpc, sleep=sleep, now=now,
        )
        self.assertEqual(out["status"], "pending")
        self.assertEqual(out["txHash"], self.HASH)
        self.assertNotIn("blockNumber", out)

    def test_no_wait_skips_receipt(self):
        # wait=False must not call eth_getTransactionReceipt
        def rpc(url, method, params):
            if method == "eth_sendRawTransaction":
                return self.HASH
            raise AssertionError("should not poll when wait=False")

        out = r.do_broadcast("hoodi", self.RAW, wait=False, rpc=rpc)
        self.assertEqual(out["status"], "submitted")


class TestDoBroadcastSubmit(unittest.TestCase):
    RAW = "0x02f8ab83088bb0"
    HASH = "0xd6133a2b2713dd86f4abe32421aed32f9945aed046dbc80751f5a03871799e85"

    def test_submit_returns_hash(self):
        seen = {}

        def rpc(url, method, params):
            seen["method"] = method
            seen["params"] = params
            return self.HASH

        out = r.do_broadcast("hoodi", self.RAW, rpc=rpc)
        self.assertEqual(seen["method"], "eth_sendRawTransaction")
        self.assertEqual(seen["params"], [self.RAW])
        self.assertEqual(
            out,
            {
                "network": "hoodi",
                "chainId": "560048",
                "txHash": self.HASH,
                "status": "submitted",
            },
        )

    def test_malformed_raw_raises(self):
        with self.assertRaises(ValueError):
            r.do_broadcast("hoodi", "not-hex", rpc=make_fake_rpc({"eth_sendRawTransaction": self.HASH}))

    def test_submit_rpc_error_propagates(self):
        rpc = make_fake_rpc({}, errors={"eth_sendRawTransaction"})
        with self.assertRaises(r.RPCError):
            r.do_broadcast("hoodi", self.RAW, rpc=rpc)


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
