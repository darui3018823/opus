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
	"strconv"
	"strings"
	"testing"
)

// decPLCFrame is one frame of the --dec-plc oracle: the libopus packet,
// whether it was concealed, and libopus's decoded float PCM.
type decPLCFrame struct {
	lost   bool
	packet []byte
	pcm    []float32
}

func runDecPLCOracle(t *testing.T, args ...string) []decPLCFrame {
	t.Helper()
	cmd := exec.Command(encOraclePath(), append([]string{"--dec-plc"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("enc_oracle --dec-plc %v: %v\n%s", args, err, stderr.String())
	}
	var out []decPLCFrame
	sc := bufio.NewScanner(&stderr)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "[CELT_ENC_INPUT_FRAME]":
			out = append(out, decPLCFrame{lost: f[len(f)-1] == "lost=1"})
		case "[ENC_PACKET]":
			pkt, err := hex.DecodeString(strings.Join(f[2:], ""))
			if err != nil {
				t.Fatal(err)
			}
			out[len(out)-1].packet = pkt
		case "[DEC_PCM]":
			vals := make([]float32, 0, len(f)-2)
			for _, v := range f[2:] {
				x, err := parseOracleFloat(v)
				if err != nil {
					t.Fatal(err)
				}
				vals = append(vals, float32(x))
			}
			out[len(out)-1].pcm = vals
		}
	}
	return out
}

// TestDecoderPLCOracle decodes libopus packets with losses and compares the
// decoded and concealed float PCM with libopus's plain-C float decoder.
func TestDecoderPLCOracle(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	type cell struct {
		rate, channels, bitrate, frameUs, frames int
		app, mask, signal, fixture               string
	}
	var cells []cell
	for _, rate := range []int{48000, 16000} {
		for _, ch := range []int{1, 2} {
			for _, us := range []int{2500, 10000, 20000} {
				for _, mask := range []string{"0001", "000110", "0000111110"} {
					for _, br := range []int{32000, 96000} {
						cells = append(cells, cell{rate, ch, br, us, max(20, 400000/us), "rcelt", mask, "music", "ref-speech"})
					}
				}
			}
		}
	}
	// Other output rates, and SILK / hybrid packets (VOIP voice) whose loss
	// conceals the SILK layer and runs the CELT noise PLC for the hybrid
	// high band.
	for _, rate := range []int{8000, 12000, 24000} {
		for _, ch := range []int{1, 2} {
			cells = append(cells, cell{rate, ch, 48000, 20000, 20, "rcelt", "000110", "music", "ref-speech"})
		}
	}
	for _, rate := range []int{16000, 24000, 48000} {
		for _, ch := range []int{1, 2} {
			for _, br := range []int{12000, 24000, 40000} {
				for _, mask := range []string{"0001", "0000111110"} {
					cells = append(cells, cell{rate, ch, br, 20000, 30, "voip", mask, "voice", "ref-speech"})
				}
			}
			cells = append(cells, cell{rate, ch, 24000, 10000, 40, "voip", "000110", "voice", "ref-speech"})
			cells = append(cells, cell{rate, ch, 24000, 60000, 12, "voip", "0001", "voice", "ref-speech"})
		}
	}
	for _, c := range cells {
		name := fmt.Sprintf("%s/%dk/%dch/%dk/%gms/%s", c.app, c.rate/1000, c.channels, c.bitrate/1000, float64(c.frameUs)/1000, c.mask)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ref := runDecPLCOracle(t, strconv.Itoa(c.rate), strconv.Itoa(c.channels), c.fixture, strconv.Itoa(c.frames),
				strconv.Itoa(c.bitrate), c.app, strconv.FormatFloat(float64(c.frameUs)/1000, 'g', -1, 64), c.mask, c.signal)
			dec, err := NewDecoder(c.rate, c.channels)
			if err != nil {
				t.Fatal(err)
			}
			frameSize := c.rate * c.frameUs / 1000000
			exact, firstDiff := 0, -1
			for f, r := range ref {
				var got []float32
				if r.lost {
					got, err = dec.DecodePLCFloat32(frameSize)
				} else {
					got, err = dec.DecodeFloat32(r.packet)
				}
				if err != nil {
					t.Fatalf("frame %d: %v", f, err)
				}
				same := len(got) == len(r.pcm)
				maxDiff, at := 0.0, -1
				for i := 0; same && i < len(got); i++ {
					if got[i] != r.pcm[i] {
						if d := math.Abs(float64(got[i] - r.pcm[i])); d > maxDiff {
							maxDiff = d
						}
						if at < 0 {
							at = i
						}
					}
				}
				if same && at < 0 {
					exact++
				} else if firstDiff < 0 {
					firstDiff = f
					t.Logf("frame %d (lost=%v): %d/%d samples, first diff at %d, max |diff| %.3g", f, r.lost, len(got), len(r.pcm), at, maxDiff)
				}
			}
			if exact != len(ref) {
				t.Errorf("%d/%d frames sample-exact (first difference at frame %d)", exact, len(ref), firstDiff)
			}
		})
	}
}
