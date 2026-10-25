package pairing

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Code is the 6-digit code both sides of a tap pairing show. It depends only
// on the pair of certificate fingerprints, in either order, so a man in the
// middle, who has to present his own certificate to each side, makes the two
// screens show different codes.
func Code(fpA, fpB string) string {
	if fpA > fpB {
		fpA, fpB = fpB, fpA
	}
	sum := sha256.Sum256([]byte("adrop-pair-code\x00" + fpA + "\x00" + fpB))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[:4])%1_000_000)
}
