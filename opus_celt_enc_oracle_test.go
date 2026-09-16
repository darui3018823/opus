//go:build opusref

package opus

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The CELT encoder oracle: scripts/oracle/build_encoder.ps1 instruments
// libopus 1.6.1's celt_encode_with_ec to dump, per frame, the pre-emphasised
// input, the MDCT, the band energies, the frame decisions and the coarse
// energy position. TestCELTEncoderOracle feeds the Go encoder the same
// CELT-only configuration (RESTRICTED_LOWDELAY, 48 kHz, fullband, CBR) and
// reports how far each analysis stage is from libopus. It is the Phase 5
// baseline: the Go CELT encoder is not expected to be exact yet, so the test
// gates only the framing and reports the rest.

type celtOracleFrame struct {
	frame       int
	n, lm, c    int
	overlap     int
	silence     bool
	isTransient bool
	shortBlocks int
	tfEstimate  float32
	tfChan      int
	pfOn        bool
	tell        int
	tellCoarse  int
	tfSelect    int
	tfRes       []int
	// [CELT_ENC_TRIM] / [CELT_ENC_OFFSETS]
	tellFracTrim int
	spread       int
	allocTrim    int
	totalBoost   int
	offsets      []int
	// [CELT_ENC_ALLOC] and the per-band arrays
	tellFine        int
	bits            int
	antiCollapseRsv int
	codedBands      int
	intensity       int
	dualStereo      bool
	balance         int
	pulses          []int
	fineQuant       []int
	finePriority    []int
	// [CELT_ENC_FINAL]
	tellFinal  int
	oldBandE   []float32
	errorE     []float32
	in         []float32
	freq       []float32
	bandE      []float32
	bandLogE   []float32
	packet     []byte
	havePacket bool
}

var (
	celtOracleFrameRe  = regexp.MustCompile(`^\[CELT_ENC_FRAME\] N=(\d+) LM=(\d+) C=(\d+) CC=(\d+) overlap=(\d+) silence=(\d) isTransient=(\d) shortBlocks=(\d+) tf_estimate=(\S+) tf_chan=(\d+) pf_on=(\d) pitch_index=(-?\d+) gain1=(\S+) tapset=(\d+) tell=(\d+)`)
	celtOracleCoarseRe = regexp.MustCompile(`^\[CELT_ENC_COARSE\] tell=(\d+) intra=(\d) tf_select=(\d)`)
	celtOracleIntRe    = regexp.MustCompile(`v\[\d+\]=(-?\d+)`)
	celtOracleTrimRe   = regexp.MustCompile(`^\[CELT_ENC_TRIM\] tell_frac=(\d+) spread=(\d+) alloc_trim=(\d+) total_boost=(\d+)`)
	celtOracleAllocRe  = regexp.MustCompile(`^\[CELT_ENC_ALLOC\] tell=(\d+) bits=(-?\d+) anti_collapse_rsv=(\d+) codedBands=(\d+) intensity=(\d+) dual_stereo=(\d+) balance=(-?\d+)`)
	celtOracleFinalRe  = regexp.MustCompile(`^\[CELT_ENC_FINAL\] tell=(\d+)`)
)

func celtOracleInts(line string) []int {
	var out []int
	for _, m := range celtOracleIntRe.FindAllStringSubmatch(line, -1) {
		v, _ := strconv.Atoi(m[1])
		out = append(out, v)
	}
	return out
}

func runCELTEncOracle(t *testing.T, fixture string, frames, bitrate, complexity int, vbr bool, channels int) []celtOracleFrame {
	t.Helper()
	vbrArg := "0"
	if vbr {
		vbrArg = "1"
	}
	cmd := exec.Command(encOraclePath(), "--celt-enc", "48000", fixture, strconv.Itoa(frames), strconv.Itoa(bitrate), strconv.Itoa(complexity), vbrArg, strconv.Itoa(channels))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("enc_oracle --celt-enc %s: %v\n%s", fixture, err, stderr.String())
	}
	out := make([]celtOracleFrame, frames)
	cur := -1
	parseFloats := func(line string) []float32 {
		ms := encOracleValuesRe.FindAllStringSubmatch(line, -1)
		vals := make([]float32, len(ms))
		for i, m := range ms {
			f, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				t.Fatalf("parse %q: %v", m[1], err)
			}
			vals[i] = float32(f)
		}
		return vals
	}
	sc := bufio.NewScanner(strings.NewReader(stderr.String()))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "[CELT_ENC_INPUT_FRAME]"):
			cur++
			if cur >= frames {
				t.Fatalf("more frames than expected")
			}
			out[cur].frame = cur
		case cur < 0:
			continue
		case strings.HasPrefix(line, "[CELT_ENC_FRAME]"):
			m := celtOracleFrameRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad CELT_ENC_FRAME line: %s", line)
			}
			f := &out[cur]
			f.n, _ = strconv.Atoi(m[1])
			f.lm, _ = strconv.Atoi(m[2])
			f.c, _ = strconv.Atoi(m[3])
			f.overlap, _ = strconv.Atoi(m[5])
			f.silence = m[6] == "1"
			f.isTransient = m[7] == "1"
			f.shortBlocks, _ = strconv.Atoi(m[8])
			v, _ := strconv.ParseFloat(m[9], 64)
			f.tfEstimate = float32(v)
			f.tfChan, _ = strconv.Atoi(m[10])
			f.pfOn = m[11] == "1"
			f.tell, _ = strconv.Atoi(m[15])
		case strings.HasPrefix(line, "[SILK_CELT_ENC_IN]"):
			out[cur].in = parseFloats(line)
		case strings.HasPrefix(line, "[SILK_CELT_ENC_FREQ]"):
			out[cur].freq = parseFloats(line)
		case strings.HasPrefix(line, "[SILK_CELT_ENC_BANDE]"):
			out[cur].bandE = parseFloats(line)
		case strings.HasPrefix(line, "[SILK_CELT_ENC_BANDLOGE]"):
			out[cur].bandLogE = parseFloats(line)
		case strings.HasPrefix(line, "[CELT_ENC_COARSE]"):
			m := celtOracleCoarseRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad CELT_ENC_COARSE line: %s", line)
			}
			out[cur].tellCoarse, _ = strconv.Atoi(m[1])
			out[cur].tfSelect, _ = strconv.Atoi(m[3])
		case strings.HasPrefix(line, "[SILK_CELT_ENC_OLDBANDE]"):
			out[cur].oldBandE = parseFloats(line)
		case strings.HasPrefix(line, "[SILK_CELT_ENC_ERROR]"):
			out[cur].errorE = parseFloats(line)
		case strings.HasPrefix(line, "[CELT_ENC_TF_RES]"):
			out[cur].tfRes = celtOracleInts(line)
		case strings.HasPrefix(line, "[CELT_ENC_TRIM]"):
			m := celtOracleTrimRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad CELT_ENC_TRIM line: %s", line)
			}
			out[cur].tellFracTrim, _ = strconv.Atoi(m[1])
			out[cur].spread, _ = strconv.Atoi(m[2])
			out[cur].allocTrim, _ = strconv.Atoi(m[3])
			out[cur].totalBoost, _ = strconv.Atoi(m[4])
		case strings.HasPrefix(line, "[CELT_ENC_OFFSETS]"):
			out[cur].offsets = celtOracleInts(line)
		case strings.HasPrefix(line, "[CELT_ENC_ALLOC]"):
			m := celtOracleAllocRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad CELT_ENC_ALLOC line: %s", line)
			}
			out[cur].tellFine, _ = strconv.Atoi(m[1])
			out[cur].bits, _ = strconv.Atoi(m[2])
			out[cur].antiCollapseRsv, _ = strconv.Atoi(m[3])
			out[cur].codedBands, _ = strconv.Atoi(m[4])
			out[cur].intensity, _ = strconv.Atoi(m[5])
			out[cur].dualStereo = m[6] == "1"
			out[cur].balance, _ = strconv.Atoi(m[7])
		case strings.HasPrefix(line, "[CELT_ENC_PULSES]"):
			out[cur].pulses = celtOracleInts(line)
		case strings.HasPrefix(line, "[CELT_ENC_FINE_QUANT]"):
			out[cur].fineQuant = celtOracleInts(line)
		case strings.HasPrefix(line, "[CELT_ENC_FINE_PRIORITY]"):
			out[cur].finePriority = celtOracleInts(line)
		case strings.HasPrefix(line, "[CELT_ENC_FINAL]"):
			m := celtOracleFinalRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad CELT_ENC_FINAL line: %s", line)
			}
			out[cur].tellFinal, _ = strconv.Atoi(m[1])
		case strings.HasPrefix(line, "[ENC_PACKET]"):
			fields := strings.Fields(line)[2:]
			pkt, err := hex.DecodeString(strings.Join(fields, ""))
			if err != nil {
				t.Fatalf("parse packet: %v", err)
			}
			out[cur].packet = pkt
			out[cur].havePacket = true
		}
	}
	return out
}

// float32Stats returns the maximum absolute difference, the maximum
// relative difference and the count of exactly equal values between the Go
// (float64) and libopus (float32) vectors, comparing in float32.
func float32Stats(g []float64, c []float32) (maxAbs, maxRel float64, equal, n int) {
	n = len(c)
	if len(g) < n {
		n = len(g)
	}
	for i := 0; i < n; i++ {
		gv := float64(float32(g[i]))
		cv := float64(c[i])
		if gv == cv {
			equal++
		}
		d := math.Abs(gv - cv)
		if d > maxAbs {
			maxAbs = d
		}
		if den := math.Max(math.Abs(cv), 1e-9); d/den > maxRel {
			maxRel = d / den
		}
	}
	return
}

type celtOracleCase struct {
	name       string
	frames     int
	bitrate    int
	complexity int
	vbr        bool
	channels   int
	exact      bool // gate: every packet must be byte-identical
}

func runCELTOracleCase(t *testing.T, tc celtOracleCase) {
	t.Helper()
	const rate = 48000
	ref := runCELTEncOracle(t, "ref-speech", tc.frames, tc.bitrate, tc.complexity, tc.vbr, tc.channels)
	enc, err := NewEncoder(rate, tc.channels, ApplicationRestrictedLowDelay)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(tc.bitrate); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(tc.vbr)
	if err := enc.SetComplexity(tc.complexity); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	frameSize := rate / 50
	identical := 0
	firstDiff := -1
	for f := 0; f < tc.frames; f++ {
		var pcm []float64
		if tc.channels == 2 {
			pcm = silkRefSpeechFrameStereo(rate, f*frameSize, frameSize)
		} else {
			pcm = encOracleRefSpeechFrame(rate, f*frameSize, frameSize)
		}
		pkt, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d: %v", f, err)
		}
		r := ref[f]
		if !r.havePacket || len(r.in) == 0 {
			t.Fatalf("frame %d: incomplete oracle trace", f)
		}
		same := bytes.Equal(pkt, r.packet)
		if same {
			identical++
		} else if firstDiff < 0 {
			firstDiff = f
		}
		tr := enc.celtEncoder.LastFrameTrace()
		prefix := 0
		for prefix < len(pkt) && prefix < len(r.packet) && pkt[prefix] == r.packet[prefix] {
			prefix++
		}
		if !same || testing.Verbose() {
			inAbs, _, inEq, inN := float32Stats(tr.In[0], r.in)
			fqAbs, _, fqEq, fqN := float32Stats(tr.Freq[0], r.freq)
			beAbs, _, beEq, beN := float32Stats(tr.BandE, r.bandE)
			blAbs, _, blEq, blN := float32Stats(tr.BandLogE, r.bandLogE)
			t.Logf("frame %d: identical=%v prefix=%d Go %d B / C %d B | in: eq %d/%d maxAbs %.3g | freq: eq %d/%d maxAbs %.3g | bandE: eq %d/%d maxAbs %.3g | bandLogE: eq %d/%d maxAbs %.3g",
				f, same, prefix, len(pkt), len(r.packet), inEq, inN, inAbs, fqEq, fqN, fqAbs, beEq, beN, beAbs, blEq, blN, blAbs)
			t.Logf("frame %d: decisions Go{transient %v short %d tfEst %.6g tfChan %d intra %v tellCoarse %d tfSelect %d tellTF %d} C{transient %v short %d tfEst %.6g tfChan %d pf_on %v tell %d tellCoarse+tf %d tfSelect %d}",
				f, tr.IsTransient, tr.ShortBlocks, tr.TFEstimate, tr.TFChan, tr.Intra, tr.TellCoarse, tr.TFSelect, tr.TellTF,
				r.isTransient, r.shortBlocks, r.tfEstimate, r.tfChan, r.pfOn, r.tell, r.tellCoarse, r.tfSelect)
			t.Logf("frame %d: trim Go{tellFrac %d spread %d trim %d boost %d} C{tellFrac %d spread %d trim %d boost %d} | alloc Go{bits %d rsv %d coded %d bal %d tellFine %d tellFinal %d} C{bits %d rsv %d coded %d bal %d tellFine %d tellFinal %d}",
				f, tr.TellFracTrim, tr.Spread, tr.AllocTrim, tr.TotalBoost, r.tellFracTrim, r.spread, r.allocTrim, r.totalBoost,
				tr.Bits, tr.AntiCollapseRsv, tr.CodedBands, tr.Balance, tr.TellFine, tr.TellFinal,
				r.bits, r.antiCollapseRsv, r.codedBands, r.balance, r.tellFine, r.tellFinal)
			if os.Getenv("CELT_ORACLE_DUMP") != "" {
				t.Logf("frame %d: oldBandE Go %v C %v", f, tr.OldBandE, r.oldBandE)
				t.Logf("frame %d: error Go %v C %v", f, tr.CoarseError, r.errorE)
			}
			if _, _, eq, n := float32Stats(tr.OldBandE, r.oldBandE); eq != n {
				t.Logf("frame %d: oldBandE after coarse differs (%d/%d equal): Go %v C %v", f, eq, n, tr.OldBandE, r.oldBandE)
			}
			if _, _, eq, n := float32Stats(tr.CoarseError, r.errorE); eq != n {
				t.Logf("frame %d: coarse error differs (%d/%d equal): Go %v C %v", f, eq, n, tr.CoarseError, r.errorE)
			}
			if !intsEqual(tr.Offsets, r.offsets) {
				t.Logf("frame %d: offsets Go %v C %v", f, tr.Offsets, r.offsets)
			}
			if !intsEqual(tr.TFRes, r.tfRes) {
				t.Logf("frame %d: tf_res Go %v C %v", f, tr.TFRes, r.tfRes)
			}
			if !intsEqual(tr.Pulses, r.pulses) {
				t.Logf("frame %d: pulses Go %v C %v", f, tr.Pulses, r.pulses)
			}
			if !intsEqual(tr.FineQuant, r.fineQuant) {
				t.Logf("frame %d: fine_quant Go %v C %v", f, tr.FineQuant, r.fineQuant)
			}
		}
	}
	t.Logf("%s: %d/%d packets byte-identical (first difference at frame %d)", tc.name, identical, tc.frames, firstDiff)
	if tc.exact && identical != tc.frames {
		t.Errorf("%s: %d/%d packets byte-identical, want all", tc.name, identical, tc.frames)
	}
}

func intsEqual(a, b []int) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCELTEncoderOracle compares the Go CELT-only encoder with the
// instrumented libopus encoder over a matrix of bitrate, rate mode and
// complexity. Cells marked exact are gated; the others report progress.
func TestCELTEncoderOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	var cases []celtOracleCase
	for _, complexity := range []int{0, 1, 2, 3, 4, 5, 10} {
		for _, bitrate := range []int{24000, 64000, 128000} {
			for _, vbr := range []bool{false, true} {
				mode := "cbr"
				if vbr {
					mode = "vbr"
				}
				cases = append(cases, celtOracleCase{
					name:       fmt.Sprintf("mono/%s/%dk/c%d", mode, bitrate/1000, complexity),
					frames:     20,
					bitrate:    bitrate,
					complexity: complexity,
					vbr:        vbr,
					channels:   1,
					exact:      complexity == 0 && !vbr,
				})
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runCELTOracleCase(t, tc) })
	}
}
