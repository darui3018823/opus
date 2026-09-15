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
)

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
}

var (
	encOracleFrameRe  = regexp.MustCompile(`^ENC_RESULT frame=(\d+) bytes=(\d+)`)
	encOracleInputRe  = regexp.MustCompile(`^\[ENC_INPUT\] frame_size=(\d+) total_buffer=(\d+) channels=(\d+) mode=(-?\d+) cutoff_Hz=(-?\d+) smth2=(-?\d+) hp_freq_smth1=(-?\d+)`)
	encOracleXInfoRe  = regexp.MustCompile(`speech_activity_Q8=(-?\d+) variable_HP_smth1_Q15=(-?\d+)`)
	encOracleValuesRe = regexp.MustCompile(`v\[\d+\]=(\S+)`)
)

func encOraclePath() string {
	return filepath.Join(os.TempDir(), "opusoracle", "enc_oracle.exe")
}

// runEncOracle runs the oracle on one fixture and returns the per-frame traces.
func runEncOracle(t *testing.T, rate int, fixture string, frames, bitrate int) []encOracleFrame {
	t.Helper()
	cmd := exec.Command(encOraclePath(), "--silk-enc", strconv.Itoa(rate), fixture, "-1", strconv.Itoa(frames), strconv.Itoa(bitrate))
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
	for _, rate := range []int{8000, 12000, 16000} {
		for _, fixture := range []string{"steady-voiced", "speech-like-harmonic", "onset", "unvoiced-noise"} {
			gen := gens[fixture]
			if gen == nil {
				t.Fatalf("no Go generator for fixture %s", fixture)
			}
			t.Run(fmt.Sprintf("%dk/%s", rate/1000, fixture), func(t *testing.T) {
				ref := runEncOracle(t, rate, fixture, frames, bitrate)
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
				}
				if firstPacketDiff < 0 && !diverged {
					t.Logf("all %d packets byte-identical to libopus", frames)
				}
			})
		}
	}
}
