package signing_test

import (
	"math/big"
	"testing"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// TestZeroBytes verifies that after ZeroBytes every byte of the slice is 0.
func TestZeroBytes(t *testing.T) {
	t.Parallel()
	b := []byte{0x01, 0x02, 0xFF, 0xAB, 0x00}
	signing.ZeroBytes(b)
	for i, v := range b {
		if v != 0 {
			t.Errorf("ZeroBytes: b[%d] = %#x, want 0x00", i, v)
		}
	}
}

// TestZeroBytes_Empty verifies ZeroBytes does not panic on an empty slice.
func TestZeroBytes_Empty(t *testing.T) {
	t.Parallel()
	var b []byte
	signing.ZeroBytes(b) // must not panic
}

// TestZeroBigInt verifies that after ZeroBigInt n.BitLen() == 0.
func TestZeroBigInt(t *testing.T) {
	t.Parallel()
	n := new(big.Int).SetBytes([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x23})
	if n.BitLen() == 0 {
		t.Fatal("pre-condition: big.Int should be non-zero before ZeroBigInt")
	}
	signing.ZeroBigInt(n)
	if n.BitLen() != 0 {
		t.Errorf("ZeroBigInt: n.BitLen() = %d, want 0", n.BitLen())
	}
}

// TestZeroBigInt_MultiWord is the regression test for the multi-word zeroing
// bug: clear(n.Bits()) zeroes the backing words but leaves the slice length
// unchanged, so BitLen() would report (words-1)*wordSize bits — 192 for a
// 32-byte (4-word, on 64-bit) scalar — unless the int is re-normalised. This is
// the exact shape of a secp256k1 private-key scalar, so getting it right is
// security-critical for Phase 2.
func TestZeroBigInt_MultiWord(t *testing.T) {
	t.Parallel()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = 0xFF // a full 256-bit magnitude → 4 words on a 64-bit build
	}
	n := new(big.Int).SetBytes(raw)
	if n.BitLen() != 256 {
		t.Fatalf("pre-condition: BitLen() = %d, want 256", n.BitLen())
	}
	signing.ZeroBigInt(n)
	if n.BitLen() != 0 {
		t.Errorf("ZeroBigInt(32-byte): BitLen() = %d, want 0 (re-normalisation bug)", n.BitLen())
	}
	if n.Sign() != 0 {
		t.Errorf("ZeroBigInt(32-byte): Sign() = %d, want 0", n.Sign())
	}
	if len(n.Bytes()) != 0 {
		t.Errorf("ZeroBigInt(32-byte): Bytes() length = %d, want 0", len(n.Bytes()))
	}
}

// TestZeroBigInt_Zero verifies ZeroBigInt does not panic on a zero big.Int.
func TestZeroBigInt_Zero(t *testing.T) {
	t.Parallel()
	n := new(big.Int)     // zero
	signing.ZeroBigInt(n) // must not panic
}

// TestZeroBigInt_Nil verifies ZeroBigInt does not panic on a nil pointer.
func TestZeroBigInt_Nil(t *testing.T) {
	t.Parallel()
	signing.ZeroBigInt(nil) // must not panic
}
