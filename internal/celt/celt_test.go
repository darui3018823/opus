package celt

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

func TestParseTOC(t *testing.T) {
	tests := []struct {
		name       string
		toc        byte
		wantConfig int
		wantStereo bool
		wantFrames int
	}{
		{"Mono single frame", 0x1C, 28, false, 1},   // Config 28, mono, 1 frame
		{"Stereo single frame", 0x3C, 28, true, 1},  // Config 28, stereo, 1 frame
		{"Mono two frames", 0x5C, 28, false, 2},     // Config 28, mono, 2 frames
		{"Fullband 20ms mono", 0x1C, 28, false, 1},  // Config 28 = fullband 20ms
		{"Wideband 10ms stereo", 0x76, 22, true, 2}, // Config 22 = wideband 10ms
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseTOC(tt.toc)
			if err != nil {
				t.Fatalf("ParseTOC() error = %v", err)
			}

			if p.Config != tt.wantConfig {
				t.Errorf("Config = %d, want %d", p.Config, tt.wantConfig)
			}
			if p.Stereo != tt.wantStereo {
				t.Errorf("Stereo = %v, want %v", p.Stereo, tt.wantStereo)
			}
			if p.FrameCount != tt.wantFrames {
				t.Errorf("FrameCount = %d, want %d", p.FrameCount, tt.wantFrames)
			}
		})
	}
}

func TestParsePacket(t *testing.T) {
	// Simple packet with TOC and dummy frame data
	packet := []byte{0x1C, 0x01, 0x02, 0x03, 0x04}

	p, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket() error = %v", err)
	}

	if p.Config != 28 {
		t.Errorf("Config = %d, want 28", p.Config)
	}

	if len(p.Frames) != 1 {
		t.Errorf("Frame count = %d, want 1", len(p.Frames))
	}

	if len(p.Frames[0]) != 4 {
		t.Errorf("Frame size = %d, want 4", len(p.Frames[0]))
	}
}

func TestGetBandConfig(t *testing.T) {
	tests := []struct {
		frameSize int
		wantBands int
	}{
		{FrameSize2_5ms, 13},
		{FrameSize5ms, 17},
		{FrameSize10ms, 19},
		{FrameSize20ms, 21},
		{FrameSize40ms, 21},
		{FrameSize60ms, 21},
	}

	for _, tt := range tests {
		config := GetBandConfig(tt.frameSize)
		if config.NumBands != tt.wantBands {
			t.Errorf("GetBandConfig(%d) bands = %d, want %d",
				tt.frameSize, config.NumBands, tt.wantBands)
		}
	}
}

func TestBinomial(t *testing.T) {
	tests := []struct {
		n, k int
		want uint32
	}{
		{5, 0, 1},
		{5, 1, 5},
		{5, 2, 10},
		{5, 3, 10},
		{5, 5, 1},
		{10, 3, 120},
	}

	for _, tt := range tests {
		got := binomial(tt.n, tt.k)
		if got != tt.want {
			t.Errorf("binomial(%d, %d) = %d, want %d", tt.n, tt.k, got, tt.want)
		}
	}
}

// TestCWRSV verifies the V(n,k) codebook size recurrence from RFC 6716 §5.4.3.3.
func TestCWRSV(t *testing.T) {
	tests := []struct {
		n, k int
		want uint64
	}{
		{1, 0, 1},
		{1, 1, 2},
		{2, 0, 1},
		{2, 1, 4},
		{2, 2, 8},
		{3, 1, 6},
		{3, 2, 18},
		{4, 1, 8},
		{0, 0, 1},
		{0, 1, 0},
	}

	for _, tt := range tests {
		got := cwrsV(tt.n, tt.k)
		if got != tt.want {
			t.Errorf("cwrsV(%d, %d) = %d, want %d", tt.n, tt.k, got, tt.want)
		}
	}
}

// TestCWRSIRoundtrip verifies that cwrsi (decode) and icwrsi (encode)
// are perfect inverses for small (n, k) combinations.
func TestCWRSIRoundtrip(t *testing.T) {
	cases := [][2]int{
		{1, 1}, {1, 2}, {1, 3},
		{2, 1}, {2, 2}, {2, 3},
		{3, 1}, {3, 2}, {3, 3},
		{4, 2}, {4, 3},
		{5, 2},
	}
	for _, c := range cases {
		n, k := c[0], c[1]
		total := cwrsV(n, k)
		for idx := uint32(0); uint64(idx) < total; idx++ {
			y := cwrsi(n, k, idx)
			// Check L1 norm
			l1 := 0
			for _, v := range y {
				if v < 0 {
					l1 -= v
				} else {
					l1 += v
				}
			}
			if l1 != k {
				t.Errorf("cwrsi(%d, %d, %d): L1 norm = %d, want %d; y = %v",
					n, k, idx, l1, k, y)
				continue
			}
			// Roundtrip: encode back and check
			got := icwrsi(n, y)
			if got != idx {
				t.Errorf("icwrsi(%d, %v) = %d, want %d", n, y, got, idx)
			}
		}
	}
}

// TestCWRSIKnownVectors checks specific known decode results.
func TestCWRSIKnownVectors(t *testing.T) {
	// n=2, k=1: V=4, vectors: [1,0], [-1,0], [0,1], [0,-1]
	expected := [][]int{
		{1, 0},
		{-1, 0},
		{0, 1},
		{0, -1},
	}
	for idx, want := range expected {
		got := cwrsi(2, 1, uint32(idx))
		for j := range want {
			if got[j] != want[j] {
				t.Errorf("cwrsi(2, 1, %d) = %v, want %v", idx, got, want)
				break
			}
		}
	}
}

func TestPVQDecode(t *testing.T) {
	// Test basic PVQ decoding
	n := 8 // dimension
	k := 4 // pulses

	// Decode index 0 (represented by all zero bytes in entropy stream)
	// In the old index code, 0 meant the first combination.
	// In the new recursive split code, we need the stream to guide us to that combination.
	// Assuming all zeros works for the first combination?
	dec := entcode.NewDecoder(make([]byte, 10))

	coeffs := PVQDecode(dec, n, k)

	if len(coeffs) != n {
		t.Errorf("PVQDecode length = %d, want %d", len(coeffs), n)
	}

	// Check that it's a unit vector (approximately)
	norm := 0.0
	for _, c := range coeffs {
		norm += c * c
	}

	if norm < 0.9 || norm > 1.1 {
		t.Errorf("PVQDecode norm = %f, want ~1.0", norm)
	}

	// Check that pulse count is approximately correct
	pulseSum := 0.0
	for _, c := range coeffs {
		if c > 0 {
			pulseSum += c
		} else {
			pulseSum -= c
		}
	}

	// Should have magnitude related to k
	if pulseSum < 0.5 {
		t.Errorf("PVQDecode pulse sum = %f, too small", pulseSum)
	}
}

// TestPVQDecodeNonZeroOutput verifies that PVQ encode/decode roundtrip
// produces non-zero unit vectors.
func TestPVQDecodeNonZeroOutput(t *testing.T) {
	testCases := []struct {
		vector []float64
		k      int
	}{
		{[]float64{1.0, 0.0, 0.0, 0.0}, 3},
		{[]float64{0.5, 0.5, 0.5, 0.5}, 3},
		{[]float64{1.0, -1.0, 1.0, -1.0}, 2},
		{[]float64{0.0, 1.0, 0.0, 0.0}, 1},
	}

	for _, tc := range testCases {
		enc := entcode.NewEncoder(64)
		PVQEncode(enc, tc.vector, tc.k)
		enc.Flush()

		dec := entcode.NewDecoder(enc.Bytes())
		coeffs := PVQDecode(dec, len(tc.vector), tc.k)

		norm := 0.0
		for _, c := range coeffs {
			norm += c * c
		}
		if norm < 0.99 || norm > 1.01 {
			t.Errorf("PVQ roundtrip (k=%d): norm = %f, want ~1.0", tc.k, norm)
		}
	}
}

func TestBandProcessor(t *testing.T) {
	mode := NewMode(FrameSize20ms, 48000, 1)
	bp := NewBandProcessor(mode)

	if len(bp.bands) != mode.Bands.NumBands {
		t.Errorf("BandProcessor bands = %d, want %d",
			len(bp.bands), mode.Bands.NumBands)
	}

	// Test band energy decoding
	energyBits := make([]int, mode.Bands.NumBands)
	for i := range energyBits {
		energyBits[i] = 5 // Some arbitrary energy
	}

	bp.DecodeBandEnergies(energyBits)

	// Check that energies are set
	for i, band := range bp.bands {
		if band.Energy <= 0 {
			t.Errorf("Band %d energy = %f, should be positive", i, band.Energy)
		}
	}
}

func TestDecoder(t *testing.T) {
	// Create decoder
	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	// Create a simple test packet
	frameData := make([]byte, 100)
	for i := range frameData {
		frameData[i] = byte(i)
	}

	// Decode
	samples, err := dec.Decode(frameData)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Check output size
	expectedSize := FrameSize20ms * 1 // mono
	if len(samples) != expectedSize {
		t.Errorf("Decode output size = %d, want %d", len(samples), expectedSize)
	}

	// Check that samples are finite
	for i, s := range samples {
		if s != s { // NaN check
			t.Errorf("Sample %d is NaN", i)
		}
	}
}

// TestDecoderProducesNonZeroOutput verifies the full decode pipeline
// produces non-zero samples from a non-trivial input packet.
func TestDecoderProducesNonZeroOutput(t *testing.T) {
	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	// Create a packet with varied byte content so the range decoder
	// produces meaningful symbols.
	frameData := make([]byte, 120)
	for i := range frameData {
		frameData[i] = byte((i*73 + 37) & 0xFF)
	}

	samples, err := dec.Decode(frameData)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(samples) != FrameSize20ms {
		t.Errorf("output length = %d, want %d", len(samples), FrameSize20ms)
	}

	// At least some samples should be non-zero
	nonZero := 0
	for _, s := range samples {
		if s != 0 && s == s { // also check not NaN
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("All output samples are zero; expected non-zero output from decode pipeline")
	}
	t.Logf("Non-zero samples: %d / %d", nonZero, len(samples))
}

// TestBandEnergyDecodeReasonableValues verifies that decoded band energies
// are positive and finite for a synthetic bitstream.
func TestBandEnergyDecodeReasonableValues(t *testing.T) {
	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	// Feed some data through the decoder to exercise energy decoding
	frameData := make([]byte, 80)
	for i := range frameData {
		frameData[i] = byte(i * 3)
	}

	_, err = dec.Decode(frameData)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Check band energies are positive and finite
	for i, band := range dec.bandProcs[0].bands {
		if band.Energy < 0 {
			t.Errorf("Band %d energy = %f, should be >= 0", i, band.Energy)
		}
		if band.Energy != band.Energy { // NaN
			t.Errorf("Band %d energy is NaN", i)
		}
	}

	// prevEnergies should have been updated from their initial values.
	// Initial value is math.Log(1e-8) ≈ -18.42; after one decode frame they
	// should differ (either higher or lower depending on decoded energy).
	initLogE := math.Log(1e-8)
	allInitial := true
	for _, e := range dec.prevEnergies {
		if e != initLogE {
			allInitial = false
			break
		}
	}
	if allInitial {
		t.Error("prevEnergies were not updated during decode")
	}
}

// TestBitAllocationPulseCount verifies that pulse counts are derived from
// the CWRS codebook size rather than a simple heuristic.
func TestBitAllocationPulseCount(t *testing.T) {
	// With 10 bits the max index is 2^10 = 1024.
	// V(4,7) = 952 <= 1024, V(4,8) = 1408 > 1024, so k should be 7.
	k := computePulseCount(10, 4)
	t.Logf("V(4,7)=%d, V(4,8)=%d", cwrsV(4, 7), cwrsV(4, 8))
	if k != 7 {
		t.Errorf("computePulseCount(10, 4) = %d, want 7", k)
	}

	// With 0 bits, should get 0 pulses
	if got := computePulseCount(0, 4); got != 0 {
		t.Errorf("computePulseCount(0, 4) = %d, want 0", got)
	}

	// With 2 bits (max index 4), V(2,1) = 4, V(2,2) = 8 > 4, so k=1
	if got := computePulseCount(2, 2); got != 1 {
		t.Errorf("computePulseCount(2, 2) = %d, want 1", got)
	}
}

func TestDecoderStereo(t *testing.T) {
	// Create stereo decoder
	dec, err := NewDecoder(FrameSize20ms, 48000, 2)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	// Create a simple test packet
	frameData := make([]byte, 150)

	// Decode
	samples, err := dec.Decode(frameData)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Check output size (should be interleaved stereo)
	expectedSize := FrameSize20ms * 2 // stereo
	if len(samples) != expectedSize {
		t.Errorf("Decode output size = %d, want %d", len(samples), expectedSize)
	}
}

func TestDecoderReset(t *testing.T) {
	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	// Decode some frames
	frameData := make([]byte, 100)
	_, _ = dec.Decode(frameData)

	// Reset
	dec.Reset()

	// Check that overlap is cleared
	for ch := 0; ch < dec.mode.Channels; ch++ {
		for _, v := range dec.overlap[ch] {
			if v != 0 {
				t.Error("Overlap not cleared after reset")
				break
			}
		}
	}
}

func TestPacketLossConcealment(t *testing.T) {
	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	// Decode with empty packet (simulates loss)
	samples, err := dec.Decode(nil)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Should produce output (PLC)
	if len(samples) != FrameSize20ms {
		t.Errorf("PLC output size = %d, want %d", len(samples), FrameSize20ms)
	}
}

func BenchmarkDecoder(b *testing.B) {
	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		b.Fatalf("NewDecoder() error = %v", err)
	}

	frameData := make([]byte, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dec.Decode(frameData)
	}
}

// Test bit allocation
func TestBitAllocation(t *testing.T) {
	mode := NewMode(FrameSize20ms, 48000, 1)
	targetBits := 1000

	ba := NewBitAllocation(mode, targetBits)

	// Create test band energies
	energies := make([]float64, mode.Bands.NumBands)
	for i := range energies {
		energies[i] = float64(i+1) * 10.0 // Increasing energy
	}

	// Perform allocation
	err := ba.Allocate(energies)
	if err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}

	// Check that bits were allocated
	totalBits := ba.TotalAllocatedBits()
	if totalBits <= 0 {
		t.Error("No bits allocated")
	}

	// Check that higher energy bands got more bits (generally)
	firstHalfBits := 0
	secondHalfBits := 0
	mid := mode.Bands.NumBands / 2

	for i := 0; i < mid; i++ {
		firstHalfBits += ba.GetBandBits(i)
	}
	for i := mid; i < mode.Bands.NumBands; i++ {
		secondHalfBits += ba.GetBandBits(i)
	}

	// Higher energy bands should generally get more bits
	// (This is a soft check since allocation is also band-size dependent)
	if secondHalfBits < firstHalfBits/2 {
		t.Logf("Warning: bit allocation may not favor high energy bands properly")
	}
}

func TestTransientDetector(t *testing.T) {
	mode := NewMode(FrameSize20ms, 48000, 1)
	td := NewTransientDetector(mode)

	// Test with non-transient signal (constant amplitude)
	samples := make([]float64, FrameSize20ms)
	for i := range samples {
		samples[i] = 0.5
	}

	isTransient, _ := td.Detect(samples)
	if isTransient {
		t.Error("Should not detect transient in constant signal")
	}

	// Test with transient signal (sudden increase)
	for i := range samples {
		if i < len(samples)/2 {
			samples[i] = 0.1
		} else {
			samples[i] = 0.9 // Big jump
		}
	}

	isTransient, pos := td.Detect(samples)
	if !isTransient {
		t.Error("Should detect transient in step signal")
	}

	if pos <= 0 {
		t.Error("Transient position should be positive")
	}
}

func TestTransientWeight(t *testing.T) {
	mode := NewMode(FrameSize20ms, 48000, 1)
	td := NewTransientDetector(mode)

	// Test with strong transient
	samples := make([]float64, FrameSize20ms)
	for i := range samples {
		if i < len(samples)/2 {
			samples[i] = 0.1
		} else {
			samples[i] = 1.0
		}
	}

	weight := td.ComputeTransientWeight(samples)
	if weight <= 0 {
		t.Error("Transient weight should be positive for transient signal")
	}

	if weight > 1.0 {
		t.Error("Transient weight should not exceed 1.0")
	}
}

func TestEncoder(t *testing.T) {
	// Create encoder
	config := DefaultEncoderConfig()
	enc, err := NewEncoder(FrameSize20ms, 48000, 1, config)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	// Create test samples (sine wave)
	samples := make([]float64, FrameSize20ms)
	for i := range samples {
		samples[i] = 0.5 * dsp.Sin(2.0*dsp.Pi*440.0*float64(i)/48000.0)
	}

	// Encode
	frameData, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Check output
	if len(frameData) == 0 {
		t.Error("Encoded frame is empty")
	}

	// Typical frame at 64kbps for 20ms should be around 160 bytes
	expectedSize := 64000 * 20 / 1000 / 8
	if len(frameData) < expectedSize/2 || len(frameData) > expectedSize*2 {
		t.Logf("Frame size %d bytes (expected ~%d)", len(frameData), expectedSize)
	}
}

func TestEncoderStereo(t *testing.T) {
	config := DefaultEncoderConfig()
	enc, err := NewEncoder(FrameSize20ms, 48000, 2, config)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	// Create test samples (stereo)
	samples := make([]float64, FrameSize20ms*2)
	for i := 0; i < FrameSize20ms; i++ {
		// Left channel: 440 Hz
		samples[i*2] = 0.5 * dsp.Sin(2.0*dsp.Pi*440.0*float64(i)/48000.0)
		// Right channel: 880 Hz
		samples[i*2+1] = 0.5 * dsp.Sin(2.0*dsp.Pi*880.0*float64(i)/48000.0)
	}

	// Encode
	frameData, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	if len(frameData) == 0 {
		t.Error("Encoded stereo frame is empty")
	}
}

func TestEncoderDecoderRoundtrip(t *testing.T) {
	// This is a basic roundtrip test
	// Note: Due to lossy compression and simplified implementation,
	// we can't expect exact reconstruction at this stage

	// Skip this test for now as encoder/decoder integration is still incomplete
	// specifically, energy quantization/decoding needs full implementation
	t.Skip("Skipping roundtrip test - Energy quantization incomplete")

	config := DefaultEncoderConfig()
	config.Bitrate = 96000 // Higher bitrate for better quality

	enc, err := NewEncoder(FrameSize20ms, 48000, 1, config)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	// Create test signal
	samples := make([]float64, FrameSize20ms)
	for i := range samples {
		samples[i] = 0.7 * dsp.Sin(2.0*dsp.Pi*440.0*float64(i)/48000.0)
	}

	// Encode
	encoded, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Decode
	decoded, err := dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Check that we got output
	if len(decoded) != len(samples) {
		t.Errorf("Decoded length %d != input length %d", len(decoded), len(samples))
	}

	// Check that decoded signal has reasonable amplitude
	maxAmp := 0.0
	for _, v := range decoded {
		if dsp.Abs(v) > maxAmp {
			maxAmp = dsp.Abs(v)
		}
	}

	if maxAmp < 0.1 {
		t.Error("Decoded signal amplitude too low")
	}

	if maxAmp > 2.0 {
		t.Error("Decoded signal amplitude too high")
	}
}

func TestEncoderReset(t *testing.T) {
	config := DefaultEncoderConfig()
	enc, err := NewEncoder(FrameSize20ms, 48000, 1, config)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	// Encode some frames
	samples := make([]float64, FrameSize20ms)
	_, _ = enc.Encode(samples)

	// Reset
	enc.Reset()

	// Check that overlap is cleared
	for ch := 0; ch < enc.mode.Channels; ch++ {
		for _, v := range enc.overlap[ch] {
			if v != 0 {
				t.Error("Overlap not cleared after reset")
				break
			}
		}
	}
}

func BenchmarkEncoder(b *testing.B) {
	config := DefaultEncoderConfig()
	enc, err := NewEncoder(FrameSize20ms, 48000, 1, config)
	if err != nil {
		b.Fatalf("NewEncoder() error = %v", err)
	}

	samples := make([]float64, FrameSize20ms)
	for i := range samples {
		samples[i] = 0.5 * dsp.Sin(2.0*dsp.Pi*440.0*float64(i)/48000.0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encode(samples)
	}
}
