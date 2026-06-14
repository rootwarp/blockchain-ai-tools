import io
import json
import pathlib
import subprocess
import sys
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

    def test_sepolia_entry(self):
        chain_id, url = r.network_config("sepolia")
        self.assertEqual(chain_id, 11155111)
        self.assertEqual(url, "https://ethereum-sepolia-rpc.publicnode.com")

    def test_holesky_entry(self):
        chain_id, url = r.network_config("holesky")
        self.assertEqual(chain_id, 17000)
        self.assertEqual(url, "https://ethereum-holesky-rpc.publicnode.com")


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


class TestMain(unittest.TestCase):
    ADDR = "0x6302794A4F2487a2540c40E2dbB211Ff6AF1CD20"
    RAW = "0x02f8ab83088bb0"

    def test_balance_prints_json_returns_zero(self):
        out = io.StringIO()
        with mock.patch(
            "eth_rpc.do_balance",
            return_value={"network": "hoodi", "balanceEth": "1"},
        ), mock.patch("sys.stdout", out):
            rc = r.main(["balance", "--network", "hoodi", "--address", self.ADDR])
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out.getvalue())["balanceEth"], "1")

    def test_broadcast_prints_json_returns_zero(self):
        out = io.StringIO()
        with mock.patch(
            "eth_rpc.do_broadcast",
            return_value={"txHash": "0xabc", "status": "submitted"},
        ) as do_bc, mock.patch("sys.stdout", out):
            rc = r.main(["broadcast", "--network", "hoodi", "--raw-tx", self.RAW])
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out.getvalue())["txHash"], "0xabc")
        # default: wait is False
        _, kwargs = do_bc.call_args
        self.assertFalse(kwargs["wait"])

    def test_broadcast_wait_flag_forwarded(self):
        out = io.StringIO()
        with mock.patch(
            "eth_rpc.do_broadcast", return_value={"status": "mined"}
        ) as do_bc, mock.patch("sys.stdout", out):
            r.main(["broadcast", "--network", "hoodi", "--raw-tx", self.RAW,
                    "--wait", "--wait-timeout", "30"])
        _, kwargs = do_bc.call_args
        self.assertTrue(kwargs["wait"])
        self.assertEqual(kwargs["wait_timeout"], 30)

    def test_rpc_error_prints_stderr_returns_one(self):
        err = io.StringIO()
        with mock.patch(
            "eth_rpc.do_balance", side_effect=r.RPCError("rpc down")
        ), mock.patch("sys.stderr", err):
            rc = r.main(["balance", "--network", "hoodi", "--address", self.ADDR])
        self.assertEqual(rc, 1)
        self.assertIn("rpc down", err.getvalue())

    def test_value_error_prints_stderr_returns_one(self):
        err = io.StringIO()
        with mock.patch(
            "eth_rpc.do_broadcast", side_effect=ValueError("bad raw tx")
        ), mock.patch("sys.stderr", err):
            rc = r.main(["broadcast", "--network", "hoodi", "--raw-tx", "0xbad"])
        self.assertEqual(rc, 1)
        self.assertIn("bad raw tx", err.getvalue())

    def test_unknown_network_rejected_by_argparse(self):
        with self.assertRaises(SystemExit):
            r.main(["balance", "--network", "goerli", "--address", self.ADDR])

    def test_missing_subcommand_rejected(self):
        with self.assertRaises(SystemExit):
            r.main([])

    def test_call_dispatch_reaches_do_call(self):
        out = io.StringIO()
        with mock.patch("eth_rpc.do_call", return_value="0x88bb0") as mock_do_call, \
             mock.patch("sys.stdout", out):
            rc = r.main([
                "call", "--network", "hoodi",
                "--method", "eth_chainId", "--params", "[]",
            ])
        self.assertEqual(rc, 0)
        mock_do_call.assert_called_once()
        kwargs = mock_do_call.call_args[1]
        self.assertEqual(kwargs["method"], "eth_chainId")
        self.assertEqual(kwargs["params"], [])


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

    def test_receipt_poll_error_preserves_hash(self):
        def rpc(url, method, params):
            if method == "eth_sendRawTransaction":
                return self.HASH
            raise r.RPCError("node hiccup")

        with self.assertRaises(r.RPCError) as ctx:
            r.do_broadcast(
                "hoodi", self.RAW, wait=True, wait_timeout=120, poll_interval=4,
                rpc=rpc, sleep=lambda s: None, now=lambda: 0.0,
            )
        self.assertIn(self.HASH, str(ctx.exception))


class TestReceiptSummary(unittest.TestCase):
    def test_none_status_is_failed(self):
        out = r._receipt_summary({"status": None, "blockNumber": "0x10"})
        self.assertEqual(out["status"], "failed")
        self.assertIsNone(out["receiptStatus"])
        self.assertEqual(out["blockNumber"], "16")


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


SKILL_DIR = pathlib.Path(__file__).parent


class TestRpcCallBoundedBody(unittest.TestCase):
    """Tests for rpc_call / rpc_batch with max_body_bytes kwarg (ADR-013)."""

    def _fake_urlopen(self, body_bytes):
        resp = mock.MagicMock()
        resp.read = mock.MagicMock(side_effect=lambda n=None: body_bytes[:n] if n is not None else body_bytes)
        resp.__enter__ = mock.MagicMock(return_value=resp)
        resp.__exit__ = mock.MagicMock(return_value=False)
        return resp

    def test_unlimited_read_unchanged(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "result": "0x1"}).encode()
        with mock.patch("eth_rpc.urllib.request.urlopen", return_value=self._fake_urlopen(body)):
            result = r.rpc_call("https://x", "eth_blockNumber", [])
        self.assertEqual(result, "0x1")

    def test_body_under_limit_accepted(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "result": "0x1"}).encode()
        limit = len(body) + 100
        with mock.patch("eth_rpc.urllib.request.urlopen", return_value=self._fake_urlopen(body)):
            result = r.rpc_call("https://x", "eth_blockNumber", [], max_body_bytes=limit)
        self.assertEqual(result, "0x1")

    def test_body_exactly_at_limit_accepted(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "result": "0x1"}).encode()
        limit = len(body)
        with mock.patch("eth_rpc.urllib.request.urlopen", return_value=self._fake_urlopen(body)):
            result = r.rpc_call("https://x", "eth_blockNumber", [], max_body_bytes=limit)
        self.assertEqual(result, "0x1")

    def test_body_over_limit_raises_rpcerror(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "result": "0x1"}).encode()
        limit = len(body) - 1
        with mock.patch("eth_rpc.urllib.request.urlopen", return_value=self._fake_urlopen(body)):
            with self.assertRaises(r.RPCError) as ctx:
                r.rpc_call("https://x", "eth_blockNumber", [], max_body_bytes=limit)
        self.assertIn("--max-body-bytes", str(ctx.exception))
        self.assertIn(str(limit), str(ctx.exception))

    def test_do_call_forwards_max_body_bytes(self):
        # do_call should pass max_body_bytes to the injected rpc
        calls = []

        def fake_rpc(url, method, params, timeout, max_body_bytes=None):
            calls.append(max_body_bytes)
            return "0x1"

        r.do_call("https://x", method="eth_blockNumber", params=[], max_body_bytes=512, rpc=fake_rpc)
        self.assertEqual(calls, [512])


class TestRpcBatchBoundedBody(unittest.TestCase):
    """Tests for rpc_batch with max_body_bytes kwarg (ADR-013)."""

    def _fake_urlopen(self, body_bytes):
        resp = mock.MagicMock()
        resp.read = mock.MagicMock(side_effect=lambda n=None: body_bytes[:n] if n is not None else body_bytes)
        resp.__enter__ = mock.MagicMock(return_value=resp)
        resp.__exit__ = mock.MagicMock(return_value=False)
        return resp

    def test_body_over_limit_raises_rpcerror(self):
        wire = [{"jsonrpc": "2.0", "id": 0, "result": "0x1"}]
        body = json.dumps(wire).encode()
        limit = len(body) - 1
        with mock.patch("eth_rpc.urllib.request.urlopen", return_value=self._fake_urlopen(body)):
            with self.assertRaises(r.RPCError) as ctx:
                r.rpc_batch("https://x", [], max_body_bytes=limit)
        self.assertIn("--max-body-bytes", str(ctx.exception))

    def test_body_at_limit_accepted(self):
        wire = [{"jsonrpc": "2.0", "id": 0, "result": "0x1"}]
        body = json.dumps(wire).encode()
        limit = len(body)
        with mock.patch("eth_rpc.urllib.request.urlopen", return_value=self._fake_urlopen(body)):
            result = r.rpc_batch("https://x", [], max_body_bytes=limit)
        self.assertEqual(result, wire)

    def test_do_batch_forwards_max_body_bytes(self):
        calls = []

        def fake_rpc(url, payload, timeout, max_body_bytes=None):
            calls.append(max_body_bytes)
            return [{"jsonrpc": "2.0", "id": 0, "result": "0x1"}]

        r.do_batch("https://x",
                   calls=[{"method": "eth_blockNumber", "params": []}],
                   max_body_bytes=1024, rpc=fake_rpc)
        self.assertEqual(calls, [1024])


class TestDoDiagnostics(unittest.TestCase):
    """Tests for do_net_version and do_client_version."""

    URL = "https://ethereum-hoodi-rpc.publicnode.com"
    CHAIN_ID = 560048

    def _fake(self, result):
        def rpc(url, method, params, timeout, max_body_bytes=None):
            return result
        return rpc

    def test_net_version_happy_path(self):
        out = r.do_net_version(self.URL, self.CHAIN_ID, rpc=self._fake("560048"))
        self.assertEqual(out["netVersion"], "560048")
        self.assertEqual(out["chainId"], str(self.CHAIN_ID))

    def test_net_version_named_network_path(self):
        called_with = {}

        def rpc(url, method, params, timeout, max_body_bytes=None):
            called_with["method"] = method
            return "1"

        out = r.do_net_version("https://ethereum-rpc.publicnode.com", 1, rpc=rpc)
        self.assertEqual(called_with["method"], "net_version")
        self.assertEqual(out["chainId"], "1")

    def test_net_version_rpcerror_propagates(self):
        def rpc(url, method, params, timeout, max_body_bytes=None):
            raise r.RPCError("down")
        with self.assertRaises(r.RPCError):
            r.do_net_version(self.URL, self.CHAIN_ID, rpc=rpc)

    def test_client_version_happy_path(self):
        out = r.do_client_version(self.URL, self.CHAIN_ID, rpc=self._fake("Geth/v1.14"))
        self.assertEqual(out["clientVersion"], "Geth/v1.14")
        self.assertEqual(out["chainId"], str(self.CHAIN_ID))

    def test_client_version_rpcerror_propagates(self):
        def rpc(url, method, params, timeout, max_body_bytes=None):
            raise r.RPCError("down")
        with self.assertRaises(r.RPCError):
            r.do_client_version(self.URL, self.CHAIN_ID, rpc=rpc)

    def test_net_version_output_has_network_key(self):
        # Spec (Issue 3.7): output must include network, chainId, AND netVersion.
        out = r.do_net_version(self.URL, self.CHAIN_ID, network="hoodi", rpc=self._fake("560048"))
        self.assertIn("chainId", out)
        self.assertIn("netVersion", out)
        self.assertIn("network", out)
        self.assertEqual(out["network"], "hoodi")

    def test_client_version_output_has_network_key(self):
        # Spec (Issue 3.7): output must include network, chainId, AND clientVersion.
        out = r.do_client_version(self.URL, self.CHAIN_ID, network="hoodi", rpc=self._fake("Geth/v1"))
        self.assertIn("chainId", out)
        self.assertIn("clientVersion", out)
        self.assertIn("network", out)
        self.assertEqual(out["network"], "hoodi")


class TestDiagnosticsCli(unittest.TestCase):
    """Tests for net-version and client-version subcommands through main."""

    def _run(self, argv):
        import contextlib
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = r.main(argv)
        return rc, out.getvalue(), err.getvalue()

    def test_net_version_dispatch(self):
        with mock.patch("eth_rpc.do_net_version", return_value={"chainId": "1", "netVersion": "1"}) as mock_fn:
            rc, out, err = self._run(["net-version", "--network", "mainnet"])
        self.assertEqual(rc, 0, err)
        mock_fn.assert_called_once()

    def test_client_version_dispatch(self):
        with mock.patch("eth_rpc.do_client_version",
                        return_value={"chainId": "1", "clientVersion": "Geth"}) as mock_fn:
            rc, out, err = self._run(["client-version", "--network", "mainnet"])
        self.assertEqual(rc, 0, err)
        mock_fn.assert_called_once()

    def test_net_version_error_exits_one(self):
        with mock.patch("eth_rpc.do_net_version", side_effect=r.RPCError("down")):
            rc, out, err = self._run(["net-version", "--network", "hoodi"])
        self.assertEqual(rc, 1)
        self.assertIn("error:", err)

    def test_both_in_help(self):
        proc = __import__("subprocess").run(
            [sys.executable,
             str(__import__("pathlib").Path(__file__).parent / "eth_rpc.py"),
             "--help"],
            capture_output=True, text=True,
        )
        self.assertIn("net-version", proc.stdout)
        self.assertIn("client-version", proc.stdout)


class TestDenylistContents(unittest.TestCase):
    """Drift guard (ADR-011): any intentional change to the denylist constants
    requires updating both the constant and this test in the same commit."""

    def test_deny_methods_exact(self):
        self.assertEqual(
            r._DENY_METHODS,
            frozenset({
                "eth_sendRawTransaction",
                "eth_sendTransaction",
                "eth_sign",
                "eth_signTransaction",
                "eth_signTypedData",
                "eth_signTypedData_v3",
                "eth_signTypedData_v4",
            }),
        )

    def test_deny_prefixes_exact(self):
        self.assertEqual(
            r._DENY_PREFIXES,
            ("personal_", "admin_", "miner_", "engine_", "clique_"),
        )

    def test_loopback_hosts_exact(self):
        self.assertEqual(
            r._LOOPBACK_HOSTS,
            frozenset({"127.0.0.1", "localhost", "::1"}),
        )


class TestValidateRpcUrl(unittest.TestCase):
    def test_https_accepted(self):
        url = "https://ethereum-rpc.publicnode.com"
        self.assertEqual(r._validate_rpc_url(url), url)

    def test_http_loopback_ipv4_accepted(self):
        url = "http://127.0.0.1:8545"
        self.assertEqual(r._validate_rpc_url(url), url)

    def test_http_loopback_localhost_accepted(self):
        url = "http://localhost:8545"
        self.assertEqual(r._validate_rpc_url(url), url)

    def test_http_loopback_ipv6_accepted(self):
        # Bracketed form in URL; urlsplit strips brackets -> "::1"
        url = "http://[::1]:8545"
        self.assertEqual(r._validate_rpc_url(url), url)

    def test_http_non_loopback_rejected(self):
        with self.assertRaises(ValueError):
            r._validate_rpc_url("http://example.com")

    def test_ftp_scheme_rejected(self):
        with self.assertRaises(ValueError):
            r._validate_rpc_url("ftp://example.com/rpc")

    def test_unsupported_scheme_rejected(self):
        with self.assertRaises(ValueError):
            r._validate_rpc_url("ws://127.0.0.1:8545")


class TestResolveEndpoint(unittest.TestCase):
    def test_named_network_hoodi(self):
        chain_id, url = r._resolve_endpoint(network="hoodi")
        self.assertEqual(chain_id, 560048)
        self.assertEqual(url, "https://ethereum-hoodi-rpc.publicnode.com")

    def test_named_network_mainnet(self):
        chain_id, url = r._resolve_endpoint(network="mainnet")
        self.assertEqual(chain_id, 1)
        self.assertEqual(url, "https://ethereum-rpc.publicnode.com")

    def test_custom_url_and_chain_id(self):
        chain_id, url = r._resolve_endpoint(
            rpc_url="http://127.0.0.1:8545", chain_id=31337
        )
        self.assertEqual(chain_id, 31337)
        self.assertEqual(url, "http://127.0.0.1:8545")

    def test_mutual_exclusion_raises(self):
        with self.assertRaises(ValueError) as ctx:
            r._resolve_endpoint(
                network="hoodi", rpc_url="http://127.0.0.1:8545", chain_id=31337
            )
        self.assertIn("not both", str(ctx.exception))

    def test_mutual_exclusion_network_with_url_only_raises(self):
        with self.assertRaises(ValueError):
            r._resolve_endpoint(network="hoodi", rpc_url="http://127.0.0.1:8545")

    def test_missing_chain_id_raises(self):
        with self.assertRaises(ValueError) as ctx:
            r._resolve_endpoint(rpc_url="http://127.0.0.1:8545")
        self.assertIn("required together", str(ctx.exception))

    def test_missing_rpc_url_raises(self):
        with self.assertRaises(ValueError) as ctx:
            r._resolve_endpoint(chain_id=31337)
        self.assertIn("required together", str(ctx.exception))

    def test_neither_mode_raises(self):
        with self.assertRaises(ValueError):
            r._resolve_endpoint()


class TestCheckMethodPolicy(unittest.TestCase):
    """Tests for _check_method_policy — pure function, no mocked rpc needed."""

    def test_permitted_method_accepted(self):
        # Should return None without raising
        result = r._check_method_policy("eth_blockNumber")
        self.assertIsNone(result)

    def test_permitted_method_with_allow_write(self):
        result = r._check_method_policy("eth_blockNumber", allow_write=True)
        self.assertIsNone(result)

    def test_each_deny_method_rejected(self):
        for method in r._DENY_METHODS:
            with self.assertRaises(ValueError) as ctx:
                r._check_method_policy(method)
            self.assertIn(method, str(ctx.exception))

    def test_each_deny_prefix_rejected(self):
        samples = [
            "personal_unlockAccount",
            "admin_peers",
            "miner_setGasPrice",
            "engine_forkchoiceUpdatedV1",
            "clique_getSnapshot",
        ]
        for method in samples:
            with self.assertRaises(ValueError):
                r._check_method_policy(method)

    def test_allow_write_bypasses_deny_methods(self):
        for method in r._DENY_METHODS:
            # Must not raise
            r._check_method_policy(method, allow_write=True)

    def test_allow_write_bypasses_deny_prefixes(self):
        r._check_method_policy("personal_unlockAccount", allow_write=True)
        r._check_method_policy("admin_peers", allow_write=True)

    def test_allowlist_none_means_not_enforced(self):
        # Phase 2 Task 2.3 contract: allowlist=None -> no allowlist check
        result = r._check_method_policy("eth_blockNumber", allowlist=None)
        self.assertIsNone(result)

    def test_allowlist_non_none_refuses_unlisted_method(self):
        allowlist = frozenset({"eth_blockNumber", "eth_chainId"})
        with self.assertRaises(ValueError) as ctx:
            r._check_method_policy("eth_getLogs", allowlist=allowlist)
        self.assertIn("eth_getLogs", str(ctx.exception))

    def test_allowlist_non_none_permits_listed_method(self):
        allowlist = frozenset({"eth_blockNumber", "eth_chainId"})
        result = r._check_method_policy("eth_blockNumber", allowlist=allowlist)
        self.assertIsNone(result)

    def test_allowlist_denylist_still_applies_unless_allow_write(self):
        # Even if method is in the allowlist, denylist still blocks it
        # unless allow_write=True
        allowlist = frozenset({"eth_sendRawTransaction"})
        with self.assertRaises(ValueError):
            r._check_method_policy("eth_sendRawTransaction", allowlist=allowlist)

    def test_allowlist_denylist_bypassed_with_allow_write(self):
        allowlist = frozenset({"eth_sendRawTransaction"})
        # allow_write=True bypasses both denylist and allowlist enforcement
        result = r._check_method_policy(
            "eth_sendRawTransaction", allow_write=True, allowlist=allowlist
        )
        self.assertIsNone(result)

    def test_empty_string_method_rejected(self):
        # _check_method_policy must reject "" as a non-empty-str guard.
        with self.assertRaises(ValueError) as ctx:
            r._check_method_policy("")
        self.assertIn("non-empty", str(ctx.exception))

    def test_none_method_rejected_by_policy(self):
        with self.assertRaises(ValueError):
            r._check_method_policy(None)

    def test_int_method_rejected_by_policy(self):
        with self.assertRaises(ValueError):
            r._check_method_policy(42)


def make_fake_rpc_call(result=None, raises=None):
    """Return a fake rpc(url, method, params, timeout, max_body_bytes=None) for do_call injection."""
    calls = []

    def fake(url, method, params, timeout, max_body_bytes=None):
        calls.append((url, method, params, timeout))
        if raises is not None:
            raise raises
        return result

    fake.calls = calls
    return fake


class TestDoCall(unittest.TestCase):
    URL = "https://ethereum-hoodi-rpc.publicnode.com"

    def test_happy_path_calls_rpc_and_returns_result(self):
        fake = make_fake_rpc_call(result="0x123")
        out = r.do_call(self.URL, method="eth_blockNumber", params=[], rpc=fake)
        self.assertEqual(out, "0x123")
        self.assertEqual(fake.calls, [(self.URL, "eth_blockNumber", [], 15)])

    def test_explicit_timeout_forwarded(self):
        fake = make_fake_rpc_call(result="0x0")
        r.do_call(self.URL, method="eth_chainId", params=[], timeout=42, rpc=fake)
        self.assertEqual(fake.calls[0][3], 42)

    def test_deny_method_raises_before_rpc(self):
        for method in r._DENY_METHODS:
            fake = make_fake_rpc_call(result="0x0")
            with self.assertRaises(ValueError) as ctx:
                r.do_call(self.URL, method=method, params=[], rpc=fake)
            self.assertEqual(fake.calls, [], "rpc should not be called for %s" % method)
            msg = str(ctx.exception)
            self.assertIn(method, msg)

    def test_prefix_denylist_raises_before_rpc(self):
        prefix_methods = [
            "personal_unlockAccount",
            "admin_peers",
            "miner_setGasPrice",
            "engine_forkchoiceUpdatedV1",
            "clique_getSnapshot",
        ]
        for method in prefix_methods:
            fake = make_fake_rpc_call(result="0x0")
            with self.assertRaises(ValueError):
                r.do_call(self.URL, method=method, params=[], rpc=fake)
            self.assertEqual(fake.calls, [], "rpc should not be called for %s" % method)

    def test_allow_write_bypasses_denylist(self):
        fake = make_fake_rpc_call(result="0xhash")
        out = r.do_call(
            self.URL,
            method="eth_sendRawTransaction",
            params=["0x02ab"],
            allow_write=True,
            rpc=fake,
        )
        self.assertEqual(out, "0xhash")
        self.assertEqual(len(fake.calls), 1)

    def test_rpc_error_propagates(self):
        fake = make_fake_rpc_call(raises=r.RPCError("boom"))
        with self.assertRaises(r.RPCError) as ctx:
            r.do_call(self.URL, method="eth_blockNumber", params=[], rpc=fake)
        self.assertIn("boom", str(ctx.exception))

    def test_empty_method_raises(self):
        with self.assertRaises(ValueError):
            r.do_call(self.URL, method="", params=[], rpc=make_fake_rpc_call())

    def test_none_method_raises(self):
        with self.assertRaises(ValueError):
            r.do_call(self.URL, method=None, params=[], rpc=make_fake_rpc_call())

    def test_params_not_list_raises(self):
        with self.assertRaises(ValueError):
            r.do_call(self.URL, method="eth_blockNumber", params="not a list",
                      rpc=make_fake_rpc_call())

    def test_params_none_raises(self):
        with self.assertRaises(ValueError):
            r.do_call(self.URL, method="eth_blockNumber", params=None,
                      rpc=make_fake_rpc_call())


def make_fake_rpc_batch(raw_results=None, raises=None):
    """Return a fake rpc_batch(url, payload, timeout, max_body_bytes=None) for do_batch injection."""
    calls = []

    def fake(url, payload, timeout, max_body_bytes=None):
        calls.append((url, payload, timeout))
        if raises is not None:
            raise raises
        return raw_results

    fake.calls = calls
    return fake


class TestRpcBatch(unittest.TestCase):
    """Unit tests for the rpc_batch transport helper."""

    def _fake_response(self, payload):
        body = json.dumps(payload).encode("utf-8")
        resp = mock.MagicMock()
        resp.read.return_value = body
        resp.__enter__.return_value = resp
        resp.__exit__.return_value = False
        return resp

    def test_returns_parsed_array(self):
        wire = [{"jsonrpc": "2.0", "id": 0, "result": "0x1"}]
        with mock.patch(
            "eth_rpc.urllib.request.urlopen",
            return_value=self._fake_response(wire),
        ):
            result = r.rpc_batch("https://x", [{"jsonrpc": "2.0", "id": 0,
                                                 "method": "eth_chainId", "params": []}])
        self.assertEqual(result, wire)

    def test_transport_error_raises_rpcerror(self):
        with mock.patch(
            "eth_rpc.urllib.request.urlopen", side_effect=OSError("down")
        ):
            with self.assertRaises(r.RPCError):
                r.rpc_batch("https://x", [])

    def test_non_list_response_raises_rpcerror(self):
        with mock.patch(
            "eth_rpc.urllib.request.urlopen",
            return_value=self._fake_response({"error": {"code": -32600, "message": "batch too large"}}),
        ):
            with self.assertRaises(r.RPCError) as ctx:
                r.rpc_batch("https://x", [])
            self.assertIn("batch", str(ctx.exception).lower())


class TestDoBatch(unittest.TestCase):
    URL = "https://ethereum-hoodi-rpc.publicnode.com"

    def _two_calls(self):
        return [
            {"method": "eth_chainId", "params": []},
            {"method": "eth_blockNumber", "params": []},
        ]

    def test_happy_path_two_calls(self):
        wire = [
            {"jsonrpc": "2.0", "id": 0, "result": "0x88bb0"},
            {"jsonrpc": "2.0", "id": 1, "result": "0x2df761"},
        ]
        fake = make_fake_rpc_batch(raw_results=wire)
        result = r.do_batch(self.URL, calls=self._two_calls(), rpc=fake)
        self.assertEqual(len(result), 2)
        self.assertEqual(result[0], {"id": 0, "result": "0x88bb0"})
        self.assertEqual(result[1], {"id": 1, "result": "0x2df761"})

    def test_out_of_order_server_response_re_sorted(self):
        # Server returns entries in reverse id order
        wire = [
            {"jsonrpc": "2.0", "id": 1, "result": "0x2df761"},
            {"jsonrpc": "2.0", "id": 0, "result": "0x88bb0"},
        ]
        fake = make_fake_rpc_batch(raw_results=wire)
        result = r.do_batch(self.URL, calls=self._two_calls(), rpc=fake)
        self.assertEqual(result[0]["id"], 0)
        self.assertEqual(result[1]["id"], 1)
        self.assertEqual(result[0]["result"], "0x88bb0")

    def test_mixed_result_server_side_error_envelope(self):
        wire = [
            {"jsonrpc": "2.0", "id": 0, "result": "0x88bb0"},
            {"jsonrpc": "2.0", "id": 1, "error": {"code": -32602, "message": "invalid params"}},
        ]
        fake = make_fake_rpc_batch(raw_results=wire)
        result = r.do_batch(self.URL, calls=self._two_calls(), rpc=fake)
        self.assertEqual(result[0], {"id": 0, "result": "0x88bb0"})
        self.assertIn("error", result[1])
        self.assertEqual(result[1]["id"], 1)

    def test_per_entry_denylist_refusal_lands_as_error_envelope(self):
        calls = [
            {"method": "eth_sendRawTransaction", "params": ["0x02ab"]},  # denied
            {"method": "eth_blockNumber", "params": []},
        ]
        # rpc should only be called for the non-denied entry
        wire = [{"jsonrpc": "2.0", "id": 1, "result": "0x1"}]
        fake = make_fake_rpc_batch(raw_results=wire)
        result = r.do_batch(self.URL, calls=calls, rpc=fake)
        # id 0 is a synthetic refusal
        self.assertIn("error", result[0])
        self.assertEqual(result[0]["id"], 0)
        self.assertIn("eth_sendRawTransaction", result[0]["error"]["message"])
        # id 1 is the allowed entry
        self.assertEqual(result[1], {"id": 1, "result": "0x1"})
        # rpc was called (for the non-denied entry)
        self.assertEqual(len(fake.calls), 1)

    def test_allow_write_bypasses_denylist_for_all_entries(self):
        calls = [
            {"method": "eth_sendRawTransaction", "params": ["0x02ab"]},
            {"method": "eth_blockNumber", "params": []},
        ]
        wire = [
            {"jsonrpc": "2.0", "id": 0, "result": "0xhash"},
            {"jsonrpc": "2.0", "id": 1, "result": "0x1"},
        ]
        fake = make_fake_rpc_batch(raw_results=wire)
        result = r.do_batch(self.URL, calls=calls, allow_write=True, rpc=fake)
        self.assertEqual(result[0]["result"], "0xhash")
        self.assertEqual(result[1]["result"], "0x1")
        # rpc called with all 2 entries
        payload = fake.calls[0][1]
        self.assertEqual(len(payload), 2)

    def test_transport_error_raises_rpcerror(self):
        fake = make_fake_rpc_batch(raises=r.RPCError("node down"))
        with self.assertRaises(r.RPCError):
            r.do_batch(self.URL, calls=self._two_calls(), rpc=fake)

    def test_empty_calls_rejected(self):
        with self.assertRaises(ValueError) as ctx:
            r.do_batch(self.URL, calls=[], rpc=make_fake_rpc_batch())
        self.assertIn("non-empty", str(ctx.exception))

    def test_malformed_entry_raises(self):
        # Entry without 'method' key
        with self.assertRaises(ValueError):
            r.do_batch(self.URL, calls=[{"params": []}], rpc=make_fake_rpc_batch())

    def test_entry_params_not_list_raises(self):
        with self.assertRaises(ValueError):
            r.do_batch(self.URL,
                       calls=[{"method": "eth_blockNumber", "params": "[]"}],
                       rpc=make_fake_rpc_batch())

    def test_server_entry_missing_id_yields_synthetic_error_not_crash(self):
        # Bug fix: server returns an entry with no "id" (JSON-RPC permits this on
        # parse error). Building {entry["id"]: entry} crashes with KeyError.
        # The kept slot must become a synthetic -32603 envelope, not a traceback.
        wire = [{"jsonrpc": "2.0", "error": {"code": -32700, "message": "Parse error"}}]
        fake = make_fake_rpc_batch(raw_results=wire)
        result = r.do_batch(self.URL, calls=[{"method": "eth_chainId", "params": []}], rpc=fake)
        self.assertEqual(len(result), 1)
        self.assertIn("error", result[0])
        self.assertEqual(result[0]["id"], 0)
        self.assertEqual(result[0]["error"]["code"], -32603)
        self.assertIn("missing result", result[0]["error"]["message"])

    def test_server_string_id_normalised_to_int(self):
        # Bug fix: some gateways echo ids back as strings ("0" instead of 0).
        # by_id.get(0) misses and every entry degrades to a synthetic -32603 even
        # though the server answered correctly. String ids must be normalised to int.
        wire = [
            {"jsonrpc": "2.0", "id": "0", "result": "0x88bb0"},
            {"jsonrpc": "2.0", "id": "1", "result": "0x1"},
        ]
        fake = make_fake_rpc_batch(raw_results=wire)
        calls = [
            {"method": "eth_chainId", "params": []},
            {"method": "eth_blockNumber", "params": []},
        ]
        result = r.do_batch(self.URL, calls=calls, rpc=fake)
        self.assertEqual(result[0], {"id": 0, "result": "0x88bb0"})
        self.assertEqual(result[1], {"id": 1, "result": "0x1"})

    def test_all_entries_denied_never_calls_rpc(self):
        # Invariant: when every batch entry is denied, rpc must never be called.
        calls = [
            {"method": "eth_sendRawTransaction", "params": ["0x02ab"]},
            {"method": "personal_unlockAccount", "params": []},
        ]

        def rpc_must_not_be_called(url, payload, timeout, max_body_bytes=None):
            raise AssertionError("rpc should not be called when all entries are denied")

        result = r.do_batch(self.URL, calls=calls, rpc=rpc_must_not_be_called)
        self.assertEqual(len(result), 2)
        for entry in result:
            self.assertIn("error", entry)


class TestBatchCli(unittest.TestCase):
    """Tests for the `batch` subcommand driven through main(argv=[...])."""

    URL = "https://ethereum-hoodi-rpc.publicnode.com"

    def _run(self, argv):
        import contextlib
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = r.main(argv)
        return rc, out.getvalue(), err.getvalue()

    def test_happy_path_batch(self):
        wire = [
            {"jsonrpc": "2.0", "id": 0, "result": "0x88bb0"},
            {"jsonrpc": "2.0", "id": 1, "result": "0x1"},
        ]
        with mock.patch("eth_rpc.rpc_batch", return_value=wire):
            rc, out, err = self._run([
                "batch", "--network", "hoodi",
                "--calls", '[{"method":"eth_chainId","params":[]},{"method":"eth_blockNumber","params":[]}]',
            ])
        self.assertEqual(rc, 0, err)
        result = json.loads(out)
        self.assertEqual(len(result), 2)

    def test_mutual_exclusion_error(self):
        rc, out, err = self._run([
            "batch", "--network", "hoodi",
            "--rpc-url", "http://127.0.0.1:8545",
            "--chain-id", "31337",
            "--calls", '[{"method":"eth_blockNumber","params":[]}]',
        ])
        self.assertEqual(rc, 1)
        self.assertIn("error:", err)

    def test_allow_write_warning_printed_once(self):
        wire = [{"jsonrpc": "2.0", "id": 0, "result": "0x1"}]
        with mock.patch("eth_rpc.rpc_batch", return_value=wire):
            rc, out, err = self._run([
                "batch", "--network", "hoodi",
                "--calls", '[{"method":"eth_blockNumber","params":[]}]',
                "--allow-write",
            ])
        self.assertEqual(rc, 0, err)
        warning_count = err.count("warning: --allow-write bypasses the call denylist")
        self.assertEqual(warning_count, 1)

    def test_empty_calls_exit_one(self):
        rc, out, err = self._run([
            "batch", "--network", "hoodi",
            "--calls", "[]",
        ])
        self.assertEqual(rc, 1)
        self.assertIn("error:", err)

    def test_batch_in_help(self):
        proc = __import__("subprocess").run(
            [sys.executable,
             str(__import__("pathlib").Path(__file__).parent / "eth_rpc.py"),
             "--help"],
            capture_output=True, text=True,
        )
        self.assertIn("batch", proc.stdout)


class TestParseParams(unittest.TestCase):
    def test_inline_empty_array(self):
        self.assertEqual(r._parse_params("[]"), [])

    def test_inline_non_empty_array(self):
        self.assertEqual(r._parse_params('["0xabc", "latest"]'), ["0xabc", "latest"])

    def test_inline_object_raises(self):
        with self.assertRaises(ValueError) as ctx:
            r._parse_params('{"a": 1}')
        self.assertIn("JSON array", str(ctx.exception))

    def test_inline_string_raises(self):
        with self.assertRaises(ValueError) as ctx:
            r._parse_params('"foo"')
        self.assertIn("JSON array", str(ctx.exception))

    def test_inline_number_raises(self):
        with self.assertRaises(ValueError) as ctx:
            r._parse_params("42")
        self.assertIn("JSON array", str(ctx.exception))

    def test_malformed_json_raises(self):
        with self.assertRaises(ValueError) as ctx:
            r._parse_params("[bad")
        self.assertIn("JSON array", str(ctx.exception))

    def test_stdin_dash_reads_from_injected(self):
        self.assertEqual(
            r._parse_params("-", stdin=io.StringIO('["x"]')),
            ["x"],
        )

    def test_stdin_malformed_raises(self):
        with self.assertRaises(ValueError):
            r._parse_params("-", stdin=io.StringIO("not json"))

    # ----- Issue 2.7: @file extension -----

    def _make_opener(self, contents):
        """Return a fake opener(path, mode) context manager yielding StringIO."""
        class _FakeFile:
            def __enter__(self_):
                return io.StringIO(contents)
            def __exit__(self_, *args):
                pass
        def opener(path, mode="r"):
            return _FakeFile()
        return opener

    def test_at_file_happy_path(self):
        opener = self._make_opener('[{"fromBlock":"0x0","toBlock":"0x10"}]')
        result = r._parse_params("@/path/to/params.json", opener=opener)
        self.assertEqual(result, [{"fromBlock": "0x0", "toBlock": "0x10"}])

    def _make_missing_opener(self, path):
        """Return a fake opener that raises FileNotFoundError for the given path."""
        def opener(p, mode="r"):
            raise FileNotFoundError(2, "No such file or directory", p)
        return opener

    def test_at_file_no_such_file_raises(self):
        # Inject a fake opener that raises FileNotFoundError (no real filesystem access).
        opener = self._make_missing_opener("/no/such/file.json")
        with self.assertRaises(ValueError) as ctx:
            r._parse_params("@/no/such/file.json", opener=opener)
        self.assertIn("/no/such/file.json", str(ctx.exception))

    def test_at_dash_raises_with_path_in_message(self):
        # @- falls into file branch; inject a fake opener that raises FileNotFoundError.
        opener = self._make_missing_opener("-")
        with self.assertRaises(ValueError) as ctx:
            r._parse_params("@-", opener=opener)
        self.assertIn("@-", str(ctx.exception))

    def test_stdin_regression(self):
        # '-' without '@' still reads stdin
        result = r._parse_params("-", stdin=io.StringIO('["regression"]'))
        self.assertEqual(result, ["regression"])

    def test_inline_regression(self):
        # Inline still works
        result = r._parse_params('["a", "b"]')
        self.assertEqual(result, ["a", "b"])


class TestCallCli(unittest.TestCase):
    """Tests for the `call` subcommand driven through main(argv=[...])."""

    URL = "https://ethereum-hoodi-rpc.publicnode.com"

    def _run(self, argv):
        """Run main(argv) capturing stdout/stderr; return (rc, stdout, stderr)."""
        import contextlib
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = r.main(argv)
        return rc, out.getvalue(), err.getvalue()

    def test_happy_path_named_network(self):
        # Patch do_call (not rpc_call): do_call binds rpc_call as a default arg
        # at definition time, so patching the module attribute would not
        # intercept it and the test would hit the live network.
        with mock.patch("eth_rpc.do_call", return_value="0x88bb0") as mock_dc:
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_chainId", "--params", "[]",
            ])
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out), "0x88bb0")
        self.assertEqual(err, "")
        self.assertTrue(mock_dc.called)  # proves no live network call

    def test_happy_path_custom_endpoint(self):
        with mock.patch("eth_rpc.do_call", return_value="0x1") as mock_dc:
            rc, out, err = self._run([
                "call",
                "--rpc-url", "http://127.0.0.1:8545",
                "--chain-id", "31337",
                "--method", "eth_blockNumber", "--params", "[]",
            ])
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out), "0x1")
        kwargs = mock_dc.call_args[1]
        self.assertEqual(kwargs["method"], "eth_blockNumber")

    def test_mutual_exclusion_error(self):
        rc, out, err = self._run([
            "call",
            "--network", "hoodi",
            "--rpc-url", "http://127.0.0.1:8545",
            "--chain-id", "31337",
            "--method", "eth_blockNumber", "--params", "[]",
        ])
        self.assertEqual(rc, 1)
        self.assertIn("error:", err)
        self.assertTrue(
            "not both" in err or "both" in err,
            "expected 'both' in error message, got: %r" % err,
        )

    def test_missing_pair_error(self):
        rc, out, err = self._run([
            "call",
            "--rpc-url", "http://127.0.0.1:8545",
            "--method", "eth_blockNumber", "--params", "[]",
        ])
        self.assertEqual(rc, 1)
        self.assertIn("required together", err)

    def test_allow_write_warning_on_stderr(self):
        # Patch do_call (not rpc_call) — see test_happy_path_named_network.
        with mock.patch("eth_rpc.do_call", return_value="0xhash") as mock_dc:
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_blockNumber", "--params", "[]",
                "--allow-write",
            ])
        self.assertEqual(rc, 0)
        self.assertIn("warning: --allow-write bypasses the call denylist", err)
        self.assertEqual(json.loads(out), "0xhash")
        self.assertTrue(mock_dc.called)  # proves no live network call

    def test_allow_write_warning_prints_even_when_do_call_raises(self):
        with mock.patch(
            "eth_rpc.do_call", side_effect=r.RPCError("node down")
        ):
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_blockNumber", "--params", "[]",
                "--allow-write",
            ])
        self.assertEqual(rc, 1)
        self.assertIn("warning: --allow-write bypasses the call denylist", err)
        self.assertIn("error:", err)

    def test_denied_method_without_allow_write(self):
        rc, out, err = self._run([
            "call", "--network", "hoodi",
            "--method", "eth_sendRawTransaction",
            "--params", '["0x02ab"]',
        ])
        self.assertEqual(rc, 1)
        self.assertIn("eth_sendRawTransaction", err)
        self.assertTrue(
            "broadcast" in err or "--allow-write" in err,
            "expected guidance in error message, got: %r" % err,
        )

    def test_malformed_params(self):
        rc, out, err = self._run([
            "call", "--network", "hoodi",
            "--method", "eth_blockNumber",
            "--params", "not json",
        ])
        self.assertEqual(rc, 1)
        self.assertIn("--params must be a JSON array", err)

    def test_stdin_params(self):
        with mock.patch("eth_rpc.do_call", return_value="0x5") as mock_dc, \
             mock.patch("eth_rpc.sys.stdin", io.StringIO('["latest"]')):
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_getBlockByNumber",
                "--params", "-",
            ])
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out), "0x5")
        # confirm the parsed list was passed to do_call
        kwargs = mock_dc.call_args[1]
        self.assertEqual(kwargs["params"], ["latest"])

    def test_allow_write_and_read_only_strict_mutually_exclusive(self):
        # argparse must reject the combo before main sees it (ADR-010)
        with self.assertRaises(SystemExit) as ctx:
            r.main([
                "call", "--network", "hoodi",
                "--method", "eth_chainId", "--params", "[]",
                "--allow-write", "--read-only-strict",
            ])
        self.assertNotEqual(ctx.exception.code, 0)

    def test_read_only_strict_allows_listed_method(self):
        with mock.patch("eth_rpc.do_call", return_value="0x88bb0"):
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_chainId", "--params", "[]",
                "--read-only-strict",
            ])
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out), "0x88bb0")

    def test_read_only_strict_rejects_unlisted_method(self):
        rc, out, err = self._run([
            "call", "--network", "hoodi",
            "--method", "net_version", "--params", "[]",
            "--read-only-strict",
        ])
        self.assertEqual(rc, 1)
        self.assertIn("net_version", err)


class TestMaxBodyBytesValidator(unittest.TestCase):
    """--max-body-bytes must reject non-positive values at argparse time (fix 6)."""

    def _run(self, argv):
        import contextlib
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                rc = r.main(argv)
            except SystemExit as exc:
                rc = exc.code
        return rc, out.getvalue(), err.getvalue()

    def test_call_max_body_bytes_zero_exits_nonzero(self):
        with self.assertRaises(SystemExit) as ctx:
            r.main(["call", "--network", "hoodi", "--method", "eth_blockNumber",
                    "--params", "[]", "--max-body-bytes", "0"])
        self.assertNotEqual(ctx.exception.code, 0)

    def test_call_max_body_bytes_negative_exits_nonzero(self):
        with self.assertRaises(SystemExit) as ctx:
            r.main(["call", "--network", "hoodi", "--method", "eth_blockNumber",
                    "--params", "[]", "--max-body-bytes", "-1"])
        self.assertNotEqual(ctx.exception.code, 0)

    def test_batch_max_body_bytes_zero_exits_nonzero(self):
        with self.assertRaises(SystemExit) as ctx:
            r.main(["batch", "--network", "hoodi",
                    "--calls", '[{"method":"eth_blockNumber","params":[]}]',
                    "--max-body-bytes", "0"])
        self.assertNotEqual(ctx.exception.code, 0)

    def test_batch_max_body_bytes_negative_exits_nonzero(self):
        with self.assertRaises(SystemExit) as ctx:
            r.main(["batch", "--network", "hoodi",
                    "--calls", '[{"method":"eth_blockNumber","params":[]}]',
                    "--max-body-bytes", "-5"])
        self.assertNotEqual(ctx.exception.code, 0)

    def test_positive_value_accepted(self):
        # A positive value should not raise at parse time
        with unittest.mock.patch("eth_rpc.do_call", return_value="0x1"):
            rc, _, _ = self._run(["call", "--network", "hoodi",
                                   "--method", "eth_blockNumber", "--params", "[]",
                                   "--max-body-bytes", "1024"])
        self.assertEqual(rc, 0)


class TestCliSmoke(unittest.TestCase):
    def test_help_runs(self):
        # Executes the module directly (not import) — catches definition-order bugs.
        # Asserts all subcommands including Phase 3 additions are listed.
        proc = subprocess.run(
            [sys.executable, str(SKILL_DIR / "eth_rpc.py"), "--help"],
            capture_output=True, text=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("balance", proc.stdout)
        self.assertIn("broadcast", proc.stdout)
        self.assertIn("call", proc.stdout)
        self.assertIn("batch", proc.stdout)
        self.assertIn("net-version", proc.stdout)
        self.assertIn("client-version", proc.stdout)

    def test_balance_bad_address_exits_one(self):
        # Drives the balance path through main() in a real process. Bad address
        # fails validation before any network call, so this stays offline.
        proc = subprocess.run(
            [sys.executable, str(SKILL_DIR / "eth_rpc.py"), "balance",
             "--network", "hoodi", "--address", "0xnope"],
            capture_output=True, text=True,
        )
        self.assertEqual(proc.returncode, 1)
        self.assertIn("error:", proc.stderr)


class TestDecodeResult(unittest.TestCase):
    """Tests for _decode_result — pure function, no mocked rpc needed.

    Fixtures are hand-rolled; comments note the source field shapes.
    """

    # ----- Issue 2.2: hex-quantity methods -----

    def test_block_number_decoded(self):
        result = r._decode_result("eth_blockNumber", "0x10")
        self.assertEqual(result, {"hex": "0x10", "decimal": 16})

    def test_chain_id_decoded(self):
        result = r._decode_result("eth_chainId", "0x88bb0")
        self.assertEqual(result["decimal"], 560048)
        self.assertEqual(result["hex"], "0x88bb0")

    def test_get_transaction_count_decoded(self):
        result = r._decode_result("eth_getTransactionCount", "0x5")
        self.assertEqual(result, {"hex": "0x5", "decimal": 5})

    def test_estimate_gas_decoded(self):
        result = r._decode_result("eth_estimateGas", "0x5208")
        self.assertEqual(result["decimal"], 21000)
        self.assertEqual(result["hex"], "0x5208")

    def test_get_balance_decoded(self):
        # 1 ETH = 10**18 wei = "0xde0b6b3a7640000"
        result = r._decode_result("eth_getBalance", "0xde0b6b3a7640000")
        self.assertEqual(result["hex"], "0xde0b6b3a7640000")
        self.assertEqual(result["wei"], 10 ** 18)
        self.assertEqual(result["eth"], "1")

    def test_get_balance_tenth_eth(self):
        # 0.1 ETH = 10**17 wei
        result = r._decode_result("eth_getBalance", "0x16345785d8a0000")
        self.assertEqual(result["wei"], 10 ** 17)
        self.assertEqual(result["eth"], "0.1")

    def test_gas_price_decoded_no_float(self):
        # 30_000_000_000 wei = 30 gwei (exact integer divmod)
        result = r._decode_result("eth_gasPrice", hex(30_000_000_000))
        self.assertEqual(result["wei"], 30_000_000_000)
        self.assertEqual(result["gwei"], "30")
        # Must not contain float keys
        self.assertNotIn("decimal", result)

    def test_max_priority_fee_decoded(self):
        # 1_500_000_000 wei = 1 gwei + 500000000 rem
        result = r._decode_result("eth_maxPriorityFeePerGas", hex(1_500_000_000))
        self.assertEqual(result["wei"], 1_500_000_000)
        self.assertIn("gwei", result)

    def test_unknown_method_passthrough(self):
        payload = {"foo": "bar"}
        result = r._decode_result("eth_someUnknown", payload)
        self.assertIs(result, payload)

    def test_null_result_returned_unchanged(self):
        result = r._decode_result("eth_blockNumber", None)
        self.assertIsNone(result)

    def test_non_hex_string_passthrough(self):
        # result is not a hex string — must return unchanged, never raise
        result = r._decode_result("eth_blockNumber", "not-hex")
        self.assertEqual(result, "not-hex")

    def test_bare_0x_passthrough(self):
        # "0x" with no hex digits: int("0x", 16) raises ValueError,
        # which the decoder catches -> passthrough (returns string unchanged).
        result = r._decode_result("eth_blockNumber", "0x")
        self.assertEqual(result, "0x")
        self.assertIsInstance(result, str)

    def test_hex_quantity_methods_frozenset(self):
        # Drift guard: the frozenset must contain exactly these seven methods
        self.assertEqual(
            r._HEX_QUANTITY_METHODS,
            frozenset({
                "eth_blockNumber",
                "eth_gasPrice",
                "eth_chainId",
                "eth_getTransactionCount",
                "eth_estimateGas",
                "eth_maxPriorityFeePerGas",
                "eth_getBalance",
            }),
        )

    # ----- Issue 2.3: block / tx / receipt objects -----
    # Fixtures are trimmed hand-rolled dicts; field names match execution-apis spec.

    # Trimmed eth_getBlockByNumber response (mainnet-shaped, post-merge).
    FAKE_BLOCK = {
        "number": "0x10",
        "gasUsed": "0x5208",
        "gasLimit": "0x1c9c380",
        "baseFeePerGas": "0x9502f900",
        "timestamp": "0x64b2c350",
        "size": "0x2a0",
        "difficulty": "0x0",
        "nonce": "0x0000000000000000",
        "totalDifficulty": "0x1",
        "hash": "0xabc",
    }

    # Trimmed eth_getTransactionByHash response (EIP-1559 tx).
    FAKE_TX = {
        "blockNumber": "0x10",
        "transactionIndex": "0x0",
        "nonce": "0x5",
        "value": "0xde0b6b3a7640000",   # 1 ETH
        "gas": "0x5208",
        "maxFeePerGas": "0x12a05f200",
        "maxPriorityFeePerGas": "0x77359400",
        "chainId": "0x1",
        "type": "0x2",
        "hash": "0xdeadbeef",
    }

    # Trimmed eth_getTransactionByHash missing 'from' (optional per spec).
    FAKE_TX_NO_FROM = {
        "blockNumber": "0x5",
        "nonce": "0x1",
        "value": "0x0",
        "gas": "0x5208",
        "type": "0x2",
        "hash": "0xcafe",
    }

    # Trimmed eth_getTransactionReceipt response.
    FAKE_RECEIPT = {
        "blockNumber": "0x10",
        "transactionIndex": "0x0",
        "gasUsed": "0x5208",
        "cumulativeGasUsed": "0x5208",
        "effectiveGasPrice": "0x9502f900",
        "status": "0x1",
        "type": "0x2",
        "logs": [],
    }

    def test_block_by_number_happy_path(self):
        result = r._decode_result("eth_getBlockByNumber", self.FAKE_BLOCK)
        self.assertEqual(result["raw"], self.FAKE_BLOCK)
        self.assertEqual(result["number"], 16)
        self.assertEqual(result["gasUsed"], 21000)
        self.assertEqual(result["gasLimit"], 30000000)
        self.assertIn("baseFeePerGas", result)
        self.assertIn("timestamp", result)
        self.assertIn("size", result)

    def test_block_total_difficulty_not_decoded(self):
        # totalDifficulty is legacy under PoS; must NOT appear as a decoded top-level key
        result = r._decode_result("eth_getBlockByNumber", self.FAKE_BLOCK)
        self.assertNotIn("totalDifficulty", result)
        # But it must still be accessible via raw
        self.assertIn("totalDifficulty", result["raw"])

    def test_block_by_hash_dispatches(self):
        result = r._decode_result("eth_getBlockByHash", self.FAKE_BLOCK)
        self.assertIn("raw", result)
        self.assertEqual(result["number"], 16)

    def test_tx_by_hash_happy_path(self):
        result = r._decode_result("eth_getTransactionByHash", self.FAKE_TX)
        self.assertEqual(result["raw"], self.FAKE_TX)
        self.assertEqual(result["blockNumber"], 16)
        self.assertEqual(result["nonce"], 5)
        # value -> {wei, eth}
        self.assertEqual(result["value"]["wei"], 10 ** 18)
        self.assertEqual(result["value"]["eth"], "1")
        # gas-price fields -> {wei, gwei}
        self.assertIn("gwei", result["maxFeePerGas"])
        self.assertIn("gwei", result["maxPriorityFeePerGas"])

    def test_tx_missing_from_omitted(self):
        # 'from' is optional per spec; must be omitted when missing, no exception
        result = r._decode_result("eth_getTransactionByHash", self.FAKE_TX_NO_FROM)
        self.assertNotIn("from", result)
        # Other fields still decoded
        self.assertEqual(result["nonce"], 1)

    def test_tx_by_block_hash_and_index_dispatches(self):
        result = r._decode_result("eth_getTransactionByBlockHashAndIndex", self.FAKE_TX)
        self.assertIn("raw", result)
        self.assertIn("blockNumber", result)

    def test_receipt_happy_path(self):
        result = r._decode_result("eth_getTransactionReceipt", self.FAKE_RECEIPT)
        self.assertEqual(result["raw"], self.FAKE_RECEIPT)
        self.assertEqual(result["status"], 1)
        self.assertEqual(result["gasUsed"], 21000)
        self.assertEqual(result["cumulativeGasUsed"], 21000)
        self.assertIn("effectiveGasPrice", result)
        self.assertIn("blockNumber", result)

    def test_null_block_result_returns_none(self):
        result = r._decode_result("eth_getBlockByNumber", None)
        self.assertIsNone(result)

    def test_null_tx_result_returns_none(self):
        result = r._decode_result("eth_getTransactionByHash", None)
        self.assertIsNone(result)

    def test_empty_block_returns_raw_only(self):
        result = r._decode_result("eth_getBlockByNumber", {})
        self.assertEqual(result, {"raw": {}})

    def test_block_non_hex_field_omitted(self):
        # If a field is present but not a hex string, it stays in raw only
        block = {"number": "not-hex", "gasUsed": "0x5208"}
        result = r._decode_result("eth_getBlockByNumber", block)
        self.assertNotIn("number", result)  # not decoded
        self.assertEqual(result["gasUsed"], 21000)  # other field still decoded

    # ----- Issue 2.4: eth_getLogs arrays -----

    # Trimmed Transfer log fixture (topics for Transfer(address,address,uint256)).
    FAKE_LOG = {
        "address": "0xdac17f958d2ee523a2206206994597c13d831ec7",
        "topics": [
            "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
            "0x000000000000000000000000abc",
            "0x000000000000000000000000def",
        ],
        "data": "0x00000000000000000000000000000000000000000000000000000002540be400",
        "blockNumber": "0x10",
        "logIndex": "0x0",
        "transactionIndex": "0x2",
        "transactionHash": "0xdeadbeef",
        "blockHash": "0xcafe",
    }

    def test_get_logs_happy_path(self):
        result = r._decode_result("eth_getLogs", [self.FAKE_LOG])
        self.assertIsInstance(result, list)
        self.assertEqual(len(result), 1)
        entry = result[0]
        self.assertEqual(entry["raw"], self.FAKE_LOG)
        self.assertEqual(entry["blockNumber"], 16)
        self.assertEqual(entry["logIndex"], 0)
        self.assertEqual(entry["transactionIndex"], 2)

    def test_get_logs_topics_not_decoded(self):
        result = r._decode_result("eth_getLogs", [self.FAKE_LOG])
        entry = result[0]
        # topics/data/address/hashes must stay in raw only
        self.assertNotIn("topics", entry)
        self.assertNotIn("data", entry)
        self.assertNotIn("address", entry)
        self.assertIn("topics", entry["raw"])

    def test_get_logs_empty_array(self):
        result = r._decode_result("eth_getLogs", [])
        self.assertEqual(result, [])

    def test_get_logs_partial_fields(self):
        # Only blockNumber present; logIndex and transactionIndex omitted -> no exception
        log = {"blockNumber": "0x10"}
        result = r._decode_result("eth_getLogs", [log])
        self.assertEqual(result[0]["blockNumber"], 16)
        self.assertNotIn("logIndex", result[0])
        self.assertNotIn("transactionIndex", result[0])

    def test_get_logs_two_entries(self):
        logs = [self.FAKE_LOG, {"blockNumber": "0x5", "logIndex": "0x1", "transactionIndex": "0x0"}]
        result = r._decode_result("eth_getLogs", logs)
        self.assertEqual(len(result), 2)
        self.assertEqual(result[1]["blockNumber"], 5)


class TestStrictAllowlistContents(unittest.TestCase):
    """Drift guard (ADR-011 pattern): any intentional change to _STRICT_ALLOWLIST
    requires updating both the constant and this test in the same commit."""

    def test_strict_allowlist_exact(self):
        self.assertEqual(
            r._STRICT_ALLOWLIST,
            frozenset({
                "eth_getBalance", "eth_getCode", "eth_getStorageAt",
                "eth_getTransactionCount",
                "eth_getTransactionByHash",
                "eth_getTransactionByBlockHashAndIndex",
                "eth_getTransactionByBlockNumberAndIndex",
                "eth_getTransactionReceipt",
                "eth_getBlockByNumber", "eth_getBlockByHash",
                "eth_getBlockTransactionCountByNumber",
                "eth_getBlockTransactionCountByHash",
                "eth_getLogs", "eth_call", "eth_estimateGas", "eth_gasPrice",
                "eth_feeHistory", "eth_maxPriorityFeePerGas",
                "eth_chainId", "eth_blockNumber", "eth_syncing",
                "eth_accounts", "eth_protocolVersion", "eth_getProof",
            }),
        )


class TestStrictBeforeDenylist(unittest.TestCase):
    """Fix 2: strict mode must fire before the denylist, with a clear 'read surface' message.

    Spec (issue 2.6): strict is the tighter rule; when strict=True and the method
    is not in _STRICT_ALLOWLIST, the error must reference the read-surface/strict
    constraint — NOT the broadcast/denylist message that would appear without strict.
    """

    URL = "https://ethereum-hoodi-rpc.publicnode.com"

    def test_strict_with_denied_method_gives_strict_message_not_denylist(self):
        # eth_sendRawTransaction is both in _DENY_METHODS and NOT in _STRICT_ALLOWLIST.
        # With strict=True, the allowlist check should fire first, giving a
        # read-surface message — NOT the 'use the broadcast op' denylist message.
        fake = make_fake_rpc_call(result="0x0")
        with self.assertRaises(ValueError) as ctx:
            r.do_call(
                self.URL,
                method="eth_sendRawTransaction",
                params=["0x02ab"],
                strict=True,
                allow_write=False,
                rpc=fake,
            )
        msg = str(ctx.exception)
        # Must reference strict / read-surface, not the broadcast guidance
        self.assertIn("eth_sendRawTransaction", msg)
        self.assertNotIn("broadcast", msg)
        self.assertNotIn("use the 'broadcast' op", msg)
        # rpc must never be called
        self.assertEqual(fake.calls, [])

    def test_strict_message_references_read_surface(self):
        # The error message for a strict miss should mention the read surface.
        fake = make_fake_rpc_call(result="0x0")
        with self.assertRaises(ValueError) as ctx:
            r.do_call(
                self.URL,
                method="eth_sendRawTransaction",
                params=["0x02ab"],
                strict=True,
                allow_write=False,
                rpc=fake,
            )
        msg = str(ctx.exception)
        # Must mention "strict" or "read surface" per spec
        self.assertTrue(
            "strict" in msg.lower() or "read surface" in msg.lower(),
            "expected 'strict' or 'read surface' in error: %r" % msg,
        )

    def test_allow_write_true_bypasses_both_strict_and_denylist(self):
        # allow_write=True must bypass everything: both the allowlist and denylist.
        fake = make_fake_rpc_call(result="0xhash")
        out = r.do_call(
            self.URL,
            method="eth_sendRawTransaction",
            params=["0x02ab"],
            strict=True,
            allow_write=True,
            rpc=fake,
        )
        self.assertEqual(out, "0xhash")
        self.assertEqual(len(fake.calls), 1)

    def test_non_strict_denylist_message_unchanged(self):
        # Without strict, the denylist message must be the existing broadcast guidance.
        fake = make_fake_rpc_call(result="0x0")
        with self.assertRaises(ValueError) as ctx:
            r.do_call(
                self.URL,
                method="eth_sendRawTransaction",
                params=["0x02ab"],
                strict=False,
                allow_write=False,
                rpc=fake,
            )
        msg = str(ctx.exception)
        # Normal denylist message must still reference broadcast or --allow-write
        self.assertTrue(
            "broadcast" in msg or "--allow-write" in msg,
            "expected denylist guidance in non-strict error: %r" % msg,
        )

    def test_strict_miss_not_in_allowlist_fires_before_prefix_denylist(self):
        # personal_ is in the prefix denylist AND not in _STRICT_ALLOWLIST.
        # strict should fire first.
        fake = make_fake_rpc_call(result="0x0")
        with self.assertRaises(ValueError) as ctx:
            r.do_call(
                self.URL,
                method="personal_unlockAccount",
                params=[],
                strict=True,
                allow_write=False,
                rpc=fake,
            )
        msg = str(ctx.exception)
        # Must not say "sensitive namespace"
        self.assertNotIn("sensitive namespace", msg)
        self.assertEqual(fake.calls, [])


class TestDoCallStrict(unittest.TestCase):
    URL = "https://ethereum-hoodi-rpc.publicnode.com"

    def test_strict_allowlist_hit_proceeds(self):
        fake = make_fake_rpc_call(result="0x88bb0")
        out = r.do_call(self.URL, method="eth_blockNumber", params=[], strict=True, rpc=fake)
        self.assertEqual(out, "0x88bb0")

    def test_strict_allowlist_miss_raises(self):
        fake = make_fake_rpc_call(result="0x1")
        with self.assertRaises(ValueError) as ctx:
            r.do_call(self.URL, method="net_version", params=[], strict=True, rpc=fake)
        self.assertIn("net_version", str(ctx.exception))
        # rpc should not have been called
        self.assertEqual(fake.calls, [])

    def test_strict_with_allow_write_bypasses(self):
        # allow_write=True still bypasses everything including strict
        fake = make_fake_rpc_call(result="0x1")
        out = r.do_call(self.URL, method="eth_sendRawTransaction", params=["0x02ab"],
                        allow_write=True, strict=True, rpc=fake)
        self.assertEqual(out, "0x1")

    def test_strict_default_false(self):
        # strict defaults to False — net_version must pass without strict
        fake = make_fake_rpc_call(result="1")
        out = r.do_call(self.URL, method="net_version", params=[], rpc=fake)
        self.assertEqual(out, "1")


class TestDecodeResultWarnsOnException(unittest.TestCase):
    """Fix 5: _decode_result must print a warning to stderr when the decoder raises,
    then return the raw result unchanged (stdout/raw result not affected).
    """

    def test_decode_exception_returns_raw_result(self):
        # Monkeypatch _decode_block to raise; the outer except must catch it and
        # return the raw result unchanged.
        raw = {"number": "0x10"}
        with mock.patch.object(r, "_decode_block", side_effect=RuntimeError("injected")), \
             mock.patch("sys.stderr", io.StringIO()):
            result = r._decode_result("eth_getBlockByNumber", raw)
        self.assertIs(result, raw)

    def test_decode_exception_emits_stderr_warning(self):
        # The warning line must go to stderr, not stdout.
        raw = {"number": "0x10"}
        fake_err = io.StringIO()
        with mock.patch.object(r, "_decode_block", side_effect=RuntimeError("boom")), \
             mock.patch("sys.stderr", fake_err):
            r._decode_result("eth_getBlockByNumber", raw)
        warning = fake_err.getvalue()
        self.assertIn("warning", warning.lower())
        self.assertIn("--decode", warning)
        self.assertIn("eth_getBlockByNumber", warning)
        self.assertIn("boom", warning)

    def test_decode_exception_does_not_affect_stdout(self):
        # The raw result must be returned so the caller can still json.dumps it.
        raw = "0xabc"
        out = io.StringIO()
        fake_err = io.StringIO()
        with mock.patch.object(r, "_decode_hex_quantity", side_effect=Exception("oops")), \
             mock.patch("sys.stderr", fake_err), \
             mock.patch("sys.stdout", out):
            result = r._decode_result("eth_blockNumber", raw)
        # stdout untouched
        self.assertEqual(out.getvalue(), "")
        # result is the original raw value
        self.assertEqual(result, raw)

    def test_normal_decode_emits_no_warning(self):
        # When decoding succeeds, no warning must appear on stderr.
        fake_err = io.StringIO()
        with mock.patch("sys.stderr", fake_err):
            result = r._decode_result("eth_blockNumber", "0x10")
        self.assertEqual(fake_err.getvalue(), "")
        self.assertIn("decimal", result)


class TestDecodeOversizedHex(unittest.TestCase):
    """Fix 1: _decode_hex_quantity must not parse hex values > 66 chars (uint256 cap).

    A legitimate uint256 is at most 64 hex chars (66 incl. 0x prefix).
    An adversarially large value would produce a Python bignum whose
    str() conversion raises ValueError on Python 3.11+ (int-to-str cap).
    """

    _HUGE = "0x" + "f" * 9000  # way beyond uint256

    def test_oversized_hex_returned_unchanged(self):
        # A >66-char hex string must pass through without decoding.
        result = r._decode_result("eth_blockNumber", self._HUGE)
        self.assertEqual(result, self._HUGE)

    def test_oversized_hex_is_not_dict(self):
        # Must return the raw string, never a dict with a bignum value.
        result = r._decode_result("eth_blockNumber", self._HUGE)
        self.assertIsInstance(result, str)

    def test_oversized_hex_json_serialisable(self):
        # json.dumps must NOT raise on the result — this was the actual crash.
        result = r._decode_result("eth_blockNumber", self._HUGE)
        # Should not raise ValueError
        serialised = json.dumps(result)
        self.assertIsInstance(serialised, str)

    def test_block_with_oversized_field_leaves_field_in_raw_only(self):
        # An oversized field inside a block must be left in raw only (not decoded).
        big_val = "0x" + "a" * 200
        block = {"number": "0x10", "gasUsed": big_val}
        result = r._decode_result("eth_getBlockByNumber", block)
        # number is small and should decode
        self.assertEqual(result["number"], 16)
        # gasUsed is huge and must NOT appear as a decoded top-level int
        self.assertNotIn("gasUsed", result)
        # but it must still be accessible via raw
        self.assertEqual(result["raw"]["gasUsed"], big_val)

    def test_tx_with_oversized_field_leaves_field_in_raw_only(self):
        big_val = "0x" + "b" * 200
        tx = {"blockNumber": "0x5", "nonce": big_val, "value": "0x0",
              "gas": "0x5208", "type": "0x2"}
        result = r._decode_result("eth_getTransactionByHash", tx)
        self.assertEqual(result["blockNumber"], 5)
        self.assertNotIn("nonce", result)

    def test_receipt_with_oversized_field_leaves_field_in_raw_only(self):
        big_val = "0x" + "c" * 200
        receipt = {"blockNumber": "0x10", "gasUsed": big_val, "status": "0x1"}
        result = r._decode_result("eth_getTransactionReceipt", receipt)
        self.assertEqual(result["blockNumber"], 16)
        self.assertNotIn("gasUsed", result)

    def test_log_with_oversized_field_leaves_field_in_raw_only(self):
        big_val = "0x" + "d" * 200
        log = {"blockNumber": "0x10", "logIndex": big_val, "transactionIndex": "0x0"}
        result = r._decode_result("eth_getLogs", [log])
        self.assertEqual(result[0]["blockNumber"], 16)
        self.assertNotIn("logIndex", result[0])

    def test_exactly_66_chars_is_decoded(self):
        # 0x + 64 hex chars = exactly 66 chars: should still decode normally.
        val = "0x" + "f" * 64  # uint256 max
        result = r._decode_result("eth_blockNumber", val)
        self.assertIsInstance(result, dict)
        self.assertIn("decimal", result)

    def test_67_chars_is_not_decoded(self):
        # 0x + 65 hex chars = 67 chars: must pass through.
        val = "0x" + "f" * 65
        result = r._decode_result("eth_blockNumber", val)
        self.assertIsInstance(result, str)
        self.assertEqual(result, val)


class TestCallDecodeCli(unittest.TestCase):
    """Tests for the --decode flag on the call subcommand (issue 2.5)."""

    def _run(self, argv):
        import contextlib
        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = r.main(argv)
        return rc, out.getvalue(), err.getvalue()

    def test_decode_flag_in_help(self):
        import contextlib
        out = io.StringIO()
        err = io.StringIO()
        with self.assertRaises(SystemExit) as ctx, \
             contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            r.main(["call", "--help"])
        self.assertEqual(ctx.exception.code, 0)
        self.assertIn("--decode", out.getvalue())

    def test_without_decode_raw_output_unchanged(self):
        # Regression: without --decode, output must be byte-identical to raw passthrough.
        with mock.patch("eth_rpc.do_call", return_value="0x88bb0"):
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_chainId", "--params", "[]",
            ])
        self.assertEqual(rc, 0)
        # Output must be the JSON-serialised raw string
        self.assertEqual(out.strip(), '"0x88bb0"')

    def test_with_decode_block_number(self):
        with mock.patch("eth_rpc.do_call", return_value="0x10"):
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_blockNumber", "--params", "[]",
                "--decode",
            ])
        self.assertEqual(rc, 0)
        result = json.loads(out)
        self.assertEqual(result["decimal"], 16)
        self.assertEqual(result["hex"], "0x10")

    def test_with_decode_block_by_number(self):
        fake_block = {"number": "0x10", "gasUsed": "0x5208", "gasLimit": "0x1c9c380"}
        with mock.patch("eth_rpc.do_call", return_value=fake_block):
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_getBlockByNumber", "--params", '["latest", false]',
                "--decode",
            ])
        self.assertEqual(rc, 0)
        result = json.loads(out)
        self.assertIn("raw", result)
        self.assertEqual(result["number"], 16)
        self.assertEqual(result["gasUsed"], 21000)

    def test_without_decode_complex_result_passthrough(self):
        # Regression: complex result without --decode must be byte-identical.
        fake_block = {"number": "0x10", "hash": "0xabc"}
        with mock.patch("eth_rpc.do_call", return_value=fake_block):
            rc, out, err = self._run([
                "call", "--network", "hoodi",
                "--method", "eth_getBlockByNumber", "--params", '["latest", false]',
            ])
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out), fake_block)


if __name__ == "__main__":
    unittest.main()
