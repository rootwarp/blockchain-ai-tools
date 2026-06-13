import unittest

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


if __name__ == "__main__":
    unittest.main()
