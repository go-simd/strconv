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
