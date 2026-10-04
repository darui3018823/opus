package audit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	opus "github.com/darui3018823/opus"
)

func oraclePath() string { return filepath.Join(os.TempDir(), "opusdecaudit", "dec_oracle.exe") }

const (
	fmtI16 = 0
	fmtF32 = 1
	fmtI24 = 2
)

type op struct {
	code      int // 1 decode, 2 reset, 3 gain, 4 phase inv
	fmt       int
	fec       bool
	frameSize int
	data      []byte // nil = PLC
	val       int
}

type decCfg struct {
	kind                     int // 0 single, 1 ms, 2 proj
	rate, ch, streams, coupl int
	mapping                  []byte
}

type result struct {
	ret     int
	rng     uint32
	lastDur int
	bw      int
	pitch   int
	i16     []int16
	f32     []float32
	i24     []int32
}

func le32(b *bytes.Buffer, v int) { binary.Write(b, binary.LittleEndian, int32(v)) }

func runOracle(t testing.TB, cfg decCfg, ops []op) []result {
	t.Helper()
	var b bytes.Buffer
	le32(&b, cfg.kind)
	le32(&b, cfg.rate)
	le32(&b, cfg.ch)
	le32(&b, cfg.streams)
	le32(&b, cfg.coupl)
	le32(&b, len(cfg.mapping))
	b.Write(cfg.mapping)
	for _, o := range ops {
		b.WriteByte(byte(o.code))
		switch o.code {
		case 1:
			b.WriteByte(byte(o.fmt))
			if o.fec {
				b.WriteByte(1)
			} else {
				b.WriteByte(0)
			}
			le32(&b, o.frameSize)
			if o.data == nil {
				le32(&b, -1)
			} else {
				le32(&b, len(o.data))
				b.Write(o.data)
			}
		case 3, 4:
			le32(&b, o.val)
		}
	}
	dir := t.TempDir()
	in, outp := filepath.Join(dir, "s.bin"), filepath.Join(dir, "o.bin")
	if err := os.WriteFile(in, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(oraclePath(), "dec", in, outp).CombinedOutput(); err != nil {
		t.Fatalf("oracle: %v %s", err, out)
	}
	raw, err := os.ReadFile(outp)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	rd := func() int32 { var v int32; binary.Read(r, binary.LittleEndian, &v); return v }
	if e := rd(); e != 0 {
		t.Fatalf("oracle create err %d", e)
	}
	var res []result
	for _, o := range ops {
		var x result
		x.ret = int(rd())
		if o.code == 1 {
			x.rng = uint32(rd())
			x.lastDur = int(rd())
			x.bw = int(rd())
			x.pitch = int(rd())
			if x.ret > 0 {
				n := x.ret * cfg.ch
				switch o.fmt {
				case fmtI16:
					x.i16 = make([]int16, n)
					binary.Read(r, binary.LittleEndian, x.i16)
				case fmtF32:
					x.f32 = make([]float32, n)
					binary.Read(r, binary.LittleEndian, x.f32)
				case fmtI24:
					x.i24 = make([]int32, n)
					binary.Read(r, binary.LittleEndian, x.i24)
				}
			}
		}
		res = append(res, x)
	}
	return res
}

// libopus error codes
func errCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, opus.ErrBufferTooSmall):
		return -2
	case errors.Is(err, opus.ErrInvalidPacket):
		return -4
	case errors.Is(err, opus.ErrBadArg):
		return -1
	case errors.Is(err, opus.ErrInternalError):
		return -3
	case errors.Is(err, opus.ErrUnimplemented):
		return -5
	case errors.Is(err, opus.ErrInvalidState):
		return -6
	}
	return -99
}

// goDecoder abstracts the Go decoders.
type goDecoder interface {
	run(o op, ch int) (result, error)
}

type singleDec struct{ d *opus.Decoder }

func (s singleDec) run(o op, ch int) (result, error) {
	var x result
	d := s.d
	var err error
	var n int
	switch o.code {
	case 2:
		err = d.Reset()
	case 3:
		err = d.SetGain(o.val)
	case 4:
		d.SetPhaseInversionDisabled(o.val != 0)
	case 1:
		switch o.fmt {
		case fmtI16:
			pcm := make([]int16, max(0, o.frameSize)*ch)
			switch {
			case o.data == nil:
				n, err = d.DecodePLC(pcm, o.frameSize)
			case o.fec:
				n, err = d.DecodeFECWithDuration(o.data, pcm, o.frameSize)
			default:
				n, err = d.Decode(o.data, pcm)
			}
			if err == nil {
				x.i16 = pcm[:n*ch]
			}
		case fmtI24:
			pcm := make([]int32, max(0, o.frameSize)*ch)
			switch {
			case o.data == nil:
				n, err = d.DecodePLC24(pcm, o.frameSize)
			case o.fec:
				n, err = d.DecodeFEC24(o.data, pcm, o.frameSize)
			default:
				n, err = d.Decode24(o.data, pcm)
			}
			if err == nil {
				x.i24 = pcm[:n*ch]
			}
		case fmtF32:
			var f []float32
			switch {
			case o.data == nil:
				f, err = d.DecodePLCFloat32(o.frameSize)
			case o.fec:
				f, err = d.DecodeFECFloat32(o.data, o.frameSize)
			default:
				f, err = d.DecodeFloat32(o.data)
				if err == nil && len(f) > o.frameSize*ch {
					err = opus.ErrBufferTooSmall
				}
			}
			if err == nil {
				x.f32 = f
				n = len(f) / ch
			}
		}
		x.rng = d.FinalRange()
		x.lastDur = d.GetLastPacketDuration()
		x.bw = d.Bandwidth()
		x.pitch = d.Pitch48kHz()
	}
	if err != nil {
		x.ret = errCode(err)
	} else {
		x.ret = n
	}
	return x, err
}

type msDec struct{ d *opus.MultistreamDecoder }

func (s msDec) run(o op, ch int) (result, error) {
	var x result
	d := s.d
	var err error
	var n int
	switch o.code {
	case 2:
		err = d.Reset()
	case 3:
		err = d.SetGain(o.val)
	case 4:
		d.SetPhaseInversionDisabled(o.val != 0)
	case 1:
		switch o.fmt {
		case fmtI16:
			pcm := make([]int16, max(0, o.frameSize)*ch)
			switch {
			case o.data == nil:
				n, err = d.DecodePLC(pcm, o.frameSize)
			case o.fec:
				n, err = d.DecodeFECWithDuration(o.data, pcm, o.frameSize)
			default:
				n, err = d.Decode(o.data, pcm)
			}
			if err == nil {
				x.i16 = pcm[:n*ch]
			}
		case fmtI24:
			pcm := make([]int32, max(0, o.frameSize)*ch)
			switch {
			case o.data == nil:
				n, err = d.DecodePLC24(pcm, o.frameSize)
			case o.fec:
				n, err = d.DecodeFEC24(o.data, pcm, o.frameSize)
			default:
				n, err = d.Decode24(o.data, pcm)
			}
			if err == nil {
				x.i24 = pcm[:n*ch]
			}
		case fmtF32:
			var f []float32
			switch {
			case o.data == nil:
				f, err = d.DecodePLCFloat32(o.frameSize)
			case o.fec:
				f, err = d.DecodeFECFloat32(o.data, o.frameSize)
			default:
				f, err = d.DecodeFloat32(o.data)
			}
			if err == nil {
				x.f32 = f
				n = len(f) / ch
			}
		}
		x.rng = d.FinalRange()
		x.lastDur = d.GetLastPacketDuration()
		x.bw = d.Bandwidth()
	}
	if err != nil {
		x.ret = errCode(err)
	} else {
		x.ret = n
	}
	return x, err
}

type projDec struct{ d *opus.ProjectionDecoder }

func (s projDec) run(o op, ch int) (result, error) {
	var x result
	d := s.d
	var err error
	var n int
	switch o.code {
	case 2:
		err = d.Reset()
	case 3, 4:
		err = opus.ErrUnimplemented
	case 1:
		if o.data == nil || o.fec {
			err = opus.ErrUnimplemented
			break
		}
		switch o.fmt {
		case fmtI16:
			pcm := make([]int16, max(0, o.frameSize)*ch)
			n, err = d.Decode(o.data, pcm)
			if err == nil {
				x.i16 = pcm[:n*ch]
			}
		case fmtI24:
			pcm := make([]int32, max(0, o.frameSize)*ch)
			n, err = d.Decode24(o.data, pcm)
			if err == nil {
				x.i24 = pcm[:n*ch]
			}
		case fmtF32:
			var f []float32
			f, err = d.DecodeFloat32(o.data)
			if err == nil {
				x.f32 = f
				n = len(f) / ch
			}
		}
		x.rng = d.FinalRange()
	}
	if err != nil {
		x.ret = errCode(err)
	} else {
		x.ret = n
	}
	return x, err
}

// cmpStats accumulates comparison results.
type cmpStats struct {
	ops, decOps, exactOps   int
	samples, exactSamples   int
	maxAbs                  float64
	retMismatch, rngMis     int
	durMis, bwMis, pitchMis int
	firstDiffOp             int
	firstDiffSample         int
	firstNote               string
	goErr                   int
	notes                   []string
}

func (s *cmpStats) String() string {
	pct := 100.0
	if s.samples > 0 {
		pct = 100 * float64(s.exactSamples) / float64(s.samples)
	}
	return fmt.Sprintf("decOps=%d exactOps=%d samples=%d exact=%.4f%% maxAbs=%.6g retMis=%d rngMis=%d durMis=%d bwMis=%d pitchMis=%d firstOp=%d firstSample=%d %s",
		s.decOps, s.exactOps, s.samples, pct, s.maxAbs, s.retMismatch, s.rngMis, s.durMis, s.bwMis, s.pitchMis, s.firstDiffOp, s.firstDiffSample, s.firstNote)
}

func (s *cmpStats) exact() bool {
	return s.exactOps == s.decOps && s.retMismatch == 0 && s.rngMis == 0
}

type cmpOpts struct {
	checkPitch, checkBW, checkDur bool
}

func compare(t testing.TB, cfg decCfg, ops []op, g goDecoder, opts cmpOpts) *cmpStats {
	t.Helper()
	ref := runOracle(t, cfg, ops)
	st := &cmpStats{firstDiffOp: -1, firstDiffSample: -1}
	for i, o := range ops {
		got, err := g.run(o, cfg.ch)
		r := ref[i]
		if o.code != 1 {
			continue
		}
		st.decOps++
		if got.ret != r.ret {
			st.retMismatch++
			if len(st.notes) < 6 {
				st.notes = append(st.notes, fmt.Sprintf("op%d ret go=%d(%v) lib=%d", i, got.ret, err, r.ret))
			}
			if st.firstDiffOp < 0 {
				st.firstDiffOp = i
				st.firstNote = fmt.Sprintf("ret go=%d(%v) lib=%d", got.ret, err, r.ret)
			}
			continue
		}
		if r.ret <= 0 {
			st.exactOps++
			continue
		}
		if o.data != nil && !o.fec && got.rng != r.rng {
			st.rngMis++
		}
		if opts.checkDur && got.lastDur != r.lastDur {
			st.durMis++
			if len(st.notes) < 6 {
				st.notes = append(st.notes, fmt.Sprintf("op%d lastDur go=%d lib=%d", i, got.lastDur, r.lastDur))
			}
		}
		if opts.checkBW && got.bw != r.bw {
			st.bwMis++
			if len(st.notes) < 6 {
				st.notes = append(st.notes, fmt.Sprintf("op%d bw go=%d lib=%d", i, got.bw, r.bw))
			}
		}
		if opts.checkPitch && got.pitch != r.pitch {
			st.pitchMis++
			if len(st.notes) < 6 {
				st.notes = append(st.notes, fmt.Sprintf("op%d pitch go=%d lib=%d", i, got.pitch, r.pitch))
			}
		}
		var n, exact int
		var maxAbs float64
		first := -1
		switch o.fmt {
		case fmtI16:
			n = len(r.i16)
			for k := range r.i16 {
				if got.i16[k] == r.i16[k] {
					exact++
				} else {
					maxAbs = math.Max(maxAbs, math.Abs(float64(got.i16[k])-float64(r.i16[k])))
					if first < 0 {
						first = k
					}
				}
			}
		case fmtI24:
			n = len(r.i24)
			for k := range r.i24 {
				if got.i24[k] == r.i24[k] {
					exact++
				} else {
					maxAbs = math.Max(maxAbs, math.Abs(float64(got.i24[k])-float64(r.i24[k])))
					if first < 0 {
						first = k
					}
				}
			}
		case fmtF32:
			n = len(r.f32)
			for k := range r.f32 {
				if got.f32[k] == r.f32[k] {
					exact++
				} else {
					maxAbs = math.Max(maxAbs, math.Abs(float64(got.f32[k])-float64(r.f32[k])))
					if first < 0 {
						first = k
					}
				}
			}
		}
		st.samples += n
		st.exactSamples += exact
		st.maxAbs = math.Max(st.maxAbs, maxAbs)
		if exact == n {
			st.exactOps++
		} else if st.firstDiffOp < 0 {
			st.firstDiffOp = i
			st.firstDiffSample = first / cfg.ch
			st.firstNote = fmt.Sprintf("plc=%v fec=%v fmt=%d", o.data == nil, o.fec, o.fmt)
		}
	}
	return st
}

// ---- encoding via oracle ----

type encCfg struct {
	kind, rate, ch, app, family, bitrate, complexity, fec, loss, vbr, cvbr, bandwidth, dtx, forcech, signal, frameSize int
}

type encOut struct {
	streams, coupled int
	mapping          []byte
	packets          [][]byte
}

func oracleEncode(t testing.TB, c encCfg, pcm []float32) encOut {
	t.Helper()
	nframes := len(pcm) / (c.frameSize * c.ch)
	var b bytes.Buffer
	for _, v := range []int{c.kind, c.rate, c.ch, c.app, c.family, c.bitrate, c.complexity, c.fec, c.loss, c.vbr, c.cvbr, c.bandwidth, c.dtx, c.forcech, c.signal, c.frameSize, nframes} {
		le32(&b, v)
	}
	binary.Write(&b, binary.LittleEndian, pcm[:nframes*c.frameSize*c.ch])
	dir := t.TempDir()
	in, outp := filepath.Join(dir, "s.bin"), filepath.Join(dir, "o.bin")
	os.WriteFile(in, b.Bytes(), 0o644)
	if out, err := exec.Command(oraclePath(), "enc", in, outp).CombinedOutput(); err != nil {
		t.Fatalf("oracle enc: %v %s", err, out)
	}
	raw, _ := os.ReadFile(outp)
	r := bytes.NewReader(raw)
	rd := func() int { var v int32; binary.Read(r, binary.LittleEndian, &v); return int(v) }
	var e encOut
	e.streams, e.coupled = rd(), rd()
	e.mapping = make([]byte, rd())
	r.Read(e.mapping)
	for i := 0; i < nframes; i++ {
		n := rd()
		if n < 0 {
			t.Fatalf("enc frame %d: %d", i, n)
		}
		p := make([]byte, n)
		r.Read(p)
		e.packets = append(e.packets, p)
	}
	return e
}
