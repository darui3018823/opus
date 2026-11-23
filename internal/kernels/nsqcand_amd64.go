//go:build amd64 && !purego

package kernels

//go:noescape
func nsqCand4SSE41(c *NSQCand4)

func nsqCand4Arch(c *NSQCand4) bool {
	if !useSSE41 {
		return false
	}
	nsqCand4SSE41(c)
	return true
}
