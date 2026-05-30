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

# Full libopus decoder sources, with the CELT/SILK decoder files replaced by
# generated instrumented copies.
$celtStock = @('celt','celt_encoder','celt_lpc','entcode','entdec','entenc','kiss_fft','laplace',
           'mathops','modes','pitch','quant_bands','rate','vq') |
         ForEach-Object { "$celt\$_.c" }
$silkStockNames = @(
  'CNG','code_signs','init_decoder','decoder_set_fs','dec_API','enc_API',
  'encode_indices','encode_pulses','gain_quant','interpolate','LP_variable_cutoff',
  'NLSF_decode','NSQ','NSQ_del_dec','PLC','shell_coder','tables_gain','tables_LTP',
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
$silkFloatNames = @('apply_sine_window_FLP','corrMatrix_FLP','encode_frame_FLP','find_LPC_FLP',
  'find_LTP_FLP','find_pitch_lags_FLP','find_pred_coefs_FLP','LPC_analysis_filter_FLP',
  'LTP_analysis_filter_FLP','LTP_scale_ctrl_FLP','noise_shape_analysis_FLP',
  'process_gains_FLP','regularize_correlations_FLP','residual_energy_FLP',
  'warped_autocorrelation_FLP','wrappers_FLP','autocorrelation_FLP','burg_modified_FLP',
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
          "$bld\decode_parameters_instr.c","$bld\decode_core_instr.c") +
        $opusStock + @("$bld\oracle.c")
$inc  = @("-I$bld","-I$celt","-I$srcDir\include","-I$srcDir","-I$srcDir\silk","-I$srcDir\silk\float","-I$srcDir\src")

# NOTE: build WITHOUT -DCUSTOM_MODES so the exact static 48000/960 mode (shipped tables) is used.
& gcc -O1 -DOPUS_BUILD -DVAR_ARRAYS @inc @srcs -lm -o "$bld\oracle.exe"
if ($LASTEXITCODE -eq 0) { Write-Host "OK: $bld\oracle.exe" } else { Write-Host "BUILD FAILED" }
