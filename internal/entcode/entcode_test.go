package entcode

import (
	"testing"
)

func TestICDFRoundtrip(t *testing.T) {
	// 4 equal-probability symbols, ftb=8 (ft=256)
	// descending: {192, 128, 64, 0}
	icdf := []uint8{192, 128, 64, 0}
	ftb := 8

	symbols := []int{0, 1, 2, 3, 1, 0, 2, 3, 0}

	enc := NewEncoder(200)
	for i, s := range symbols {
		t.Logf("Enc[%d]=%d: val=0x%08X rng=0x%08X rem=%d ext=%d buflen=%d",
			i, s, enc.GetVal(), enc.GetRng(), enc.GetRem(), enc.GetExt(), len(enc.Bytes()))
		enc.EncodeIcdf(s, icdf, ftb)
	}
	enc.Flush()
	data := enc.Bytes()

	t.Logf("Encoded %d symbols to %d bytes: %v", len(symbols), len(data), data)

	dec := NewDecoder(data)
	for i, want := range symbols {
		t.Logf("Dec[%d]: dif=0x%08X rng=0x%08X rem=%d pos=%d",
			i, dec.GetDif(), dec.GetRng(), dec.GetRem(), dec.GetPos())
		got := dec.DecodeIcdf(icdf, ftb)
		if got != want {
			t.Errorf("symbol %d: got %d, want %d", i, got, want)
		}
	}
	if dec.Error() != nil {
		t.Errorf("decoder error: %v", dec.Error())
	}
}

func TestICDFBinary(t *testing.T) {
	// Binary: prob=3/8 for symbol 0, 5/8 for symbol 1; ftb=3 (ft=8)
	// icdf[0] = ft - CDF(1) = 8-3 = 5, icdf[1] = ft - CDF(2) = 8-8 = 0
	icdf := []uint8{5, 0}
	ftb := 3
	symbols := []int{0, 1, 0, 0, 1, 1, 0, 1}

	enc := NewEncoder(200)
	for _, s := range symbols {
		enc.EncodeIcdf(s, icdf, ftb)
	}
	enc.Flush()
	data := enc.Bytes()

	t.Logf("Encoded %d binary symbols to %d bytes: %v", len(symbols), len(data), data)

	dec := NewDecoder(data)
	for i, want := range symbols {
		got := dec.DecodeIcdf(icdf, ftb)
		if got != want {
			t.Errorf("symbol %d: got %d, want %d", i, got, want)
		}
	}
}

func TestICDFManySymbols(t *testing.T) {
	// Test with more symbols to exercise normalization and carry propagation
	icdf := []uint8{192, 128, 64, 0}
	ftb := 8

	// Generate a longer sequence
	symbols := make([]int, 100)
	for i := range symbols {
		symbols[i] = i % 4
	}

	enc := NewEncoder(200)
	for _, s := range symbols {
		enc.EncodeIcdf(s, icdf, ftb)
	}
	enc.Flush()
	data := enc.Bytes()

	t.Logf("Encoded %d symbols to %d bytes", len(symbols), len(data))

	dec := NewDecoder(data)
	for i, want := range symbols {
		got := dec.DecodeIcdf(icdf, ftb)
		if got != want {
			t.Errorf("symbol %d: got %d, want %d", i, got, want)
		}
	}
}

func TestBitLogpRoundtrip(t *testing.T) {
	enc := NewEncoder(100)
	bits := []bool{true, false, true, true, false}
	for _, b := range bits {
		enc.EncodeBitLogp(b, 1) // logp=1 means 50/50
	}
	enc.Flush()
	data := enc.Bytes()

	t.Logf("Encoded %d bits to %d bytes: %v", len(bits), len(data), data)

	dec := NewDecoder(data)
	for i, want := range bits {
		got := dec.DecodeBitLogp(1)
		if got != want {
			t.Errorf("bit %d: got %v, want %v", i, got, want)
		}
	}
}

func TestBitLogpSkewed(t *testing.T) {
	// Test with logp=3 (probability 1/8 of being true)
	enc := NewEncoder(100)
	bits := []bool{false, false, true, false, false, false, true, false}
	for _, b := range bits {
		enc.EncodeBitLogp(b, 3)
	}
	enc.Flush()
	data := enc.Bytes()

	dec := NewDecoder(data)
	for i, want := range bits {
		got := dec.DecodeBitLogp(3)
		if got != want {
			t.Errorf("bit %d: got %v, want %v", i, got, want)
		}
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	// Test ec_encode / ec_decode with various ft values
	enc := NewEncoder(200)
	enc.Encode(2, 3, 5)   // symbol 2 out of 5
	enc.Encode(0, 1, 10)  // symbol 0 out of 10
	enc.Encode(9, 10, 10) // symbol 9 out of 10
	enc.Encode(3, 4, 8)   // symbol 3 out of 8
	enc.Flush()
	data := enc.Bytes()

	t.Logf("Encoded 4 symbols to %d bytes: %v", len(data), data)

	dec := NewDecoder(data)

	s := dec.Decode(5)
	if s != 2 {
		t.Errorf("symbol 0: got %d, want 2", s)
	}
	dec.DecodeUpdate(2, 3, 5)

	s = dec.Decode(10)
	if s != 0 {
		t.Errorf("symbol 1: got %d, want 0", s)
	}
	dec.DecodeUpdate(0, 1, 10)

	s = dec.Decode(10)
	if s != 9 {
		t.Errorf("symbol 2: got %d, want 9", s)
	}
	dec.DecodeUpdate(9, 10, 10)

	s = dec.Decode(8)
	if s != 3 {
		t.Errorf("symbol 3: got %d, want 3", s)
	}
	dec.DecodeUpdate(3, 4, 8)
}

func TestEncodeUintRoundtrip(t *testing.T) {
	cases := []struct {
		val uint32
		ft  uint32
	}{
		{5, 8},
		{0, 16},
		{15, 16},
		{255, 256},
		{0, 256},
		{127, 256},
	}
	enc := NewEncoder(200)
	for _, c := range cases {
		enc.EncodeUint(c.val, c.ft)
	}
	enc.Flush()
	data := enc.Bytes()

	dec := NewDecoder(data)
	for i, c := range cases {
		got := dec.DecodeUint(c.ft)
		if got != c.val {
			t.Errorf("case %d: got %d, want %d (ft=%d)", i, got, c.val, c.ft)
		}
	}
	if dec.Error() != nil {
		t.Errorf("decoder error: %v", dec.Error())
	}
}

func TestSingleSymbolICDF(t *testing.T) {
	// Test encoding a single symbol with various icdf tables
	tests := []struct {
		name   string
		symbol int
		icdf   []uint8
		ftb    int
	}{
		{"sym0-of-2", 0, []uint8{128, 0}, 8},
		{"sym1-of-2", 1, []uint8{128, 0}, 8},
		{"sym0-of-4", 0, []uint8{192, 128, 64, 0}, 8},
		{"sym3-of-4", 3, []uint8{192, 128, 64, 0}, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(100)
			enc.EncodeIcdf(tt.symbol, tt.icdf, tt.ftb)
			enc.Flush()
			data := enc.Bytes()

			dec := NewDecoder(data)
			got := dec.DecodeIcdf(tt.icdf, tt.ftb)
			if got != tt.symbol {
				t.Errorf("got %d, want %d (data=%v)", got, tt.symbol, data)
			}
		})
	}
}

func TestTell(t *testing.T) {
	enc := NewEncoder(100)
	initial := enc.Tell()
	// Initial tell: 0 bytes, rng=CodeTop=0x80000000, ILog(CodeTop)=32
	// So tell = 0*8 + (32-32) = 0
	if initial != 0 {
		t.Errorf("Tell() at start: got %d, want 0", initial)
	}
	enc.EncodeBitLogp(true, 1)
	if enc.Tell() < 1 {
		t.Errorf("Tell() after 1 bit: got %d, want >= 1", enc.Tell())
	}
}

func TestEmptyBuffer(t *testing.T) {
	dec := NewDecoder([]byte{})
	_ = dec.DecodeBitLogp(1)
	_ = dec.DecodeBits(4)
	// Should not panic
}

func TestLog2Ceiling(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{1, 0},
		{2, 1},
		{3, 2},
		{4, 2},
		{5, 3},
		{8, 3},
		{9, 4},
		{16, 4},
		{17, 5},
	}
	for _, tt := range tests {
		result := Log2Ceiling(tt.input)
		if result != tt.expected {
			t.Errorf("Log2Ceiling(%d) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

func TestILog(t *testing.T) {
	tests := []struct {
		input    uint32
		expected int
	}{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 2},
		{4, 3},
		{7, 3},
		{8, 4},
		{15, 4},
		{16, 5},
	}
	for _, tt := range tests {
		result := ILog(tt.input)
		if result != tt.expected {
			t.Errorf("ILog(%d) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

func TestICDFNonUniform(t *testing.T) {
	// Test with a non-uniform distribution
	// 3 symbols with probabilities 4/8, 3/8, 1/8
	// CDF = [0, 4, 7, 8]
	// icdf = [ft-CDF(1), ft-CDF(2), ft-CDF(3)] = [4, 1, 0]
	icdf := []uint8{4, 1, 0}
	ftb := 3

	symbols := []int{0, 0, 1, 2, 0, 1, 0, 2, 1, 0, 0, 1, 2, 0}

	enc := NewEncoder(200)
	for _, s := range symbols {
		enc.EncodeIcdf(s, icdf, ftb)
	}
	enc.Flush()
	data := enc.Bytes()

	dec := NewDecoder(data)
	for i, want := range symbols {
		got := dec.DecodeIcdf(icdf, ftb)
		if got != want {
			t.Errorf("symbol %d: got %d, want %d", i, got, want)
		}
	}
}

func TestMixedICDFAndBits(t *testing.T) {
	// Test interleaving ICDF coding with bit coding
	icdf := []uint8{192, 128, 64, 0}
	ftb := 8

	enc := NewEncoder(200)
	enc.EncodeIcdf(2, icdf, ftb)
	enc.EncodeBitLogp(true, 1)
	enc.EncodeIcdf(0, icdf, ftb)
	enc.EncodeBitLogp(false, 1)
	enc.EncodeIcdf(3, icdf, ftb)
	enc.Flush()
	data := enc.Bytes()

	dec := NewDecoder(data)
	if got := dec.DecodeIcdf(icdf, ftb); got != 2 {
		t.Errorf("icdf 0: got %d, want 2", got)
	}
	if got := dec.DecodeBitLogp(1); got != true {
		t.Errorf("bit 0: got %v, want true", got)
	}
	if got := dec.DecodeIcdf(icdf, ftb); got != 0 {
		t.Errorf("icdf 1: got %d, want 0", got)
	}
	if got := dec.DecodeBitLogp(1); got != false {
		t.Errorf("bit 1: got %v, want false", got)
	}
	if got := dec.DecodeIcdf(icdf, ftb); got != 3 {
		t.Errorf("icdf 2: got %d, want 3", got)
	}
}

func TestCarryPropagation(t *testing.T) {
	// Encode many high-value symbols to trigger carry propagation
	// Symbol 3 is at the top of the range, should trigger carries
	icdf := []uint8{192, 128, 64, 0}
	ftb := 8

	symbols := make([]int, 50)
	for i := range symbols {
		symbols[i] = 3 // all high symbols
	}

	enc := NewEncoder(200)
	for _, s := range symbols {
		enc.EncodeIcdf(s, icdf, ftb)
	}
	enc.Flush()
	data := enc.Bytes()

	dec := NewDecoder(data)
	for i, want := range symbols {
		got := dec.DecodeIcdf(icdf, ftb)
		if got != want {
			t.Errorf("symbol %d: got %d, want %d", i, got, want)
		}
	}
}

func TestEncoderConstants(t *testing.T) {
	// Verify constants match libopus
	if SymBits != 8 {
		t.Errorf("SymBits: got %d, want 8", SymBits)
	}
	if CodeBits != 32 {
		t.Errorf("CodeBits: got %d, want 32", CodeBits)
	}
	if SymMax != 255 {
		t.Errorf("SymMax: got %d, want 255", SymMax)
	}
	if CodeShift != 23 {
		t.Errorf("CodeShift: got %d, want 23", CodeShift)
	}
	if CodeTop != 0x80000000 {
		t.Errorf("CodeTop: got 0x%08X, want 0x80000000", CodeTop)
	}
	if CodeBot != 0x00800000 {
		t.Errorf("CodeBot: got 0x%08X, want 0x00800000", CodeBot)
	}
	if CodeExtra != 7 {
		t.Errorf("CodeExtra: got %d, want 7", CodeExtra)
	}
}

func BenchmarkEncodeIcdf(b *testing.B) {
	icdf := []uint8{192, 128, 64, 0}
	enc := NewEncoder(100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.EncodeIcdf(i%4, icdf, 8)
	}
}

func BenchmarkDecodeIcdf(b *testing.B) {
	icdf := []uint8{192, 128, 64, 0}
	enc := NewEncoder(100000)
	for i := 0; i < 10000; i++ {
		enc.EncodeIcdf(i%4, icdf, 8)
	}
	enc.Flush()
	data := enc.Bytes()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec := NewDecoder(data)
		for j := 0; j < 1000; j++ {
			dec.DecodeIcdf(icdf, 8)
		}
	}
}
