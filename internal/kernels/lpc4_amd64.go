//go:build amd64 && !purego

package kernels

//go:noescape
func lpcPred4SSE41(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)

//go:noescape
func lpcPred2SSE41(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)

func lpc4Arch(p *LPC4, i int, out *[4]int32) bool {
	if !useSSE41 {
		return false
	}
	if p.Order < 1 || p.Order > 16 {
		panic("kernels: bad LPC order")
	}
	if p.Lanes == 2 {
		lpcPred2SSE41(&p.H[i+15], &p.A, p.Order, out)
	} else {
		lpcPred4SSE41(&p.H[i+15], &p.A, p.Order, out)
	}
	return true
}
