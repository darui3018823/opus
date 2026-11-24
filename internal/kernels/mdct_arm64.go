//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func imdctPreNEON(fout *float32, x *float64, trig *float32, bitrev *int, n4, blocks int)

//go:noescape
func imdctPostNEON(buf *float64, fout, trig *float32, n4, blocks int)

func imdctPreArch(fout []float32, x []float64, trig []float32, bitrev []int, n4 int) int {
	if !useNEON || n4 < 4 {
		return 0
	}
	imdctPreNEON(&fout[0], &x[0], &trig[0], &bitrev[0], n4, n4/4)
	return n4 &^ 3
}

func imdctPostArch(buf []float64, fout, trig []float32, n4 int) int {
	blocks := ((n4 + 1) / 2) / 4
	if !useNEON || blocks == 0 || n4%4 != 0 {
		return 0
	}
	imdctPostNEON(&buf[0], &fout[0], &trig[0], n4, blocks)
	return blocks * 4
}
