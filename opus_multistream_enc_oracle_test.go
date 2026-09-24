//go:build opusref

package opus

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// msTwoPi is a variable so that the fixture products are rounded one by one,
// as the oracle's C evaluates them, instead of being folded as constants.
var msTwoPi = 2 * math.Pi

// msFixtureSample is the oracle's mc / mc-gaps fixture (ms_fixture_sample):
// channel c is a harmonic tone of its own pitch, envelope and phase plus a
// little hashed noise; mc-gaps puts ref-speech-gaps' silence and noise
// segments over it.
func msFixtureSample(fixture string, idx, c, rate int) float64 {
	tm := float64(idx) / float64(rate)
	cf := float64(c)
	f0 := 140.0 + 45.0*cf
	envArg := msTwoPi * (2.0 + 0.7*cf)
	envArg = envArg*tm + 0.4*cf
	env := 0.5 + 0.3*math.Sin(envArg)
	a1 := msTwoPi * f0
	a1 = a1*tm + 0.3*cf
	a2 := msTwoPi * 2.0
	a2 = a2 * f0
	a2 = a2*tm + 0.9
	a3 := msTwoPi * 3.3
	a3 = a3 * f0
	a3 = a3*tm + 0.2*cf
	v := env * (0.25*math.Sin(a1) + 0.10*math.Sin(a2) + 0.05*math.Sin(a3))
	v += 10.0 * msGapsNoise(idx, c+7, 2)
	if fixture == "mc-gaps" {
		switch seg := msGapsSegment(idx, rate); {
		case seg == 1:
			return 0
		case seg >= 2:
			return msGapsNoise(idx, c, seg)
		}
	}
	return v
}

func msGapsSegment(idx, rate int) int {
	pos := idx % (rate * 12 / 5)
	switch {
	case pos < rate*2/5:
		return 0
	case pos < rate*9/10:
		return 1
	case pos < rate*7/5:
		return 2
	case pos < rate*23/10:
		return 3
	}
	return 0
}

func msGapsNoise(idx, ch, seg int) float64 {
	h := uint32(idx)*2654435761 + uint32(ch)*40503 + 12345
	h ^= h >> 15
	h *= 2246822519
	h ^= h >> 13
	amp := 0.0002
	if seg == 2 {
		amp = 0.002
	}
	return (float64(h&0xffff)/65536.0 - 0.5) * amp
}

// msCell is one multistream oracle configuration. family is the oracle's
// encoder selector: 0/1/255 surround (NewSurroundEncoder), -1 the generic
// multistream encoder with the family-1 layout, 2 or 3 the projection
// encoder.
type msCell struct {
	rate, channels, family, bitrate, complexity, frameUs, frames, lossPerc int
	vbr, constrained, int16In, dtx                                         bool
	signal, app, fixture                                                   string
	schedule                                                               []int
}

func (c msCell) name() string {
	vbr := "cbr"
	if c.vbr {
		vbr = "vbr"
		if c.constrained {
			vbr = "cvbr"
		}
	}
	br := "auto"
	if c.bitrate > 0 {
		br = strconv.Itoa(c.bitrate/1000) + "k"
	}
	n := fmt.Sprintf("fam%d/%dk/%dch/%s/%s/c%d/%s/%s/%s/%gms", c.family, c.rate/1000, c.channels, br, vbr,
		c.complexity, c.signal, c.app, c.fixture, float64(c.frameUs)/1000)
	if c.lossPerc > 0 {
		n += fmt.Sprintf("/loss%d", c.lossPerc)
	}
	if c.dtx {
		n += "/dtx"
	}
	if c.int16In {
		n += "/int16"
	}
	if len(c.schedule) > 0 {
		n += "/sched"
	}
	return n
}

// msEncoder is the common surface of the Go multistream encoders.
type msEncoder interface {
	SetModePolicy(ModePolicy) error
	SetBitrate(int) error
	SetVBR(bool)
	SetVBRConstraint(bool)
	SetComplexity(int) error
	Encode([]int16, int) ([]byte, error)
	EncodeFloat([]float64, int) ([]byte, error)
}

func newMSOracleEncoder(t *testing.T, c msCell, app Application) (msEncoder, *MultistreamEncoder) {
	t.Helper()
	switch c.family {
	case -1:
		layout, err := surroundLayoutFor(c.channels, MappingFamilyVorbis)
		if c.channels > 8 {
			layout, err = surroundLayoutFor(c.channels, MappingFamilyDiscrete)
		}
		if err != nil {
			t.Fatal(err)
		}
		enc, err := NewMultistreamEncoder(c.rate, c.channels, layout.streams, layout.coupledStreams, layout.mapping, app)
		if err != nil {
			t.Fatal(err)
		}
		return enc, enc
	case 2, 3:
		enc, err := NewProjectionEncoder(c.rate, c.channels, c.family, app)
		if err != nil {
			t.Fatal(err)
		}
		return enc, enc.multistream
	default:
		enc, err := NewSurroundEncoder(c.rate, c.channels, c.family, app)
		if err != nil {
			t.Fatal(err)
		}
		return enc, enc.MultistreamEncoder
	}
}

func runMSOracleCell(t *testing.T, c msCell) {
	t.Helper()
	vbrArg, dtxArg := "0", "0"
	if c.vbr {
		vbrArg = "1"
	}
	if c.dtx {
		dtxArg = "1"
	}
	bitrateArg := strconv.Itoa(c.bitrate)
	if c.bitrate == 0 {
		bitrateArg = strconv.Itoa(BitrateAuto)
	}
	if len(c.schedule) > 0 {
		parts := make([]string, len(c.schedule))
		for i, b := range c.schedule {
			parts[i] = strconv.Itoa(b)
		}
		bitrateArg = strings.Join(parts, ",")
	}
	var opts []string
	if !c.constrained {
		opts = append(opts, "vbrc=0")
	}
	if c.int16In {
		opts = append(opts, "int16=1")
	}
	optArg := "-"
	if len(opts) > 0 {
		optArg = strings.Join(opts, ",")
	}
	ref := runCELTOracleCmd(t, c.fixture, "--ms-enc", strconv.Itoa(c.rate), c.fixture, strconv.Itoa(c.frames),
		bitrateArg, vbrArg, strconv.Itoa(c.channels), strconv.Itoa(c.complexity), c.signal, c.app,
		strconv.FormatFloat(float64(c.frameUs)/1000, 'g', -1, 64), strconv.Itoa(c.lossPerc), dtxArg,
		strconv.Itoa(c.family), optArg)

	app := ApplicationVOIP
	switch c.app {
	case "audio":
		app = ApplicationAudio
	case "lowdelay":
		app = ApplicationRestrictedLowDelay
	}
	enc, ms := newMSOracleEncoder(t, c, app)
	if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
		t.Fatal(err)
	}
	bitrate := c.bitrate
	if bitrate == 0 {
		bitrate = BitrateAuto
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(c.vbr)
	enc.SetVBRConstraint(c.constrained)
	if err := enc.SetComplexity(c.complexity); err != nil {
		t.Fatal(err)
	}
	switch c.signal {
	case "voice":
		ms.SetSignalType(SignalVoice)
	case "music":
		ms.SetSignalType(SignalMusic)
	}
	if c.lossPerc > 0 {
		ms.SetInbandFEC(true)
		ms.SetPacketLossPerc(c.lossPerc)
	}
	ms.SetDTX(c.dtx)

	frameSize := c.rate * c.frameUs / 1000000
	identical, firstDiff := 0, -1
	for f := 0; f < c.frames; f++ {
		if len(c.schedule) > 0 {
			if b := c.schedule[min(f, len(c.schedule)-1)]; b != bitrate {
				bitrate = b
				if err := enc.SetBitrate(bitrate); err != nil {
					t.Fatal(err)
				}
			}
		}
		pcm := make([]float64, frameSize*c.channels)
		for i := 0; i < frameSize; i++ {
			for ch := 0; ch < c.channels; ch++ {
				v := float32(msFixtureSample(c.fixture, f*frameSize+i, ch, c.rate))
				pcm[i*c.channels+ch] = math.Floor(float64(v)*32768+0.5) / 32768
			}
		}
		var pkt []byte
		var err error
		if c.int16In {
			pcm16 := make([]int16, len(pcm))
			for i, v := range pcm {
				pcm16[i] = int16(math.Max(-32768, math.Min(32767, math.Floor(v*32768+0.5))))
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
			t.Logf("frame %d: Go %d B / C %d B", f, len(pkt), len(r.packet))
			goStreams, _, errGo := splitMultistreamPackets(pkt, ms.streams, c.rate)
			cStreams, _, errC := splitMultistreamPackets(r.packet, ms.streams, c.rate)
			if errGo == nil && errC == nil {
				for s := range goStreams {
					at := -1
					for i := 0; i < min(len(goStreams[s]), len(cStreams[s])); i++ {
						if goStreams[s][i] != cStreams[s][i] {
							at = i
							break
						}
					}
					t.Logf("  stream %d: Go %d B TOC %#x / C %d B TOC %#x equal=%v first diff at byte %d", s, len(goStreams[s]), goStreams[s][0],
						len(cStreams[s]), cStreams[s][0], bytes.Equal(goStreams[s], cStreams[s]), at)
					if at >= 0 && len(goStreams[s]) <= 64 {
						t.Logf("    Go %x", goStreams[s])
						t.Logf("    C  %x", cStreams[s])
					}
				}
			}
		}
	}
	if identical != c.frames {
		t.Errorf("%d/%d packets byte-identical (first difference at frame %d)", identical, c.frames, firstDiff)
	}
}

// TestMultistreamEncoderOracle compares the multistream, surround and
// projection encoders under ModePolicyLibopus with libopus's
// opus_multistream_encode_native.
func TestMultistreamEncoderOracle(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	var cells []msCell
	base := msCell{rate: 48000, complexity: 5, frameUs: 20000, frames: 25, vbr: true, constrained: true,
		signal: "auto", app: "audio", fixture: "mc"}
	add := func(mod func(*msCell)) {
		c := base
		mod(&c)
		cells = append(cells, c)
	}
	// Layouts and rates: the generic encoder, every family-1 layout, the
	// families 0 and 255, and the ambisonics families 2 and 3.
	layouts := []struct{ family, channels int }{
		{-1, 3}, {-1, 6}, {-1, 8},
		{1, 1}, {1, 2}, {1, 3}, {1, 4}, {1, 5}, {1, 6}, {1, 7}, {1, 8},
		{0, 1}, {0, 2}, {255, 3}, {255, 11},
		{2, 4}, {2, 9}, {2, 11}, {3, 4}, {3, 9}, {3, 16},
	}
	for _, l := range layouts {
		for _, perChannel := range []int{0, 12000, 40000} {
			add(func(c *msCell) {
				c.family, c.channels = l.family, l.channels
				c.bitrate = perChannel * l.channels
			})
		}
	}
	// Rate control, packet durations, input rates, applications, signal
	// hints, complexities and int16 input on the 5.1 and ambisonics layouts.
	for _, l := range []struct{ family, channels int }{{1, 6}, {1, 3}, {3, 9}, {2, 4}} {
		for _, perChannel := range []int{8000, 24000} {
			br := perChannel * l.channels
			add(func(c *msCell) { c.family, c.channels, c.bitrate, c.vbr = l.family, l.channels, br, false })
			add(func(c *msCell) { c.family, c.channels, c.bitrate, c.constrained = l.family, l.channels, br, false })
			for _, us := range []int{2500, 10000, 40000, 60000} {
				add(func(c *msCell) {
					c.family, c.channels, c.bitrate, c.frameUs = l.family, l.channels, br, us
					c.frames = max(10, 500000/us)
				})
			}
			add(func(c *msCell) { c.family, c.channels, c.bitrate, c.rate = l.family, l.channels, br, 16000 })
			add(func(c *msCell) {
				c.family, c.channels, c.bitrate, c.app, c.signal = l.family, l.channels, br, "voip", "voice"
			})
			add(func(c *msCell) { c.family, c.channels, c.bitrate, c.signal = l.family, l.channels, br, "music" })
			add(func(c *msCell) { c.family, c.channels, c.bitrate, c.complexity = l.family, l.channels, br, 10 })
			add(func(c *msCell) { c.family, c.channels, c.bitrate, c.int16In = l.family, l.channels, br, true })
			add(func(c *msCell) {
				c.family, c.channels, c.bitrate, c.app, c.lossPerc = l.family, l.channels, br, "voip", 20
			})
			add(func(c *msCell) {
				c.family, c.channels, c.bitrate, c.app, c.dtx, c.fixture = l.family, l.channels, br, "voip", true, "mc-gaps"
				c.frames = 120
			})
		}
		add(func(c *msCell) {
			c.family, c.channels = l.family, l.channels
			c.schedule = []int{}
			for _, pc := range []int{8000, 48000, 16000} {
				for i := 0; i < 8; i++ {
					c.schedule = append(c.schedule, pc*l.channels)
				}
			}
			c.bitrate = c.schedule[0]
		})
	}
	for _, c := range cells {
		t.Run(c.name(), func(t *testing.T) {
			t.Parallel()
			runMSOracleCell(t, c)
		})
	}
}
