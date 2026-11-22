package oggopus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestPageDeterministicRoundTripAndFlags(t *testing.T) {
	page := Page{
		Version:         StreamVersion,
		HeaderType:      HeaderContinued | HeaderBOS | HeaderEOS,
		GranulePosition: 123456789,
		Serial:          0x10203040,
		Sequence:        17,
		Segments:        []byte{255, 3, 0},
		Data:            append(bytes.Repeat([]byte{0xa5}, 255), 1, 2, 3),
	}
	encoded, err := page.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded[:4]); got != CapturePattern {
		t.Fatalf("capture = %q", got)
	}
	if encoded[4] != StreamVersion {
		t.Fatalf("version = %d", encoded[4])
	}
	parsed, n, err := ParsePage(append(encoded, 99))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(encoded) {
		t.Fatalf("consumed %d, want %d", n, len(encoded))
	}
	if !parsed.Continued() || !parsed.BOS() || !parsed.EOS() {
		t.Fatalf("flags = 0x%02x", parsed.HeaderType)
	}
	reencoded, err := parsed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Fatal("page round trip was not deterministic")
	}
}

func TestPageRejectsCRCVersionFlagsAndTruncation(t *testing.T) {
	page := Page{
		Version:         StreamVersion,
		GranulePosition: -1,
		Serial:          7,
		Segments:        []byte{3},
		Data:            []byte("abc"),
	}
	encoded, err := page.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	badCapture := append([]byte(nil), encoded...)
	badCapture[0] = 'X'
	if _, _, err := ParsePage(badCapture); !errors.Is(err, ErrInvalidCapture) {
		t.Fatalf("capture error = %v, want ErrInvalidCapture", err)
	}

	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)-1] ^= 1
	if _, _, err := ParsePage(corrupt); !errors.Is(err, ErrChecksum) {
		t.Fatalf("CRC error = %v", err)
	}

	badVersion := append([]byte(nil), encoded...)
	badVersion[4] = 1
	binary.LittleEndian.PutUint32(badVersion[22:26], 0)
	binary.LittleEndian.PutUint32(badVersion[22:26], checksum(badVersion))
	if _, _, err := ParsePage(badVersion); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("version error = %v", err)
	}

	badFlags := append([]byte(nil), encoded...)
	badFlags[5] = 0x80
	binary.LittleEndian.PutUint32(badFlags[22:26], 0)
	binary.LittleEndian.PutUint32(badFlags[22:26], checksum(badFlags))
	if _, _, err := ParsePage(badFlags); !errors.Is(err, ErrInvalidHeaderType) {
		t.Fatalf("flags error = %v", err)
	}

	for _, cut := range []int{0, 26, 27, len(encoded) - 1} {
		if _, _, err := ParsePage(encoded[:cut]); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("cut %d error = %v", cut, err)
		}
	}
}

func TestPageLacingValidation(t *testing.T) {
	page := Page{
		Version:  StreamVersion,
		Segments: []byte{255, 0},
		Data:     bytes.Repeat([]byte{1}, 254),
	}
	if _, err := page.MarshalBinary(); !errors.Is(err, ErrInvalidPage) {
		t.Fatalf("error = %v", err)
	}
}

// bitwiseOggCRC is the reference bit-at-a-time Ogg CRC-32.
func bitwiseOggCRC(data []byte) uint32 {
	var crc uint32
	for _, v := range data {
		crc ^= uint32(v) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func TestCRCTableMatchesBitwise(t *testing.T) {
	data := make([]byte, 4096)
	seed := uint32(1)
	for i := range data {
		seed = seed*1664525 + 1013904223
		data[i] = byte(seed >> 24)
	}
	for _, n := range []int{0, 1, 21, 22, 25, 26, 27, 300, len(data)} {
		if got, want := checksum(data[:n]), bitwiseOggCRC(data[:n]); got != want {
			t.Fatalf("checksum(%d bytes) = %08x, want %08x", n, got, want)
		}
		zeroed := append([]byte(nil), data[:n]...)
		for i := 22; i < 26 && i < n; i++ {
			zeroed[i] = 0
		}
		if got, want := checksumWithZeroedField(data[:n]), bitwiseOggCRC(zeroed); got != want {
			t.Fatalf("checksumWithZeroedField(%d bytes) = %08x, want %08x", n, got, want)
		}
	}
}
