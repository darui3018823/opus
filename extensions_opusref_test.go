//go:build opusref

package opus

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
	packetext "github.com/darui3018823/opus/internal/extensions"
)

func TestPacketExtensionsLibopusOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		nbFrames := 1 + rng.Intn(8)
		count := rng.Intn(24)
		exts := make([]packetext.Extension, 0, count)
		refExts := make([]cgoref.PacketExtension, 0, count)
		for frame := 0; frame < nbFrames; frame++ {
			for len(exts) < count && rng.Intn(nbFrames-frame) == 0 {
				id := 3 + rng.Intn(125)
				n := 0
				if id < 32 {
					n = rng.Intn(2)
				} else {
					n = rng.Intn(520)
				}
				data := make([]byte, n)
				_, _ = rng.Read(data)
				ext := packetext.Extension{ID: id, Frame: frame, Data: data}
				exts = append(exts, ext)
				refExts = append(refExts, cgoref.PacketExtension{ID: id, Frame: frame, Data: data})
			}
		}
		for len(exts) < count {
			id := 3 + rng.Intn(125)
			n := 0
			if id < 32 {
				n = rng.Intn(2)
			} else {
				n = rng.Intn(520)
			}
			data := make([]byte, n)
			_, _ = rng.Read(data)
			frame := nbFrames - 1
			exts = append(exts, packetext.Extension{ID: id, Frame: frame, Data: data})
			refExts = append(refExts, cgoref.PacketExtension{ID: id, Frame: frame, Data: data})
		}

		goData, err := packetext.Generate(exts, nbFrames, 0)
		if err != nil {
			t.Fatalf("trial %d Go generate: %v", trial, err)
		}
		refData, err := cgoref.GenerateExtensions(refExts, nbFrames, 0)
		if err != nil {
			t.Fatalf("trial %d libopus generate: %v", trial, err)
		}
		if !bytes.Equal(goData, refData) {
			t.Fatalf("trial %d mismatch:\n Go % x\nref % x", trial, goData, refData)
		}
	}
}
