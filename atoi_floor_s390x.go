//go:build s390x

package strconv

// minAtoiDigits is minFastDigits (16) on s390x. Unlike amd64, the
// z/Architecture vector digit-fold kernel (~12 ns for a 16-digit fold, measured
// on real IBM z15, go1.26.4, count=6) is FASTER than strconv.Atoi's inlined
// scalar fast path (17.8 ns at 16 digits, 26.5 ns at 18), so Atoi should take
// the SIMD path from 16 digits up rather than delegate. With the amd64 floor of
// 19, Atoi on s390x delegated 16..18-digit inputs to strconv.Atoi and paid a
// non-inlined wrapper frame on top, measuring ~0.92x — a false-win regression.
// Lowering the floor to 16 turns those into 1.46x (16d), 1.89x (17d) and 2.19x
// (18d) wins while keeping the 4.1x win at 19 digits. Correctness is unchanged:
// FuzzAtoi and TestAtoiTable still gate every result against strconv.Atoi. This
// is an s390x-only tuning; no other architecture is affected.
const minAtoiDigits = minFastDigits
