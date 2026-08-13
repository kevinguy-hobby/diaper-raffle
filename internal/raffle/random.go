package raffle

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
)

// SerialLength is the number of digits printed on a ticket stub.
const SerialLength = 6

// CryptoFloat returns a uniform float64 in (0,1) from the system CSPRNG.
//
// The real draw uses this. The odds simulation does not need to: it is an
// estimate shown in a table, not a decision about who takes a prize home.
func CryptoFloat() float64 {
	var buf [8]byte
	for {
		mustRead(buf[:])
		// The top 53 bits give every float64 representable in [0,1) an equal
		// footing; anything finer than that cannot be represented anyway.
		v := float64(binary.BigEndian.Uint64(buf[:])>>11) / (1 << 53)
		if v > 0 {
			return v
		}
	}
}

// Serial makes a ticket number for a stub. It is decoration, not identity —
// the database id is what a winner is actually keyed on — but it should still
// look like it came off a real roll of tickets.
func Serial() string {
	var buf [SerialLength]byte
	var b strings.Builder
	b.Grow(SerialLength)

	// Rejection sampling: 256 is not a multiple of 10, so taking a byte modulo
	// 10 would make the digits 0 to 5 slightly more likely than 6 to 9.
	for b.Len() < SerialLength {
		mustRead(buf[:])
		for _, v := range buf {
			if v >= 250 {
				continue
			}
			b.WriteByte('0' + v%10)
			if b.Len() == SerialLength {
				break
			}
		}
	}
	return b.String()
}

// Serials makes n distinct ticket numbers, so no two stubs in the same draw
// print the same one.
func Serials(n int) []string {
	seen := make(map[string]bool, n)
	out := make([]string, 0, n)
	for len(out) < n {
		s := Serial()
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// mustRead fills buf with system randomness. crypto/rand does not fail on a
// supported platform, and if it ever did there is no safe fallback for
// picking a winner — better to stop than to hand out a prize badly.
func mustRead(buf []byte) {
	if _, err := rand.Read(buf); err != nil {
		panic("raffle: system randomness unavailable: " + err.Error())
	}
}
