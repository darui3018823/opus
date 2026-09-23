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

static void fill_silk_fixture_stereo(float *pcm, int rate, int frame_size, int frame, const char *fixture)
{
    int i;
    int start = frame * frame_size;
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
   trailing <frame_ms> (20, 40 or 60; default 20) sets the packet duration. */
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
    int rate, frames, bitrate, complexity, vbr, channels, frame_size, err, frame, app, frame_ms, loss_perc;
    int schedule[AUTO_ENC_MAX_SCHEDULE], nschedule;
    const char *fixture, *signal, *appname;
    OpusEncoder *enc;
    float pcm[2880 * 2];
    unsigned char packet[1500];

    if (argc < 4) {
        fprintf(stderr, "usage: %s --auto-enc <rate> <fixture> [frames] [bitrate] [vbr] [channels] [complexity] [signal] [app] [frame_ms] [loss_perc]\n", argv[0]);
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
    frame_ms = (argc >= 12) ? atoi(argv[11]) : 20;
    loss_perc = (argc >= 13) ? atoi(argv[12]) : 0;
    if (rate != 8000 && rate != 12000 && rate != 16000 && rate != 24000 && rate != 48000) {
        fprintf(stderr, "--auto-enc rate must be 8000, 12000, 16000, 24000 or 48000\n");
        return 2;
    }
    if (frame_ms != 20 && frame_ms != 40 && frame_ms != 60) {
        fprintf(stderr, "--auto-enc frame_ms must be 20, 40 or 60\n");
        return 2;
    }
    app = strcmp(appname, "audio") == 0 ? OPUS_APPLICATION_AUDIO : OPUS_APPLICATION_VOIP;
    frame_size = rate * frame_ms / 1000;
    enc = opus_encoder_create(rate, channels, app, &err);
    if (enc == NULL || err != OPUS_OK) {
        fprintf(stderr, "opus_encoder_create failed: %d\n", err);
        return 2;
    }
    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity));
    opus_encoder_ctl(enc, OPUS_SET_VBR(vbr ? 1 : 0));
    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(1));
    if (loss_perc > 0) {
        opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(1));
        opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(loss_perc));
    }
    if (strcmp(signal, "voice") == 0) opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
    else if (strcmp(signal, "music") == 0) opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_MUSIC));
    fprintf(stderr, "AUTO_ENC_ORACLE rate=%d frame_size=%d fixture=%s frames=%d bitrate=%d vbr=%d channels=%d complexity=%d signal=%s app=%s loss=%d\n",
            rate, frame_size, fixture, frames, bitrate, vbr, channels, complexity, signal, appname, loss_perc);
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
        oracle_trace_enabled = 1;
        fprintf(stderr, "[CELT_ENC_INPUT_FRAME] frame=%d bitrate=%d\n", frame, bitrate);
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

int main(int argc, char **argv)
{
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
