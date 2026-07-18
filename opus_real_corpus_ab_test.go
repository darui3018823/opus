//go:build opusref

package opus

import (
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestOpusRealCorpusMatchedBitrateScoreboard(t *testing.T) {
	if os.Getenv("OPUS_REAL_CORPUS") != "1" {
		t.Skip("set OPUS_REAL_CORPUS=1 to run the real-corpus matched-bitrate scoreboard")
	}
	// filepath.Glob does not support recursive **, so walk the corpus tree;
	// user-provided WAVs may be nested at any depth.
	var files []string
	err := filepath.WalkDir(filepath.Join("testdata", "real_corpus"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".wav") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no WAV files found under testdata/real_corpus")
	}
	outPath := os.Getenv("OPUS_REAL_CORPUS_OUT")
	if outPath == "" {
		outPath = filepath.Join("testdata", "real_corpus_scoreboard.csv")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatal(err)
	}
	outFile, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	w := csv.NewWriter(outFile)
	defer w.Flush()
	header := []string{
		"status", "error",
		"file", "class", "rate", "channels", "bitrate", "loss_percent", "frames",
		"own_bytes", "libopus_bytes", "matched_bitrate", "matched_bytes",
		"own_snr_db", "libopus_snr_db", "matched_snr_db", "gap_snr_matched_db",
		"ratio_bytes", "ratio_bytes_matched", "own_cfg", "libopus_cfg", "matched_cfg",
	}
	if err := w.Write(header); err != nil {
		t.Fatal(err)
	}

	maxSeconds := envInt("OPUS_REAL_CORPUS_MAX_SECONDS", 6)
	bitrates, err := realCorpusBitrates(os.Getenv("OPUS_REAL_CORPUS_BITRATES"))
	if err != nil {
		t.Fatal(err)
	}
	classes := realCorpusClasses(os.Getenv("OPUS_REAL_CORPUS_CLASSES"))
	losses := []int{0, 5, 10, 20}
	for _, path := range files {
		clip, err := readCorpusWAV(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		frameSize := clip.rate * 20 / 1000
		maxFrames := maxSeconds * 50
		frames := len(clip.pcm) / (frameSize * clip.channels)
		if frames > maxFrames {
			frames = maxFrames
		}
		if frames < 5 {
			t.Logf("skip %s: only %d complete 20 ms frames", path, frames)
			continue
		}
		clip.pcm = clip.pcm[:frames*frameSize*clip.channels]
		kind := corpusClass(path)
		if len(classes) > 0 && !classes[kind] {
			continue
		}
		for _, bitrate := range bitrates {
			ownPackets, ownBytes, ownCfg, err := encodeRealCorpusOwn(clip, kind, bitrate)
			if err != nil {
				writeRealCorpusErrorRow(t, w, path, kind, clip, bitrate, frames, "own_encode_error", err)
				continue
			}
			refPackets, refBytes, refCfg, err := encodeRealCorpusRef(t, clip, kind, bitrate)
			if err != nil {
				writeRealCorpusErrorRow(t, w, path, kind, clip, bitrate, frames, "libopus_encode_error", err)
				continue
			}
			matchedBitrate := realCorpusMatchedBitrateFor(ownBytes, clip.rate, frameSize, frames)
			matchedPackets, matchedBytes, matchedCfg, err := encodeRealCorpusRef(t, clip, kind, matchedBitrate)
			if err != nil {
				writeRealCorpusErrorRow(t, w, path, kind, clip, bitrate, frames, "matched_encode_error", err)
				continue
			}
			for _, loss := range losses {
				ownOut := decodePacketSequenceWithLoss(t, ownPackets, clip.rate, clip.channels, frameSize, loss)
				refOut := decodePacketSequenceWithLoss(t, refPackets, clip.rate, clip.channels, frameSize, loss)
				matchedOut := decodePacketSequenceWithLoss(t, matchedPackets, clip.rate, clip.channels, frameSize, loss)
				ownSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, ownOut, frameSize)
				refSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, refOut, frameSize)
				matchedSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, matchedOut, frameSize)
				ratioBytes := float64(ownBytes) / float64(refBytes)
				ratioMatched := float64(ownBytes) / float64(matchedBytes)
				row := []string{
					"ok", "",
					filepath.ToSlash(path), kind,
					strconv.Itoa(clip.rate), strconv.Itoa(clip.channels), strconv.Itoa(bitrate), strconv.Itoa(loss), strconv.Itoa(frames),
					strconv.Itoa(ownBytes), strconv.Itoa(refBytes), strconv.Itoa(matchedBitrate), strconv.Itoa(matchedBytes),
					fmt.Sprintf("%.3f", ownSNR), fmt.Sprintf("%.3f", refSNR), fmt.Sprintf("%.3f", matchedSNR),
					fmt.Sprintf("%.3f", matchedSNR-ownSNR),
					fmt.Sprintf("%.6f", ratioBytes), fmt.Sprintf("%.6f", ratioMatched),
					strconv.Itoa(ownCfg), strconv.Itoa(refCfg), strconv.Itoa(matchedCfg),
				}
				if err := w.Write(row); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t.Logf("wrote real-corpus scoreboard: %s", outPath)
}

type corpusClip struct {
	rate     int
	channels int
	pcm      []float64
}

func encodeRealCorpusOwn(clip corpusClip, kind string, bitrate int) (packets [][]byte, totalBytes, firstConfig int, err error) {
	app, signal := corpusEncoderMode(kind)
	enc, err := NewEncoder(clip.rate, clip.channels, app)
	if err != nil {
		return nil, 0, 0, err
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, 0, 0, err
	}
	if err := enc.SetComplexity(5); err != nil {
		return nil, 0, 0, err
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetSignalType(signal)
	forcedBandwidth, err := realCorpusForcedBandwidth(os.Getenv("OPUS_REAL_CORPUS_FORCE_BANDWIDTH"))
	if err != nil {
		return nil, 0, 0, err
	}
	if forcedBandwidth != BandwidthAuto {
		if err := enc.SetBandwidth(forcedBandwidth); err != nil {
			return nil, 0, 0, err
		}
	}
	frameSize := clip.rate * 20 / 1000
	return encodeRealCorpusPackets(clip, frameSize, func(pcm []float64) ([]byte, error) {
		return enc.EncodeFloat(pcm, frameSize)
	})
}

func realCorpusBitrates(value string) ([]int, error) {
	if strings.TrimSpace(value) == "" {
		return []int{16000, 24000, 32000, 48000, 64000}, nil
	}
	parts := strings.Split(value, ",")
	bitrates := make([]int, 0, len(parts))
	for _, part := range parts {
		bitrate, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || bitrate < 6000 || bitrate > 510000 {
			return nil, fmt.Errorf("invalid OPUS_REAL_CORPUS_BITRATES value %q", part)
		}
		bitrates = append(bitrates, bitrate)
	}
	return bitrates, nil
}

func realCorpusClasses(value string) map[string]bool {
	classes := make(map[string]bool)
	for _, class := range strings.Split(value, ",") {
		if class = strings.TrimSpace(class); class != "" {
			classes[class] = true
		}
	}
	return classes
}

func realCorpusForcedBandwidth(value string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return BandwidthAuto, nil
	case "narrowband", "nb":
		return BandwidthNarrowband, nil
	case "mediumband", "mb":
		return BandwidthMediumband, nil
	case "wideband", "wb":
		return BandwidthWideband, nil
	case "superwideband", "swb":
		return BandwidthSuperWideband, nil
	case "fullband", "fb":
		return BandwidthFullband, nil
	default:
		return 0, fmt.Errorf("invalid OPUS_REAL_CORPUS_FORCE_BANDWIDTH value %q", value)
	}
}

func encodeRealCorpusRef(t *testing.T, clip corpusClip, kind string, bitrate int) (packets [][]byte, totalBytes, firstConfig int, err error) {
	t.Helper()
	app, signal := corpusEncoderMode(kind)
	enc, err := cgoref.NewEncoder(clip.rate, clip.channels, app)
	if err != nil {
		return nil, 0, 0, err
	}
	defer enc.Close()
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, 0, 0, err
	}
	if err := enc.SetComplexity(5); err != nil {
		return nil, 0, 0, err
	}
	if err := enc.SetVBR(true); err != nil {
		return nil, 0, 0, err
	}
	if err := enc.SetVBRConstraint(true); err != nil {
		return nil, 0, 0, err
	}
	if signal == SignalVoice {
		if err := enc.SetVoiceMode(); err != nil {
			return nil, 0, 0, err
		}
	} else if signal == SignalMusic {
		if err := enc.SetMusicMode(); err != nil {
			return nil, 0, 0, err
		}
	}
	frameSize := clip.rate * 20 / 1000
	return encodeRealCorpusPackets(clip, frameSize, func(pcm []float64) ([]byte, error) {
		return enc.Encode(float64ToFloat32(pcm), frameSize)
	})
}

func encodeRealCorpusPackets(clip corpusClip, frameSize int, encode func([]float64) ([]byte, error)) (packets [][]byte, totalBytes, firstConfig int, err error) {
	stride := frameSize * clip.channels
	frames := len(clip.pcm) / stride
	for frame := 0; frame < frames; frame++ {
		pcm := clip.pcm[frame*stride : (frame+1)*stride]
		packet, err := encode(pcm)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("frame %d: encode: %w", frame, err)
		}
		if len(packet) == 0 {
			return nil, 0, 0, fmt.Errorf("frame %d: empty packet", frame)
		}
		if frame == 0 {
			firstConfig = int(packet[0]>>3) & 0x1f
		}
		packets = append(packets, packet)
		totalBytes += len(packet)
	}
	return packets, totalBytes, firstConfig, nil
}

func writeRealCorpusErrorRow(t *testing.T, w *csv.Writer, path, kind string, clip corpusClip, bitrate, frames int, status string, err error) {
	t.Helper()
	row := []string{
		status, err.Error(),
		filepath.ToSlash(path), kind,
		strconv.Itoa(clip.rate), strconv.Itoa(clip.channels), strconv.Itoa(bitrate), "", strconv.Itoa(frames),
		"", "", "", "",
		"", "", "", "",
		"", "", "", "", "",
	}
	if writeErr := w.Write(row); writeErr != nil {
		t.Fatal(writeErr)
	}
}

func decodePacketSequenceWithLoss(t *testing.T, packets [][]byte, rate, channels, frameSize, loss int) []float64 {
	t.Helper()
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]float64, 0, len(packets)*frameSize*channels)
	tmp := make([]int16, frameSize*channels)
	haveHistory := false
	for i, packet := range packets {
		if deterministicLoss(i, loss) {
			if haveHistory {
				if _, err := dec.DecodePLC(tmp, frameSize); err != nil {
					t.Fatalf("frame %d: PLC: %v", i, err)
				}
				out = appendInt16AsFloat(out, tmp)
			} else {
				out = append(out, make([]float64, frameSize*channels)...)
			}
			continue
		}
		pcm, err := dec.DecodeFloat(packet)
		if err != nil {
			t.Fatalf("frame %d: decode: %v", i, err)
		}
		out = append(out, pcm...)
		haveHistory = true
	}
	return out
}

func deterministicLoss(frame, loss int) bool {
	if loss <= 0 {
		return false
	}
	if loss >= 100 {
		return true
	}
	return (frame*37+13)%100 < loss
}

func appendInt16AsFloat(out []float64, pcm []int16) []float64 {
	for _, sample := range pcm {
		out = append(out, float64(sample)/32768)
	}
	return out
}

func realCorpusMatchedBitrateFor(bytes, rate, frameSize, frames int) int {
	return int(math.Round(float64(bytes*8*rate) / float64(frames*frameSize)))
}

func corpusEncoderMode(kind string) (Application, SignalType) {
	if strings.Contains(kind, "music") || strings.Contains(kind, "mixed") {
		return ApplicationAudio, SignalMusic
	}
	return ApplicationVOIP, SignalVoice
}

func corpusClass(path string) string {
	parent := filepath.Base(filepath.Dir(path))
	if parent == "." || parent == string(filepath.Separator) {
		return "unknown"
	}
	return strings.ToLower(parent)
}

func envInt(name string, fallback int) int {
	if value := os.Getenv(name); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func readCorpusWAV(path string) (corpusClip, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return corpusClip{}, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return corpusClip{}, fmt.Errorf("not a RIFF/WAVE file")
	}
	var format uint16
	var channels uint16
	var rate uint32
	var bits uint16
	var payload []byte
	for pos := 12; pos+8 <= len(data); {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if size < 0 || size > len(data)-pos {
			return corpusClip{}, fmt.Errorf("truncated %q chunk", id)
		}
		chunk := data[pos : pos+size]
		switch id {
		case "fmt ":
			if len(chunk) < 16 {
				return corpusClip{}, fmt.Errorf("short fmt chunk")
			}
			format = binary.LittleEndian.Uint16(chunk[0:2])
			channels = binary.LittleEndian.Uint16(chunk[2:4])
			rate = binary.LittleEndian.Uint32(chunk[4:8])
			bits = binary.LittleEndian.Uint16(chunk[14:16])
		case "data":
			payload = chunk
		}
		pos += size
		if pos&1 != 0 {
			pos++
		}
	}
	if payload == nil {
		return corpusClip{}, fmt.Errorf("missing data chunk")
	}
	if channels != 1 && channels != 2 {
		return corpusClip{}, fmt.Errorf("unsupported channel count %d", channels)
	}
	if !isValidOpusRate(int(rate)) {
		return corpusClip{}, fmt.Errorf("unsupported sample rate %d; convert to 8000/12000/16000/24000/48000 Hz", rate)
	}
	var pcm []float64
	switch {
	case format == 1 && bits == 16:
		if len(payload)%2 != 0 {
			return corpusClip{}, fmt.Errorf("odd 16-bit payload length")
		}
		pcm = make([]float64, len(payload)/2)
		for i := range pcm {
			pcm[i] = float64(int16(binary.LittleEndian.Uint16(payload[i*2:i*2+2]))) / 32768
		}
	case format == 3 && bits == 32:
		if len(payload)%4 != 0 {
			return corpusClip{}, fmt.Errorf("misaligned float32 payload length")
		}
		pcm = make([]float64, len(payload)/4)
		for i := range pcm {
			bits := binary.LittleEndian.Uint32(payload[i*4 : i*4+4])
			pcm[i] = float64(math.Float32frombits(bits))
		}
	default:
		return corpusClip{}, fmt.Errorf("unsupported WAV format=%d bits=%d", format, bits)
	}
	return corpusClip{rate: int(rate), channels: int(channels), pcm: pcm}, nil
}
