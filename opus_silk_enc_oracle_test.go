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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/darui3018823/opus/internal/silk"
)

// encOracleStages holds the last analysis-stage dump of a frame (libopus
// runs the NSQ several times inside its gain loop; the final call wins).
type encOracleStages struct {
	haveNSQ      bool
	signalType   int
	quantOffset  int
	seed         int
	lambdaQ10    int
	ltpScaleQ14  int
	nlsfQuantQ15 []int
	predCoefQ12  [][]int // 2 rows
	ltpCoefQ14   []int
	arQ13        [][]int // nb_subfr rows
	gainsQ16     []int
	gainsIdx     []int
	pitchL       []int
	tiltQ14      []int
	harmQ14      []int
	lfQ14        []int
	pulses       []int
	// noise_shape_analysis_FLP outputs (float32)
	haveShape     bool
	shapeInputQ   float32
	shapeCodingQ  float32
	shapeSNRdBQ7  int
	shapeWarping  int
	shapePredGain float32
	shapeLTPCorr  float32
	shapeAR       [][]float32
	shapeGains    []float32
	shapeLFMA     []float32
	shapeLFAR     []float32
	shapeTilt     []float32
	shapeHarm     []float32
}

// The encoder input-pipeline oracle: scripts/oracle/build_encoder.ps1 builds
// the checked-in libopus 1.6.1 with the real opus_encode_float instrumented
// to print, per frame, the conditioned pcm_buf, the SILK x_buf, and the
// packet bytes. This test feeds the Go encoder the same fixtures with the same
// settings and compares the pipeline stage by stage. It is skipped when the
// oracle binary has not been built.

type encOracleFrame struct {
	frame        int
	totalBuffer  int
	cutoffHz     int32
	smth2        int32
	hpFreqSmth1  int32
	pcmBuf       []float32
	xBuf         []float32
	activityQ8   int
	hpSmth1Q15   int32
	packet       []byte
	haveInput    bool
	haveXBuf     bool
	havePacketOK bool
	stages       encOracleStages
}

var (
	encOracleFrameRe  = regexp.MustCompile(`^ENC_RESULT frame=(\d+) bytes=(\d+)`)
	encOracleInputRe  = regexp.MustCompile(`^\[ENC_INPUT\] frame_size=(\d+) total_buffer=(\d+) channels=(\d+) mode=(-?\d+) cutoff_Hz=(-?\d+) smth2=(-?\d+) hp_freq_smth1=(-?\d+)`)
	encOracleXInfoRe  = regexp.MustCompile(`speech_activity_Q8=(-?\d+) variable_HP_smth1_Q15=(-?\d+)`)
	encOracleValuesRe = regexp.MustCompile(`v\[[\d,]+\]=(\S+)`)
	encOracleNSQInRe  = regexp.MustCompile(`^\[SILK_ENC_NSQ_INPUT\] signalType=(\d+) quantOffset=(\d+) seed=(\d+) Lambda_Q10=(-?\d+) LTP_scale_Q14=(-?\d+)`)
	encOracleRowsRe   = regexp.MustCompile(`rows=(\d+) cols=(\d+)`)
	encOracleShapeRe  = regexp.MustCompile(`inputQuality=(\S+) codingQuality=(\S+) SNR_dB_Q7=(-?\d+) warping_Q16=(-?\d+) predGain=(\S+) LTPCorr=(\S+)`)
)

func parseFloat32Values(line string) []float32 {
	ms := encOracleValuesRe.FindAllStringSubmatch(line, -1)
	vals := make([]float32, len(ms))
	for i, m := range ms {
		f, _ := strconv.ParseFloat(m[1], 64)
		vals[i] = float32(f)
	}
	return vals
}

func parseFloat32Rows(line string) [][]float32 {
	m := encOracleRowsRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	rows, _ := strconv.Atoi(m[1])
	cols, _ := strconv.Atoi(m[2])
	flat := parseFloat32Values(line)
	out := make([][]float32, rows)
	for r := 0; r < rows; r++ {
		if (r+1)*cols <= len(flat) {
			out[r] = flat[r*cols : (r+1)*cols]
		}
	}
	return out
}

func parseIntValues(line string) []int {
	ms := encOracleValuesRe.FindAllStringSubmatch(line, -1)
	vals := make([]int, len(ms))
	for i, m := range ms {
		v, err := strconv.Atoi(m[1])
		if err != nil {
			f, _ := strconv.ParseFloat(m[1], 64)
			v = int(f)
		}
		vals[i] = v
	}
	return vals
}

func parseIntRows(line string) [][]int {
	m := encOracleRowsRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	rows, _ := strconv.Atoi(m[1])
	cols, _ := strconv.Atoi(m[2])
	flat := parseIntValues(line)
	out := make([][]int, rows)
	for r := 0; r < rows; r++ {
		if (r+1)*cols <= len(flat) {
			out[r] = flat[r*cols : (r+1)*cols]
		}
	}
	return out
}

func encOraclePath() string {
	return filepath.Join(os.TempDir(), "opusoracle", "enc_oracle.exe")
}

// runEncOracle runs the oracle on one fixture and returns the per-frame traces.
func runEncOracle(t *testing.T, rate int, fixture string, frames, bitrate int, bandwidth string) []encOracleFrame {
	t.Helper()
	cmd := exec.Command(encOraclePath(), "--silk-enc", strconv.Itoa(rate), fixture, "-1", strconv.Itoa(frames), strconv.Itoa(bitrate), bandwidth)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("enc_oracle %d %s: %v\n%s", rate, fixture, err, stderr.String())
	}
	out := make([]encOracleFrame, frames)
	cur := 0
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
		case strings.HasPrefix(line, "[ENC_INPUT]"):
			m := encOracleInputRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad ENC_INPUT line: %s", line)
			}
			f := &out[cur]
			f.frame = cur
			f.totalBuffer, _ = strconv.Atoi(m[2])
			c, _ := strconv.Atoi(m[5])
			f.cutoffHz = int32(c)
			s2, _ := strconv.Atoi(m[6])
			f.smth2 = int32(s2)
			h1, _ := strconv.Atoi(m[7])
			f.hpFreqSmth1 = int32(h1)
			f.haveInput = true
		case strings.HasPrefix(line, "[ENC_PCM_BUF]"):
			out[cur].pcmBuf = parseFloats(line)
		case strings.HasPrefix(line, "[SILK_ENC_XFRAME_INFO]"):
			m := encOracleXInfoRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad XFRAME_INFO line: %s", line)
			}
			out[cur].activityQ8, _ = strconv.Atoi(m[1])
			s1, _ := strconv.Atoi(m[2])
			out[cur].hpSmth1Q15 = int32(s1)
		case strings.HasPrefix(line, "[SILK_ENC_XBUF]"):
			out[cur].xBuf = parseFloats(line)
			out[cur].haveXBuf = true
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_INPUT]"):
			m := encOracleNSQInRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad NSQ_INPUT line: %s", line)
			}
			st := &out[cur].stages
			st.haveNSQ = true
			st.signalType, _ = strconv.Atoi(m[1])
			st.quantOffset, _ = strconv.Atoi(m[2])
			st.seed, _ = strconv.Atoi(m[3])
			st.lambdaQ10, _ = strconv.Atoi(m[4])
			st.ltpScaleQ14, _ = strconv.Atoi(m[5])
		case strings.HasPrefix(line, "[SILK_ENC_NOISE_SHAPE]"):
			m := encOracleShapeRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad NOISE_SHAPE line: %s", line)
			}
			st := &out[cur].stages
			st.haveShape = true
			f, _ := strconv.ParseFloat(m[1], 64)
			st.shapeInputQ = float32(f)
			f, _ = strconv.ParseFloat(m[2], 64)
			st.shapeCodingQ = float32(f)
			st.shapeSNRdBQ7, _ = strconv.Atoi(m[3])
			st.shapeWarping, _ = strconv.Atoi(m[4])
			f, _ = strconv.ParseFloat(m[5], 64)
			st.shapePredGain = float32(f)
			f, _ = strconv.ParseFloat(m[6], 64)
			st.shapeLTPCorr = float32(f)
		case strings.HasPrefix(line, "[SILK_ENC_SHAPE_AR_FLP]"):
			out[cur].stages.shapeAR = parseFloat32Rows(line)
		case strings.HasPrefix(line, "[SILK_ENC_SHAPE_GAINS_PRE_FLP]"):
			out[cur].stages.shapeGains = parseFloat32Values(line)
		case strings.HasPrefix(line, "[SILK_ENC_SHAPE_LF_MA_FLP]"):
			out[cur].stages.shapeLFMA = parseFloat32Values(line)
		case strings.HasPrefix(line, "[SILK_ENC_SHAPE_LF_AR_FLP]"):
			out[cur].stages.shapeLFAR = parseFloat32Values(line)
		case strings.HasPrefix(line, "[SILK_ENC_SHAPE_TILT_FLP]"):
			out[cur].stages.shapeTilt = parseFloat32Values(line)
		case strings.HasPrefix(line, "[SILK_ENC_SHAPE_HARM_FLP]"):
			out[cur].stages.shapeHarm = parseFloat32Values(line)
		case strings.HasPrefix(line, "[SILK_ENC_NLSF_QUANT_Q15]"):
			out[cur].stages.nlsfQuantQ15 = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_PREDCOEF_Q12]"):
			out[cur].stages.predCoefQ12 = parseIntRows(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_LTPCOEF_Q14]"):
			out[cur].stages.ltpCoefQ14 = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_AR_Q13]"):
			out[cur].stages.arQ13 = parseIntRows(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_GAINS_Q16]"):
			out[cur].stages.gainsQ16 = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_GAINS_IDX]"):
			out[cur].stages.gainsIdx = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_PITCHL]"):
			out[cur].stages.pitchL = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_TILT_Q14]"):
			out[cur].stages.tiltQ14 = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_HARM_Q14]"):
			out[cur].stages.harmQ14 = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_LF_Q14]"):
			out[cur].stages.lfQ14 = parseIntValues(line)
		case strings.HasPrefix(line, "[SILK_ENC_NSQ_PULSES]"):
			out[cur].stages.pulses = parseIntValues(line)
		case strings.HasPrefix(line, "[ENC_PACKET]"):
			fields := strings.Fields(line)[2:]
			pkt, err := hex.DecodeString(strings.Join(fields, ""))
			if err != nil {
				t.Fatalf("parse packet: %v", err)
			}
			// Printed after ENC_RESULT, so it belongs to the frame just closed.
			out[cur-1].packet = pkt
			out[cur-1].havePacketOK = true
		case strings.HasPrefix(line, "ENC_RESULT"):
			m := encOracleFrameRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad ENC_RESULT line: %s", line)
			}
			cur, _ = strconv.Atoi(m[1])
			cur++
			if cur > frames {
				cur = frames
			}
		}
	}
	return out
}

// encOracleNoiseFrame replicates enc_oracle.c's "unvoiced-noise" fixture
// (an LCG, unlike the math/rand generator of the AB test's signal of the same
// name), so the two encoders see identical samples.
func encOracleNoiseFrame(rate, start, n int) []float64 {
	out := make([]float64, n)
	state := uint32(0x61515) + uint32(start/rate)
	prev := 0.0
	for i := range out {
		state = state*1664525 + 1013904223
		white := (float64(state>>8)/16777215.0)*2.0 - 1.0
		y := 0.28*white - 0.18*prev
		prev = white
		out[i] = float64(float32(y))
	}
	return out
}

// stageDiffs lists the analysis stages (in libopus order) whose values differ
// between the Go frame trace and the oracle dump; empty when all match.
// libopus stores delta-coded gain indices for subframes > 0, so the indices
// are compared through the dequantised Gains_Q16.
func stageDiffs(g silk.FrameTrace, c encOracleStages) []string {
	if !c.haveNSQ {
		return []string{"no NSQ trace"}
	}
	eqI16 := func(a []int16, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if int(a[i]) != b[i] {
				return false
			}
		}
		return true
	}
	eqI32 := func(a []int32, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if int(a[i]) != b[i] {
				return false
			}
		}
		return true
	}
	eqInt := func(a []int, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	var diffs []string
	if g.SignalType != c.signalType || g.QuantOffset != c.quantOffset {
		diffs = append(diffs, fmt.Sprintf("signalType/quantOffset (Go %d/%d, C %d/%d)", g.SignalType, g.QuantOffset, c.signalType, c.quantOffset))
	}
	if !eqI16(g.NLSFQ15, c.nlsfQuantQ15) {
		diffs = append(diffs, "NLSF_Q15")
	}
	if len(c.predCoefQ12) == 2 && (!eqI16(g.PredCoefQ12[0], c.predCoefQ12[0]) || !eqI16(g.PredCoefQ12[1], c.predCoefQ12[1])) {
		diffs = append(diffs, "PredCoef_Q12")
	}
	if !eqInt(g.PitchL, c.pitchL) {
		diffs = append(diffs, fmt.Sprintf("pitchL (Go %v, C %v)", g.PitchL, c.pitchL))
	}
	if !eqI16(g.LTPCoefQ14, c.ltpCoefQ14) {
		diffs = append(diffs, "LTPCoef_Q14")
	}
	if !eqI32(g.GainsQ16, c.gainsQ16) {
		diffs = append(diffs, fmt.Sprintf("Gains_Q16 (Go %v, C %v)", g.GainsQ16, c.gainsQ16))
	}
	for sf := range c.arQ13 {
		if sf >= len(g.ARQ13) || !eqI16(g.ARQ13[sf], c.arQ13[sf]) {
			diffs = append(diffs, fmt.Sprintf("AR_Q13[%d]", sf))
		}
	}
	if !eqI32(g.TiltQ14, c.tiltQ14) {
		diffs = append(diffs, fmt.Sprintf("Tilt_Q14 (Go %v, C %v)", g.TiltQ14, c.tiltQ14))
	}
	if !eqI32(g.HarmShapeGainQ14, c.harmQ14) {
		diffs = append(diffs, fmt.Sprintf("HarmShapeGain_Q14 (Go %v, C %v)", g.HarmShapeGainQ14, c.harmQ14))
	}
	if !eqI32(g.LFShpQ14, c.lfQ14) {
		diffs = append(diffs, fmt.Sprintf("LF_shp_Q14 (Go %v, C %v)", g.LFShpQ14, c.lfQ14))
	}
	if int(g.LambdaQ10) != c.lambdaQ10 || int(g.LTPScaleQ14) != c.ltpScaleQ14 {
		diffs = append(diffs, fmt.Sprintf("Lambda_Q10/LTP_scale (Go %d/%d, C %d/%d)", g.LambdaQ10, g.LTPScaleQ14, c.lambdaQ10, c.ltpScaleQ14))
	}
	if int(g.Seed) != c.seed {
		diffs = append(diffs, fmt.Sprintf("seed (Go %d, C %d)", g.Seed, c.seed))
	}
	if !eqI16(g.Pulses, c.pulses) {
		diffs = append(diffs, "pulses")
	}
	return diffs
}

// shapeDiffs compares the Go float32 noise-shape port with the oracle's
// noise_shape_analysis_FLP outputs (float32 bits).
func shapeDiffs(g silk.FrameTrace, c encOracleStages, nbSubfr, order int) []string {
	if !c.haveShape {
		return []string{"no noise-shape trace"}
	}
	ar, gains, lfMA, lfAR, tilt, harm, iq, cq := g.Shape32Values(nbSubfr, order)
	var diffs []string
	eq := func(a, b []float32) (int, bool) { return firstFloat32Mismatch(a, b) }
	if math.Float32bits(iq) != math.Float32bits(c.shapeInputQ) {
		diffs = append(diffs, fmt.Sprintf("input_quality (Go %.9g, C %.9g)", iq, c.shapeInputQ))
	}
	if math.Float32bits(cq) != math.Float32bits(c.shapeCodingQ) {
		diffs = append(diffs, fmt.Sprintf("coding_quality (Go %.9g, C %.9g)", cq, c.shapeCodingQ))
	}
	for k := 0; k < nbSubfr && k < len(c.shapeAR); k++ {
		if i, ok := eq(ar[k], c.shapeAR[k]); !ok {
			diffs = append(diffs, fmt.Sprintf("AR[%d][%d] (Go %.9g, C %.9g)", k, i, ar[k][i], c.shapeAR[k][i]))
			break
		}
	}
	if i, ok := eq(gains, c.shapeGains); !ok {
		diffs = append(diffs, fmt.Sprintf("Gains[%d] (Go %v, C %v)", i, gains, c.shapeGains))
	}
	if i, ok := eq(lfMA, c.shapeLFMA); !ok {
		diffs = append(diffs, fmt.Sprintf("LF_MA[%d] (Go %v, C %v)", i, lfMA, c.shapeLFMA))
	}
	if i, ok := eq(lfAR, c.shapeLFAR); !ok {
		diffs = append(diffs, fmt.Sprintf("LF_AR[%d] (Go %v, C %v)", i, lfAR, c.shapeLFAR))
	}
	if i, ok := eq(tilt, c.shapeTilt); !ok {
		diffs = append(diffs, fmt.Sprintf("Tilt[%d] (Go %v, C %v)", i, tilt, c.shapeTilt))
	}
	if i, ok := eq(harm, c.shapeHarm); !ok {
		diffs = append(diffs, fmt.Sprintf("HarmShapeGain[%d] (Go %v, C %v)", i, harm, c.shapeHarm))
	}
	return diffs
}

func firstFloat32Mismatch(got, want []float32) (int, bool) {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			return i, false
		}
	}
	if len(got) != len(want) {
		return n, false
	}
	return -1, true
}

// TestSILKEncoderInputPipelineOracle compares the Go encoder's conditioned
// input, SILK x_buf, VAD activity, high-pass cutoff state, and packet bytes
// with the instrumented libopus encoder on the mono SILK AB fixtures.
//
// Gates: the conditioned input, the SILK x_buf, and the cutoff state must be
// bit-exact for every frame up to (and including) the first frame whose
// packet bytes differ — until then the two encoders share all state. Later
// frames are reported for diagnosis only.
func TestSILKEncoderInputPipelineOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	const (
		frames  = 12
		bitrate = 24000
	)
	gens := map[string]func(rate, start, n int) []float64{}
	for _, sig := range opusSILKQualitySignals() {
		gens[sig.name] = sig.gen
	}
	gens["unvoiced-noise"] = encOracleNoiseFrame
	// 24/48 kHz input exercises the libopus encoder-direction resampler in
	// front of the 16 kHz SILK encoder (wideband forced on both sides).
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, fixture := range []string{"steady-voiced", "speech-like-harmonic", "onset", "unvoiced-noise"} {
			gen := gens[fixture]
			if gen == nil {
				t.Fatalf("no Go generator for fixture %s", fixture)
			}
			t.Run(fmt.Sprintf("%dk/%s", rate/1000, fixture), func(t *testing.T) {
				bandwidth := "auto"
				if rate > 16000 {
					bandwidth = "wb"
				}
				ref := runEncOracle(t, rate, fixture, frames, bitrate, bandwidth)
				frameSize := rate / 50
				enc, err := NewEncoder(rate, 1, ApplicationVOIP)
				if err != nil {
					t.Fatal(err)
				}
				if err := enc.SetBitrate(bitrate); err != nil {
					t.Fatal(err)
				}
				if err := enc.SetComplexity(5); err != nil {
					t.Fatal(err)
				}
				enc.SetVBR(true)
				enc.SetVBRConstraint(true)
				enc.SetSignalType(SignalVoice)
				if rate > 16000 {
					if err := enc.SetBandwidth(BandwidthWideband); err != nil {
						t.Fatal(err)
					}
				}

				var prevCond []float64
				firstPacketDiff := -1
				diverged := false // encoder state no longer shared (silence shortcut)
				for f := 0; f < frames; f++ {
					pcm := gen(rate, f*frameSize, frameSize)
					pkt, err := enc.EncodeFloat(pcm, frameSize)
					if err != nil {
						t.Fatalf("frame %d: EncodeFloat: %v", f, err)
					}
					r := ref[f]
					if !r.haveInput || !r.haveXBuf || !r.havePacketOK {
						t.Fatalf("frame %d: incomplete oracle trace", f)
					}
					packetsEqual := bytes.Equal(pkt, r.packet)
					if !packetsEqual && firstPacketDiff < 0 {
						firstPacketDiff = f
					}
					gate := !diverged && (firstPacketDiff < 0 || f == firstPacketDiff)

					// (a) conditioned input: pcm_buf[total_buffer:] is this frame,
					// pcm_buf[:total_buffer] the delayed tail of the previous one.
					cond := enc.lastConditionedInput
					goPCMBuf := make([]float32, 0, len(r.pcmBuf))
					if r.totalBuffer > 0 {
						tail := make([]float32, r.totalBuffer)
						if prevCond != nil {
							for i := range tail {
								tail[i] = float32(prevCond[len(prevCond)-r.totalBuffer+i])
							}
						}
						goPCMBuf = append(goPCMBuf, tail...)
					}
					for _, v := range cond {
						goPCMBuf = append(goPCMBuf, float32(v))
					}
					prevCond = cond
					idx, ok := firstFloat32Mismatch(goPCMBuf, r.pcmBuf)
					cutoffOK := enc.variableHPSmth2Q15 == r.smth2
					if !ok || !cutoffOK {
						msg := fmt.Sprintf("frame %d: conditioned input mismatch at %d (Go smth2=%d C smth2=%d cutoff=%d)", f, idx, enc.variableHPSmth2Q15, r.smth2, r.cutoffHz)
						if ok {
							msg = fmt.Sprintf("frame %d: cutoff state Go smth2=%d C smth2=%d (cutoff %d Hz)", f, enc.variableHPSmth2Q15, r.smth2, r.cutoffHz)
						} else if idx >= 0 && idx < len(goPCMBuf) && idx < len(r.pcmBuf) {
							msg += fmt.Sprintf(": Go=%.9g (%08x) C=%.9g (%08x)", goPCMBuf[idx], math.Float32bits(goPCMBuf[idx]), r.pcmBuf[idx], math.Float32bits(r.pcmBuf[idx]))
						}
						if gate {
							t.Fatal(msg)
						}
						t.Log(msg)
					}

					// (b) SILK x_buf (int16-scale float32). A one-byte Go packet
					// (TOC + one-byte frame) is the Go digital-silence shortcut (libopus codes silent
					// frames normally): a known policy divergence, reported but
					// not gated, and the SILK-side checks do not apply to it.
					goX := enc.silkEncoder.InputBufferFLP()
					if len(pkt) <= 2 {
						t.Logf("frame %d: Go silence shortcut (1-byte frame); libopus coded %d bytes — encoder state diverges from here (policy)", f, len(r.packet))
						diverged = true
						continue
					}
					idx, ok = firstFloat32Mismatch(goX, r.xBuf)
					if !ok {
						msg := fmt.Sprintf("frame %d: x_buf mismatch at %d (len Go=%d C=%d)", f, idx, len(goX), len(r.xBuf))
						if idx >= 0 && idx < len(goX) && idx < len(r.xBuf) {
							msg += fmt.Sprintf(": Go=%.9g C=%.9g", goX[idx], r.xBuf[idx])
						}
						if gate {
							t.Fatal(msg)
						}
						t.Log(msg)
					}

					// (c) VAD activity and the SILK-side cutoff smoother.
					if a := enc.silkEncoder.LastSpeechActivityQ8(); a != r.activityQ8 || enc.silkEncoder.VariableHPSmth1Q15() != r.hpSmth1Q15 {
						msg := fmt.Sprintf("frame %d: speech_activity_Q8 Go=%d C=%d, variable_HP_smth1_Q15 Go=%d C=%d", f, a, r.activityQ8, enc.silkEncoder.VariableHPSmth1Q15(), r.hpSmth1Q15)
						if gate {
							t.Fatal(msg)
						}
						t.Log(msg)
					}
					if !packetsEqual && f == firstPacketDiff {
						t.Logf("frame %d: first packet difference (Go %d bytes, libopus %d bytes); pipeline exact through this frame", f, len(pkt), len(r.packet))
					}
					if !packetsEqual {
						tr := enc.silkEncoder.LastFrameTrace()
						if len(r.stages.shapeAR) > 0 {
							if d := shapeDiffs(tr, r.stages, len(r.stages.shapeAR), len(r.stages.shapeAR[0])); len(d) > 0 {
								t.Logf("frame %d: noise-shape port differs: %s (C SNR_dB_Q7=%d warping=%d predGain=%.9g LTPCorr=%.9g)", f, strings.Join(d, "; "), r.stages.shapeSNRdBQ7, r.stages.shapeWarping, r.stages.shapePredGain, r.stages.shapeLTPCorr)
							} else {
								t.Logf("frame %d: noise-shape port exact", f)
							}
						}
						if d := stageDiffs(enc.silkEncoder.LastFrameTrace(), r.stages); len(d) > 0 {
							t.Logf("frame %d: differing analysis stages: %s", f, strings.Join(d, "; "))
						} else {
							t.Logf("frame %d: all traced analysis stages match; difference is in the entropy coding / rate control", f)
						}
					}
				}
				if firstPacketDiff < 0 && !diverged {
					t.Logf("all %d packets byte-identical to libopus", frames)
				}
			})
		}
	}
}
