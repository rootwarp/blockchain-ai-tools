//go:build ignore

// gen_fixtures.go generates the Web3 Secret Storage keystore fixtures used by
// the signing-package tests. Run it from the testdata directory:
//
//	cd apps/eth-signer-mcp/internal/signing/testdata
//	go run gen_fixtures.go
//
// # Why Go instead of geth CLI?
//
// geth CLI cannot emit a keystore with n=2 (the weakened test KDF). Using
// go-ethereum v1.17.3's keystore.EncryptKey directly lets us generate all three
// KDF strengths (standard, light, and weakened) from one program. The produced
// files are byte-compatible Web3 Secret Storage keystores that decrypt
// identically via keystore.DecryptKey. See README.md for the geth-equivalent
// commands that would produce the standard and light variants.
//
// # Fixed test key
//
// The same ONE throwaway private key is encrypted under all three KDF strengths.
// Its raw scalar, address, and encoded forms are documented ONLY in README.md —
// that is the single disclosure path. Do not copy the key value elsewhere.
//
// WARNING: This key is TEST-ONLY. Never use it to hold real funds.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
)

// testPrivKeyHex is the fixed test private key scalar (32 bytes, hex-encoded).
// WARNING: TEST-ONLY — never use this key for real funds.
// Single disclosure path: README.md (this constant exists only to drive generation).
const testPrivKeyHex = "1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727"

// password is the passphrase encrypting all three keystores.
// Written to password.txt WITH a trailing newline so the vault's
// strip-trailing-newline path is exercised.
const password = "test-only-password-do-not-reuse"

func main() {
	privKeyBytes, err := hex.DecodeString(testPrivKeyHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid privkey hex: %v\n", err)
		os.Exit(1)
	}
	privKey, err := crypto.ToECDSA(privKeyBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid privkey: %v\n", err)
		os.Exit(1)
	}

	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	fmt.Printf("Test key address: %s\n", addr.Hex())

	// Build the keystore Key. The UUID (Id field) is left as the zero value —
	// it is a file-identity field only and does not affect decryption.
	key := &keystore.Key{
		Address:    addr,
		PrivateKey: privKey,
		// Id zero UUID — acceptable for test fixtures.
	}

	type kdfSpec struct {
		file    string
		scryptN int
		scryptP int
		label   string
	}
	specs := []kdfSpec{
		{
			file:    "keystore-standard.json",
			scryptN: keystore.StandardScryptN, // 262144
			scryptP: keystore.StandardScryptP, // 1
			label:   "standard",
		},
		{
			file:    "keystore-light.json",
			scryptN: keystore.LightScryptN, // 4096
			scryptP: keystore.LightScryptP, // 1
			label:   "light",
		},
		{
			// Weakened KDF: n=2, r=8 (r is fixed at 8 by go-ethereum), p=1.
			// TEST-ONLY WEAKENED KDF — NEVER USE THIS PATTERN IN PRODUCTION.
			file:    "keystore-weak.json",
			scryptN: 2,
			scryptP: 1,
			label:   "weak (n=2, test-only)",
		},
	}

	for _, spec := range specs {
		ciphertext, err := keystore.EncryptKey(key, password, spec.scryptN, spec.scryptP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "EncryptKey(%s): %v\n", spec.file, err)
			os.Exit(1)
		}

		// Pretty-print for readable diffs.
		var pretty map[string]any
		if err := json.Unmarshal(ciphertext, &pretty); err != nil {
			fmt.Fprintf(os.Stderr, "json.Unmarshal(%s): %v\n", spec.file, err)
			os.Exit(1)
		}
		out, err := json.MarshalIndent(pretty, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "json.MarshalIndent(%s): %v\n", spec.file, err)
			os.Exit(1)
		}
		out = append(out, '\n')

		if err := os.WriteFile(spec.file, out, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", spec.file, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %-28s  KDF: scrypt %s\n", spec.file, spec.label)
	}

	// Write password.txt WITH a trailing newline.
	if err := os.WriteFile("password.txt", []byte(password+"\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write password.txt: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("wrote password.txt")

	// Verify round-trip decryption for all generated files.
	fmt.Println("\nVerifying generated keystores...")
	for _, spec := range specs {
		raw, err := os.ReadFile(spec.file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", spec.file, err)
			os.Exit(1)
		}
		k, err := keystore.DecryptKey(raw, password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "DecryptKey(%s): %v\n", spec.file, err)
			os.Exit(1)
		}
		gotAddr := k.Address.Hex()
		if gotAddr != addr.Hex() {
			fmt.Fprintf(os.Stderr, "address mismatch in %s: got %s, want %s\n", spec.file, gotAddr, addr.Hex())
			os.Exit(1)
		}
		gotHex := hex.EncodeToString(crypto.FromECDSA(k.PrivateKey))
		if gotHex != testPrivKeyHex {
			fmt.Fprintf(os.Stderr, "private key mismatch in %s\n", spec.file)
			os.Exit(1)
		}
		fmt.Printf("  OK: %s → address=%s\n", spec.file, gotAddr)
	}
	fmt.Println("All keystores verified.")

	fmt.Printf(`
After generation, create the malformed-keystore fixtures:
  cp keystore-weak.json keystore-no-address.json    # then remove the "address" field
  cp keystore-weak.json keystore-empty-address.json # then set "address": ""
`)
}
