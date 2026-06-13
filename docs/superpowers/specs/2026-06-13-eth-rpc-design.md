# eth-rpc skill — design

**Date:** 2026-06-13
**Status:** approved (design phase)

## Summary

A new Claude Code skill, `eth-rpc`, that acts as the read/write RPC companion to
the strictly-offline `eth-signer-mcp` signer. It supports two operations:

1. **`balance`** — query the ETH balance of an EOA.
2. **`broadcast`** — propagate an already-signed raw transaction to the network.

Like `eth-tx-builder`, this skill makes outbound JSON-RPC calls; the signer
itself remains offline. The two concerns are separate.

## Motivation

The signer can produce a signed `rawTransaction`, but nothing in the skill set
submits it or reads chain state. Today that is done with ad-hoc `curl` calls.
`eth-rpc` packages those two read/write RPC operations as a tested, reusable
skill that mirrors the existing `eth-tx-builder` conventions.

## Non-goals (v1)

- Building/estimating transactions (that is `eth-tx-builder`).
- ERC-20 / contract-call decoding, logs, or event queries.
- Block-tag selection for balance (always `latest`).
- Mempool/gas oracles, nonce management, or multi-address batch queries.

## Architecture

Self-contained skill directory mirroring `eth-tx-builder/`:

```
.claude/skills/eth-rpc/
  SKILL.md            # frontmatter + procedure for the two operations
  eth_rpc.py          # stdlib-only helper, two subcommands, injectable rpc
  test_eth_rpc.py     # mocked-RPC unit tests, no live network
  README.md
  .gitignore          # __pycache__/, *.py[cod]
```

**Intentional duplication.** `eth_rpc.py` copies the small shared primitives from
`build_send_eth.py` — the `NETWORKS` table, `rpc_call`, `RPCError`, `USER_AGENT`,
`validate_hex_address`, `parse_hex_int` — rather than importing them. Each skill
stays independently runnable and portable. If a third consumer needs these,
extract to `libs/` then; not before (YAGNI).

**Networks** (hardcoded, identical to `eth-tx-builder`):

| network | chainId | RPC |
|---|---|---|
| `mainnet` | 1 | `https://ethereum-rpc.publicnode.com` |
| `hoodi` | 560048 | `https://ethereum-hoodi-rpc.publicnode.com` |

`User-Agent: eth-rpc/1.0` (publicnode rejects the default `Python-urllib` UA with 403).

## CLI contract

Single script, `argparse` subcommands. Shared contract with `build_send_eth.py`:
result JSON to **stdout**; on failure, `error: <msg>` to **stderr** and a non-zero
exit; never a partial result.

### `balance`

```bash
python3 eth_rpc.py balance --network <mainnet|hoodi> --address 0x<40hex>
```

- Validates network and address (`0x` + 40 hex; EIP-55 not required — read path).
- Calls `eth_getBalance(address, "latest")`.
- Converts wei → ETH exactly with `decimal.Decimal` (no float).
- Prints:

```json
{
  "network": "hoodi",
  "chainId": "560048",
  "address": "0x6302794A4F2487a2540c40E2dbB211Ff6AF1CD20",
  "blockTag": "latest",
  "balanceWei": "899976476745084000",
  "balanceEth": "0.899976476745084"
}
```

- `--address` is **required at the script level** (keeps the helper free of any MCP
  coupling). The "default to the signer" UX lives in `SKILL.md`: if the user does
  not name an address, Claude calls the `get_address` MCP tool first and passes the
  result. Naming any other EOA skips `get_address`.

### `broadcast`

```bash
python3 eth_rpc.py broadcast --network <mainnet|hoodi> --raw-tx 0x02... \
    [--wait] [--wait-timeout 120]
```

- Validates network and that `--raw-tx` is a `0x`-prefixed hex string.
- Calls `eth_sendRawTransaction(raw)`.
- **Without `--wait`:** prints `txHash` + `status: "submitted"`, exit 0.

```json
{ "network": "hoodi", "chainId": "560048",
  "txHash": "0xd613...9e85", "status": "submitted" }
```

- **With `--wait`:** polls `eth_getTransactionReceipt(txHash)` every ~4s until a
  receipt appears or `--wait-timeout` seconds elapse. Adds:
  - `status`: `"mined"` (receipt `status` `0x1`), `"failed"` (receipt `status`
    `0x0`, i.e. reverted-but-included), or `"pending"` (timed out, not yet mined).
  - `blockNumber`, `gasUsed`, `effectiveGasPrice` (decimal strings) when a receipt
    was obtained.

```json
{ "network": "hoodi", "chainId": "560048", "txHash": "0xd613...9e85",
  "status": "mined", "receiptStatus": "0x1",
  "blockNumber": "3008098", "gasUsed": "21000",
  "effectiveGasPrice": "1120154996" }
```

- **Exit codes:** submit failure (RPC rejects: bad nonce, underpriced, malformed)
  → `error: ...` on stderr, exit 1. A successful submit always exits 0 — including
  a reverted (`status: failed`) or timed-out (`status: pending`) tx, since the
  broadcast itself succeeded. The caller distinguishes outcomes via the `status`
  field.

- **`--wait` runtime note:** the poll loop can exceed Claude's default Bash
  timeout. `SKILL.md` instructs Claude to run `--wait` with a raised Bash timeout
  or in the background, and to keep `--wait-timeout` ≤ that budget.

## SKILL.md procedure (outline)

Frontmatter `name: eth-rpc`, `description:` covering "query ETH balance of an EOA"
and "broadcast/propagate a signed transaction", with `mainnet`/`hoodi` and the
"does not sign / does not build" boundary stated so it triggers correctly and does
not overlap `eth-tx-builder`.

Procedure:
1. Identify the operation (balance vs broadcast) and validate inputs.
2. **balance:** if no address given, call the `get_address` MCP tool for the
   signer's address; else use the supplied EOA. Run `eth_rpc.py balance`. Present
   the balance making units loud (wei **and** ETH).
3. **broadcast:** take the signer's `rawTransaction` hex. Run `eth_rpc.py
   broadcast` (with `--wait` when the user wants confirmation). On submit error,
   surface it and stop. On success, report `txHash`; with `--wait`, report
   mined/failed/pending + block + gas.
4. Note that `get_address` requires the signer MCP to be connected (balance default
   only); broadcast needs no MCP, just the raw tx.

## Error handling

- `RPCError` for transport failures and JSON-RPC error responses (same as
  `build_send_eth.py`).
- `ValueError` for bad network/address/hex/timeout inputs.
- `main()` catches `(ValueError, RPCError)`, prints `error: <msg>` to stderr,
  returns 1.

## Testing

`test_eth_rpc.py`, stdlib `unittest`, injected fake `rpc` — no live network:

- **balance:** formats a normal balance, zero (`0x0`), and a sub-1-ETH value;
  `balanceEth` is exact (no float drift); `latest` block tag passed.
- **broadcast (no wait):** returns the hash from `eth_sendRawTransaction`,
  `status: submitted`.
- **broadcast (--wait):** receipt sequence `null → mined (0x1)` yields
  `status: mined` with block/gas fields; receipt `status 0x0` yields
  `status: failed`; exhausted timeout yields `status: pending`.
- **submit error:** `eth_sendRawTransaction` RPC error → `RPCError` → exit 1,
  message on stderr.
- **validation:** malformed network, malformed address, non-hex `--raw-tx`.
- Polling tests inject a fake clock/sleep so they do not actually wait.

Manual e2e (documented in README, hoodi only): query the signer's balance, then
broadcast a tx built by `eth-tx-builder` and signed by the MCP, with `--wait`.

## Open questions

None. Scope, CLI, output shapes, and exit codes are fixed above.
