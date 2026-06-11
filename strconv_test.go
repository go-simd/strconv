package strconv

import (
	"fmt"
	stdconv "strconv"
	"testing"
)

// errStr renders an error (possibly *strconv.NumError) as its full string, so
// tests compare the exact message — Func, quoted Num and reason — not just the
// sentinel.
func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// checkParseUint asserts (value, full error string) for ParseUint equals the
// standard library for the given args.
func checkParseUint(t *testing.T, s string, base, bitSize int) {
	t.Helper()
	gv, ge := ParseUint(s, base, bitSize)
	wv, we := stdconv.ParseUint(s, base, bitSize)
	if gv != wv || errStr(ge) != errStr(we) {
		t.Fatalf("ParseUint(%q,%d,%d) = (%d,%v); want (%d,%v)", s, base, bitSize, gv, ge, wv, we)
	}
}

func checkParseInt(t *testing.T, s string, base, bitSize int) {
	t.Helper()
	gv, ge := ParseInt(s, base, bitSize)
	wv, we := stdconv.ParseInt(s, base, bitSize)
	if gv != wv || errStr(ge) != errStr(we) {
		t.Fatalf("ParseInt(%q,%d,%d) = (%d,%v); want (%d,%v)", s, base, bitSize, gv, ge, wv, we)
	}
}

func checkAtoi(t *testing.T, s string) {
	t.Helper()
	gv, ge := Atoi(s)
	wv, we := stdconv.Atoi(s)
	if gv != wv || errStr(ge) != errStr(we) {
		t.Fatalf("Atoi(%q) = (%d,%v); want (%d,%v)", s, gv, ge, wv, we)
	}
}

// digitStrings is a broad table of decimal inputs: zero, every length 1..21,
// leading zeros, MaxUint64 / MaxInt64 / MinInt64 boundaries and one past them.
var digitStrings = func() []string {
	out := []string{
		"0", "00", "000", "0000000000000000000000",
		"1", "9", "10", "99", "100",
		"01", "007", "0000123",
		"123", "12345678", "123456789", "1234567890",
		"9999999999999999999",  // 19 nines
		"10000000000000000000", // 20 digits
		"18446744073709551615", // MaxUint64
		"18446744073709551616", // MaxUint64+1
		"99999999999999999999", // 20 nines (overflow)
		"9223372036854775807",  // MaxInt64
		"9223372036854775808",  // MaxInt64+1
		"4294967295",           // MaxUint32
		"2147483647",           // MaxInt32
		"2147483648",
	}
	// All lengths 1..21 as a run of 1s, and as 9s, exercising every digit count
	// across the 8-digit SIMD group boundary and the 19/20-digit overflow edge.
	for n := 1; n <= 21; n++ {
		ones := make([]byte, n)
		nines := make([]byte, n)
		for i := range ones {
			ones[i] = '1'
			nines[i] = '9'
		}
		out = append(out, string(ones), string(nines))
	}
	return out
}()

func TestParseUintTable(t *testing.T) {
	bases := []int{10, 0, 2, 8, 16, 36, 1, 37, -1}
	bitSizes := []int{0, 64, 1, 8, 16, 32, 63, 65, -1}
	for _, s := range digitStrings {
		for _, base := range bases {
			for _, bs := range bitSizes {
				checkParseUint(t, s, base, bs)
			}
		}
	}
}

func TestParseUintSigns(t *testing.T) {
	// ParseUint forbids a sign — must surface ErrSyntax via fallback.
	for _, s := range []string{"+1", "-1", "+0", "-0", "+", "-", "+123", "-123"} {
		checkParseUint(t, s, 10, 64)
		checkParseUint(t, s, 10, 0)
	}
}

func TestAtoiTable(t *testing.T) {
	for _, s := range digitStrings {
		checkAtoi(t, s)
		checkAtoi(t, "+"+s)
		checkAtoi(t, "-"+s)
	}
	for _, s := range []string{"", "+", "-", "++1", "--1", "+-1", " 1", "1 ", "1_000", "0x10", "0b1", "0o7", "abc", "1a", "a1"} {
		checkAtoi(t, s)
	}
}

func TestParseIntTable(t *testing.T) {
	bases := []int{10, 0, 16}
	bitSizes := []int{0, 64, 8, 32}
	for _, s := range digitStrings {
		for _, base := range bases {
			for _, bs := range bitSizes {
				checkParseInt(t, s, base, bs)
				checkParseInt(t, "+"+s, base, bs)
				checkParseInt(t, "-"+s, base, bs)
			}
		}
	}
}

// TestNonDigitEveryPosition injects a non-digit byte at every position of valid
// digit strings of several lengths (crossing the 8-digit SIMD boundary) and
// asserts the fallback reproduces strconv's exact (value, error).
func TestNonDigitEveryPosition(t *testing.T) {
	bad := []byte{'/', ':', '_', ' ', '+', '-', 'a', 'A', '.', 0x00, 0xff, '\t'}
	for _, n := range []int{1, 2, 7, 8, 9, 15, 16, 17, 19, 20} {
		base := make([]byte, n)
		for i := range base {
			base[i] = byte('0' + (i % 10))
		}
		if base[0] == '0' {
			base[0] = '1'
		}
		for _, bc := range bad {
			for off := 0; off < n; off++ {
				s := append([]byte(nil), base...)
				s[off] = bc
				str := string(s)
				checkParseUint(t, str, 10, 64)
				checkAtoi(t, str)
				checkParseInt(t, str, 10, 64)
			}
		}
	}
}

// TestUnderscoreSeparators routes underscore forms (only valid for base 0) and
// asserts equality — these must all fall back.
func TestUnderscoreSeparators(t *testing.T) {
	for _, s := range []string{"1_000", "1_0_0", "_1", "1_", "1__0", "12_345_678", "1_2345_6789"} {
		for _, base := range []int{10, 0} {
			checkParseUint(t, s, base, 64)
			checkParseInt(t, s, base, 64)
		}
		checkAtoi(t, s)
	}
}

// TestParseDigitsExact directly checks the fast-path digit parser against an
// independent computation for clean inputs of every length up to the bound.
func TestParseDigitsExact(t *testing.T) {
	for n := 1; n <= maxFastDigits; n++ {
		for _, seed := range []byte{'0', '1', '5', '9'} {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('0' + ((int(seed-'0') + i*7) % 10))
			}
			s := string(b)
			v, ok := parseDigits(s)
			if !ok {
				t.Fatalf("parseDigits(%q) ok=false", s)
			}
			want, err := stdconv.ParseUint(s, 10, 64)
			if err != nil {
				t.Fatalf("stdlib rejected %q: %v", s, err)
			}
			if v != want {
				t.Fatalf("parseDigits(%q)=%d want %d", s, v, want)
			}
		}
	}
}

func ExampleParseUint() {
	v, err := ParseUint("123456789", 10, 64)
	fmt.Println(v, err)
	// Output: 123456789 <nil>
}
