// stdio-client is a minimal MCP demo client that drives eth-signer-mcp over stdio.
//
// It is the documented fallback when a GUI MCP client (e.g. Claude Desktop) is
// not available on the demo machine.  See apps/eth-signer-mcp/docs/demo.md for the
// full walkthrough.
//
// Usage:
//
//	go run ./cmd/stdio-client \
//	  -binary   /path/to/bin/eth-signer-mcp \
//	  -keystore /path/to/keystore.json \
//	  -password-file /path/to/password.txt \
//	  -want-raw-tx 0x... \
//	  [-chain-id 1]
//
// The program calls get_address then sign_transaction (legacy-mainnet golden-vector
// input), prints both results to stdout, and exits 0 on success. If -want-raw-tx is
// provided the rawTransaction must match exactly; exit 1 otherwise.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// legacyMainnetArgs are the transaction fields from the legacy-mainnet golden vector.
// Source of truth: apps/eth-signer-mcp/internal/signing/testdata/vectors/legacy-mainnet.json
var legacyMainnetArgs = map[string]any{
	"type":     "0x0",
	"chainId":  "1",
	"nonce":    "0",
	"to":       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
	"value":    "1000000000000000000",
	"data":     "0xdeadbeef",
	"gas":      "100000",
	"gasPrice": "20000000000",
}

func main() {
	binary := flag.String("binary", "", "path to eth-signer-mcp binary (required)")
	keystore := flag.String("keystore", "", "path to keystore JSON file (required)")
	passwordFile := flag.String("password-file", "", "path to password file (required)")
	wantRawTx := flag.String("want-raw-tx", "", "expected rawTransaction hex; compared byte-for-byte if provided")
	chainID := flag.String("chain-id", "", "optional --chain-id guard to pass to the server")
	flag.Parse()

	if *binary == "" || *keystore == "" || *passwordFile == "" {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "error: -binary, -keystore, and -password-file are required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Build the server command: the binary is launched as a subprocess by the
	// SDK's CommandTransport, which wires stdio pipes automatically.
	args := []string{
		"--keystore", *keystore,
		"--password-file", *passwordFile,
	}
	if *chainID != "" {
		args = append(args, "--chain-id", *chainID)
	}

	cmd := exec.CommandContext(ctx, *binary, args...)
	cmd.Stderr = os.Stderr // server logs go to this process's stderr

	client := mcp.NewClient(
		&mcp.Implementation{Name: "stdio-demo-client", Version: "v1.0.0"},
		nil,
	)

	fmt.Println("connecting to eth-signer-mcp via stdio (CommandTransport) ...")
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		log.Fatalf("Connect: %v", err)
	}
	defer func() {
		if closeErr := cs.Close(); closeErr != nil {
			log.Printf("cs.Close: %v (benign)", closeErr)
		}
	}()

	// ── get_address ──────────────────────────────────────────────────────────
	fmt.Println("\n>>> calling get_address ...")
	addrCallCtx, addrCancel := context.WithTimeout(ctx, 10*time.Second)
	addrResult, addrErr := cs.CallTool(addrCallCtx, &mcp.CallToolParams{
		Name:      "get_address",
		Arguments: map[string]any{},
	})
	addrCancel()
	if addrErr != nil {
		log.Fatalf("get_address protocol error: %v", addrErr)
	}
	if addrResult == nil || addrResult.IsError {
		log.Fatalf("get_address returned error result: %v", addrResult)
	}
	if len(addrResult.Content) == 0 {
		log.Fatal("get_address: empty Content")
	}
	addrTC, ok := addrResult.Content[0].(*mcp.TextContent)
	if !ok {
		log.Fatalf("get_address Content[0] is %T; want *mcp.TextContent", addrResult.Content[0])
	}
	var addrPayload struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(addrTC.Text), &addrPayload); err != nil {
		log.Fatalf("get_address unmarshal: %v\ntext: %s", err, addrTC.Text)
	}
	fmt.Printf("<<< get_address result: %s\n", addrTC.Text)

	// ── sign_transaction ─────────────────────────────────────────────────────
	fmt.Printf("\n>>> calling sign_transaction (legacy-mainnet vector) ...\n")
	fmt.Printf("    input: %v\n", legacyMainnetArgs)

	signCallCtx, signCancel := context.WithTimeout(ctx, 30*time.Second)
	signResult, signErr := cs.CallTool(signCallCtx, &mcp.CallToolParams{
		Name:      "sign_transaction",
		Arguments: legacyMainnetArgs,
	})
	signCancel()
	if signErr != nil {
		log.Fatalf("sign_transaction protocol error: %v", signErr)
	}
	if signResult == nil || signResult.IsError {
		var errText string
		if signResult != nil && len(signResult.Content) > 0 {
			if tc, ok := signResult.Content[0].(*mcp.TextContent); ok {
				errText = tc.Text
			}
		}
		log.Fatalf("sign_transaction returned error result: %s", errText)
	}
	if len(signResult.Content) == 0 {
		log.Fatal("sign_transaction: empty Content")
	}
	signTC, ok := signResult.Content[0].(*mcp.TextContent)
	if !ok {
		log.Fatalf("sign_transaction Content[0] is %T; want *mcp.TextContent", signResult.Content[0])
	}

	var signPayload struct {
		RawTransaction string `json:"rawTransaction"`
		Hash           string `json:"hash"`
		From           string `json:"from"`
		Signature      struct {
			R string `json:"r"`
			S string `json:"s"`
			V string `json:"v"`
		} `json:"signature"`
	}
	if err := json.Unmarshal([]byte(signTC.Text), &signPayload); err != nil {
		log.Fatalf("sign_transaction unmarshal: %v\ntext: %s", err, signTC.Text)
	}

	fmt.Printf("<<< sign_transaction result:\n")
	fmt.Printf("    rawTransaction: %s\n", signPayload.RawTransaction)
	fmt.Printf("    hash:           %s\n", signPayload.Hash)
	fmt.Printf("    from:           %s\n", signPayload.From)
	fmt.Printf("    signature.r:    %s\n", signPayload.Signature.R)
	fmt.Printf("    signature.s:    %s\n", signPayload.Signature.S)
	fmt.Printf("    signature.v:    %s\n", signPayload.Signature.V)

	// ── golden-vector assertion ──────────────────────────────────────────────
	if *wantRawTx != "" {
		if signPayload.RawTransaction == *wantRawTx {
			fmt.Printf("\n✓ rawTransaction MATCHES golden vector (byte-identical)\n")
		} else {
			fmt.Fprintf(os.Stderr, "\n✗ rawTransaction MISMATCH:\n  got:  %s\n  want: %s\n",
				signPayload.RawTransaction, *wantRawTx)
			os.Exit(1)
		}
	}

	fmt.Println("\n✓ stdio demo complete — server signs from the committed keystore fixture")
	fmt.Println("  NOTE: this transaction is NOT broadcast — the binary has no RPC capability (ADR-007)")
	fmt.Println("  NOTE: off-localhost exposure is unsupported")
}
