# Builds the libopus 1.6.1 *encoder* input-pipeline oracle from the checked-in
# source tree (repo `libopus/`, the bit-exact convergence target).
#
#   pwsh scripts/oracle/build_encoder.ps1
#   $env:TEMP\opusoracle\enc_oracle.exe --silk-enc <rate> <fixture> [targetFrame|-1] [frames] [bitrate] [bandwidth]
#
# The exe runs the real opus_encode_float with the AB-test settings and prints,
# to stderr, for every traced frame: the conditioned pcm_buf that CELT/SILK
# consume ([ENC_INPUT]/[ENC_PCM_BUF]), the SILK int16 inputBuf and float x_buf
# ([SILK_ENC_XFRAME_INFO]/[SILK_ENC_INPUTBUF]/[SILK_ENC_XBUF]), and the packet
# bytes ([ENC_PACKET]). Consumed by opus_silk_enc_oracle_test.go (opusref tag).
#
# Requires: gcc (msys2 mingw64). Scalar build (no SIMD, no FMA): -O1 only.

param([string]$Source = "")

$ErrorActionPreference = 'Stop'
$bld = "$env:TEMP\opusoracle"
New-Item -ItemType Directory -Force $bld | Out-Null

$srcDir = if ($Source -ne "") { (Resolve-Path $Source).Path } else { (Resolve-Path (Join-Path $PSScriptRoot "..\..\libopus")).Path }
Write-Host "libopus source: $srcDir"
if (-not (Test-Path "$srcDir\silk\enc_API.c")) { throw "not a libopus source tree: $srcDir" }

$celt = "$srcDir\celt"
$silk = "$srcDir\silk"
$opusSrc = "$srcDir\src"

function Replace-Checked([string]$text, [string]$old, [string]$new, [string]$label) {
    if (-not $text.Contains($old)) { throw "instrumentation anchor not found: $label" }
    return $text.Replace($old, $new)
}

Copy-Item "$PSScriptRoot\silk_trace.h" "$bld\silk_trace.h" -Force
Copy-Item "$PSScriptRoot\enc_oracle.c" "$bld\enc_oracle.c" -Force

# opus_encoder.c: dump pcm_buf right after the input conditioning.
$opusEnc = (Get-Content "$opusSrc\opus_encoder.c" -Raw).Replace("`r`n", "`n")
$opusEnc = Replace-Checked $opusEnc '#include "opus_private.h"' "#include `"opus_private.h`"`n#include <stdio.h>`nextern int oracle_trace_enabled;" "opus_encoder include"
$pcmBufDump = @'
    (void)float_api;
#endif
    if (oracle_trace_enabled) {
        int _i, _n = (total_buffer+frame_size)*st->channels;
        fprintf(stderr, "[ENC_INPUT] frame_size=%d total_buffer=%d channels=%d mode=%d cutoff_Hz=%d smth2=%d hp_freq_smth1=%d\n",
                frame_size, total_buffer, st->channels, st->mode, cutoff_Hz, st->variable_HP_smth2_Q15, hp_freq_smth1);
        fprintf(stderr, "[ENC_PCM_BUF] n=%d", _n);
        for (_i = 0; _i < _n; _i++) fprintf(stderr, " v[%d]=%.17g", _i, (double)pcm_buf[_i]);
        fprintf(stderr, "\n");
    }
'@
$opusEnc = Replace-Checked $opusEnc "    (void)float_api;`n#endif`n" ($pcmBufDump.Replace("`r`n", "`n") + "`n") "opus_encoder pcm_buf"
Set-Content "$bld\opus_encoder_instr.c" $opusEnc -NoNewline

# encode_frame_FLP.c: dump inputBuf and x_buf once the new frame is in place.
$encFrame = (Get-Content "$silk\float\encode_frame_FLP.c" -Raw).Replace("`r`n", "`n")
$encFrame = Replace-Checked $encFrame '#include "stack_alloc.h"' "#include `"stack_alloc.h`"`n#include `"silk_trace.h`"" "encode_frame include"
$xbufDump = @'
        x_frame[ LA_SHAPE_MS * psEnc->sCmn.fs_kHz + i * ( psEnc->sCmn.frame_length >> 3 ) ] += ( 1 - ( i & 2 ) ) * 1e-6f;
    }
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_XFRAME_INFO] nFramesEncoded=%d frame_length=%d fs_kHz=%d ltp_mem_length=%d la_shape=%d first=%d prefill=%d speech_activity_Q8=%d variable_HP_smth1_Q15=%d\n",
                psEnc->sCmn.nFramesEncoded, psEnc->sCmn.frame_length, psEnc->sCmn.fs_kHz, psEnc->sCmn.ltp_mem_length,
                LA_SHAPE_MS * psEnc->sCmn.fs_kHz, psEnc->sCmn.first_frame_after_reset, psEnc->sCmn.prefillFlag,
                psEnc->sCmn.speech_activity_Q8, psEnc->sCmn.variable_HP_smth1_Q15);
        oracle_silk_dump_i16("ENC_INPUTBUF", psEnc->sCmn.inputBuf, psEnc->sCmn.frame_length + 2);
        oracle_silk_dump_float("ENC_XBUF", psEnc->x_buf, psEnc->sCmn.ltp_mem_length + psEnc->sCmn.frame_length + LA_SHAPE_MS * psEnc->sCmn.fs_kHz);
    }
'@
$encFrame = Replace-Checked $encFrame "        x_frame[ LA_SHAPE_MS * psEnc->sCmn.fs_kHz + i * ( psEnc->sCmn.frame_length >> 3 ) ] += ( 1 - ( i & 2 ) ) * 1e-6f;`n    }`n" ($xbufDump.Replace("`r`n", "`n") + "`n") "encode_frame x_buf"
Set-Content "$bld\encode_frame_FLP_instr.c" $encFrame -NoNewline

$celtSrcs = Get-ChildItem "$celt\*.c" | Where-Object { $_.Name -notmatch '^(opus_custom_demo|dump_modes|.*_test.*)' } | ForEach-Object { $_.FullName }
$silkSrcs = Get-ChildItem "$silk\*.c" | ForEach-Object { $_.FullName }
$silkFloat = Get-ChildItem "$silk\float\*.c" | Where-Object { $_.Name -ne 'encode_frame_FLP.c' } | ForEach-Object { $_.FullName }
$opusStock = @('opus','opus_decoder','extensions','opus_multistream','opus_multistream_encoder',
  'opus_multistream_decoder','repacketizer','opus_projection_encoder','opus_projection_decoder',
  'mapping_matrix','analysis','mlp','mlp_data') | ForEach-Object { "$opusSrc\$_.c" }
$srcs = $celtSrcs + $silkSrcs + $silkFloat + $opusStock +
        @("$bld\encode_frame_FLP_instr.c", "$bld\opus_encoder_instr.c", "$bld\enc_oracle.c")
$inc = @("-I$bld", "-I$celt", "-I$srcDir\include", "-I$srcDir", "-I$silk", "-I$silk\float", "-I$opusSrc")

# Float build, no SIMD/RTCD, no FMA contraction: plain scalar C like the
# cgoref arch=0 comparisons. No CUSTOM_MODES so the static 48000/960 mode is used.
& gcc -O1 -ffp-contract=off -DOPUS_BUILD -DVAR_ARRAYS -DHAVE_LRINTF @inc @srcs -lm -o "$bld\enc_oracle.exe"
if ($LASTEXITCODE -eq 0) { Write-Host "OK: $bld\enc_oracle.exe" } else { Write-Host "BUILD FAILED"; exit 1 }
