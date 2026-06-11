//go:build ignore

// Command gen produces parse_amd64.s with go-asmgen: a vectorised base-10
// 8-digit decimal parser used by the SIMD fast path of ParseUint / Atoi /
// ParseInt. It is the SIMD core of the Lemire "parse a number at a gigabyte per
// second" technique, restricted to the unambiguous clean case (exactly eight
// ASCII '0'..'9' bytes, base 10). The Go wrappers (strconv.go) handle sign,
// digit-run detection, chunking and overflow, and fall back to the standard
// library for everything else, so the value AND the *strconv.NumError are
// byte-identical to strconv.
//
// Signature: parse8SSE(p *byte) (val, ok uint64) — reads the 8 bytes at p (the
// caller guarantees at least 8 readable bytes there); ok = 1 and val = the
// eight-digit value when every byte is an ASCII decimal digit, else ok = 0 (val
// unspecified). A *byte (not a slice) is used so the hot path needs no
// []byte(s) allocation: the caller passes unsafe.StringData(s) directly.
//
// Method (all instructions verified present in cmd/asm: PSUBB, PCMPGTB,
// PMADDUBSW, PMULLW, PHADDW; the SSE2 PMADDWD form is NOT recognised by Go's
// assembler, so the word->dword fuse uses PMULLW+PHADDW instead):
//
//   - Load the 8 chars into the low half of X0 (MOVQ); the bytes stay packed
//     for the range check and the PMADDUBSW fuse.
//   - Validate: with the chars still as bytes, d = c-'0' (PSUBB). A char is a
//     decimal digit iff 0 <= d <= 9, i.e. (d > -1) AND (9 > d) under signed
//     PCMPGTB (all digit chars < 0x80 so the sign bit is clear). If any of the
//     eight lanes is out of range, PMOVMSKB of the "bad" mask is non-zero and
//     the kernel returns ok = 0.
//   - Fuse digits -> value:
//     PMADDUBSW [10,1,10,1,10,1,10,1]  : 8 digit-bytes -> 4 words, each
//     10*d_hi + d_lo  (a 2-digit value, <100).
//     PMULLW    [100,1,100,1]          : scale the high word of each adjacent
//     pair by 100.
//     PHADDW                            : add adjacent words -> 2 words, each a
//     4-digit value (<10000, fits a word).
//     The final  hi*10000 + lo  combine would overflow 16 bits, so the two
//     4-digit words are returned to Go (PEXTRW) and combined there with one
//     32-bit multiply. This keeps the asm trivially correct.
//
// Run: go run parse_gen.go
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/amd64"
	"github.com/go-asmgen/asmgen/emit"
)

func repByte(x byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = x
	}
	return b
}

func sig() abi.Signature {
	return abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("p", abi.Ptr)},
		[]abi.Arg{abi.Scalar("val", abi.Uint64), abi.Scalar("ok", abi.Uint64)},
	)
}

func main() {
	f := emit.NewFile("amd64")

	cLo := f.Data("cLo", repByte('0'-1, 16)) // PCMPGTB c, cLo => c >= '0'  i.e. d >= 0
	cHi := f.Data("cHi", repByte('9'+1, 16)) // PCMPGTB cHi, c => c <= '9'  i.e. d <= 9
	sub := f.Data("subD", repByte('0', 16))

	// madd: PMADDUBSW multiplier [10,1,10,1,...] fusing each (tens,units) byte
	// pair into one word.
	madd := make([]byte, 16)
	for i := 0; i < 16; i += 2 {
		madd[i] = 10
		madd[i+1] = 1
	}
	mw := f.Data("madd", madd)

	// mul100: PMULLW multiplier [100,1,100,1,...] scaling the high word of each
	// adjacent word pair by 100 before the PHADDW.
	mul := make([]byte, 16)
	for i := 0; i < 16; i += 4 {
		// little-endian 16-bit 100 in the first lane, 1 in the second.
		mul[i] = 100
		mul[i+1] = 0
		mul[i+2] = 1
		mul[i+3] = 0
	}
	mulw := f.Data("mul100", mul)

	genSSE(f, cLo, cHi, sub, mw, mulw)

	if err := os.WriteFile("parse_amd64.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote parse_amd64.s")
}

func genSSE(f *emit.File, cLo, cHi, sub, madd, mul100 string) {
	b := amd64.NewFunc("parse8SSE", sig(), 0)
	b.LoadArg("p", "SI")

	// Load 8 chars into low half of X0.
	b.Raw("MOVQ (SI), X0")

	// Validate: d = c - '0' (as bytes) in X1; bad lanes if d<0 or d>9.
	b.Raw("MOVO X0, X1").Raw("MOVOU %s+0(SB), X2", sub).Raw("PSUBB X2, X1") // X1 = digit values (bytes), junk in high 8 lanes
	// ge0 = (c > '0'-1)  -> PCMPGTB c, ('0'-1)
	b.Raw("MOVO X0, X3").Raw("MOVOU %s+0(SB), X4", cLo).Raw("PCMPGTB X4, X3") // X3 = c >= '0'
	// le9 = (('9'+1) > c) -> PCMPGTB ('9'+1), c
	b.Raw("MOVOU %s+0(SB), X4", cHi).Raw("PCMPGTB X0, X4") // X4 = c <= '9'
	b.Raw("PAND X4, X3")                                   // X3 = isDigit (per byte)
	// Only the low 8 lanes matter; force the high 8 lanes valid by masking the
	// "bad" detection to the low 8 bytes. We build bad = ~isDigit, then test only
	// the low 8 bits of PMOVMSKB.
	b.Raw("PCMPEQB X5, X5").Raw("PXOR X5, X3") // X3 = bad = ~isDigit
	b.Raw("PMOVMSKB X3, AX")
	b.Raw("ANDL $0xFF, AX") // low 8 lanes only
	b.Raw("TESTL AX, AX").Raw("JNZ bad")

	// Fuse: X1 holds 8 digit bytes (0..9) in the low half (high half is junk but
	// we only consume the low 8 via PMADDUBSW's first 8 lanes -> 4 words).
	b.Raw("MOVOU %s+0(SB), X2", madd).Raw("PMADDUBSW X2, X1") // 4 words (low 64 bits): 2-digit values
	b.Raw("MOVOU %s+0(SB), X2", mul100).Raw("PMULLW X2, X1")  // high word of each pair *100
	b.Raw("PHADDW X1, X1")                                    // adjacent add -> 2 words: 4-digit values (lanes 0,1)

	// Extract the two 4-digit halves and combine: val = hi*10000 + lo.
	b.Raw("PEXTRW $0, X1, AX") // AX = high 4 digits (most significant)
	b.Raw("PEXTRW $1, X1, DX") // DX = low 4 digits
	b.Raw("IMULQ $10000, AX")
	b.Raw("ADDQ DX, AX")

	b.StoreRet("AX", "val")
	b.Raw("MOVQ $1, AX").StoreRet("AX", "ok")
	b.Ret()

	b.Label("bad")
	b.Raw("XORQ AX, AX").StoreRet("AX", "val").StoreRet("AX", "ok")
	b.Ret()

	f.Add(b.Func())
}
