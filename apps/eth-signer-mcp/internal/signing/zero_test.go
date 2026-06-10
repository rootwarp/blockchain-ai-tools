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

// TestZeroBigInt_Zero verifies ZeroBigInt does not panic on a zero big.Int.
func TestZeroBigInt_Zero(t *testing.T) {
	t.Parallel()
	n := new(big.Int)     // zero
	signing.ZeroBigInt(n) // must not panic
}
