package silk

// tables.go — ICDF tables ported verbatim from libopus silk/tables_*.c
// All values are uint8 inverse-CDF arrays for use with ec_dec_icdf (ftb=8, ft=256).

// ── Gain tables (silk/tables_gain.c) ────────────────────────────────────────

// silkGainICDF is the first-subframe gain iCDF [signalType][8].
// silk_gain_iCDF[3][N_LEVELS_QGAIN/8] from libopus silk/tables_gain.c.
// 8 entries per row -> 8 MSB symbols (0..7) for the first gain index.
var silkGainICDF = [3][8]uint8{
	{224, 112, 44, 15, 3, 2, 1, 0},     // Inactive
	{254, 237, 192, 132, 70, 23, 4, 0}, // Unvoiced
	{255, 252, 226, 155, 61, 11, 2, 0}, // Voiced
}

// silkDeltaGainICDF — delta gain iCDF (41 entries -> 41 symbols).
// silk_delta_gain_iCDF from libopus silk/tables_gain.c.
// Decoded symbol + MIN_DELTA_GAIN_QUANT(-4) = actual delta in [-4..36].
var silkDeltaGainICDF = [41]uint8{
	250, 245, 234, 203, 71, 50, 42, 38,
	35, 33, 31, 29, 28, 27, 26, 25,
	24, 23, 22, 21, 20, 19, 18, 17,
	16, 15, 14, 13, 12, 11, 10, 9,
	8, 7, 6, 5, 4, 3, 2, 1,
	0,
}

// ── Pitch tables (silk/tables_pitch_lag.c) ───────────────────────────────────

// silkPitchLagICDF — pitch lag iCDF for absolute coding.
// 2*(PITCH_EST_MAX_LAG_MS - PITCH_EST_MIN_LAG_MS) = 2*(18-2) = 32 entries.
var silkPitchLagICDF = [32]uint8{
	253, 250, 244, 233, 212, 182, 150, 131,
	120, 110, 98, 85, 72, 60, 49, 40,
	32, 25, 19, 15, 13, 11, 9, 8,
	7, 6, 5, 4, 3, 2, 1, 0,
}

// silkPitchDeltaICDF — pitch lag delta iCDF (21 values).
var silkPitchDeltaICDF = [21]uint8{
	210, 208, 206, 203, 199, 193, 183, 168,
	142, 104, 74, 52, 37, 27, 20, 14,
	10, 6, 4, 2, 0,
}

// silkPitchContourICDF — pitch contour iCDF for 20ms frames (34 values).
var silkPitchContourICDF = [34]uint8{
	223, 201, 183, 167, 152, 138, 124, 111,
	98, 88, 79, 70, 62, 56, 50, 44,
	39, 35, 31, 27, 24, 21, 18, 16,
	14, 12, 10, 8, 6, 4, 3, 2,
	1, 0,
}

// silkPitchContour10msICDF — pitch contour iCDF for 10ms frames (12 values).
var silkPitchContour10msICDF = [12]uint8{
	165, 119, 80, 61, 47, 35, 27, 20,
	14, 9, 4, 0,
}

// ── LTP tables (silk/tables_LTP.c) ──────────────────────────────────────────

// silkLTPPerIndexICDF — LTP codebook selection iCDF (3 values = 3 codebooks).
var silkLTPPerIndexICDF = [3]uint8{179, 99, 0}

// silkLTPGainICDF0 — LTP gain iCDF for codebook 0 (8 entries).
var silkLTPGainICDF0 = [8]uint8{71, 56, 43, 30, 21, 12, 6, 0}

// silkLTPGainICDF1 — LTP gain iCDF for codebook 1 (16 entries).
var silkLTPGainICDF1 = [16]uint8{
	199, 165, 144, 124, 109, 96, 84, 71,
	61, 51, 42, 32, 23, 15, 8, 0,
}

// silkLTPGainICDF2 — LTP gain iCDF for codebook 2 (32 entries).
var silkLTPGainICDF2 = [32]uint8{
	241, 225, 211, 199, 187, 175, 164, 153,
	142, 132, 123, 114, 105, 96, 88, 80,
	72, 64, 57, 50, 44, 38, 33, 29,
	24, 20, 16, 12, 9, 5, 2, 0,
}

// silkLTPGainVQ0 — LTP gain codebook 0, 8 entries × 5 taps (Q7).
var silkLTPGainVQ0 = [8][5]int8{
	{4, 6, 24, 7, 5},
	{0, 0, 2, 0, 0},
	{12, 28, 41, 13, -4},
	{-9, 15, 42, 25, 14},
	{1, -2, 62, 41, -9},
	{-10, 37, 65, -4, 3},
	{-6, 4, 66, 7, -8},
	{16, 14, 38, -3, 33},
}

// silkLTPGainVQ1 — LTP gain codebook 1, 16 entries × 5 taps (Q7).
var silkLTPGainVQ1 = [16][5]int8{
	{13, 22, 39, 23, 12},
	{-1, 36, 64, 27, -6},
	{-7, 10, 55, 43, 17},
	{1, 1, 8, 1, 1},
	{6, -11, 74, 53, -9},
	{-12, 55, 76, -12, 8},
	{-3, 3, 93, 27, -4},
	{26, 39, 59, 3, -8},
	{2, 0, 77, 11, 9},
	{-8, 22, 44, -6, 7},
	{40, 9, 26, 3, 9},
	{-7, 20, 101, -7, 4},
	{3, -8, 42, 26, 0},
	{-15, 33, 68, 2, 23},
	{-2, 55, 46, -2, 15},
	{3, -1, 21, 16, 41},
}

// silkLTPGainVQ2 — LTP gain codebook 2, 32 entries × 5 taps (Q7).
var silkLTPGainVQ2 = [32][5]int8{
	{-6, 27, 61, 39, 5},
	{-11, 42, 88, 4, 1},
	{-2, 60, 65, 6, -4},
	{-1, -5, 73, 56, 1},
	{-9, 19, 94, 29, -9},
	{0, 12, 99, 6, 4},
	{8, -19, 102, 46, -13},
	{3, 2, 13, 3, 2},
	{9, -21, 84, 72, -18},
	{-11, 46, 104, -22, 8},
	{18, 38, 48, 23, 0},
	{-16, 70, 83, -21, 11},
	{5, -11, 117, 22, -8},
	{-6, 23, 117, -12, 3},
	{3, -8, 95, 28, 4},
	{-10, 15, 77, 60, -15},
	{-1, 4, 124, 2, -4},
	{3, 38, 84, 24, -25},
	{2, 13, 42, 13, 31},
	{21, -4, 56, 46, -1},
	{-1, 35, 79, -13, 19},
	{-7, 65, 88, -9, -14},
	{20, 4, 81, 49, -29},
	{20, 0, 75, 3, -17},
	{5, -9, 44, 92, -8},
	{1, -3, 22, 69, 31},
	{-6, 95, 41, -12, 5},
	{39, 67, 16, -4, 1},
	{0, -6, 120, 55, -36},
	{-13, 44, 122, 4, -24},
	{81, 5, 11, 3, 7},
	{2, 0, 9, 10, 88},
}

// ── Other tables (silk/tables_other.c) ──────────────────────────────────────

// silkTypeOffsetVADICDF — signal type + quantization offset for VAD=1 frames (4 symbols).
// silk_type_offset_VAD_iCDF[4]
var silkTypeOffsetVADICDF = [4]uint8{232, 158, 10, 0}

// silkTypeOffsetNoVADICDF — signal type + quantization offset for VAD=0 frames (2 symbols).
// silk_type_offset_no_VAD_iCDF[2]
var silkTypeOffsetNoVADICDF = [2]uint8{230, 0}

// silkNLSFInterpFactorICDF — NLSF interpolation factor iCDF (5 values: 0..4).
var silkNLSFInterpFactorICDF = [5]uint8{243, 221, 192, 181, 0}

// SILK stereo predictor coding tables from silk/tables_other.c.
var silkStereoPredQuantQ13 = [16]int16{
	-13732, -10050, -8266, -7526, -6500, -5000, -2950, -820,
	820, 2950, 5000, 6500, 7526, 8266, 10050, 13732,
}

var silkStereoPredJointICDF = [25]uint8{
	249, 247, 246, 245, 244,
	234, 210, 202, 201, 200,
	197, 174, 82, 59, 56,
	55, 54, 46, 22, 12,
	11, 10, 9, 7, 0,
}

var silkStereoOnlyCodeMidICDF = [2]uint8{64, 0}

// silkLBRRFlags2ICDF — LBRR flags for 2 frames (3 symbols).
var silkLBRRFlags2ICDF = [3]uint8{203, 150, 0}

// silkLBRRFlags3ICDF — LBRR flags for 3 frames (7 symbols).
var silkLBRRFlags3ICDF = [7]uint8{215, 195, 166, 125, 110, 82, 0}

// silkUniform3ICDF — uniform iCDF for 3 symbols.
var silkUniform3ICDF = [3]uint8{171, 85, 0}

// silkUniform4ICDF — uniform iCDF for 4 symbols (seed).
var silkUniform4ICDF = [4]uint8{192, 128, 64, 0}

// silkUniform5ICDF — uniform iCDF for 5 symbols (LTP scale).
var silkUniform5ICDF = [5]uint8{205, 154, 102, 51, 0}

// silkUniform6ICDF — uniform iCDF for 6 symbols.
var silkUniform6ICDF = [6]uint8{213, 171, 128, 85, 43, 0}

// silkUniform8ICDF — uniform iCDF for 8 symbols (LSB).
var silkUniform8ICDF = [8]uint8{224, 192, 160, 128, 96, 64, 32, 0}

// silkLTPScaleICDF — LTP scale entropy table.
var silkLTPScaleICDF = [3]uint8{128, 64, 0}

// silkLSBICDF — LSB coding iCDF.
var silkLSBICDF = [2]uint8{120, 0}

// silkQuantizationOffsetsQ10 — quantization offsets in Q10.
// [voiced/unvoiced][low/high]: [0][0]=OFFSET_UVL_Q10=100, [0][1]=OFFSET_UVH_Q10=240,
// [1][0]=OFFSET_VL_Q10=32, [1][1]=OFFSET_VH_Q10=100.
var silkQuantizationOffsetsQ10 = [2][2]int16{
	{100, 240}, // unvoiced: low=100, high=240
	{32, 100},  // voiced:   low=32,  high=100
}

// silkLTPScalesTable — LTP scale table (Q14): 15565, 12288, 8192
// These correspond to approximately 0.95, 0.75, 0.5 in linear.
var silkLTPScalesTable = [3]int16{15565, 12288, 8192}

// Gain-related constants from silk/define.h
const (
	// NLevelsQGain = 64 — number of gain quantization levels
	NLevelsQGain = 64
	// MaxDeltaGainQuant = 36 (from libopus silk/define.h)
	MaxDeltaGainQuant = 36
	// MinDeltaGainQuant = -4 (from libopus silk/define.h)
	MinDeltaGainQuant = -4
	// PitchEstMaxLagMs = 18
	PitchEstMaxLagMs = 18
	// PitchEstMinLagMs = 2
	PitchEstMinLagMs = 2
)

// silkGainDequantQ16 dequantizes a gain index (0..63) to a Q16 linear gain.
// From silk/decode_parameters.c: silk_gains_dequant
// gain_Q16 = silk_log2lin( gain_dB_Q1/128.0 * 128.0/6.02 + 16.0 )
// Simplified: silkGainQ16Table[i] holds precomputed values.
// The actual libopus formula: gain_dB_Q1 = 8 * i - 16 (in Q1 = half-dB units scaled)
// gain_Q16 = silk_log2lin(((gain_dB_Q1<<7)-SILK_FIX_CONST(16.0, 7)) / 128 + 16*128)
// We'll compute at runtime.

// ── Legacy encoder-compatible ICDF variables ─────────────────────────────────
// These are referenced by encoder.go and kept for compatibility.

// icdfVAD — VAD flag probability (2 symbols: 0=no speech, 1=speech).
var icdfVAD = []uint8{128, 0}

// icdfLBRR — LBRR flag probability.
var icdfLBRR = []uint8{26, 0}

// icdfSignalTypeQOffset — signal type and quantization offset combined (6 symbols).
var icdfSignalTypeQOffset = []uint8{231, 196, 151, 90, 36, 0}

// icdfNLSFStage1 — NLSF stage 1 index (32 entries, uniform).
var icdfNLSFStage1 [32]uint8

// icdfNLSFStage2 — NLSF stage 2 index (8 entries, uniform).
var icdfNLSFStage2 [8]uint8

// icdfPitchHighBits — Pitch lag high bits (3 bits = 8 values).
var icdfPitchHighBits [8]uint8

// icdfLTPFilter — LTP filter index (3 codebooks).
var icdfLTPFilter = []uint8{171, 86, 0}

// icdfGainFirst — Gain index (first subframe, 32 levels).
var icdfGainFirst [32]uint8

// icdfGainDelta — Gain index (delta, 41 levels centered at 0).
var icdfGainDelta [41]uint8

// icdfExcPulseCount — Excitation pulse count per subframe.
var icdfExcPulseCount [19]uint8

func init() {
	// Initialize uniform ICDF tables for encoder compatibility
	for i := 0; i < 32; i++ {
		v := 256 - (i+1)*256/32
		if v < 0 {
			v = 0
		}
		icdfNLSFStage1[i] = uint8(v)
	}
	icdfNLSFStage1[31] = 0

	for i := 0; i < 8; i++ {
		v := 256 - (i+1)*256/8
		if v < 0 {
			v = 0
		}
		icdfNLSFStage2[i] = uint8(v)
	}
	icdfNLSFStage2[7] = 0

	for i := 0; i < 8; i++ {
		v := 256 - (i+1)*256/8
		if v < 0 {
			v = 0
		}
		icdfPitchHighBits[i] = uint8(v)
	}
	icdfPitchHighBits[7] = 0

	for i := 0; i < 32; i++ {
		v := 256 - (i+1)*256/32
		if v < 0 {
			v = 0
		}
		icdfGainFirst[i] = uint8(v)
	}
	icdfGainFirst[31] = 0

	for i := 0; i < 41; i++ {
		v := 256 - (i+1)*256/41
		if v < 0 {
			v = 0
		}
		icdfGainDelta[i] = uint8(v)
	}
	icdfGainDelta[40] = 0

	for i := 0; i < 19; i++ {
		v := 256 - (i+1)*256/19
		if v < 0 {
			v = 0
		}
		icdfExcPulseCount[i] = uint8(v)
	}
	icdfExcPulseCount[18] = 0
}
