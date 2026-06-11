//go:build ignore

// Command gen produces parse_amd64.s with go-asmgen: a vectorised base-10
// 16-digit decimal parser used by the SIMD fast path of ParseUint / Atoi /
// ParseInt. It is the SIMD core of the Lemire "parse a number at a gigabyte per
// second" technique, restricted to the unambiguous clean case (exactly sixteen
// ASCII '0'..'9' bytes, base 10). The Go wrappers (strconv.go) handle sign,
// digit-run detection, the short 0..3-digit tail and overflow, and fall back to
// the standard library for everything else, so the value AND the
// *strconv.NumError are byte-identical to strconv.
//
// Signature: parse16SSE(p *byte) (val, ok uint64) — reads the 16 bytes at p
// (the caller guarantees at least 16 readable bytes there); ok = 1 and val = the
// sixteen-digit value when every byte is an ASCII decimal digit, else ok = 0 (val
// unspecified). A *byte (not a slice) is used so the hot path needs no []byte(s)
// allocation: the caller passes unsafe.StringData(s) directly.
//
// One call handles a whole 16-digit group, so the dominant 16-digit ParseUint
// case is a SINGLE assembly CALL (the previous 8-digit kernel needed two calls,
// each with its own register-spill / frame-reload overhead — the per-call cost,
// not the digit crunch, dominated). 17..19-digit inputs are one parse16SSE call
// plus a 1..3-digit scalar tail in Go.
//
// Method (all instructions verified present in cmd/asm: PSUBB, PCMPGTB,
// PMADDUBSW, PMADDWL, PMOVMSKB). PMADDWL is Go's mnemonic for SSE2 pmaddwd; it
// fuses the staged word->dword fold in one instruction (the older kernel used a
// PMULLW+PHADDW pair under the mistaken belief pmaddwd was unavailable):
//
//   - Load the 16 chars into X0 (MOVOU); the bytes stay packed for the range
//     check and the PMADDUBSW fuse.
//   - Validate: with the chars still as bytes, a char is a decimal digit iff
//     ('0'-1) < c AND c < ('9'+1) under signed PCMPGTB (all digit chars < 0x80,
//     sign bit clear). PMOVMSKB of the per-byte isDigit mask must be 0xFFFF
//     (all sixteen lanes valid), else the kernel returns ok = 0.
//   - Fuse digits -> value:
//     PSUBB '0'                          : 16 chars -> 16 digit bytes (0..9).
//     PMADDUBSW [10,1,10,1,...]          : 16 bytes -> 8 words, each
//     10*d_hi + d_lo  (a 2-digit value, <100).
//     PMADDWL   [100,1,100,1,...]        : 8 words -> 4 dwords, each
//     100*w_hi + w_lo (a 4-digit value, <10000).
//     The four 4-digit dwords c0..c3 (c0 most significant) are extracted to GP
//     registers and combined as
//       hi = c0*10000 + c1 ; lo = c2*10000 + c3 ; val = hi*1e8 + lo
//     with three multiplies — cheap, and keeps the asm trivially correct.
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

	// madd1: PMADDUBSW multiplier [10,1,10,1,...] fusing each (tens,units) byte
	// pair into one word (16 bytes -> 8 words).
	madd1 := make([]byte, 16)
	for i := 0; i < 16; i += 2 {
		madd1[i] = 10
		madd1[i+1] = 1
	}
	mw1 := f.Data("madd1", madd1)

	// madd2: PMADDWL (pmaddwd) multiplier [100,1,100,1,...] fusing each
	// (hundreds,units) word pair into one dword (8 words -> 4 dwords).
	madd2 := make([]byte, 16)
	for i := 0; i < 16; i += 4 {
		// little-endian 16-bit 100 in the first lane, 1 in the second.
		madd2[i] = 100
		madd2[i+1] = 0
		madd2[i+2] = 1
		madd2[i+3] = 0
	}
	mw2 := f.Data("madd2", madd2)

	genSSE(f, cLo, cHi, sub, mw1, mw2)

	if err := os.WriteFile("parse_amd64.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote parse_amd64.s")
}

func genSSE(f *emit.File, cLo, cHi, sub, madd1, madd2 string) {
	b := amd64.NewFunc("parse16SSE", sig(), 0)
	b.LoadArg("p", "SI")

	// Load 16 chars into X0.
	b.Raw("MOVOU (SI), X0")

	// Validate all 16 lanes: ge0 = (c > '0'-1), le9 = (('9'+1) > c).
	b.Raw("MOVO X0, X3").Raw("MOVOU %s+0(SB), X4", cLo).Raw("PCMPGTB X4, X3") // X3 = c >= '0'
	b.Raw("MOVOU %s+0(SB), X4", cHi).Raw("PCMPGTB X0, X4")                    // X4 = c <= '9'
	b.Raw("PAND X4, X3")                                                      // X3 = isDigit (per byte)
	b.Raw("PMOVMSKB X3, AX")
	b.Raw("CMPL AX, $0xFFFF").Raw("JNE bad") // all sixteen lanes must be digits

	// Fuse: digits = c - '0', then the staged madd fold.
	b.Raw("MOVOU %s+0(SB), X2", sub).Raw("PSUBB X2, X0")        // X0 = 16 digit bytes (0..9)
	b.Raw("MOVOU %s+0(SB), X2", madd1).Raw("PMADDUBSW X0, X2")  // X2 = 8 words: 2-digit values
	b.Raw("MOVOU %s+0(SB), X4", madd2).Raw("PMADDWL X4, X2")    // X2 = 4 dwords: 4-digit values c0..c3

	// Extract the four 4-digit dwords (c0 most significant) and combine.
	b.Raw("MOVL X2, AX")     // AX = c0 (most significant 4 digits)
	b.Raw("PEXTRD $1, X2, CX") // CX = c1
	b.Raw("PEXTRD $2, X2, DX") // DX = c2
	b.Raw("PEXTRD $3, X2, DI") // DI = c3 (least significant 4 digits)
	b.Raw("IMULQ $10000, AX")  // hi = c0*10000 + c1
	b.Raw("ADDQ CX, AX")
	b.Raw("IMULQ $10000, DX")  // lo = c2*10000 + c3
	b.Raw("ADDQ DI, DX")
	b.Raw("IMULQ $100000000, AX") // val = hi*1e8 + lo
	b.Raw("ADDQ DX, AX")

	b.StoreRet("AX", "val")
	b.Raw("MOVQ $1, AX").StoreRet("AX", "ok")
	b.Ret()

	b.Label("bad")
	b.Raw("XORQ AX, AX").StoreRet("AX", "val").StoreRet("AX", "ok")
	b.Ret()

	f.Add(b.Func())
}
