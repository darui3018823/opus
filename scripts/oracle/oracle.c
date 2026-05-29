/* Ground-truth CELT decode tracer using libopus internals.
   Usage: oracle <testvectorNN.bit> [pktIndex]
   Parses opus_demo .bit format (BE u32 size, BE u32 final-range, payload),
   decodes the given packet's CELT payload, and prints the libopus final range. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus_defines.h"
#include "celt.h"

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
            int C = stereo ? 2 : 1;
            fprintf(stderr, "pkt%d: TOC=0x%02x config=%d stereo=%d code=%d size=%u rexp=%08x\n",
                    idx, toc, config, stereo, code, psize, rexp);
            if (config < 16) { fprintf(stderr, "not CELT-only; skipping\n"); return 1; }

            int err = celt_decoder_get_size(C);
            CELTDecoder *dec = malloc(err);
            celt_decoder_init(dec, 48000, C);

            float pcm[5760*2];
            /* code 0: single frame, payload = pkt+1 .. psize-1 */
            int ret = celt_decode_with_ec(dec, pkt+1, psize-1, pcm, 960, NULL, 0);
            unsigned int rng = 0;
            celt_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&rng));
            fprintf(stderr, "RESULT ret=%d finalrng=%08x expected=%08x match=%d\n",
                    ret, rng, rexp, rng == rexp);
            fprintf(stderr, "pcm[0..7]:");
            for (int i = 0; i < 8 && i < ret*C; i++) fprintf(stderr, " %.5f", pcm[i]);
            fprintf(stderr, "\n");
            free(dec);
            return 0;
        }
        off += 8 + psize;
        idx++;
    }
    fprintf(stderr, "packet %d not found (have %d)\n", want, idx);
    return 1;
}
