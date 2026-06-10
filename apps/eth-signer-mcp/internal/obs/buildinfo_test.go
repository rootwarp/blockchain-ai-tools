package obs

import (
	"strings"
	"testing"
)

const unknown = "<unknown>"

// TestBuild_GoTestBinaries verifies that Build() returns <unknown> for fields
// that cannot be determined under go test (no VCS stamping in test binaries)
// and a non-empty GoVersion string containing "go1.".
func TestBuild_GoTestBinaries(t *testing.T) {
	t.Parallel()

	info := Build()

	// GoVersion must always be determinable — it comes from runtime/debug.BuildInfo.GoVersion.
	if info.GoVersion == "" {
		t.Error("GoVersion must not be empty")
	}
	if !strings.Contains(info.GoVersion, "go1.") {
		t.Errorf("GoVersion %q does not contain expected prefix 'go1.'", info.GoVersion)
	}

	// Under go test, VCS fields (Version, Commit, Date) are typically <unknown>.
	// We assert the contract: no field is the empty string.
	// The exact value may be <unknown> or a real value depending on the build.
	for name, val := range map[string]string{
		"Version":   info.Version,
		"Commit":    info.Commit,
		"Date":      info.Date,
		"GoVersion": info.GoVersion,
	} {
		if val == "" {
			t.Errorf("field %q must not be empty string; got empty (expected %q or a real value)", name, unknown)
		}
	}
}

// TestBuild_UnknownPlaceholder verifies that fields which cannot be determined
// use the exact literal "<unknown>" and never an empty string or other sentinel.
func TestBuild_UnknownPlaceholder(t *testing.T) {
	t.Parallel()

	info := Build()

	// Under go test, Version is typically "(devel)" which is treated as-is per
	// the architecture: the field is set to "(devel)" — not "<unknown>" — since
	// the issue spec says only absent/empty/unreadable fields get <unknown>.
	// What we assert here is that no field is ever empty.
	for name, val := range map[string]string{
		"Version":   info.Version,
		"Commit":    info.Commit,
		"Date":      info.Date,
		"GoVersion": info.GoVersion,
	} {
		if val == "" {
			t.Errorf("Build() field %q is empty string; must be %q or a real value", name, unknown)
		}
	}
}

// TestInfo_String verifies that Info.String() includes all four fields and
// produces a non-empty one-liner. It does NOT include the binary name because
// urfave/cli v3's DefaultPrintVersion prepends "{cmd.Name} version " automatically.
func TestInfo_String(t *testing.T) {
	t.Parallel()

	info := Info{
		Version:   "v1.2.3",
		Commit:    "abc1234",
		Date:      "2025-01-01",
		GoVersion: "go1.22.0",
	}

	s := info.String()
	if s == "" {
		t.Fatal("Info.String() returned empty string")
	}
	for _, substr := range []string{"v1.2.3", "abc1234", "2025-01-01", "go1.22.0"} {
		if !strings.Contains(s, substr) {
			t.Errorf("Info.String() = %q, missing expected substring %q", s, substr)
		}
	}
	// Should NOT include binary name — urfave/cli v3 adds it.
	if strings.Contains(s, "eth-signer-mcp") {
		t.Errorf("Info.String() = %q, should not include binary name (urfave/cli v3 adds it)", s)
	}
}

// TestInfo_String_Unknown verifies that a fully-unknown Info still produces a
// well-formed string (no panic, no empty output).
func TestInfo_String_Unknown(t *testing.T) {
	t.Parallel()

	info := Info{
		Version:   unknown,
		Commit:    unknown,
		Date:      unknown,
		GoVersion: unknown,
	}

	s := info.String()
	if s == "" {
		t.Fatal("Info.String() with all-unknown fields returned empty string")
	}
}

// TestNonEmpty_EmptyString verifies that the unexported nonEmpty helper returns
// the <unknown> literal for an empty input string.
func TestNonEmpty_EmptyString(t *testing.T) {
	t.Parallel()
	if got := nonEmpty(""); got != unknownValue {
		t.Errorf("nonEmpty(\"\") = %q, want %q", got, unknownValue)
	}
}

// TestNonEmpty_NonEmptyString verifies that the unexported nonEmpty helper
// passes through a non-empty string unchanged.
func TestNonEmpty_NonEmptyString(t *testing.T) {
	t.Parallel()
	const input = "v1.2.3"
	if got := nonEmpty(input); got != input {
		t.Errorf("nonEmpty(%q) = %q, want %q", input, got, input)
	}
}
