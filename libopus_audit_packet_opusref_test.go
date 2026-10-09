//go:build opusref && opus16

package opus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

type auditStat struct {
	name                                  string
	total, agree, goErrLibOK, goOKLibErr  int
	valueDiff, errKindDiff                int
	exGoErr, exLibErr, exValue, exErrKind string
}

func (s *auditStat) rec(input []byte, lib int, libOK bool, goV int, goErr error, extra string) {
	s.total++
	switch {
	case libOK && goErr == nil:
		if lib == goV {
			s.agree++
		} else {
			s.valueDiff++
			if s.exValue == "" {
				s.exValue = fmt.Sprintf("in=%x %s lib=%d go=%d", trunc(input), extra, lib, goV)
			}
		}
	case !libOK && goErr != nil:
		s.agree++
		if k := goErrCode(goErr); k != lib {
			s.errKindDiff++
			if s.exErrKind == "" {
				s.exErrKind = fmt.Sprintf("in=%x %s lib=%d go=%v", trunc(input), extra, lib, goErr)
			}
		}
	case libOK:
		s.goErrLibOK++
		if s.exGoErr == "" {
			s.exGoErr = fmt.Sprintf("in=%x %s lib=%d go=%v", trunc(input), extra, lib, goErr)
		}
	default:
		s.goOKLibErr++
		if s.exLibErr == "" {
			s.exLibErr = fmt.Sprintf("in=%x %s lib=%d go=%d", trunc(input), extra, lib, goV)
		}
	}
}

func (s *auditStat) log(t *testing.T) {
	t.Logf("[%s] total=%d agree=%d valueDiff=%d goErr/libOK=%d goOK/libErr=%d errKindDiff=%d",
		s.name, s.total, s.agree, s.valueDiff, s.goErrLibOK, s.goOKLibErr, s.errKindDiff)
	for _, e := range []struct{ k, v string }{{"value", s.exValue}, {"goErr", s.exGoErr}, {"libErr", s.exLibErr}, {"errKind", s.exErrKind}} {
		if e.v != "" {
			t.Logf("    first %s mismatch: %s", e.k, e.v)
		}
	}
}

func trunc(b []byte) []byte {
	if len(b) > auditTrunc {
		return b[:auditTrunc]
	}
	return b
}

func goErrCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrBadArg):
		return -1
	case errors.Is(err, ErrBufferTooSmall):
		return -2
	case errors.Is(err, ErrInternalError):
		return -3
	case errors.Is(err, ErrInvalidPacket):
		return -4
	case errors.Is(err, ErrUnimplemented):
		return -5
	case errors.Is(err, ErrInvalidState):
		return -6
	case errors.Is(err, ErrAllocFail):
		return -7
	}
	return -99
}

func vectorPackets(limit int) [][]byte {
	var packets [][]byte
	for v := 1; v <= 12; v++ {
		data, err := os.ReadFile(filepath.Join("testdata", "opus_newvectors", fmt.Sprintf("testvector%02d.bit", v)))
		if err != nil {
			continue
		}
		n := 0
		for pos := 0; pos+8 <= len(data) && n < limit; n++ {
			l := int(binary.BigEndian.Uint32(data[pos:]))
			pos += 8
			if l > 0 && pos+l <= len(data) {
				packets = append(packets, append([]byte(nil), data[pos:pos+l]...))
			}
			pos += l
		}
	}
	return packets
}

// randomValidPacket builds a well-formed packet with the given TOC base.
func randomValidPacket(rng *rand.Rand, tocBase byte, nFrames int) []byte {
	frames := make([][]byte, nFrames)
	cbr := rng.Intn(3) == 0
	sz := rng.Intn(80)
	for i := range frames {
		n := sz
		if !cbr {
			switch rng.Intn(6) {
			case 0:
				n = 0
			case 1:
				n = 250 + rng.Intn(10)
			case 2:
				n = rng.Intn(1276)
			default:
				n = rng.Intn(60)
			}
		}
		frames[i] = make([]byte, n)
		rng.Read(frames[i])
	}
	payload, code, err := packOpusFrames(frames, false)
	if err != nil {
		return nil
	}
	return append([]byte{tocBase&0xfc | byte(code)}, payload...)
}

func auditCorpus(rng *rand.Rand) [][]byte {
	corpus := vectorPackets(400)
	for i := 0; i < 30000; i++ {
		p := make([]byte, rng.Intn(48))
		rng.Read(p)
		if len(p) > 1 && rng.Intn(2) == 0 {
			p[0] |= 3
			p[1] = p[1]&0xC0 | byte(1+rng.Intn(8))
		}
		corpus = append(corpus, p)
	}
	for i := 0; i < 5000; i++ {
		toc := byte(rng.Intn(256))
		fs := packetSamplesPerFrame48k(toc)
		maxN := 5760 / fs
		if maxN > 48 {
			maxN = 48
		}
		n := 1 + rng.Intn(maxN+1) // occasionally one too many
		if p := randomValidPacket(rng, toc, n); p != nil {
			corpus = append(corpus, p)
			// truncated and extended variants
			if len(p) > 2 {
				corpus = append(corpus, p[:rng.Intn(len(p))])
			}
		}
	}
	return corpus
}

func TestAuditPacketGetters(t *testing.T) {
	auditEnabled(t)
	rng := rand.New(rand.NewSource(7))
	corpus := auditCorpus(rng)
	st := map[string]*auditStat{}
	get := func(n string) *auditStat {
		if st[n] == nil {
			st[n] = &auditStat{name: n}
		}
		return st[n]
	}
	rates := []int{8000, 12000, 16000, 24000, 48000, 44100, 96000, 0, -1}
	lbrrDiffSizes := map[int]int{}
	for _, p := range corpus {
		pr, _, psz, _ := cgoref.PacketParse(p)
		sfx := ""
		if pr > 0 {
			sfx = "(parse-valid)"
		}
		get := func(n string) *auditStat { return get(n + sfx) }
		if pr > 0 && len(psz) > 0 {
			lib := cgoref.AudPacketHasLBRR(p)
			gb, _ := PacketHasLBRR(p)
			if (lib == 1) != gb {
				lbrrDiffSizes[psz[0]]++
			}
		}
		if len(p) >= 1 {
			lib := cgoref.AudPacketGetBandwidth(p)
			g, err := PacketGetBandwidth(p)
			get("get_bandwidth").rec(p, lib, lib >= 0, g, err, "")
			lib = cgoref.AudPacketGetNbChannels(p)
			g, err = PacketGetNumChannels(p)
			get("get_nb_channels").rec(p, lib, lib >= 0, g, err, "")
			for _, fs := range rates {
				lib = cgoref.AudPacketGetSamplesPerFrame(p, fs)
				g, err = PacketGetSamplesPerFrame(p, fs)
				get("get_samples_per_frame").rec(p, lib, true, g, err, fmt.Sprintf("fs=%d", fs))
			}
			lib = cgoref.AudPacketHasLBRR(p)
			gb, err := PacketHasLBRR(p)
			gi := 0
			if gb {
				gi = 1
			}
			get("has_lbrr").rec(p, lib, lib >= 0, gi, err, "")
		}
		lib := cgoref.AudPacketGetNbFrames(p)
		g, err := PacketGetNumFrames(p)
		get("get_nb_frames").rec(p, lib, lib >= 0, g, err, "")
		for _, fs := range rates[:7] {
			lib = cgoref.AudPacketGetNbSamples(p, fs)
			g, err = PacketGetNumSamples(p, fs)
			get("get_nb_samples").rec(p, lib, lib >= 0, g, err, fmt.Sprintf("fs=%d", fs))
		}
		for _, fs := range []int{8000, 48000} {
			lib = cgoref.AudDecoderGetNbSamples(fs, 1, p)
			g, err = PacketGetNumSamples(p, fs)
			get("decoder_get_nb_samples").rec(p, lib, lib >= 0, g, err, fmt.Sprintf("fs=%d", fs))
		}
		ret, toc, sizes, off := cgoref.PacketParse(p)
		gtoc, gframes, goff, gerr := PacketParse(p)
		s := get("packet_parse")
		if ret >= 0 && gerr == nil {
			same := toc == gtoc && off == goff && len(sizes) == len(gframes)
			for i := 0; same && i < len(sizes); i++ {
				same = sizes[i] == len(gframes[i])
			}
			v := 0
			if !same {
				v = 1
			}
			s.rec(p, 0, true, v, nil, "")
		} else {
			s.rec(p, ret, ret >= 0, len(gframes), gerr, "")
		}
	}
	for _, n := range []string{"get_bandwidth", "get_nb_channels", "get_samples_per_frame", "get_nb_frames", "get_nb_samples", "decoder_get_nb_samples", "has_lbrr", "packet_parse"} {
		get(n).log(t)
		get(n + "(parse-valid)").log(t)
	}
	t.Logf("has_lbrr mismatches by frame0 size (parse-valid): %v", lbrrDiffSizes)
}

// --- repacketizer ---

func extPacket(rng *rand.Rand, p []byte, nf int) []byte {
	// add 1..3 random extensions with libopus
	n := 1 + rng.Intn(3)
	exts := make([]cgoref.AudExt, n)
	for i := range exts {
		id := 3 + rng.Intn(125)
		var d []byte
		if id < 32 {
			d = make([]byte, rng.Intn(2))
		} else {
			d = make([]byte, rng.Intn(20))
		}
		rng.Read(d)
		exts[i] = cgoref.AudExt{ID: id, Frame: rng.Intn(nf), Data: d}
	}
	out, ret := cgoref.AudPadImpl(p, len(p)+400, false, exts)
	if ret <= 0 {
		return nil
	}
	return out
}

func TestAuditRepacketizer(t *testing.T) {
	auditEnabled(t)
	rng := rand.New(rand.NewSource(11))
	cat := &auditStat{name: "repacketizer_cat"}
	nb := &auditStat{name: "repacketizer_get_nb_frames"}
	outSt := &auditStat{name: "repacketizer_out_range(bytes)"}
	outExt := &auditStat{name: "repacketizer_out_range(with extension packets)"}
	for trial := 0; trial < 6000; trial++ {
		lr := cgoref.NewAudRepacketizer()
		gr := NewRepacketizer()
		toc := byte(rng.Intn(256))
		withExt := trial%3 == 0
		k := 1 + rng.Intn(8)
		for i := 0; i < k; i++ {
			tb := toc
			if rng.Intn(10) == 0 {
				tb = byte(rng.Intn(256))
			}
			nf := 1 + rng.Intn(4)
			p := randomValidPacket(rng, tb, nf)
			if p == nil {
				continue
			}
			if withExt && rng.Intn(2) == 0 {
				if e := extPacket(rng, p, nf); e != nil {
					p = e
				}
			}
			if rng.Intn(15) == 0 {
				p = p[:rng.Intn(len(p))]
			}
			lret := lr.Cat(p)
			gerr := gr.Cat(p)
			cat.rec(p, lret, lret >= 0, 0, gerr, "")
			nb.rec(p, lr.NbFrames(), true, gr.NumFrames(), nil, "")
		}
		for j := 0; j < 4; j++ {
			b := rng.Intn(lr.NbFrames()+2) - 1
			e := rng.Intn(lr.NbFrames()+2) + 0
			if j == 0 {
				b, e = 0, lr.NbFrames()
			}
			lout, lret := lr.OutRange(b, e, 100000)
			gout, gerr := gr.OutRange(b, e)
			s := outSt
			if withExt {
				s = outExt
			}
			if lret >= 0 && gerr == nil {
				v := 0
				if !bytes.Equal(lout, gout) {
					v = 1
				}
				s.rec(lout, 0, true, v, nil, fmt.Sprintf("range=[%d,%d) go=%x", b, e, trunc(gout)))
			} else {
				s.rec(nil, lret, lret >= 0, 0, gerr, fmt.Sprintf("range=[%d,%d) nb=%d", b, e, lr.NbFrames()))
			}
		}
		lr.Close()
	}
	for _, s := range []*auditStat{cat, nb, outSt, outExt} {
		s.log(t)
	}
	// libopus maxlen semantics (no Go equivalent): count BUFFER_TOO_SMALL
	lr := cgoref.NewAudRepacketizer()
	p := randomValidPacket(rng, 0x80, 2)
	lr.Cat(p)
	_, r := lr.Out(len(p) - 1)
	t.Logf("libopus out(maxlen=len-1) => %d (Go Out has no maxlen parameter)", r)
	lr.Close()
}

// --- pad / unpad ---

func TestAuditPadUnpad(t *testing.T) {
	auditEnabled(t)
	rng := rand.New(rand.NewSource(13))
	corpus := auditCorpus(rng)
	// add extension-bearing packets and zero-padded packets
	for i := 0; i < 3000; i++ {
		nf := 1 + rng.Intn(3)
		p := randomValidPacket(rng, byte(rng.Intn(256)), nf)
		if p == nil {
			continue
		}
		if e := extPacket(rng, p, nf); e != nil {
			corpus = append(corpus, e)
		}
		if q, r := cgoref.AudPad(p, len(p)+1+rng.Intn(600)); r == 0 {
			corpus = append(corpus, q)
		}
	}
	pad := &auditStat{name: "packet_pad"}
	padExt := &auditStat{name: "packet_pad(input has extensions)"}
	unpad := &auditStat{name: "packet_unpad"}
	for _, p := range corpus {
		hasExt := false
		if r, _, _, _, _, padding := cgoref.AudParseImpl(p, false); r > 0 && cgoref.AudExtCount(padding, r) > 0 {
			hasExt = true
		}
		for _, delta := range []int{-1, 0, 1, 2, 254, 255, 256, 600, rng.Intn(2000)} {
			newLen := len(p) + delta
			if newLen < 0 {
				continue
			}
			lout, lret := cgoref.AudPad(p, newLen)
			gout, gerr := PacketPad(p, newLen)
			s := pad
			if hasExt {
				s = padExt
			}
			if lret == 0 && gerr == nil {
				v := 0
				if !bytes.Equal(lout, gout) {
					v = 1
				}
				s.rec(p, 0, true, v, nil, fmt.Sprintf("newLen=%d lib=%x go=%x", newLen, trunc(lout), trunc(gout)))
			} else {
				s.rec(p, lret, lret == 0, 0, gerr, fmt.Sprintf("newLen=%d", newLen))
			}
		}
		lout, lret := cgoref.AudUnpad(p)
		gout, gerr := PacketUnpad(p)
		if lret >= 0 && gerr == nil {
			v := 0
			if !bytes.Equal(lout, gout) {
				v = 1
			}
			unpad.rec(p, 0, true, v, nil, fmt.Sprintf("lib=%x go=%x", trunc(lout), trunc(gout)))
		} else {
			unpad.rec(p, lret, lret >= 0, 0, gerr, "")
		}
	}
	pad.log(t)
	padExt.log(t)
	unpad.log(t)

	// multistream
	mpad := &auditStat{name: "multistream_packet_pad"}
	munpad := &auditStat{name: "multistream_packet_unpad"}
	for trial := 0; trial < 6000; trial++ {
		streams := 1 + rng.Intn(4)
		var pkts [][]byte
		toc := byte(rng.Intn(256))
		nf := 1 + rng.Intn(2)
		for s := 0; s < streams; s++ {
			p := randomValidPacket(rng, toc, nf)
			if p == nil {
				p = []byte{toc &^ 3, 1, 2}
			}
			if rng.Intn(3) == 0 {
				if q, r := cgoref.AudPad(p, len(p)+1+rng.Intn(300)); r == 0 {
					p = q
				}
			}
			if rng.Intn(4) == 0 {
				if e := extPacket(rng, p, nf); e != nil {
					p = e
				}
			}
			pkts = append(pkts, p)
		}
		data, err := joinMultistreamPackets(pkts)
		if err != nil {
			continue
		}
		if rng.Intn(10) == 0 {
			data = data[:rng.Intn(len(data))]
		}
		nsArg := streams
		if rng.Intn(10) == 0 {
			nsArg = rng.Intn(6)
		}
		for _, delta := range []int{0, 1, 300, rng.Intn(1000)} {
			newLen := len(data) + delta
			lout, lret := cgoref.AudMSPad(data, newLen, nsArg)
			gout, gerr := MultistreamPacketPad(data, nsArg, newLen)
			if lret == 0 && gerr == nil {
				v := 0
				if !bytes.Equal(lout, gout) {
					v = 1
				}
				mpad.rec(data, 0, true, v, nil, fmt.Sprintf("streams=%d newLen=%d", nsArg, newLen))
			} else {
				mpad.rec(data, lret, lret == 0, 0, gerr, fmt.Sprintf("streams=%d newLen=%d", nsArg, newLen))
			}
		}
		lout, lret := cgoref.AudMSUnpad(data, nsArg)
		gout, gerr := MultistreamPacketUnpad(data, nsArg)
		if lret >= 0 && gerr == nil {
			v := 0
			if !bytes.Equal(lout, gout) {
				v = 1
			}
			munpad.rec(data, 0, true, v, nil, fmt.Sprintf("streams=%d", nsArg))
		} else {
			munpad.rec(data, lret, lret >= 0, 0, gerr, fmt.Sprintf("streams=%d", nsArg))
		}
	}
	mpad.log(t)
	munpad.log(t)
}

// --- soft clip ---

func TestAuditSoftClip(t *testing.T) {
	auditEnabled(t)
	rng := rand.New(rand.NewSource(17))
	calls, sampleDiff, memDiff, seqs := 0, 0, 0, 0
	var maxAbs float64
	first := ""
	for seq := 0; seq < 3000; seq++ {
		seqs++
		ch := 1 + rng.Intn(3)
		lmem := make([]float32, ch)
		gmem := make([]float32, ch)
		amp := []float32{0.5, 1.2, 2.5, 4, 1e6}[rng.Intn(5)]
		for c := 0; c < 6; c++ {
			n := rng.Intn(500)
			if c == 0 && rng.Intn(5) == 0 {
				n = 1
			}
			pcm := make([]float32, n*ch)
			for i := range pcm {
				switch rng.Intn(20) {
				case 0:
					pcm[i] = 0
				default:
					pcm[i] = (rng.Float32()*2 - 1) * amp
				}
			}
			// smooth signals too
			if rng.Intn(2) == 0 {
				f := rng.Float64() * 0.2
				for i := 0; i < n; i++ {
					for k := 0; k < ch; k++ {
						pcm[i*ch+k] = amp * float32(math.Sin(f*float64(i)+float64(k)))
					}
				}
			}
			lp := append([]float32(nil), pcm...)
			gp := append([]float32(nil), pcm...)
			cgoref.AudSoftClip(lp, n, ch, lmem)
			if err := SoftClipFloat32(gp, ch, gmem); err != nil {
				t.Fatalf("go softclip err %v", err)
			}
			calls++
			for i := range lp {
				if math.Float32bits(lp[i]) != math.Float32bits(gp[i]) {
					sampleDiff++
					d := math.Abs(float64(lp[i] - gp[i]))
					if d > maxAbs {
						maxAbs = d
					}
					if first == "" {
						first = fmt.Sprintf("seq=%d call=%d ch=%d n=%d i=%d in=%g lib=%g go=%g", seq, c, ch, n, i, pcm[i], lp[i], gp[i])
					}
				}
			}
			for k := range lmem {
				if math.Float32bits(lmem[k]) != math.Float32bits(gmem[k]) {
					memDiff++
				}
			}
		}
	}
	t.Logf("[soft_clip] seqs=%d calls=%d sampleBitDiffs=%d memBitDiffs=%d maxAbsDiff=%g first=%s", seqs, calls, sampleDiff, memDiff, maxAbs, first)
	// argument handling
	t.Logf("go SoftClipFloat32(ch=0)=%v (libopus: silent no-op)", SoftClipFloat32([]float32{3}, 0, []float32{0}))
	t.Logf("go SoftClipFloat32(mem short)=%v (libopus: no-op when mem NULL)", SoftClipFloat32([]float32{3, 3}, 2, []float32{0}))
	x := []float32{3, 3, 3}
	t.Logf("go SoftClipFloat32(len%%ch!=0)=%v", SoftClipFloat32(x, 2, []float32{0, 0}))
}

// --- extensions ---

func randExts(rng *rand.Rand, nf int, allowInvalid bool) []cgoref.AudExt {
	n := rng.Intn(6)
	exts := make([]cgoref.AudExt, n)
	for i := range exts {
		id := 3 + rng.Intn(125)
		if allowInvalid && rng.Intn(15) == 0 {
			id = []int{0, 1, 2, 128, -1}[rng.Intn(5)]
		}
		var d []byte
		switch {
		case id < 32:
			d = make([]byte, rng.Intn(2))
			if allowInvalid && rng.Intn(20) == 0 {
				d = make([]byte, 2)
			}
		case rng.Intn(4) == 0:
			d = make([]byte, 250+rng.Intn(300))
		default:
			d = make([]byte, rng.Intn(10))
		}
		rng.Read(d)
		f := rng.Intn(nf)
		if allowInvalid && rng.Intn(20) == 0 {
			f = nf
		}
		exts[i] = cgoref.AudExt{ID: id, Frame: f, Data: d}
	}
	// libopus requires frame-nondecreasing order? keep random order sometimes
	if rng.Intn(2) == 0 {
		for i := 1; i < len(exts); i++ {
			for j := i; j > 0 && exts[j].Frame < exts[j-1].Frame; j-- {
				exts[j], exts[j-1] = exts[j-1], exts[j]
			}
		}
	}
	// duplicate one ext across all frames to exercise repeats
	if n > 0 && rng.Intn(3) == 0 {
		base := exts[0]
		exts = exts[:0]
		for f := 0; f < nf; f++ {
			exts = append(exts, cgoref.AudExt{ID: base.ID, Frame: f, Data: base.Data})
		}
	}
	return exts
}

func TestAuditExtensions(t *testing.T) {
	auditEnabled(t)
	rng := rand.New(rand.NewSource(19))
	gen := &auditStat{name: "extensions_generate(packet,minimal) vs pad_impl(pad=0)"}
	genPad := &auditStat{name: "extensions_generate(packet,paddingBytes) vs pad_impl(pad=1)"}
	parse := &auditStat{name: "extensions_parse(generated)"}
	count := &auditStat{name: "extensions_count(random padding)"}
	parseR := &auditStat{name: "extensions_parse(random padding)"}
	for trial := 0; trial < 20000; trial++ {
		nf := 1 + rng.Intn(6)
		toc := byte(rng.Intn(256))
		if packetSamplesPerFrame48k(toc)*nf > 5760 {
			toc = 0x80 // 2.5ms CELT
		}
		p := randomValidPacket(rng, toc, nf)
		if p == nil {
			continue
		}
		lexts := randExts(rng, nf, trial%4 == 0)
		gexts := make([]PacketExtension, len(lexts))
		for i, e := range lexts {
			gexts[i] = PacketExtension{ID: e.ID, Frame: e.Frame, Data: e.Data}
		}
		lout, lret := cgoref.AudPadImpl(p, len(p)+20000, false, lexts)
		gout, gerr := PacketExtensionsGenerate(p, gexts, 0)
		if lret > 0 && gerr == nil {
			v := 0
			if !bytes.Equal(lout, gout) {
				v = 1
			}
			gen.rec(p, 0, true, v, nil, fmt.Sprintf("exts=%v lib=%x go=%x", extSummary(lexts), trunc(lout), trunc(gout)))
		} else {
			gen.rec(p, lret, lret > 0, 0, gerr, fmt.Sprintf("exts=%v", extSummary(lexts)))
		}
		if gerr == nil && len(lexts) > 0 {
			pb := rng.Intn(400)
			gout2, gerr2 := PacketExtensionsGenerate(p, gexts, pb)
			if gerr2 == nil {
				lout2, lret2 := cgoref.AudPadImpl(p, len(gout2), true, lexts)
				v := 0
				if lret2 <= 0 || !bytes.Equal(lout2, gout2) {
					v = 1
				}
				genPad.rec(p, 0, true, v, nil, fmt.Sprintf("paddingBytes=%d lret=%d lib=%x go=%x", pb, lret2, trunc(lout2), trunc(gout2)))
			} else {
				genPad.rec(p, 0, true, 0, gerr2, fmt.Sprintf("paddingBytes=%d", pb))
			}
			// parse comparison on the generated packet
			r, _, _, _, _, padding := cgoref.AudParseImpl(gout, false)
			le, lr := cgoref.AudExtParse(padding, r)
			ge, ge2 := PacketExtensionsParse(gout)
			if lr >= 0 && ge2 == nil {
				parse.rec(gout, 0, true, boolInt(!sameExts(le, ge)), nil, fmt.Sprintf("lib=%v go=%v", extSummary(le), extSummaryGo(ge)))
			} else {
				parse.rec(gout, lr, lr >= 0, 0, ge2, "")
			}
		}
		// random padding parse/count
		padding := make([]byte, rng.Intn(40))
		rng.Read(padding)
		for i := range padding {
			if rng.Intn(3) == 0 {
				padding[i] = byte(rng.Intn(8))
			}
		}
		pk, err := buildPaddedPacket(p, nf, padding)
		if err != nil {
			continue
		}
		lc := cgoref.AudExtCount(padding, nf)
		gc, gce := PacketExtensionsCount(pk)
		count.rec(padding, lc, lc >= 0, gc, gce, fmt.Sprintf("nf=%d", nf))
		le, lr := cgoref.AudExtParse(padding, nf)
		ge, ge2 := PacketExtensionsParse(pk)
		if lr >= 0 && ge2 == nil {
			parseR.rec(padding, 0, true, boolInt(!sameExts(le, ge)), nil, fmt.Sprintf("nf=%d lib=%v go=%v", nf, extSummary(le), extSummaryGo(ge)))
		} else {
			parseR.rec(padding, lr, lr >= 0, 0, ge2, fmt.Sprintf("nf=%d", nf))
		}
	}
	for _, s := range []*auditStat{gen, genPad, parse, count, parseR} {
		s.log(t)
	}
}

// buildPaddedPacket makes a code-3 packet with the given raw padding bytes.
func buildPaddedPacket(p []byte, nf int, padding []byte) ([]byte, error) {
	_, frames, _, err := PacketParse(p)
	if err != nil {
		return nil, err
	}
	out := []byte{p[0] | 3, byte(len(frames)) | 0x80 | 0x40}
	n := len(padding)
	for n >= 255 {
		out = append(out, 255)
		n -= 254
	}
	out = append(out, byte(n))
	for i := 0; i < len(frames)-1; i++ {
		l := len(frames[i])
		if l < 252 {
			out = append(out, byte(l))
		} else {
			out = append(out, byte(252+(l&3)), byte((l-252)>>2))
		}
	}
	for _, f := range frames {
		out = append(out, f...)
	}
	return append(out, padding...), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sameExts(a []cgoref.AudExt, b []PacketExtension) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Frame != b[i].Frame || !bytes.Equal(a[i].Data, b[i].Data) {
			return false
		}
	}
	return true
}

func extSummary(e []cgoref.AudExt) string {
	s := ""
	for _, x := range e {
		s += fmt.Sprintf("{%d f%d l%d}", x.ID, x.Frame, len(x.Data))
	}
	return s
}

func extSummaryGo(e []PacketExtension) string {
	s := ""
	for _, x := range e {
		s += fmt.Sprintf("{%d f%d l%d}", x.ID, x.Frame, len(x.Data))
	}
	return s
}

var auditTrunc = 24
