# Rebuilds the instrumented-libopus CELT oracle used to debug bit-exact decoding.
# Run from anywhere (paths are absolute-ish via $PSScriptRoot).
#
#   pwsh scripts/oracle/build.ps1
#   $env:TEMP\opusoracle\oracle.exe <path-to>\testvectorNN.bit <pktIndex>
#
# The oracle decodes through ONE target packet with REAL libopus internals and
# prints, to stderr, CELT or SILK stage dumps for that target packet only, plus
# the final range vs the .bit-stored expected value.
#
# Requires: gcc (msys2 mingw64), curl, tar. No cmake/make needed.

$ErrorActionPreference = 'Stop'
$srcRoot = "$env:TEMP\opussrc"
$srcDir  = "$srcRoot\opus-1.5.2"
$bld     = "$env:TEMP\opusoracle"
New-Item -ItemType Directory -Force $srcRoot | Out-Null
New-Item -ItemType Directory -Force $bld | Out-Null

if (-not (Test-Path "$srcDir\celt\bands.c")) {
    Write-Host "Downloading opus 1.5.2 source..."
    $tar = "$srcRoot\opus.tar.gz"
    curl.exe -sL -o $tar https://github.com/xiph/opus/releases/download/v1.5.2/opus-1.5.2.tar.gz
    tar -xzf $tar -C $srcRoot
}

# Use the instrumented copies kept in the repo (this folder) instead of the
# stock celt_decoder.c / bands.c / cwrs.c.
Copy-Item "$PSScriptRoot\celt_decoder_instr.c" "$bld\celt_decoder_instr.c" -Force
Copy-Item "$PSScriptRoot\bands_instr.c"        "$bld\bands_instr.c"        -Force
Copy-Item "$PSScriptRoot\cwrs_instr.c"         "$bld\cwrs_instr.c"         -Force
Copy-Item "$PSScriptRoot\oracle.c"             "$bld\oracle.c"             -Force
Copy-Item "$PSScriptRoot\silk_trace.h"         "$bld\silk_trace.h"         -Force

$celt = "$srcDir\celt"
$silk = "$srcDir\silk"
$opusSrc = "$srcDir\src"

$mdct = Get-Content "$celt\mdct.c" -Raw
$mdct = $mdct.Replace("#include <math.h>", "#include <math.h>`r`n#include <stdio.h>`r`nextern int oracle_trace_enabled;")
$mdctDump = @'
   if (oracle_trace_enabled) {
      int _i;
      extern int oracle_mdct_dump_ch;
      extern int oracle_mdct_dump_block;
      fprintf(stderr, "[IMDCT_RAW] ch=%d block=%d N=%d", oracle_mdct_dump_ch, oracle_mdct_dump_block, N2);
      for (_i=0; _i<N2; _i++)
         fprintf(stderr, " T[%d]=%.17g", _i, (double)out[(overlap>>1)+_i]);
      fprintf(stderr, "\n");
   }

'@
$mdct = $mdct.Replace("   /* Mirror on both sides for TDAC */", $mdctDump + "   /* Mirror on both sides for TDAC */")
Set-Content "$bld\mdct_instr.c" $mdct

# Generate instrumented SILK decoder sources from the downloaded libopus source.
$decodeFrame = Get-Content "$silk\decode_frame.c" -Raw
$decodeFrame = $decodeFrame.Replace("`r`n", "`n")
$decodeFrame = $decodeFrame.Replace('#include "main.h"', "#include `"main.h`"`r`n#include `"silk_trace.h`"")
$decodeFrame = $decodeFrame.Replace("        silk_decode_indices( psDec, psRangeDec, psDec->nFramesDecoded, lostFlag, condCoding );",
@"
        silk_decode_indices( psDec, psRangeDec, psDec->nFramesDecoded, lostFlag, condCoding );
        if( oracle_trace_enabled ) {
            fprintf(stderr, "[SILK_FRAME] nFramesDecoded=%d lost=%d cond=%d L=%d fs_kHz=%d nb_subfr=%d LPC_order=%d\n",
                    psDec->nFramesDecoded, lostFlag, condCoding, psDec->frame_length,
                    psDec->fs_kHz, psDec->nb_subfr, psDec->LPC_order);
        }
        oracle_silk_range("AFTER_INDICES", psRangeDec);
"@)
$decodeFrame = $decodeFrame.Replace("        silk_decode_pulses( psRangeDec, pulses, psDec->indices.signalType,`n                psDec->indices.quantOffsetType, psDec->frame_length );",
@"
        silk_decode_pulses( psRangeDec, pulses, psDec->indices.signalType,
                psDec->indices.quantOffsetType, psDec->frame_length );
        oracle_silk_range("AFTER_PULSES", psRangeDec);
"@)
$decodeFrame = $decodeFrame.Replace("        silk_decode_parameters( psDec, psDecCtrl, condCoding );",
@"
        silk_decode_parameters( psDec, psDecCtrl, condCoding );
        oracle_silk_range("AFTER_PARAMS", psRangeDec);
"@)
$decodeFrame = $decodeFrame.Replace("        silk_decode_core( psDec, psDecCtrl, pOut, pulses, arch );",
@"
        silk_decode_core( psDec, psDecCtrl, pOut, pulses, arch );
        oracle_silk_dump_i16("CORE_OUT", pOut, psDec->frame_length);
"@)
$decodeFrame = $decodeFrame.Replace("    silk_CNG( psDec, psDecCtrl, pOut, L );",
@"
    silk_CNG( psDec, psDecCtrl, pOut, L );
    oracle_silk_dump_i16("FRAME_OUT", pOut, L);
"@)
Set-Content "$bld\decode_frame_instr.c" $decodeFrame

$decodeIndices = Get-Content "$silk\decode_indices.c" -Raw
$decodeIndices = $decodeIndices.Replace("`r`n", "`n")
$decodeIndices = $decodeIndices.Replace('#include "main.h"', "#include `"main.h`"`r`n#include `"silk_trace.h`"")
$decodeIndices = $decodeIndices.Replace("    psDec->indices.Seed = (opus_int8)ec_dec_icdf( psRangeDec, silk_uniform4_iCDF, 8 );`n}",
@"
    psDec->indices.Seed = (opus_int8)ec_dec_icdf( psRangeDec, silk_uniform4_iCDF, 8 );
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_INDICES] frame=%d cond=%d lbrr=%d sig=%d qoff=%d interp=%d lag=%d contour=%d PER=%d LTP_scale_idx=%d seed=%d\n",
                FrameIndex, condCoding, decode_LBRR, psDec->indices.signalType,
                psDec->indices.quantOffsetType, psDec->indices.NLSFInterpCoef_Q2,
                psDec->indices.lagIndex, psDec->indices.contourIndex,
                psDec->indices.PERIndex, psDec->indices.LTP_scaleIndex,
                psDec->indices.Seed);
        oracle_silk_dump_i8("GAINS_IDX", psDec->indices.GainsIndices, psDec->nb_subfr);
        oracle_silk_dump_i8("NLSF_IDX", psDec->indices.NLSFIndices, psDec->LPC_order + 1);
        oracle_silk_dump_i8("LTP_IDX", psDec->indices.LTPIndex, psDec->nb_subfr);
    }
    oracle_silk_range("INDICES", psRangeDec);
}
"@)
Set-Content "$bld\decode_indices_instr.c" $decodeIndices

$decodePulses = Get-Content "$silk\decode_pulses.c" -Raw
$decodePulses = $decodePulses.Replace("`r`n", "`n")
$decodePulses = $decodePulses.Replace('#include "main.h"', "#include `"main.h`"`r`n#include `"silk_trace.h`"")
$decodePulses = $decodePulses.Replace("    silk_decode_signs( psRangeDec, pulses, frame_length, signalType, quantOffsetType, sum_pulses );`n}",
@"
    oracle_silk_range("BEFORE_SIGNS", psRangeDec);
    silk_decode_signs( psRangeDec, pulses, frame_length, signalType, quantOffsetType, sum_pulses );
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_PULSE_HEADER] rateLevel=%d iter=%d signalType=%d qoff=%d frame_length=%d\n",
                RateLevelIndex, iter, signalType, quantOffsetType, frame_length);
        oracle_silk_dump_int("SUM_PULSES", sum_pulses, iter);
        oracle_silk_dump_int("NLSHIFTS", nLshifts, iter);
        oracle_silk_dump_i16("PULSES", pulses, frame_length);
    }
    oracle_silk_range("PULSES", psRangeDec);
}
"@)
Set-Content "$bld\decode_pulses_instr.c" $decodePulses

$decodeParameters = Get-Content "$silk\decode_parameters.c" -Raw
$decodeParameters = $decodeParameters.Replace("`r`n", "`n")
$decodeParameters = $decodeParameters.Replace('#include "main.h"', "#include `"main.h`"`r`n#include `"silk_trace.h`"")
$decodeParameters = $decodeParameters.Replace("        psDecCtrl->LTP_scale_Q14 = 0;`n    }`n}",
@"
        psDecCtrl->LTP_scale_Q14 = 0;
    }
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_PARAMS] cond=%d sig=%d LTP_scale_Q14=%d\n",
                condCoding, psDec->indices.signalType, psDecCtrl->LTP_scale_Q14);
        oracle_silk_dump_i32("GAINS_Q16", psDecCtrl->Gains_Q16, psDec->nb_subfr);
        oracle_silk_dump_i16("NLSF_Q15", pNLSF_Q15, psDec->LPC_order);
        oracle_silk_dump_i16("PREDCOEF0_Q12", psDecCtrl->PredCoef_Q12[0], psDec->LPC_order);
        oracle_silk_dump_i16("PREDCOEF1_Q12", psDecCtrl->PredCoef_Q12[1], psDec->LPC_order);
        oracle_silk_dump_int("PITCHL", psDecCtrl->pitchL, psDec->nb_subfr);
        oracle_silk_dump_i16("LTPCOEF_Q14", psDecCtrl->LTPCoef_Q14, LTP_ORDER * psDec->nb_subfr);
    }
}
"@)
Set-Content "$bld\decode_parameters_instr.c" $decodeParameters

$decodeCore = Get-Content "$silk\decode_core.c" -Raw
$decodeCore = $decodeCore.Replace("`r`n", "`n")
$decodeCore = $decodeCore.Replace('#include "main.h"', "#include `"main.h`"`r`n#include `"silk_trace.h`"")
$decodeCore = $decodeCore.Replace("    /* Copy LPC state */", "    oracle_silk_dump_i32(`"EXC_Q14`", psDec->exc_Q14, psDec->frame_length);`r`n`r`n    /* Copy LPC state */")
$decodeCore = $decodeCore.Replace("        /* Update LPC filter state */`n        silk_memcpy( sLPC_Q14, &sLPC_Q14[ psDec->subfr_length ], MAX_LPC_ORDER * sizeof( opus_int32 ) );",
@"
        if( oracle_trace_enabled ) {
            fprintf(stderr, "[SILK_CORE_SUBFR] k=%d signalType=%d gain_Q16=%d gain_Q10=%d inv_gain_Q31=%d lag=%d\n",
                    k, signalType, (int)psDecCtrl->Gains_Q16[k], (int)Gain_Q10, (int)inv_gain_Q31, lag);
            oracle_silk_dump_i32("RES_Q14", pres_Q14, psDec->subfr_length);
            oracle_silk_dump_i16("SUBFR_OUT", &xq[k * psDec->subfr_length], psDec->subfr_length);
        }

        /* Update LPC filter state */
        silk_memcpy( sLPC_Q14, &sLPC_Q14[ psDec->subfr_length ], MAX_LPC_ORDER * sizeof( opus_int32 ) );
"@)
Set-Content "$bld\decode_core_instr.c" $decodeCore

# Generate instrumented SILK encoder sources. These are used by
# `oracle.exe --silk-enc ...` to dump the libopus encoder's per-frame analysis
# and NSQ inputs for the mono SILK-only AB fixtures.
$findLPC = Get-Content "$silk\float\find_LPC_FLP.c" -Raw
$findLPC = $findLPC.Replace("`r`n", "`n")
$findLPC = $findLPC.Replace('#include "tuning_parameters.h"', "#include `"tuning_parameters.h`"`r`n#include `"silk_trace.h`"")
$findLPC = $findLPC.Replace("    res_nrg = silk_burg_modified_FLP( a, x, minInvGain, subfr_length, psEncC->nb_subfr, psEncC->predictLPCOrder, arch );",
@"
    res_nrg = silk_burg_modified_FLP( a, x, minInvGain, subfr_length, psEncC->nb_subfr, psEncC->predictLPCOrder, arch );
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_FIND_LPC] subfr_length=%d nb_subfr=%d order=%d minInvGain=%.17g full_res_nrg=%.17g useInterp=%d first=%d\n",
                subfr_length, psEncC->nb_subfr, psEncC->predictLPCOrder, (double)minInvGain, (double)res_nrg,
                psEncC->useInterpolatedNLSFs, psEncC->first_frame_after_reset);
        oracle_silk_dump_float("ENC_FIND_LPC_FULL_AR", a, psEncC->predictLPCOrder);
    }
"@)
$findLPC = $findLPC.Replace("        silk_A2NLSF_FLP( NLSF_Q15, a_tmp, psEncC->predictLPCOrder );",
@"
        silk_A2NLSF_FLP( NLSF_Q15, a_tmp, psEncC->predictLPCOrder );
        oracle_silk_dump_i16("ENC_FIND_LPC_LAST_HALF_NLSF_Q15", NLSF_Q15, psEncC->predictLPCOrder);
"@)
$findLPC = $findLPC.Replace("            /* Determine whether current interpolated NLSFs are best so far */",
@"
            if( oracle_trace_enabled ) {
                fprintf(stderr, "[SILK_ENC_NLSF_INTERP_CAND] k=%d res=%.17g best=%.17g second=%.17g\n",
                        k, (double)res_nrg_interp, (double)res_nrg, (double)res_nrg_2nd);
                oracle_silk_dump_i16("ENC_NLSF_INTERP_Q15", NLSF0_Q15, psEncC->predictLPCOrder);
                oracle_silk_dump_float("ENC_NLSF_INTERP_AR", a_tmp, psEncC->predictLPCOrder);
            }

            /* Determine whether current interpolated NLSFs are best so far */
"@)
$findLPC = $findLPC.Replace("    celt_assert( psEncC->indices.NLSFInterpCoef_Q2 == 4 ||",
@"
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_NLSF_TARGET] interp=%d\n", psEncC->indices.NLSFInterpCoef_Q2);
        oracle_silk_dump_i16("ENC_NLSF_TARGET_Q15", NLSF_Q15, psEncC->predictLPCOrder);
    }

    celt_assert( psEncC->indices.NLSFInterpCoef_Q2 == 4 ||
"@)
Set-Content "$bld\find_LPC_FLP_instr.c" $findLPC

$findPred = Get-Content "$silk\float\find_pred_coefs_FLP.c" -Raw
$findPred = $findPred.Replace("`r`n", "`n")
$findPred = $findPred.Replace('#include "main_FLP.h"', "#include `"main_FLP.h`"`r`n#include `"silk_trace.h`"")
$findPred = $findPred.Replace("    silk_process_NLSFs_FLP( &psEnc->sCmn, psEncCtrl->PredCoef, NLSF_Q15, psEnc->sCmn.prev_NLSFq_Q15 );",
@"
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
"@)
$findPred = $findPred.Replace("    silk_residual_energy_FLP( psEncCtrl->ResNrg, LPC_in_pre, psEncCtrl->PredCoef, psEncCtrl->Gains,`n        psEnc->sCmn.subfr_length, psEnc->sCmn.nb_subfr, psEnc->sCmn.predictLPCOrder );",
@"
    silk_residual_energy_FLP( psEncCtrl->ResNrg, LPC_in_pre, psEncCtrl->PredCoef, psEncCtrl->Gains,
        psEnc->sCmn.subfr_length, psEnc->sCmn.nb_subfr, psEnc->sCmn.predictLPCOrder );
    oracle_silk_dump_float("ENC_RESNRG_FLP", psEncCtrl->ResNrg, psEnc->sCmn.nb_subfr);
"@)
Set-Content "$bld\find_pred_coefs_FLP_instr.c" $findPred

$noiseShape = Get-Content "$silk\float\noise_shape_analysis_FLP.c" -Raw
$noiseShape = $noiseShape.Replace("`r`n", "`n")
$noiseShape = $noiseShape.Replace('#include "tuning_parameters.h"', "#include `"tuning_parameters.h`"`r`n#include `"silk_trace.h`"")
$noiseShape = $noiseShape.Replace("        psEncCtrl->Tilt[ k ]           = psShapeSt->Tilt_smth;`n    }`n}",
@"
        psEncCtrl->Tilt[ k ]           = psShapeSt->Tilt_smth;
    }
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_NOISE_SHAPE] signalType=%d quantOffset=%d speechActivity=%.17g inputQuality=%.17g codingQuality=%.17g\n",
                psEnc->sCmn.indices.signalType, psEnc->sCmn.indices.quantOffsetType,
                (double)( psEnc->sCmn.speech_activity_Q8 * ( 1.0f / 256.0f ) ),
                (double)psEncCtrl->input_quality, (double)psEncCtrl->coding_quality);
        oracle_silk_dump_float_strided("ENC_SHAPE_AR_FLP", psEncCtrl->AR, psEnc->sCmn.nb_subfr, MAX_SHAPE_LPC_ORDER, psEnc->sCmn.shapingLPCOrder);
        oracle_silk_dump_float("ENC_SHAPE_GAINS_PRE_FLP", psEncCtrl->Gains, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_LF_MA_FLP", psEncCtrl->LF_MA_shp, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_LF_AR_FLP", psEncCtrl->LF_AR_shp, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_TILT_FLP", psEncCtrl->Tilt, psEnc->sCmn.nb_subfr);
        oracle_silk_dump_float("ENC_SHAPE_HARM_FLP", psEncCtrl->HarmShapeGain, psEnc->sCmn.nb_subfr);
    }
}
"@)
Set-Content "$bld\noise_shape_analysis_FLP_instr.c" $noiseShape

$processGains = Get-Content "$silk\float\process_gains_FLP.c" -Raw
$processGains = $processGains.Replace("`r`n", "`n")
$processGains = $processGains.Replace('#include "tuning_parameters.h"', "#include `"tuning_parameters.h`"`r`n#include `"silk_trace.h`"")
$processGains = $processGains.Replace("    silk_assert( psEncCtrl->Lambda > 0.0f );",
@"
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
"@)
Set-Content "$bld\process_gains_FLP_instr.c" $processGains

$wrappers = Get-Content "$silk\float\wrappers_FLP.c" -Raw
$wrappers = $wrappers.Replace("`r`n", "`n")
$wrappers = $wrappers.Replace('#include "main_FLP.h"', "#include `"main_FLP.h`"`r`n#include `"silk_trace.h`"")
$wrappers = $wrappers.Replace("    /* Call NSQ */",
@"
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
"@)
$wrappers = $wrappers.Replace("    } else {`n        silk_NSQ( &psEnc->sCmn, psNSQ, psIndices, x16, pulses, PredCoef_Q12[ 0 ], LTPCoef_Q14,`n            AR_Q13, HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, psEncCtrl->pitchL, Lambda_Q10, LTP_scale_Q14, psEnc->sCmn.arch );`n    }`n}",
@"
    } else {
        silk_NSQ( &psEnc->sCmn, psNSQ, psIndices, x16, pulses, PredCoef_Q12[ 0 ], LTPCoef_Q14,
            AR_Q13, HarmShapeGain_Q14, Tilt_Q14, LF_shp_Q14, Gains_Q16, psEncCtrl->pitchL, Lambda_Q10, LTP_scale_Q14, psEnc->sCmn.arch );
    }
    oracle_silk_dump_i8("ENC_NSQ_PULSES", pulses, psEnc->sCmn.frame_length);
}
"@)
Set-Content "$bld\wrappers_FLP_instr.c" $wrappers

$nsqDelDec = Get-Content "$silk\NSQ_del_dec.c" -Raw
$nsqDelDec = $nsqDelDec.Replace("`r`n", "`n")
$nsqDelDec = $nsqDelDec.Replace('#include "stack_alloc.h"', "#include `"stack_alloc.h`"`r`n#include `"silk_trace.h`"")
$nsqDelDec = $nsqDelDec.Replace("    opus_int16          *pxq;",
@"
    opus_int16          *pxq;
    opus_int8           *pulses_start;
    opus_int16          *pxq_start;
"@)
$nsqDelDec = $nsqDelDec.Replace("    pxq                   = &NSQ->xq[ psEncC->ltp_mem_length ];",
@"
    pxq                   = &NSQ->xq[ psEncC->ltp_mem_length ];
    pulses_start          = pulses;
    pxq_start             = pxq;
"@)
$nsqDelDec = $nsqDelDec.Replace("    /* Save quantized speech signal */",
@"
    if( oracle_trace_enabled ) {
        fprintf(stderr, "[SILK_ENC_NSQ_DEL_DEC_DONE] winner=%d rd=%d seed=%d lagPrev=%d\n",
                Winner_ind, (int)RDmin_Q10, psIndices->Seed, NSQ->lagPrev);
        oracle_silk_dump_i8("ENC_NSQ_DEL_DEC_PULSES", pulses_start, psEncC->frame_length);
        oracle_silk_dump_i16("ENC_NSQ_DEL_DEC_XQ", pxq_start, psEncC->frame_length);
    }

    /* Save quantized speech signal */
"@)
Set-Content "$bld\NSQ_del_dec_instr.c" $nsqDelDec

# Full libopus decoder sources, with the CELT/SILK decoder files replaced by
# generated instrumented copies.
$celtStock = @('celt','celt_encoder','celt_lpc','entcode','entdec','entenc','kiss_fft','laplace',
           'mathops','modes','pitch','quant_bands','rate','vq') |
         ForEach-Object { "$celt\$_.c" }
$silkStockNames = @(
  'CNG','code_signs','init_decoder','decoder_set_fs','dec_API','enc_API',
  'encode_indices','encode_pulses','gain_quant','interpolate','LP_variable_cutoff',
  'NLSF_decode','NSQ','PLC','shell_coder','tables_gain','tables_LTP',
  'tables_NLSF_CB_NB_MB','tables_NLSF_CB_WB','tables_other','tables_pitch_lag',
  'tables_pulses_per_block','VAD','control_audio_bandwidth','quant_LTP_gains',
  'VQ_WMat_EC','HP_variable_cutoff','NLSF_encode','NLSF_VQ','NLSF_unpack',
  'NLSF_del_dec_quant','process_NLSFs','stereo_LR_to_MS','stereo_MS_to_LR',
  'check_control_input','control_SNR','init_encoder','control_codec','A2NLSF',
  'ana_filt_bank_1','biquad_alt','bwexpander_32','bwexpander','debug','decode_pitch',
  'inner_prod_aligned','lin2log','log2lin','LPC_analysis_filter','LPC_inv_pred_gain',
  'table_LSF_cos','NLSF2A','NLSF_stabilize','NLSF_VQ_weights_laroia','pitch_est_tables',
  'resampler','resampler_down2_3','resampler_down2','resampler_private_AR2',
  'resampler_private_down_FIR','resampler_private_IIR_FIR','resampler_private_up2_HQ',
  'resampler_rom','sigm_Q15','sort','sum_sqr_shift','stereo_decode_pred',
  'stereo_encode_pred','stereo_find_predictor','stereo_quant_pred','LPC_fit'
)
$silkStock = $silkStockNames | ForEach-Object { "$silk\$_.c" }
$silkFloatNames = @('apply_sine_window_FLP','corrMatrix_FLP','encode_frame_FLP',
  'find_LTP_FLP','find_pitch_lags_FLP','LPC_analysis_filter_FLP',
  'LTP_analysis_filter_FLP','LTP_scale_ctrl_FLP',
  'regularize_correlations_FLP','residual_energy_FLP',
  'warped_autocorrelation_FLP','autocorrelation_FLP','burg_modified_FLP',
  'bwexpander_FLP','energy_FLP','inner_product_FLP','k2a_FLP','LPC_inv_pred_gain_FLP',
  'pitch_analysis_core_FLP','scale_copy_vector_FLP','scale_vector_FLP','schur_FLP','sort_FLP')
$silkFloat = $silkFloatNames | ForEach-Object { "$silk\float\$_.c" }
$opusStock = @('opus','opus_decoder','opus_encoder','extensions','opus_multistream',
  'opus_multistream_encoder','opus_multistream_decoder','repacketizer',
  'opus_projection_encoder','opus_projection_decoder','mapping_matrix',
  'analysis','mlp','mlp_data') | ForEach-Object { "$opusSrc\$_.c" }
$srcs = $celtStock + @("$bld\mdct_instr.c","$bld\bands_instr.c","$bld\cwrs_instr.c","$bld\celt_decoder_instr.c") +
        $silkStock + $silkFloat +
        @("$bld\decode_frame_instr.c","$bld\decode_indices_instr.c","$bld\decode_pulses_instr.c",
          "$bld\decode_parameters_instr.c","$bld\decode_core_instr.c",
          "$bld\find_LPC_FLP_instr.c","$bld\find_pred_coefs_FLP_instr.c",
          "$bld\noise_shape_analysis_FLP_instr.c","$bld\process_gains_FLP_instr.c",
          "$bld\wrappers_FLP_instr.c","$bld\NSQ_del_dec_instr.c") +
        $opusStock + @("$bld\oracle.c")
$inc  = @("-I$bld","-I$celt","-I$srcDir\include","-I$srcDir","-I$srcDir\silk","-I$srcDir\silk\float","-I$srcDir\src")

# NOTE: build WITHOUT -DCUSTOM_MODES so the exact static 48000/960 mode (shipped tables) is used.
& gcc -O1 -DOPUS_BUILD -DVAR_ARRAYS @inc @srcs -lm -o "$bld\oracle.exe"
if ($LASTEXITCODE -eq 0) { Write-Host "OK: $bld\oracle.exe" } else { Write-Host "BUILD FAILED" }
