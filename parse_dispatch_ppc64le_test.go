//go:build ppc64le

package strconv

import (
	stdconv "strconv"
	"testing"

	"golang.org/x/sys/cpu"
)

// TestDispatchPPC64LE drives both branches of the ppc64le SIMD fast paths
// (parseDigitsSIMD and parse16Window) — the VSX kernel and the scalar fallback.
// The kernel emits ISA-3.0 (POWER9) instructions (LXVB16X) that raise SIGILL on
// POWER8, so the kernel-forcing branch runs only when the host is actually
// POWER9+ (mirroring the amd64 force tests, which run because Rosetta executes
// SSE). The scalar-fallback branch is always exercised. The power9-targeted QEMU
// CI job and the native POWER9/POWER10 farm runs cover the kernel branch.
func TestDispatchPPC64LE(t *testing.T) {
	saved := hasVSX
	defer func() { hasVSX = saved }()

	check := func(label string) {
		for n := 1; n <= maxFastDigits; n++ {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('0' + ((i*3 + 1) % 10))
			}
			s := string(b)
			v, ok := parseDigitsSIMD(s)
			if !ok {
				t.Fatalf("%s: parseDigitsSIMD(%q) ok=false", label, s)
			}
			want, err := stdconv.ParseUint(s, 10, 64)
			if err != nil {
				t.Fatalf("%s: stdlib rejected %q: %v", label, s, err)
			}
			if v != want {
				t.Fatalf("%s: parseDigitsSIMD(%q)=%d want %d", label, s, v, want)
			}
			// Bad byte inside the kernel window routes through the scalar fallback
			// to ok=false; a bad byte in the tail likewise.
			if n >= 16 {
				bb := append([]byte(nil), b...)
				bb[5] = 'x'
				if _, ok := parseDigitsSIMD(string(bb)); ok {
					t.Fatalf("%s: parseDigitsSIMD bad byte at 5 returned ok=true", label)
				}
				// parse16Window over a clean 16-byte window.
				wv, wok := parse16Window(s[:16], 0)
				w16, _ := stdconv.ParseUint(s[:16], 10, 64)
				if !wok || wv != w16 {
					t.Fatalf("%s: parse16Window(%q)=(%d,%v) want (%d,true)", label, s[:16], wv, wok, w16)
				}
				if _, wok := parse16Window(string(bb)[:16], 0); wok {
					t.Fatalf("%s: parse16Window with bad byte returned ok=true", label)
				}
			}
			if n > 16 {
				bb := append([]byte(nil), b...)
				bb[16] = 'x'
				if _, ok := parseDigitsSIMD(string(bb)); ok {
					t.Fatalf("%s: parseDigitsSIMD bad byte at 16 returned ok=true", label)
				}
			}
		}
	}

	// Scalar fallback: always safe on every ppc64le host (POWER8 included).
	hasVSX = false
	check("fallback")

	// VSX kernel: only force it on when the CPU is POWER9+, otherwise the LXVB16X
	// in parse16 would SIGILL (e.g. on a POWER8 farm node).
	if !cpu.PPC64.IsPOWER9 {
		t.Log("CPU is pre-POWER9; VSX kernel branch not exercised on this host")
		return
	}
	hasVSX = true
	check("kernel")
}
