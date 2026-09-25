package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal"
)

func TestSILKOnlyTrailingRedundancyFinalRange(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector08.bit")
	if len(packets) < 5 {
		t.Fatalf("vector has %d packets, need 5", len(packets))
	}

	dec, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]int16, MaxFrameSize*ChannelsStereo)
	if _, err := dec.Decode(packets[4].packet, pcm); err != nil {
		t.Fatalf("Decode packet 4: %v", err)
	}
	if got := dec.FinalRange(); got != packets[4].finalRange {
		t.Fatalf("FinalRange=%08x, want SILK/CELT XOR %08x", got, packets[4].finalRange)
	}
	if !dec.prevRedundancy {
		t.Fatal("SILK-to-CELT redundancy did not update loss-mode state")
	}
}

func TestCELTEndBandIncludesSILKMediumband(t *testing.T) {
	if got := celtEndBandForFramingBW(internal.BandwidthMediumband); got != 17 {
		t.Fatalf("mediumband CELT redundancy end band=%d, want 17", got)
	}
}
