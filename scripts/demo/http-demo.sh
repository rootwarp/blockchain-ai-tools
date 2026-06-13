#!/bin/sh
# http-demo.sh — Streamable HTTP demo for eth-signer-mcp (Issue 4.1).
#
# What this script does:
#   1. Builds the binary (make build from the repo root) unless --no-build is given.
#   2. Generates a throwaway bearer token with mktemp (OUTSIDE the repo tree; never printed).
#   3. Starts eth-signer-mcp with --http --http-addr 127.0.0.1:0 and the throwaway token.
#   4. Reads the bound host:port from the server's startup stderr (no sleeps on the happy path).
#   5. Calls: initialize, tools/list, get_address, sign_transaction (bearer in every request).
#   6. Asserts the returned rawTransaction is byte-equal to the committed golden vector.
#   7. Demonstrates: missing bearer → 401; wrong bearer → 401; forged Host header → 403.
#   8. Kills the server; removes the throwaway token file; exits 0.
#
# Usage:
#   scripts/demo/http-demo.sh [--no-build]
#
# Requirements:
#   curl, python3 (JSON extraction), openssl (token generation), and a POSIX shell.
#
# Security notes:
#   - The throwaway token file is created outside the repo tree via mktemp.
#   - The token VALUE is never printed, logged, or stored inside the repo tree.
#   - The server binds only on loopback (127.0.0.1). Off-localhost access is unsupported.
#   - The signed transaction is NEVER broadcast; the binary has no RPC capability (ADR-007).
#
# Run from the repo root:
#   cd /path/to/blockchain-ai-tools && scripts/demo/http-demo.sh

set -eu

# ── Helpers ──────────────────────────────────────────────────────────────────

die() { echo "ERROR: $*" >&2; exit 1; }
info() { echo ">>> $*"; }
ok() { echo "✓  $*"; }

# ── Locate repo root ─────────────────────────────────────────────────────────
# Resolve the script's own location so the script can be run from any directory.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
info "repo root: $REPO_ROOT"

# ── Parse flags ──────────────────────────────────────────────────────────────
NO_BUILD=0
for arg in "$@"; do
  case "$arg" in
    --no-build) NO_BUILD=1 ;;
    *) die "unknown argument: $arg" ;;
  esac
done

# ── Build ─────────────────────────────────────────────────────────────────────
BINARY="$REPO_ROOT/bin/eth-signer-mcp"
if [ "$NO_BUILD" -eq 0 ]; then
  info "building binary (make build) ..."
  make -C "$REPO_ROOT" build >/dev/null
  ok "build complete: $BINARY"
else
  info "--no-build: skipping build; using $BINARY"
fi
[ -x "$BINARY" ] || die "binary not found or not executable: $BINARY"

# ── Fixture paths ─────────────────────────────────────────────────────────────
# The demo uses the light-scrypt keystore fixture (~50 ms per decrypt).
# These are committed test-only keys; do NOT use for real funds.
KS="$REPO_ROOT/apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json"
PW="$REPO_ROOT/apps/eth-signer-mcp/internal/signing/testdata/password.txt"
[ -f "$KS" ] || die "keystore fixture not found: $KS"
[ -f "$PW" ] || die "password fixture not found: $PW"

# Golden vector: rawTransaction from legacy-mainnet.json (committed).
GOLDEN_RAW_TX="0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac"

# ── Token generation (outside the repo tree) ──────────────────────────────────
# The token file is created under /tmp (not inside the repo tree) and removed at exit.
# The token value is never printed.
TOKEN_FILE="$(mktemp /tmp/eth-signer-mcp-demo-token.XXXXXX)"
chmod 600 "$TOKEN_FILE"
# Generate a 32-byte random hex token.
openssl rand -hex 32 > "$TOKEN_FILE"
TOKEN="$(cat "$TOKEN_FILE")"  # read token for use in headers (never echoed)

# ── Cleanup trap ──────────────────────────────────────────────────────────────
# SERVER_PID and STDERR_FILE are declared here (before the trap registration) so
# cleanup() can always `rm -f` them even if they were never assigned — an empty
# string makes rm -f a safe no-op.
SERVER_PID=""
STDERR_FILE=""

cleanup() {
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -f "$TOKEN_FILE"
  rm -f "$STDERR_FILE"
}
# EXIT only — avoids double-fire: the shell delivers EXIT on signal-caused exit too.
trap cleanup EXIT

# ── Start the server ─────────────────────────────────────────────────────────
# Capture stderr to a temp file so we can scrape the bound address without
# a sleep. The announce line format is:
#   eth-signer-mcp listening on 127.0.0.1:PORT
STDERR_FILE="$(mktemp /tmp/eth-signer-mcp-demo-stderr.XXXXXX)"

"$BINARY" \
  --keystore "$KS" \
  --password-file "$PW" \
  --http \
  --http-addr "127.0.0.1:0" \
  --http-auth-token-file "$TOKEN_FILE" \
  2>"$STDERR_FILE" &
SERVER_PID=$!

# Poll for the announce line (max 10 s, check every 100 ms).
ADDR=""
I=0
while [ $I -lt 100 ]; do
  ADDR="$(grep -m1 "listening on" "$STDERR_FILE" 2>/dev/null | sed 's/.*listening on //' | tr -d '[:space:]')" || true
  [ -n "$ADDR" ] && break
  sleep 0.1
  I=$((I + 1))
done
# On failure: emit the captured startup stderr for diagnostics BEFORE dying.
# STDERR_FILE is cleaned up by the EXIT trap (not here), so it is available here.
if [ -z "$ADDR" ]; then
  echo "--- server startup stderr (diagnostics) ---" >&2
  cat "$STDERR_FILE" >&2
  die "server did not print 'listening on' within 10 s"
fi
ok "server bound at http://$ADDR"

# Verify the server is still running.
kill -0 "$SERVER_PID" 2>/dev/null || die "server process exited prematurely"

MCP_URL="http://$ADDR/mcp"

# Helper: POST to MCP and return the SSE response.
# Args: request_body [session_id]
# Uses set -- to build the optional session-id header without word-splitting.
mcp_call() {
  REQ_BODY="$1"
  SID="${2:-}"
  if [ -n "$SID" ]; then
    set -- -H "Mcp-Session-Id: $SID"
  else
    set --
  fi
  curl -s \
    -X POST "$MCP_URL" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -H "Authorization: Bearer $TOKEN" \
    "$@" \
    -d "$REQ_BODY" \
    --max-time 30
}

# Helper: extract rawTransaction from a structuredContent SSE data response.
# JSON data is passed via the MCP_DATA env var; the quoted heredoc (<<'EOF')
# prevents shell expansion of the Python source.
extract_raw_tx() {
  DATA_LINE="$(echo "$1" | grep '^data: ' | sed 's/^data: //')"
  MCP_DATA="$DATA_LINE" python3 <<'EOF'
import json, os
d = json.loads(os.environ['MCP_DATA'])
sc = d.get('result', {}).get('structuredContent', {})
print(sc.get('rawTransaction', ''))
EOF
}

# Helper: extract a named field from structuredContent.
# The field name is passed via sys.argv[1] (not interpolated into Python source).
# JSON data is passed via the MCP_DATA env var; the quoted heredoc (<<'EOF')
# prevents shell expansion of the Python source.
extract_sc_field() {
  DATA_LINE="$(echo "$1" | grep '^data: ' | sed 's/^data: //')"
  MCP_DATA="$DATA_LINE" python3 - "$2" <<'EOF'
import json, os, sys
d = json.loads(os.environ['MCP_DATA'])
sc = d.get('result', {}).get('structuredContent', {})
print(sc.get(sys.argv[1], ''))
EOF
}

# Helper: extract HTTP status code from raw curl -si output.
http_status() {
  echo "$1" | head -1 | awk '{print $2}'
}

# ── Step 1: initialize ───────────────────────────────────────────────────────
info "Step 1: initialize"
INIT_RESP="$(curl -si \
  -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"http-demo-sh","version":"1.0"}}}' \
  --max-time 10)"
INIT_STATUS="$(http_status "$INIT_RESP")"
[ "$INIT_STATUS" = "200" ] || die "initialize: expected HTTP 200, got $INIT_STATUS"

# Case-insensitive header extraction: strip "HeaderName: " using [^:]* pattern so
# the sed expression is independent of the actual capitalisation the server sends.
SESSION_ID="$(echo "$INIT_RESP" | grep -i "^mcp-session-id:" | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')"
[ -n "$SESSION_ID" ] || die "initialize: no Mcp-Session-Id header in response"
ok "initialized; session=$SESSION_ID"

# Send the required initialized notification (no response expected, status 202).
curl -s \
  -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
  --max-time 5 >/dev/null

# ── Step 2: tools/list ───────────────────────────────────────────────────────
info "Step 2: tools/list"
TOOLS_RESP="$(mcp_call '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' "$SESSION_ID")"
# Check both sign_transaction and get_address are listed.
echo "$TOOLS_RESP" | grep -q '"sign_transaction"' || die "tools/list: sign_transaction not listed"
echo "$TOOLS_RESP" | grep -q '"get_address"' || die "tools/list: get_address not listed"
ok "tools/list: sign_transaction + get_address present"

# ── Step 3: get_address ──────────────────────────────────────────────────────
info "Step 3: get_address"
ADDR_RESP="$(mcp_call '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_address","arguments":{}}}' "$SESSION_ID")"
FIXTURE_ADDR="$(extract_sc_field "$ADDR_RESP" "address")"
EXPECTED_ADDR="0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
[ "$FIXTURE_ADDR" = "$EXPECTED_ADDR" ] || die "get_address: got '$FIXTURE_ADDR'; want '$EXPECTED_ADDR'"
ok "get_address: $FIXTURE_ADDR"

# ── Step 4: sign_transaction ─────────────────────────────────────────────────
info "Step 4: sign_transaction (legacy-mainnet golden vector)"
SIGN_REQ='{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"0x0","chainId":"1","nonce":"0","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"1000000000000000000","data":"0xdeadbeef","gas":"100000","gasPrice":"20000000000"}}}'
SIGN_RESP="$(mcp_call "$SIGN_REQ" "$SESSION_ID")"

RAW_TX="$(extract_raw_tx "$SIGN_RESP")"
FROM="$(extract_sc_field "$SIGN_RESP" "from")"
TX_HASH="$(extract_sc_field "$SIGN_RESP" "hash")"

info "  rawTransaction: $RAW_TX"
info "  from:           $FROM"
info "  hash:           $TX_HASH"

# ── Golden-vector assertion ──────────────────────────────────────────────────
info "Asserting rawTransaction == golden vector ..."
if [ "$RAW_TX" = "$GOLDEN_RAW_TX" ]; then
  ok "rawTransaction is byte-identical to the committed golden vector"
else
  echo "MISMATCH:" >&2
  echo "  got:  $RAW_TX" >&2
  echo "  want: $GOLDEN_RAW_TX" >&2
  die "rawTransaction does not match golden vector"
fi

if [ "$FROM" = "$EXPECTED_ADDR" ]; then
  ok "recovered from == fixture address ($FROM)"
else
  die "from mismatch: got '$FROM'; want '$EXPECTED_ADDR'"
fi

# ── Security demonstrations ───────────────────────────────────────────────────
info "Security demo 1: missing bearer token → 401"
UNAUTH_RESP="$(curl -si \
  -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":99,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"bad","version":"1.0"}}}' \
  --max-time 5)"
UNAUTH_STATUS="$(http_status "$UNAUTH_RESP")"
[ "$UNAUTH_STATUS" = "401" ] || die "expected 401 for missing bearer, got $UNAUTH_STATUS"
ok "missing bearer → 401 Unauthorized"

info "Security demo 2: wrong bearer token → 401"
WRONG_RESP="$(curl -si \
  -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer this-is-the-wrong-token" \
  -d '{"jsonrpc":"2.0","id":99,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"bad","version":"1.0"}}}' \
  --max-time 5)"
WRONG_STATUS="$(http_status "$WRONG_RESP")"
[ "$WRONG_STATUS" = "401" ] || die "expected 401 for wrong bearer, got $WRONG_STATUS"
ok "wrong bearer → 401 Unauthorized"

info "Security demo 3: forged Host header (DNS-rebinding guard) → 403"
REBIND_RESP="$(curl -si \
  -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Host: evil.example.com" \
  -d '{"jsonrpc":"2.0","id":99,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"bad","version":"1.0"}}}' \
  --max-time 5)"
REBIND_STATUS="$(http_status "$REBIND_RESP")"
[ "$REBIND_STATUS" = "403" ] || die "expected 403 for forged Host, got $REBIND_STATUS"
ok "forged Host header → 403 Forbidden (DNS-rebinding guard)"

# ── Done ─────────────────────────────────────────────────────────────────────
echo ""
echo "✓ HTTP demo complete"
echo "  NOTE: the signed transaction was NOT broadcast — the binary has no RPC capability (ADR-007)"
echo "  NOTE: off-localhost exposure is unsupported (loopback-only bind enforced)"
echo ""
