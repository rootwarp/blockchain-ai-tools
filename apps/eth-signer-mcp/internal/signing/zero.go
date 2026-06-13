package signing

import (
	"math/big"
	"runtime"
)

// ZeroBytes overwrites every byte of b with zero and then calls
// runtime.KeepAlive(b) to prevent the compiler from optimising away the clear.
//
// ADR-009 best-effort limitation: Go's garbage collector may retain transient
// copies of the slice data created by GC moves or stack-to-heap escapes. This
// zeroing is therefore best-effort. The observable and test-enforced
// requirement is "no secrets in logs or outputs, raw or encoded" — not
// guaranteed in-memory erasure. Do not rely on ZeroBytes as a defence against
// a root/kernel-level adversary who can read process memory.
func ZeroBytes(b []byte) {
	clear(b)
	runtime.KeepAlive(b)
}

// ZeroBigInt overwrites n's magnitude with zero and normalises n so that
// n.BitLen() == 0, then calls runtime.KeepAlive(n) to prevent the clear from
// being optimised away.
//
// Implementation note: clear(n.Bits()) zeroes the backing word slice (the secret
// magnitude) in place, but big.Int does NOT re-normalise after that — BitLen()
// would still report (len(words)-1)*wordSize bits (e.g. 192 for a 32-byte key
// scalar) because it derives the length from the slice length, not the values.
// The follow-up n.SetInt64(0) reslices the (already-zeroed) backing array to
// length 0, so the secret words stay zeroed AND BitLen() becomes 0.
//
// ADR-009 best-effort limitation: the Go runtime may have retained copies of
// the integer's bits in transient allocations (GC moves, stack copies). This is
// the same approach used by go-ethereum's key-zeroing code. The test-enforced
// requirement is "no secrets in logs or outputs" — not guaranteed in-memory
// erasure against a privileged adversary.
func ZeroBigInt(n *big.Int) {
	if n == nil {
		return
	}
	clear(n.Bits()) // zero the secret magnitude in the backing word slice
	n.SetInt64(0)   // re-normalise: reslices the now-zeroed backing array to len 0
	runtime.KeepAlive(n)
}
