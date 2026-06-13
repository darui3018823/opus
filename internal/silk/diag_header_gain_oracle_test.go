package silk

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readOpusDemoPacket(t *testing.T, vector string, packetIndex int) []byte {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", "opus_newvectors", vector)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test vector not found: %v", err)
	}

	off := 0
	for i := 0; off+8 <= len(data); i++ {
		size := int(binary.BigEndian.Uint32(data[off:]))
		if off+8+size > len(data) {
			t.Fatalf("truncated packet %d: size=%d remaining=%d", i, size, len(data)-off-8)
		}
		pkt := data[off+8 : off+8+size]
		if i == packetIndex {
			return pkt
		}
		off += 8 + size
	}
	t.Fatalf("packet %d not found in %s", packetIndex, vector)
	return nil
}

func silkOracleFrameCount(config, countCode int, payload []byte) (int, []byte) {
	silkFramesPerOpusFrame := 1
	switch config & 3 {
	case 2:
		silkFramesPerOpusFrame = 2
	case 3:
		silkFramesPerOpusFrame = 3
	}

	opusFrames := 1
	if countCode == 1 || countCode == 2 {
		opusFrames = 2
	} else if countCode == 3 && len(payload) > 0 {
		opusFrames = int(payload[0] & 0x3f)
		payload = payload[1:]
		if opusFrames == 0 {
			opusFrames = 1
		}
	}
	return opusFrames * silkFramesPerOpusFrame, payload
}

func TestSILKHeaderAndGainOracle(t *testing.T) {
	tests := []struct {
		name       string
		vector     string
		packet     int
		wantConfig int
		wantFrames int
		wantVAD    []uint32
		wantRaw    [][]int
		wantGains  [][]int32
	}{
		{
			name:       "tv02-pkt0-inactive-60ms",
			vector:     "testvector02.bit",
			packet:     0,
			wantConfig: 3,
			wantFrames: 3,
			wantVAD:    []uint32{0, 0, 0},
			wantRaw: [][]int{
				{10, 9, 0, 0},
				{3, 4, 4, 4},
				{4, 4, 4, 4},
			},
			wantGains: [][]int32{
				{397312, 872448, 464896, 246784},
				{210944, 210944, 210944, 210944},
				{210944, 210944, 210944, 210944},
			},
		},
		{
			name:       "tv02-pkt2-voiced-60ms",
			vector:     "testvector02.bit",
			packet:     2,
			wantConfig: 3,
			wantFrames: 3,
			wantVAD:    []uint32{1, 1, 1},
			wantRaw: [][]int{
				{26, 3, 11, 9},
			},
			wantGains: [][]int32{
				{4915200, 4194304, 12713984, 28049408},
			},
		},
		{
			name:       "tv12-pkt0-inactive-20ms",
			vector:     "testvector12.bit",
			packet:     0,
			wantConfig: 1,
			wantFrames: 1,
			wantVAD:    []uint32{0},
			wantRaw: [][]int{
				{6, 7, 2, 4},
			},
			wantGains: [][]int32{
				{210944, 335872, 246784, 246784},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := readOpusDemoPacket(t, tt.vector, tt.packet)
			if len(pkt) < 2 {
				t.Fatalf("packet too short: %d", len(pkt))
			}

			toc := pkt[0]
			config := int((toc >> 3) & 0x1f)
			countCode := int(toc & 3)
			if config != tt.wantConfig {
				t.Fatalf("config=%d want=%d", config, tt.wantConfig)
			}

			nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])
			if nFrames != tt.wantFrames {
				t.Fatalf("SILK frame count=%d want=%d", nFrames, tt.wantFrames)
			}

			dec, err := NewDecoderWithFrameMs(8000, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			tr := &decodeTrace{}
			dec.trace = tr
			_, _ = dec.DecodeMulti(stream, nFrames)

			if len(tr.VADFlags) != 1 {
				t.Fatalf("got %d VAD flag groups, want 1", len(tr.VADFlags))
			}
			if !reflect.DeepEqual(tr.VADFlags[0], tt.wantVAD) {
				t.Fatalf("VAD flags=%v want=%v", tr.VADFlags[0], tt.wantVAD)
			}
			if len(tr.Frames) < len(tt.wantRaw) {
				t.Fatalf("traced frames=%d want at least %d", len(tr.Frames), len(tt.wantRaw))
			}
			for i := range tt.wantRaw {
				frame := tr.Frames[i]
				t.Logf("frame %d: vad=%d sig=%d qoff=%d raw=%v abs=%v gains_Q16=%v",
					i, frame.VADFlag, frame.SignalType, frame.QuantOffset, frame.RawGainIndices, frame.AbsGainIndices, frame.GainsQ16)
				if !reflect.DeepEqual(frame.RawGainIndices, tt.wantRaw[i]) {
					t.Fatalf("frame %d raw gain indices=%v want=%v", i, frame.RawGainIndices, tt.wantRaw[i])
				}
				if !reflect.DeepEqual(frame.GainsQ16, tt.wantGains[i]) {
					t.Fatalf("frame %d gains_Q16=%v want=%v", i, frame.GainsQ16, tt.wantGains[i])
				}
			}
			t.Logf("%s pkt%d: config=%d countCode=%d silkFrames=%d", tt.vector, tt.packet, config, countCode, nFrames)
		})
	}
}

func TestSILKCode3FrameCountHeader(t *testing.T) {
	pkt := readOpusDemoPacket(t, "testvector01.bit", 0)
	if len(pkt) < 2 {
		t.Fatalf("packet too short: %d", len(pkt))
	}
	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	countCode := int(toc & 3)
	if countCode != 3 {
		t.Fatalf("countCode=%d want=3", countCode)
	}
	frameCount := int(pkt[1] & 0x3f)
	if frameCount != 3 {
		t.Fatalf("Opus code-3 frame count=%d want=3", frameCount)
	}
	t.Logf("code-3 header: config=%d frameCount=%d", config, frameCount)
}

func TestSILKNLSFAndLPCOracle(t *testing.T) {
	tests := []struct {
		name       string
		vector     string
		packet     int
		wantConfig int
		wantFrames []struct {
			indices []int
			nlsfQ15 []int16
			pred0   []int16
			pred1   []int16
		}
	}{
		{
			name:       "tv02-pkt0-60ms",
			vector:     "testvector02.bit",
			packet:     0,
			wantConfig: 3,
			wantFrames: []struct {
				indices []int
				nlsfQ15 []int16
				pred0   []int16
				pred1   []int16
			}{
				{
					indices: []int{1, -1, -1, -1, 0, 0, 0, 0, 0, 0, 0},
					nlsfQ15: []int16{321, 2471, 5904, 9856, 12928, 16000, 19328, 22400, 25728, 28800},
					pred0:   []int16{5041, -1246, 1017, -614, 431, -560, 131, -357, 284, -46},
					pred1:   []int16{5041, -1246, 1017, -614, 431, -560, 131, -357, 284, -46},
				},
				{
					indices: []int{2, 1, -3, -1, 0, 0, 0, 0, 0, 0, 0},
					nlsfQ15: []int16{634, 1000, 7286, 11392, 14592, 17536, 20736, 23552, 26752, 29440},
					pred0:   []int16{1856, 1608, 1448, 540, 174, -469, -368, -496, -34, -289},
					pred1:   []int16{1856, 1608, 1448, 540, 174, -469, -368, -496, -34, -289},
				},
				{
					indices: []int{5, 0, -2, 1, 0, 0, -1, 1, 0, 0, 0},
					nlsfQ15: []int16{633, 1144, 7947, 10055, 13266, 16136, 21156, 23040, 26240, 29184},
					pred0:   []int16{3337, 576, 263, -494, 1292, 334, -560, -166, 560, -1174},
					pred1:   []int16{3337, 576, 263, -494, 1292, 334, -560, -166, 560, -1174},
				},
			},
		},
		{
			name:       "tv12-pkt0-20ms",
			vector:     "testvector12.bit",
			packet:     0,
			wantConfig: 1,
			wantFrames: []struct {
				indices []int
				nlsfQ15 []int16
				pred0   []int16
				pred1   []int16
			}{
				{
					indices: []int{0, 0, -1, 0, 0, 0, -1, 0, 0, 0, 0},
					nlsfQ15: []int16{841, 3241, 7454, 10221, 13122, 15707, 20096, 23040, 26368, 29184},
					pred0:   []int16{3811, -131, -149, 96, 953, -301, -188, -77, 487, -585},
					pred1:   []int16{3811, -131, -149, 96, 953, -301, -188, -77, 487, -585},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := readOpusDemoPacket(t, tt.vector, tt.packet)
			toc := pkt[0]
			config := int((toc >> 3) & 0x1f)
			countCode := int(toc & 3)
			if config != tt.wantConfig {
				t.Fatalf("config=%d want=%d", config, tt.wantConfig)
			}

			nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])
			dec, err := NewDecoderWithFrameMs(8000, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			tr := &decodeTrace{}
			dec.trace = tr
			_, _ = dec.DecodeMulti(stream, nFrames)
			if len(tr.Frames) < len(tt.wantFrames) {
				t.Fatalf("traced frames=%d want at least %d", len(tr.Frames), len(tt.wantFrames))
			}
			for i, want := range tt.wantFrames {
				got := tr.Frames[i]
				if !reflect.DeepEqual(got.NLSFIndices, want.indices) {
					t.Fatalf("frame %d NLSF indices=%v want=%v", i, got.NLSFIndices, want.indices)
				}
				if !reflect.DeepEqual(got.NLSFQ15, want.nlsfQ15) {
					t.Fatalf("frame %d NLSF_Q15=%v want=%v", i, got.NLSFQ15, want.nlsfQ15)
				}
				if !reflect.DeepEqual(got.PredCoef0Q12, want.pred0) {
					t.Fatalf("frame %d PredCoef0_Q12=%v want=%v", i, got.PredCoef0Q12, want.pred0)
				}
				if !reflect.DeepEqual(got.PredCoef1Q12, want.pred1) {
					t.Fatalf("frame %d PredCoef1_Q12=%v want=%v", i, got.PredCoef1Q12, want.pred1)
				}
				t.Logf("frame %d: nlsf=%v pred0=%v", i, got.NLSFQ15, got.PredCoef0Q12)
			}
		})
	}
}
