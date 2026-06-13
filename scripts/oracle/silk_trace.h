#ifndef OPUS_ORACLE_SILK_TRACE_H
#define OPUS_ORACLE_SILK_TRACE_H

#include <stdio.h>
#include "entcode.h"
#include "entdec.h"

extern int oracle_trace_enabled;

static void oracle_silk_range(const char *stage, ec_dec *dec)
{
    if (!oracle_trace_enabled || dec == NULL) return;
    fprintf(stderr, "[SILK_%s] tell=%d tellf=%d rng=%08x val=%08x\n",
            stage, ec_tell(dec), ec_tell_frac(dec),
            (unsigned)dec->rng, (unsigned)dec->val);
}

static void oracle_silk_dump_i16(const char *tag, const opus_int16 *v, int n)
{
    int i;
    if (!oracle_trace_enabled || v == NULL) return;
    fprintf(stderr, "[SILK_%s] n=%d", tag, n);
    for (i = 0; i < n; i++) fprintf(stderr, " v[%d]=%d", i, (int)v[i]);
    fprintf(stderr, "\n");
}

static void oracle_silk_dump_i32(const char *tag, const opus_int32 *v, int n)
{
    int i;
    if (!oracle_trace_enabled || v == NULL) return;
    fprintf(stderr, "[SILK_%s] n=%d", tag, n);
    for (i = 0; i < n; i++) fprintf(stderr, " v[%d]=%d", i, (int)v[i]);
    fprintf(stderr, "\n");
}

static void oracle_silk_dump_int(const char *tag, const opus_int *v, int n)
{
    int i;
    if (!oracle_trace_enabled || v == NULL) return;
    fprintf(stderr, "[SILK_%s] n=%d", tag, n);
    for (i = 0; i < n; i++) fprintf(stderr, " v[%d]=%d", i, (int)v[i]);
    fprintf(stderr, "\n");
}

static void oracle_silk_dump_i8(const char *tag, const opus_int8 *v, int n)
{
    int i;
    if (!oracle_trace_enabled || v == NULL) return;
    fprintf(stderr, "[SILK_%s] n=%d", tag, n);
    for (i = 0; i < n; i++) fprintf(stderr, " v[%d]=%d", i, (int)v[i]);
    fprintf(stderr, "\n");
}

#endif
