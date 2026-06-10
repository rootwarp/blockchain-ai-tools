// Package signing_test contains tests for the signing package's wire-contract
// schema and marshalling guarantees.
package signing_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// update is set via -update to regenerate golden schema files.
// Usage: go test ./internal/signing/... -run TestGoldenSchema -update
var update = flag.Bool("update", false, "regenerate golden schema files instead of comparing")

const schemaTestDataDir = "testdata/schema"

// TestGoldenSchema_TxRequest pins the JSON schema inferred from signing.TxRequest.
//
// What this test guarantees:
//   - additionalProperties:false is present (struct inference always emits it)
//   - The required-field set matches the non-omitempty fields exactly
//   - A tag change causes the golden diff to fail loudly
//
// What the tag surface CANNOT express (enforced in validate.go instead):
//   - Hex patterns on numeric/address fields (the jsonschema tag is description-only
//     in github.com/google/jsonschema-go v0.4.3; "WORD=" prefixes are reserved for
//     future use and currently rejected)
//   - maxLength on the data field (512 KiB bytes → 524,290 hex chars incl. 0x prefix)
//
// To regenerate: go test ./internal/signing/... -run TestGoldenSchema -update
func TestGoldenSchema_TxRequest(t *testing.T) {
	schema, err := jsonschema.For[signing.TxRequest](nil)
	if err != nil {
		t.Fatalf("jsonschema.For[TxRequest]: %v", err)
	}

	goldenPath := filepath.Join(schemaTestDataDir, "sign_transaction.golden.json")
	compareOrUpdateGolden(t, schema, goldenPath)

	// Structural assertions on the live schema (independent of the golden byte-compare).
	assertTxRequestSchema(t, schema)
}

// TestGoldenSchema_AddressResult pins the JSON schema inferred from signing.AddressResult.
//
// To regenerate: go test ./internal/signing/... -run TestGoldenSchema -update
func TestGoldenSchema_AddressResult(t *testing.T) {
	schema, err := jsonschema.For[signing.AddressResult](nil)
	if err != nil {
		t.Fatalf("jsonschema.For[AddressResult]: %v", err)
	}

	goldenPath := filepath.Join(schemaTestDataDir, "get_address_result.golden.json")
	compareOrUpdateGolden(t, schema, goldenPath)

	// Structural assertions on the live schema.
	assertAddressResultSchema(t, schema)
}

// TestSignResult_MarshalAllKeysPresent verifies that marshalling a zero-value
// signing.SignResult produces JSON with rawTransaction, signature (r, s, v),
// hash, and from — all keys always present.
//
// The locked decision (Issue 2.3 / Phase Assumption): SignResult ships hash and
// from from day one with NO omitempty on those fields; no later retrofit needed.
func TestSignResult_MarshalAllKeysPresent(t *testing.T) {
	result := signing.SignResult{} // zero value
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(SignResult{}): %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	requiredKeys := []string{"rawTransaction", "signature", "hash", "from"}
	for _, key := range requiredKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("key %q missing from SignResult JSON; got keys: %v", key, jsonKeys(m))
		}
	}

	// Check signature sub-keys.
	var sig map[string]json.RawMessage
	if err := json.Unmarshal(m["signature"], &sig); err != nil {
		t.Fatalf("json.Unmarshal signature: %v", err)
	}
	for _, key := range []string{"r", "s", "v"} {
		if _, ok := sig[key]; !ok {
			t.Errorf("signature sub-key %q missing; got keys: %v", key, jsonKeys(sig))
		}
	}
}

// TestSignResult_NoOmitemptyOnHashAndFrom verifies that zero-value hash and from
// fields are marshalled (i.e., both are present even when empty).
func TestSignResult_NoOmitemptyOnHashAndFrom(t *testing.T) {
	result := signing.SignResult{
		RawTransaction: "0xdeadbeef",
		Hash:           "", // explicitly empty
		From:           "", // explicitly empty
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, key := range []string{"hash", "from"} {
		raw, ok := m[key]
		if !ok {
			t.Errorf("key %q absent from JSON (should always be present — no omitempty)", key)
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Errorf("key %q: unexpected type in JSON: %s", key, raw)
		}
		if s != "" {
			t.Errorf("key %q: expected empty string, got %q", key, s)
		}
	}
}

// TestAddressResult_Marshal verifies that AddressResult marshals with the
// correct JSON key "address".
func TestAddressResult_Marshal(t *testing.T) {
	ar := signing.AddressResult{Address: "0xAbCd"}
	data, err := json.Marshal(ar)
	if err != nil {
		t.Fatalf("json.Marshal(AddressResult): %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if v, ok := m["address"]; !ok {
		t.Error(`key "address" missing from AddressResult JSON`)
	} else if v != "0xAbCd" {
		t.Errorf(`"address": got %q, want "0xAbCd"`, v)
	}
}

// assertTxRequestSchema performs structural assertions on the live inferred schema,
// independently of the golden byte comparison. These assertions fail loudly if the
// schema's structure changes in a way that breaks the wire contract.
func assertTxRequestSchema(t *testing.T, schema *jsonschema.Schema) {
	t.Helper()

	// additionalProperties must be false (not absent, not true).
	// jsonschema-go v0.4.3 emits {"not":{}} (serialized as JSON false) for structs.
	if schema.AdditionalProperties == nil {
		t.Error("TxRequest schema: additionalProperties is absent; expected false (strict schema)")
	} else {
		// The false schema is {"not":{}}, marshalled as the JSON boolean false.
		ap, err := json.Marshal(schema.AdditionalProperties)
		if err != nil {
			t.Fatalf("marshal additionalProperties: %v", err)
		}
		if string(ap) != "false" {
			t.Errorf("TxRequest schema: additionalProperties: got %s, want false", ap)
		}
	}

	// Required fields: non-omitempty fields in TxRequest.
	// type, chainId, nonce, value, data, gas must be required.
	// to, gasPrice, maxFeePerGas, maxPriorityFeePerGas, accessList must NOT be required.
	wantRequired := map[string]bool{
		"type":    true,
		"chainId": true,
		"nonce":   true,
		"value":   true,
		"data":    true,
		"gas":     true,
	}
	wantOptional := map[string]bool{
		"to":                   true,
		"gasPrice":             true,
		"maxFeePerGas":         true,
		"maxPriorityFeePerGas": true,
		"accessList":           true,
	}

	gotRequired := make(map[string]bool, len(schema.Required))
	for _, f := range schema.Required {
		gotRequired[f] = true
	}
	for field := range wantRequired {
		if !gotRequired[field] {
			t.Errorf("TxRequest schema: required field %q is missing from required array", field)
		}
	}
	for field := range wantOptional {
		if gotRequired[field] {
			t.Errorf("TxRequest schema: optional field %q is incorrectly in the required array", field)
		}
	}

	// All expected properties must be present.
	for field := range wantRequired {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("TxRequest schema: required field %q not in properties", field)
		}
	}
	for field := range wantOptional {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("TxRequest schema: optional field %q not in properties", field)
		}
	}

	// NOTE: Hex patterns (e.g. "^0x[0-9a-fA-F]*$") and maxLength for the data
	// field (524,290 chars) are NOT present in the inferred schema because the
	// jsonschema tag in google/jsonschema-go v0.4.3 is description-only (it does
	// not support WORD= key=value annotations). These constraints are enforced
	// exclusively in validate.go (Issue 2.4). The end-to-end schema strictness
	// is re-asserted in 2.7/2.11 via tools/list assertions.
}

// assertAddressResultSchema performs structural assertions on the inferred
// AddressResult schema.
func assertAddressResultSchema(t *testing.T, schema *jsonschema.Schema) {
	t.Helper()

	if schema.AdditionalProperties == nil {
		t.Error("AddressResult schema: additionalProperties is absent; expected false")
	} else {
		ap, err := json.Marshal(schema.AdditionalProperties)
		if err != nil {
			t.Fatalf("marshal additionalProperties: %v", err)
		}
		if string(ap) != "false" {
			t.Errorf("AddressResult schema: additionalProperties: got %s, want false", ap)
		}
	}

	if _, ok := schema.Properties["address"]; !ok {
		t.Error(`AddressResult schema: property "address" not found`)
	}

	// "address" must be required.
	found := false
	for _, r := range schema.Required {
		if r == "address" {
			found = true
			break
		}
	}
	if !found {
		t.Error(`AddressResult schema: "address" must be in required array`)
	}
}

// compareOrUpdateGolden either compares schema against the golden file or
// rewrites the golden file (when -update is passed).
func compareOrUpdateGolden(t *testing.T, schema *jsonschema.Schema, goldenPath string) {
	t.Helper()

	got, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent schema: %v", err)
	}
	got = append(got, '\n') // trailing newline for clean diffs

	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0750); err != nil {
			t.Fatalf("MkdirAll %s: %v", filepath.Dir(goldenPath), err)
		}
		if err := os.WriteFile(goldenPath, got, 0600); err != nil {
			t.Fatalf("WriteFile %s: %v", goldenPath, err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v (run with -update to create)", goldenPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("schema golden mismatch for %s:\ngot:\n%s\nwant:\n%s\n(run with -update to regenerate)",
			goldenPath, got, want)
	}
}

// jsonKeys returns the sorted list of keys from a JSON object map, for use in
// error messages.
func jsonKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
