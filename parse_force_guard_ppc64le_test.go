//go:build ppc64le

package strconv

import "golang.org/x/sys/cpu"

// simdKernelForceSafe reports whether the raw VSX kernel may be invoked directly
// on this host. The kernel emits ISA-3.0 (POWER9) instructions (LXVB16X) that
// raise SIGILL on POWER8, so a direct call is only safe on POWER9+.
func simdKernelForceSafe() bool { return cpu.PPC64.IsPOWER9 }
