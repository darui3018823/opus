//go:build opusref

package opus

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type celtScalarStageKey struct {
	packet  int
	frame   int
	stage   string
	channel int
}

type celtScalarStageWant struct {
	hash uint64
	n    int
}

var celtScalarStagePattern = regexp.MustCompile(
	`^\[(SYNTH|POSTFILTER|PCM)\] packet=(\d+) frame=(\d+) ch=(\d+) n=(\d+) hash=([0-9a-f]{16})$`,
)

var celtScalarCoefficientPattern = regexp.MustCompile(
	`^\[(NORM|DENORM)_SUMMARY\] packet=(\d+) frame=(\d+) ch=(\d+) n=(\d+) hash=([0-9a-f]{16})(?: energyN=(\d+) energy=([0-9a-f]{16}))?$`,
)

// TestCELTScalarSynthesisStageScan compares every CELT constituent frame in a
// selected official vector with an oracle compiled from the checked-in scalar
// libopus source. It is opt-in because the oracle is a local build artifact.
//
// Build it with scripts/oracle/build_celt_coeff.ps1, then set
// OPUS_CELT_SCALAR_ORACLE to the emitted executable. Set
// OPUS_CELT_SCALAR_VECTOR to select a vector; testvector01.bit is the default.
func TestCELTScalarSynthesisStageScan(t *testing.T) {
	oracle := os.Getenv("OPUS_CELT_SCALAR_ORACLE")
	if oracle == "" {
		t.Skip("set OPUS_CELT_SCALAR_ORACLE to the checked-in scalar CELT oracle")
	}
	vector := os.Getenv("OPUS_CELT_SCALAR_VECTOR")
	if vector == "" {
		vector = "testvector01.bit"
	}
	vectorPath := filepath.Join("testdata", "opus_newvectors", vector)
	if _, err := os.Stat(vectorPath); err != nil {
		t.Skipf("official vector unavailable: %v", err)
	}

	out, err := exec.Command(oracle, vectorPath, "--all-stages").CombinedOutput()
	if err != nil {
		t.Fatalf("run scalar oracle: %v\n%s", err, out)
	}
	want := parseCELTScalarStageHashes(t, out)
	if len(want) == 0 {
		t.Fatal("scalar oracle produced no stage hashes")
	}

	packets := readOpusDemoPackets(t, vector)
	decoder, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	currentPacket := -1
	frameCounts := make(map[string]int)
	comparedKeys := make(map[celtScalarStageKey]bool)
	compared := 0
	compare := func(stage string, channel int, values []float64, required bool) {
		counter := fmt.Sprintf("%s/%d", stage, channel)
		frame := frameCounts[counter]
		frameCounts[counter] = frame + 1
		key := celtScalarStageKey{
			packet: currentPacket, frame: frame, stage: stage, channel: channel,
		}
		expected, ok := want[key]
		if !ok {
			if !required {
				return
			}
			t.Fatalf("Go emitted unexpected stage %+v", key)
		}
		if len(values) < expected.n {
			t.Fatalf("stage %+v has %d values, oracle hashed %d", key, len(values), expected.n)
		}
		got := hashFloat32Slice(values[:expected.n])
		if got != expected.hash {
			t.Fatalf("first scalar stage mismatch: packet=%d frame=%d stage=%s channel=%d got=%016x want=%016x",
				currentPacket, frame, stage, channel, got, expected.hash)
		}
		comparedKeys[key] = true
		compared++
	}
	synthesisHook := func(stage string, channel int, samples []float64) {
		stage = strings.ToUpper(stage)
		if stage == "SYNTHESIS" {
			stage = "SYNTH"
		}
		values := samples
		if stage == "PCM" {
			values = make([]float64, len(samples))
			for i, sample := range samples {
				values[i] = sample * (1.0 / 32768.0)
			}
		}
		compare(stage, channel, values, true)
	}
	coefficientHook := func(stage string, channel int, coeffs, energies []float64) {
		switch stage {
		case "normalized":
			// libopus omits coefficient callbacks on some silence/PLC paths,
			// while the Go diagnostic hook still reports its zeroed buffers.
			// Treat those Go-only diagnostic callbacks as optional; every stage
			// actually emitted by the C oracle remains mandatory via the final
			// compared-count check below.
			compare("NORM", channel, coeffs, false)
			compare("ENERGY", channel, energies, false)
		case "denormalized":
			compare("DENORM", channel, coeffs, false)
		default:
			t.Fatalf("unexpected coefficient stage %q", stage)
		}
	}
	for bandwidth := range decoder.celtDecoders {
		for lm := range decoder.celtDecoders[bandwidth] {
			for channel := range decoder.celtDecoders[bandwidth][lm] {
				decoder.celtDecoders[bandwidth][lm][channel].SetCoefficientStageHook(coefficientHook)
				decoder.celtDecoders[bandwidth][lm][channel].SetSynthesisStageHook(synthesisHook)
			}
		}
	}

	for packet, record := range packets {
		currentPacket = packet
		clear(frameCounts)
		if _, err := decoder.DecodeFloat(record.packet); err != nil {
			t.Fatalf("decode packet %d: %v", packet, err)
		}
	}
	for key, expected := range want {
		if comparedKeys[key] {
			continue
		}
		// The C oracle is configured for stereo output and synthesizes a second
		// identical channel for mono streams. Go duplicates that channel in the
		// outer Opus decoder, after these internal CELT hooks. Accept only an
		// exact C-side duplicate of the already-compared channel-zero stage.
		channelZero := key
		channelZero.channel = 0
		if key.channel == 1 && comparedKeys[channelZero] && want[channelZero] == expected {
			continue
		}
		t.Fatalf("C oracle stage was not emitted by Go: %+v (compared %d of %d)",
			key, compared, len(want))
	}
	t.Logf("%s: all %d scalar CELT stage hashes match", vector, compared)
}

func parseCELTScalarStageHashes(t *testing.T, output []byte) map[celtScalarStageKey]celtScalarStageWant {
	t.Helper()
	hashes := make(map[celtScalarStageKey]celtScalarStageWant)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if match := celtScalarStagePattern.FindStringSubmatch(line); match != nil {
			addCELTScalarHash(t, hashes, match[1], match[2], match[3], match[4], match[5], match[6])
			continue
		}
		if match := celtScalarCoefficientPattern.FindStringSubmatch(line); match != nil {
			addCELTScalarHash(t, hashes, match[1], match[2], match[3], match[4], match[5], match[6])
			if match[1] == "NORM" {
				if match[7] == "" || match[8] == "" {
					t.Fatalf("normalized summary lacks energy hash: %s", line)
				}
				addCELTScalarHash(t, hashes, "ENERGY", match[2], match[3], match[4], match[7], match[8])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan scalar oracle output: %v", err)
	}
	return hashes
}

func addCELTScalarHash(t *testing.T, hashes map[celtScalarStageKey]celtScalarStageWant,
	stage, packetText, frameText, channelText, countText, hashText string,
) {
	t.Helper()
	key := celtScalarStageKey{
		packet:  mustCELTScalarInt(t, packetText),
		frame:   mustCELTScalarInt(t, frameText),
		stage:   stage,
		channel: mustCELTScalarInt(t, channelText),
	}
	hash, err := strconv.ParseUint(hashText, 16, 64)
	if err != nil {
		t.Fatalf("parse scalar hash %q: %v", hashText, err)
	}
	if _, exists := hashes[key]; exists {
		t.Fatalf("duplicate scalar stage hash %+v", key)
	}
	hashes[key] = celtScalarStageWant{hash: hash, n: mustCELTScalarInt(t, countText)}
}

func mustCELTScalarInt(t *testing.T, value string) int {
	t.Helper()
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse scalar integer %q: %v", value, err)
	}
	return n
}
