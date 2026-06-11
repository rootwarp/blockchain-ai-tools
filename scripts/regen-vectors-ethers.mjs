#!/usr/bin/env node
/**
 * regen-vectors-ethers.mjs
 *
 * One-shot ethers v6 oracle: builds and signs every tx in the vector matrix,
 * then writes canonical JSON files to the output directory.
 *
 * Private key sourced from (single disclosure path):
 *   apps/eth-signer-mcp/internal/signing/testdata/README.md
 * Address: 0x9858EfFD232B4033E47d90003D41EC34EcaEda94
 *
 * Usage (invoked by scripts/regen-vectors.sh; can also be run manually):
 *   node scripts/regen-vectors-ethers.mjs [output-dir]
 *
 * Requires ethers@6 resolvable from this file's directory (e.g. via
 * `npm install ethers@6` run inside scripts/ or a parent directory).
 *
 * Output: one <name>.json per signing vector in output-dir.
 * Rejection vectors are static hand-written JSON files (no oracle signing needed;
 * they only define the expected error code and input, not a signed transaction output).
 *
 * No network calls are made. All signing is fully offline.
 */

import { ethers } from 'ethers';
import { writeFileSync, mkdirSync } from 'fs';
import { resolve, dirname, join } from 'path';
import { fileURLToPath } from 'url';

// ---------------------------------------------------------------------------
// Key material — test-only, see README.md for the full disclosure block.
// ---------------------------------------------------------------------------
const PRIVATE_KEY = '0x1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727';
const EXPECTED_ADDRESS = '0x9858EfFD232B4033E47d90003D41EC34EcaEda94';

const wallet = new ethers.Wallet(PRIVATE_KEY);
if (wallet.address !== EXPECTED_ADDRESS) {
  console.error(
    `FATAL: key sanity check failed: got ${wallet.address}, ` +
    `expected ${EXPECTED_ADDRESS}`
  );
  process.exit(1);
}

// Destination address used for transfer vectors (the fixture address itself).
const TO = '0x9858EfFD232B4033E47d90003D41EC34EcaEda94';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Parse a TxRequest-style numeric string (decimal or 0x-hex) to BigInt.
 * JavaScript's BigInt() constructor natively handles both "0x..."-prefixed hex
 * strings and plain decimal strings, so no explicit prefix check is needed.
 */
function parseBigInt(s) {
  if (typeof s === 'bigint') return s;
  return BigInt(String(s).trim());
}

/** Convert a BigInt to a 0x-prefixed lowercase hex string. */
function toHex(n) {
  return '0x' + BigInt(n).toString(16);
}

/**
 * Build an ethers v6 transaction object from a TxRequest-style spec.
 * Field name mapping:
 *   TxRequest.gas          → ethers.gasLimit
 *   TxRequest.type "0x0"   → ethers.type 0
 *   TxRequest.type "0x2"   → ethers.type 2
 */
function buildEthersTx(req) {
  const txType = (req.type === '0x0' || req.type === 'legacy') ? 0 : 2;
  const tx = {
    type: txType,
    chainId: parseBigInt(req.chainId),
    // nonce must be a number, not BigInt, for ethers v6
    nonce: Number(parseBigInt(req.nonce)),
    value: parseBigInt(req.value),
    data: req.data,
    gasLimit: parseBigInt(req.gas),
  };
  // 'to' is omitted for contract creation
  if (req.to !== undefined && req.to !== '') {
    tx.to = req.to;
  }
  if (req.gasPrice !== undefined) {
    tx.gasPrice = parseBigInt(req.gasPrice);
  }
  if (req.maxFeePerGas !== undefined) {
    tx.maxFeePerGas = parseBigInt(req.maxFeePerGas);
  }
  if (req.maxPriorityFeePerGas !== undefined) {
    tx.maxPriorityFeePerGas = parseBigInt(req.maxPriorityFeePerGas);
  }
  return tx;
}

// ---------------------------------------------------------------------------
// Vector specs — tx field is the EXACT TxRequest JSON sent on the wire.
// These are the 9 signing vectors; rejection vectors are written separately.
// ---------------------------------------------------------------------------
const SIGNING_SPECS = [
  {
    name: 'legacy-mainnet',
    description: 'type 0, chainId 1, standard transfer — asserts EIP-155 v = chainId*2+35/36',
    tx: {
      type: '0x0',
      chainId: '1',
      nonce: '0',
      to: TO,
      value: '1000000000000000000',
      data: '0xdeadbeef',
      gas: '100000',
      gasPrice: '20000000000',
    },
  },
  {
    name: 'legacy-sepolia',
    description: 'type 0, chainId 11155111 — asserts large-chainId EIP-155 v',
    tx: {
      type: '0x0',
      chainId: '11155111',
      nonce: '5',
      to: TO,
      value: '1000000000000000000',
      data: '0xabcdef',
      gas: '100000',
      gasPrice: '1000000000',
    },
  },
  {
    name: '1559-mainnet',
    description: 'type 2, chainId 1 — asserts yParity v = 0 or 1',
    tx: {
      type: '0x2',
      chainId: '1',
      nonce: '42',
      to: TO,
      value: '1000000000000000000',
      data: '0xcafebabe',
      gas: '100000',
      maxFeePerGas: '30000000000',
      maxPriorityFeePerGas: '2000000000',
    },
  },
  {
    name: '1559-sepolia',
    description: 'type 2, chainId 11155111 — asserts yParity v on non-mainnet',
    tx: {
      type: '0x2',
      chainId: '11155111',
      nonce: '10',
      to: TO,
      value: '500000000000000000',
      data: '0x1234',
      gas: '100000',
      maxFeePerGas: '5000000000',
      maxPriorityFeePerGas: '500000000',
    },
  },
  {
    name: 'legacy-empty-data-zero-value',
    description: 'type 0, data "0x", value "0" — empty data must encode to RLP 0x80',
    tx: {
      type: '0x0',
      chainId: '1',
      nonce: '0',
      to: TO,
      value: '0',
      data: '0x',
      gas: '21000',
      gasPrice: '20000000000',
    },
  },
  {
    name: '1559-empty-data-zero-value',
    description: 'type 2, data "0x", value "0" — same empty-data/zero-value edge for type 2',
    tx: {
      type: '0x2',
      chainId: '1',
      nonce: '0',
      to: TO,
      value: '0',
      data: '0x',
      gas: '21000',
      maxFeePerGas: '30000000000',
      maxPriorityFeePerGas: '2000000000',
    },
  },
  {
    name: 'legacy-contract-creation',
    description: 'type 0, to omitted, non-empty data — contract creation path',
    tx: {
      type: '0x0',
      chainId: '1',
      nonce: '0',
      // 'to' intentionally omitted
      value: '0',
      data: '0x6080604052',
      gas: '200000',
      gasPrice: '20000000000',
    },
  },
  {
    name: '1559-contract-creation',
    description: 'type 2, to omitted, non-empty data — contract creation for EIP-1559',
    tx: {
      type: '0x2',
      chainId: '1',
      nonce: '0',
      // 'to' intentionally omitted
      value: '0',
      data: '0x6080604052',
      gas: '200000',
      maxFeePerGas: '30000000000',
      maxPriorityFeePerGas: '2000000000',
    },
  },
  {
    name: 'legacy-padded-nonce',
    description: 'type 0, input nonce "0x0009" — leading zeros must normalize to canonical 9',
    tx: {
      type: '0x0',
      chainId: '1',
      nonce: '0x0009',
      to: TO,
      value: '0',
      data: '0x',
      gas: '21000',
      gasPrice: '20000000000',
    },
  },
];

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
async function main() {
  const scriptDir = dirname(fileURLToPath(import.meta.url));
  const outputDir = process.argv[2]
    ? resolve(process.argv[2])
    : resolve(scriptDir, '../apps/eth-signer-mcp/internal/signing/testdata/vectors');

  mkdirSync(outputDir, { recursive: true });

  // REGEN_TIMESTAMP is a deterministic placeholder for the regenerated_at field.
  // Using a fixed value (rather than Date.now()) keeps git diffs minimal: re-running
  // the script on identical inputs produces identical JSON. Update this timestamp
  // manually when intentionally regenerating vectors for a new oracle version or
  // after a go-ethereum bump so that drift is attributable to a specific event.
  const REGEN_TIMESTAMP = '2026-06-11T00:00:00Z';

  const ethersVersion = ethers.version;
  console.log(`ethers version: ${ethersVersion}`);
  console.log(`output dir:     ${outputDir}`);
  console.log('');

  const rawTxByName = {};

  for (const spec of SIGNING_SPECS) {
    const ethersTx = buildEthersTx(spec.tx);
    const signedRaw = await wallet.signTransaction(ethersTx);
    const parsed = ethers.Transaction.from(signedRaw);

    // Determine v consistent with go-ethereum RawSignatureValues():
    //   type 0 (legacy + EIP-155): networkV = chainId*2 + 35 + yParity
    //   type 2 (EIP-1559):         yParity = 0 or 1
    const txType = spec.tx.type === '0x0' || spec.tx.type === 'legacy' ? 0 : 2;
    const vBigInt = txType === 0
      ? parsed.signature.networkV          // EIP-155 v (chainId*2+35/36)
      : BigInt(parsed.signature.yParity);  // yParity (0 or 1)

    const vector = {
      name: spec.name,
      tx: spec.tx,
      expected: {
        raw_tx: signedRaw,
        tx_hash: parsed.hash,
        r: parsed.signature.r,
        s: parsed.signature.s,
        v: toHex(vBigInt),
      },
      meta: {
        oracles: {
          ethers: ethersVersion,
          cast: 'not-run-in-this-env',
        },
        regenerated_at: REGEN_TIMESTAMP,
      },
    };

    const outPath = join(outputDir, `${spec.name}.json`);
    writeFileSync(outPath, JSON.stringify(vector, null, 2) + '\n');

    rawTxByName[spec.name] = signedRaw;
    console.log(`  [ok] ${spec.name}`);
    console.log(`       hash:   ${parsed.hash}`);
    console.log(`       raw_tx: ${signedRaw.slice(0, 40)}...`);
  }

  console.log('');
  console.log(`Wrote ${SIGNING_SPECS.length} signing vectors to ${outputDir}`);

  // Emit raw tx map to stdout as JSON so regen-vectors.sh can do byte-compare
  // with cast output when cast is available.
  const resultLine = JSON.stringify({ raw_tx_by_name: rawTxByName });
  process.stdout.write('\n__ETHERS_RESULTS__\n' + resultLine + '\n');
}

main().catch((err) => {
  console.error('fatal:', err.message || err);
  process.exit(1);
});
