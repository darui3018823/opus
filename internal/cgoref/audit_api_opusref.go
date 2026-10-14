//go:build opusref && opus16

package cgoref

/*
#include <opus.h>
#include <opus_multistream.h>
#include <opus_projection.h>
#include <stdlib.h>
#include <string.h>

typedef struct { int id; int frame; const unsigned char *data; opus_int32 len; } aud_ext;
opus_int32 opus_packet_extensions_parse(const unsigned char*, opus_int32, aud_ext*, opus_int32*, int);
opus_int32 opus_packet_extensions_count(const unsigned char*, opus_int32, int);
opus_int32 opus_packet_extensions_generate(unsigned char*, opus_int32, const aud_ext*, opus_int32, int, int);
int opus_packet_parse_impl(const unsigned char *data, opus_int32 len, int self_delimited, unsigned char *out_toc,
      const unsigned char *frames[48], opus_int16 size[48], int *payload_offset, opus_int32 *packet_offset,
      const unsigned char **padding, opus_int32 *padding_len);
opus_int32 opus_packet_pad_impl(unsigned char *data, opus_int32 len, opus_int32 new_len, int pad, const aud_ext *extensions, int nb_extensions);

static int aud_enc_set(OpusEncoder *e, int req, int v) { return opus_encoder_ctl(e, req, (opus_int32)v); }
static int aud_enc_get(OpusEncoder *e, int req, opus_int32 *v) { return opus_encoder_ctl(e, req, v); }
static int aud_enc_reset(OpusEncoder *e) { return opus_encoder_ctl(e, OPUS_RESET_STATE); }
static int aud_dec_set(OpusDecoder *e, int req, int v) { return opus_decoder_ctl(e, req, (opus_int32)v); }
static int aud_dec_get(OpusDecoder *e, int req, opus_int32 *v) { return opus_decoder_ctl(e, req, v); }
static int aud_dec_reset(OpusDecoder *e) { return opus_decoder_ctl(e, OPUS_RESET_STATE); }
static int aud_msenc_set(OpusMSEncoder *e, int req, int v) { return opus_multistream_encoder_ctl(e, req, (opus_int32)v); }
static int aud_msenc_get(OpusMSEncoder *e, int req, opus_int32 *v) { return opus_multistream_encoder_ctl(e, req, v); }
static int aud_msdec_set(OpusMSDecoder *e, int req, int v) { return opus_multistream_decoder_ctl(e, req, (opus_int32)v); }
static int aud_msdec_get(OpusMSDecoder *e, int req, opus_int32 *v) { return opus_multistream_decoder_ctl(e, req, v); }
static int aud_proj_get(OpusProjectionEncoder *e, int req, opus_int32 *v) { return opus_projection_encoder_ctl(e, req, v); }
static int aud_proj_matrix(OpusProjectionEncoder *e, unsigned char *m, opus_int32 n) {
	return opus_projection_encoder_ctl(e, OPUS_PROJECTION_GET_DEMIXING_MATRIX(m, n));
}
*/
import "C"

import "unsafe"

func cbuf(b []byte) (*C.uchar, func()) {
	n := len(b)
	if n == 0 {
		n = 1
	}
	p := (*C.uchar)(C.malloc(C.size_t(n)))
	if len(b) > 0 {
		C.memcpy(unsafe.Pointer(p), unsafe.Pointer(&b[0]), C.size_t(len(b)))
	}
	return p, func() { C.free(unsafe.Pointer(p)) }
}

func gobytes(p *C.uchar, n int) []byte {
	if n <= 0 {
		return []byte{}
	}
	return C.GoBytes(unsafe.Pointer(p), C.int(n))
}

// Packet getters. len(data) must be >= 1 for the TOC-only getters.
func AudPacketGetBandwidth(data []byte) int {
	p, f := cbuf(data)
	defer f()
	return int(C.opus_packet_get_bandwidth(p))
}
func AudPacketGetSamplesPerFrame(data []byte, fs int) int {
	p, f := cbuf(data)
	defer f()
	return int(C.opus_packet_get_samples_per_frame(p, C.opus_int32(fs)))
}
func AudPacketGetNbChannels(data []byte) int {
	p, f := cbuf(data)
	defer f()
	return int(C.opus_packet_get_nb_channels(p))
}
func AudPacketGetNbFrames(data []byte) int {
	p, f := cbuf(data)
	defer f()
	return int(C.opus_packet_get_nb_frames(p, C.opus_int32(len(data))))
}
func AudPacketGetNbSamples(data []byte, fs int) int {
	p, f := cbuf(data)
	defer f()
	return int(C.opus_packet_get_nb_samples(p, C.opus_int32(len(data)), C.opus_int32(fs)))
}
func AudPacketHasLBRR(data []byte) int {
	p, f := cbuf(data)
	defer f()
	return int(C.opus_packet_has_lbrr(p, C.opus_int32(len(data))))
}
func AudDecoderGetNbSamples(fs, ch int, data []byte) int {
	var err C.int
	d := C.opus_decoder_create(C.opus_int32(fs), C.int(ch), &err)
	if d == nil {
		return -1000
	}
	defer C.opus_decoder_destroy(d)
	p, f := cbuf(data)
	defer f()
	return int(C.opus_decoder_get_nb_samples(d, p, C.opus_int32(len(data))))
}

// AudParseImpl wraps opus_packet_parse_impl and returns the padding bytes.
func AudParseImpl(data []byte, selfDelimited bool) (ret int, toc byte, sizes []int, payloadOffset, packetOffset int, padding []byte) {
	p, f := cbuf(data)
	defer f()
	var ctoc C.uchar
	var csize [48]C.opus_int16
	var poff C.int
	var pkoff C.opus_int32
	var pad *C.uchar
	var padLen C.opus_int32
	sd := 0
	if selfDelimited {
		sd = 1
	}
	ret = int(C.opus_packet_parse_impl(p, C.opus_int32(len(data)), C.int(sd), &ctoc, nil, &csize[0], &poff, &pkoff, &pad, &padLen))
	if ret < 0 {
		return
	}
	sizes = make([]int, ret)
	for i := range sizes {
		sizes[i] = int(csize[i])
	}
	if pad != nil && padLen > 0 {
		padding = C.GoBytes(unsafe.Pointer(pad), C.int(padLen))
	} else {
		padding = []byte{}
	}
	return ret, byte(ctoc), sizes, int(poff), int(pkoff), padding
}

// Repacketizer.
type AudRepacketizer struct {
	rp   *C.OpusRepacketizer
	bufs []func()
}

func NewAudRepacketizer() *AudRepacketizer {
	return &AudRepacketizer{rp: C.opus_repacketizer_create()}
}
func (r *AudRepacketizer) Cat(data []byte) int {
	p, f := cbuf(data)
	r.bufs = append(r.bufs, f)
	return int(C.opus_repacketizer_cat(r.rp, p, C.opus_int32(len(data))))
}
func (r *AudRepacketizer) NbFrames() int { return int(C.opus_repacketizer_get_nb_frames(r.rp)) }
func (r *AudRepacketizer) OutRange(b, e, maxlen int) ([]byte, int) {
	out := (*C.uchar)(C.malloc(C.size_t(maxlen + 1)))
	defer C.free(unsafe.Pointer(out))
	ret := int(C.opus_repacketizer_out_range(r.rp, C.int(b), C.int(e), out, C.opus_int32(maxlen)))
	return gobytes(out, ret), ret
}
func (r *AudRepacketizer) Out(maxlen int) ([]byte, int) {
	out := (*C.uchar)(C.malloc(C.size_t(maxlen + 1)))
	defer C.free(unsafe.Pointer(out))
	ret := int(C.opus_repacketizer_out(r.rp, out, C.opus_int32(maxlen)))
	return gobytes(out, ret), ret
}
func (r *AudRepacketizer) Init() {
	C.opus_repacketizer_init(r.rp)
	for _, f := range r.bufs {
		f()
	}
	r.bufs = nil
}
func (r *AudRepacketizer) Close() {
	r.Init()
	C.opus_repacketizer_destroy(r.rp)
}

func padBuf(data []byte, n int) (*C.uchar, func()) {
	if n < len(data) {
		n = len(data)
	}
	if n < 1 {
		n = 1
	}
	p := (*C.uchar)(C.malloc(C.size_t(n)))
	C.memset(unsafe.Pointer(p), 0xAA, C.size_t(n))
	if len(data) > 0 {
		C.memcpy(unsafe.Pointer(p), unsafe.Pointer(&data[0]), C.size_t(len(data)))
	}
	return p, func() { C.free(unsafe.Pointer(p)) }
}

// AudPad returns (output packet of newLen bytes, return code).
func AudPad(data []byte, newLen int) ([]byte, int) {
	p, f := padBuf(data, newLen)
	defer f()
	ret := int(C.opus_packet_pad(p, C.opus_int32(len(data)), C.opus_int32(newLen)))
	if ret != 0 {
		return nil, ret
	}
	return gobytes(p, newLen), ret
}
func AudUnpad(data []byte) ([]byte, int) {
	p, f := padBuf(data, len(data))
	defer f()
	ret := int(C.opus_packet_unpad(p, C.opus_int32(len(data))))
	if ret < 0 {
		return nil, ret
	}
	return gobytes(p, ret), ret
}
func AudMSPad(data []byte, newLen, streams int) ([]byte, int) {
	p, f := padBuf(data, newLen)
	defer f()
	ret := int(C.opus_multistream_packet_pad(p, C.opus_int32(len(data)), C.opus_int32(newLen), C.int(streams)))
	if ret != 0 {
		return nil, ret
	}
	return gobytes(p, newLen), ret
}
func AudMSUnpad(data []byte, streams int) ([]byte, int) {
	p, f := padBuf(data, len(data))
	defer f()
	ret := int(C.opus_multistream_packet_unpad(p, C.opus_int32(len(data)), C.int(streams)))
	if ret < 0 {
		return nil, ret
	}
	return gobytes(p, ret), ret
}

// AudSoftClip runs opus_pcm_soft_clip in place.
func AudSoftClip(pcm []float32, frameSize, channels int, mem []float32) {
	var pp, mp *C.float
	if len(pcm) > 0 {
		pp = (*C.float)(unsafe.Pointer(&pcm[0]))
	}
	if len(mem) > 0 {
		mp = (*C.float)(unsafe.Pointer(&mem[0]))
	}
	C.opus_pcm_soft_clip(pp, C.int(frameSize), C.int(channels), mp)
}

// Extensions.
type AudExt struct {
	ID, Frame int
	Data      []byte
}

func AudExtCount(padding []byte, nbFrames int) int {
	p, f := cbuf(padding)
	defer f()
	return int(C.opus_packet_extensions_count(p, C.opus_int32(len(padding)), C.int(nbFrames)))
}
func AudExtParse(padding []byte, nbFrames int) ([]AudExt, int) {
	p, f := cbuf(padding)
	defer f()
	const maxN = 4096
	arr := (*C.aud_ext)(C.malloc(C.size_t(maxN) * C.size_t(unsafe.Sizeof(C.aud_ext{}))))
	defer C.free(unsafe.Pointer(arr))
	n := C.opus_int32(maxN)
	ret := int(C.opus_packet_extensions_parse(p, C.opus_int32(len(padding)), arr, &n, C.int(nbFrames)))
	if ret < 0 {
		return nil, ret
	}
	s := unsafe.Slice(arr, int(n))
	out := make([]AudExt, int(n))
	for i := range out {
		out[i] = AudExt{ID: int(s[i].id), Frame: int(s[i].frame), Data: gobytes(s[i].data, int(s[i].len))}
	}
	return out, ret
}
func cexts(exts []AudExt) (*C.aud_ext, func()) {
	if len(exts) == 0 {
		return nil, func() {}
	}
	arr := (*C.aud_ext)(C.malloc(C.size_t(len(exts)) * C.size_t(unsafe.Sizeof(C.aud_ext{}))))
	s := unsafe.Slice(arr, len(exts))
	var frees []func()
	for i, e := range exts {
		s[i].id = C.int(e.ID)
		s[i].frame = C.int(e.Frame)
		s[i].len = C.opus_int32(len(e.Data))
		p, f := cbuf(e.Data)
		frees = append(frees, f)
		s[i].data = p
	}
	return arr, func() {
		for _, f := range frees {
			f()
		}
		C.free(unsafe.Pointer(arr))
	}
}
func AudExtGenerate(exts []AudExt, nbFrames, maxLen int, pad bool) ([]byte, int) {
	arr, f := cexts(exts)
	defer f()
	out := (*C.uchar)(C.malloc(C.size_t(maxLen + 1)))
	defer C.free(unsafe.Pointer(out))
	pd := 0
	if pad {
		pd = 1
	}
	ret := int(C.opus_packet_extensions_generate(out, C.opus_int32(maxLen), arr, C.opus_int32(len(exts)), C.int(nbFrames), C.int(pd)))
	return gobytes(out, ret), ret
}

// AudPadImpl wraps opus_packet_pad_impl (pad with extensions).
func AudPadImpl(data []byte, newLen int, pad bool, exts []AudExt) ([]byte, int) {
	arr, f := cexts(exts)
	defer f()
	p, f2 := padBuf(data, newLen)
	defer f2()
	pd := 0
	if pad {
		pd = 1
	}
	ret := int(C.opus_packet_pad_impl(p, C.opus_int32(len(data)), C.opus_int32(newLen), C.int(pd), arr, C.int(len(exts))))
	if ret <= 0 {
		return nil, ret
	}
	return gobytes(p, ret), ret
}

// Encoder CTL access.
type AudEncoder struct{ e *C.OpusEncoder }

func NewAudEncoder(fs, ch, app int) (*AudEncoder, int) {
	var err C.int
	e := C.opus_encoder_create(C.opus_int32(fs), C.int(ch), C.int(app), &err)
	if e == nil {
		return nil, int(err)
	}
	return &AudEncoder{e}, int(err)
}
func (a *AudEncoder) Set(req, v int) int { return int(C.aud_enc_set(a.e, C.int(req), C.int(v))) }
func (a *AudEncoder) Get(req int) (int, int) {
	var v C.opus_int32
	r := C.aud_enc_get(a.e, C.int(req), &v)
	return int(v), int(r)
}
func (a *AudEncoder) GetU(req int) (uint32, int) {
	var v C.opus_int32
	r := C.aud_enc_get(a.e, C.int(req), &v)
	return uint32(v), int(r)
}
func (a *AudEncoder) Reset() int { return int(C.aud_enc_reset(a.e)) }
func (a *AudEncoder) EncodeFloat(pcm []float32, frameSize, maxBytes int) ([]byte, int) {
	var pp *C.float
	if len(pcm) > 0 {
		pp = (*C.float)(unsafe.Pointer(&pcm[0]))
	}
	out := (*C.uchar)(C.malloc(C.size_t(maxBytes + 1)))
	defer C.free(unsafe.Pointer(out))
	ret := int(C.opus_encode_float(a.e, pp, C.int(frameSize), out, C.opus_int32(maxBytes)))
	return gobytes(out, ret), ret
}
func (a *AudEncoder) Close() { C.opus_encoder_destroy(a.e) }

// Decoder CTL access.
type AudDecoder struct{ d *C.OpusDecoder }

func NewAudDecoder(fs, ch int) (*AudDecoder, int) {
	var err C.int
	d := C.opus_decoder_create(C.opus_int32(fs), C.int(ch), &err)
	if d == nil {
		return nil, int(err)
	}
	return &AudDecoder{d}, int(err)
}
func (a *AudDecoder) Set(req, v int) int { return int(C.aud_dec_set(a.d, C.int(req), C.int(v))) }
func (a *AudDecoder) Get(req int) (int, int) {
	var v C.opus_int32
	r := C.aud_dec_get(a.d, C.int(req), &v)
	return int(v), int(r)
}
func (a *AudDecoder) Reset() int { return int(C.aud_dec_reset(a.d)) }

// Decode decodes into a buffer of frameSize samples per channel (float).
func (a *AudDecoder) Decode(packet []byte, frameSize, channels int, fec bool) ([]float32, int) {
	buf := make([]float32, max(frameSize*channels, 1))
	var pp *C.uchar
	var f func()
	if packet != nil {
		pp, f = cbuf(packet)
		defer f()
	}
	fc := 0
	if fec {
		fc = 1
	}
	ret := int(C.opus_decode_float(a.d, pp, C.opus_int32(len(packet)), (*C.float)(unsafe.Pointer(&buf[0])), C.int(frameSize), C.int(fc)))
	if ret < 0 {
		return nil, ret
	}
	return buf[:ret*channels], ret
}
func (a *AudDecoder) Close() { C.opus_decoder_destroy(a.d) }

// Multistream.
type AudMSEncoder struct{ e *C.OpusMSEncoder }

func cmap(m []byte) (*C.uchar, func()) { return cbuf(m) }

func NewAudMSEncoder(fs, ch, streams, coupled int, mapping []byte, app int) (*AudMSEncoder, int) {
	var err C.int
	mp, f := cmap(mapping)
	defer f()
	e := C.opus_multistream_encoder_create(C.opus_int32(fs), C.int(ch), C.int(streams), C.int(coupled), mp, C.int(app), &err)
	if e == nil {
		return nil, int(err)
	}
	return &AudMSEncoder{e}, int(err)
}
func NewAudSurroundEncoder(fs, ch, family, app int) (*AudMSEncoder, int, int, []byte, int) {
	var err C.int
	var streams, coupled C.int
	mapping := make([]byte, 256)
	e := C.opus_multistream_surround_encoder_create(C.opus_int32(fs), C.int(ch), C.int(family), &streams, &coupled,
		(*C.uchar)(unsafe.Pointer(&mapping[0])), C.int(app), &err)
	if e == nil {
		return nil, 0, 0, nil, int(err)
	}
	return &AudMSEncoder{e}, int(streams), int(coupled), mapping[:ch], int(err)
}
func (a *AudMSEncoder) Set(req, v int) int { return int(C.aud_msenc_set(a.e, C.int(req), C.int(v))) }
func (a *AudMSEncoder) Get(req int) (int, int) {
	var v C.opus_int32
	r := C.aud_msenc_get(a.e, C.int(req), &v)
	return int(v), int(r)
}
func (a *AudMSEncoder) Close() { C.opus_multistream_encoder_destroy(a.e) }
func (a *AudMSEncoder) EncodeFloat(pcm []float32, frameSize, maxBytes int) ([]byte, int) {
	out := (*C.uchar)(C.malloc(C.size_t(maxBytes + 1)))
	defer C.free(unsafe.Pointer(out))
	ret := int(C.opus_multistream_encode_float(a.e, (*C.float)(unsafe.Pointer(&pcm[0])), C.int(frameSize), out, C.opus_int32(maxBytes)))
	return gobytes(out, ret), ret
}

type AudMSDecoder struct{ d *C.OpusMSDecoder }

func NewAudMSDecoder(fs, ch, streams, coupled int, mapping []byte) (*AudMSDecoder, int) {
	var err C.int
	mp, f := cmap(mapping)
	defer f()
	d := C.opus_multistream_decoder_create(C.opus_int32(fs), C.int(ch), C.int(streams), C.int(coupled), mp, &err)
	if d == nil {
		return nil, int(err)
	}
	return &AudMSDecoder{d}, int(err)
}
func (a *AudMSDecoder) Set(req, v int) int { return int(C.aud_msdec_set(a.d, C.int(req), C.int(v))) }
func (a *AudMSDecoder) Get(req int) (int, int) {
	var v C.opus_int32
	r := C.aud_msdec_get(a.d, C.int(req), &v)
	return int(v), int(r)
}
func (a *AudMSDecoder) Close() { C.opus_multistream_decoder_destroy(a.d) }

// AudAmbisonics creates a projection encoder and returns its geometry/matrix.
func AudAmbisonics(fs, ch, family, app int) (streams, coupled, gain int, matrix []byte, err int) {
	var cerr C.int
	var s, c C.int
	e := C.opus_projection_ambisonics_encoder_create(C.opus_int32(fs), C.int(ch), C.int(family), &s, &c, C.int(app), &cerr)
	if e == nil {
		return 0, 0, 0, nil, int(cerr)
	}
	defer C.opus_projection_encoder_destroy(e)
	var g, size C.opus_int32
	C.aud_proj_get(e, C.OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN_REQUEST, &g)
	C.aud_proj_get(e, C.OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE_REQUEST, &size)
	m := (*C.uchar)(C.malloc(C.size_t(size + 1)))
	defer C.free(unsafe.Pointer(m))
	C.aud_proj_matrix(e, m, size)
	return int(s), int(c), int(g), gobytes(m, int(size)), int(cerr)
}

func AudProjectionDecoderCreate(fs, ch, streams, coupled int, matrix []byte) int {
	var err C.int
	mp, f := cbuf(matrix)
	defer f()
	d := C.opus_projection_decoder_create(C.opus_int32(fs), C.int(ch), C.int(streams), C.int(coupled), mp, C.opus_int32(len(matrix)), &err)
	if d != nil {
		C.opus_projection_decoder_destroy(d)
	}
	return int(err)
}

func AudStrerror(code int) string { return C.GoString(C.opus_strerror(C.int(code))) }
