//go:build amd64 && !purego

package kernels

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)

func xgetbv() (eax, edx uint32)

// cpuHasAVX reports whether the CPU has AVX and the OS saves the YMM state
// (CPUID.1:ECX.AVX and OSXSAVE, and XCR0 bits 1 and 2).
func cpuHasAVX() bool {
	maxID, _, _, _ := cpuid(0, 0)
	if maxID < 1 {
		return false
	}
	_, _, ecx1, _ := cpuid(1, 0)
	const osxsave = 1 << 27
	const avx = 1 << 28
	if ecx1&osxsave == 0 || ecx1&avx == 0 {
		return false
	}
	xcr0, _ := xgetbv()
	return xcr0&6 == 6
}
