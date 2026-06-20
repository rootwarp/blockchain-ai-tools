# Address Book

Human-readable names for Ethereum addresses, read **inline at runtime by the
`eth-ops` skill** to resolve names → addresses for inputs (recipient / spender /
sender / holder) and to annotate addresses → names in output. This file is the
**single source of truth** for those mappings: `eth-ops` hard-codes none of them —
editing this table is the only step needed to change a mapping. Sibling to, and
parsed the same inline way as, `ERC20.md`.

This committed file is a **template**. Copy it to `address_book.md` (the live book
`eth-ops` actually reads, kept out of git) and edit that copy with your own
entries:

```bash
cp address_book.example.md address_book.md
```

Names are **case-insensitive**, restricted to the charset `[A-Za-z0-9._-]`, and a
name that looks like a `0x`+40-hex address is invalid. The **Network** column is
optional: leave it **blank** for an address that is the same on every EVM chain
(EOAs — the common case), and set it (`mainnet` / `hoodi` / `sepolia` /
`holesky`; `holesky` is deprecated) only for entries that live at a **different
address per chain** (typically contracts).

> **Safety.** An **unknown** name is never guessed, fuzzy-matched, or coerced
> into a raw address — `eth-ops` stops and asks. A name never shadows a literal
> `0x` address: a pasted `0x`+40-hex input is always taken as-is. Both
> confirmation gates show the resolved name next to the **full** hex, and remain
> the real backstop — the name is a recognition aid, never the verification
> control.
>
> **Curation.** Add or edit an address **only** after verifying it against a
> trusted, independent source — **never** paste an address from transaction
> history or a block-explorer "from" field (that is exactly how address poisoning
> lands). Treat the first send to a new or edited entry as **unverified**: keep
> the full hex in view at Gate 2 and send a small test transfer first.
>
> **Checksum.** Addresses are stored **EIP-55 checksummed** (canonical mixed
> case). A mixed-case checksum failure is a **typo** warning only — never an
> authenticity guarantee. A poisoned look-alike is a perfectly valid,
> correctly-checksummed address, so a passing checksum proves nothing about
> whether it is the address you meant.

## Format rules

- Columns are fixed in this order: **Name | Address | Network | Notes**. Every
  row has all four cells: keep the leading and trailing `|`, and write any empty
  `Network`/`Notes` cell as `| |` — **never drop a pipe**. A short row does not
  error in GFM; it silently pads/shifts, sliding data into the wrong column:

  ```markdown
  GOOD (4 cells; Network is an explicit empty string after trimming):
  | vitalik | `0xd8dA…6045` |  | example EOA |

  BAD  (3 cells; GFM pads/shifts silently — Notes slides into Network):
  | vitalik | `0xd8dA…6045` | example EOA |
  ```

- **Name** — case-insensitive; charset `[A-Za-z0-9._-]` only; must **not** look
  like a `0x`+40-hex address. Each name appears at most once **globally**, or at
  most once **per named Network** — never both a global row and named rows for
  the same name.
- **Address** — backticked, stored **EIP-55 checksummed** (canonical mixed case),
  exactly `0x` + 40 hex.
- **Network** — **optional**. Blank = **global / all EVM chains** (use for EOAs,
  identical on every EVM chain). Set it (`mainnet` / `hoodi` / `sepolia` /
  `holesky`) only for entries that live at a **different address per chain**
  (typically contracts). `holesky` is deprecated; any other value is a malformed
  cell to flag.
- **Notes** — free text, optional, plain only (no pipes, code fences, or HTML
  comments inside any cell — put commentary here as plain text or in prose).

## Entries

> **Authoring note.** Every cell below is shown in full. **Do not** author a row
> with a truncated `0x…` placeholder in the **Address** column — a literal-minded
> copy would be a malformed row (fails the exact `0x`+40-hex load check). Where a
> real full address is not at hand, leave the row out until you have it.

| Name    | Address                                      | Network | Notes                                |
|---------|----------------------------------------------|---------|--------------------------------------|
| vitalik | `0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045` |         | example EOA; global (all EVM chains) |
| beacon  | `0x00000000219ab540356cBB839Cbe05303d7705Fa` | mainnet | example per-chain contract — mainnet |
| beacon  | `0x4242424242424242424242424242424242424242` | sepolia | example per-chain contract — sepolia |

The two `beacon` rows demonstrate network scoping with **full** checksummed
addresses; `vitalik` is the global-EOA example with a **blank** Network cell
(written `|         |`, pipe present). All entries are illustrative — replace
them with your own in `address_book.md`.
