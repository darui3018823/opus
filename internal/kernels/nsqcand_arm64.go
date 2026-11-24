//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func nsqCand4NEON(c *NSQCand4)

func nsqCand4Arch(c *NSQCand4) bool {
	if !useNEON {
		return false
	}
	nsqCand4NEON(c)
	return true
}
