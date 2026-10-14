package opus_test

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	opus "github.com/darui3018823/opus"
)

// vectorDecodeConfig is one output configuration used by the official-vector
// digest test and benchmark.
type vectorDecodeConfig struct {
	rate     int
	channels int
	float    bool
}

func (c vectorDecodeConfig) String() string {
	format := "int16"
	if c.float {
		format = "float32"
	}
	return fmt.Sprintf("%dk/%dch/%s", c.rate/1000, c.channels, format)
}

func loadOfficialVectorPackets(tb testing.TB) [][][]byte {
	tb.Helper()
	vecDir := filepath.Join("testdata", "opus_newvectors")
	if _, err := os.Stat(vecDir); os.IsNotExist(err) {
		tb.Skip("test vectors not found at testdata/opus_newvectors/")
	}
	vectors := make([][][]byte, 0, 12)
	for v := 1; v <= 12; v++ {
		frames, err := parseOpusDemoBit(filepath.Join(vecDir, fmt.Sprintf("testvector%02d.bit", v)))
		if err != nil {
			tb.Fatalf("testvector%02d: %v", v, err)
		}
		packets := make([][]byte, len(frames))
		for i, f := range frames {
			packets[i] = f.packet
		}
		vectors = append(vectors, packets)
	}
	return vectors
}

// decodeVectorDigest decodes every packet of one vector and hashes the PCM
// and final range of each packet.
func decodeVectorDigest(tb testing.TB, packets [][]byte, cfg vectorDecodeConfig) string {
	tb.Helper()
	dec, err := opus.NewDecoder(cfg.rate, cfg.channels)
	if err != nil {
		tb.Fatal(err)
	}
	h := sha256.New()
	var word [4]byte
	pcm16 := make([]int16, 5760*cfg.channels)
	var pcm32 []float32
	for i, pkt := range packets {
		var n int
		if cfg.float {
			pcm32, err = dec.DecodeFloat32(pkt)
			n = len(pcm32) / cfg.channels
		} else {
			n, err = dec.Decode(pkt, pcm16)
		}
		if err != nil {
			tb.Fatalf("packet %d: %v", i, err)
		}
		for j := 0; j < n*cfg.channels; j++ {
			if cfg.float {
				binary.LittleEndian.PutUint32(word[:], math.Float32bits(pcm32[j]))
			} else {
				binary.LittleEndian.PutUint32(word[:], uint32(uint16(pcm16[j])))
			}
			h.Write(word[:])
		}
		binary.LittleEndian.PutUint32(word[:], dec.FinalRange())
		h.Write(word[:])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

var officialVectorDigestConfigs = []vectorDecodeConfig{
	{48000, 2, false},
	{48000, 1, true},
	{16000, 1, false},
	{24000, 2, true},
}

// officialVectorDigests pins the decoder output of v1.6.0, which matches
// plain-C libopus 1.6.1 on these vectors, so optimisations cannot change a
// single sample.
var officialVectorDigests = map[string][12]string{
	"48k/2ch/int16": {
		"14346f13b28bb4696d8177102d6a4d242cba5b923db3c80fc51aa803eaa67137",
		"4fde3712183885693272a112a76ebe4dbb0158420af8e3ea0a15108386959c33",
		"e6f900cf02071fe9aa8129d5ab744e331f7428940668ca5e5ce9007c42a4b8fe",
		"8b95cde5e21577ebb5825fe5da7141b0f85b92ae9ac008c8a0f43cb312c4a92d",
		"6347c5c3fa83102117cd35af4fadd45e7e0e66f02e3bfa8bf8afefbd5b0cd64f",
		"b44cd3e3d09b1542793d227ce7acf7e22e9729d968b443d06ce5fd1e795f53d8",
		"5f62d6e62fe7f43900f3681a75bae7be51d5dfdc886d84355dbe3b90e55bd7e4",
		"fb4abb0c86d0159ebfc7d2ddf402dcecb9534d37410148aa17b74bda543cf49c",
		"a4701edc3134315312c3efc92109445cd6422a6c0e28e9b054e90a8acfa7dfa3",
		"c7b2de65f24ba44c91a9ea6a08014a776f2b7e77cbe8d4948fd0f7db1ec9d5c6",
		"38ff431d9747402bb43590c691ddb03a2cddfa7dee0c051490af6aad05b95115",
		"c58d6e4b99b5aabef7a00b0f9afc60c3035415c4e390d42f549a281affa566c0",
	},
	"48k/1ch/float32": {
		"dee69899399c0a0ee050edad938e36f6591bdef11841bb0250d42c6ac4f5f930",
		"efdddae724277a7f73195275dae712cb6b2715a959bfa38bcfff7ecfc6b1a733",
		"091ea134f1f37961ca559d1f544c292d74bd4079ca0cffc2c75df96146f631bf",
		"b3914b01874e64b961798d761ed29786aa3578eceee050371031d3796fa0bb60",
		"644756c8af7119ea0b6313303f805be05174e4cfb86d6b88615374eacac0a35c",
		"02fad41bfe206ac21ac15f80f0df93e17878f862cd8c7935b6dd007c62e9ad0b",
		"eb879d63b6f1d35c189052faf3e7a6e509e05e9e55a8fe9f4c82b5658a2967f5",
		"a384601b3f87e7ab82b59849fb6503a1e31585a485eb3ab95b19b7e0900d4012",
		"ccbdddd02e96713540e51fd0ae64179580733594bbae40dfc49043b81100abee",
		"a6514f573bdd2c46600a23d1ce8afa6f4c0e93cffd0704c6c694706899966a40",
		"06e386afd5de086ad689c939d2cc97de81b56b9ac133621ab26091645294f346",
		"ecd2ef2d481fa07daeace1e6ffe108ddb9de804fa8eda9320e3083657e1c479a",
	},
	"16k/1ch/int16": {
		"ce13f0e0df4304ffde70bc6a26657dfe5405362d367e1308d9cd5a86335b968e",
		"edc0a571a63de1d150e1942048881d9fb20e97027c13ef4fccbf535a649750c2",
		"c2bba3c82b312e81b730246c47dcfa473c32207f835fbe7710bba4e97ddc0186",
		"35a0ea1e035c1ef8061cdf3343b71440ef8b64bfa1d701100353ccdcc228c949",
		"57faf281390310398723e3b51598348c130ceebf1c28120f121e2501c61d9a51",
		"58eb260fd9f368e6d2964ee0ebb0435dfae581a2fdc9ab8bfa361b643ada62b0",
		"f6e50ad991dddca72d2e9cb516e60d40139547b93e32527e3ec674ddaee61216",
		"8fecd368954522669c21a2a6efe5d6a95a0a01f4bd5397950bba8b7ece9325c1",
		"91e58db8b7a02ab152bb129d67ce9843df1ebfa0a4a85bc7eee15f2c76959caf",
		"e13a7f04785350a95f3af274e971c24be5997e84825423158c0b836e3741476d",
		"4ec7ae931241d2b541dc14a05cbc090c7b922b3961c293e2d3788fb6d273add7",
		"06e11e00fadf1487cbab1e972c051fb4a7b00684c083b3d1d5684ce6df19229b",
	},
	"24k/2ch/float32": {
		"7eaf641c64ff9de0b063500b7da2d948faf40a315f72c6d9ef150d564ea658e4",
		"70663ac58c695645a5102f69f333a280b27dd62f569fca8ff8cd4bd7d9e0792c",
		"9548b7eaf78095cc24e2c2d74a81358a16937d9f1c428650adeafeafdb30043b",
		"6b0d21d5d1de4615a042de465d0b500d17753f9659fd2055657a428f0ae8c769",
		"e7e0d0042c3a7e1e9d3393ab5bf31b6de42855f913bae94df654fe2bc388920f",
		"f2c7bf32fad2756a83d0ed5151351c40bcd654092231ea5ef3bf3e1a46dd97d8",
		"54725758b20a78a097f85fd35c0c18b4865272668d4f6babd29439ee38105fda",
		"8358b3f3ac4e17eeeeba3b7a521fcd8415375e2eb11917f3727ec391f36a4ba4",
		"d8213af9c3a6ee5f4df384bd0b536b28f4be728edc27c17e5e4c440cbfd49816",
		"77f9e22bf22d3393e2e539bddd2cebfdb3ad9e15c57f14a4fb0ef7d50bbac7d0",
		"51548782da6a77bb7eb71834449d9f8541cdab697f968a31fe8a4a952caa82a1",
		"ed1d922a9842157dc363ccc4c6624060d8c84c4d235671231645b54565be9f62",
	},
}

func TestOfficialVectorDecodeDigests(t *testing.T) {
	vectors := loadOfficialVectorPackets(t)
	for _, cfg := range officialVectorDigestConfigs {
		want, ok := officialVectorDigests[cfg.String()]
		for v, packets := range vectors {
			got := decodeVectorDigest(t, packets, cfg)
			if !ok {
				t.Logf("%s tv%02d %s", cfg, v+1, got)
				continue
			}
			if got != want[v] {
				t.Errorf("%s tv%02d digest = %s, want %s", cfg, v+1, got, want[v])
			}
		}
		if !ok {
			t.Errorf("%s: no golden digests", cfg)
		}
	}
}

// BenchmarkDecodeOfficialVectors decodes each official vector from a fresh
// decoder at 48 kHz stereo int16. The x-realtime metric is the audio duration
// divided by the decode time.
func BenchmarkDecodeOfficialVectors(b *testing.B) {
	vectors := loadOfficialVectorPackets(b)
	for v, packets := range vectors {
		samples := 0
		for _, pkt := range packets {
			if n, err := opus.PacketGetNumSamples(pkt, 48000); err == nil {
				samples += n
			}
		}
		b.Run(fmt.Sprintf("tv%02d", v+1), func(b *testing.B) {
			pcm := make([]int16, 5760*2)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dec, err := opus.NewDecoder(48000, 2)
				if err != nil {
					b.Fatal(err)
				}
				for _, pkt := range packets {
					if _, err := dec.Decode(pkt, pcm); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportMetric(float64(samples)/48000*float64(b.N)/b.Elapsed().Seconds(), "x-realtime")
		})
	}
}
