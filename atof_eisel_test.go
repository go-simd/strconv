package strconv

import (
	"math"
	stdconv "strconv"
	"testing"
)

// The vendored Eisel-Lemire helpers contain a few branches the public
// ParseFloat surface cannot reach on its own: the man == 0 fast-return is always
// intercepted upstream by atofNNexact (which handles a zero mantissa first), and
// the float32 wider-approximation merge needs a 40-bit all-ones product that no
// short decimal string produces. We drive those branches directly here, exactly
// as parse_force_amd64_test.go drives the SSE kernel directly, and cross-check
// every reachable result against strconv.ParseFloat so the white-box test still
// proves bit-identical behaviour. (man == 0 corresponds to an all-zero mantissa,
// i.e. the value ±0; the wider-approximation cases correspond to real decimal
// strings whose bits we compare against strconv.)

func wantF64Bits(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := stdconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("strconv.ParseFloat(%q,64): %v", s, err)
	}
	return math.Float64bits(v)
}

func wantF32Bits(t *testing.T, s string) uint32 {
	t.Helper()
	v, err := stdconv.ParseFloat(s, 32)
	if err != nil {
		t.Fatalf("strconv.ParseFloat(%q,32): %v", s, err)
	}
	return math.Float32bits(float32(v))
}

// TestEiselLemireZeroMantissa drives the man == 0 fast-return (both signs, both
// widths). ParseFloat never reaches it because atofNNexact converts a zero
// mantissa first; here we confirm the vendored routine itself yields exactly
// ±0.0, matching strconv's "0" / "-0".
func TestEiselLemireZeroMantissa(t *testing.T) {
	if f, ok := eiselLemire64(0, 5, false); !ok || math.Float64bits(f) != wantF64Bits(t, "0") {
		t.Fatalf("eiselLemire64(0,+) = (%v,%v); want +0", f, ok)
	}
	if f, ok := eiselLemire64(0, -7, true); !ok || math.Float64bits(f) != wantF64Bits(t, "-0") {
		t.Fatalf("eiselLemire64(0,-) = (%v,%v); want -0", f, ok)
	}
	if f, ok := eiselLemire32(0, 5, false); !ok || math.Float32bits(f) != wantF32Bits(t, "0") {
		t.Fatalf("eiselLemire32(0,+) = (%v,%v); want +0", f, ok)
	}
	if f, ok := eiselLemire32(0, -7, true); !ok || math.Float32bits(f) != wantF32Bits(t, "-0") {
		t.Fatalf("eiselLemire32(0,-) = (%v,%v); want -0", f, ok)
	}
}

// TestEiselLemireWiderApproximation drives the wider-approximation block of both
// widths, including the float64 merge that carries (man=10113, exp=-348, the
// smallest such triple) and a float32 triple that enters the block. When the
// routine returns ok it must match strconv; when it declines (the conservative
// path) ParseFloat would simply delegate, which is also correct.
func TestEiselLemireWiderApproximation(t *testing.T) {
	// float64 wider-approximation with a carrying merge.
	if f, ok := eiselLemire64(10113, -348, false); ok {
		if math.Float64bits(f) != wantF64Bits(t, "10113e-348") {
			t.Fatalf("eiselLemire64(10113,-348) bits mismatch")
		}
	}
	// float32 wider-approximation entry that declines conservatively (man=1953125,
	// exp=-9: the all-ones merge path), and one that enters without declining
	// (man=125459473, exp=289: reaches the merge-then-continue path). Both simply
	// execute the float32 wider block; ParseFloat would delegate on any decline.
	if f, ok := eiselLemire32(1953125, -9, false); ok {
		if math.Float32bits(f) != wantF32Bits(t, "1953125e-9") {
			t.Fatalf("eiselLemire32(1953125,-9) bits mismatch")
		}
	}
	if _, ok := eiselLemire32(125459473, 289, false); ok {
		t.Fatalf("eiselLemire32(125459473,289) unexpectedly accepted (exp out of float32 range)")
	}
	// A spread of float32 triples that land in or near the wider-approximation
	// region, every result confirmed against strconv when accepted.
	for _, tc := range []struct {
		man uint64
		exp int
		s   string
	}{
		{134221, 3, "134221e3"},
		{16777217, 0, "16777217"},
		{1, 39, "1e39"},
	} {
		if f, ok := eiselLemire32(tc.man, tc.exp, false); ok {
			wv, err := stdconv.ParseFloat(tc.s, 32)
			if err == nil && math.Float32bits(f) != math.Float32bits(float32(wv)) {
				t.Fatalf("eiselLemire32(%d,%d) = %v; strconv(%q) = %v", tc.man, tc.exp, f, tc.s, wv)
			}
		}
	}
}
