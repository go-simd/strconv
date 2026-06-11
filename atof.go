package strconv

import "strconv"

// ParseFloat is a drop-in for strconv.ParseFloat: identical value (the exact
// float64/float32 bits) AND identical error.
//
// Fast path (decimal, bitSize 32 or 64): an optional leading sign, a run of
// ASCII decimal digits with at most one '.', and an optional 'e'/'E' exponent —
// no '_' separators, no hex (0x…p…), no inf/Inf/Infinity/nan. We extract the
// significant mantissa (up to 19 digits, SIMD-folded by the parse16SSE kernel
// when a clean 16-digit window is present) and the base-10 exponent exactly the
// way the standard library's readFloat does, then convert via the same
// atofNNexact / Eisel-Lemire routines strconv uses. Those routines are
// self-limiting: when the cheap conversion cannot prove the correctly-rounded
// result (rounding-boundary inputs, out-of-range exponents, truncated mantissas
// it cannot confirm), they decline and we delegate verbatim to
// strconv.ParseFloat. We never round ourselves, so the result is bit-identical.
//
// Everything the fast path does not recognise — other bitSizes, hex floats,
// inf/nan, '_' separators, exponent-only / leading-dot / trailing-dot oddities,
// empty or malformed input, over-long mantissas, and any value near a rounding
// boundary — is delegated to strconv.ParseFloat, which produces the canonical
// value and the exact *strconv.NumError.
func ParseFloat(s string, bitSize int) (float64, error) {
	if bitSize == 64 {
		if f, ok := fastParseFloat64(s); ok {
			return f, nil
		}
	} else if bitSize == 32 {
		if f, ok := fastParseFloat32(s); ok {
			return float64(f), nil
		}
	}
	return strconv.ParseFloat(s, bitSize)
}

// floatPrefix scans a clean decimal float prefix of s and extracts the standard
// library's (mantissa, exp, neg, trunc) quadruple — using exactly readFloat's
// rules — while accelerating a dense leading digit run with the SIMD kernel.
//
// It returns ok == false (so the caller delegates to strconv) for ANY input it
// is not certain it parses identically to readFloat: an empty string, a sign
// with no digits, no significant digits, hex prefixes, '_' separators, inf/nan
// leads, a stray dot, a malformed or absent-but-required exponent, a trailing
// non-digit byte, an exponent magnitude at/over readFloat's 10000 clamp, or a
// mantissa whose untruncated form we cannot vouch for. When ok is true, the
// quadruple equals what strconv's readFloat returns for the same s.
func floatPrefix(s string) (mantissa uint64, exp int, neg, trunc, ok bool) {
	i := 0
	if i >= len(s) {
		return
	}
	// Optional sign.
	switch s[i] {
	case '+':
		i++
	case '-':
		i++
		neg = true
	}
	// Reject hex floats (0x…) up front — delegated.
	if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
		return
	}

	// Significant-digit scan, replicating readFloat exactly:
	//   nd     = count of significant digits seen (post leading-zero skip),
	//   ndMant = count folded into mantissa (capped at 19),
	//   dp     = position of the decimal point among significant digits.
	const maxMantDigits = 19
	sawdot := false
	sawdigits := false
	nd := 0
	ndMant := 0
	dp := 0

	// SIMD fast lane: when no sign-relative dot has appeared yet and at least 16
	// raw '0'..'9' bytes (no leading zeros, no dot) sit at s[i:], fold the first
	// 16 in one kernel call. We only take it when s[i] != '0' so the leading-zero
	// rule below is untouched; the kernel both validates (all 16 are digits) and
	// returns their value.
	if !sawdot && i+16 <= len(s) && s[i] >= '1' && s[i] <= '9' {
		if v, kok := parse16Window(s, i); kok {
			mantissa = v
			nd = 16
			ndMant = 16
			i += 16
			sawdigits = true
			// Fall through to the scalar loop for any further digits / dot / exp.
		}
	}

loop:
	for ; i < len(s); i++ {
		switch c := s[i]; true {
		case c == '_':
			// Underscores require the full strconv validation; delegate.
			return 0, 0, false, false, false
		case c == '.':
			if sawdot {
				return 0, 0, false, false, false
			}
			sawdot = true
			dp = nd
			continue
		case '0' <= c && c <= '9':
			sawdigits = true
			if c == '0' && nd == 0 { // ignore leading zeros
				dp--
				continue
			}
			nd++
			if ndMant < maxMantDigits {
				mantissa *= 10
				mantissa += uint64(c - '0')
				ndMant++
			} else if c != '0' {
				trunc = true
			}
			continue
		}
		break loop
	}
	if !sawdigits {
		return 0, 0, false, false, false
	}
	if !sawdot {
		dp = nd
	}

	// Optional exponent.
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i >= len(s) {
			return 0, 0, false, false, false
		}
		esign := 1
		switch s[i] {
		case '+':
			i++
		case '-':
			i++
			esign = -1
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return 0, 0, false, false, false
		}
		e := 0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			if e < 10000 {
				e = e*10 + int(s[i]) - '0'
			} else {
				// readFloat clamps at 10000; matching that needs care. Delegate
				// rather than risk a mismatch on absurd exponents.
				return 0, 0, false, false, false
			}
		}
		dp += e * esign
	}

	// The whole string must be the number (ParseFloat requires n == len(s)).
	if i != len(s) {
		return 0, 0, false, false, false
	}

	if mantissa != 0 {
		exp = dp - ndMant
	}
	return mantissa, exp, neg, trunc, true
}

// fastParseFloat64 attempts the bit-identical fast path for bitSize 64.
func fastParseFloat64(s string) (float64, bool) {
	mantissa, exp, neg, trunc, ok := floatPrefix(s)
	if !ok {
		return 0, false
	}
	// Pure floating-point exact conversion (only when not truncated).
	if !trunc {
		if f, ok := atof64exact(mantissa, exp, neg); ok {
			return f, true
		}
	}
	f, ok := eiselLemire64(mantissa, exp, neg)
	if !ok {
		return 0, false
	}
	if !trunc {
		return f, true
	}
	// Truncated: confirm the upper mantissa bound rounds the same way; else
	// delegate (strconv's slow path resolves it exactly).
	fUp, okUp := eiselLemire64(mantissa+1, exp, neg)
	if okUp && f == fUp {
		return f, true
	}
	return 0, false
}

// fastParseFloat32 attempts the bit-identical fast path for bitSize 32.
func fastParseFloat32(s string) (float32, bool) {
	mantissa, exp, neg, trunc, ok := floatPrefix(s)
	if !ok {
		return 0, false
	}
	if !trunc {
		if f, ok := atof32exact(mantissa, exp, neg); ok {
			return f, true
		}
	}
	f, ok := eiselLemire32(mantissa, exp, neg)
	if !ok {
		return 0, false
	}
	if !trunc {
		return f, true
	}
	fUp, okUp := eiselLemire32(mantissa+1, exp, neg)
	if okUp && f == fUp {
		return f, true
	}
	return 0, false
}
