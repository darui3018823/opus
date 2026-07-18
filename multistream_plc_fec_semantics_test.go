package opus

import "testing"

func TestMultistreamDecodeLossDurationAndVariants(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 120 // 7.5 ms
	)
	newDecoder := func() *MultistreamDecoder {
		dec, err := NewMultistreamDecoder(rate, 3, 2, 0, []byte{0, 1, 255})
		if err != nil {
			t.Fatal(err)
		}
		return dec
	}
	f64, err := newDecoder().DecodePLCFloat(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	f32, err := newDecoder().DecodePLCFloat32(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	i16 := make([]int16, frameSize*3)
	i24 := make([]int32, frameSize*3)
	if n, err := newDecoder().DecodePLC(i16, frameSize); err != nil || n != frameSize {
		t.Fatalf("DecodePLC=(%d,%v)", n, err)
	}
	if n, err := newDecoder().DecodePLC24(i24, frameSize); err != nil || n != frameSize {
		t.Fatalf("DecodePLC24=(%d,%v)", n, err)
	}
	for i := range i16 {
		if f64[i] != 0 || f32[i] != 0 || i16[i] != 0 || i24[i] != 0 {
			t.Fatalf("sample %d differs: %g %g %d %d", i, f64[i], f32[i], i16[i], i24[i])
		}
	}
}

func TestMultistreamDecodeFECExplicitDuration(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)
	enc, err := NewMultistreamEncoder(rate, 2, 2, 0, []byte{0, 1}, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	for stream := 0; stream < 2; stream++ {
		child, _ := enc.StreamEncoder(stream)
		if err := child.SetBitrate(18000); err != nil {
			t.Fatal(err)
		}
		child.SetInbandFEC(true)
		child.SetPacketLossPerc(20)
	}
	packets := make([][]byte, 8)
	for p := range packets {
		packets[p], err = enc.EncodeFloat(multistreamPLCFrame(rate, 2, p*frameSize, frameSize), frameSize)
		if err != nil {
			t.Fatal(err)
		}
	}
	prime := func() *MultistreamDecoder {
		dec, err := NewMultistreamDecoder(rate, 2, 2, 0, []byte{0, 1})
		if err != nil {
			t.Fatal(err)
		}
		for p := 0; p < lost; p++ {
			if _, err := dec.DecodeFloat(packets[p]); err != nil {
				t.Fatal(err)
			}
		}
		return dec
	}
	dec := prime()
	pcm := make([]int16, 2*frameSize*2)
	if n, err := dec.DecodeFECWithDuration(packets[lost+1], pcm, 2*frameSize); err != nil || n != 2*frameSize {
		t.Fatalf("DecodeFECWithDuration=(%d,%v), want (%d,nil)", n, err, 2*frameSize)
	}
	if dec.GetLastPacketDurationForTest() != 2*frameSize {
		t.Fatalf("elementary durations do not reflect explicit loss")
	}
	var childXOR uint32
	for stream := 0; stream < 2; stream++ {
		child, _ := dec.StreamDecoder(stream)
		childXOR ^= child.FinalRange()
	}
	if dec.FinalRange() != childXOR {
		t.Fatalf("aggregate range=%08x child XOR=%08x", dec.FinalRange(), childXOR)
	}

	floatDec := prime()
	if out, err := floatDec.DecodeFECFloat(packets[lost+1], frameSize); err != nil || len(out) != frameSize*2 {
		t.Fatalf("DecodeFECFloat length=%d err=%v", len(out), err)
	}
	float32Dec := prime()
	if out, err := float32Dec.DecodeFECFloat32(packets[lost+1], frameSize); err != nil || len(out) != frameSize*2 {
		t.Fatalf("DecodeFECFloat32 length=%d err=%v", len(out), err)
	}
	int24Dec := prime()
	if n, err := int24Dec.DecodeFEC24(packets[lost+1], make([]int32, frameSize*2), frameSize); err != nil || n != frameSize {
		t.Fatalf("DecodeFEC24=(%d,%v)", n, err)
	}
}

func (d *MultistreamDecoder) GetLastPacketDurationForTest() int {
	if len(d.decoders) == 0 {
		return 0
	}
	return d.decoders[0].GetLastPacketDuration()
}
