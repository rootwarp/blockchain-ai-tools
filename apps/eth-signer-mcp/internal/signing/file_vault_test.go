// Tests for NewFileKeyVault constructor — Issue 2.2.
// These are INTERNAL tests (package signing) to allow type assertions into
// unexported types during the captured-pointer zeroing technique in decrypt_test.go.
package signing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// testdataDir returns the absolute path to the signing testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata")
}

// testdataFile returns the path to a named file inside the signing testdata dir.
func testdataFile(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(testdataDir(t), name)
}

// TestNewFileKeyVault_WeakKeystore verifies that NewFileKeyVault succeeds against
// the weak (n=2) fixture and returns the documented address.
// No password read or DecryptKey call occurs at construction time (proven by using
// a non-existent password file — the vault must still be created successfully).
func TestNewFileKeyVault_WeakKeystore(t *testing.T) {
	t.Parallel()

	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: "/nonexistent/password-file-should-not-be-read",
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(weak): unexpected error: %v", err)
	}
	if vault == nil {
		t.Fatal("NewFileKeyVault(weak): returned nil vault")
	}

	got := vault.Address().Hex()
	if got != FixtureTestAddress {
		t.Errorf("Address() = %q, want %q", got, FixtureTestAddress)
	}
}

// TestNewFileKeyVault_LightKeystore verifies that NewFileKeyVault succeeds against
// the light-scrypt (N=4096) fixture and returns the documented address.
func TestNewFileKeyVault_LightKeystore(t *testing.T) {
	t.Parallel()

	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-light.json"),
		PasswordPath: "/nonexistent/password-file-should-not-be-read",
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(light): unexpected error: %v", err)
	}
	got := vault.Address().Hex()
	if got != FixtureTestAddress {
		t.Errorf("Address() = %q, want %q", got, FixtureTestAddress)
	}
}

// TestNewFileKeyVault_StandardKeystore verifies that NewFileKeyVault succeeds against
// the standard-scrypt (N=262144) fixture.
// Skipped under -short because the fixture is large (only address parse is needed,
// but the full JSON is read into memory).
func TestNewFileKeyVault_StandardKeystore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standard-scrypt vault construction under -short")
	}
	t.Parallel()

	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-standard.json"),
		PasswordPath: "/nonexistent/password-file-should-not-be-read",
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(standard): unexpected error: %v", err)
	}
	got := vault.Address().Hex()
	if got != FixtureTestAddress {
		t.Errorf("Address() = %q, want %q", got, FixtureTestAddress)
	}
}

// TestNewFileKeyVault_ConstructorDoesNotReadPassword proves that the constructor
// does NOT read the password file. A vault constructed with a nonexistent password
// path must succeed (the path is only used inside WithSigningKey).
func TestNewFileKeyVault_ConstructorDoesNotReadPassword(t *testing.T) {
	t.Parallel()

	_, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: "/does/not/exist/password.txt",
	})
	if err != nil {
		t.Errorf("NewFileKeyVault: constructor read password file (unexpected error): %v", err)
	}
}

// TestNewFileKeyVault_NoAddressKeystore verifies that a keystore with the
// top-level "address" field absent now succeeds (per spec the field is optional).
// Initial Address() must be the zero address; discovery occurs on first sign.
func TestNewFileKeyVault_NoAddressKeystore(t *testing.T) {
	t.Parallel()

	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-no-address.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(no-address): unexpected error: %v", err)
	}
	if vault == nil {
		t.Fatal("NewFileKeyVault(no-address): returned nil vault")
	}
	if got := vault.Address().Hex(); got != "0x0000000000000000000000000000000000000000" {
		t.Errorf("Address() = %q, want zero address", got)
	}
}

// TestNewFileKeyVault_EmptyAddressKeystore verifies that a keystore with the
// top-level "address" field present but empty now succeeds (per spec optional).
// Initial Address() must be the zero address.
func TestNewFileKeyVault_EmptyAddressKeystore(t *testing.T) {
	t.Parallel()

	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-empty-address.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(empty-address): unexpected error: %v", err)
	}
	if vault == nil {
		t.Fatal("NewFileKeyVault(empty-address): returned nil vault")
	}
	if got := vault.Address().Hex(); got != "0x0000000000000000000000000000000000000000" {
		t.Errorf("Address() = %q, want zero address", got)
	}
}

// TestNewFileKeyVault_MissingFile verifies that a nonexistent keystore path
// returns a *ToolError{Code: CodeKeystoreError}.
func TestNewFileKeyVault_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := NewFileKeyVault(VaultOptions{
		KeystorePath: "/nonexistent/keystore.json",
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err == nil {
		t.Fatal("NewFileKeyVault(missing): expected error, got nil")
	}

	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("NewFileKeyVault(missing): error type = %T, want *ToolError", err)
	}
	if te.Code != CodeKeystoreError {
		t.Errorf("NewFileKeyVault(missing): Code = %q, want %q", te.Code, CodeKeystoreError)
	}
}

// TestAddressPointer_NoAddressKeystore verifies that AddressPointer() returns nil
// for a keystore with no top-level "address" field (pre-discovery state).
func TestAddressPointer_NoAddressKeystore(t *testing.T) {
	t.Parallel()

	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-no-address.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(no-address): unexpected error: %v", err)
	}
	if ptr := vault.AddressPointer(); ptr != nil {
		t.Errorf("AddressPointer() = %v; want nil for no-address keystore", ptr)
	}
	if got := vault.Address(); got != (common.Address{}) {
		t.Errorf("Address() = %q; want zero address for no-address keystore", got.Hex())
	}
}

// TestAddressPointer_LightKeystore verifies that AddressPointer() returns a non-nil
// pointer for a keystore with a declared top-level "address" field, and that the
// pointed-to value equals Address().
func TestAddressPointer_LightKeystore(t *testing.T) {
	t.Parallel()

	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-light.json"),
		PasswordPath: "/nonexistent/password-file-should-not-be-read",
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(light): unexpected error: %v", err)
	}
	ptr := vault.AddressPointer()
	if ptr == nil {
		t.Fatal("AddressPointer() = nil; want non-nil for keystore with declared address")
	}
	if *ptr != vault.Address() {
		t.Errorf("*AddressPointer() = %q; want %q (= Address())", ptr.Hex(), vault.Address().Hex())
	}
}

// TestValidateKeystoreAddressField_Matrix is a table-driven test for boot-time
// address validation. It covers accept cases (keystores that should load) and
// reject cases (keystores that should fail with CodeKeystoreError).
func TestValidateKeystoreAddressField_Matrix(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		fixture string
		wantErr bool
	}

	cases := []testCase{
		// ── Accept cases ──────────────────────────────────────────────────
		{name: "eip55-mixed-case (light)", fixture: "keystore-light.json", wantErr: false},
		{name: "eip55-mixed-case (weak)", fixture: "keystore-weak.json", wantErr: false},
		{name: "all-uppercase", fixture: "keystore-uppercase-address.json", wantErr: false},
		{name: "empty-address", fixture: "keystore-empty-address.json", wantErr: false},
		{name: "no-address", fixture: "keystore-no-address.json", wantErr: false},
		// ── Reject cases ──────────────────────────────────────────────────
		{name: "short-address", fixture: "keystore-malformed-address-short.json", wantErr: true},
		{name: "nonhex-address", fixture: "keystore-malformed-address-nonhex.json", wantErr: true},
		{name: "wrong-prefix-address", fixture: "keystore-malformed-address-no-prefix.json", wantErr: true},
		{name: "wrong-checksum-mixed-case", fixture: "keystore-malformed-address-checksum.json", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewFileKeyVault(VaultOptions{
				KeystorePath: testdataFile(t, tc.fixture),
				PasswordPath: "/nonexistent/password-file-should-not-be-read",
			})

			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewFileKeyVault(%q): expected error, got nil", tc.fixture)
				}
				te, ok := err.(*ToolError)
				if !ok {
					t.Fatalf("NewFileKeyVault(%q): error type = %T, want *ToolError", tc.fixture, err)
				}
				if te.Code != CodeKeystoreError {
					t.Errorf("NewFileKeyVault(%q): Code = %q, want %q", tc.fixture, te.Code, CodeKeystoreError)
				}
			} else {
				if err != nil {
					t.Fatalf("NewFileKeyVault(%q): unexpected error: %v", tc.fixture, err)
				}
			}
		})
	}
}

// TestNewFileKeyVault_MalformedJSON verifies that a keystore file with invalid JSON
// returns a *ToolError{Code: CodeKeystoreError}.
func TestNewFileKeyVault_MalformedJSON(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "bad-keystore-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString("{not valid json}"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_ = f.Close()

	_, err = NewFileKeyVault(VaultOptions{
		KeystorePath: f.Name(),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err == nil {
		t.Fatal("NewFileKeyVault(bad-json): expected error, got nil")
	}

	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("NewFileKeyVault(bad-json): error type = %T, want *ToolError", err)
	}
	if te.Code != CodeKeystoreError {
		t.Errorf("NewFileKeyVault(bad-json): Code = %q, want %q", te.Code, CodeKeystoreError)
	}
}

// TestNewFileKeyVault_UnreadableFile verifies that an unreadable keystore file
// returns a *ToolError{Code: CodeKeystoreError}.
func TestNewFileKeyVault_UnreadableFile(t *testing.T) {
	t.Parallel()

	// Create a temp file and then remove its read permission.
	f, err := os.CreateTemp(t.TempDir(), "unreadable-keystore-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod 000: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) }) // restore for cleanup

	// Skip if running as root (chmod 000 has no effect on root).
	if os.Getuid() == 0 {
		t.Skip("skipping unreadable-file test: running as root")
	}

	_, err = NewFileKeyVault(VaultOptions{
		KeystorePath: path,
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err == nil {
		t.Fatal("NewFileKeyVault(unreadable): expected error, got nil")
	}

	te, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("NewFileKeyVault(unreadable): error type = %T, want *ToolError", err)
	}
	if te.Code != CodeKeystoreError {
		t.Errorf("NewFileKeyVault(unreadable): Code = %q, want %q", te.Code, CodeKeystoreError)
	}
}
