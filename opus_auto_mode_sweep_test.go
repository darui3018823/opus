//go:build opusref

package opus

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// sweepCell is one ModePolicyLibopus configuration of TestAutoModeOracleSweep.
type sweepCell struct {
	// frameUs is the packet duration in microseconds (2.5 ms is 2500).
	rate, channels, bitrate, complexity, frameUs, frames, lossPerc int
	vbr, constrained, int16In, dtx                                 bool
	signal, app, fixture                                           string
	bandwidth, maxBandwidth, forceChannels, lsbDepth               int
	// schedule, when set, is the per-frame bitrate (the last entry holds);
	// bitrate is then its first entry.
	schedule []int
}

func (c sweepCell) name() string {
	mode := "cbr"
	if c.vbr && c.constrained {
		mode = "cvbr"
	} else if c.vbr {
		mode = "uvbr"
	}
	n := fmt.Sprintf("in%dk/ch%d/%dk/%s/c%d/%gms/%s-%s", c.rate/1000, c.channels, c.bitrate/1000, mode, c.complexity, float64(c.frameUs)/1000, c.app, c.signal)
	if c.lossPerc > 0 {
		n += fmt.Sprintf("/loss%d", c.lossPerc)
	}
	if c.dtx {
		n += "/dtx"
	}
	if c.int16In {
		n += "/int16"
	}
	if c.lsbDepth != 0 {
		n += fmt.Sprintf("/lsb%d", c.lsbDepth)
	}
	if c.bandwidth != 0 {
		n += "/bw-" + sweepBandwidthName(c.bandwidth)
	}
	if c.maxBandwidth != 0 {
		n += "/maxbw-" + sweepBandwidthName(c.maxBandwidth)
	}
	if c.forceChannels != 0 {
		n += fmt.Sprintf("/fc%d", c.forceChannels)
	}
	if len(c.schedule) > 0 {
		steps := []string{}
		prev := -1
		for _, b := range c.schedule {
			if b != prev {
				steps = append(steps, strconv.Itoa(b/1000)+"k")
				prev = b
			}
		}
		n += "/sched-" + strings.Join(steps, ">")
	}
	return n
}

func sweepBandwidthName(bw int) string {
	switch bw {
	case BandwidthNarrowband:
		return "nb"
	case BandwidthMediumband:
		return "mb"
	case BandwidthWideband:
		return "wb"
	case BandwidthSuperWideband:
		return "swb"
	case BandwidthFullband:
		return "fb"
	}
	return strconv.Itoa(bw)
}

// oracleOptions is the oracle's trailing option string for the cell.
func (c sweepCell) oracleOptions() string {
	opts := []string{"notrace=1"}
	if !c.constrained {
		opts = append(opts, "vbrc=0")
	}
	if c.bandwidth != 0 {
		opts = append(opts, "bw="+sweepBandwidthName(c.bandwidth))
	}
	if c.maxBandwidth != 0 {
		opts = append(opts, "maxbw="+sweepBandwidthName(c.maxBandwidth))
	}
	if c.forceChannels != 0 {
		opts = append(opts, "fc="+strconv.Itoa(c.forceChannels))
	}
	if c.int16In {
		opts = append(opts, "int16=1")
	}
	if c.lsbDepth != 0 {
		opts = append(opts, "lsb="+strconv.Itoa(c.lsbDepth))
	}
	return strings.Join(opts, ",")
}

// TestAutoModeOracleSweep checks ModePolicyLibopus against the libopus
// oracle over a broad matrix (2340 cells) beyond the traced gate tests: every
// bitrate step, CBR / constrained / unconstrained VBR, complexities 0-10, the
// "auto" signal hint with the tonality analysis, 8-24 kHz input, 40-120 ms
// packets, forced / capped bandwidths, forced mono, int16 input and a
// reduced LSB depth, and DTX / FEC away from 16/48 kHz. The oracle runs
// without its traces, so the whole sweep takes seconds; cells run in
// parallel and a mismatching cell fails its subtest.
func TestAutoModeOracleSweep(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	groups := map[string][]sweepCell{}
	add := func(group string, c sweepCell) {
		if c.frameUs == 0 {
			c.frameUs = 20000
		}
		if c.frames == 0 {
			c.frames = 240000 / c.frameUs
		}
		if c.fixture == "" {
			c.fixture = "ref-speech"
		}
		groups[group] = append(groups[group], c)
	}
	type rateMode struct{ vbr, constrained bool }
	rateModes := []rateMode{{false, false}, {true, true}, {true, false}}
	for _, app := range []string{"voip", "audio"} {
		for _, signal := range []string{"voice", "music", "auto"} {
			for _, ch := range []int{1, 2} {
				for _, kbps := range []int{8, 16, 24, 32, 48, 64, 96, 160, 256} {
					for _, rm := range rateModes {
						for _, cpx := range []int{0, 3, 7, 10} {
							add("core", sweepCell{rate: 48000, channels: ch, bitrate: kbps * 1000, complexity: cpx, vbr: rm.vbr, constrained: rm.constrained, signal: signal, app: app})
						}
					}
				}
				for _, rate := range []int{8000, 12000, 16000, 24000} {
					for _, kbps := range []int{8, 16, 32, 64} {
						for _, cpx := range []int{0, 7, 10} {
							add("rates", sweepCell{rate: rate, channels: ch, bitrate: kbps * 1000, complexity: cpx, vbr: true, constrained: true, signal: signal, app: app})
						}
					}
				}
			}
		}
	}
	for _, frameMs := range []int{40, 60, 80, 100, 120} {
		for _, rate := range []int{16000, 48000} {
			for _, ch := range []int{1, 2} {
				for _, kbps := range []int{12, 24, 64} {
					for _, signal := range []string{"voice", "music"} {
						for _, cpx := range []int{5, 9} {
							add("frames", sweepCell{rate: rate, channels: ch, bitrate: kbps * 1000, complexity: cpx, frameUs: frameMs * 1000, vbr: true, constrained: true, signal: signal, app: "voip"})
						}
					}
				}
			}
		}
	}
	// Packets shorter than 20 ms: 2.5 and 5 ms are CELT-only, 10 ms may
	// be SILK or hybrid; transitions to and from them come from bitrate
	// schedules elsewhere.
	for _, frameUs := range []int{2500, 5000, 10000} {
		for _, rate := range []int{16000, 48000} {
			for _, ch := range []int{1, 2} {
				for _, kbps := range []int{12, 24, 64, 128} {
					for _, signal := range []string{"voice", "music"} {
						for _, app := range []string{"voip", "audio"} {
							add("short", sweepCell{rate: rate, channels: ch, bitrate: kbps * 1000, complexity: 5, frameUs: frameUs, vbr: true, constrained: true, signal: signal, app: app})
						}
					}
				}
			}
		}
	}
	// Mode and bandwidth transitions with packets shorter than 20 ms: the
	// switch rules change below 10 ms (no deferred switch to CELT, no
	// redundancy).
	for _, frameUs := range []int{2500, 5000, 10000} {
		frames := 240000 / frameUs
		k := min(frames/3, 30)
		steps := func(rates ...int) []int {
			var out []int
			for i, r := range rates {
				n := k
				if i == len(rates)-1 {
					n = 1
				}
				for j := 0; j < n; j++ {
					out = append(out, r)
				}
			}
			return out
		}
		for _, rate := range []int{16000, 48000} {
			for _, ch := range []int{1, 2} {
				for _, signal := range []string{"voice", "music"} {
					for _, app := range []string{"voip", "audio"} {
						for _, sched := range [][]int{steps(12000, 128000), steps(128000, 12000), steps(8000, 24000), steps(24000, 128000, 16000)} {
							add("short-transitions", sweepCell{rate: rate, channels: ch, bitrate: sched[0], schedule: sched, complexity: 5, frameUs: frameUs, frames: frames, vbr: true, constrained: true, signal: signal, app: app})
						}
					}
				}
			}
		}
		for _, rate := range []int{16000, 48000} {
			for _, ch := range []int{1, 2} {
				for _, kbps := range []int{12, 24} {
					for _, cpx := range []int{5, 9} {
						add("short-dtx", sweepCell{rate: rate, channels: ch, bitrate: kbps * 1000, complexity: cpx, frameUs: frameUs, vbr: true, constrained: true, signal: "voice", app: "voip", dtx: true, fixture: "ref-speech-gaps", frames: 2400000 / frameUs})
					}
				}
				for _, kbps := range []int{16, 32} {
					add("short-fec", sweepCell{rate: rate, channels: ch, bitrate: kbps * 1000, complexity: 5, frameUs: frameUs, vbr: true, constrained: true, signal: "voice", app: "voip", lossPerc: 20})
				}
			}
		}
	}
	bws := []int{BandwidthNarrowband, BandwidthMediumband, BandwidthWideband, BandwidthSuperWideband, BandwidthFullband}
	for _, ch := range []int{1, 2} {
		for _, kbps := range []int{16, 32, 64} {
			for _, signal := range []string{"voice", "music"} {
				base := sweepCell{rate: 48000, channels: ch, bitrate: kbps * 1000, complexity: 5, vbr: true, constrained: true, signal: signal, app: "voip"}
				for _, bw := range bws {
					c := base
					c.bandwidth = bw
					add("forced", c)
					if bw != BandwidthFullband {
						c = base
						c.maxBandwidth = bw
						add("forced", c)
					}
				}
				if ch == 2 {
					c := base
					c.forceChannels = 1
					add("forced", c)
				}
				for _, cpx := range []int{5, 9} {
					c := base
					c.complexity = cpx
					c.int16In = true
					add("input", c)
					c.int16In = false
					c.lsbDepth = 16
					add("input", c)
					c.lsbDepth = 8
					add("input", c)
				}
			}
		}
	}
	for _, rate := range []int{8000, 12000, 24000} {
		for _, ch := range []int{1, 2} {
			for _, kbps := range []int{12, 24, 32} {
				add("fec", sweepCell{rate: rate, channels: ch, bitrate: kbps * 1000, complexity: 5, vbr: true, constrained: true, signal: "voice", app: "voip", lossPerc: 20})
			}
			for _, kbps := range []int{12, 24} {
				for _, cpx := range []int{5, 9} {
					add("dtx", sweepCell{rate: rate, channels: ch, bitrate: kbps * 1000, complexity: cpx, vbr: true, constrained: true, signal: "voice", app: "voip", dtx: true, fixture: "ref-speech-gaps", frames: 120})
				}
			}
		}
	}

	for _, group := range []string{"core", "rates", "frames", "forced", "input", "fec", "dtx", "short", "short-transitions", "short-dtx", "short-fec"} {
		cells := groups[group]
		var bad atomic.Int32
		t.Run(group, func(t *testing.T) {
			for _, c := range cells {
				t.Run(c.name(), func(t *testing.T) {
					t.Parallel()
					// Count every failed cell, including a t.Fatal (e.g. the
					// oracle failing to run).
					defer func() {
						if t.Failed() {
							bad.Add(1)
						}
					}()
					runSweepCell(t, c)
				})
			}
		})
		t.Logf("%s: %d/%d cells differ", group, bad.Load(), len(cells))
	}
}

// runSweepCell encodes one sweep configuration with the Go encoder and the
// oracle and reports whether every packet is byte-identical.
func runSweepCell(t *testing.T, c sweepCell) bool {
	vbrArg, dtxArg := "0", "0"
	if c.vbr {
		vbrArg = "1"
	}
	if c.dtx {
		dtxArg = "1"
	}
	bitrateArg := strconv.Itoa(c.bitrate)
	if len(c.schedule) > 0 {
		parts := make([]string, len(c.schedule))
		for i, b := range c.schedule {
			parts[i] = strconv.Itoa(b)
		}
		bitrateArg = strings.Join(parts, ",")
	}
	ref := runCELTOracleCmd(t, c.fixture, "--auto-enc", strconv.Itoa(c.rate), c.fixture, strconv.Itoa(c.frames),
		bitrateArg, vbrArg, strconv.Itoa(c.channels), strconv.Itoa(c.complexity), c.signal, c.app,
		strconv.FormatFloat(float64(c.frameUs)/1000, 'g', -1, 64), strconv.Itoa(c.lossPerc), dtxArg, c.oracleOptions())
	app := ApplicationVOIP
	if c.app == "audio" {
		app = ApplicationAudio
	}
	enc, err := NewEncoder(c.rate, c.channels, app)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
		t.Fatal(err)
	}
	switch c.signal {
	case "voice":
		enc.SetSignalType(SignalVoice)
	case "music":
		enc.SetSignalType(SignalMusic)
	}
	if err := enc.SetBitrate(c.bitrate); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(c.vbr)
	enc.SetVBRConstraint(c.constrained)
	if err := enc.SetComplexity(c.complexity); err != nil {
		t.Fatal(err)
	}
	if c.lossPerc > 0 {
		enc.SetInbandFEC(true)
		enc.SetPacketLossPerc(c.lossPerc)
	}
	enc.SetDTX(c.dtx)
	if c.bandwidth != 0 {
		if err := enc.SetBandwidth(c.bandwidth); err != nil {
			t.Fatal(err)
		}
	}
	if c.maxBandwidth != 0 {
		if err := enc.SetMaxBandwidth(c.maxBandwidth); err != nil {
			t.Fatal(err)
		}
	}
	if c.forceChannels != 0 {
		if err := enc.SetForceChannels(c.forceChannels); err != nil {
			t.Fatal(err)
		}
	}
	if c.lsbDepth != 0 {
		if err := enc.SetLSBDepth(c.lsbDepth); err != nil {
			t.Fatal(err)
		}
	}
	frameSize := c.rate * c.frameUs / 1000000
	identical, firstDiff := 0, -1
	bitrate := c.bitrate
	for f := 0; f < c.frames; f++ {
		if len(c.schedule) > 0 {
			if b := c.schedule[min(f, len(c.schedule)-1)]; b != bitrate {
				bitrate = b
				if err := enc.SetBitrate(bitrate); err != nil {
					t.Fatal(err)
				}
			}
		}
		var pcm []float64
		switch {
		case c.fixture == "ref-speech-gaps":
			pcm = refSpeechGapsFrame(c.rate, c.channels, f*frameSize, frameSize)
		case c.channels == 2:
			pcm = silkRefSpeechFrameStereo(c.rate, f*frameSize, frameSize)
		default:
			pcm = encOracleRefSpeechFrame(c.rate, f*frameSize, frameSize)
		}
		for i, v := range pcm {
			pcm[i] = math.Floor(float64(float32(v))*32768+0.5) / 32768
		}
		var pkt []byte
		if c.int16In {
			pcm16 := make([]int16, len(pcm))
			for i, v := range pcm {
				s := math.Floor(v*32768 + 0.5)
				pcm16[i] = int16(math.Max(-32768, math.Min(32767, s)))
			}
			pkt, err = enc.Encode(pcm16, frameSize)
		} else {
			pkt, err = enc.EncodeFloat(pcm, frameSize)
		}
		if err != nil {
			t.Fatalf("frame %d: %v", f, err)
		}
		r := ref[f]
		if !r.havePacket {
			t.Fatalf("frame %d: incomplete oracle trace", f)
		}
		if bytes.Equal(pkt, r.packet) {
			identical++
		} else if firstDiff < 0 {
			firstDiff = f
			t.Logf("frame %d: Go %d B TOC %#x / C %d B TOC %#x", f, len(pkt), pkt[0], len(r.packet), r.packet[0])
		}
	}
	if identical != c.frames {
		t.Errorf("%d/%d packets byte-identical (first difference at frame %d)", identical, c.frames, firstDiff)
		return false
	}
	return true
}
