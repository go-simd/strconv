//go:build !amd64

package strconv

// parseDigitsSIMD has no SIMD kernel on this architecture; it parses the whole
// digit run with the scalar loop (which is what the amd64 path also uses for the
// tail). Output is identical to the amd64 path, so all correctness tests hold on
// every architecture.
func parseDigitsSIMD(s string) (uint64, bool) {
	return parseScalar(s, 0, 0)
}
