/* Ground-truth Opus decode tracer using libopus internals.
   Usage: oracle <testvectorNN.bit> [pktIndex]
   Parses opus_demo .bit format (BE u32 size, BE u32 final-range, payload),
   decodes through the given packet, and prints the libopus final range. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus_defines.h"
#include "opus.h"

int oracle_trace_enabled = 0;

static int frame_samples_48k(const unsigned char *pkt, int len)
{
    int s = opus_packet_get_samples_per_frame(pkt, 48000);
    int n = opus_packet_get_nb_frames(pkt, len);
    if (n < 1) n = 1;
    return s * n;
}

int main(int argc, char **argv)
{
    if (argc < 2) { fprintf(stderr, "usage: %s file.bit [pktIndex]\n", argv[0]); return 2; }
    int want = (argc >= 3) ? atoi(argv[2]) : 0;

    FILE *f = fopen(argv[1], "rb");
    if (!f) { fprintf(stderr, "open fail\n"); return 2; }
    fseek(f, 0, SEEK_END); long sz = ftell(f); fseek(f, 0, SEEK_SET);
    unsigned char *buf = malloc(sz);
    fread(buf, 1, sz, f); fclose(f);

    long off = 0; int idx = 0;
    while (off + 8 <= sz) {
        unsigned int psize = (buf[off]<<24)|(buf[off+1]<<16)|(buf[off+2]<<8)|buf[off+3];
        unsigned int rexp  = (buf[off+4]<<24)|(buf[off+5]<<16)|(buf[off+6]<<8)|buf[off+7];
        unsigned char *pkt = buf + off + 8;
        if (off + 8 + psize > sz) break;
        if (idx == want) {
            unsigned char toc = pkt[0];
            int config = (toc >> 3) & 0x1f;
            int stereo = (toc >> 2) & 1;
            int code = toc & 3;
            const char *mode = config < 12 ? "SILK" : (config < 16 ? "HYBRID" : "CELT");
            fprintf(stderr, "pkt%d: TOC=0x%02x config=%d stereo=%d code=%d size=%u rexp=%08x\n",
                    idx, toc, config, stereo, code, psize, rexp);
            fprintf(stderr, "mode=%s\n", mode);
            int err = OPUS_OK;
            int api_channels = 2;
            OpusDecoder *dec = opus_decoder_create(48000, api_channels, &err);
            if (dec == NULL || err != OPUS_OK) {
                fprintf(stderr, "opus_decoder_create failed: %d\n", err);
                return 2;
            }
            float pcm[5760*2];
            long off2 = 0;
            int idx2 = 0;
            int ret = 0;
            while (off2 + 8 <= sz && idx2 <= want) {
                unsigned int psize2 = (buf[off2]<<24)|(buf[off2+1]<<16)|(buf[off2+2]<<8)|buf[off2+3];
                unsigned char *pkt2 = buf + off2 + 8;
                if (off2 + 8 + psize2 > sz) break;
                oracle_trace_enabled = idx2 == want;
                ret = opus_decode_float(dec, pkt2, psize2, pcm, 5760, 0);
                if (ret < 0) {
                    fprintf(stderr, "decode pkt%d failed: %d\n", idx2, ret);
                    opus_decoder_destroy(dec);
                    return 1;
                }
                off2 += 8 + psize2;
                idx2++;
            }
            oracle_trace_enabled = 0;
            unsigned int rng = 0;
            opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&rng));
            fprintf(stderr, "RESULT ret=%d samples_expected=%d finalrng=%08x expected=%08x match=%d\n",
                    ret, frame_samples_48k(pkt, (int)psize), rng, rexp, rng == rexp);
            fprintf(stderr, "pcm[0..7]:");
            for (int i = 0; i < 8 && i < ret*api_channels; i++) fprintf(stderr, " %.5f", pcm[i]);
            fprintf(stderr, "\n");
            opus_decoder_destroy(dec);
            return 0;
        }
        off += 8 + psize;
        idx++;
    }
    fprintf(stderr, "packet %d not found (have %d)\n", want, idx);
    return 1;
}
