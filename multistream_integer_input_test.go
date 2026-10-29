package opus

import "testing"

// opus_multistream_encode / opus_projection_encode hand every stream
// downmix_int (no +6 dBFS analysis cap), the float entry points
// downmix_float. The streams' flag must follow the multistream call, not
// an earlier direct Encode on a stream encoder.
func TestMultistreamIntegerInputFlag(t *testing.T) {
	const frameSize = 960
	for _, policy := range []ModePolicy{ModePolicyLegacy, ModePolicyLibopus} {
		enc, err := NewMultistreamEncoder(48000, 2, 1, 1, []byte{0, 1}, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetModePolicy(policy); err != nil {
			t.Fatal(err)
		}
		stream, err := enc.StreamEncoder(0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Encode(make([]int16, 2*frameSize), frameSize); err != nil {
			t.Fatal(err)
		}
		calls := []struct {
			name    string
			encode  func() error
			integer bool
		}{
			{"EncodeFloat", func() error { _, err := enc.EncodeFloat(make([]float64, 2*frameSize), frameSize); return err }, false},
			{"Encode", func() error { _, err := enc.Encode(make([]int16, 2*frameSize), frameSize); return err }, true},
			{"EncodeFloat32", func() error { _, err := enc.EncodeFloat32(make([]float32, 2*frameSize), frameSize); return err }, false},
			{"Encode24", func() error { _, err := enc.Encode24(make([]int32, 2*frameSize), frameSize); return err }, true},
		}
		for _, c := range calls {
			if err := c.encode(); err != nil {
				t.Fatalf("policy %v %s: %v", policy, c.name, err)
			}
			if stream.inputIntegerAPI != c.integer {
				t.Fatalf("policy %v %s: inputIntegerAPI = %v, want %v", policy, c.name, stream.inputIntegerAPI, c.integer)
			}
		}
	}

	proj, err := NewProjectionEncoder(48000, 4, 3, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		encode  func() error
		integer bool
	}{
		{"Encode", func() error { _, err := proj.Encode(make([]int16, 4*frameSize), frameSize); return err }, true},
		{"EncodeFloat", func() error { _, err := proj.EncodeFloat(make([]float64, 4*frameSize), frameSize); return err }, false},
	} {
		if err := c.encode(); err != nil {
			t.Fatalf("projection %s: %v", c.name, err)
		}
		for i, s := range proj.multistream.encoders {
			if s.inputIntegerAPI != c.integer {
				t.Fatalf("projection %s stream %d: inputIntegerAPI = %v, want %v", c.name, i, s.inputIntegerAPI, c.integer)
			}
		}
	}
}
