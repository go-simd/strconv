//go:build ignore

// Command gen produces parse_ppc64le.s with go-asmgen: the POWER VSX port of the
// amd64 parse16 16-digit decimal fold used by the SIMD fast path of ParseUint /
// Atoi / ParseInt AND (via parse16Window) ParseFloat. The kernel uses ISA-3.0
// (POWER9) instructions (LXVB16X), which raise SIGILL on POWER8, so the
// dispatcher gates it on cpu.PPC64.IsPOWER9 and falls back to the scalar digit
// loop on POWER8.
//
// Signature: parse16(p *byte) (val, ok uint64) — identical to the amd64 kernel.
// Reads the 16 bytes at p (caller guarantees >= 16 readable bytes); ok = 1 and
// val = the sixteen-digit value when every byte is an ASCII decimal digit, else
// ok = 0.
//
// Endianness / lane order. The kernel loads the digit string with LXVB16X, the
// byte-indexed VSX load: memory byte i lands in vector ELEMENT i under the
// architectural (big-endian) element numbering POWER's vector ALU uses for ALL
// instructions regardless of the machine's memory endianness. So the most
// significant digit d0 (lowest address) is element 0, and the staged even/odd
// multiply fold below is written once against that fixed element order and is
// therefore valid on ppc64le. A position-dependent qemu test (parse a known
// 16-digit number -> known value) pins this.
//
// VSX↔AltiVec aliasing (a WAVE-1 hazard): an AltiVec register Vn is the SAME
// physical register as VSX register VS(32+n), NOT VSn. LXVB16X writes a VS
// register, so every load targets VS(32+k) and the AltiVec arithmetic then
// names that same register Vk.
//
// Method (all mnemonics verified in cmd/asm testdata/ppc64.s: LXVB16X,
// VSUBUBM, VCMPGTUB, VMULEUB/VMULOUB, VMULEUH/VMULOUH, VMULEUW/VMULOUW,
// VADDUHM/VADDUWM/VADDUDM, MFVSRD, VSLDOI). The POWER vector unit has no single
// pmaddwd, so each fold stage is a pair of even/odd element multiplies (which
// widen) followed by an add — the same arithmetic as the amd64 PMADDUBSW /
// PMADDWL pair:
//
//	VSUBUBM '0'                          : 16 chars -> 16 digit bytes (0..9).
//	validate: a non-digit char, after the wrapping byte subtract, always
//	  becomes > 9 (chars < '0' wrap high, chars > '9' give 10..), so a single
//	  unsigned VCMPGTUB(digit, 9) flags every invalid byte; any nonzero lane
//	  => ok = 0.
//	stage1 (bytes->halfwords): VMULEUB d,[10,1,..] + VMULOUB d,[10,1,..]
//	  -> 8 halfwords, each 10*d_even + d_odd  (a 2-digit value).
//	stage2 (halfwords->words): VMULEUH hw,[100,1,..] + VMULOUH -> 4 words,
//	  each 100*hw_even + hw_odd (a 4-digit value).
//	stage3 (words->dwords): VMULEUW w,[10000,1,..] + VMULOUW -> 2 dwords,
//	  each 10000*w_even + w_odd (an 8-digit value).
//	combine: the two dwords (dwHi most significant 8 digits, dwLo least) are
//	  moved to GPRs (MFVSRD reads VSR doubleword 0; VSLDOI $8 rotates the second
//	  dword into place) and combined as dwHi*1e8 + dwLo.
//
// Run: go run parse_gen_ppc64.go
package main

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/emit"
	"github.com/go-asmgen/asmgen/ppc64"
)

func sig() abi.Signature {
	return abi.LayoutArgs(
		[]abi.Arg{abi.Scalar("p", abi.Ptr)},
		[]abi.Arg{abi.Scalar("val", abi.Uint64), abi.Scalar("ok", abi.Uint64)},
	)
}

func repByte(x byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = x
	}
	return b
}

// weights16 builds a 16-byte constant holding `even` in the even element slots
// and `odd` in the odd element slots, for an element width of `width` bytes. The
// elements are laid out in architectural element order (element 0 first), each
// stored big-endian, matching the LXVB16X element numbering the fold uses.
func weights16(width int, even, odd uint64) []byte {
	b := make([]byte, 16)
	n := 16 / width
	for e := 0; e < n; e++ {
		v := odd
		if e%2 == 0 {
			v = even
		}
		off := e * width
		switch width {
		case 2:
			binary.BigEndian.PutUint16(b[off:], uint16(v))
		case 4:
			binary.BigEndian.PutUint32(b[off:], uint32(v))
		default:
			b[off] = byte(v)
		}
	}
	return b
}

func main() {
	f := emit.NewFile("ppc64le")

	sub := f.Data("subD", repByte('0', 16))
	nine := f.Data("nine", repByte(9, 16))
	w1 := f.Data("w1b", weights16(1, 10, 1))    // bytes:     [10,1,...]
	w2 := f.Data("w2h", weights16(2, 100, 1))   // halfwords: [100,1,...]
	w3 := f.Data("w3w", weights16(4, 10000, 1)) // words:     [10000,1,...]

	b := ppc64.NewFunc("parse16", sig(), 0)
	b.LoadArg("p", "R3")

	// loadConst loads a 16-byte DATA symbol into V(reg) via its alias VS(32+reg),
	// using R4 as a scratch base. R0 reads as constant 0, giving (base+0).
	loadConst := func(sym string, vreg int) {
		b.Raw("MOVD $%s(SB), R4", sym)
		b.Raw("LXVB16X (R4)(R0), VS%d", 32+vreg)
	}

	// Load digit chars into V0, constants into V1..V5.
	b.Raw("LXVB16X (R3)(R0), VS32") // V0 = 16 chars
	loadConst(sub, 1)               // V1 = '0' x16
	loadConst(nine, 2)              // V2 = 9 x16
	loadConst(w1, 3)                // V3 = byte weights
	loadConst(w2, 4)                // V4 = halfword weights
	loadConst(w3, 5)                // V5 = word weights

	// digits = chars - '0'. Go's VX-form 3-register order is VRA, VRB, VRT, so
	// VSUBUBM V0, V1, V0 computes V0 = V0 - V1 (chars - '0'), byte-wise modulo.
	b.Raw("VSUBUBM V0, V1, V0") // V0 = digit bytes (0..9 for valid, >9 wrapped for invalid)

	// validate: invalid := digit >u 9. VCMPGTUB VRA, VRB, VRT sets VRT = (VRA >u
	// VRB), so VCMPGTUB V0, V2 yields (digit >u 9).
	b.Raw("VCMPGTUB V0, V2, V6") // V6 = invalid mask (0xFF where digit > 9)
	b.Raw("MFVSRD VS38, R5")     // R5 = mask dword 0
	b.Raw("VSLDOI $8, V6, V6, V7")
	b.Raw("MFVSRD VS39, R6") // R6 = mask dword 1
	b.Raw("OR R5, R6, R5")
	b.Raw("CMP R5, $0")
	b.Raw("BNE bad") // any invalid lane -> ok = 0

	// stage1: bytes -> halfwords. even*10 + odd*1.
	b.Raw("VMULEUB V0, V3, V8")  // V8 = even bytes * 10  (halfwords)
	b.Raw("VMULOUB V0, V3, V9")  // V9 = odd  bytes * 1   (halfwords)
	b.Raw("VADDUHM V8, V9, V10") // V10 = 8 halfwords (2-digit values)

	// stage2: halfwords -> words. even*100 + odd*1.
	b.Raw("VMULEUH V10, V4, V11")  // V11 = even halfwords * 100 (words)
	b.Raw("VMULOUH V10, V4, V12")  // V12 = odd  halfwords * 1   (words)
	b.Raw("VADDUWM V11, V12, V13") // V13 = 4 words (4-digit values)

	// stage3: words -> dwords. even*10000 + odd*1.
	b.Raw("VMULEUW V13, V5, V14")  // V14 = even words * 10000 (dwords)
	b.Raw("VMULOUW V13, V5, V15")  // V15 = odd  words * 1     (dwords)
	b.Raw("VADDUDM V14, V15, V16") // V16 = 2 dwords (8-digit values), dw0 = high

	// combine: dwHi = dword 0 (most significant 8 digits), dwLo = dword 1.
	b.Raw("MFVSRD VS48, R5") // R5 = dwHi (V16 dword 0)
	b.Raw("VSLDOI $8, V16, V16, V17")
	b.Raw("MFVSRD VS49, R6") // R6 = dwLo (V16 dword 1)
	b.Raw("MOVD $100000000, R7")
	b.Raw("MULLD R7, R5, R5") // R5 = dwHi * 1e8
	b.Raw("ADD R6, R5, R5")   // R5 = dwHi*1e8 + dwLo

	b.StoreRet("R5", "val")
	b.Raw("MOVD $1, R5")
	b.StoreRet("R5", "ok")
	b.Ret()

	b.Label("bad")
	b.Raw("MOVD $0, R5")
	b.StoreRet("R5", "val")
	b.StoreRet("R5", "ok")
	b.Ret()

	f.Add(b.Func())
	if err := os.WriteFile("parse_ppc64le.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote parse_ppc64le.s")
}
