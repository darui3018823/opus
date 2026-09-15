/* libopus 1.6.1 encoder input-pipeline oracle (built by build_encoder.ps1).
   Usage: enc_oracle --silk-enc <rate> <fixture> [targetFrame|-1] [frames] [bitrate] [bandwidth]
   Runs opus_encode_float with the SILK AB settings (VOIP, complexity 5,
   constrained VBR, voice, SILK-only forced) on the deterministic AB fixtures
   and traces the instrumented encoder stages for the target frame (or every
   frame when targetFrame is negative). */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>
#include "opus_defines.h"
#include "opus.h"
#include "opus_private.h"

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

int oracle_trace_enabled = 0;

static double harmonic_sample(double tm, double f0, double amp)
{
    return amp * (0.72 * sin(2.0 * M_PI * f0 * tm) +
        0.22 * sin(2.0 * M_PI * 2.0 * f0 * tm + 0.3) +
        0.09 * sin(2.0 * M_PI * 3.0 * f0 * tm + 0.7));
}

static unsigned int lcg_next(unsigned int *state)
{
    *state = *state * 1664525u + 1013904223u;
    return *state;
}

static float noise_sample(unsigned int *state, double *prev)
{
    double white = ((double)(lcg_next(state) >> 8) / 16777215.0) * 2.0 - 1.0;
    double y = 0.28 * white - 0.18 * *prev;
    *prev = white;
    return (float)y;
}

static void fill_silk_fixture(float *pcm, int rate, int frame_size, int frame, const char *fixture)
{
    int i;
    int start = frame * frame_size;
    if (strcmp(fixture, "silence") == 0) {
        memset(pcm, 0, (size_t)frame_size * sizeof(*pcm));
        return;
    }
    if (strcmp(fixture, "unvoiced-noise") == 0) {
        unsigned int state = 0x61515u + (unsigned int)(start / rate);
        double prev = 0.0;
        for (i = 0; i < frame_size; i++) pcm[i] = noise_sample(&state, &prev);
        return;
    }
    if (strcmp(fixture, "steady-voiced") == 0) {
        for (i = 0; i < frame_size; i++) {
            double tm = (double)(start + i) / (double)rate;
            pcm[i] = (float)harmonic_sample(tm, 180.0, 0.20);
        }
        return;
    }
    if (strcmp(fixture, "speech-like-harmonic") == 0) {
        for (i = 0; i < frame_size; i++) {
            double tm = (double)(start + i) / (double)rate;
            double f0 = 145.0 + 24.0 * sin(2.0 * M_PI * 1.7 * tm);
            double env = 0.18 + 0.10 * sin(2.0 * M_PI * 3.1 * tm + 0.2);
            pcm[i] = (float)(env * (0.58 * sin(2.0 * M_PI * f0 * tm) +
                0.24 * sin(2.0 * M_PI * 2.0 * f0 * tm + 0.35) +
                0.11 * sin(2.0 * M_PI * 3.0 * f0 * tm + 0.85)));
        }
        return;
    }
    if (strcmp(fixture, "onset") == 0) {
        for (i = 0; i < frame_size; i++) {
            int global = start + i;
            double tm = (double)global / (double)rate;
            double y = harmonic_sample(tm, 220.0, 0.20);
            if (global < 2 * frame_size) y = 0.0;
            else if (global < 3 * frame_size) y *= (double)(global - 2 * frame_size) / (double)frame_size;
            pcm[i] = (float)y;
        }
        return;
    }
    fprintf(stderr, "unknown fixture %s\n", fixture);
    exit(2);
}

static int parse_bandwidth(const char *s)
{
    if (strcmp(s, "auto") == 0) return OPUS_AUTO;
    if (strcmp(s, "nb") == 0) return OPUS_BANDWIDTH_NARROWBAND;
    if (strcmp(s, "mb") == 0) return OPUS_BANDWIDTH_MEDIUMBAND;
    if (strcmp(s, "wb") == 0) return OPUS_BANDWIDTH_WIDEBAND;
    if (strcmp(s, "swb") == 0) return OPUS_BANDWIDTH_SUPERWIDEBAND;
    if (strcmp(s, "fb") == 0) return OPUS_BANDWIDTH_FULLBAND;
    return atoi(s);
}

static int run_silk_encoder_oracle(int argc, char **argv)
{
    int rate, target, frames, bitrate, bandwidth, frame_size, err, frame;
    const char *fixture;
    OpusEncoder *enc;
    float pcm[960 * 5];
    unsigned char packet[1500];

    if (argc < 4) {
        fprintf(stderr, "usage: %s --silk-enc <rate> <fixture> [targetFrame] [frames] [bitrate] [bandwidth]\n", argv[0]);
        fprintf(stderr, "fixtures: silence, unvoiced-noise, steady-voiced, speech-like-harmonic, onset\n");
        return 2;
    }
    rate = atoi(argv[2]);
    fixture = argv[3];
    target = (argc >= 5) ? atoi(argv[4]) : 0;
    frames = (argc >= 6) ? atoi(argv[5]) : 12;
    bitrate = (argc >= 7) ? atoi(argv[6]) : 24000;
    bandwidth = (argc >= 8) ? parse_bandwidth(argv[7]) : OPUS_AUTO;
    frame_size = rate / 50;
    if (rate != 8000 && rate != 12000 && rate != 16000 && rate != 24000 && rate != 48000) {
        fprintf(stderr, "--silk-enc rate must be 8000, 12000, 16000, 24000 or 48000\n");
        return 2;
    }
    if (target >= frames) {
        fprintf(stderr, "targetFrame %d outside frame count %d\n", target, frames);
        return 2;
    }

    enc = opus_encoder_create(rate, 1, OPUS_APPLICATION_VOIP, &err);
    if (enc == NULL || err != OPUS_OK) {
        fprintf(stderr, "opus_encoder_create failed: %d\n", err);
        return 2;
    }
    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(5));
    opus_encoder_ctl(enc, OPUS_SET_VBR(1));
    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(1));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
    if (bandwidth != OPUS_AUTO) opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth));
    opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_SILK_ONLY));

    fprintf(stderr, "SILK_ENC_ORACLE rate=%d frame_size=%d fixture=%s target=%d frames=%d bitrate=%d bandwidth=%d\n",
            rate, frame_size, fixture, target, frames, bitrate, bandwidth);
    for (frame = 0; frame < frames; frame++) {
        int n, config;
        fill_silk_fixture(pcm, rate, frame_size, frame, fixture);
        oracle_trace_enabled = target < 0 || frame == target;
        if (oracle_trace_enabled) {
            fprintf(stderr, "[SILK_ENC_FRAME] frame=%d rate=%d frame_size=%d fixture=%s\n",
                    frame, rate, frame_size, fixture);
        }
        n = opus_encode_float(enc, pcm, frame_size, packet, (opus_int32)sizeof(packet));
        if (n < 0) {
            fprintf(stderr, "encode frame %d failed: %d\n", frame, n);
            opus_encoder_destroy(enc);
            return 1;
        }
        config = (packet[0] >> 3) & 0x1f;
        fprintf(stderr, "ENC_RESULT frame=%d bytes=%d toc=0x%02x config=%d traced=%d\n",
                frame, n, packet[0], config, oracle_trace_enabled);
        if (oracle_trace_enabled) {
            int b;
            fprintf(stderr, "[ENC_PACKET] n=%d", n);
            for (b = 0; b < n; b++) fprintf(stderr, " %02x", packet[b]);
            fprintf(stderr, "\n");
        }
    }
    oracle_trace_enabled = 0;
    opus_encoder_destroy(enc);
    return 0;
}

int main(int argc, char **argv)
{
    if (argc < 2 || strcmp(argv[1], "--silk-enc") != 0) {
        fprintf(stderr, "usage: %s --silk-enc <rate> <fixture> [targetFrame|-1] [frames] [bitrate] [bandwidth]\n", argv[0]);
        return 2;
    }
    return run_silk_encoder_oracle(argc, argv);
}
