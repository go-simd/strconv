//go:build amd64

package strconv

import (
	stdconv "strconv"
	"testing"
)

// TestParse8SSEKernel drives the SSE 8-digit kernel directly over every clean
// 8-digit value pattern of interest, plus a non-digit byte injected at each of
// the eight positions, asserting (val, ok) matches an independent computation.
// Rosetta executes SSE, so this exercises the real kernel on the dev machine.
func TestParse8SSEKernel(t *testing.T) {
	clean := []string{
		"00000000", "00000001", "00000009", "12345678", "99999999",
		"10000000", "01234567", "87654321", "00000010", "90000000",
	}
	for _, s := range clean {
		// Pad to >=8 with trailing digits so the kernel's 8-byte load is in
		// bounds; we only assert the first group.
		buf := []byte(s + "00000000")
		val, ok := parse8SSE(&buf[0])
		if ok != 1 {
			t.Fatalf("parse8SSE(%q) ok=%d, want 1", s, ok)
		}
		want, _ := stdconv.ParseUint(s, 10, 64)
		if val != want {
			t.Fatalf("parse8SSE(%q)=%d want %d", s, val, want)
		}
	}

	// Non-digit at every position must yield ok==0.
	bad := []byte{'/', ':', '_', ' ', 'a', 'A', '.', 0x00, 0xff}
	for _, bc := range bad {
		for off := 0; off < 8; off++ {
			b := []byte("1234567800000000")
			b[off] = bc
			if _, ok := parse8SSE(&b[0]); ok != 0 {
				t.Fatalf("parse8SSE with %q at %d: ok=%d want 0", bc, off, ok)
			}
		}
	}
}

// TestParseDigitsSIMDGroups checks parseDigitsSIMD across lengths that span
// multiple 8-digit groups plus a scalar tail, against the standard library.
func TestParseDigitsSIMDGroups(t *testing.T) {
	for n := 1; n <= maxFastDigits; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('0' + ((i*3 + 1) % 10))
		}
		s := string(b)
		v, ok := parseDigitsSIMD(s)
		if !ok {
			t.Fatalf("parseDigitsSIMD(%q) ok=false", s)
		}
		want, err := stdconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatalf("stdlib rejected %q: %v", s, err)
		}
		if v != want {
			t.Fatalf("parseDigitsSIMD(%q)=%d want %d", s, v, want)
		}
		// Inject a bad byte in the second group (offset 8..) to confirm the
		// kernel's ok==0 path routes through the scalar fallback to ok=false.
		if n >= 9 {
			bb := append([]byte(nil), b...)
			bb[8] = 'x'
			if _, ok := parseDigitsSIMD(string(bb)); ok {
				t.Fatalf("parseDigitsSIMD with bad byte at 8 returned ok=true")
			}
		}
	}
}
