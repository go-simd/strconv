//go:build !amd64

package strconv

// parseDigitsSIMD has no SIMD kernel on this architecture; it parses the whole
// digit run with the scalar loop (which is what the amd64 path also uses for the
// tail). Output is identical to the amd64 path, so all correctness tests hold on
// every architecture.
func parseDigitsSIMD(s string) (uint64, bool) {
	return parseScalar(s, 0, 0)
}

// parse16Window folds the 16 bytes at s[i:i+16] as decimal digits with the
// scalar loop (no SIMD kernel on this architecture). The caller guarantees
// i+16 <= len(s). It returns ok == false if any byte is not a decimal digit.
func parse16Window(s string, i int) (uint64, bool) {
	return parseScalar(s[i:i+16], 0, 0)
}
