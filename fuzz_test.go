package strconv

import (
	stdconv "strconv"
	"testing"
)

// seedCorpus feeds the fuzzers a spread of clean, boundary, overflow, signed,
// underscore, prefixed and garbage inputs so coverage-guided fuzzing starts from
// the interesting edges.
var seedCorpus = []string{
	"", "0", "1", "9", "10", "+0", "-0", "+1", "-1",
	"123456789", "12345678", "1234567",
	"9999999999999999999", "10000000000000000000",
	"18446744073709551615", "18446744073709551616", "99999999999999999999",
	"9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809",
	"0x10", "0b101", "0o17", "1_000", "_1", "1_", " 1", "1 ", "abc", "1a", "++1", "--1",
	"00000000", "000000001", "007",
}

func addSeeds(f *testing.F, withSign bool) {
	for _, s := range seedCorpus {
		f.Add(s)
		if withSign {
			f.Add("+" + s)
			f.Add("-" + s)
		}
	}
}

// FuzzParseUint asserts ParseUint matches strconv.ParseUint on arbitrary input,
// comparing the value AND the full error string, across several base / bitSize
// combinations.
func FuzzParseUint(f *testing.F) {
	addSeeds(f, false)
	f.Fuzz(func(t *testing.T, s string) {
		for _, base := range []int{10, 0, 2, 8, 16, 36} {
			for _, bs := range []int{0, 64, 8, 16, 32, 63, 65} {
				gv, ge := ParseUint(s, base, bs)
				wv, we := stdconv.ParseUint(s, base, bs)
				if gv != wv || errStr(ge) != errStr(we) {
					t.Fatalf("ParseUint(%q,%d,%d) = (%d,%v); want (%d,%v)", s, base, bs, gv, ge, wv, we)
				}
			}
		}
	})
}

// FuzzAtoi asserts Atoi matches strconv.Atoi (value AND full error string) on
// arbitrary input.
func FuzzAtoi(f *testing.F) {
	addSeeds(f, true)
	f.Fuzz(func(t *testing.T, s string) {
		gv, ge := Atoi(s)
		wv, we := stdconv.Atoi(s)
		if gv != wv || errStr(ge) != errStr(we) {
			t.Fatalf("Atoi(%q) = (%d,%v); want (%d,%v)", s, gv, ge, wv, we)
		}
	})
}

// FuzzParseInt asserts ParseInt matches strconv.ParseInt (value AND full error
// string) across several base / bitSize combinations.
func FuzzParseInt(f *testing.F) {
	addSeeds(f, true)
	f.Fuzz(func(t *testing.T, s string) {
		for _, base := range []int{10, 0, 16} {
			for _, bs := range []int{0, 64, 8, 32} {
				gv, ge := ParseInt(s, base, bs)
				wv, we := stdconv.ParseInt(s, base, bs)
				if gv != wv || errStr(ge) != errStr(we) {
					t.Fatalf("ParseInt(%q,%d,%d) = (%d,%v); want (%d,%v)", s, base, bs, gv, ge, wv, we)
				}
			}
		}
	})
}
