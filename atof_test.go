package strconv

import (
	"fmt"
	"math"
	stdconv "strconv"
	"testing"
)

// checkParseFloat asserts (bits, full error string) for ParseFloat equals the
// standard library for the given bitSize. Float values are compared by their
// IEEE-754 bit pattern (math.Float64bits / Float32bits) so that -0.0 vs +0.0 and
// NaN payloads are distinguished, and the exact rounding is verified.
func checkParseFloat(t *testing.T, s string, bitSize int) {
	t.Helper()
	gv, ge := ParseFloat(s, bitSize)
	wv, we := stdconv.ParseFloat(s, bitSize)
	var gb, wb uint64
	if bitSize == 32 {
		gb = uint64(math.Float32bits(float32(gv)))
		wb = uint64(math.Float32bits(float32(wv)))
	} else {
		gb = math.Float64bits(gv)
		wb = math.Float64bits(wv)
	}
	if gb != wb || errStr(ge) != errStr(we) {
		t.Fatalf("ParseFloat(%q,%d) = (bits %#x=%v, %v); want (bits %#x=%v, %v)",
			s, bitSize, gb, gv, ge, wb, wv, we)
	}
}

// floatStrings is a broad table covering: integers-as-floats, fractions,
// scientific notation (all sign/exponent forms), negatives, zero / -0.0,
// subnormals, MaxFloat64 / SmallestNonzeroFloat64 boundaries, overflow,
// underflow, every malformed form, hex floats, inf / nan, and '_' separators.
var floatStrings = []string{
	// zero and signed zero
	"0", "+0", "-0", "0.0", "-0.0", "0e0", "-0e5", "0.000", "00", "000",
	// small integers as floats
	"1", "-1", "+1", "2", "3", "10", "100", "12345", "-9999",
	// fractions
	"3.14159", "2.718281828", "0.5", "-0.25", ".5", "-.5", "5.", "5.e2",
	"0.1", "0.2", "0.3", "0.1234567890123456789",
	// long-but-fast mantissas (16..19 significant digits — SIMD lane)
	"1234567890.12345", "1234567812345678", "12345678123456789",
	"1234567812345678.9", "9999999999999999", "1000000000000000",
	"3.141592653589793", "2.302585092994046",
	// 17-significant-digit values (classic round-trip stressors)
	"1.7976931348623157e308", "2.2250738585072014e-308",
	"1.0000000000000002", "0.99999999999999994",
	"9007199254740993", // 2^53+1 — not exactly representable
	// scientific notation
	"1e0", "1e1", "1e-1", "1e10", "1e-10", "1e308", "1e-308", "1e309",
	"1e-323", "1e-324", "1e-330", "5e-324", "4e-324", "1E10", "1.5E-3",
	"6.022e23", "1.602e-19", "-2.5e-3", "+7.5E+2",
	// boundaries
	"1.7976931348623157e+308", // MaxFloat64
	"1.7976931348623159e+308", // just over MaxFloat64 -> +Inf, ErrRange
	"-1.7976931348623159e+308",
	"4.9406564584124654e-324", // SmallestNonzeroFloat64 (subnormal)
	"2.4703282292062327e-324", // half of smallest -> rounds to 0, no error
	"1.4012984643e-45",        // ~SmallestNonzeroFloat32
	"3.4028234663852886e+38",  // MaxFloat32
	"3.4028235677973366e+38",  // over MaxFloat32
	// huge exponents (clamp territory) and overflow/underflow
	"1e99999", "1e-99999", "1e10000", "1e-10000", "1e1000000000000",
	"123456789012345678901234567890e-30",
	// truncated mantissas (> 19 significant digits)
	"123456789012345678901234567890",
	"1.23456789012345678901234567890",
	"0.123456789012345678901234567890e5",
	"9999999999999999999999999",
	"100000000000000000000000000000000000000000000000000",
	// malformed / syntax-error forms (all must ErrSyntax via delegation)
	"", "+", "-", ".", "+.", "-.", "e", "e5", "E-3", ".e5", "1.2.3",
	"1..2", "abc", "1a", "a1", "0x", "1e", "1e+", "1e-", "1ee5", "++1",
	"--1", "1.5x", " 1", "1 ", "1\t", "0b101", "1_", "_1", "0x1p4z",
	// underscore separators (valid only in specific positions; delegate)
	"1_000.5", "1_0.0", "3.14_159", "1e1_0", "1_000_000", "1__0", ".5_",
	// hex floats (delegate)
	"0x1p0", "0x1.8p1", "0x1p-1", "0X1.FP+3", "-0x1p10", "0x1.fffffffffffffp+1023",
	"0x10p-4", "0x0p0", "0x1p1024",
	// inf / nan (delegate, all cases)
	"inf", "Inf", "INF", "+inf", "-inf", "infinity", "Infinity", "INFINITY",
	"nan", "NaN", "NAN", "+nan", "-nan", "iNf", "nAn",
	// branch-exercising Eisel-Lemire / atofNNexact inputs (found by search):
	// wider-approximation entry, half-way ambiguity decline, division-by-10^k
	// exact path, and truncated mantissas whose upper bound disagrees (decline).
	"101e-348", "295149e15", "5.5", "-5.5", "0.0009765625",
	"295149000000000000000",
	// atofNNexact int*10^k with exp>22 (moves zeros into integer part):
	"1e23", "5e25", "7e30", "123e24", "-9e28",
	// atofNNexact int/10^k division branch (also for float32):
	"0.5", "0.25", "0.125", "12.34", "-0.0078125", "7.5",
	// Eisel-Lemire wider-approximation carry (man*pow merge carries):
	"10113e-348",
	"0.4999999999999999999999", "0.9999999999999999999999",
	"100000000000000000010000", "100000000000000000010001",
	"12345678901234567890.5", "-99999999999999999999.99e-3",
}

func TestParseFloatTable(t *testing.T) {
	for _, s := range floatStrings {
		checkParseFloat(t, s, 64)
		checkParseFloat(t, s, 32)
	}
}

// float32Branch inputs exercise the float32 Eisel-Lemire branches (wider
// approximation, half-way ambiguity) that the float64 inputs above do not
// reach. Each is cross-checked against strconv at bitSize 32.
var float32Branch = []string{
	"134221e3",   // half-way ambiguity decline (found by search)
	"1.1e10",     // wider-approximation territory
	"8.589973e9", // 2^33 region, float32 rounding stressor
	"16777217",   // 2^24+1, not exactly representable as float32
	"1e39",       // float32 overflow -> +Inf, ErrRange
	"1e-46",      // float32 underflow -> 0
	"3.4028236e38",
	"1953125e-9", // float32 wider-approximation entry (found by search)
}

func TestParseFloat32Branches(t *testing.T) {
	for _, s := range float32Branch {
		checkParseFloat(t, s, 32)
	}
}

// TestParseFloatBitSizes exercises bitSizes other than 32/64, which must all
// delegate to strconv (which returns ErrSyntax for unsupported sizes).
func TestParseFloatBitSizes(t *testing.T) {
	for _, bs := range []int{0, 16, 31, 33, 48, 63, 65, 128, -1} {
		for _, s := range []string{"3.14", "1", "1e10", "inf", "0x1p2", ""} {
			gv, ge := ParseFloat(s, bs)
			wv, we := stdconv.ParseFloat(s, bs)
			if math.Float64bits(gv) != math.Float64bits(wv) || errStr(ge) != errStr(we) {
				t.Fatalf("ParseFloat(%q,%d) = (%v,%v); want (%v,%v)", s, bs, gv, ge, wv, we)
			}
		}
	}
}

// TestParseFloatExhaustiveLengths feeds every digit length 1..25 (crossing the
// 16-digit SIMD window and the 19-digit truncation boundary), as integers, as
// fractions, and with assorted exponents, asserting bit equality with strconv.
func TestParseFloatExhaustiveLengths(t *testing.T) {
	for n := 1; n <= 25; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('1' + (i*7+3)%9) // digits '1'..'9', never a leading zero
		}
		digits := string(b)
		variants := []string{
			digits,
			digits + ".5",
			digits + "e3",
			digits + "e-7",
			digits + "E+12",
			"0." + digits,
			"-" + digits + ".25e2",
			digits[:1] + "." + digits[1:],
		}
		for _, s := range variants {
			checkParseFloat(t, s, 64)
			checkParseFloat(t, s, 32)
		}
	}
}

// TestParseFloatExample documents the drop-in behaviour.
func ExampleParseFloat() {
	v, err := ParseFloat("3.14159", 64)
	fmt.Println(v, err)
	// Output: 3.14159 <nil>
}

// FuzzParseFloat asserts ParseFloat matches strconv.ParseFloat on arbitrary
// input — comparing the IEEE-754 BITS (so rounding, sign of zero and NaN are
// exact) AND the full error string — for both bitSize 32 and 64.
func FuzzParseFloat(f *testing.F) {
	for _, s := range floatStrings {
		f.Add(s)
	}
	// A few extra integer-shaped seeds reuse the integer corpus.
	for _, s := range seedCorpus {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, bs := range []int{64, 32} {
			gv, ge := ParseFloat(s, bs)
			wv, we := stdconv.ParseFloat(s, bs)
			var gb, wb uint64
			if bs == 32 {
				gb = uint64(math.Float32bits(float32(gv)))
				wb = uint64(math.Float32bits(float32(wv)))
			} else {
				gb = math.Float64bits(gv)
				wb = math.Float64bits(wv)
			}
			if gb != wb || errStr(ge) != errStr(we) {
				t.Fatalf("ParseFloat(%q,%d) = (bits %#x, %v); want (bits %#x, %v)",
					s, bs, gb, ge, wb, we)
			}
		}
	})
}
