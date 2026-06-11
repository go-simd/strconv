//go:build amd64

package strconv

import (
	stdconv "strconv"
	"testing"
)

// TestParse16SSEKernel drives the SSE 16-digit kernel directly over clean
// 16-digit value patterns of interest, plus a non-digit byte injected at each of
// the sixteen positions, asserting (val, ok) matches an independent computation.
// Rosetta executes SSE, so this exercises the real kernel on the dev machine.
func TestParse16SSEKernel(t *testing.T) {
	clean := []string{
		"0000000000000000", "0000000000000001", "0000000000000009",
		"1234567812345678", "9999999999999999", "1000000000000000",
		"0123456789012345", "8765432187654321", "0000000010000000",
		"9000000000000000",
	}
	for _, s := range clean {
		buf := []byte(s)
		val, ok := parse16SSE(&buf[0])
		if ok != 1 {
			t.Fatalf("parse16SSE(%q) ok=%d, want 1", s, ok)
		}
		want, _ := stdconv.ParseUint(s, 10, 64)
		if val != want {
			t.Fatalf("parse16SSE(%q)=%d want %d", s, val, want)
		}
	}

	// Non-digit at every position must yield ok==0.
	bad := []byte{'/', ':', '_', ' ', 'a', 'A', '.', 0x00, 0xff}
	for _, bc := range bad {
		for off := 0; off < 16; off++ {
			b := []byte("1234567812345678")
			b[off] = bc
			if _, ok := parse16SSE(&b[0]); ok != 0 {
				t.Fatalf("parse16SSE with %q at %d: ok=%d want 0", bc, off, ok)
			}
		}
	}
}

// TestParseDigitsSIMDGroups checks parseDigitsSIMD across every length 1..19
// (the short < 16 lengths take the scalar branch; 16..19 take the kernel plus a
// 0..3-digit scalar tail), against the standard library.
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
		// Inject a bad byte inside the kernel window (offset 0..15) once n>=16 to
		// confirm the kernel's ok==0 path routes through the scalar fallback to
		// ok=false; and a bad byte in the tail (offset 16..) for n>16.
		if n >= 16 {
			bb := append([]byte(nil), b...)
			bb[5] = 'x'
			if _, ok := parseDigitsSIMD(string(bb)); ok {
				t.Fatalf("parseDigitsSIMD with bad byte at 5 returned ok=true")
			}
		}
		if n > 16 {
			bb := append([]byte(nil), b...)
			bb[16] = 'x'
			if _, ok := parseDigitsSIMD(string(bb)); ok {
				t.Fatalf("parseDigitsSIMD with bad byte at 16 returned ok=true")
			}
		}
	}
}
