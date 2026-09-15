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

# SILK encoder analysis-stage instrumentation (shared with the legacy build.ps1
# blocks, but every anchor is checked against the 1.6.1 sources).
# Generate instrumented SILK encoder sources. These are used by
# `oracle.exe --silk-enc ...` to dump the libopus encoder's per-frame analysis
# and NSQ inputs for the mono SILK-only AB fixtures.
$findLPC = Get-Content "$silk\float\find_LPC_FLP.c" -Raw
$findLPC = $findLPC.Replace("`r`n", "`n")
$findLPC = Replace-Checked $findLPC '#include "tuning_parameters.h"' "#include `"tuning_parameters.h`"`r`n#include `"silk_trace.h`"" "stage anchor 1"
$__old = "    res_nrg = silk_burg_modified_FLP( a, x, minInvGain, subfr_length, psEncC->nb_subfr, psEncC->predictLPCOrder, arch );"
$__new = @"
    res_nrg = silk_burg_modified_FLP( a, x, minInvGain, subfr_length, psEncC->nb_subfr, psEncC->predictLPCOrder, arch );
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_FIND_LPC] subfr_length=%d nb_subfr=%d order=%d minInvGain=%.17g full_res_nrg=%.17g useInterp=%d first=%d\n",
                subfr_length, psEncC->nb_subfr, psEncC->predictLPCOrder, (double)minInvGain, (double)res_nrg,
                psEncC->useInterpolatedNLSFs, psEncC->first_frame_after_reset);
        oracle_silk_dump_float("ENC_FIND_LPC_FULL_AR", a, psEncC->predictLPCOrder);
    }
"@
$findLPC = Replace-Checked $findLPC $__old ($__new.Replace("`r`n", "`n")) "stage anchor 2"
$__old = "        silk_A2NLSF_FLP( NLSF_Q15, a_tmp, psEncC->predictLPCOrder );"
$__new = @"
        silk_A2NLSF_FLP( NLSF_Q15, a_tmp, psEncC->predictLPCOrder );
        oracle_silk_dump_i16("ENC_FIND_LPC_LAST_HALF_NLSF_Q15", NLSF_Q15, psEncC->predictLPCOrder);
"@
$findLPC = Replace-Checked $findLPC $__old ($__new.Replace("`r`n", "`n")) "stage anchor 3"
$__old = "            /* Determine whether current interpolated NLSFs are best so far */"
$__new = @"
            if( oracle_trace_enabled ) {
                fprintf(stderr, "[SILK_ENC_NLSF_INTERP_CAND] k=%d res=%.17g best=%.17g second=%.17g\n",
                        k, (double)res_nrg_interp, (double)res_nrg, (double)res_nrg_2nd);
                oracle_silk_dump_i16("ENC_NLSF_INTERP_Q15", NLSF0_Q15, psEncC->predictLPCOrder);
                oracle_silk_dump_float("ENC_NLSF_INTERP_AR", a_tmp, psEncC->predictLPCOrder);
            }

            /* Determine whether current interpolated NLSFs are best so far */
"@
$findLPC = Replace-Checked $findLPC $__old ($__new.Replace("`r`n", "`n")) "stage anchor 4"
$__old = "    celt_assert( psEncC->indices.NLSFInterpCoef_Q2 == 4 ||"
$__new = @"
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_NLSF_TARGET] interp=%d\n", psEncC->indices.NLSFInterpCoef_Q2);
        oracle_silk_dump_i16("ENC_NLSF_TARGET_Q15", NLSF_Q15, psEncC->predictLPCOrder);
    }

    celt_assert( psEncC->indices.NLSFInterpCoef_Q2 == 4 ||
"@
$findLPC = Replace-Checked $findLPC $__old ($__new.Replace("`r`n", "`n")) "stage anchor 5"
Set-Content "$bld\find_LPC_FLP_instr.c" $findLPC

$findPred = Get-Content "$silk\float\find_pred_coefs_FLP.c" -Raw
$findPred = $findPred.Replace("`r`n", "`n")
$findPred = Replace-Checked $findPred '#include "main_FLP.h"' "#include `"main_FLP.h`"`r`n#include `"silk_trace.h`"" "stage anchor 6"
$__old = "    silk_process_NLSFs_FLP( &psEnc->sCmn, psEncCtrl->PredCoef, NLSF_Q15, psEnc->sCmn.prev_NLSFq_Q15 );"
$__new = @"
    silk_process_NLSFs_FLP( &psEnc->sCmn, psEncCtrl->PredCoef, NLSF_Q15, psEnc->sCmn.prev_NLSFq_Q15 );
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_PRED_COEFS] signalType=%d interp=%d LTPredCodGain=%.17g\n",
                psEnc->sCmn.indices.signalType, psEnc->sCmn.indices.NLSFInterpCoef_Q2,
                (double)psEncCtrl->LTPredCodGain);
        oracle_silk_dump_i16("ENC_NLSF_QUANT_Q15", NLSF_Q15, psEnc->sCmn.predictLPCOrder);
        oracle_silk_dump_float("ENC_PREDCOEF0_FLP", psEncCtrl->PredCoef[0], psEnc->sCmn.predictLPCOrder);
        oracle_silk_dump_float("ENC_PREDCOEF1_FLP", psEncCtrl->PredCoef[1], psEnc->sCmn.predictLPCOrder);
        oracle_silk_dump_float("ENC_LTP_COEF_FLP", psEncCtrl->LTPCoef, psEnc->sCmn.nb_subfr * LTP_ORDER);
        oracle_silk_dump_int("ENC_PITCHL", psEncCtrl->pitchL, psEnc->sCmn.nb_subfr);
    }
"@
$findPred = Replace-Checked $findPred $__old ($__new.Replace("`r`n", "`n")) "stage anchor 7"
$__old = "    silk_residual_energy_FLP( psEncCtrl->ResNrg, LPC_in_pre, psEncCtrl->PredCoef, psEncCtrl->Gains,`n        psEnc->sCmn.subfr_length, psEnc->sCmn.nb_subfr, psEnc->sCmn.predictLPCOrder );"
$__new = @"
    silk_residual_energy_FLP( psEncCtrl->ResNrg, LPC_in_pre, psEncCtrl->PredCoef, psEncCtrl->Gains,
        psEnc->sCmn.subfr_length, psEnc->sCmn.nb_subfr, psEnc->sCmn.predictLPCOrder );
    oracle_silk_dump_float("ENC_RESNRG_FLP", psEncCtrl->ResNrg, psEnc->sCmn.nb_subfr);
"@
$findPred = Replace-Checked $findPred $__old ($__new.Replace("`r`n", "`n")) "stage anchor 8"
Set-Content "$bld\find_pred_coefs_FLP_instr.c" $findPred

$noiseShape = Get-Content "$silk\float\noise_shape_analysis_FLP.c" -Raw
$noiseShape = $noiseShape.Replace("`r`n", "`n")
$noiseShape = Replace-Checked $noiseShape '#include "tuning_parameters.h"' "#include `"tuning_parameters.h`"`r`n#include `"silk_trace.h`"" "stage anchor 9"
$__old = "        psEncCtrl->Tilt[ k ]           = psShapeSt->Tilt_smth;`n    }`n}"
$__new = @"
        psEncCtrl->Tilt[ k ]           = psShapeSt->Tilt_smth;
    }
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_NOISE_SHAPE] signalType=%d quantOffset=%d speechActivity=%.17g inputQuality=%.17g codingQuality=%.17g SNR_dB_Q7=%d warping_Q16=%d predGain=%.17g LTPCorr=%.17g useCBR=%d shapeWinLength=%d\n",
                psEnc->sCmn.indices.signalType, psEnc->sCmn.indices.quantOffsetType,
                (double)( psEnc->sCmn.speech_activity_Q8 * ( 1.0f / 256.0f ) ),
                (double)psEncCtrl->input_quality, (double)psEncCtrl->coding_quality,
                psEnc->sCmn.SNR_dB_Q7, psEnc->sCmn.warping_Q16, (double)psEncCtrl->predGain, (double)psEnc->LTPCorr,
                psEnc->sCmn.useCBR, psEnc->sCmn.shapeWinLength);
        oracle_silk_dump_float_strided("ENC_SHAPE_AR_FLP", psEncCtrl->AR, psEnc->sCmn.nb_subfr, MAX_SHAPE_LPC_ORDER, psEnc->sCmn.shapingLPCOrder);
        oracle_silk_dump_float("ENC_SHAPE_GAINS_PRE_FLP", psEncCtrl->Gains, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_LF_MA_FLP", psEncCtrl->LF_MA_shp, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_LF_AR_FLP", psEncCtrl->LF_AR_shp, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_TILT_FLP", psEncCtrl->Tilt, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_HARM_FLP", psEncCtrl->HarmShapeGain, psEnc->sCmn.nb_subfr);
    }
}
"@
$noiseShape = Replace-Checked $noiseShape $__old ($__new.Replace("`r`n", "`n")) "stage anchor 10"
Set-Content "$bld\noise_shape_analysis_FLP_instr.c" $noiseShape

$processGains = Get-Content "$silk\float\process_gains_FLP.c" -Raw
$processGains = $processGains.Replace("`r`n", "`n")
$processGains = Replace-Checked $processGains '#include "tuning_parameters.h"' "#include `"tuning_parameters.h`"`r`n#include `"silk_trace.h`"" "stage anchor 11"
$__old = "    silk_assert( psEncCtrl->Lambda > 0.0f );"
$__new = @"
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_PROCESS_GAINS] cond=%d signalType=%d quantOffset=%d lastGainPrev=%d Lambda=%.17g\n",
                condCoding, psEnc->sCmn.indices.signalType, psEnc->sCmn.indices.quantOffsetType,
                psEncCtrl->lastGainIndexPrev, (double)psEncCtrl->Lambda);
        oracle_silk_dump_float("ENC_GAINS_FLP", psEncCtrl->Gains, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_i32("ENC_GAINS_UNQ_Q16", psEncCtrl->GainsUnq_Q16, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_i8("ENC_GAINS_IDX", psEnc->sCmn.indices.GainsIndices, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_scalar("ENC_LAMBDA_FLP", psEncCtrl->Lambda);
    }

    silk_assert( psEncCtrl->Lambda > 0.0f );
"@
$processGains = Replace-Checked $processGains $__old ($__new.Replace("`r`n", "`n")) "stage anchor 12"
Set-Content "$bld\process_gains_FLP_instr.c" $processGains

$wrappers = Get-Content "$silk\float\wrappers_FLP.c" -Raw
$wrappers = $wrappers.Replace("`r`n", "`n")
$wrappers = Replace-Checked $wrappers '#include "main_FLP.h"' "#include `"main_FLP.h`"`r`n#include `"silk_trace.h`"" "stage anchor 13"
$__old = "    /* Call NSQ */"
$__new = @"
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_NSQ_INPUT] signalType=%d quantOffset=%d seed=%d Lambda_Q10=%d LTP_scale_Q14=%d\n",
                psIndices->signalType, psIndices->quantOffsetType, psIndices->Seed, Lambda_Q10, LTP_scale_Q14);
        oracle_silk_dump_i16("ENC_NSQ_X16", x16, psEnc->sCmn.frame_length);
        oracle_silk_dump_i16_strided("ENC_NSQ_PREDCOEF_Q12", &PredCoef_Q12[0][0], 2, MAX_LPC_ORDER, psEnc->sCmn.predictLPCOrder);
        {
            int sf, j;
            int lsf_interpolation_flag = psIndices->NLSFInterpCoef_Q2 == 4 ? 0 : 1;
            fprintf(stderr, "[SILK_ENC_NSQ_SUBFR_PREDCOEF_Q12] rows=%d cols=%d", psEnc->sCmn.nb_subfr, psEnc->sCmn.predictLPCOrder);
            for( sf = 0; sf < psEnc->sCmn.nb_subfr; sf++ ) {
                int row = ( sf >> 1 ) | ( 1 - lsf_interpolation_flag );
                for( j = 0; j < psEnc->sCmn.predictLPCOrder; j++ ) {
                    fprintf(stderr, " v[%d,%d]=%d", sf, j, (int)PredCoef_Q12[row][j]);
                }
            }
            fprintf(stderr, "\n");
        }
        oracle_silk_dump_i16("ENC_NSQ_LTPCOEF_Q14", LTPCoef_Q14, psEnc->sCmn.nb_subfr * LTP_ORDER);
        oracle_silk_dump_i16_strided("ENC_NSQ_AR_Q13", AR_Q13, psEnc->sCmn.nb_subfr, MAX_SHAPE_LPC_ORDER, psEnc->sCmn.shapingLPCOrder);
        oracle_silk_dump_i32("ENC_NSQ_GAINS_Q16", Gains_Q16, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_int("ENC_NSQ_PITCHL", psEncCtrl->pitchL, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_int("ENC_NSQ_TILT_Q14", Tilt_Q14, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_int("ENC_NSQ_HARM_Q14", HarmShapeGain_Q14, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_i32("ENC_NSQ_LF_Q14", LF_shp_Q14, psEnc->sCmn.nb_subfr);
    }

    /* Call NSQ */
"@
$wrappers = Replace-Checked $wrappers $__old ($__new.Replace("`r`n", "`n")) "stage anchor 14"
$__old = "    } else {`n        silk_NSQ( &psEnc->sCmn, psNSQ, psIndices, x16, pulses, PredCoef_Q12[ 0 ], LTPCoef_Q14,`n            AR_Q13, HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, psEncCtrl->pitchL, Lambda_Q10, LTP_scale_Q14, psEnc->sCmn.arch );`n    }`n}"
$__new = @"
    } else {
        silk_NSQ( &psEnc->sCmn, psNSQ, psIndices, x16, pulses, PredCoef_Q12[ 0 ], LTPCoef_Q14,
            AR_Q13, HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, psEncCtrl->pitchL, Lambda_Q10, LTP_scale_Q14, psEnc->sCmn.arch );
    }
    oracle_silk_dump_i8("ENC_NSQ_PULSES", pulses, psEnc->sCmn.frame_length);
}
"@
$wrappers = Replace-Checked $wrappers $__old ($__new.Replace("`r`n", "`n")) "stage anchor 15"
Set-Content "$bld\wrappers_FLP_instr.c" $wrappers

$nsqDelDec = Get-Content "$silk\NSQ_del_dec.c" -Raw
$nsqDelDec = $nsqDelDec.Replace("`r`n", "`n")
$nsqDelDec = Replace-Checked $nsqDelDec '#include "stack_alloc.h"' "#include `"stack_alloc.h`"`r`n#include `"silk_trace.h`"" "stage anchor 16"
$__old = "    opus_int16          *pxq;"
$__new = @"
    opus_int16          *pxq;
    opus_int8           *pulses_start;
    opus_int16          *pxq_start;
"@
$nsqDelDec = Replace-Checked $nsqDelDec $__old ($__new.Replace("`r`n", "`n")) "stage anchor 17"
$__old = "    pxq                   = &NSQ->xq[ psEncC->ltp_mem_length ];"
$__new = @"
    pxq                   = &NSQ->xq[ psEncC->ltp_mem_length ];
    pulses_start          = pulses;
    pxq_start             = pxq;
"@
$nsqDelDec = Replace-Checked $nsqDelDec $__old ($__new.Replace("`r`n", "`n")) "stage anchor 18"
$__old = "    /* Save quantized speech signal */"
$__new = @"
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_NSQ_DEL_DEC_DONE] winner=%d rd=%d seed=%d lagPrev=%d\n",
                Winner_ind, (int)RDmin_Q10, psIndices->Seed, NSQ->lagPrev);
        oracle_silk_dump_i8("ENC_NSQ_DEL_DEC_PULSES", pulses_start, psEncC->frame_length);
        oracle_silk_dump_i16("ENC_NSQ_DEL_DEC_XQ", pxq_start, psEncC->frame_length);
    }

    /* Save quantized speech signal */
"@
$nsqDelDec = Replace-Checked $nsqDelDec $__old ($__new.Replace("`r`n", "`n")) "stage anchor 19"
Set-Content "$bld\NSQ_del_dec_instr.c" $nsqDelDec

# enc_API.c: dump the per-frame SILK target rate derivation.
$encAPI = (Get-Content "$silk\enc_API.c" -Raw).Replace("`r`n", "`n")
$encAPI = Replace-Checked $encAPI '#include "tuning_parameters.h"' "#include `"tuning_parameters.h`"`n#include `"silk_trace.h`"" "enc_API include"
$targetDump = @'
            TargetRate_bps = silk_LIMIT( TargetRate_bps, encControl->bitRate, 5000 );
            if( oracle_trace_enabled ) {
                fprintf(stderr, "[SILK_ENC_TARGET] bitRate=%d payloadSize_ms=%d nBits=%d TargetRate_bps=%d nBitsExceeded=%d nBitsUsedLBRR=%d curr_nBitsUsedLBRR=%d nFramesEncoded=%d nFramesPerPacket=%d tell=%d useCBR=%d maxBits=%d\n",
                        encControl->bitRate, encControl->payloadSize_ms, nBits, TargetRate_bps, psEnc->nBitsExceeded, psEnc->nBitsUsedLBRR, curr_nBitsUsedLBRR,
                        psEnc->state_Fxx[ 0 ].sCmn.nFramesEncoded, psEnc->state_Fxx[ 0 ].sCmn.nFramesPerPacket, ec_tell( psRangeEnc ), encControl->useCBR, encControl->maxBits);
            }
'@
$encAPI = Replace-Checked $encAPI "            TargetRate_bps = silk_LIMIT( TargetRate_bps, encControl->bitRate, 5000 );`n" ($targetDump.Replace("`r`n", "`n") + "`n") "enc_API target rate"
Set-Content "$bld\enc_API_instr.c" $encAPI -NoNewline

$celtSrcs = Get-ChildItem "$celt\*.c" | Where-Object { $_.Name -notmatch '^(opus_custom_demo|dump_modes|.*_test.*)' } | ForEach-Object { $_.FullName }
$silkSrcs = Get-ChildItem "$silk\*.c" | ForEach-Object { $_.FullName }
$instrFloat = @('encode_frame_FLP.c','find_LPC_FLP.c','find_pred_coefs_FLP.c','noise_shape_analysis_FLP.c','process_gains_FLP.c','wrappers_FLP.c')
$silkSrcs = $silkSrcs | Where-Object { $_ -notmatch 'NSQ_del_dec\.c$' -and $_ -notmatch 'enc_API\.c$' }
$silkFloat = Get-ChildItem "$silk\float\*.c" | Where-Object { $instrFloat -notcontains $_.Name } | ForEach-Object { $_.FullName }
$opusStock = @('opus','opus_decoder','extensions','opus_multistream','opus_multistream_encoder',
  'opus_multistream_decoder','repacketizer','opus_projection_encoder','opus_projection_decoder',
  'mapping_matrix','analysis','mlp','mlp_data') | ForEach-Object { "$opusSrc\$_.c" }
$srcs = $celtSrcs + $silkSrcs + $silkFloat + $opusStock +
        @("$bld\encode_frame_FLP_instr.c", "$bld\opus_encoder_instr.c",
          "$bld\find_LPC_FLP_instr.c", "$bld\find_pred_coefs_FLP_instr.c", "$bld\noise_shape_analysis_FLP_instr.c",
          "$bld\process_gains_FLP_instr.c", "$bld\wrappers_FLP_instr.c", "$bld\NSQ_del_dec_instr.c", "$bld\enc_API_instr.c",
          "$bld\enc_oracle.c")
$inc = @("-I$bld", "-I$celt", "-I$srcDir\include", "-I$srcDir", "-I$silk", "-I$silk\float", "-I$opusSrc")

# Float build, no SIMD/RTCD, no FMA contraction: plain scalar C like the
# cgoref arch=0 comparisons. No CUSTOM_MODES so the static 48000/960 mode is used.
& gcc -O1 -ffp-contract=off -DOPUS_BUILD -DVAR_ARRAYS -DHAVE_LRINTF @inc @srcs -lm -o "$bld\enc_oracle.exe"
if ($LASTEXITCODE -eq 0) { Write-Host "OK: $bld\enc_oracle.exe" } else { Write-Host "BUILD FAILED"; exit 1 }
