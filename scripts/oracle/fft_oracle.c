/* Emit deterministic hashes from libopus's floating-point KISS FFT.
 *
 * Build from the repository root with:
 *   gcc -O1 -DOPUS_BUILD -DVAR_ARRAYS \
 *     -Ilibopus/celt -Ilibopus/include -Ilibopus \
 *     scripts/oracle/fft_oracle.c libopus/celt/kiss_fft.c \
 *     libopus/celt/mdct.c -lm \
 *     -o fft_oracle.exe
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "kiss_fft.h"
#include "mdct.h"
#include "modes.h"
static const opus_int16 eband5ms[] = {0};
static const unsigned char band_allocation[] = {0};
#include "static_modes_float.h"

static uint64_t hash_u32(uint64_t hash, uint32_t value) {
  int byte;
  for (byte = 0; byte < 4; ++byte) {
    hash ^= (value >> (8 * byte)) & 0xff;
    hash *= UINT64_C(1099511628211);
  }
  return hash;
}

static uint32_t float_bits(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return bits;
}

static int run_size(int n) {
  const kiss_fft_state *state;
  kiss_fft_cpx *input;
  kiss_fft_cpx *output;
  uint64_t hash = UINT64_C(14695981039346656037);
  int i;

  switch (n) {
    case 60: state = &fft_state48000_960_3; break;
    case 120: state = &fft_state48000_960_2; break;
    case 240: state = &fft_state48000_960_1; break;
    case 480: state = &fft_state48000_960_0; break;
    default: return 1;
  }
  input = (kiss_fft_cpx *)calloc((size_t)n, sizeof(*input));
  output = (kiss_fft_cpx *)calloc((size_t)n, sizeof(*output));
  if (state == NULL || input == NULL || output == NULL) return 1;

  for (i = 0; i < n; ++i) {
    input[i].r = (float)((i * 37) % 257 - 128) * (1.0f / 64.0f);
    input[i].i = (float)((i * 73) % 251 - 125) * (1.0f / 128.0f);
    output[state->bitrev[i]] = input[i];
  }
  opus_fft_impl(state, output);

  for (i = 0; i < n; ++i) {
    hash = hash_u32(hash, float_bits(output[i].r));
    hash = hash_u32(hash, float_bits(output[i].i));
  }
  printf("n=%d hash=%016llx first=%08x/%08x last=%08x/%08x\n", n,
         (unsigned long long)hash, float_bits(output[0].r),
         float_bits(output[0].i), float_bits(output[n - 1].r),
         float_bits(output[n - 1].i));

  free(input);
  free(output);
  return 0;
}

static int run_mdct(void) {
  const mdct_lookup *lookup = &mode48000_960_120.mdct;
  int shift;
  for (shift = 0; shift <= 3; ++shift) {
    int n = 960 >> shift;
    float *input = (float *)calloc((size_t)n, sizeof(*input));
    float *output = (float *)calloc((size_t)(n + 60), sizeof(*output));
    uint64_t hash = UINT64_C(14695981039346656037);
    int i;
    if (input == NULL || output == NULL) return 1;
    for (i = 0; i < n; ++i)
      input[i] = (float)((i * 29) % 263 - 131) * (1.0f / 256.0f);
    for (i = 0; i < 60; ++i)
      output[i] = (float)((i * 17) % 61 - 30) * (1.0f / 512.0f);
    clt_mdct_backward_c(lookup, input, output, window120, 120, shift, 1,
                        0);
    for (i = 0; i < n + 60; ++i)
      hash = hash_u32(hash, float_bits(output[i]));
    printf("mdct=%d hash=%016llx first=%08x tail=%08x\n", n,
           (unsigned long long)hash, float_bits(output[0]),
           float_bits(output[n + 59]));
    free(input);
    free(output);
  }
  return 0;
}

int main(void) {
  static const int sizes[] = {60, 120, 240, 480};
  size_t i;
  for (i = 0; i < sizeof(sizes) / sizeof(sizes[0]); ++i) {
    if (run_size(sizes[i]) != 0) return 1;
  }
  return run_mdct();
}
