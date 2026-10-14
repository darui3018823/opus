/* Audit-only scripted libopus 1.6.1 (plain C float) decoder/encoder oracle.
 *
 *   dec_oracle dec <script.bin> <out.bin>
 *   dec_oracle enc <script.bin> <out.bin>
 *
 * All integers little-endian.
 * dec script: u32 kind(0 single,1 ms,2 proj); i32 rate, ch, streams, coupled, maplen; u8 map[maplen]
 *   ops: u8 op
 *     1 decode: u8 fmt(0 i16,1 f32,2 i24) u8 fec i32 frame_size i32 len(-1 = NULL) u8 data[len]
 *     2 reset
 *     3 set gain i32
 *     4 set phase inversion disabled i32
 *     5 get nb samples of packet: i32 len data  -> out i32
 * dec out per decode: i32 ret u32 rng i32 lastdur i32 bw i32 pitch ; samples (ret*ch values)
 *          per ctl:    i32 ret
 * enc script: u32 kind(0 single,1 surround ms,2 ambisonics projection, 3 ambisonics ms);
 *   i32 rate ch app family bitrate complexity fec losspct vbr cvbr bandwidth dtx forcech signal frame_size nframes
 *   then nframes*frame_size*ch f32 samples.
 * enc out: i32 streams coupled maplen u8 map[maplen]; per frame i32 n, bytes.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus.h"
#include "opus_multistream.h"
#include "opus_projection.h"

static unsigned char *buf; static long bufLen, pos;
static int rd_i32(void) { int v; memcpy(&v, buf + pos, 4); pos += 4; return v; }
static unsigned rd_u8(void) { return buf[pos++]; }
static FILE *out;
static void wr_i32(int v) { fwrite(&v, 4, 1, out); }

static int load(const char *p)
{
    FILE *f = fopen(p, "rb");
    if (!f) return 0;
    fseek(f, 0, SEEK_END); bufLen = ftell(f); fseek(f, 0, SEEK_SET);
    buf = malloc(bufLen + 1);
    if (fread(buf, 1, bufLen, f) != (size_t)bufLen) return 0;
    fclose(f);
    return 1;
}

#define MAXCH 255
static opus_int16 s16[5760 * 2 * MAXCH];
static opus_int32 s24[5760 * 2 * MAXCH];
static float sf[5760 * 2 * MAXCH];

static int run_dec(void)
{
    int kind = rd_i32(), rate = rd_i32(), ch = rd_i32(), streams = rd_i32(), coupled = rd_i32(), maplen = rd_i32();
    unsigned char *map = buf + pos; int err = 0;
    OpusDecoder *d = NULL; OpusMSDecoder *ms = NULL; OpusProjectionDecoder *pj = NULL;
    pos += maplen;
    if (kind == 0) d = opus_decoder_create(rate, ch, &err);
    else if (kind == 1) ms = opus_multistream_decoder_create(rate, ch, streams, coupled, map, &err);
    else pj = opus_projection_decoder_create(rate, ch, streams, coupled, map, maplen, &err);
    wr_i32(err);
    if (err != OPUS_OK) return 0;
#define CTL(req) (d ? opus_decoder_ctl(d, req) : ms ? opus_multistream_decoder_ctl(ms, req) : opus_projection_decoder_ctl(pj, req))
    while (pos < bufLen) {
        int op = rd_u8();
        if (op == 1) {
            int fmt = rd_u8(), fec = rd_u8(), fs = rd_i32(), len = rd_i32(), ret, i;
            const unsigned char *data = len < 0 ? NULL : buf + pos;
            opus_uint32 rng = 0; int lastdur = 0, bw = 0, pitch = 0;
            if (len < 0) len = 0; else pos += len;
            if (fmt == 0) ret = d ? opus_decode(d, data, len, s16, fs, fec) : ms ? opus_multistream_decode(ms, data, len, s16, fs, fec) : opus_projection_decode(pj, data, len, s16, fs, fec);
            else if (fmt == 1) ret = d ? opus_decode_float(d, data, len, sf, fs, fec) : ms ? opus_multistream_decode_float(ms, data, len, sf, fs, fec) : opus_projection_decode_float(pj, data, len, sf, fs, fec);
            else ret = d ? opus_decode24(d, data, len, s24, fs, fec) : ms ? opus_multistream_decode24(ms, data, len, s24, fs, fec) : opus_projection_decode24(pj, data, len, s24, fs, fec);
            CTL(OPUS_GET_FINAL_RANGE(&rng));
            CTL(OPUS_GET_LAST_PACKET_DURATION(&lastdur));
            CTL(OPUS_GET_BANDWIDTH(&bw));
            if (d) opus_decoder_ctl(d, OPUS_GET_PITCH(&pitch));
            wr_i32(ret); wr_i32((int)rng); wr_i32(lastdur); wr_i32(bw); wr_i32(pitch);
            if (ret > 0) {
                int n = ret * ch;
                if (fmt == 0) fwrite(s16, 2, n, out);
                else if (fmt == 1) fwrite(sf, 4, n, out);
                else fwrite(s24, 4, n, out);
            }
            (void)i;
        } else if (op == 2) {
            wr_i32(CTL(OPUS_RESET_STATE));
        } else if (op == 3) {
            int g = rd_i32();
            wr_i32(CTL(OPUS_SET_GAIN(g)));
        } else if (op == 4) {
            int v = rd_i32();
            wr_i32(CTL(OPUS_SET_PHASE_INVERSION_DISABLED(v)));
        } else if (op == 5) {
            int len = rd_i32();
            wr_i32(opus_packet_get_nb_samples(buf + pos, len, rate));
            pos += len;
        } else {
            fprintf(stderr, "bad op %d at %ld\n", op, pos);
            return 1;
        }
    }
    return 0;
}

static int run_enc(void)
{
    int kind = rd_i32(), rate = rd_i32(), ch = rd_i32(), app = rd_i32(), family = rd_i32();
    int bitrate = rd_i32(), complexity = rd_i32(), fec = rd_i32(), loss = rd_i32(), vbr = rd_i32(), cvbr = rd_i32();
    int bandwidth = rd_i32(), dtx = rd_i32(), forcech = rd_i32(), signal = rd_i32(), fs = rd_i32(), nframes = rd_i32();
    int err = 0, streams = 1, coupled = ch == 2, f;
    unsigned char mapping[256]; int maplen = 0;
    static unsigned char pkt[1275 * 3 * 255];
    OpusEncoder *e = NULL; OpusMSEncoder *ms = NULL; OpusProjectionEncoder *pj = NULL;
    if (kind == 0) e = opus_encoder_create(rate, ch, app, &err);
    else if (kind == 1 || kind == 3) ms = opus_multistream_surround_encoder_create(rate, ch, family, &streams, &coupled, mapping, app, &err);
    else pj = opus_projection_ambisonics_encoder_create(rate, ch, family, &streams, &coupled, app, &err);
    if (err != OPUS_OK) { fprintf(stderr, "enc create %d\n", err); return 1; }
#define ECTL(req) (e ? opus_encoder_ctl(e, req) : ms ? opus_multistream_encoder_ctl(ms, req) : opus_projection_encoder_ctl(pj, req))
    if (bitrate) ECTL(OPUS_SET_BITRATE(bitrate));
    ECTL(OPUS_SET_COMPLEXITY(complexity));
    ECTL(OPUS_SET_INBAND_FEC(fec));
    ECTL(OPUS_SET_PACKET_LOSS_PERC(loss));
    ECTL(OPUS_SET_VBR(vbr));
    ECTL(OPUS_SET_VBR_CONSTRAINT(cvbr));
    if (bandwidth) ECTL(OPUS_SET_BANDWIDTH(bandwidth));
    ECTL(OPUS_SET_DTX(dtx));
    if (forcech) ECTL(OPUS_SET_FORCE_CHANNELS(forcech));
    if (signal) ECTL(OPUS_SET_SIGNAL(signal));
    if (kind == 0) { maplen = 0; }
    else if (ms) { maplen = ch; }
    else {
        int sz = 0;
        opus_projection_encoder_ctl(pj, OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE(&sz));
        maplen = sz;
    }
    {
        static unsigned char big[65536];
        if (pj) { opus_projection_encoder_ctl(pj, OPUS_PROJECTION_GET_DEMIXING_MATRIX(big, maplen)); }
        wr_i32(streams); wr_i32(coupled); wr_i32(maplen);
        fwrite(pj ? big : mapping, 1, maplen, out);
    }
    for (f = 0; f < nframes; f++) {
        float *pcm = (float *)(buf + pos);
        int n;
        pos += (long)fs * ch * 4;
        n = e ? opus_encode_float(e, pcm, fs, pkt, 1275 * 3) : ms ? opus_multistream_encode_float(ms, pcm, fs, pkt, sizeof(pkt)) : opus_projection_encode_float(pj, pcm, fs, pkt, sizeof(pkt));
        wr_i32(n);
        if (n > 0) fwrite(pkt, 1, n, out);
    }
    return 0;
}

int main(int argc, char **argv)
{
    int r;
    if (argc < 4 || !load(argv[2])) { fprintf(stderr, "usage: dec_oracle dec|enc script out\n"); return 2; }
    out = fopen(argv[3], "wb");
    if (!out) return 2;
    r = strcmp(argv[1], "dec") == 0 ? run_dec() : run_enc();
    fclose(out);
    return r;
}
