//go:build opusref

package opus

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/silk"
)

// TestRealAudioCELTParity encodes an external 48 kHz stereo s16le file with
// the default (libopus-policy) encoder and with the plain-C libopus oracle
// through --script-enc, then reports the first packet that differs and the
// CELT decisions of that frame on both sides. It needs OPUS_PARITY_PCM;
// OPUS_PARITY_FRAMES (default 20) and OPUS_PARITY_KBPS (default 64) are
// optional. The audio is not in the repository.
func TestRealAudioCELTParity(t *testing.T) {
	path := os.Getenv("OPUS_PARITY_PCM")
	if path == "" {
		t.Skip("OPUS_PARITY_PCM not set")
	}
	t.Cleanup(silk.EnableFrameTraces())
	frames := parityEnvInt("OPUS_PARITY_FRAMES", 20)
	kbps := parityEnvInt("OPUS_PARITY_KBPS", 64)
	const rate, channels, n = 48000, 2, 960
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if have := len(raw) / (2 * channels * n); have < frames {
		frames = have
	}
	pcm16 := make([]int16, frames*n*channels)
	for i := range pcm16 {
		pcm16[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}

	// The oracle reads float32 and snaps it back to int16 ("i" frames), so
	// both encoders see the same int16 input.
	dir := t.TempDir()
	pf := filepath.Join(dir, "in.f32")
	var fb bytes.Buffer
	for _, v := range pcm16 {
		binary.Write(&fb, binary.LittleEndian, float32(v)/32768)
	}
	if err := os.WriteFile(pf, fb.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	// Only frames from OPUS_PARITY_FROM on are traced; a full trace of a
	// long track is too large to parse.
	from := parityEnvInt("OPUS_PARITY_FROM", 0)
	fmt.Fprintf(&script, "c bitrate %d\nc complexity 10\nc vbr 1\nc vbrc 0\nt 0\n", kbps*1000)
	for f := 0; f < frames; f++ {
		if f == from {
			script.WriteString("t 1\n")
		}
		fmt.Fprintf(&script, "i %d\n", n)
	}
	sf := filepath.Join(dir, "script.txt")
	if err := os.WriteFile(sf, []byte(script.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(encOraclePath(), "--script-enc", "48000", "2", "audio", pf, sf)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle: %v\n%s", err, stderr.String())
	}
	var refPkts [][]byte
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 3 && f[0] == "P" {
			b, err := hex.DecodeString(f[2])
			if err != nil {
				t.Fatal(err)
			}
			refPkts = append(refPkts, b)
		}
	}
	// Script mode prints no per-frame [CELT_ENC_INPUT_FRAME] marker; each
	// opus_encode call starts with [ENC_INPUT], so mark frames there.
	var marked strings.Builder
	for k := 0; k < from; k++ {
		fmt.Fprintf(&marked, "[CELT_ENC_INPUT_FRAME] frame=%d\n", k)
	}
	k := from
	frameText := map[int]string{}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "[ENC_INPUT]") {
			fmt.Fprintf(&marked, "[CELT_ENC_INPUT_FRAME] frame=%d\n", k)
			k++
		}
		marked.WriteString(line)
		marked.WriteByte('\n')
		if k > from {
			frameText[k-1] += line + "\n"
		}
	}
	ref := parseCELTOracleTrace(t, marked.String(), frames)

	enc, err := NewEncoder(rate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(kbps * 1000); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(false)
	shown := 0
	var goPkts [][]byte
	defer func() {
		// OPUS_DBG_HYBRID: decode both streams and let the decoder print the
		// hybrid layout (SILK tell, redundancy) of the frames from `from` on.
		if os.Getenv("OPUS_PARITY_DECODE") == "" {
			return
		}
		for _, side := range []struct {
			name string
			pkts [][]byte
		}{{"C", refPkts}, {"Go", goPkts}} {
			dec, err := NewDecoder(rate, channels)
			if err != nil {
				t.Fatal(err)
			}
			out := make([]int16, 5760*channels)
			for f, p := range side.pkts {
				if f >= from {
					os.Setenv("OPUS_DBG_HYBRID", "1")
					fmt.Fprintf(os.Stderr, "DBG %s frame %d len %d toc %02x\n", side.name, f, len(p), p[0])
				}
				if _, err := dec.Decode(p, out); err != nil {
					t.Fatalf("%s frame %d: %v", side.name, f, err)
				}
				os.Unsetenv("OPUS_DBG_HYBRID")
			}
		}
	}()
	for f := 0; f < frames && f < len(refPkts); f++ {
		if f >= from && os.Getenv("OPUS_PARITY_ACT") != "" {
			os.Setenv("OPUS_DBG_ACT", "1")
		}
		pkt, err := enc.Encode(pcm16[f*n*channels:(f+1)*n*channels], n)
		os.Unsetenv("OPUS_DBG_ACT")
		if err != nil {
			t.Fatal(err)
		}
		goPkts = append(goPkts, append([]byte(nil), pkt...))
		tr := enc.celtEncoder.LastFrameTrace()
		r := ref[f]
		same := bytes.Equal(pkt, refPkts[f])
		if same && shown == 0 && os.Getenv("OPUS_PARITY_ALL") == "" {
			continue
		}
		t.Logf("frame %d: identical=%v Go %d B (toc %02x) / C %d B (toc %02x)", f, same, len(pkt), pkt[0], len(refPkts[f]), refPkts[f][0])
		logParityDiff(t, f, tr, r)
		if !same && enc.silkEncoder != nil && os.Getenv("OPUS_PARITY_RAW") != "" {
			st := enc.silkEncoder.LastFrameTrace()
			t.Logf("frame %d: Go SILK{signalType %d quantOffset %d gains %v nBits %d targetRate %d tell %d speechActQ8 %d loop %+v}",
				f, st.SignalType, st.QuantOffset, st.GainsIndices, st.NBits, st.TargetRateBps, st.Tell, enc.silkEncoder.LastSpeechActivityQ8(), st.Loop)
		}
		if !same && os.Getenv("OPUS_PARITY_PREFIXES") != "" {
			counts := map[string]int{}
			for _, line := range strings.Split(frameText[f], "\n") {
				if i := strings.IndexByte(line, ']'); strings.HasPrefix(line, "[") && i > 0 {
					counts[line[:i+1]]++
				}
			}
			t.Logf("frame %d: C trace prefixes %v", f, counts)
		}
		if !same && os.Getenv("OPUS_PARITY_RAW") != "" {
			t.Logf("frame %d: Go VBR{target %d base %d reservoir %d drift %d offset %d temporal %.9g equivRate %d maxDepth %.9g totBoost %d stereoSaving %.9g lastCoded %d}",
				f, tr.VBRTarget, tr.VBRBaseTarget, tr.VBRReservoir, tr.VBRDrift, tr.VBROffset, tr.TemporalVBR, tr.EquivRate, tr.MaxDepth, tr.TotBoost, tr.StereoSaving, tr.LastCodedBandsIn)
			for _, line := range strings.Split(frameText[f], "\n") {
				if strings.Contains(line, os.Getenv("OPUS_PARITY_RAW")) {
					if len(line) > 400 {
						line = line[:400]
					}
					t.Logf("frame %d: C %s", f, line)
				}
			}
		}
		if !same {
			shown++
		}
		if shown >= 2 {
			break
		}
	}
	if shown == 0 {
		t.Logf("all %d packets identical", frames)
	}
}

func parityEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// logParityDiff prints the CELT decisions that differ between the Go trace
// and the oracle trace of one frame.
func logParityDiff(t *testing.T, f int, tr celt.FrameTrace, r celtOracleFrame) {
	t.Helper()
	diff := func(name string, g, c any) {
		if fmt.Sprint(g) != fmt.Sprint(c) {
			t.Logf("frame %d: %-12s Go %v | C %v", f, name, g, c)
		}
	}
	diff("transient", tr.IsTransient, r.isTransient)
	diff("shortBlocks", tr.ShortBlocks, r.shortBlocks)
	diff("tfEstimate", float32(tr.TFEstimate), r.tfEstimate)
	diff("tfChan", tr.TFChan, r.tfChan)
	diff("pf", fmt.Sprint(tr.PFOn, tr.PitchIndex, float32(tr.PFGain)), fmt.Sprint(r.pfOn, r.pitchIndex, r.gain1))
	diff("tfSelect", tr.TFSelect, r.tfSelect)
	diff("tfRes", tr.TFRes, r.tfRes)
	diff("spread", tr.Spread, r.spread)
	diff("allocTrim", tr.AllocTrim, r.allocTrim)
	diff("totalBoost", tr.TotalBoost, r.totalBoost)
	diff("offsets", tr.Offsets, r.offsets)
	diff("bits", tr.Bits, r.bits)
	diff("codedBands", tr.CodedBands, r.codedBands)
	diff("intensity", tr.Intensity, r.intensity)
	diff("dualStereo", tr.DualStereo, r.dualStereo)
	diff("balance", tr.Balance, r.balance)
	diff("pulses", tr.Pulses, r.pulses)
	diff("fineQuant", tr.FineQuant, r.fineQuant)
	diff("analysis", fmt.Sprint(tr.Analysis.Valid, float32(tr.Analysis.Tonality), float32(tr.Analysis.MusicProb), tr.Analysis.Bandwidth),
		fmt.Sprint(r.anValid, r.anTonality, r.anMusicProb, r.anBandwidth))
	count := func(name string, g []float64, c []float32) {
		neq, first := 0, -1
		for i := 0; i < len(g) && i < len(c); i++ {
			if float32(g[i]) != c[i] {
				if first < 0 {
					first = i
				}
				neq++
			}
		}
		if neq > 0 || len(g) != len(c) {
			t.Logf("frame %d: %-12s %d/%d differ (lens %d/%d), first at %d", f, name, neq, min(len(g), len(c)), len(g), len(c), first)
		}
	}
	var in []float64
	for c := range tr.In {
		in = append(in, tr.In[c]...)
	}
	count("in", in, r.in)
	count("bandE", tr.BandE, r.bandE)
	count("bandLogE", tr.BandLogE, r.bandLogE)
	count("oldBandE", tr.OldBandE, r.oldBandE)
}

// runScriptOraclePackets runs the --script-enc oracle (48 kHz stereo, audio
// application) on int16 PCM with the given script and returns the packets.
func runScriptOraclePackets(t *testing.T, pcm16 []int16, script string) [][]byte {
	t.Helper()
	dir := t.TempDir()
	pf := filepath.Join(dir, "in.f32")
	var fb bytes.Buffer
	for _, v := range pcm16 {
		binary.Write(&fb, binary.LittleEndian, float32(v)/32768)
	}
	if err := os.WriteFile(pf, fb.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sf := filepath.Join(dir, "script.txt")
	if err := os.WriteFile(sf, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(encOraclePath(), "--script-enc", "48000", "2", "audio", pf, sf)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle: %v\n%s", err, stderr.String())
	}
	var pkts [][]byte
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 3 && f[0] == "P" {
			b, err := hex.DecodeString(f[2])
			if err != nil {
				t.Fatal(err)
			}
			pkts = append(pkts, b)
		}
	}
	return pkts
}

// TestCELTBandwidthSwitchVBROracle switches the maximum bandwidth of a
// stereo unconstrained-VBR stream mid-way and compares every packet with
// plain-C libopus. It is a general regression test for bandwidth switches;
// the intensity carry fixed alongside it needs a frame whose coded band
// count falls below the hysteresis intensity, which this input does not
// reach (the real-audio probe above does).
func TestCELTBandwidthSwitchVBROracle(t *testing.T) {
	const n, frames, switchAt = 960, 40, 12
	for _, tc := range []struct {
		kbps     int
		from, to int
	}{
		{48, BandwidthFullband, BandwidthSuperWideband},
		{64, BandwidthFullband, BandwidthSuperWideband},
		{96, BandwidthFullband, BandwidthSuperWideband},
		{64, BandwidthFullband, BandwidthWideband},
		{64, BandwidthSuperWideband, BandwidthFullband},
	} {
		t.Run(fmt.Sprintf("%dk/%d-%d", tc.kbps, tc.from, tc.to), func(t *testing.T) {
			// Uncorrelated stereo noise with a slow level change and a tone:
			// every band carries energy, so the allocation runs short of the
			// bands the intensity hysteresis asks for.
			pcm16 := make([]int16, frames*n*2)
			seed := uint32(12345)
			for i := 0; i < frames*n; i++ {
				lvl := 0.15 + 0.1*math.Sin(2*math.Pi*float64(i)/48000)
				tone := 0.2 * math.Sin(2*math.Pi*330*float64(i)/48000)
				for c := 0; c < 2; c++ {
					seed = seed*1664525 + 1013904223
					v := lvl*float64(int32(seed))/float64(1<<31) + tone
					pcm16[2*i+c] = int16(math.Max(-32768, math.Min(32767, math.Floor(v*32768+0.5))))
				}
			}
			var script strings.Builder
			fmt.Fprintf(&script, "c bitrate %d\nc complexity 10\nc vbr 1\nc vbrc 0\nc maxbw %d\n", tc.kbps*1000, tc.from)
			for f := 0; f < frames; f++ {
				if f == switchAt {
					fmt.Fprintf(&script, "c maxbw %d\n", tc.to)
				}
				fmt.Fprintf(&script, "i %d\n", n)
			}
			ref := runScriptOraclePackets(t, pcm16, script.String())
			enc, err := NewEncoder(48000, 2, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.kbps * 1000); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)
			if err := enc.SetMaxBandwidth(tc.from); err != nil {
				t.Fatal(err)
			}
			for f := 0; f < frames && f < len(ref); f++ {
				if f == switchAt {
					if err := enc.SetMaxBandwidth(tc.to); err != nil {
						t.Fatal(err)
					}
				}
				pkt, err := enc.Encode(pcm16[f*n*2:(f+1)*n*2], n)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(pkt, ref[f]) {
					t.Fatalf("frame %d: Go %d B (toc %02x) != C %d B (toc %02x)", f, len(pkt), pkt[0], len(ref[f]), ref[f][0])
				}
			}
		})
	}
}
