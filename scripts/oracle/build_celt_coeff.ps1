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

static void oracle_dump_signal(const char *stage, celt_sig *const signal[],
      int channels, int frame_size)
{
   int ch, i;
   if (!oracle_trace_enabled) return;
   for (ch=0;ch<channels;ch++) {
      uint64_t hash = UINT64_C(14695981039346656037);
      for (i=0;i<frame_size;i++)
         hash = oracle_hash_float(hash, signal[ch][i]);
      fprintf(stderr, "[%s] ch=%d n=%d hash=%016llx\n", stage, ch,
            frame_size, (unsigned long long)hash);
   }
}

static void oracle_dump_pcm(const opus_res *pcm, int channels, int frame_size)
{
   int ch, i;
   if (!oracle_trace_enabled) return;
   for (ch=0;ch<channels;ch++) {
      uint64_t hash = UINT64_C(14695981039346656037);
      for (i=0;i<frame_size;i++)
         hash = oracle_hash_float(hash, pcm[i*channels+ch]);
      fprintf(stderr, "[PCM] ch=%d n=%d hash=%016llx\n", ch, frame_size,
            (unsigned long long)hash);
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
$synthesisPattern = '(?s)(celt_synthesis\(mode, X, out_syn, oldBandE, start, effEnd,\s*C, CC, isTransient, LM, st->downsample, silence, st->arch.*?\);)'
if (-not [regex]::IsMatch($source, $synthesisPattern)) { throw "decode celt_synthesis call not found" }
$source = [regex]::Replace($source, $synthesisPattern,
    '$1' + "`r`n   " + 'oracle_dump_signal("SYNTH", out_syn, CC, N);', 1)
$postfilterPattern = '(?ms)(^   c=0; do \{\s*st->postfilter_period=IMAX\(st->postfilter_period, COMBFILTER_MINPERIOD\);.*?^   \} while \(\+\+c<CC\);)'
if (-not [regex]::IsMatch($source, $postfilterPattern)) { throw "decode postfilter block not found" }
$source = [regex]::Replace($source, $postfilterPattern,
    '$1' + "`r`n   " + 'oracle_dump_signal("POSTFILTER", out_syn, CC, N);', 1)
$deemphasisPattern = '(?m)(^   deemphasis\(out_syn, pcm, N, CC, st->downsample, mode->preemph, st->preemph_memD, accum\);)'
if (-not [regex]::IsMatch($source, $deemphasisPattern)) { throw "decode deemphasis call not found" }
$source = [regex]::Replace($source, $deemphasisPattern,
    '$1' + "`r`n   " + 'oracle_dump_pcm(pcm, CC, N);', 1)
Set-Content -LiteralPath $generated -Value $source

$generatedBands = Join-Path $env:TEMP 'opus_celt_bands_coeff_instr.c'
$bandsSource = Get-Content (Join-Path $celt 'bands.c') -Raw
$bandsHelper = @'

#include <stdio.h>
#include <stdint.h>
#include <string.h>
extern int oracle_trace_enabled;
static int oracle_stereo_merge_call;

static uint32_t oracle_band_float_bits(float value)
{
   uint32_t bits;
   memcpy(&bits, &value, sizeof(bits));
   return bits;
}

static uint64_t oracle_band_hash(const celt_norm *x, int n)
{
   int i, byte;
   uint64_t hash = UINT64_C(14695981039346656037);
   for (i=0;i<n;i++) {
      uint32_t bits = oracle_band_float_bits(x[i]);
      for (byte=0;byte<4;byte++) {
         hash ^= (bits >> (8*byte)) & 0xff;
         hash *= UINT64_C(1099511628211);
      }
   }
   return hash;
}

'@
$bandsMarker = 'static void stereo_merge('
if (-not $bandsSource.Contains($bandsMarker)) { throw "stereo_merge marker not found" }
$bandsSource = $bandsSource.Replace($bandsMarker, $bandsHelper + $bandsMarker)
$gainMarker = '   rgain = celt_rsqrt_norm32(t);'
if (-not $bandsSource.Contains($gainMarker)) { throw "stereo_merge gain marker not found" }
$gainTrace = @'
   if (oracle_trace_enabled) {
      fprintf(stderr, "[STEREO_MERGE] call=%d n=%d xhash=%016llx yhash=%016llx mid=%08x xp=%08x side=%08x el=%08x er=%08x lgain=%08x rgain=%08x\n",
            oracle_stereo_merge_call++, N,
            (unsigned long long)oracle_band_hash(X, N),
            (unsigned long long)oracle_band_hash(Y, N), oracle_band_float_bits(mid),
            oracle_band_float_bits(xp), oracle_band_float_bits(side),
            oracle_band_float_bits(El), oracle_band_float_bits(Er),
            oracle_band_float_bits(lgain), oracle_band_float_bits(rgain));
   }
'@
$bandsSource = $bandsSource.Replace($gainMarker, $gainMarker + "`r`n" + $gainTrace)
Set-Content -LiteralPath $generatedBands -Value $bandsSource

$generatedVQ = Join-Path $env:TEMP 'opus_celt_vq_coeff_instr.c'
$vqSource = Get-Content (Join-Path $celt 'vq.c') -Raw
$vqHelper = @'

#include <stdio.h>
#include <stdint.h>
#include <string.h>
extern int oracle_trace_enabled;
static int oracle_pvq_call;

static uint64_t oracle_vq_hash_float(uint64_t hash, float value)
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

static uint32_t oracle_vq_float_bits(float value)
{
   uint32_t bits;
   memcpy(&bits, &value, sizeof(bits));
   return bits;
}

static void oracle_dump_pvq_stage(const char *stage, int call,
      const celt_norm *x, int n, int k, int spread, int b, opus_val32 gain)
{
   int i;
   uint64_t hash = UINT64_C(14695981039346656037);
   if (!oracle_trace_enabled) return;
   for (i=0;i<n;i++) hash = oracle_vq_hash_float(hash, x[i]);
   fprintf(stderr, "[PVQ_%s] call=%d n=%d k=%d spread=%d b=%d gain=%08x hash=%016llx\n",
         stage, call, n, k, spread, b, oracle_vq_float_bits(gain),
         (unsigned long long)hash);
}

static void oracle_dump_pvq_pulses(int call, const int *iy, int n, int k,
      opus_val32 ryy)
{
   int i, byte;
   uint64_t hash = UINT64_C(14695981039346656037);
   if (!oracle_trace_enabled) return;
   for (i=0;i<n;i++) {
      uint32_t bits = (uint32_t)iy[i];
      for (byte=0;byte<4;byte++) {
         hash ^= (bits >> (8*byte)) & 0xff;
         hash *= UINT64_C(1099511628211);
      }
   }
   fprintf(stderr, "[PVQ_PULSES] call=%d n=%d k=%d ryy=%08x hash=%016llx values=",
         call, n, k, oracle_vq_float_bits(ryy), (unsigned long long)hash);
   for (i=0;i<n;i++) fprintf(stderr, "%s%d", i ? "," : "", iy[i]);
   fprintf(stderr, "\n");
}

'@
$vqMarker = 'unsigned alg_unquant('
$vqIndex = $vqSource.IndexOf($vqMarker)
if ($vqIndex -lt 0) { throw "alg_unquant marker not found" }
$vqPrefix = $vqSource.Substring(0, $vqIndex)
$vqSuffix = $vqSource.Substring($vqIndex)
$vqSuffix = $vqSuffix.Replace($vqMarker, $vqHelper + $vqMarker)
$normaliseMarker = '   normalise_residual(iy, X, N, Ryy, gain, yy_shift);'
if (-not $vqSuffix.Contains($normaliseMarker)) { throw "alg_unquant normalise marker not found" }
$vqSuffix = $vqSuffix.Replace($normaliseMarker,
    '   oracle_dump_pvq_pulses(oracle_pvq_call, iy, N, K, Ryy);' + "`r`n" +
    $normaliseMarker + "`r`n" +
    '   oracle_dump_pvq_stage("NORM", oracle_pvq_call, X, N, K, spread, B, gain);')
$rotationMarker = '   exp_rotation(X, N, -1, B, K, spread);'
if (-not $vqSuffix.Contains($rotationMarker)) { throw "alg_unquant rotation marker not found" }
$vqSuffix = $vqSuffix.Replace($rotationMarker, $rotationMarker + "`r`n" +
    '   oracle_dump_pvq_stage("ROT", oracle_pvq_call, X, N, K, spread, B, gain);' + "`r`n" +
    '   if (oracle_trace_enabled) oracle_pvq_call++;')
$vqSource = $vqPrefix + $vqSuffix
Set-Content -LiteralPath $generatedVQ -Value $vqSource

$generatedCWRS = Join-Path $env:TEMP 'opus_celt_cwrs_coeff_instr.c'
$cwrsSource = Get-Content (Join-Path $celt 'cwrs.c') -Raw
$cwrsInclude = '#include "os_support.h"'
if (-not $cwrsSource.Contains($cwrsInclude)) { throw "cwrs include marker not found" }
$cwrsSource = $cwrsSource.Replace($cwrsInclude, $cwrsInclude + @'

#include <stdio.h>
extern int oracle_trace_enabled;
static int oracle_cwrs_call;
'@)
$cwrsDecodeTrace = @'
opus_val32 decode_pulses(int *_y,int _n,int _k,ec_dec *_dec){
  opus_uint32 ft;
  opus_uint32 index;
  ft=CELT_PVQ_V(_n,_k);
  index=ec_dec_uint(_dec,ft);
  if (oracle_trace_enabled)
    fprintf(stderr,"[CWRS] call=%d n=%d k=%d ft=%u index=%u\n",
          oracle_cwrs_call++,_n,_k,ft,index);
  return cwrsi(_n,_k,index,_y);
}
'@
$cwrsDecodePattern = 'opus_val32 decode_pulses\(int \*_y,int _n,int _k,ec_dec \*_dec\)\{\r?\n  return cwrsi\(_n,_k,ec_dec_uint\(_dec,CELT_PVQ_V\(_n,_k\)\),_y\);\r?\n\}'
if (-not [regex]::IsMatch($cwrsSource, $cwrsDecodePattern)) { throw "non-small decode_pulses marker not found" }
$cwrsSource = [regex]::Replace($cwrsSource, $cwrsDecodePattern, $cwrsDecodeTrace, 1)
Set-Content -LiteralPath $generatedCWRS -Value $cwrsSource

$sources = @(
    'celt.c', 'entcode.c', 'entdec.c', 'entenc.c',
    'kiss_fft.c', 'laplace.c', 'mathops.c', 'mdct.c', 'modes.c', 'pitch.c',
    'celt_lpc.c', 'quant_bands.c', 'rate.c'
) | ForEach-Object { Join-Path $celt $_ }

& gcc -O1 -DOPUS_BUILD -DVAR_ARRAYS "-I$celt" "-I$(Join-Path $repo 'libopus\include')" `
    "-I$(Join-Path $repo 'libopus')" "-I$(Join-Path $repo 'libopus\silk')" `
    (Join-Path $PSScriptRoot 'celt_coeff_oracle.c') `
    $generated $generatedBands $generatedVQ $generatedCWRS @sources -lm -o $Output
if ($LASTEXITCODE -ne 0) { throw "gcc failed with exit code $LASTEXITCODE" }
Write-Output $Output
