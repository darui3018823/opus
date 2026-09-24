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
#include "opus_multistream.h"
#include "opus_projection.h"

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

/* ref-speech-gaps (refSpeechGapsSample in opus_dtx_oracle_test.go): a 2.4 s
   cycle of ref-speech (0-0.4 s), digital silence (0.4-0.9 s), noise at
   about -60 dBFS (0.9-1.4 s), noise of a few LSBs (1.4-2.3 s) and ref-speech
   again (2.3-2.4 s), for the DTX paths. The noise is a hash of the sample
   index, so any frame size sees the same signal. */
static int gaps_segment(long idx, int rate)
{
    long period = (long)rate * 12 / 5;
    long pos = idx % period;
    if (pos < (long)rate * 2 / 5) return 0;
    if (pos < (long)rate * 9 / 10) return 1;
    if (pos < (long)rate * 7 / 5) return 2;
    if (pos < (long)rate * 23 / 10) return 3;
    return 0;
}

static double gaps_noise(long idx, int ch, int seg)
{
    unsigned int h = (unsigned int)idx * 2654435761u + (unsigned int)ch * 40503u + 12345u;
    h ^= h >> 15;
    h *= 2246822519u;
    h ^= h >> 13;
    return ((double)(h & 0xffff) / 65536.0 - 0.5) * (seg == 2 ? 0.002 : 0.0002);
}

static void fill_silk_fixture_stereo(float *pcm, int rate, int frame_size, int frame, const char *fixture)
{
    int i;
    int start = frame * frame_size;
    if (strcmp(fixture, "ref-speech-gaps") == 0) {
        for (i = 0; i < frame_size; i++) {
            long idx = (long)start + i;
            double tm = (double)idx / (double)rate;
            int seg = gaps_segment(idx, rate);
            if (seg == 1) {
                pcm[2 * i] = pcm[2 * i + 1] = 0.0f;
            } else if (seg >= 2) {
                pcm[2 * i] = (float)gaps_noise(idx, 0, seg);
                pcm[2 * i + 1] = (float)gaps_noise(idx, 1, seg);
            } else {
                double env = 0.55 + 0.35 * sin(2.0 * M_PI * 3.0 * tm);
                double s = 0.32 * sin(2.0 * M_PI * 180.0 * tm) +
                    0.12 * sin(2.0 * M_PI * 360.0 * tm + 0.4) +
                    0.06 * sin(2.0 * M_PI * 720.0 * tm + 0.9) +
                    0.025 * sin(2.0 * M_PI * 1100.0 * tm + 1.7);
                double r = 0.30 * sin(2.0 * M_PI * 185.0 * tm + 0.2) +
                    0.10 * sin(2.0 * M_PI * 370.0 * tm + 0.7) +
                    0.05 * sin(2.0 * M_PI * 740.0 * tm + 1.1);
                pcm[2 * i] = (float)(env * s);
                pcm[2 * i + 1] = (float)(env * r);
            }
        }
        return;
    }
    if (strcmp(fixture, "ref-speech") == 0) {
        /* silkRefSpeechFrame (opus_cgo_silk_encode_test.go), channels = 2. */
        for (i = 0; i < frame_size; i++) {
            double tm = (double)(start + i) / (double)rate;
            double env = 0.55 + 0.35 * sin(2.0 * M_PI * 3.0 * tm);
            double s = 0.32 * sin(2.0 * M_PI * 180.0 * tm) +
                0.12 * sin(2.0 * M_PI * 360.0 * tm + 0.4) +
                0.06 * sin(2.0 * M_PI * 720.0 * tm + 0.9) +
                0.025 * sin(2.0 * M_PI * 1100.0 * tm + 1.7);
            double r = 0.30 * sin(2.0 * M_PI * 185.0 * tm + 0.2) +
                0.10 * sin(2.0 * M_PI * 370.0 * tm + 0.7) +
                0.05 * sin(2.0 * M_PI * 740.0 * tm + 1.1);
            pcm[2 * i] = (float)(env * s);
            pcm[2 * i + 1] = (float)(env * r);
        }
        return;
    }
    fprintf(stderr, "unknown stereo fixture %s\n", fixture);
    exit(2);
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
    if (strcmp(fixture, "ref-speech") == 0) {
        /* silkRefSpeechFrame (opus_cgo_silk_encode_test.go), mono. */
        for (i = 0; i < frame_size; i++) {
            double tm = (double)(start + i) / (double)rate;
            double env = 0.55 + 0.35 * sin(2.0 * M_PI * 3.0 * tm);
            double s = 0.32 * sin(2.0 * M_PI * 180.0 * tm) +
                0.12 * sin(2.0 * M_PI * 360.0 * tm + 0.4) +
                0.06 * sin(2.0 * M_PI * 720.0 * tm + 0.9) +
                0.025 * sin(2.0 * M_PI * 1100.0 * tm + 1.7);
            pcm[i] = (float)(env * s);
        }
        return;
    }
    if (strcmp(fixture, "ref-speech-gaps") == 0) {
        for (i = 0; i < frame_size; i++) {
            long idx = (long)start + i;
            double tm = (double)idx / (double)rate;
            int seg = gaps_segment(idx, rate);
            if (seg == 1) {
                pcm[i] = 0.0f;
            } else if (seg >= 2) {
                pcm[i] = (float)gaps_noise(idx, 0, seg);
            } else {
                double env = 0.55 + 0.35 * sin(2.0 * M_PI * 3.0 * tm);
                double s = 0.32 * sin(2.0 * M_PI * 180.0 * tm) +
                    0.12 * sin(2.0 * M_PI * 360.0 * tm + 0.4) +
                    0.06 * sin(2.0 * M_PI * 720.0 * tm + 0.9) +
                    0.025 * sin(2.0 * M_PI * 1100.0 * tm + 1.7);
                pcm[i] = (float)(env * s);
            }
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
    int rate, target, frames, bitrate, bandwidth, frame_size, err, frame, lossPerc, channels, vbr, complexity;
    const char *fixture;
    OpusEncoder *enc;
    float pcm[960 * 5 * 2];
    unsigned char packet[1500];

    if (argc < 4) {
        fprintf(stderr, "usage: %s --silk-enc <rate> <fixture> [targetFrame] [frames] [bitrate] [bandwidth]\n", argv[0]);
        fprintf(stderr, "fixtures: silence, unvoiced-noise, steady-voiced, speech-like-harmonic, onset, ref-speech\n");
        return 2;
    }
    rate = atoi(argv[2]);
    fixture = argv[3];
    target = (argc >= 5) ? atoi(argv[4]) : 0;
    frames = (argc >= 6) ? atoi(argv[5]) : 12;
    bitrate = (argc >= 7) ? atoi(argv[6]) : 24000;
    bandwidth = (argc >= 8) ? parse_bandwidth(argv[7]) : OPUS_AUTO;
    lossPerc = (argc >= 9) ? atoi(argv[8]) : 0; /* > 0 enables in-band FEC with that loss percentage */
    channels = (argc >= 10) ? atoi(argv[9]) : 1;
    vbr = (argc >= 11) ? atoi(argv[10]) : 1; /* 0 = CBR, 1 = constrained VBR */
    complexity = (argc >= 12) ? atoi(argv[11]) : 5;
    if (channels != 1 && channels != 2) {
        fprintf(stderr, "channels must be 1 or 2\n");
        return 2;
    }
    frame_size = rate / 50;
    if (rate != 8000 && rate != 12000 && rate != 16000 && rate != 24000 && rate != 48000) {
        fprintf(stderr, "--silk-enc rate must be 8000, 12000, 16000, 24000 or 48000\n");
        return 2;
    }
    if (target >= frames) {
        fprintf(stderr, "targetFrame %d outside frame count %d\n", target, frames);
        return 2;
    }

    enc = opus_encoder_create(rate, channels, OPUS_APPLICATION_VOIP, &err);
    if (enc == NULL || err != OPUS_OK) {
        fprintf(stderr, "opus_encoder_create failed: %d\n", err);
        return 2;
    }
    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity));
    opus_encoder_ctl(enc, OPUS_SET_VBR(vbr ? 1 : 0));
    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(1));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
    if (bandwidth != OPUS_AUTO) opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth));
    opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_SILK_ONLY));
    if (lossPerc > 0) {
        opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(lossPerc));
        opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(1));
    }

    fprintf(stderr, "SILK_ENC_ORACLE rate=%d frame_size=%d fixture=%s target=%d frames=%d bitrate=%d bandwidth=%d channels=%d\n",
            rate, frame_size, fixture, target, frames, bitrate, bandwidth, channels);
    for (frame = 0; frame < frames; frame++) {
        int n, config;
        if (channels == 2) fill_silk_fixture_stereo(pcm, rate, frame_size, frame, fixture);
        else fill_silk_fixture(pcm, rate, frame_size, frame, fixture);
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

/* --celt-enc <rate> <fixture> <frames> <bitrate> <complexity> <vbr> <channels>:
   CELT-only (RESTRICTED_LOWDELAY, fullband forced) encoder trace. */
static int run_celt_encoder_oracle(int argc, char **argv)
{
    int rate, frames, bitrate, complexity, vbr, channels, frame_size, err, frame;
    const char *fixture;
    OpusEncoder *enc;
    float pcm[960 * 2];
    unsigned char packet[1500];

    if (argc < 4) {
        fprintf(stderr, "usage: %s --celt-enc <rate> <fixture> [frames] [bitrate] [complexity] [vbr] [channels]\n", argv[0]);
        return 2;
    }
    rate = atoi(argv[2]);
    fixture = argv[3];
    frames = (argc >= 5) ? atoi(argv[4]) : 8;
    bitrate = (argc >= 6) ? atoi(argv[5]) : 64000;
    complexity = (argc >= 7) ? atoi(argv[6]) : 5;
    vbr = (argc >= 8) ? atoi(argv[7]) : 0;
    channels = (argc >= 9) ? atoi(argv[8]) : 1;
    if (rate != 48000) {
        fprintf(stderr, "--celt-enc rate must be 48000\n");
        return 2;
    }
    frame_size = rate / 50;
    enc = opus_encoder_create(rate, channels, OPUS_APPLICATION_RESTRICTED_LOWDELAY, &err);
    if (enc == NULL || err != OPUS_OK) {
        fprintf(stderr, "opus_encoder_create failed: %d\n", err);
        return 2;
    }
    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity));
    opus_encoder_ctl(enc, OPUS_SET_VBR(vbr ? 1 : 0));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND));
    fprintf(stderr, "CELT_ENC_ORACLE rate=%d frame_size=%d fixture=%s frames=%d bitrate=%d complexity=%d vbr=%d channels=%d\n",
            rate, frame_size, fixture, frames, bitrate, complexity, vbr, channels);
    for (frame = 0; frame < frames; frame++) {
        int n, b;
        if (channels == 2) fill_silk_fixture_stereo(pcm, rate, frame_size, frame, fixture);
        else fill_silk_fixture(pcm, rate, frame_size, frame, fixture);
        /* Snap the fixture to the int16 grid so that a last-ulp difference
           between the C and Go sin() cannot leak into the float CELT path. */
        for (b = 0; b < frame_size * channels; b++)
            pcm[b] = (float)(floor((double)pcm[b] * 32768.0 + 0.5) / 32768.0);
        oracle_trace_enabled = 1;
        fprintf(stderr, "[CELT_ENC_INPUT_FRAME] frame=%d\n", frame);
        n = opus_encode_float(enc, pcm, frame_size, packet, (opus_int32)sizeof(packet));
        if (n < 0) {
            fprintf(stderr, "encode frame %d failed: %d\n", frame, n);
            opus_encoder_destroy(enc);
            return 1;
        }
        fprintf(stderr, "ENC_RESULT frame=%d bytes=%d toc=0x%02x config=%d traced=1\n", frame, n, packet[0], (packet[0] >> 3) & 0x1f);
        fprintf(stderr, "[ENC_PACKET] n=%d", n);
        for (b = 0; b < n; b++) fprintf(stderr, " %02x", packet[b]);
        fprintf(stderr, "\n");
    }
    oracle_trace_enabled = 0;
    opus_encoder_destroy(enc);
    return 0;
}

/* --hybrid-enc <rate> <fixture> <frames> <bitrate> <bw> <vbr> <channels> <complexity>:
   VOIP + voice hint, forced MODE_HYBRID, full SILK and CELT traces. */
static int run_hybrid_encoder_oracle(int argc, char **argv)
{
    int rate, frames, bitrate, bandwidth, complexity, vbr, channels, frame_size, err, frame;
    const char *fixture;
    OpusEncoder *enc;
    float pcm[960 * 2];
    unsigned char packet[1500];

    if (argc < 4) {
        fprintf(stderr, "usage: %s --hybrid-enc <rate> <fixture> [frames] [bitrate] [bw] [vbr] [channels] [complexity]\n", argv[0]);
        return 2;
    }
    rate = atoi(argv[2]);
    fixture = argv[3];
    frames = (argc >= 5) ? atoi(argv[4]) : 8;
    bitrate = (argc >= 6) ? atoi(argv[5]) : 64000;
    bandwidth = (argc >= 7) ? parse_bandwidth(argv[6]) : OPUS_BANDWIDTH_FULLBAND;
    vbr = (argc >= 8) ? atoi(argv[7]) : 1;
    channels = (argc >= 9) ? atoi(argv[8]) : 1;
    complexity = (argc >= 10) ? atoi(argv[9]) : 5;
    if (rate != 48000) {
        fprintf(stderr, "--hybrid-enc rate must be 48000\n");
        return 2;
    }
    frame_size = rate / 50;
    enc = opus_encoder_create(rate, channels, OPUS_APPLICATION_VOIP, &err);
    if (enc == NULL || err != OPUS_OK) {
        fprintf(stderr, "opus_encoder_create failed: %d\n", err);
        return 2;
    }
    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity));
    opus_encoder_ctl(enc, OPUS_SET_VBR(vbr ? 1 : 0));
    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(1));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth));
    opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(MODE_HYBRID));
    fprintf(stderr, "HYBRID_ENC_ORACLE rate=%d frame_size=%d fixture=%s frames=%d bitrate=%d bandwidth=%d vbr=%d channels=%d complexity=%d\n",
            rate, frame_size, fixture, frames, bitrate, bandwidth, vbr, channels, complexity);
    for (frame = 0; frame < frames; frame++) {
        int n, b;
        if (channels == 2) fill_silk_fixture_stereo(pcm, rate, frame_size, frame, fixture);
        else fill_silk_fixture(pcm, rate, frame_size, frame, fixture);
        for (b = 0; b < frame_size * channels; b++)
            pcm[b] = (float)(floor((double)pcm[b] * 32768.0 + 0.5) / 32768.0);
        oracle_trace_enabled = 1;
        fprintf(stderr, "[CELT_ENC_INPUT_FRAME] frame=%d\n", frame);
        n = opus_encode_float(enc, pcm, frame_size, packet, (opus_int32)sizeof(packet));
        if (n < 0) {
            fprintf(stderr, "encode frame %d failed: %d\n", frame, n);
            opus_encoder_destroy(enc);
            return 1;
        }
        fprintf(stderr, "ENC_RESULT frame=%d bytes=%d toc=0x%02x config=%d traced=1\n", frame, n, packet[0], (packet[0] >> 3) & 0x1f);
        fprintf(stderr, "[ENC_PACKET] n=%d", n);
        for (b = 0; b < n; b++) fprintf(stderr, " %02x", packet[b]);
        fprintf(stderr, "\n");
    }
    oracle_trace_enabled = 0;
    opus_encoder_destroy(enc);
    return 0;
}

/* --auto-enc <rate> <fixture> <frames> <bitrate> <vbr> <channels> <complexity> <signal> <app>:
   automatic mode / bandwidth / channel decisions (no forced mode), with
   the SILK and CELT traces. signal = voice|music|auto, app = voip|audio.
   <bitrate> may be a comma-separated per-frame schedule ("12000,12000,64000"):
   frame f uses entry min(f, n-1), so the last entry repeats. An optional
   trailing <frame_ms> (2.5, 5, 10, 20, 40, 60, 80, 100 or 120; default 20)
   sets the packet duration. */
#define AUTO_ENC_MAX_SCHEDULE 64
static int parse_bitrate_schedule(const char *s, int *out, int max)
{
    int n = 0;
    while (*s && n < max) {
        out[n++] = atoi(s);
        while (*s && *s != ',') s++;
        if (*s == ',') s++;
    }
    return n;
}

static int run_auto_encoder_oracle(int argc, char **argv)
{
    int rate, frames, bitrate, complexity, vbr, channels, frame_size, err, frame, app, frame_us, loss_perc, use_dtx;
    int schedule[AUTO_ENC_MAX_SCHEDULE], nschedule;
    const char *fixture, *signal, *appname;
    OpusEncoder *enc;
    static float pcm[5760 * 2];
    static opus_int16 pcm16[5760 * 2];
    unsigned char packet[1500];
    /* Options (argv[14], comma-separated key=value): vbrc=0|1 (default 1),
       bw=/maxbw=nb|mb|wb|swb|fb, fc=1|2 (force channels), int16=1 (encode
       through opus_encode), lsb=N (OPUS_SET_LSB_DEPTH), notrace=1. */
    int opt_vbrc = 1, opt_bw = 0, opt_maxbw = 0, opt_fc = 0, opt_int16 = 0, opt_lsb = 0, opt_notrace = 0;

    if (argc < 4) {
        fprintf(stderr, "usage: %s --auto-enc <rate> <fixture> [frames] [bitrate] [vbr] [channels] [complexity] [signal] [app] [frame_ms] [loss_perc] [dtx] [options]\n", argv[0]);
        return 2;
    }
    rate = atoi(argv[2]);
    fixture = argv[3];
    frames = (argc >= 5) ? atoi(argv[4]) : 8;
    nschedule = (argc >= 6) ? parse_bitrate_schedule(argv[5], schedule, AUTO_ENC_MAX_SCHEDULE) : 0;
    if (nschedule == 0) {
        schedule[0] = 32000;
        nschedule = 1;
    }
    bitrate = schedule[0];
    vbr = (argc >= 7) ? atoi(argv[6]) : 1;
    channels = (argc >= 8) ? atoi(argv[7]) : 1;
    complexity = (argc >= 9) ? atoi(argv[8]) : 5;
    signal = (argc >= 10) ? argv[9] : "voice";
    appname = (argc >= 11) ? argv[10] : "voip";
    /* The duration in microseconds, so that 2.5 ms is expressible. */
    frame_us = (argc >= 12) ? (int)(atof(argv[11]) * 1000.0 + 0.5) : 20000;
    loss_perc = (argc >= 13) ? atoi(argv[12]) : 0;
    use_dtx = (argc >= 14) ? atoi(argv[13]) : 0;
    if (argc >= 15 && argv[14][0] != '\0' && strcmp(argv[14], "-") != 0) {
        char opts[512];
        char *tok;
        strncpy(opts, argv[14], sizeof(opts) - 1);
        opts[sizeof(opts) - 1] = '\0';
        for (tok = strtok(opts, ","); tok != NULL; tok = strtok(NULL, ",")) {
            char *eq = strchr(tok, '=');
            if (eq == NULL) { fprintf(stderr, "bad option %s\n", tok); return 2; }
            *eq = '\0';
            if (strcmp(tok, "vbrc") == 0) opt_vbrc = atoi(eq + 1);
            else if (strcmp(tok, "bw") == 0) opt_bw = parse_bandwidth(eq + 1);
            else if (strcmp(tok, "maxbw") == 0) opt_maxbw = parse_bandwidth(eq + 1);
            else if (strcmp(tok, "fc") == 0) opt_fc = atoi(eq + 1);
            else if (strcmp(tok, "int16") == 0) opt_int16 = atoi(eq + 1);
            else if (strcmp(tok, "lsb") == 0) opt_lsb = atoi(eq + 1);
            else if (strcmp(tok, "notrace") == 0) opt_notrace = atoi(eq + 1);
            else { fprintf(stderr, "unknown option %s\n", tok); return 2; }
        }
    }
    if (rate != 8000 && rate != 12000 && rate != 16000 && rate != 24000 && rate != 48000) {
        fprintf(stderr, "--auto-enc rate must be 8000, 12000, 16000, 24000 or 48000\n");
        return 2;
    }
    if (frame_us != 2500 && frame_us != 5000 && frame_us != 10000 && frame_us != 20000 && frame_us != 40000 &&
        frame_us != 60000 && frame_us != 80000 && frame_us != 100000 && frame_us != 120000) {
        fprintf(stderr, "--auto-enc frame_ms must be 2.5, 5, 10, 20, 40, 60, 80, 100 or 120\n");
        return 2;
    }
    app = strcmp(appname, "audio") == 0 ? OPUS_APPLICATION_AUDIO : OPUS_APPLICATION_VOIP;
    frame_size = (int)((long long)rate * frame_us / 1000000);
    enc = opus_encoder_create(rate, channels, app, &err);
    if (enc == NULL || err != OPUS_OK) {
        fprintf(stderr, "opus_encoder_create failed: %d\n", err);
        return 2;
    }
    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity));
    opus_encoder_ctl(enc, OPUS_SET_VBR(vbr ? 1 : 0));
    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(opt_vbrc));
    if (opt_bw) opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(opt_bw));
    if (opt_maxbw) opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(opt_maxbw));
    if (opt_fc) opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(opt_fc));
    if (opt_lsb) opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(opt_lsb));
    if (use_dtx) opus_encoder_ctl(enc, OPUS_SET_DTX(1));
    if (loss_perc > 0) {
        opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(1));
        opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(loss_perc));
    }
    if (strcmp(signal, "voice") == 0) opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
    else if (strcmp(signal, "music") == 0) opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_MUSIC));
    fprintf(stderr, "AUTO_ENC_ORACLE rate=%d frame_size=%d fixture=%s frames=%d bitrate=%d vbr=%d channels=%d complexity=%d signal=%s app=%s loss=%d dtx=%d\n",
            rate, frame_size, fixture, frames, bitrate, vbr, channels, complexity, signal, appname, loss_perc, use_dtx);
    for (frame = 0; frame < frames; frame++) {
        int n, b;
        int fb = schedule[frame < nschedule ? frame : nschedule - 1];
        if (fb != bitrate) {
            bitrate = fb;
            opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
        }
        if (channels == 2) fill_silk_fixture_stereo(pcm, rate, frame_size, frame, fixture);
        else fill_silk_fixture(pcm, rate, frame_size, frame, fixture);
        for (b = 0; b < frame_size * channels; b++)
            pcm[b] = (float)(floor((double)pcm[b] * 32768.0 + 0.5) / 32768.0);
        oracle_trace_enabled = !opt_notrace;
        fprintf(stderr, "[CELT_ENC_INPUT_FRAME] frame=%d bitrate=%d\n", frame, bitrate);
        if (opt_int16) {
            for (b = 0; b < frame_size * channels; b++) {
                double v = floor((double)pcm[b] * 32768.0 + 0.5);
                if (v > 32767.0) v = 32767.0;
                if (v < -32768.0) v = -32768.0;
                pcm16[b] = (opus_int16)v;
            }
            n = opus_encode(enc, pcm16, frame_size, packet, (opus_int32)sizeof(packet));
        } else {
            n = opus_encode_float(enc, pcm, frame_size, packet, (opus_int32)sizeof(packet));
        }
        if (n < 0) {
            fprintf(stderr, "encode frame %d failed: %d\n", frame, n);
            opus_encoder_destroy(enc);
            return 1;
        }
        fprintf(stderr, "ENC_RESULT frame=%d bytes=%d toc=0x%02x config=%d traced=1\n", frame, n, packet[0], (packet[0] >> 3) & 0x1f);
        fprintf(stderr, "[ENC_PACKET] n=%d", n);
        for (b = 0; b < n; b++) fprintf(stderr, " %02x", packet[b]);
        fprintf(stderr, "\n");
    }
    oracle_trace_enabled = 0;
    opus_encoder_destroy(enc);
    return 0;
}

/* mc / mc-gaps (msFixtureSample in opus_multistream_enc_oracle_test.go):
   channel c of a multichannel input is a harmonic tone of its own pitch,
   envelope and phase plus a little hashed noise, so that no two channels are
   alike. mc-gaps puts ref-speech-gaps' silence and noise segments over it. */
static double ms_fixture_sample(const char *fixture, long idx, int c, int rate)
{
    double tm = (double)idx / (double)rate;
    double f0 = 140.0 + 45.0 * c;
    double env = 0.5 + 0.3 * sin(2.0 * M_PI * (2.0 + 0.7 * c) * tm + 0.4 * c);
    double v = env * (0.25 * sin(2.0 * M_PI * f0 * tm + 0.3 * c) +
        0.10 * sin(2.0 * M_PI * 2.0 * f0 * tm + 0.9) +
        0.05 * sin(2.0 * M_PI * 3.3 * f0 * tm + 0.2 * c));
    v += 10.0 * gaps_noise(idx, c + 7, 2);
    if (strcmp(fixture, "mc-gaps") == 0) {
        int seg = gaps_segment(idx, rate);
        if (seg == 1) return 0.0;
        if (seg >= 2) return gaps_noise(idx, c, seg);
        return v;
    }
    if (strcmp(fixture, "mc") != 0) {
        fprintf(stderr, "unknown multichannel fixture %s\n", fixture);
        exit(2);
    }
    return v;
}

/* --ms-enc <rate> <fixture> <frames> <bitrate|schedule> <vbr> <channels>
   <complexity> <signal> <app> <frame_ms> <loss_perc> <dtx> <family> [options]:
   the multistream encoders. family 0, 1, 2 or 255 is
   opus_multistream_surround_encoder_create with that mapping family, -1 is
   opus_multistream_encoder_create with the family-1 layout (family 255 above
   8 channels), and 3 is opus_projection_ambisonics_encoder_create. Options
   as --auto-enc (vbrc, bw, maxbw, int16, lsb); trace=1 enables the stage
   traces of every elementary encoder (interleaved by stream). */
static int run_ms_encoder_oracle(int argc, char **argv)
{
    int rate, frames, bitrate, complexity, vbr, channels, frame_size, err, frame, app, frame_us, loss_perc, use_dtx, family;
    int schedule[AUTO_ENC_MAX_SCHEDULE], nschedule;
    int streams = 0, coupled = 0;
    unsigned char mapping[256];
    const char *fixture, *signal, *appname;
    OpusMSEncoder *enc = NULL;
    OpusProjectionEncoder *proj = NULL;
    static float pcm[5760 * 255];
    static opus_int16 pcm16[5760 * 255];
    static unsigned char packet[1275 * 255];
    int opt_vbrc = 1, opt_bw = 0, opt_maxbw = 0, opt_int16 = 0, opt_lsb = 0, opt_trace = 0;

    if (argc < 15) {
        fprintf(stderr, "usage: %s --ms-enc <rate> <fixture> <frames> <bitrate> <vbr> <channels> <complexity> <signal> <app> <frame_ms> <loss_perc> <dtx> <family> [options]\n", argv[0]);
        return 2;
    }
    rate = atoi(argv[2]);
    fixture = argv[3];
    frames = atoi(argv[4]);
    nschedule = parse_bitrate_schedule(argv[5], schedule, AUTO_ENC_MAX_SCHEDULE);
    if (nschedule == 0) {
        schedule[0] = OPUS_AUTO;
        nschedule = 1;
    }
    bitrate = schedule[0];
    vbr = atoi(argv[6]);
    channels = atoi(argv[7]);
    complexity = atoi(argv[8]);
    signal = argv[9];
    appname = argv[10];
    frame_us = (int)(atof(argv[11]) * 1000.0 + 0.5);
    loss_perc = atoi(argv[12]);
    use_dtx = atoi(argv[13]);
    family = atoi(argv[14]);
    if (argc >= 16 && argv[15][0] != '\0' && strcmp(argv[15], "-") != 0) {
        char opts[512];
        char *tok;
        strncpy(opts, argv[15], sizeof(opts) - 1);
        opts[sizeof(opts) - 1] = '\0';
        for (tok = strtok(opts, ","); tok != NULL; tok = strtok(NULL, ",")) {
            char *eq = strchr(tok, '=');
            if (eq == NULL) { fprintf(stderr, "bad option %s\n", tok); return 2; }
            *eq = '\0';
            if (strcmp(tok, "vbrc") == 0) opt_vbrc = atoi(eq + 1);
            else if (strcmp(tok, "bw") == 0) opt_bw = parse_bandwidth(eq + 1);
            else if (strcmp(tok, "maxbw") == 0) opt_maxbw = parse_bandwidth(eq + 1);
            else if (strcmp(tok, "int16") == 0) opt_int16 = atoi(eq + 1);
            else if (strcmp(tok, "lsb") == 0) opt_lsb = atoi(eq + 1);
            else if (strcmp(tok, "notrace") == 0) (void)0;
            else if (strcmp(tok, "trace") == 0) opt_trace = atoi(eq + 1);
            else { fprintf(stderr, "unknown option %s\n", tok); return 2; }
        }
    }
    if (channels < 1 || channels > 255) {
        fprintf(stderr, "--ms-enc channels must be 1..255\n");
        return 2;
    }
    app = strcmp(appname, "audio") == 0 ? OPUS_APPLICATION_AUDIO :
        strcmp(appname, "lowdelay") == 0 ? OPUS_APPLICATION_RESTRICTED_LOWDELAY : OPUS_APPLICATION_VOIP;
    frame_size = (int)((long long)rate * frame_us / 1000000);
    if (family == 3) {
        proj = opus_projection_ambisonics_encoder_create(rate, channels, 3, &streams, &coupled, app, &err);
        if (proj == NULL || err != OPUS_OK) {
            fprintf(stderr, "opus_projection_ambisonics_encoder_create failed: %d\n", err);
            return 2;
        }
    } else if (family >= 0) {
        enc = opus_multistream_surround_encoder_create(rate, channels, family, &streams, &coupled, mapping, app, &err);
    } else {
        int f = channels <= 8 ? 1 : 255;
        OpusMSEncoder *probe = opus_multistream_surround_encoder_create(rate, channels, f, &streams, &coupled, mapping, app, &err);
        if (probe != NULL) opus_multistream_encoder_destroy(probe);
        if (err == OPUS_OK)
            enc = opus_multistream_encoder_create(rate, channels, streams, coupled, mapping, app, &err);
    }
    if (proj == NULL && (enc == NULL || err != OPUS_OK)) {
        fprintf(stderr, "multistream encoder create failed: %d\n", err);
        return 2;
    }
#define MS_CTL(x) (proj ? opus_projection_encoder_ctl(proj, x) : opus_multistream_encoder_ctl(enc, x))
    MS_CTL(OPUS_SET_BITRATE(bitrate));
    MS_CTL(OPUS_SET_COMPLEXITY(complexity));
    MS_CTL(OPUS_SET_VBR(vbr ? 1 : 0));
    MS_CTL(OPUS_SET_VBR_CONSTRAINT(opt_vbrc));
    if (opt_bw) MS_CTL(OPUS_SET_BANDWIDTH(opt_bw));
    if (opt_maxbw) MS_CTL(OPUS_SET_MAX_BANDWIDTH(opt_maxbw));
    if (opt_lsb) MS_CTL(OPUS_SET_LSB_DEPTH(opt_lsb));
    if (use_dtx) MS_CTL(OPUS_SET_DTX(1));
    if (loss_perc > 0) {
        MS_CTL(OPUS_SET_INBAND_FEC(1));
        MS_CTL(OPUS_SET_PACKET_LOSS_PERC(loss_perc));
    }
    if (strcmp(signal, "voice") == 0) MS_CTL(OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
    else if (strcmp(signal, "music") == 0) MS_CTL(OPUS_SET_SIGNAL(OPUS_SIGNAL_MUSIC));
    fprintf(stderr, "MS_ENC_ORACLE rate=%d frame_size=%d fixture=%s frames=%d bitrate=%d vbr=%d channels=%d streams=%d coupled=%d family=%d\n",
            rate, frame_size, fixture, frames, bitrate, vbr, channels, streams, coupled, family);
    for (frame = 0; frame < frames; frame++) {
        int n, b, i, c;
        int fb = schedule[frame < nschedule ? frame : nschedule - 1];
        if (fb != bitrate) {
            bitrate = fb;
            MS_CTL(OPUS_SET_BITRATE(bitrate));
        }
        for (i = 0; i < frame_size; i++) {
            long idx = (long)frame * frame_size + i;
            for (c = 0; c < channels; c++) {
                float v = (float)ms_fixture_sample(fixture, idx, c, rate);
                pcm[i * channels + c] = (float)(floor((double)v * 32768.0 + 0.5) / 32768.0);
            }
        }
        fprintf(stderr, "[CELT_ENC_INPUT_FRAME] frame=%d bitrate=%d\n", frame, bitrate);
        oracle_trace_enabled = opt_trace;
        if (opt_int16) {
            for (b = 0; b < frame_size * channels; b++) {
                double v = floor((double)pcm[b] * 32768.0 + 0.5);
                if (v > 32767.0) v = 32767.0;
                if (v < -32768.0) v = -32768.0;
                pcm16[b] = (opus_int16)v;
            }
            n = proj ? opus_projection_encode(proj, pcm16, frame_size, packet, (opus_int32)sizeof(packet))
                     : opus_multistream_encode(enc, pcm16, frame_size, packet, (opus_int32)sizeof(packet));
        } else {
            n = proj ? opus_projection_encode_float(proj, pcm, frame_size, packet, (opus_int32)sizeof(packet))
                     : opus_multistream_encode_float(enc, pcm, frame_size, packet, (opus_int32)sizeof(packet));
        }
        if (n < 0) {
            fprintf(stderr, "encode frame %d failed: %d\n", frame, n);
            return 1;
        }
        fprintf(stderr, "[ENC_PACKET] n=%d", n);
        for (b = 0; b < n; b++) fprintf(stderr, " %02x", packet[b]);
        fprintf(stderr, "\n");
    }
#undef MS_CTL
    if (proj) opus_projection_encoder_destroy(proj);
    if (enc) opus_multistream_encoder_destroy(enc);
    return 0;
}

int main(int argc, char **argv)
{
    if (argc >= 2 && strcmp(argv[1], "--ms-enc") == 0) {
        return run_ms_encoder_oracle(argc, argv);
    }
    if (argc >= 2 && strcmp(argv[1], "--auto-enc") == 0) {
        return run_auto_encoder_oracle(argc, argv);
    }
    if (argc >= 2 && strcmp(argv[1], "--hybrid-enc") == 0) {
        return run_hybrid_encoder_oracle(argc, argv);
    }
    if (argc >= 2 && strcmp(argv[1], "--celt-enc") == 0) {
        return run_celt_encoder_oracle(argc, argv);
    }
    if (argc < 2 || strcmp(argv[1], "--silk-enc") != 0) {
        fprintf(stderr, "usage: %s --silk-enc <rate> <fixture> [targetFrame|-1] [frames] [bitrate] [bandwidth]\n", argv[0]);
        return 2;
    }
    return run_silk_encoder_oracle(argc, argv);
}
