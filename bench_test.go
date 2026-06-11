package strconv

import (
	stdconv "strconv"
	"testing"
)

// benchInputs are representative clean decimal strings: 8, 16 and 20 digits
// (the 20-digit case overflows the fast-path length bound and falls back, so it
// honestly shows the no-win case).
var benchInputs = []struct {
	name string
	s    string
}{
	{"8digit", "12345678"},
	{"16digit", "1234567812345678"},
	{"17digit", "12345678123456781"},
	{"18digit", "123456781234567812"},
	{"19digit", "1234567812345678123"},
	{"20digit", "12345678123456781234"},
}

func BenchmarkParseUint(b *testing.B) {
	for _, in := range benchInputs {
		b.Run(in.name+"/simd", func(b *testing.B) {
			b.SetBytes(int64(len(in.s)))
			var sink uint64
			for i := 0; i < b.N; i++ {
				sink, _ = ParseUint(in.s, 10, 64)
			}
			_ = sink
		})
		b.Run(in.name+"/std", func(b *testing.B) {
			b.SetBytes(int64(len(in.s)))
			var sink uint64
			for i := 0; i < b.N; i++ {
				sink, _ = stdconv.ParseUint(in.s, 10, 64)
			}
			_ = sink
		})
	}
}

// floatBenchInputs are representative ParseFloat cases: a short fraction, a
// 15-significant-digit fraction, a large-exponent value, and a 17-significant-
// digit value (the round-trip stressor). All take the SIMD/Eisel-Lemire fast
// path except where the fast path conservatively declines (reported honestly).
var floatBenchInputs = []struct {
	name string
	s    string
}{
	{"3.14159", "3.14159"},
	{"15sig", "1234567890.12345"},
	{"1e308", "1e308"},
	{"17sig", "1.7976931348623157"},
}

func BenchmarkParseFloat(b *testing.B) {
	for _, in := range floatBenchInputs {
		b.Run(in.name+"/simd", func(b *testing.B) {
			b.SetBytes(int64(len(in.s)))
			var sink float64
			for i := 0; i < b.N; i++ {
				sink, _ = ParseFloat(in.s, 64)
			}
			_ = sink
		})
		b.Run(in.name+"/std", func(b *testing.B) {
			b.SetBytes(int64(len(in.s)))
			var sink float64
			for i := 0; i < b.N; i++ {
				sink, _ = stdconv.ParseFloat(in.s, 64)
			}
			_ = sink
		})
	}
}

func BenchmarkAtoi(b *testing.B) {
	for _, in := range benchInputs {
		b.Run(in.name+"/simd", func(b *testing.B) {
			b.SetBytes(int64(len(in.s)))
			var sink int
			for i := 0; i < b.N; i++ {
				sink, _ = Atoi(in.s)
			}
			_ = sink
		})
		b.Run(in.name+"/std", func(b *testing.B) {
			b.SetBytes(int64(len(in.s)))
			var sink int
			for i := 0; i < b.N; i++ {
				sink, _ = stdconv.Atoi(in.s)
			}
			_ = sink
		})
	}
}
