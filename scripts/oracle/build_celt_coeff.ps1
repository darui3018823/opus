param(
    [string]$Output = (Join-Path $env:TEMP 'opus_celt_coeff_oracle.exe')
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$celt = Join-Path $repo 'libopus\celt'
$generated = Join-Path $env:TEMP 'opus_celt_decoder_coeff_instr.c'
$source = Get-Content (Join-Path $celt 'celt_decoder.c') -Raw

$helper = @'

#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
extern int oracle_trace_enabled;

static uint64_t oracle_hash_float(uint64_t hash, float value)
{
   uint32_t bits;
   int byte;
   memcpy(&bits, &value, sizeof(bits));
   for (byte=0;byte<4;byte++) {
      hash ^= (bits >> (8*byte)) & 0xff;
      hash *= UINT64_C(1099511628211);
   }
   return hash;
}

static uint32_t oracle_float_bits(float value)
{
   uint32_t bits;
   memcpy(&bits, &value, sizeof(bits));
   return bits;
}

static void oracle_dump_denorm(const CELTMode *mode, const celt_sig *freq,
      int ch, int start, int end, int M)
{
   int band, j;
   uint64_t full = UINT64_C(14695981039346656037);
   if (!oracle_trace_enabled) return;
   for (j=0;j<mode->shortMdctSize*M;j++)
      full = oracle_hash_float(full, freq[j]);
   fprintf(stderr, "[DENORM] ch=%d full=%016llx\n", ch,
         (unsigned long long)full);
   for (band=start;band<end;band++) {
      int first = M*mode->eBands[band];
      int last = M*mode->eBands[band+1];
      uint64_t hash = UINT64_C(14695981039346656037);
      for (j=first;j<last;j++) hash = oracle_hash_float(hash, freq[j]);
      fprintf(stderr, "[DENORM_BAND] ch=%d band=%d n=%d hash=%016llx\n",
            ch, band, last-first, (unsigned long long)hash);
   }
}

static void oracle_dump_norm(const CELTMode *mode, const celt_norm *x,
      const celt_glog *oldBandE, int ch, int start, int end, int M)
{
   int band, j;
   if (!oracle_trace_enabled) return;
   for (band=start;band<end;band++) {
      int first = M*mode->eBands[band];
      int last = M*mode->eBands[band+1];
      uint64_t hash = UINT64_C(14695981039346656037);
      for (j=first;j<last;j++) hash = oracle_hash_float(hash, x[j]);
      fprintf(stderr, "[NORM_BAND] ch=%d band=%d n=%d hash=%016llx energy=%.9g energyBits=%08x\n",
            ch, band, last-first, (unsigned long long)hash,
            (double)oldBandE[band], oracle_float_bits(oldBandE[band]));
      if (getenv("OPUS_ORACLE_COEFFS") != NULL) {
         for (j=first;j<last;j++)
            fprintf(stderr, "[NORM_COEFF] ch=%d band=%d index=%d value=%.9g bits=%08x\n",
                  ch, band, j-first, (double)x[j], oracle_float_bits(x[j]));
      }
   }
}

'@

$marker = 'void celt_synthesis('
if (-not $source.Contains($marker)) { throw "celt_synthesis marker not found" }
$source = $source.Replace($marker, $helper + $marker)
$callPattern = '(?s)(denormalise_bands\(mode, X\+c\*N, freq, oldBandE\+c\*nbEBands, start, effEnd, M,\s*downsample, silence\);)'
if (-not [regex]::IsMatch($source, $callPattern)) { throw "normal stereo denormalise call not found" }
$source = [regex]::Replace($source, $callPattern, '$1' + "`r`n         oracle_dump_denorm(mode, freq, c, start, effEnd, M);", 1)
$source = [regex]::Replace($source, $callPattern,
    'oracle_dump_norm(mode, X+c*N, oldBandE+c*nbEBands, c, start, effEnd, M);' + "`r`n         " + '$1', 1)
Set-Content -LiteralPath $generated -Value $source

$sources = @(
    'bands.c', 'celt.c', 'cwrs.c', 'entcode.c', 'entdec.c', 'entenc.c',
    'kiss_fft.c', 'laplace.c', 'mathops.c', 'mdct.c', 'modes.c', 'pitch.c',
    'celt_lpc.c', 'quant_bands.c', 'rate.c', 'vq.c'
) | ForEach-Object { Join-Path $celt $_ }

& gcc -O1 -DOPUS_BUILD -DVAR_ARRAYS "-I$celt" "-I$(Join-Path $repo 'libopus\include')" `
    "-I$(Join-Path $repo 'libopus')" "-I$(Join-Path $repo 'libopus\silk')" `
    (Join-Path $PSScriptRoot 'celt_coeff_oracle.c') `
    $generated @sources -lm -o $Output
if ($LASTEXITCODE -ne 0) { throw "gcc failed with exit code $LASTEXITCODE" }
Write-Output $Output
