//go:build !s390x

package strconv

// minAtoiDigits is 19 on every architecture except s390x. strconv.Atoi has an
// inlined digit-at-a-time fast path for inputs shorter than 19 digits (on
// 64-bit) that the assembly-CALL SIMD kernel cannot beat; only at exactly 19
// digits does strconv.Atoi fall back to its slower strconv.ParseInt path, where
// SIMD wins. So Atoi takes the SIMD path only at 19 digits and delegates
// otherwise — never a regression.
const minAtoiDigits = 19
