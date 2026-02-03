package testing

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// BitstreamDiff represents a single difference in the bitstream.
type BitstreamDiff struct {
	Position int    // Byte position
	Expected byte   // Expected value from libopus
	Actual   byte   // Actual value from our encoder
	Context  string // Surrounding bytes for context
}

// BitstreamCompareResult holds the result of bitstream comparison.
type BitstreamCompareResult struct {
	Passed       bool
	TotalBytes   int
	MatchedBytes int
	Differences  []BitstreamDiff
	FirstDiffPos int // Position of first difference (-1 if none)
}

// CompareBitstreams compares two byte slices and returns detailed diff.
func CompareBitstreams(expected, actual []byte) *BitstreamCompareResult {
	result := &BitstreamCompareResult{
		Passed:       true,
		FirstDiffPos: -1,
		Differences:  make([]BitstreamDiff, 0),
	}

	maxLen := len(expected)
	if len(actual) > maxLen {
		maxLen = len(actual)
	}
	result.TotalBytes = maxLen

	minLen := len(expected)
	if len(actual) < minLen {
		minLen = len(actual)
	}

	// Compare overlapping region
	for i := 0; i < minLen; i++ {
		if expected[i] == actual[i] {
			result.MatchedBytes++
		} else {
			if result.FirstDiffPos == -1 {
				result.FirstDiffPos = i
			}
			result.Passed = false

			// Only store first 100 differences to avoid memory explosion
			if len(result.Differences) < 100 {
				diff := BitstreamDiff{
					Position: i,
					Expected: expected[i],
					Actual:   actual[i],
					Context:  getContext(expected, actual, i),
				}
				result.Differences = append(result.Differences, diff)
			}
		}
	}

	// Handle length mismatch
	if len(expected) != len(actual) {
		result.Passed = false
		if result.FirstDiffPos == -1 {
			result.FirstDiffPos = minLen
		}
	}

	return result
}

// getContext returns surrounding bytes for debugging.
func getContext(expected, actual []byte, pos int) string {
	start := pos - 4
	if start < 0 {
		start = 0
	}
	end := pos + 5
	if end > len(expected) {
		end = len(expected)
	}
	if end > len(actual) && len(actual) < len(expected) {
		end = len(actual)
	}

	var sb strings.Builder
	sb.WriteString("expected: ")
	for i := start; i < end && i < len(expected); i++ {
		if i == pos {
			sb.WriteString(fmt.Sprintf("[%02x]", expected[i]))
		} else {
			sb.WriteString(fmt.Sprintf(" %02x ", expected[i]))
		}
	}
	sb.WriteString("\n  actual: ")
	for i := start; i < end && i < len(actual); i++ {
		if i == pos {
			sb.WriteString(fmt.Sprintf("[%02x]", actual[i]))
		} else {
			sb.WriteString(fmt.Sprintf(" %02x ", actual[i]))
		}
	}

	return sb.String()
}

// PrintReport outputs comparison result as readable string.
func (r *BitstreamCompareResult) PrintReport() string {
	var sb strings.Builder

	sb.WriteString("=== Bitstream Comparison Report ===\n\n")

	if r.Passed {
		sb.WriteString("✓ BITSTREAMS MATCH\n")
	} else {
		sb.WriteString("✗ BITSTREAMS DIFFER\n")
	}

	sb.WriteString(fmt.Sprintf("Total bytes: %d\n", r.TotalBytes))
	sb.WriteString(fmt.Sprintf("Matched bytes: %d (%.2f%%)\n",
		r.MatchedBytes, float64(r.MatchedBytes)/float64(r.TotalBytes)*100))

	if r.FirstDiffPos >= 0 {
		sb.WriteString(fmt.Sprintf("First difference at: byte %d (0x%x)\n", r.FirstDiffPos, r.FirstDiffPos))
	}

	if len(r.Differences) > 0 {
		sb.WriteString(fmt.Sprintf("\nFirst %d differences:\n", len(r.Differences)))
		for i, diff := range r.Differences {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... and %d more differences\n", len(r.Differences)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("\n[%d] Position %d (0x%x):\n", i+1, diff.Position, diff.Position))
			sb.WriteString(fmt.Sprintf("  Expected: 0x%02x, Actual: 0x%02x\n", diff.Expected, diff.Actual))
			sb.WriteString(fmt.Sprintf("  %s\n", diff.Context))
		}
	}

	return sb.String()
}

// EncodeWithLibopus encodes PCM using libopus opusenc command.
// Returns encoded bytes or error.
//
// Prerequisites: opusenc must be in PATH.
// This is Pure Go - we use exec, not CGO.
func EncodeWithLibopus(pcmData []byte, sampleRate, channels, bitrate int) ([]byte, error) {
	// Create temp files for input/output
	inFile, err := os.CreateTemp("", "opus_test_*.raw")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp input file: %w", err)
	}
	defer os.Remove(inFile.Name())

	outFile, err := os.CreateTemp("", "opus_test_*.opus")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp output file: %w", err)
	}
	defer os.Remove(outFile.Name())

	// Write PCM data
	if _, err := inFile.Write(pcmData); err != nil {
		return nil, fmt.Errorf("failed to write PCM data: %w", err)
	}
	inFile.Close()

	// Run opusenc
	// opusenc --raw --raw-rate <rate> --raw-chan <chan> --bitrate <kbps> input.raw output.opus
	cmd := exec.Command("opusenc",
		"--raw",
		"--raw-rate", fmt.Sprintf("%d", sampleRate),
		"--raw-chan", fmt.Sprintf("%d", channels),
		"--bitrate", fmt.Sprintf("%d", bitrate/1000), // kbps
		inFile.Name(),
		outFile.Name(),
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opusenc failed: %v\nstderr: %s", err, stderr.String())
	}

	// Read output
	outFile, err = os.Open(outFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer outFile.Close()

	return io.ReadAll(outFile)
}

// DecodeWithLibopus decodes Opus using libopus opusdec command.
// Returns decoded PCM or error.
//
// Prerequisites: opusdec must be in PATH.
func DecodeWithLibopus(opusData []byte, sampleRate int) ([]byte, error) {
	// Create temp files
	inFile, err := os.CreateTemp("", "opus_test_*.opus")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp input file: %w", err)
	}
	defer os.Remove(inFile.Name())

	outFile, err := os.CreateTemp("", "opus_test_*.raw")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp output file: %w", err)
	}
	defer os.Remove(outFile.Name())

	// Write Opus data
	if _, err := inFile.Write(opusData); err != nil {
		return nil, fmt.Errorf("failed to write Opus data: %w", err)
	}
	inFile.Close()

	// Run opusdec
	cmd := exec.Command("opusdec",
		"--rate", fmt.Sprintf("%d", sampleRate),
		"--float",
		inFile.Name(),
		outFile.Name(),
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opusdec failed: %v\nstderr: %s", err, stderr.String())
	}

	// Read output
	outFile, err = os.Open(outFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer outFile.Close()

	return io.ReadAll(outFile)
}

// CompareEncoderOutput encodes same PCM with both libopus and our encoder,
// then compares the bitstreams.
func CompareEncoderOutput(pcmData []byte, sampleRate, channels, bitrate int, ourEncoder func([]byte) ([]byte, error)) (*BitstreamCompareResult, error) {
	// Encode with libopus
	libopusOutput, err := EncodeWithLibopus(pcmData, sampleRate, channels, bitrate)
	if err != nil {
		return nil, fmt.Errorf("libopus encoding failed: %w", err)
	}

	// Encode with our encoder
	ourOutput, err := ourEncoder(pcmData)
	if err != nil {
		return nil, fmt.Errorf("our encoder failed: %w", err)
	}

	// Compare
	return CompareBitstreams(libopusOutput, ourOutput), nil
}
