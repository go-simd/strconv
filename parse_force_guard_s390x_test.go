//go:build s390x

package strconv

// simdKernelForceSafe reports whether the raw vector kernel may be invoked
// directly on this host. The z/Architecture vector facility is present on every
// s390x target we build for (and the QEMU CI model enables it), so the raw
// kernel is always safe to call directly here.
func simdKernelForceSafe() bool { return true }
