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


if __name__ == "__main__":
    unittest.main()
