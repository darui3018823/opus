package oggopus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestOpusHeadRoundTripMetadata(t *testing.T) {
	head := Head{
		Version:         1,
		Channels:        6,
		PreSkip:         312,
		InputSampleRate: 44100,
		OutputGain:      -384,
		MappingFamily:   1,
		StreamCount:     4,
		CoupledCount:    2,
		ChannelMapping:  []byte{0, 4, 1, 2, 3, 5},
	}
	packet, err := head.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseHead(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreSkip != 312 || got.OutputGain != -384 || got.InputSampleRate != 44100 {
		t.Fatalf("metadata = %+v", got)
	}
	if !bytes.Equal(got.ChannelMapping, head.ChannelMapping) {
		t.Fatalf("mapping = %v", got.ChannelMapping)
	}
	reencoded, err := got.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, packet) {
		t.Fatal("OpusHead round trip was not deterministic")
	}
}

func TestOpusHeadValidation(t *testing.T) {
	valid := Head{Version: 1, Channels: 2, MappingFamily: 0}
	packet, err := valid.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]byte{
		packet[:18],
		append([]byte("NotHead!"), packet[8:]...),
		append(append([]byte(nil), packet...), 0),
	}
	zeroChannels := append([]byte(nil), packet...)
	zeroChannels[9] = 0
	cases = append(cases, zeroChannels)
	for i, data := range cases {
		if _, err := ParseHead(data); !errors.Is(err, ErrInvalidOpusHead) {
			t.Fatalf("case %d error = %v", i, err)
		}
	}

	badMap := Head{
		Version:        1,
		Channels:       3,
		MappingFamily:  1,
		StreamCount:    2,
		CoupledCount:   1,
		ChannelMapping: []byte{0, 1, 4},
	}
	if _, err := badMap.MarshalBinary(); !errors.Is(err, ErrInvalidOpusHead) {
		t.Fatalf("mapping error = %v", err)
	}
}

func TestOpusTagsRoundTripAndValidation(t *testing.T) {
	tags := Tags{
		Vendor:   "go-opus",
		Comments: []string{"TITLE=テスト", "R128_TRACK_GAIN=-123"},
		Extra:    []byte{1, 2, 3},
	}
	packet, err := tags.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseTags(packet)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := got.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, packet) {
		t.Fatal("OpusTags round trip was not deterministic")
	}

	badVendorLength := append([]byte(nil), packet...)
	binary.LittleEndian.PutUint32(badVendorLength[8:12], ^uint32(0))
	if _, err := ParseTags(badVendorLength); !errors.Is(err, ErrInvalidOpusTags) {
		t.Fatalf("vendor length error = %v", err)
	}
	badCount := append([]byte(nil), packet...)
	offset := 12 + len(tags.Vendor)
	binary.LittleEndian.PutUint32(badCount[offset:offset+4], ^uint32(0))
	if _, err := ParseTags(badCount); !errors.Is(err, ErrInvalidOpusTags) {
		t.Fatalf("comment count error = %v", err)
	}
	invalidUTF8 := Tags{Vendor: string([]byte{0xff})}
	if _, err := invalidUTF8.MarshalBinary(); !errors.Is(err, ErrInvalidOpusTags) {
		t.Fatalf("UTF-8 error = %v", err)
	}
}
