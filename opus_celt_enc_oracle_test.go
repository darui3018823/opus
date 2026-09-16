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
	in          []float32
	freq        []float32
	bandE       []float32
	bandLogE    []float32
	packet      []byte
	havePacket  bool
}

var (
	celtOracleFrameRe  = regexp.MustCompile(`^\[CELT_ENC_FRAME\] N=(\d+) LM=(\d+) C=(\d+) CC=(\d+) overlap=(\d+) silence=(\d) isTransient=(\d) shortBlocks=(\d+) tf_estimate=(\S+) tf_chan=(\d+) pf_on=(\d) pitch_index=(-?\d+) gain1=(\S+) tapset=(\d+) tell=(\d+)`)
	celtOracleCoarseRe = regexp.MustCompile(`^\[CELT_ENC_COARSE\] tell=(\d+) intra=(\d) tf_select=(\d)`)
	celtOracleIntRe    = regexp.MustCompile(`v\[\d+\]=(-?\d+)`)
)

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
		case strings.HasPrefix(line, "[CELT_ENC_TF_RES]"):
			for _, m := range celtOracleIntRe.FindAllStringSubmatch(line, -1) {
				v, _ := strconv.Atoi(m[1])
				out[cur].tfRes = append(out[cur].tfRes, v)
			}
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

func TestCELTEncoderOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	const (
		frames  = 6
		rate    = 48000
		bitrate = 64000
	)
	for _, complexity := range []int{0, 5} {
		t.Run(fmt.Sprintf("complexity%d", complexity), func(t *testing.T) {
			ref := runCELTEncOracle(t, "ref-speech", frames, bitrate, complexity, false, 1)
			enc, err := NewEncoder(rate, 1, ApplicationRestrictedLowDelay)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(false)
			if err := enc.SetComplexity(complexity); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBandwidth(BandwidthFullband); err != nil {
				t.Fatal(err)
			}
			frameSize := rate / 50
			identical := 0
			for f := 0; f < frames; f++ {
				pcm := encOracleRefSpeechFrame(rate, f*frameSize, frameSize)
				pkt, err := enc.EncodeFloat(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", f, err)
				}
				r := ref[f]
				if !r.havePacket || len(r.in) == 0 {
					t.Fatalf("frame %d: incomplete oracle trace", f)
				}
				if len(pkt) != len(r.packet) || pkt[0] != r.packet[0] {
					t.Fatalf("frame %d: framing differs: Go %d bytes TOC %#x, libopus %d bytes TOC %#x", f, len(pkt), pkt[0], len(r.packet), r.packet[0])
				}
				if bytes.Equal(pkt, r.packet) {
					identical++
				}
				tr := enc.celtEncoder.LastFrameTrace()
				prefix := 0
				for prefix < len(pkt) && pkt[prefix] == r.packet[prefix] {
					prefix++
				}
				inAbs, inRel, inEq, inN := float32Stats(tr.In[0], r.in)
				fqAbs, fqRel, fqEq, fqN := float32Stats(tr.Freq[0], r.freq)
				beAbs, beRel, beEq, beN := float32Stats(tr.BandE, r.bandE)
				blAbs, blRel, blEq, blN := float32Stats(tr.BandLogE, r.bandLogE)
				t.Logf("frame %d: identical=%v prefix=%d/%d | in: eq %d/%d maxAbs %.3g maxRel %.3g | freq: eq %d/%d maxAbs %.3g maxRel %.3g | bandE: eq %d/%d maxAbs %.3g maxRel %.3g | bandLogE: eq %d/%d maxAbs %.3g maxRel %.3g",
					f, bytes.Equal(pkt, r.packet), prefix, len(pkt), inEq, inN, inAbs, inRel, fqEq, fqN, fqAbs, fqRel, beEq, beN, beAbs, beRel, blEq, blN, blAbs, blRel)
				t.Logf("frame %d: decisions Go{transient %v short %d tfEst %.6g tfChan %d intra %v tellCoarse %d tfSelect %d tellTF %d} C{transient %v short %d tfEst %.6g tfChan %d pf_on %v tell %d tellCoarse %d tfSelect %d}",
					f, tr.IsTransient, tr.ShortBlocks, tr.TFEstimate, tr.TFChan, tr.Intra, tr.TellCoarse, tr.TFSelect, tr.TellTF,
					r.isTransient, r.shortBlocks, r.tfEstimate, r.tfChan, r.pfOn, r.tell, r.tellCoarse, r.tfSelect)
			}
			t.Logf("%d/%d packets byte-identical", identical, frames)
		})
	}
}
