//go:build !race

package signing

// raceDetectorEnabled is false when the test binary is compiled without -race.
const raceDetectorEnabled = false
