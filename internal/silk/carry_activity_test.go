package silk

import "testing"

// TestCarryPacketStateKeepsCallArguments checks that the Opus VAD decision
// and the DTX setting survive the prefill re-initialisation: in libopus
// they are silk_Encode arguments, not channel state.
func TestCarryPacketStateKeepsCallArguments(t *testing.T) {
	for _, channels := range []int{1, 2} {
		src, err := NewEncoder(16000, channels)
		if err != nil {
			t.Fatal(err)
		}
		src.SetOpusActivity(VADNoActivity)
		src.SetDTX(true)
		dst, err := NewEncoder(16000, channels)
		if err != nil {
			t.Fatal(err)
		}
		dst.CarryPacketState(src)
		if !dst.opusNoActivity || !dst.useDTX {
			t.Fatalf("channels %d: opusNoActivity %v useDTX %v after CarryPacketState", channels, dst.opusNoActivity, dst.useDTX)
		}
		if dst.side != nil && (!dst.side.opusNoActivity || !dst.side.useDTX) {
			t.Fatalf("channels %d: side channel lost the call arguments", channels)
		}
		src.SetOpusActivity(VADActivity)
		dst.CarryPacketState(src)
		if dst.opusNoActivity {
			t.Fatalf("channels %d: activity not cleared", channels)
		}
	}
}
