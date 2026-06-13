package obs

import (
	"fmt"
	"runtime/debug"
)

const unknownValue = "<unknown>"

// readBuildInfo is an indirection over debug.ReadBuildInfo so tests can exercise
// the ok==false fallback and the vcs.* settings arms (which are never populated
// under `go test`). Production code never reassigns it.
var readBuildInfo = debug.ReadBuildInfo

// Info holds the four fields exposed on --version.
// Every field that cannot be determined is set to the literal "<unknown>".
type Info struct {
	Version   string // module version from debug.BuildInfo.Main.Version
	Commit    string // VCS revision from the vcs.revision build setting
	Date      string // VCS commit time from the vcs.time build setting
	GoVersion string // Go toolchain version from debug.BuildInfo.GoVersion
}

// String returns a one-line human-readable version string suitable for
// assignment to cmd.Version in urfave/cli v3. urfave/cli v3's DefaultPrintVersion
// prints "{cmd.Name} version {cmd.Version}", so String() does NOT include the
// binary name — the caller provides it automatically via cmd.Name.
//
// All four fields are included; <unknown> appears for any field that could not
// be determined at build time.
//
// Example output (after urfave/cli adds "eth-signer-mcp version "):
//
//	eth-signer-mcp version v1.2.3 (commit abc1234, built 2025-01-01T00:00:00Z, go1.22.0)
func (i Info) String() string {
	return fmt.Sprintf("%s (commit %s, built %s, %s)",
		i.Version, i.Commit, i.Date, i.GoVersion)
}

// Build reads runtime/debug.ReadBuildInfo and populates an Info value.
//
// Field derivation:
//   - Version   — debug.BuildInfo.Main.Version; "<unknown>" when ok==false or
//     empty. The Go toolchain placeholder "(devel)" is passed through unchanged
//     to indicate an untagged development build — it is informative, not
//     undeterminable, so it is NOT replaced with "<unknown>".
//   - Commit    — vcs.revision build setting; "<unknown>" when absent or empty.
//   - Date      — vcs.time build setting; "<unknown>" when absent or empty.
//   - GoVersion — debug.BuildInfo.GoVersion; "<unknown>" when ok==false or empty.
//
// go test binaries carry no VCS stamping; Commit and Date will be "<unknown>"
// in that environment. GoVersion is always available from the toolchain.
func Build() Info {
	info, ok := readBuildInfo()
	if !ok {
		return Info{
			Version:   unknownValue,
			Commit:    unknownValue,
			Date:      unknownValue,
			GoVersion: unknownValue,
		}
	}

	result := Info{
		GoVersion: nonEmpty(info.GoVersion),
		Version:   nonEmpty(info.Main.Version),
	}

	// Walk build settings for VCS fields.
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			result.Commit = nonEmpty(s.Value)
		case "vcs.time":
			result.Date = nonEmpty(s.Value)
		}
	}

	// Fill any fields not yet set by the settings walk.
	if result.Commit == "" {
		result.Commit = unknownValue
	}
	if result.Date == "" {
		result.Date = unknownValue
	}

	return result
}

// nonEmpty returns s if it is non-empty, otherwise unknownValue.
func nonEmpty(s string) string {
	if s == "" {
		return unknownValue
	}
	return s
}
