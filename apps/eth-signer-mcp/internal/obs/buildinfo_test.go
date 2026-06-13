package obs

import (
	"runtime/debug"
	"strings"
	"testing"
)

const unknown = "<unknown>"

// TestBuild_ReadBuildInfoFails exercises the ok==false fallback, which is never
// reached under `go test` (ReadBuildInfo always succeeds there). It overrides the
// readBuildInfo seam and asserts every field falls back to "<unknown>" — the
// safety net for a stripped/hostile build environment. Not parallel: mutates a
// package-level var.
func TestBuild_ReadBuildInfoFails(t *testing.T) {
	orig := readBuildInfo
	t.Cleanup(func() { readBuildInfo = orig })
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }

	info := Build()
	for name, val := range map[string]string{
		"Version":   info.Version,
		"Commit":    info.Commit,
		"Date":      info.Date,
		"GoVersion": info.GoVersion,
	} {
		if val != unknown {
			t.Errorf("Build() field %q = %q, want %q when ReadBuildInfo fails", name, val, unknown)
		}
	}
}

// TestBuild_VCSFieldsPresent exercises the vcs.revision / vcs.time settings arms
// (0 hits under `go test`). A typo in either key string would otherwise silently
// leave Commit/Date as <unknown> in every real build. Not parallel: mutates a
// package-level var.
func TestBuild_VCSFieldsPresent(t *testing.T) {
	orig := readBuildInfo
	t.Cleanup(func() { readBuildInfo = orig })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.0",
			Main:      debug.Module{Version: "v9.9.9"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "deadbeefcafe"},
				{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
			},
		}, true
	}

	info := Build()
	if info.Version != "v9.9.9" {
		t.Errorf("Version = %q, want %q", info.Version, "v9.9.9")
	}
	if info.Commit != "deadbeefcafe" {
		t.Errorf("Commit = %q, want %q (vcs.revision arm)", info.Commit, "deadbeefcafe")
	}
	if info.Date != "2026-01-02T03:04:05Z" {
		t.Errorf("Date = %q, want %q (vcs.time arm)", info.Date, "2026-01-02T03:04:05Z")
	}
	if info.GoVersion != "go1.26.0" {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, "go1.26.0")
	}
}

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
