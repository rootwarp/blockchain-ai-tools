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


class TestCliSmoke(unittest.TestCase):
    def test_help_runs(self):
        # Executes the module directly (not import) — catches definition-order bugs.
        proc = subprocess.run(
            [sys.executable, str(SKILL_DIR / "eth_rpc.py"), "--help"],
            capture_output=True, text=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("balance", proc.stdout)
        self.assertIn("broadcast", proc.stdout)
        self.assertIn("call", proc.stdout)

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


if __name__ == "__main__":
    unittest.main()
