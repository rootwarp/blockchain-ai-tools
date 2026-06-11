package main

import (
	"fmt"
	"net"
	"strings"
)

// config holds the parsed and validated CLI configuration for eth-signer-mcp.
//
// This struct lives in package main (the cmd composition root) and is NOT
// exported to — nor imported by — any internal package. It is parsed once,
// validated, and its fields passed individually to constructors (architecture
// §cmd/eth-signer-mcp, §Module Overview: "config is a cmd-local struct, not a
// package").
type config struct {
	KeystorePath, PasswordPath string
	HTTP                       bool
	HTTPAddr                   string  // default "127.0.0.1:0"
	TokenFilePath              string  // required when HTTP is true
	ChainIDGuard               *uint64 // nil when --chain-id unset; see buildConfig
	StrictPerms                bool
	LogLevel                   string // default "info"; must be debug|info|warn|error
}

// validate performs cross-field validation on cfg. urfave/cli's Required:true
// already enforces that KeystorePath and PasswordPath are non-empty at the
// flag-parsing layer; we re-check defensively here so validate() can be unit-
// tested in isolation.
//
// Validation rules:
//
//   - KeystorePath and PasswordPath must be non-empty.
//   - --http set requires --http-auth-token-file (PRD P0-CLI-4: no token, no HTTP).
//   - --chain-id 0 is rejected: chain-id 0 is replay-unprotected (no EIP-155
//     protection). A guard value of 0 can never match a valid transaction
//     chain-id, so we fail fast with a clear message (architecture §Assumptions).
//   - --log-level must be one of debug|info|warn|error (case-insensitive).
//     obs.NewLogger falls back to "info" on unknown levels, but the flag
//     validation gives the operator an explicit error instead of a silent fallback.
func validate(cfg config) error {
	if cfg.KeystorePath == "" {
		return fmt.Errorf("--keystore is required")
	}
	if cfg.PasswordPath == "" {
		return fmt.Errorf("--password-file is required")
	}
	if cfg.HTTP && cfg.TokenFilePath == "" {
		return fmt.Errorf("--http-auth-token-file is required when --http is set")
	}
	// When --http is set, validate that --http-addr is a loopback address
	// (ADR-006: bind only on loopback).  We parse host and IP; the error message
	// deliberately does NOT echo cfg.HTTPAddr (architecture §Error Handling: never
	// echo raw user input in error messages).
	if cfg.HTTP && cfg.HTTPAddr != "" {
		host, _, splitErr := net.SplitHostPort(cfg.HTTPAddr)
		if splitErr != nil {
			return fmt.Errorf("--http-addr: invalid host:port — must be a loopback address (e.g. 127.0.0.1:0 or [::1]:0)")
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("--http-addr must be a loopback address (127.0.0.1 or [::1]); non-loopback binds are rejected (ADR-006)")
		}
	}
	if cfg.ChainIDGuard != nil && *cfg.ChainIDGuard == 0 {
		return fmt.Errorf("--chain-id 0 is rejected: chain-id 0 is replay-unprotected; use a non-zero chain-id")
	}
	switch strings.ToLower(cfg.LogLevel) {
	case "debug", "info", "warn", "error":
		// valid
	default:
		// Do not echo cfg.LogLevel: architecture §Error Handling forbids echoing
		// raw user input in error messages (it could surface a mis-pasted secret
		// in stderr / CI logs / shell history).
		return fmt.Errorf("--log-level must be one of debug|info|warn|error")
	}
	return nil
}
