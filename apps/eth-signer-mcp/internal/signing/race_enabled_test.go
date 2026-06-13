//go:build race

package signing

// raceDetectorEnabled is set to true when the test binary is compiled with -race.
// Timing benchmarks (TestSigner_NonKDFOverhead_*) skip themselves under the race
// detector because the detector instruments every memory access, inflating all
// operation timings by 5–10x and making the < 10 ms non-KDF overhead assertion
// meaningless. The race-detector correctness is verified by the concurrency tests.
const raceDetectorEnabled = true
