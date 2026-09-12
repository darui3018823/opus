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

var celtScalarStagePattern = regexp.MustCompile(
	`^\[(SYNTH|POSTFILTER|PCM)\] packet=(\d+) frame=(\d+) ch=(\d+) n=(\d+) hash=([0-9a-f]{16})$`,
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
	compared := 0
	hook := func(stage string, channel int, samples []float64) {
		stage = strings.ToUpper(stage)
		if stage == "SYNTHESIS" {
			stage = "SYNTH"
		}
		counter := fmt.Sprintf("%s/%d", stage, channel)
		frame := frameCounts[counter]
		frameCounts[counter] = frame + 1
		key := celtScalarStageKey{
			packet: currentPacket, frame: frame, stage: stage, channel: channel,
		}
		expected, ok := want[key]
		if !ok {
			t.Fatalf("Go emitted unexpected stage %+v", key)
		}
		values := samples
		if stage == "PCM" {
			values = make([]float64, len(samples))
			for i, sample := range samples {
				values[i] = sample * (1.0 / 32768.0)
			}
		}
		got := hashFloat32Slice(values)
		if got != expected {
			t.Fatalf("first scalar stage mismatch: packet=%d frame=%d stage=%s channel=%d got=%016x want=%016x",
				currentPacket, frame, stage, channel, got, expected)
		}
		compared++
	}
	for bandwidth := range decoder.celtDecoders {
		for lm := range decoder.celtDecoders[bandwidth] {
			for channel := range decoder.celtDecoders[bandwidth][lm] {
				decoder.celtDecoders[bandwidth][lm][channel].SetSynthesisStageHook(hook)
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
	if compared != len(want) {
		t.Fatalf("compared %d stage hashes, oracle emitted %d", compared, len(want))
	}
	t.Logf("%s: all %d scalar CELT stage hashes match", vector, compared)
}

func parseCELTScalarStageHashes(t *testing.T, output []byte) map[celtScalarStageKey]uint64 {
	t.Helper()
	hashes := make(map[celtScalarStageKey]uint64)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		match := celtScalarStagePattern.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		packet := mustCELTScalarInt(t, match[2])
		frame := mustCELTScalarInt(t, match[3])
		channel := mustCELTScalarInt(t, match[4])
		hash, err := strconv.ParseUint(match[6], 16, 64)
		if err != nil {
			t.Fatalf("parse scalar hash %q: %v", match[6], err)
		}
		key := celtScalarStageKey{packet: packet, frame: frame, stage: match[1], channel: channel}
		if _, exists := hashes[key]; exists {
			t.Fatalf("duplicate scalar stage hash %+v", key)
		}
		hashes[key] = hash
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan scalar oracle output: %v", err)
	}
	return hashes
}

func mustCELTScalarInt(t *testing.T, value string) int {
	t.Helper()
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse scalar integer %q: %v", value, err)
	}
	return n
}
