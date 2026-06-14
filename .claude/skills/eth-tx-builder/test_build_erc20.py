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


if __name__ == "__main__":
    unittest.main()
