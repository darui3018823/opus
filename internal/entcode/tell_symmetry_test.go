package entcode

import "testing"

// TestEncDecTellSymmetry verifies that the encoder and decoder report identical
// ECTell()/TellFrac() values at every symbol boundary for the same symbol
// sequence. The shared CELT quant/allocation code relies on this so that
// budget-dependent decisions match in both directions.
func TestEncDecTellSymmetry(t *testing.T) {
	enc := NewEncoder(64)
	var encTellF, encTell []int
	encTellF = append(encTellF, enc.TellFrac())
	encTell = append(encTell, enc.ECTell())

	// A mix of the symbol kinds the CELT path uses.
	enc.EncodeBitLogp(true, 3)
	encTellF = append(encTellF, enc.TellFrac())
	encTell = append(encTell, enc.ECTell())

	enc.EncodeUint(7, 13)
	encTellF = append(encTellF, enc.TellFrac())
	encTell = append(encTell, enc.ECTell())

	icdf := []uint8{25, 23, 2, 0}
	enc.EncodeIcdf(2, icdf, 5)
	encTellF = append(encTellF, enc.TellFrac())
	encTell = append(encTell, enc.ECTell())

	enc.Encode(3, 7, 20)
	encTellF = append(encTellF, enc.TellFrac())
	encTell = append(encTell, enc.ECTell())

	enc.EncodeBits(0x2A, 6) // raw bits at end
	encTellF = append(encTellF, enc.TellFrac())
	encTell = append(encTell, enc.ECTell())

	enc.Flush()
	data := enc.Bytes()

	dec := NewDecoder(data)
	var decTellF, decTell []int
	decTellF = append(decTellF, dec.TellFrac())
	decTell = append(decTell, dec.ECTell())

	if !dec.DecodeBitLogp(3) {
		t.Fatalf("bit logp mismatch")
	}
	decTellF = append(decTellF, dec.TellFrac())
	decTell = append(decTell, dec.ECTell())

	if v := dec.DecodeUint(13); v != 7 {
		t.Fatalf("uint mismatch: %d", v)
	}
	decTellF = append(decTellF, dec.TellFrac())
	decTell = append(decTell, dec.ECTell())

	if s := dec.DecodeIcdf(icdf, 5); s != 2 {
		t.Fatalf("icdf mismatch: %d", s)
	}
	decTellF = append(decTellF, dec.TellFrac())
	decTell = append(decTell, dec.ECTell())

	fm := dec.Decode(20)
	if fm < 3 || fm >= 7 {
		t.Fatalf("decode range mismatch: %d", fm)
	}
	dec.DecodeUpdate(3, 7, 20)
	decTellF = append(decTellF, dec.TellFrac())
	decTell = append(decTell, dec.ECTell())

	if v := dec.DecodeBits(6); v != 0x2A {
		t.Fatalf("raw bits mismatch: %x", v)
	}
	decTellF = append(decTellF, dec.TellFrac())
	decTell = append(decTell, dec.ECTell())

	for i := range encTellF {
		if encTellF[i] != decTellF[i] {
			t.Errorf("step %d: TellFrac enc=%d dec=%d", i, encTellF[i], decTellF[i])
		}
		if encTell[i] != decTell[i] {
			t.Errorf("step %d: ECTell enc=%d dec=%d", i, encTell[i], decTell[i])
		}
	}
}
