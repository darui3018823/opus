//go:build opusref

package opus

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestDecoderShortFrameOracle decodes streams in which real SILK, hybrid and
// CELT packets are interleaved with frames of at most one byte under other
// TOC modes (DTX packets, libopus's "PLC frames"), and compares the int16 PCM
// with libopus's plain-C decoder (the encoder oracle's --dec-bit mode). Such
// frames are concealed in the previous mode, which becomes prev_mode, and the
// next data frame switches modes from there.
func TestDecoderShortFrameOracle(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	type stream struct {
		rate, channels, bitrate int
		app                     Application
	}
	packetsOf := func(s stream, n int) [][]byte {
		enc, err := NewEncoder(48000, s.channels, s.app)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetBitrate(s.bitrate); err != nil {
			t.Fatal(err)
		}
		var out [][]byte
		for p := 0; p < n; p++ {
			pcm := encOracleRefSpeechFrame(48000, p*960, 960)
			if s.channels == 2 {
				pcm = silkRefSpeechFrameStereo(48000, p*960, 960)
			}
			pkt, err := enc.EncodeFloat(pcm, 960)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, pkt)
		}
		return out
	}
	silkPkts := packetsOf(stream{48000, 1, 12000, ApplicationVOIP}, 6)
	hybridPkts := packetsOf(stream{48000, 1, 32000, ApplicationVOIP}, 6)
	celtPkts := packetsOf(stream{48000, 1, 96000, ApplicationAudio}, 6)
	stereoCELT := packetsOf(stream{48000, 2, 128000, ApplicationAudio}, 6)

	empty := [][]byte{{0x7c}, {0x7f, 0x02}, {0xff, 0x02}, {0xf8}, {0x0b << 3}, {0xfc, 0x00}, {0x7b, 0x02}}
	var seq [][]byte
	add := func(p ...[]byte) { seq = append(seq, p...) }
	// Start-of-stream empty frames (no history: silence, prev_mode unset).
	add(empty[3], empty[0])
	add(silkPkts[:3]...)
	for _, e := range empty {
		add(e)
	}
	add(silkPkts[3:]...)
	add(empty[1], hybridPkts[0], hybridPkts[1], empty[3], hybridPkts[2], empty[4])
	add(hybridPkts[3:]...)
	add(empty[2], celtPkts[0], celtPkts[1], empty[0], celtPkts[2], empty[4], silkPkts[0])
	add(celtPkts[3:]...)
	add(empty[5], stereoCELT[0], stereoCELT[1], empty[6], stereoCELT[2])

	for _, o := range []struct{ rate, channels int }{{48000, 2}, {48000, 1}, {16000, 1}} {
		if msg := decodeSeqAgainstOracle(t, seq, o.rate, o.channels); msg != "" {
			t.Fatal(msg)
		}
	}
}

// decodeSeqAgainstOracle decodes seq with the Go decoder and libopus's
// plain-C decoder and returns a description of the first differing sample.
func decodeSeqAgainstOracle(t *testing.T, seq [][]byte, rate, channels int) string {
	t.Helper()
	dir := t.TempDir()
	bit := filepath.Join(dir, "seq.bit")
	var data []byte
	for _, p := range seq {
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[:4], uint32(len(p)))
		data = append(data, hdr[:]...)
		data = append(data, p...)
	}
	if err := os.WriteFile(bit, data, 0o644); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(dir, "ref.raw")
	if out, err := exec.Command(encOraclePath(), "--dec-bit", bit, strconv.Itoa(rate), strconv.Itoa(channels), raw).CombinedOutput(); err != nil {
		t.Fatalf("oracle: %v\n%s", err, out)
	}
	refBytes, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 5760*channels)
	off := 0
	for i, p := range seq {
		n, err := dec.Decode(p, pcm)
		if err != nil {
			return fmt.Sprintf("%d Hz %dch packet %d (% x): %v", rate, channels, i, p[:min(len(p), 2)], err)
		}
		for k := 0; k < n*channels; k++ {
			if off+2*k+1 >= len(refBytes) {
				return fmt.Sprintf("%d Hz %dch packet %d: libopus output too short", rate, channels, i)
			}
			ref := int16(binary.LittleEndian.Uint16(refBytes[off+2*k:]))
			if pcm[k] != ref {
				return fmt.Sprintf("%d Hz %dch packet %d (% x): sample %d Go %d libopus %d",
					rate, channels, i, p[:min(len(p), 2)], k/channels, pcm[k], ref)
			}
		}
		off += 2 * n * channels
	}
	if off != len(refBytes) {
		return fmt.Sprintf("%d Hz %dch: Go %d bytes of PCM, libopus %d", rate, channels, off, len(refBytes))
	}
	return ""
}
