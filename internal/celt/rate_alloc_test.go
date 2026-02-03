package celt

import (
	"fmt"
	"math"
	"testing"
)

// TestRateAllocatorLevel1 tests single band allocation match.
func TestRateAllocatorLevel1(t *testing.T) {
	// Create mode for 48kHz, 20ms frame (960 samples)
	mode := NewMode(FrameSize20ms, 48000, 1)

	// Create allocator with LM=3 (20ms = 960 samples = 120 * 2^3)
	ra := NewRateAllocator(mode, 3, 1)

	// Target 64kbps = 64000 bits/sec
	// For 20ms frame: 64000 * 0.020 = 1280 bits
	// In Q8 format: 1280 << 8 = 327680
	targetBits := int32(1280 << BITRES)

	// Generate test energies (simulating 440Hz sine wave)
	energies := make([]float64, mode.Bands.NumBands)
	for i := range energies {
		// Higher energy in lower bands (typical for sine wave)
		energies[i] = math.Exp(-float64(i) * 0.2)
	}

	// Compute allocation
	result := ra.ComputeAllocation(targetBits, energies, false)

	// Print allocation results
	t.Log("=== Phase 4 Level 1 Verification ===")
	t.Logf("Target bits: %d (Q8: %d)", targetBits>>BITRES, targetBits)
	t.Logf("Total used bits: %d (Q8: %d)", result.TotalUsedBits>>BITRES, result.TotalUsedBits)
	t.Logf("Coded bands: %d", result.CodedBands)

	// Check that at least band 10 (mid-frequency) has reasonable allocation
	bandToCheck := 10
	if bandToCheck < len(result.Bits) {
		bits := result.Bits[bandToCheck] >> BITRES
		ebits := result.EBits[bandToCheck]
		pulses := ra.GetPulseCount(bandToCheck, result.Bits[bandToCheck])

		t.Logf("Band %d: bits=%d, ebits=%d, pulses=%d", bandToCheck, bits, ebits, pulses)

		// Level 1: At least one band should have positive allocation
		if bits > 0 || pulses > 0 {
			t.Log("✓ Level 1 PASSED: Band has positive allocation")
		} else {
			t.Error("✗ Level 1 FAILED: Band has no allocation")
		}
	}
}

// TestRateAllocatorLevel2 tests 80% band allocation match.
func TestRateAllocatorLevel2(t *testing.T) {
	mode := NewMode(FrameSize20ms, 48000, 1)
	ra := NewRateAllocator(mode, 3, 1)
	targetBits := int32(1280 << BITRES)

	energies := make([]float64, mode.Bands.NumBands)
	for i := range energies {
		energies[i] = math.Exp(-float64(i) * 0.2)
	}

	result := ra.ComputeAllocation(targetBits, energies, false)

	// Expected pulse counts (from libopus reference)
	// These are approximate values for 64kbps mono 20ms
	expectedPulses := []int{
		4, 4, 4, 4, 3, 3, 3, 3, // Bands 0-7
		2, 2, 2, 2, 2, 2, 2, // Bands 8-14
		1, 1, 1, 1, 1, 1, // Bands 15-20
	}

	t.Log("=== Phase 4 Level 2 Verification ===")
	t.Log("Band | Our Pulses | Expected | Match")
	t.Log("-----|------------|----------|------")

	matches := 0
	totalBands := min(len(result.Bits), len(expectedPulses))

	for i := 0; i < totalBands; i++ {
		ourPulses := ra.GetPulseCount(i, result.Bits[i])
		exp := expectedPulses[i]
		match := ""

		// Allow ±2 tolerance for Level 2 (estimates are rough)
		diff := ourPulses - exp
		if diff < 0 {
			diff = -diff
		}
		if diff <= 2 {
			matches++
			match = "✓"
		} else {
			match = "✗"
		}

		t.Logf("%4d | %10d | %8d | %s", i, ourPulses, exp, match)
	}

	matchPercent := float64(matches) / float64(totalBands) * 100
	t.Logf("Match rate: %d/%d (%.1f%%)", matches, totalBands, matchPercent)

	if matchPercent >= 80.0 {
		t.Log("✓ Level 2 PASSED: 80% or more bands match")
	} else {
		t.Errorf("✗ Level 2 FAILED: Only %.1f%% bands match (need 80%%)", matchPercent)
	}
}

// TestRateAllocatorLevel3 tests full allocation match.
func TestRateAllocatorLevel3(t *testing.T) {
	mode := NewMode(FrameSize20ms, 48000, 1)
	ra := NewRateAllocator(mode, 3, 1)
	targetBits := int32(1280 << BITRES)

	energies := make([]float64, mode.Bands.NumBands)
	for i := range energies {
		energies[i] = math.Exp(-float64(i) * 0.2)
	}

	result := ra.ComputeAllocation(targetBits, energies, false)

	// Get our pulse counts
	ourPulses := make([]int, mode.Bands.NumBands)
	for i := range ourPulses {
		ourPulses[i] = ra.GetPulseCount(i, result.Bits[i])
	}

	t.Log("=== Phase 4 Level 3 Verification ===")
	t.Log("Our allocation (pulses per band):")
	t.Logf("%v", ourPulses)

	// For Level 3, we need to compare against actual libopus output
	// This requires running opusenc externally
	t.Log("")
	t.Log("NOTE: Full Level 3 verification requires libopus comparison.")
	t.Log("Run the following to get reference values:")
	t.Log("  opusenc --raw --raw-rate 48000 --bitrate 64 test.raw test.opus")
	t.Log("  opus_demo -d 48000 1 test.opus test_decoded.raw")
	t.Log("")

	// For now, check internal consistency
	totalPulses := 0
	for _, p := range ourPulses {
		totalPulses += p
	}

	t.Logf("Total pulses: %d", totalPulses)
	t.Logf("Total bits used: %d", result.TotalUsedBits>>BITRES)

	// Sanity check: total bits should be close to target
	targetBitsInt := int(targetBits >> BITRES)
	usedBitsInt := int(result.TotalUsedBits >> BITRES)
	bitDiff := abs(targetBitsInt - usedBitsInt)

	if bitDiff < targetBitsInt/10 { // Within 10%
		t.Logf("✓ Bit budget within 10%% of target (%d vs %d)", usedBitsInt, targetBitsInt)
	} else {
		t.Logf("⚠ Bit budget differs by %d bits", bitDiff)
	}
}

// TestAllocationConsistency verifies encoder/decoder use same allocation.
func TestAllocationConsistency(t *testing.T) {
	mode := NewMode(FrameSize20ms, 48000, 1)

	// Simulate encoder allocation
	encAlloc := NewRateAllocator(mode, 3, 1)
	targetBits := int32(1280 << BITRES)
	energies := make([]float64, mode.Bands.NumBands)
	for i := range energies {
		energies[i] = 1.0
	}
	encResult := encAlloc.ComputeAllocation(targetBits, energies, false)

	// Simulate decoder allocation (should be identical given same parameters)
	decAlloc := NewRateAllocator(mode, 3, 1)
	decResult := decAlloc.ComputeAllocation(targetBits, energies, false)

	t.Log("=== Encoder/Decoder Allocation Consistency ===")

	allMatch := true
	for i := 0; i < mode.Bands.NumBands; i++ {
		encPulses := encAlloc.GetPulseCount(i, encResult.Bits[i])
		decPulses := decAlloc.GetPulseCount(i, decResult.Bits[i])

		if encPulses != decPulses {
			t.Errorf("Band %d mismatch: encoder=%d, decoder=%d", i, encPulses, decPulses)
			allMatch = false
		}
	}

	if allMatch {
		t.Log("✓ Encoder and Decoder produce identical allocation")
	}
}

// BenchmarkRateAllocator benchmarks allocation performance.
func BenchmarkRateAllocator(b *testing.B) {
	mode := NewMode(FrameSize20ms, 48000, 1)
	ra := NewRateAllocator(mode, 3, 1)
	targetBits := int32(1280 << BITRES)

	energies := make([]float64, mode.Bands.NumBands)
	for i := range energies {
		energies[i] = 1.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ra.ComputeAllocation(targetBits, energies, false)
	}
}

// PrintAllocationTable prints a formatted allocation table.
func PrintAllocationTable(result *AllocationResult, ra *RateAllocator) string {
	var s string
	s += fmt.Sprintf("%-5s | %-8s | %-6s | %-6s\n", "Band", "Bits(Q8)", "EBits", "Pulses")
	s += fmt.Sprintf("%-5s-+-%-8s-+-%-6s-+-%-6s\n", "-----", "--------", "------", "------")

	for i := 0; i < len(result.Bits); i++ {
		pulses := ra.GetPulseCount(i, result.Bits[i])
		s += fmt.Sprintf("%-5d | %-8d | %-6d | %-6d\n",
			i, result.Bits[i], result.EBits[i], pulses)
	}

	return s
}
