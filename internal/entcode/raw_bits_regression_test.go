package entcode

import "testing"

func TestEncodeBitsRawOnlyRoundtrip(t *testing.T) {
	enc := NewEncoder(8)
	enc.EncodeBits(0x15, 5)
	enc.Flush()
	data := enc.Bytes()
	if len(data) == 0 {
		t.Fatal("raw-only EncodeBits flushed to an empty packet")
	}
	dec := NewDecoder(data)
	if got := dec.DecodeBits(5); got != 0x15 {
		t.Fatalf("DecodeBits = 0x%x, want 0x15 (packet %v)", got, data)
	}
}
