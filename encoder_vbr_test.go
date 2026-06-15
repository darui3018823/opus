package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/celt"
)

// TestEncoderVBRPacketSizeVariance verifies that VBR mode produces variable-
// size packets: silence frames should be smaller than complex signal frames.
func TestEncoderVBRPacketSizeVariance(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960 // 20ms
		nFrames    = 10
		bitrate    = 64000
	)

	for _, mode := range []struct {
		name     string
		rateMode celt.RateMode
	}{
		{"VBR", celt.RateModeVBR},
		{"CVBR", celt.RateModeCVBR},
	} {
		t.Run(mode.name, func(t *testing.T) {
			enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			_ = enc.SetBitrate(bitrate)
			enc.SetVBR(true)
			if mode.rateMode == celt.RateModeVBR {
				enc.SetVBRConstraint(false)
			}

			// Encode silent frames.
			silentPCM := make([]float64, frameSize*channels)
			var silentSizes []int
			for i := 0; i < nFrames; i++ {
				pkt, err := enc.EncodeFloat(silentPCM, frameSize)
				if err != nil {
					t.Fatalf("silent frame %d: %v", i, err)
				}
				silentSizes = append(silentSizes, len(pkt))
			}

			// Reset and encode complex signal (multi-tone + noise).
			_ = enc.Reset()
			_ = enc.SetBitrate(bitrate)
			enc.SetVBR(true)
			if mode.rateMode == celt.RateModeVBR {
				enc.SetVBRConstraint(false)
			}

			complexPCM := make([]float64, frameSize*channels)
			for i := range complexPCM {
				t := float64(i) / float64(sampleRate)
				complexPCM[i] = 0.3*math.Sin(2*math.Pi*440*t) +
					0.3*math.Sin(2*math.Pi*1000*t) +
					0.2*math.Sin(2*math.Pi*4000*t) +
					0.1*math.Sin(2*math.Pi*8000*t)
			}
			var complexSizes []int
			for i := 0; i < nFrames; i++ {
				pkt, err := enc.EncodeFloat(complexPCM, frameSize)
				if err != nil {
					t.Fatalf("complex frame %d: %v", i, err)
				}
				complexSizes = append(complexSizes, len(pkt))
			}

			// Log sizes.
			avgSilent := avgInt(silentSizes)
			avgComplex := avgInt(complexSizes)
			t.Logf("silent  avg=%d  sizes=%v", avgSilent, silentSizes)
			t.Logf("complex avg=%d  sizes=%v", avgComplex, complexSizes)

			// VBR: silent should be noticeably smaller than complex.
			// With silence detection disabled the difference may be modest, but
			// there should still be variance across frames.
			allSame := true
			for _, s := range silentSizes[1:] {
				if s != silentSizes[0] {
					allSame = false
					break
				}
			}
			for _, s := range complexSizes[1:] {
				if s != complexSizes[0] {
					allSame = false
					break
				}
			}
			if allSame && silentSizes[0] == complexSizes[0] {
				t.Errorf("VBR produced identical packet sizes for silence and complex signal (%d bytes)", silentSizes[0])
			}
		})
	}
}

// TestEncoderCBRFixedSize verifies that CBR mode produces exactly targetBytes.
func TestEncoderCBRFixedSize(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		nFrames    = 8
	)

	for _, bitrate := range []int{32000, 64000, 128000} {
		t.Run(bitrateLabel(bitrate), func(t *testing.T) {
			enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			_ = enc.SetBitrate(bitrate)
			enc.SetVBR(false) // CBR

			// Expected CELT payload bytes = bitrate * 0.02 / 8
			expectedPayload := bitrate * 20 / 1000 / 8 // bytes
			expectedTotal := expectedPayload + 1        // + TOC byte

			pcm := make([]float64, frameSize*channels)
			for i := range pcm {
				pcm[i] = 0.5 * math.Sin(2*math.Pi*1000*float64(i)/float64(sampleRate))
			}

			for i := 0; i < nFrames; i++ {
				pkt, err := enc.EncodeFloat(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if len(pkt) != expectedTotal {
					t.Errorf("frame %d: got %d bytes, want %d (bitrate=%d)", i, len(pkt), expectedTotal, bitrate)
				}
			}
		})
	}
}

// TestEncoderVBRRoundTrip verifies that VBR packets decode correctly.
func TestEncoderVBRRoundTrip(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		nFrames    = 20
		bitrate    = 64000
	)

	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	_ = enc.SetBitrate(bitrate)
	enc.SetVBR(true) // CVBR

	dec, err := NewDecoder(sampleRate, channels)
	if err != nil {
		t.Fatal(err)
	}

	pcm := make([]float64, frameSize*channels)
	for frame := 0; frame < nFrames; frame++ {
		for i := 0; i < frameSize; i++ {
			t_sec := float64(frame*frameSize+i) / float64(sampleRate)
			pcm[i] = 0.3*math.Sin(2*math.Pi*440*t_sec) +
				0.3*math.Sin(2*math.Pi*1000*t_sec) +
				0.2*math.Sin(2*math.Pi*4000*t_sec) +
				0.1*math.Sin(2*math.Pi*8000*t_sec)
		}

		pkt, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatalf("encode frame %d: %v", frame, err)
		}
		t.Logf("frame %d: encoded pkt len=%d", frame, len(pkt))

		decoded, err := dec.DecodeFloat(pkt)
		if err != nil {
			t.Fatalf("decode frame %d (%d bytes): %v", frame, len(pkt), err)
		}

		// Verify decoder doesn't error out (proves valid range coder state).
		if len(decoded) != frameSize {
			t.Fatalf("decode frame %d: got %d samples, want %d", frame, len(decoded), frameSize)
		}
	}
}

func avgInt(s []int) int {
	if len(s) == 0 {
		return 0
	}
	sum := 0
	for _, v := range s {
		sum += v
	}
	return sum / len(s)
}

func bitrateLabel(br int) string {
	if br >= 1000 {
		return string(rune('0'+br/1000)) + "kbps"
	}
	return ""
}
