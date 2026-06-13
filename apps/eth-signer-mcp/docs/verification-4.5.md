# Issue 4.5: Pre-Tag Checklist Evidence

This file records the mechanical verification evidence for Issue 4.5.  
All commands run in the worktree `/Users/nil-00/git/rootwarp/blockchain-ai-tools-wt-4.5`
unless otherwise noted (path scrubbed in committed transcripts; worktree is on
branch `feat/4.5-release` at base commit `a358bef`).  
Date: 2026-06-13.

---

## 1. Confidence Re-run: `make lint && make test && make build`

All three targets run from the worktree root.

### 1.1 `make lint`

```
>> lint apps/eth-signer-mcp
0 issues.
```

Exit code: **0** ✅

### 1.2 `make test`

```
>> test apps/eth-signer-mcp
ok  	github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/cmd/eth-signer-mcp	3.638s
ok  	github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs	0.505s
ok  	github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/server	5.029s
ok  	github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing	10.655s
```

Exit code: **0** — all four packages green. ✅

### 1.3 `make build`

```
>> build apps/eth-signer-mcp
```

(No output — binary written to `bin/eth-signer-mcp`.)

Exit code: **0** ✅

---

## 2. Pins Spot-check

Command:

```
cd apps/eth-signer-mcp && go list -m \
  github.com/modelcontextprotocol/go-sdk \
  github.com/ethereum/go-ethereum \
  github.com/urfave/cli/v3
```

Output:

```
github.com/modelcontextprotocol/go-sdk v1.6.1
github.com/ethereum/go-ethereum v1.17.3
github.com/urfave/cli/v3 v3.9.0
```

| Dependency | Expected | Actual | Status |
|---|---|---|---|
| `github.com/modelcontextprotocol/go-sdk` | v1.6.1 | v1.6.1 | ✅ |
| `github.com/ethereum/go-ethereum` | v1.17.3 | v1.17.3 | ✅ |
| `github.com/urfave/cli/v3` | v3.9.0 | v3.9.0 | ✅ |

All three pins match expectations. ✅

---

## 3. Docs Mutual-Consistency Check

Checks performed with `grep`; cross-checked against the canonical sources
listed in issue 4.5.

### 3.1 Version strings — README vs release notes vs CHANGELOG

Command:

```
grep -rEn "v1\.6\.1|v1\.17\.3|v3\.9\.0|v1\.7\.1|2025-11-25" \
  apps/eth-signer-mcp/README.md \
  apps/eth-signer-mcp/docs/demo.md \
  apps/eth-signer-mcp/CHANGELOG.md \
  apps/eth-signer-mcp/docs/release-notes-v1.0.0.md
```

Output:

```
README.md:252:| MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) | `v1.6.1` |
README.md:253:| MCP protocol revision | **`2025-11-25`** — read verbatim from ...
README.md:254:| go-ethereum (`github.com/ethereum/go-ethereum`) | `v1.17.3` |
README.md:255:| urfave/cli (`github.com/urfave/cli/v3`) | `v3.9.0` |
README.md:257:| Foundry (cast) | `v1.7.1` ...
README.md:262:Golden vector regeneration (requires Foundry v1.7.1):
docs/demo.md:89:> ... go-sdk v1.6.1
docs/demo.md:92:> `mcp.CommandTransport` from go-sdk v1.6.1 ...
docs/release-notes-v1.0.0.md:46:| MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) | `v1.6.1` |
docs/release-notes-v1.0.0.md:47:| MCP protocol revision | **`2025-11-25`** |
docs/release-notes-v1.0.0.md:48:| go-ethereum (`github.com/ethereum/go-ethereum`) | `v1.17.3` |
docs/release-notes-v1.0.0.md:49:| urfave/cli (`github.com/urfave/cli/v3`) | `v3.9.0` |
docs/release-notes-v1.0.0.md:51:| Foundry (cast) | `v1.7.1` ...
docs/release-notes-v1.0.0.md:53:The MCP protocol revision `2025-11-25` is read verbatim from
docs/release-notes-v1.0.0.md:54:`latestProtocolVersion = protocolVersion20251125 = "2025-11-25"` in
docs/release-notes-v1.0.0.md:55:`mcp/shared.go` of the go-sdk v1.6.1 source.
docs/release-notes-v1.0.0.md:119:go-ethereum v1.17.3 is not affected by GO-2026-4314 ...
CHANGELOG.md:63:  v1.7.1) cross-check is deferred ...
```

**Finding:** All version pins are consistent across README, demo.md, CHANGELOG, and
release notes. CHANGELOG references Foundry v1.7.1 in the parity-suite note (cast
deferred status); release notes match. ✅

### 3.2 Latency numbers — README vs release notes vs demo.md

Command:

```
grep -rEn "0\.5.1 s|50 ms|warm path|standard.scrypt|light.scrypt" \
  apps/eth-signer-mcp/README.md \
  apps/eth-signer-mcp/docs/demo.md \
  apps/eth-signer-mcp/docs/release-notes-v1.0.0.md
```

Output (representative matches):

```
README.md:154:| Standard (geth default) | 262,144 | ~0.5–1 s |
README.md:155:| Light (`geth --lightkdf`) | 4,096 | ~50 ms |
README.md:158:**This cost is paid on every `sign_transaction` call — there is no warm path.**
docs/demo.md:39:This gives ~50 ms per decrypt ... Standard-scrypt
docs/demo.md:40:keystores (N=262144, geth default) cost ~0.5–1 s per call ...
docs/demo.md:346:cost ~0.5–1 s per call.  This is by design: scrypt KDF cost is paid on **every** signing
docs/demo.md:347:call because the decrypted key material is never cached (ADR-010 / no warm path).
```

Release-notes latency table: ~0.5–1 s standard, ~50 ms light, no warm path —
consistent with README and demo.md. ✅

Benchmark numbers in release notes (§Latency): light ~35.5 ms total / ~35.0 ms KDF /
~470 µs delta; standard ~416 ms / ~411 ms / ~4.9 ms — sourced verbatim from
`docs/verification-4.3.md` §4. ✅

### 3.3 Error codes — README vs CHANGELOG

Command:

```
grep -rEn "invalid_input|unsupported_type|chain_id_mismatch|keystore_error|password_error|internal_error" \
  apps/eth-signer-mcp/README.md \
  apps/eth-signer-mcp/CHANGELOG.md \
  apps/eth-signer-mcp/docs/release-notes-v1.0.0.md
```

Output (selected):

```
README.md:212:| `invalid_input` | ...
README.md:213:| `unsupported_type` | ...
README.md:214:| `chain_id_mismatch` | ...
README.md:215:| `keystore_error` | ...
README.md:216:| `password_error` | ...
README.md:217:| `internal_error` | ...
CHANGELOG.md:89:  six error-code paths (`invalid_input`, `unsupported_type`,
CHANGELOG.md:90:  `chain_id_mismatch`, `keystore_error`, `password_error`,
CHANGELOG.md:91:  `internal_error`), ...
docs/release-notes-v1.0.0.md:103: ... → `keystore_error`, non-zero exit.
docs/release-notes-v1.0.0.md:106: ... `password_error` returned ...
```

README §9 lists all six stable codes. CHANGELOG Security section lists all six in the
sentinel scan coverage. Release notes lifecycle table references `keystore_error` and
`password_error`. All consistent. ✅

### 3.4 `mcpServers` snippet — README vs demo.md

Command:

```
diff <(grep -A 12 '"mcpServers"' apps/eth-signer-mcp/README.md | head -13) \
     <(grep -A 12 '"mcpServers"' apps/eth-signer-mcp/docs/demo.md | head -13)
```

Output: *(empty — no diff)*

Both snippets are byte-identical. ✅

### 3.5 Flag reference — CHANGELOG vs README

All flags documented in CHANGELOG match the README §5 flag table
(`--keystore`, `--password-file`, `--http`, `--http-addr`,
`--http-auth-token-file`, `--chain-id`, `--strict-perms`, `--log-level`). ✅

### 3.6 Parity / cast-deferred claim consistency

CHANGELOG states: "parity verified against ethers v6.16.0; cast cross-check deferred
(documented in `internal/signing/testdata/vectors/cast-version.txt`)."

Release notes states: "Byte-identical parity verified against ethers v6.16.0 on all
committed golden vectors. `cast` cross-check is deferred; the deferred status is
documented in `internal/signing/testdata/vectors/cast-version.txt`."

demo.md and README both note the deferred cast status via `cast-version.txt`.

All four documents are consistent on the cast-deferred claim. ✅

**Note (acceptable imprecision):** `docs/demo.md` and `README.md` §11 say "ethers v6"
and "dual-oracle: cast + ethers v6" respectively, without pinning the exact patch
version (v6.16.0). The patch version is pinned in `cast-version.txt` and in
`verification-4.3.md` §1.3. This is not a consistency defect — the README links to
`scripts/regen-vectors.sh` which uses the pinned version, and the patch is recorded
where it is used (vectors). No change warranted.

---

## 4. Repo Tidy

### 4.1 Worktree status

Command:

```
git status
```

Output (before commit of this issue's files):

```
On branch feat/4.5-release
Untracked files:
  (use "git add <file>..." to include in what will be committed)
	apps/eth-signer-mcp/CHANGELOG.md
	apps/eth-signer-mcp/docs/release-notes-v1.0.0.md
	apps/eth-signer-mcp/docs/verification-4.5.md

nothing added to commit but untracked files present (use "git add" to track)
```

Only the three intended deliverables are untracked. No stray scratch files. ✅

The main tree (`blockchain-ai-tools/`) has two pre-existing user items:
`D apps/.gitkeep` (deleted) and `?? plan/research/05-e2e-tooling.md` (untracked
research note). These are in the main tree only, not in this worktree, and were
present before this branch was created. They are not touched by this issue.

### 4.2 Leftover mutation branches

Command:

```
git branch -a
```

Output:

```
+ develop
* feat/4.5-release
  main
  remotes/origin/HEAD -> origin/main
  remotes/origin/develop
  remotes/origin/main
```

No leftover mutation branches. The only local branches are `develop`, `main`, and
`feat/4.5-release` (this issue). ✅

### 4.3 TODO/FIXME in `internal/signing`

Command:

```
grep -rn "TODO\|FIXME" apps/eth-signer-mcp/internal/signing/ --include="*.go"
```

Output: *(empty)*

Zero TODO/FIXME in `internal/signing`. ✅

### 4.4 TODO/FIXME in all production code

Command:

```
grep -rn "TODO\|FIXME" apps/eth-signer-mcp/ --include="*.go" | grep -v "_test.go"
```

Output: *(empty)*

Zero TODO/FIXME in any production file. ✅

### 4.5 `bin/` untracked

`bin/eth-signer-mcp` is in `.gitignore` and does not appear in `git status`. ✅

### 4.6 `plan/` untouched

```
git diff HEAD -- plan/
```

Output: *(empty — no diff)*

Plan docs are untouched by this branch. ✅

---

## 5. Handoff List for Team Lead

The following steps are **not performed in this issue** and must be completed by the
team lead at release time:

### 5.1 Push `develop` and merge to `main`

```sh
# From the main tree (not the worktree):
git checkout develop
git merge --no-ff feat/4.5-release
git push origin develop
# Open PR: feat/4.5-release → develop → main (per project workflow)
# After CI green on the PR commit, merge to main.
git push origin main
```

### 5.2 Confirm CI green at the exact SHA

Workflow from issue 1.2 must be green on the tagged commit (includes:
`make lint` with depguard, `make test`, `make build`, `govulncheck ./...`,
`GOOS=windows` compile check).

```sh
gh run list --branch main --limit 5
```

### 5.3 Tag and push

```sh
git tag -a eth-signer-mcp/v1.0.0 -m "eth-signer-mcp v1.0.0"
git push origin eth-signer-mcp/v1.0.0
```

### 5.4 Verify tag

```sh
git fetch --tags && git describe --tags
# Expected output: eth-signer-mcp/v1.0.0
```

### 5.5 Post-release smoke (fresh-clone from the tag)

```sh
# Clone into a temp directory — must NOT be inside the existing monorepo worktrees:
git clone https://github.com/rootwarp/blockchain-ai-tools.git /tmp/eth-signer-smoke
cd /tmp/eth-signer-smoke
git checkout eth-signer-mcp/v1.0.0

# Build + version
make build
./bin/eth-signer-mcp --version
# Expected: version / commit / date / Go version all populated from the tag

# Test (record pass counts — parity suite and offline-import test explicitly)
make test

# Stdio demo (follow docs/demo.md §Demo 1 verbatim)
# Confirm: rawTransaction byte-matches golden vector; recovers to fixture address.

# HTTP demo
scripts/demo/http-demo.sh
# Confirm: exits 0; re-run 401/403 curl one-liners.

# Record full transcript in the issue 4.5 PR description.
# Any failure is a v1 blocker: fix forward on main and re-tag.
```

---

## 6. Acceptance Criteria Status

| Criterion | Status |
|---|---|
| `CHANGELOG.md` exists in Keep-a-Changelog format; v1.0.0 entry content maps to shipped, tested behaviour only | ✅ |
| Cast-deferred claim stated honestly (not papered over) | ✅ |
| `docs/release-notes-v1.0.0.md` exists with scope in/out, pins + protocol revision, measured benchmark numbers, honest latency, lifecycle contract, threat-model pointer | ✅ |
| Release notes advisory content limited to: govulncheck-in-CI + GO-2026-4508 fixed exactly v1.17.0 / other four fixed ≤ v1.16.9 + verification reference | ✅ |
| No "no known issues", no exploitability editorials, no forward-looking advisory promises | ✅ |
| `make lint` green | ✅ |
| `make test` green (all 4 packages) | ✅ |
| `make build` green | ✅ |
| Pins spot-check: go-sdk v1.6.1 / go-ethereum v1.17.3 / urfave/cli v3.9.0 | ✅ |
| README vs demo.md vs CHANGELOG vs release notes: versions consistent | ✅ |
| README vs CHANGELOG: error codes consistent (all six) | ✅ |
| README vs demo.md: mcpServers snippet byte-identical | ✅ |
| Latency numbers consistent across README, demo.md, release notes | ✅ |
| Zero TODO/FIXME in `internal/signing` | ✅ |
| Zero TODO/FIXME in production code | ✅ |
| No stray scratch files in worktree | ✅ |
| No leftover mutation branches | ✅ |
| `bin/` untracked | ✅ |
| `plan/` untouched by this branch | ✅ |
| Handoff list recorded for team lead (push, CI, tag, push-tag, smoke) | ✅ |
| Tag, push, CI verification at tagged SHA, smoke — **team lead's responsibility** | Handoff |
