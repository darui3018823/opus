package celt

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/darui3018823/opus/internal/dsp"
)

type oracleIMDCTBlock struct {
	ch      int
	block   int
	samples []float64
}

// TestOracleCLTMDCTBackwardAgainstGoIMDCT compares libopus clt_mdct_backward()
// raw TDAC-input time[] dumps against Go's CELTMode.IMDCT() for the same oracle
// [XD] denormalized MDCT coefficients.
//
// It is opt-in because it shells out to the locally built oracle:
//
//	$env:OPUS_ORACLE_IMDCT_COMPARE=1
//	go test ./internal/celt -run TestOracleCLTMDCTBackwardAgainstGoIMDCT -v
func TestOracleCLTMDCTBackwardAgainstGoIMDCT(t *testing.T) {
	if os.Getenv("OPUS_ORACLE_IMDCT_COMPARE") == "" {
		t.Skip("set OPUS_ORACLE_IMDCT_COMPARE=1 to run the oracle IMDCT comparison")
	}

	oracle := os.Getenv("OPUS_ORACLE")
	if oracle == "" {
		oracle = filepath.Join(os.TempDir(), "opusoracle", "oracle.exe")
	}
	if _, err := os.Stat(oracle); err != nil {
		t.Skipf("oracle not found at %s; run scripts/oracle/build.ps1 first", oracle)
	}

	bitPath := filepath.Join("..", "..", "testdata", "opus_newvectors", "testvector07.bit")
	out, err := exec.Command(oracle, bitPath, "0").CombinedOutput()
	if err != nil {
		t.Fatalf("oracle failed: %v\n%s", err, out)
	}

	coeffsByCh, blocks, err := parseOracleIMDCTTrace(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		t.Fatal("oracle trace has no [IMDCT_RAW] lines; rebuild oracle after updating scripts/oracle/build.ps1")
	}

	blocksPerChannel := make(map[int]int)
	for _, b := range blocks {
		if blocksPerChannel[b.ch] < b.block+1 {
			blocksPerChannel[b.ch] = b.block + 1
		}
	}

	var failures int
	for _, b := range blocks {
		coeffs := coeffsByCh[b.ch]
		if len(coeffs) == 0 {
			t.Fatalf("missing [XD] coefficients for channel %d", b.ch)
		}

		M := blocksPerChannel[b.ch]
		subCoeffs, err := oracleBlockCoeffs(coeffs, M, b.block, len(b.samples))
		if err != nil {
			t.Fatalf("ch=%d block=%d: %v", b.ch, b.block, err)
		}

		mode := dsp.NewCELTMode(len(subCoeffs), MaxOverlap, celtWindow(MaxOverlap))
		got := mode.IMDCT(subCoeffs)
		maxIdx, maxErr, first := compareFloatSlices(got, b.samples, 1e-6)
		t.Logf("ch=%d block=%d N=%d maxErr=%.9g at sample=%d", b.ch, b.block, len(got), maxErr, maxIdx)
		if b.ch == 0 && b.block == 1 {
			t.Logf("block1 coeff[0:16]=%s", formatFloatPrefix(subCoeffs, 16))
			t.Logf("block1 go[0:16]=%s", formatFloatPrefix(got, 16))
			t.Logf("block1 oracle[0:16]=%s", formatFloatPrefix(b.samples, 16))
			for _, msg := range comparePattern("go", got, b.samples) {
				t.Logf("block1 %s", msg)
			}
			rev := reversedCopy(got)
			for _, msg := range comparePattern("reverse(go)", rev, b.samples) {
				t.Logf("block1 %s", msg)
			}
			for _, msg := range bestLagPattern(got, b.samples) {
				t.Logf("block1 %s", msg)
			}
			for _, msg := range halfBlockPatterns(got, b.samples) {
				t.Logf("block1 %s", msg)
			}
		}
		for _, msg := range first {
			t.Log(msg)
		}
		if maxErr > 1e-6 {
			failures++
		}
	}
	if failures > 0 {
		t.Fatalf("%d IMDCT block(s) differ from oracle clt_mdct_backward raw time[]", failures)
	}
}

func parseOracleIMDCTTrace(out []byte) (map[int][]float64, []oracleIMDCTBlock, error) {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	lineRE := regexp.MustCompile(`^\[(XD|IMDCT_RAW)\]\s+(.*)$`)
	fieldRE := regexp.MustCompile(`([A-Za-z]+)=(-?\d+)`)
	valueRE := regexp.MustCompile(`([XT])\[(\d+)\]=([-+0-9.eE]+)`)

	coeffsByCh := make(map[int][]float64)
	var blocks []oracleIMDCTBlock

	for _, rawLine := range bytes.Split(out, []byte{'\n'}) {
		line := strings.TrimSpace(ansi.ReplaceAllString(string(rawLine), ""))
		m := lineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		fields := make(map[string]int)
		for _, fm := range fieldRE.FindAllStringSubmatch(m[2], -1) {
			v, err := strconv.Atoi(fm[2])
			if err != nil {
				return nil, nil, err
			}
			fields[fm[1]] = v
		}

		ch := fields["ch"]
		n := fields["N"]
		values := make([]float64, n)
		for _, vm := range valueRE.FindAllStringSubmatch(m[2], -1) {
			i, err := strconv.Atoi(vm[2])
			if err != nil {
				return nil, nil, err
			}
			if i >= len(values) {
				return nil, nil, fmt.Errorf("%s index %d out of %d", vm[1], i, len(values))
			}
			v, err := strconv.ParseFloat(vm[3], 64)
			if err != nil {
				return nil, nil, err
			}
			values[i] = v
		}

		switch m[1] {
		case "XD":
			band := fields["band"]
			M := n / int(EBands48000[band+1]-EBands48000[band])
			start := M * int(EBands48000[band])
			coeffs := coeffsByCh[ch]
			if len(coeffs) < start+n {
				grown := make([]float64, start+n)
				copy(grown, coeffs)
				coeffs = grown
			}
			copy(coeffs[start:start+n], values)
			coeffsByCh[ch] = coeffs
		case "IMDCT_RAW":
			blocks = append(blocks, oracleIMDCTBlock{
				ch:      ch,
				block:   fields["block"],
				samples: values,
			})
		}
	}
	return coeffsByCh, blocks, nil
}

func oracleBlockCoeffs(coeffs []float64, blocks, block, n int) ([]float64, error) {
	if blocks <= 1 {
		sub := make([]float64, n)
		copy(sub, coeffs)
		return sub, nil
	}
	if block < 0 || block >= blocks {
		return nil, fmt.Errorf("invalid block %d for %d blocks", block, blocks)
	}
	sub := make([]float64, n)
	for i := range sub {
		idx := block + i*blocks
		if idx < len(coeffs) {
			sub[i] = coeffs[idx]
		}
	}
	return sub, nil
}

func compareFloatSlices(got, want []float64, tol float64) (maxIdx int, maxErr float64, first []string) {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	maxIdx = -1
	for i := 0; i < n; i++ {
		err := math.Abs(got[i] - want[i])
		if err > maxErr {
			maxErr = err
			maxIdx = i
		}
		if err > tol && len(first) < 16 {
			first = append(first, fmt.Sprintf("sample[%d]: go=%.17g oracle=%.17g diff=%.9g", i, got[i], want[i], err))
		}
	}
	if len(got) != len(want) {
		first = append(first, fmt.Sprintf("length mismatch: go=%d oracle=%d", len(got), len(want)))
	}
	return maxIdx, maxErr, first
}

func formatFloatPrefix(v []float64, n int) string {
	if len(v) < n {
		n = len(v)
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("%.9g", v[i])
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func comparePattern(name string, got, want []float64) []string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	dotGW, dotGG, dotWW := 0.0, 0.0, 0.0
	for i := 0; i < n; i++ {
		dotGW += got[i] * want[i]
		dotGG += got[i] * got[i]
		dotWW += want[i] * want[i]
	}
	scale := 0.0
	if dotGG != 0 {
		scale = dotGW / dotGG
	}
	rmse := scaledRMSE(got[:n], want[:n], scale)
	corr := 0.0
	if dotGG > 0 && dotWW > 0 {
		corr = dotGW / math.Sqrt(dotGG*dotWW)
	}
	return []string{
		fmt.Sprintf("%s: lsScale=%.9g corr=%.9g scaledRMSE=%.9g", name, scale, corr, rmse),
	}
}

func bestLagPattern(got, want []float64) []string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	bestLag, bestScale, bestRMSE, bestCorr := 0, 0.0, math.Inf(1), 0.0
	bestOverlap := 0
	for lag := -n + 1; lag < n; lag++ {
		var x, y []float64
		if lag >= 0 {
			x = got[:n-lag]
			y = want[lag:n]
		} else {
			x = got[-lag:n]
			y = want[:n+lag]
		}
		if len(x) < n/2 {
			continue
		}
		dotXY, dotXX, dotYY := 0.0, 0.0, 0.0
		for i := range x {
			dotXY += x[i] * y[i]
			dotXX += x[i] * x[i]
			dotYY += y[i] * y[i]
		}
		scale := 0.0
		if dotXX != 0 {
			scale = dotXY / dotXX
		}
		rmse := scaledRMSE(x, y, scale)
		corr := 0.0
		if dotXX > 0 && dotYY > 0 {
			corr = dotXY / math.Sqrt(dotXX*dotYY)
		}
		if rmse < bestRMSE {
			bestLag, bestScale, bestRMSE, bestCorr, bestOverlap = lag, scale, rmse, corr, len(x)
		}
	}
	return []string{
		fmt.Sprintf("bestLag: lag=%d overlap=%d lsScale=%.9g corr=%.9g scaledRMSE=%.9g", bestLag, bestOverlap, bestScale, bestCorr, bestRMSE),
	}
}

func scaledRMSE(got, want []float64, scale float64) float64 {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	if n == 0 {
		return 0
	}
	sse := 0.0
	for i := 0; i < n; i++ {
		d := scale*got[i] - want[i]
		sse += d * d
	}
	return math.Sqrt(sse / float64(n))
}

func reversedCopy(v []float64) []float64 {
	out := make([]float64, len(v))
	for i := range v {
		out[i] = v[len(v)-1-i]
	}
	return out
}

func halfBlockPatterns(got, want []float64) []string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	if n%2 != 0 || n == 0 {
		return nil
	}
	h := n / 2
	cases := []struct {
		name string
		x    []float64
		y    []float64
	}{
		{"oracle[0:h] vs go[h:n]", got[h:n], want[:h]},
		{"oracle[h:n] vs go[0:h]", got[:h], want[h:n]},
		{"oracle[h:n] vs reverse(go[0:h])", reversedCopy(got[:h]), want[h:n]},
		{"oracle[0:h] vs reverse(go[h:n])", reversedCopy(got[h:n]), want[:h]},
	}
	out := make([]string, 0, len(cases))
	for _, c := range cases {
		out = append(out, comparePattern(c.name, c.x, c.y)...)
	}
	return out
}
