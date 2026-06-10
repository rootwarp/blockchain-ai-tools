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

// ZeroBigInt sets n's magnitude to zero by clearing its internal word slice,
// then calls runtime.KeepAlive(n) to prevent the clear from being optimised away.
// After ZeroBigInt, n.BitLen() == 0.
//
// ADR-009 best-effort limitation: the Go runtime may have retained copies of
// the integer's bits in transient allocations (GC moves, stack copies). This is
// the same approach used by go-ethereum's key-zeroing code. The test-enforced
// requirement is "no secrets in logs or outputs" — not guaranteed in-memory
// erasure against a privileged adversary.
func ZeroBigInt(n *big.Int) {
	clear(n.Bits())
	runtime.KeepAlive(n)
}
