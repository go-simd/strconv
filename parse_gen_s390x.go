//go:build ignore

// Command gen produces parse_s390x.s with go-asmgen: the IBM z/Architecture
// vector-facility port of the amd64 parse16 16-digit decimal fold used by the
// SIMD fast path of ParseUint / Atoi / ParseInt AND (via parse16Window)
// ParseFloat. The vector facility is assumed present (z13+), so there is no
// runtime feature dispatch — the build tag selects this kernel.
//
// Signature: parse16(p *byte) (val, ok uint64) — identical to the amd64 kernel.
// Reads the 16 bytes at p (caller guarantees >= 16 readable bytes); ok = 1 and
// val = the sixteen-digit value when every byte is an ASCII decimal digit, else
// ok = 0.
//
// BIG-ENDIAN lane order (the WAVE-1 s390x hazard, handled here). s390x is a
// big-endian machine AND its vector facility numbers elements big-endian:
// element 0 is the leftmost / most significant. VL loads memory byte i into
// element i, so the digit string "d0 d1 ... d15" read left-to-right (d0 the most
// significant digit, lowest address) lands with d0 in element 0. The staged
// even/odd multiply fold below therefore assigns the power-of-ten weights with
// the most-significant weight in the even (lower-numbered) element, exactly as
// the digit order demands. A position-dependent qemu test (parse a known
// 16-digit number -> its known value) pins this assignment; an inverted weight
// order would parse the digits in reverse and the test would fail.
//
// Method (mnemonics confirmed in cmd/internal/obj/s390x: VL, VSB, VCHLB,
// VMLEB/VMLOB, VMLEH/VMLOH, VMLEF/VMLOF, VAH/VAF/VAG, VLGVG). z's vector unit
// has no fused multiply-add-across for this shape, so — like the POWER port and
// mirroring the amd64 PMADDUBSW/PMADDWL pair — each fold stage is an even/odd
// widening multiply pair followed by an add:
//
//   VSB '0'                              : 16 chars -> 16 digit bytes (0..9).
//   validate: after the wrapping byte subtract every non-digit char becomes
//     > 9, so a single unsigned VCHLB(digit, 9) flags every invalid byte; any
//     nonzero lane => ok = 0.
//   stage1 (bytes->halfwords): VMLEB d,[10,1,..] + VMLOB d,[10,1,..] -> 8
//     halfwords, each 10*d_even + d_odd (a 2-digit value).
//   stage2 (halfwords->words): VMLEH hw,[100,1,..] + VMLOH -> 4 words, each
//     100*hw_even + hw_odd (a 4-digit value).
//   stage3 (words->dwords): VMLEF w,[10000,1,..] + VMLOF -> 2 dwords, each
//     10000*w_even + w_odd (an 8-digit value).
//   combine: VLGVG extracts dword element 0 (most significant 8 digits) and
//     dword element 1 (least significant 8 digits) to GPRs; the value is
//     dwHi*1e8 + dwLo.
//
// Run: go run parse_gen_s390x.go
package main

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/emit"
	"github.com/go-asmgen/asmgen/s390x"
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
// and `odd` in the odd element slots, for an element width of `width` bytes,
// laid out in big-endian element order (element 0 first, most significant), so
// the most-significant decimal weight sits in the even element a VMLE* reads.
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
	f := emit.NewFile("s390x")

	sub := f.Data("subD", repByte('0', 16))
	nine := f.Data("nine", repByte(9, 16))
	w1 := f.Data("w1b", weights16(1, 10, 1))    // bytes:     [10,1,...]
	w2 := f.Data("w2h", weights16(2, 100, 1))   // halfwords: [100,1,...]
	w3 := f.Data("w3w", weights16(4, 10000, 1)) // words:     [10000,1,...]

	b := s390x.NewFunc("parse16", sig(), 0)
	b.LoadArg("p", "R1")

	// loadConst loads a 16-byte DATA symbol into Vreg via VL, using R2 as scratch.
	loadConst := func(sym string, vreg int) {
		b.Raw("MOVD $%s(SB), R2", sym)
		b.Raw("VL (R2), V%d", vreg)
	}

	b.Raw("VL (R1), V0") // V0 = 16 chars (byte i -> element i, element 0 = MSdigit)
	loadConst(sub, 1)    // V1 = '0' x16
	loadConst(nine, 2)   // V2 = 9 x16
	loadConst(w1, 3)     // V3 = byte weights
	loadConst(w2, 4)     // V4 = halfword weights
	loadConst(w3, 5)     // V5 = word weights

	// digits = chars - '0' (byte-wise, modulo).
	b.Raw("VSB V1, V0, V0") // V0 = V0 - V1

	// validate: invalid := digit >u 9.
	b.Raw("VCHLB V0, V2, V6") // V6 lane = 0xFF where V0 > V2 (>9), else 0x00
	b.Raw("VLGVG $0, V6, R3") // R3 = mask dword 0
	b.Raw("VLGVG $1, V6, R4") // R4 = mask dword 1
	b.Raw("OR R3, R4, R3")
	b.Raw("CMPBNE R3, $0, bad") // any invalid lane -> ok = 0

	// stage1: bytes -> halfwords. even*10 + odd*1.
	b.Raw("VMLEB V0, V3, V7") // V7 = even bytes * 10 (halfwords)
	b.Raw("VMLOB V0, V3, V8") // V8 = odd  bytes * 1  (halfwords)
	b.Raw("VAH V7, V8, V9")   // V9 = 8 halfwords (2-digit values)

	// stage2: halfwords -> words. even*100 + odd*1.
	b.Raw("VMLEH V9, V4, V10") // V10 = even halfwords * 100 (words)
	b.Raw("VMLOH V9, V4, V11") // V11 = odd  halfwords * 1   (words)
	b.Raw("VAF V10, V11, V12") // V12 = 4 words (4-digit values)

	// stage3: words -> dwords. even*10000 + odd*1.
	b.Raw("VMLEF V12, V5, V13") // V13 = even words * 10000 (dwords)
	b.Raw("VMLOF V12, V5, V14") // V14 = odd  words * 1     (dwords)
	b.Raw("VAG V13, V14, V15")  // V15 = 2 dwords (8-digit values), elem0 = high

	// combine: dwHi = element 0 (most significant 8 digits), dwLo = element 1.
	b.Raw("VLGVG $0, V15, R3") // R3 = dwHi
	b.Raw("VLGVG $1, V15, R4") // R4 = dwLo
	b.Raw("MOVD $100000000, R5")
	b.Raw("MULLD R5, R3, R3") // R3 = dwHi * 1e8
	b.Raw("ADD R4, R3, R3")   // R3 = dwHi*1e8 + dwLo

	b.StoreRet("R3", "val")
	b.Raw("MOVD $1, R3")
	b.StoreRet("R3", "ok")
	b.Ret()

	b.Label("bad")
	b.Raw("MOVD $0, R3")
	b.StoreRet("R3", "val")
	b.StoreRet("R3", "ok")
	b.Ret()

	f.Add(b.Func())
	if err := os.WriteFile("parse_s390x.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote parse_s390x.s")
}
