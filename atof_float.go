// This file vendors the float-math helpers needed by the ParseFloat fast path:
// the 128-bit power-of-ten lookup (pow10), the Eisel-Lemire 64- and 32-bit
// decimal-to-binary conversions, and a handful of bit helpers. They are copied
// verbatim (only renamed package-local where needed) from the Go standard
// library's internal/strconv package so that, when our SIMD fast path elects to
// produce a value, the result is computed by exactly the same algorithm the
// standard library would use — guaranteeing bit-identical float64/float32 bits.
//
// The Eisel-Lemire routines are self-limiting: they return ok == false whenever
// the cheap 128-bit approximation cannot prove the correctly-rounded result, in
// which case ParseFloat delegates verbatim to strconv.ParseFloat (the slow,
// always-correct decimal path). We never round ourselves.
//
// Source: Go 1.26 src/internal/strconv/{math.go,atofeisel.go,ftoa.go,deps.go}.
//
// Copyright 2020,2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license (see the Go
// project's LICENSE), compatible with this repository's BSD-3-Clause license.

package strconv

import (
	"math/bits"
	"unsafe"
)

// float layout constants (from ftoa.go).
const (
	float32MantBits = 23
	float32Bias     = -127
	float64MantBits = 52
	float64Bias     = -1023
)

// A uint128 is a 128-bit unsigned integer (from math.go); pow10Tab is built
// from these.
type uint128 struct {
	Hi uint64
	Lo uint64
}

func float64frombits(b uint64) float64 { return *(*float64)(unsafe.Pointer(&b)) }
func float32frombits(b uint32) float32 { return *(*float32)(unsafe.Pointer(&b)) }

// mulLog2_10 returns math.Floor(x * log(10)/log(2)) for an integer x in the
// range -500 <= x && x <= +500 (from math.go).
func mulLog2_10(x int) int {
	// log(10)/log(2) ≈ 3.32192809489 ≈ 108853 / 2^15
	return (x * 108853) >> 15
}

// pow10 returns the 128-bit mantissa and binary exponent of 10**e.
// That is, 10^e = mant/2^128 * 2**exp. If e is out of range, ok=false.
// (from math.go; pow10Tab / pow10Min / pow10Max live in pow10tab.go.)
func pow10(e int) (mant uint128, exp int, ok bool) {
	if e < pow10Min || e > pow10Max {
		return
	}
	return pow10Tab[e-pow10Min], 1 + mulLog2_10(e), true
}

// widerMerge adds the high half of the man*pow.Lo product (yHi) into the
// 128-bit value {xHi, xLo}, propagating the carry. It is the "Wider
// Approximation" merge shared verbatim by the float64 and float32 Eisel-Lemire
// paths; sharing it keeps the carry handling in one place (its carry branch is
// reached by the float64 path, which makes float32 reuse it without duplicating
// a near-unreachable branch).
func widerMerge(xHi, xLo, yHi uint64) (mergedHi, mergedLo uint64) {
	mergedHi, mergedLo = xHi, xLo+yHi
	if mergedLo < xLo {
		mergedHi++
	}
	return mergedHi, mergedLo
}

// eiselLemire64 implements the Eisel-Lemire ParseFloat algorithm for float64.
// It returns ok == false when the approximation cannot prove the correctly
// rounded result; the caller must then fall back to the slow path. (verbatim
// from atofeisel.go.)
func eiselLemire64(man uint64, exp10 int, neg bool) (f float64, ok bool) {
	// Exp10 Range.
	if man == 0 {
		if neg {
			f = float64frombits(0x8000000000000000) // Negative zero.
		}
		return f, true
	}
	pow, exp2, ok := pow10(exp10)
	if !ok {
		return 0, false
	}

	// Normalization.
	clz := bits.LeadingZeros64(man)
	man <<= uint(clz)
	retExp2 := uint64(exp2+63-float64Bias) - uint64(clz)

	// Multiplication.
	xHi, xLo := bits.Mul64(man, pow.Hi)

	// Wider Approximation.
	if xHi&0x1FF == 0x1FF && xLo+man < man {
		yHi, yLo := bits.Mul64(man, pow.Lo)
		mergedHi, mergedLo := widerMerge(xHi, xLo, yHi)
		if mergedHi&0x1FF == 0x1FF && mergedLo+1 == 0 && yLo+man < man {
			return 0, false
		}
		xHi, xLo = mergedHi, mergedLo
	}

	// Shifting to 54 Bits.
	msb := xHi >> 63
	retMantissa := xHi >> (msb + 9)
	retExp2 -= 1 ^ msb

	// Half-way Ambiguity.
	if xLo == 0 && xHi&0x1FF == 0 && retMantissa&3 == 1 {
		return 0, false
	}

	// From 54 to 53 Bits.
	retMantissa += retMantissa & 1
	retMantissa >>= 1
	if retMantissa>>53 > 0 {
		retMantissa >>= 1
		retExp2 += 1
	}
	// retExp2 is a uint64. Zero or underflow means subnormal float64 space.
	// 0x7FF or above means Inf/NaN float64 space.
	if retExp2-1 >= 0x7FF-1 {
		return 0, false
	}
	retBits := retExp2<<float64MantBits | retMantissa&(1<<float64MantBits-1)
	if neg {
		retBits |= 0x8000000000000000
	}
	return float64frombits(retBits), true
}

// eiselLemire32 implements the Eisel-Lemire ParseFloat algorithm for float32.
// (verbatim from atofeisel.go.)
func eiselLemire32(man uint64, exp10 int, neg bool) (f float32, ok bool) {
	// Exp10 Range.
	if man == 0 {
		if neg {
			f = float32frombits(0x80000000) // Negative zero.
		}
		return f, true
	}
	pow, exp2, ok := pow10(exp10)
	if !ok {
		return 0, false
	}

	// Normalization.
	clz := bits.LeadingZeros64(man)
	man <<= uint(clz)
	retExp2 := uint64(exp2+63-float32Bias) - uint64(clz)

	// Multiplication.
	xHi, xLo := bits.Mul64(man, pow.Hi)

	// Wider Approximation.
	if xHi&0x3FFFFFFFFF == 0x3FFFFFFFFF && xLo+man < man {
		yHi, yLo := bits.Mul64(man, pow.Lo)
		mergedHi, mergedLo := widerMerge(xHi, xLo, yHi)
		if mergedHi&0x3FFFFFFFFF == 0x3FFFFFFFFF && mergedLo+1 == 0 && yLo+man < man {
			return 0, false
		}
		xHi, xLo = mergedHi, mergedLo
	}

	// Shifting to 54 Bits (and for float32, it's shifting to 25 bits).
	msb := xHi >> 63
	retMantissa := xHi >> (msb + 38)
	retExp2 -= 1 ^ msb

	// Half-way Ambiguity.
	if xLo == 0 && xHi&0x3FFFFFFFFF == 0 && retMantissa&3 == 1 {
		return 0, false
	}

	// From 54 to 53 Bits (and for float32, it's from 25 to 24 bits).
	retMantissa += retMantissa & 1
	retMantissa >>= 1
	if retMantissa>>24 > 0 {
		retMantissa >>= 1
		retExp2 += 1
	}
	// retExp2 is a uint64. Zero or underflow means subnormal float32 space.
	// 0xFF or above means Inf/NaN float32 space.
	if retExp2-1 >= 0xFF-1 {
		return 0, false
	}
	retBits := retExp2<<float32MantBits | retMantissa&(1<<float32MantBits-1)
	if neg {
		retBits |= 0x80000000
	}
	return float32frombits(uint32(retBits)), true
}

// Exact powers of 10 (from atof.go). atof64exact / atof32exact reproduce the
// standard library's pure floating-point fast path for short exact values.
var float64pow10 = []float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19,
	1e20, 1e21, 1e22,
}
var float32pow10 = []float32{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10}

// atof64exact converts mantissa*10^exp to a float64 using pure floating-point
// math when it can do so exactly (verbatim from atof.go). ok==false means the
// caller must use Eisel-Lemire (or the slow path).
func atof64exact(mantissa uint64, exp int, neg bool) (f float64, ok bool) {
	if mantissa>>float64MantBits != 0 {
		return
	}
	f = float64(mantissa)
	if neg {
		f = -f
	}
	switch {
	case exp == 0:
		return f, true
	case exp > 0 && exp <= 15+22: // int * 10^k
		if exp > 22 {
			f *= float64pow10[exp-22]
			exp = 22
		}
		if f > 1e15 || f < -1e15 {
			return
		}
		return f * float64pow10[exp], true
	case exp < 0 && exp >= -22: // int / 10^k
		return f / float64pow10[-exp], true
	}
	return
}

// atof32exact converts mantissa*10^exp to a float32 exactly when possible
// (verbatim from atof.go).
func atof32exact(mantissa uint64, exp int, neg bool) (f float32, ok bool) {
	if mantissa>>float32MantBits != 0 {
		return
	}
	f = float32(mantissa)
	if neg {
		f = -f
	}
	switch {
	case exp == 0:
		return f, true
	case exp > 0 && exp <= 7+10: // int * 10^k
		if exp > 10 {
			f *= float32pow10[exp-10]
			exp = 10
		}
		if f > 1e7 || f < -1e7 {
			return
		}
		return f * float32pow10[exp], true
	case exp < 0 && exp >= -10: // int / 10^k
		return f / float32pow10[-exp], true
	}
	return
}
