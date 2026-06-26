//go:build ppc64le || s390x

package strconv

import (
	stdconv "strconv"
	"testing"
)

// TestParse16Kernel drives the SIMD 16-digit kernel directly (the POWER VSX
// kernel on ppc64le, the z/Architecture vector kernel on s390x) over clean
// 16-digit value patterns of interest, plus a non-digit byte injected at each of
// the sixteen positions, asserting (val, ok) matches an independent computation.
// It runs under qemu in the emulated CI job and is the position-dependent check
// that pins the big-endian digit-to-weight lane assignment: a reversed or
// mis-paired fold would parse these known numbers to a different value and fail
// here.
func TestParse16Kernel(t *testing.T) {
	// parse16 is the raw SIMD kernel, called here with no dispatch guard. On
	// ppc64le it emits ISA-3.0 (POWER9) instructions (LXVB16X) that SIGILL on a
	// POWER8 host, so skip when the CPU lacks the required level; the s390x
	// vector facility is always present on our targets, so the guard is a no-op
	// there. The POWER9-targeted QEMU job and the native POWER9/POWER10 farm runs
	// still exercise the kernel.
	if !simdKernelForceSafe() {
		t.Skip("CPU lacks the SIMD kernel's required ISA level; covered by POWER9+ runs")
	}
	clean := []string{
		"0000000000000000", "0000000000000001", "0000000000000009",
		"1234567812345678", "9999999999999999", "1000000000000000",
		"0123456789012345", "8765432187654321", "0000000010000000",
		"9000000000000000", "1000000000000001", "0000000000010000",
	}
	for _, s := range clean {
		buf := []byte(s)
		val, ok := parse16(&buf[0])
		if ok != 1 {
			t.Fatalf("parse16(%q) ok=%d, want 1", s, ok)
		}
		want, _ := stdconv.ParseUint(s, 10, 64)
		if val != want {
			t.Fatalf("parse16(%q)=%d want %d", s, val, want)
		}
	}

	// Non-digit at every position must yield ok==0.
	bad := []byte{'/', ':', '_', ' ', 'a', 'A', '.', 0x00, 0xff}
	for _, bc := range bad {
		for off := 0; off < 16; off++ {
			b := []byte("1234567812345678")
			b[off] = bc
			if _, ok := parse16(&b[0]); ok != 0 {
				t.Fatalf("parse16 with %q at %d: ok=%d want 0", bc, off, ok)
			}
		}
	}
}

// TestParseDigitsSIMDGroups checks parseDigitsSIMD across every length 1..19
// (the short < 16 lengths take the scalar branch; 16..19 take the kernel plus a
// 0..3-digit scalar tail), against the standard library, and that an injected
// bad byte in the kernel window and in the tail both route to ok=false.
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
