package silk

import (
	"fmt"
	"math"
	"os"

	"github.com/darui3018823/opus/internal/entcode"
)

// RateMode describes the packet-size contract supplied by the top-level Opus
// encoder. Both VBR modes may use the natural SNR-target size; CBR must stay on
// the budget-fitting path so packetization can pad every active stream equally.
type RateMode int

const (
	RateModeCBR RateMode = iota
	RateModeVBR
	RateModeCVBR
)

// Encoder represents a SILK encoder instance
type Encoder struct {
	sampleRate       int // Sample rate (8000, 12000, 16000, 24000)
	frameSize        int // Frame size in samples
	frameMs          int // Frame duration in milliseconds (10 or 20)
	nSubframes       int // Number of SILK subframes in one frame
	packetFrames     int // Number of SILK frames in the packet currently being encoded
	channels         int // Number of channels (1 or 2)
	lpcOrder         int // LPC order based on bandwidth
	complexity       int // Complexity (0-10)
	bitrate          int // Target bitrate in bps
	silkVAD          silkVADState
	speechActivity   float64
	inputTilt        float64
	speechActivityQ8 int
	inputTiltQ15     int
	// frameVAD holds the fixed-point VAD result of every frame in the packet
	// being encoded, computed in frame order before any frame is coded
	// (silk_encode_do_VAD_FLP); curFrame indexes it during encodeRangeFrame.
	frameVAD        []silkVADResult
	curFrame        int
	noSpeechCounter int
	inputQuality    float64
	inputQualityB   [silkVADNBands]float64
	// inputQualityBandQ15 keeps the fixed-point band quality of the current
	// frame; hpVariableCutoff reads the previous frame's value from it.
	inputQualityBandQ15 [silkVADNBands]int
	// variableHPSmth1Q15 is silk_encoder_state.variable_HP_smth1_Q15, the
	// smoothed log2 cutoff the Opus-layer high-pass follows.
	variableHPSmth1Q15 int32
	prevEnergy         float64   // Previous frame energy for smoothing
	prevLPC            []float64 // Previous LPC coefficients
	prevNLSF           []float64 // Previous NLSF
	prevNLSFQ15        []int16   // Previous quantized NLSF in Q15 (matches decoder prevNLSFQ15; used for interpolation search)
	prevPitchLag       int       // Previous pitch lag
	prevLagIndex       int       // Previous entropy-coded pitch lag index
	prevSignalType     int       // Previous SILK signal type
	prevGains          []float64 // Previous subframe gains
	prevGainIdx        int       // Previous absolute gain index, matching decoder state
	prevGainQ16        int32     // Previous synthesis gain, matching decoder state
	lpcState           []int32   // Encoder-side LPC synthesis state, Q14
	ltpState           []int32   // Encoder-side LTP output history, Q0
	nsq                silkNSQState
	nsqDelDec          [4]nsqDelayedDecision
	nsqSeed            int32 // winning del-dec seed (silk_NSQ_del_dec writes this back to the bitstream)
	lastFinalRange     uint32
	// useTrellisNSQ enables the FLP noise-shape analysis + delayed-decision
	// trellis NSQ (Q3+Q4) for active frames. Voiced frames use the perceptual
	// shaping path; unvoiced/stereo-component frames keep neutral shaping while
	// retaining the delayed-decision rate/distortion search.
	useTrellisNSQ bool
	// snrTargetEnabled records the OPUS_SILK_RC_SNR A/B selection.
	// useSNRTargetVBR is its rate-mode-gated effective value.
	rateMode         RateMode
	snrTargetEnabled bool
	useSNRTargetVBR  bool
	lastSNRVBRFrame  bool
	lastSNRVBRStream bool
	// stereoComponent marks the mid/side encoders of a stereo packet. The stereo
	// predictor path converts L/R to decoder-symmetric adaptive M/S before these
	// component encoders run.
	stereoComponent bool
	// hybridMode marks frames encoded as the SILK low band of a hybrid packet.
	// It gates off the Step 4 voiced trellis: the hybrid SILK+CELT energy balance
	// is a separate WIP and the trellis-coded low band is not yet conformant
	// there. Set per-call by the hybrid encoder.
	hybridMode      bool
	shapeHarmSmooth float64
	shapeTiltSmooth float64
	noiseShapeBuf   []float64
	side            *Encoder // side-channel encoder for stereo packets
	stereoState     stereoEncState
	prevOnlyMiddle  bool // previous stereo frame omitted the side channel
	// streamChannels is nChannelsInternal for a stereo (API) encoder: 1 codes
	// the caller's L/R downmix as a mono stream; prevStreamChannels is
	// nPrevChannelsInternal (0 before the first packet). toMono marks the
	// last stereo frame before a stereo->mono transition.
	streamChannels     int
	prevStreamChannels int
	toMono             bool

	// Input buffer (silk_encoder_state_FLP.x_buf): [ltp_mem_length history |
	// frame_length coded frame | LA_SHAPE_MS look-ahead] in [-1,1]. New input
	// lands at xBuf[ltpMem+laShape:], so the coded frame trails the caller's
	// frame by LA_SHAPE_MS (5 ms) exactly as in libopus. pitchHist aliases the
	// history region.
	xBuf      []float64
	pitchHist []float64 // Past ltp_mem_length input samples, [-1,1]
	// Front end (front_end.go): the Opus-layer input rate, the libopus
	// resampler/inputBuf delay line, and the float32 x_buf snapshot of the
	// most recently coded frame.
	apiSampleRate int
	encInputDelay []float64
	lastXBuf      []float32
	// Stage traces (frame_trace.go): pendingTrace is filled by the NSQ call,
	// lastTrace is the final (non-LBRR) frame's trace.
	pendingTrace FrameTrace
	lastTrace    FrameTrace
	traceNLSFQ15 []int16
	// pitchPredGain is psEncCtrl->predGain from find_pitch_lags (float32).
	pitchPredGain float64
	// float32 sShape smoothers of the libopus-faithful noise-shape port
	// (trace-only until the port drives the quantizer).
	shapeHarmSmooth32 float64
	shapeTiltSmooth32 float64
	pendingShape32    silkNoiseShapeOutputs
	haveShape32       bool
	exactGainSymbols  []int
	// find_LPC trace: the unquantised NLSF target and minInvGain of the last
	// analyzeNLSF call.
	traceNLSFTargetQ15 []int16
	traceMinInvGain    float64
	traceLPCInPre      []float32
	traceInvGains      []float32
	// frameCounter is silk_encoder_state.frameCounter (NSQ seed source).
	frameCounter int
	// libopus bit reservoir (target_rate.go).
	nBitsExceeded        int
	nBitsUsedLBRR        int
	targetRateBps        int
	prevLagForPitch      int       // Previous frame pitch lag (0 if unvoiced)
	ltpCorrState         float64   // Normalized LTP correlation from prev frame
	pitchResidual        []float64 // res_pitch: whitened [history|frame|LTP_ORDER] from the pitch analysis
	curLTP               *frameLTPResult
	firstFrameAfterReset bool // True until the first frame after reset is encoded
	curPitchLagIndex     int  // Lag index selected for the current frame
	curPitchContourIndex int  // Pitch contour index for the current frame

	// ltpSumLogGainQ7 is the cumulative log prediction gain across subframes
	// (silk sum_log_gain_Q7), limiting the total LTP gain for stability.
	ltpSumLogGainQ7 int32

	// ── Inband Low Bitrate Redundancy (LBRR / in-band FEC) ──────────────────
	// lbrrEnabled is the SILK LBRR_coded gate (set by the top-level FEC
	// decision); packetLossPerc feeds the LBRR gain-increase schedule.
	lbrrEnabled    bool
	packetLossPerc int
	// lbrrEnabledPrev is LBRR_enabled of the previous packet and
	// lbrrGainIncreases the resulting LBRR_GainIncreases (silk_setup_LBRR);
	// lbrrPrevLastGainIndex is LBRRprevLastGainIndex; lbrrFlag the LBRR_flag
	// written in the current packet (it feeds silk_LTP_scale_ctrl).
	lbrrEnabledPrev       bool
	lbrrGainIncreases     int
	lbrrPrevLastGainIndex int
	lbrrFlag              bool
	// pendingLBRR holds the LBRR frames generated while encoding the previous
	// packet; they are emitted at the front of the current packet (the cross-
	// packet one-packet FEC delay). curLBRR accumulates the current packet's
	// LBRR frames. lbrrPlanned is set when curLBRR has at least one frame.
	pendingLBRR       []lbrrFrameData
	curLBRR           []lbrrFrameData
	pendingLBRRFrames int // frame count the pending LBRR data was generated for
	// pendingLBRRStereoPred carries the M/S predictor indices for the frames in
	// pendingLBRR. Stereo LBRR syntax writes these controls frame-by-frame
	// before the corresponding mid/side redundant bodies.
	pendingLBRRStereoPred [][2][3]int8
	// lbrrBitsPerFrame is the current packet's emitted LBRR cost divided across
	// its regular frames. silkFrameTargetBits subtracts it so CBR/CVBR do not
	// simply add redundancy on top of the configured bitrate.
	lbrrBitsPerFrame int
	// Capture of the current voiced frame's coded pitch/LTP indices, populated
	// by encodePitchAndLTP so the LBRR generator can replay them.
	capLagIndex      int
	capContour       int
	capLTPPerIdx     int
	capLTPGainIdx    []int
	capLTPScaleIndex int
	// curLTPScaleIndex is this frame's LTP_scaleIndex (silk_LTP_scale_ctrl_FLP)
	// and lastGainSymbols the gain symbols encodeGains wrote for the frame.
	curLTPScaleIndex int
	lastGainSymbols  []int
	// codeNoLTPScaling marks CODE_INDEPENDENTLY_NO_LTP_SCALING: the side
	// channel frame after a mid-only frame is coded independently but without
	// LTP scaling (no LTP_scaleIndex symbol).
	codeNoLTPScaling bool
	// channelRateBps is this channel's TargetRate_bps when the stereo layer
	// has split the packet rate (MStargetRates_bps); 0 means the packet rate
	// divided by the channel count.
	channelRateBps int
	// pendingLBRRStereoMidOnly carries the mid-only flags for the frames in
	// pendingLBRR (coded with an LBRR mid frame whose side LBRR is absent).
	pendingLBRRStereoMidOnly []bool
	// lastStereoTrace holds the per-frame stereo decisions of the last packet.
	lastStereoTrace []StereoFrameTrace
	// lambda32 is the frame's noise-shape Lambda (silk_float) once computed
	// (haveLambda32); the encode_frame_FLP loop raises it when a frame busts
	// its bit budget.
	lambda32     float64
	haveLambda32 bool
	// maxBits is encControl->maxBits, the packet's bit budget (0 = none);
	// frameMaxBits / frameUseCBR are the per-frame values silk_Encode derives
	// from it for the frame being coded.
	maxBits      int
	frameMaxBits int
	frameUseCBR  bool
}

type nlsfAnalysis struct {
	cb1Idx       int
	rawIdx       []int
	nlsfQ15      []int16
	lpcQ12       []int16
	lpcQ12Interp []int16 // LPC from interpolated NLSF for subframes 0,1 (nil when interpFactor==4)
	interpFactor int
}

type lpcBurgDomain struct {
	signal      []float64
	subfrLength int
	nbSubfr     int
	minInvGain  float64
}

type encoderFrameState struct {
	prevPitchLag    int
	prevLagIndex    int
	prevGainQ16     int32
	lpcState        []int32
	ltpState        []int32
	nsq             silkNSQState
	shapeHarmSmooth float64
	shapeTiltSmooth float64
	ltpSumLogGainQ7 int32
}

type rateControlPlan struct {
	gainTargets []int
	gainIndices []int
	rateScale   float64
	snrVBR      bool
}

type silkShapeSubframe struct {
	feedback float64
	tilt     float64
	lf       float64
	hf       float64
	harmonic float64
	lambda   float64
}

type silkShapeAnalysis struct {
	subframes []silkShapeSubframe
}

// NewEncoder creates a new 20 ms SILK encoder.
func NewEncoder(sampleRate, channels int) (*Encoder, error) {
	return NewEncoderWithFrameMs(sampleRate, channels, 20)
}

// NewEncoderWithFrameMs creates a new SILK encoder for 10 ms or 20 ms frames.
func NewEncoderWithFrameMs(sampleRate, channels, frameMs int) (*Encoder, error) {
	if sampleRate != 8000 && sampleRate != 12000 && sampleRate != 16000 && sampleRate != 24000 {
		return nil, fmt.Errorf("invalid sample rate: %d (must be 8000, 12000, 16000, or 24000)", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channels: %d (must be 1 or 2)", channels)
	}
	if frameMs != 10 && frameMs != 20 {
		return nil, fmt.Errorf("invalid frame duration: %d ms (must be 10 or 20)", frameMs)
	}

	lpcOrder := 10
	if sampleRate >= 16000 {
		lpcOrder = 16
	}

	frameSize := sampleRate * frameMs / 1000
	nSubframes := 4
	if frameMs == 10 {
		nSubframes = 2
	}

	prevNLSF := make([]float64, lpcOrder)
	prevNLSFQ15 := make([]int16, lpcOrder)
	for i := range prevNLSF {
		prevNLSF[i] = math.Pi * float64(i+1) / float64(lpcOrder+1)
		prevNLSFQ15[i] = int16((float64(i+1) / float64(lpcOrder+1)) * 32768.0)
	}

	enc := &Encoder{
		sampleRate:       sampleRate,
		frameSize:        frameSize,
		frameMs:          frameMs,
		nSubframes:       nSubframes,
		channels:         channels,
		lpcOrder:         lpcOrder,
		complexity:       5,
		bitrate:          sampleRate * channels * 16 / 8,
		silkVAD:          newSilkVADState(),
		speechActivity:   1.0,
		speechActivityQ8: 0,
		inputQuality:     1.0,
		prevEnergy:       1.0,
		prevLPC:          make([]float64, lpcOrder),
		prevNLSF:         prevNLSF,
		prevPitchLag:     100,
		prevLagIndex:     0,
		prevSignalType:   SignalTypeUnvoiced,
		prevGains:        []float64{1.0, 1.0, 1.0, 1.0},
		prevGainIdx:      10,
		prevGainQ16:      65536,
		lpcState:         make([]int32, silkMaxLPCOrder),
		ltpState:         make([]int32, silkLTPMemLengthMs*(sampleRate/1000)),
		nsq:              newSilkNSQState(frameSize, silkLTPMemLengthMs*(sampleRate/1000)),

		xBuf:                 make([]float64, (peLtpMemLengthMs+silkLAShapeMs)*(sampleRate/1000)+frameSize),
		prevLagForPitch:      0,
		ltpCorrState:         0,
		firstFrameAfterReset: true,
		// Step 4: voiced frames use the delayed-decision trellis NSQ with gains
		// co-designed by silk_process_gains_FLP. OPUS_SILK_TRELLIS=0 forces the
		// legacy homebrew quantizer everywhere for A/B comparison.
		useTrellisNSQ:    os.Getenv("OPUS_SILK_TRELLIS") != "0",
		rateMode:         RateModeVBR,
		snrTargetEnabled: os.Getenv("OPUS_SILK_RC_SNR") != "0",
	}
	enc.useSNRTargetVBR = enc.snrTargetEnabled
	enc.pitchHist = enc.xBuf[:enc.ltpMemLength()]
	enc.variableHPSmth1Q15 = variableHPSmth1Initial()
	for i := range enc.inputQualityB {
		enc.inputQualityB[i] = 1.0
	}
	if channels == 2 {
		side, err := NewEncoderWithFrameMs(sampleRate, 1, frameMs)
		if err != nil {
			return nil, err
		}
		side.complexity = enc.complexity
		side.bitrate = enc.bitrate
		enc.stereoComponent = true
		side.stereoComponent = true
		enc.side = side
		enc.stereoState.reset()
	}
	enc.streamChannels = channels
	return enc, nil
}

// SetComplexity sets the computational complexity (0-10)
func (e *Encoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return fmt.Errorf("complexity must be between 0 and 10, got %d", complexity)
	}
	e.complexity = complexity
	if e.side != nil {
		return e.side.SetComplexity(complexity)
	}
	return nil
}

// SetBitrate sets the target bitrate in bps
func (e *Encoder) SetBitrate(bitrate int) error {
	// MIN_TARGET_RATE_BPS .. MAX_TARGET_RATE_BPS (silk/define.h); the Opus
	// layer passes the whole packet rate for stereo streams too.
	if bitrate < 5000 || bitrate > 80000 {
		return fmt.Errorf("bitrate must be between 5000 and 80000 bps, got %d", bitrate)
	}
	e.bitrate = bitrate
	if e.side != nil {
		return e.side.SetBitrate(bitrate)
	}
	return nil
}

// SetMaxBits sets encControl->maxBits, the bit budget of the next packet
// (0 = unlimited). In CBR the frame loop codes up to it; in VBR a frame that
// busts it is re-quantised.
func (e *Encoder) SetMaxBits(bits int) {
	e.maxBits = bits
	if e.side != nil {
		e.side.maxBits = bits
	}
}

// frameBitBudget returns silk_Encode's per-frame maxBits and useCBR for
// frame index frame of an nFrames packet.
func (e *Encoder) frameBitBudget(nFrames, frame int) (maxBits int, useCBR bool) {
	maxBits = e.maxBits
	if maxBits > 0 {
		if nFrames == 2 && frame == 0 {
			maxBits = maxBits * 3 / 5
		} else if nFrames == 3 {
			if frame == 0 {
				maxBits = maxBits * 2 / 5
			} else if frame == 1 {
				maxBits = maxBits * 3 / 4
			}
		}
	}
	useCBR = e.rateMode == RateModeCBR && frame == nFrames-1 && maxBits > 0
	return maxBits, useCBR
}

// RateMode returns the current rate mode.
func (e *Encoder) RateMode() RateMode { return e.rateMode }

// SetRateMode supplies the top-level Opus packet-size contract. The
// SNR-target natural-size path is available only in VBR/CVBR and remains
// independently disableable with OPUS_SILK_RC_SNR=0.
func (e *Encoder) SetRateMode(mode RateMode) {
	e.rateMode = mode
	e.useSNRTargetVBR = e.snrTargetEnabled && mode != RateModeCBR
	if e.side != nil {
		e.side.SetRateMode(mode)
	}
}

// Encode encodes a frame of audio samples using the range encoder.
func (e *Encoder) Encode(pcm []float64) ([]byte, error) {
	if len(pcm) != e.frameSize*e.channels {
		return nil, fmt.Errorf("invalid PCM length: got %d, expected %d", len(pcm), e.frameSize*e.channels)
	}

	return e.EncodeMulti(pcm, 1)
}

// EncodeMulti encodes n consecutive SILK frames into one shared range-coded
// stream. This mirrors the SILK packet structure used by Opus: VAD flags for
// all frames, LBRR flags, then frame payloads in order. Stereo input is encoded
// as adaptive mid/side, with predictor indices written before each frame.
func (e *Encoder) EncodeMulti(pcm []float64, nFrames int) ([]byte, error) {
	enc := entcode.NewEncoder(64)
	if err := e.EncodeMultiWithEncoder(enc, pcm, nFrames); err != nil {
		return nil, err
	}
	e.lastFinalRange = enc.GetRng()
	// opus_encode_native sizes a SILK-only payload as (ec_tell + 7) >> 3 before
	// ec_enc_done; the carry byte ec_enc_done may emit past that is dropped
	// (the range decoder pads with zeros).
	n := (enc.ECTell() + 7) >> 3
	enc.Flush()
	out := enc.Bytes()
	if len(out) > n && n >= 2 {
		out = out[:n]
	}
	return out, nil
}

// EncodeMultiWithEncoder writes n consecutive SILK frames into an existing
// range encoder. It is used by hybrid Opus packets, where SILK and CELT share
// one entropy stream.
func (e *Encoder) EncodeMultiWithEncoder(enc *entcode.Encoder, pcm []float64, nFrames int) error {
	if enc == nil {
		return fmt.Errorf("nil range encoder")
	}
	if nFrames < 1 {
		return fmt.Errorf("invalid frame count: %d", nFrames)
	}
	streamChannels := e.StreamChannels()
	expected := e.frameSize * streamChannels * nFrames
	if len(pcm) != expected {
		return fmt.Errorf("invalid PCM length: got %d, expected %d", len(pcm), expected)
	}
	if streamChannels == 2 {
		defer func() { e.prevStreamChannels = 2 }()
		return e.encodeMultiStereoWithEncoder(enc, pcm, nFrames)
	}
	if e.channels == 2 {
		// A stereo encoder coding a mono stream (nChannelsInternal == 1).
		if e.prevStreamChannels == 2 {
			e.enterMonoStream()
		}
		defer func() { e.prevStreamChannels = 1 }()
	}
	prevPacketFrames := e.packetFrames
	e.packetFrames = nFrames
	defer func() {
		e.packetFrames = prevPacketFrames
	}()

	frames := make([][]float64, nFrames)
	vadFlags := make([]bool, nFrames)
	for frame := 0; frame < nFrames; frame++ {
		start := frame * e.frameSize
		// int16 quantisation and the resampler/inputBuf delay come first,
		// so the VAD and the coder see the same frame as silk_Encode.
		framePCM := e.frontEndFrame(pcm[start:start+e.frameSize], true)
		frames[frame] = framePCM
		vadFlags[frame] = e.runFrameVAD(frame, framePCM)
		if e.channels == 2 {
			e.trackMonoStreamHistory(framePCM)
		}
	}

	for _, active := range vadFlags {
		enc.EncodeBitLogp(active, 1)
	}
	// LBRR flag + redundant frames carried over from the previous packet (the
	// one-packet FEC delay). When FEC is disabled this writes a single 0 bit,
	// identical to the previous hardcoded behaviour.
	e.setupLBRR()
	lbrrBits := e.emitPendingLBRR(enc, nFrames)
	e.lbrrBitsPerFrame = 0
	if lbrrBits > 1 {
		e.lbrrBitsPerFrame = (lbrrBits + nFrames - 1) / nFrames
	}
	if lbrrDebug {
		fmt.Fprintf(os.Stderr, "[LBRR budget] bits=%d perFrame=%d base=%d adjusted=%d\n",
			lbrrBits, e.lbrrBitsPerFrame, e.bitrate*e.frameMs/1000, e.silkFrameTargetBits())
	}

	e.updateLBRRUsage(lbrrBits)

	e.beginLBRRPacket()
	e.lastSNRVBRStream = false
	for i, signal := range frames {
		e.curFrame = i
		tell := enc.ECTell()
		e.targetRateBps = e.frameTargetRate(nFrames, i, tell)
		e.frameMaxBits, e.frameUseCBR = e.frameBitBudget(nFrames, i)
		e.encodeRangeFrame(enc, signal, vadFlags[i], i > 0)
		e.recordRateTrace(nFrames, tell, lbrrBits)
		e.lastSNRVBRStream = e.lastSNRVBRStream || e.lastSNRVBRFrame
	}
	e.finishLBRRPacket(nFrames)
	e.finishPacketBitReservoir(nFrames, enc.ECTell())
	return nil
}

func (e *Encoder) encodeMultiStereo(pcm []float64, nFrames int) ([]byte, error) {
	enc := entcode.NewEncoder(64)
	if err := e.encodeMultiStereoWithEncoder(enc, pcm, nFrames); err != nil {
		return nil, err
	}
	e.lastFinalRange = enc.GetRng()
	enc.Flush()
	return enc.Bytes(), nil
}

// encodeMultiStereoWithEncoder codes a stereo SILK packet like silk_Encode
// with nChannelsInternal == 2: both channels pass the front end, the VAD/LBRR
// flag bits are reserved with a placeholder symbol and patched in at the end
// (the side VAD depends on the per-frame mid-only decision), and every frame
// runs silk_stereo_LR_to_MS with the packet's TargetRate_bps, codes the
// predictor indices (and the mid-only flag when the side VAD is inactive),
// then the mid frame and — unless mid-only — the side frame.
func (e *Encoder) encodeMultiStereoWithEncoder(enc *entcode.Encoder, pcm []float64, nFrames int) error {
	if e.side == nil {
		return fmt.Errorf("missing SILK side-channel encoder")
	}
	fsKHz := e.sampleRate / 1000
	if e.prevStreamChannels == 1 {
		e.enterStereoStream()
	}
	// nFramesPerPacket for both channels (silk_LTP_scale_ctrl's round_loss).
	prevPacketFrames, prevSidePacketFrames := e.packetFrames, e.side.packetFrames
	e.packetFrames, e.side.packetFrames = nFrames, nFrames
	defer func() {
		e.packetFrames, e.side.packetFrames = prevPacketFrames, prevSidePacketFrames
	}()

	left := make([][]int16, nFrames)
	right := make([][]int16, nFrames)
	for frame := 0; frame < nFrames; frame++ {
		base := frame * e.frameSize * 2
		// Each channel passes the int16 quantisation and resampler delay
		// before the mid/side conversion (which adds the inputBuf offset).
		l := make([]float64, e.frameSize)
		r := make([]float64, e.frameSize)
		for i := 0; i < e.frameSize; i++ {
			l[i] = pcm[base+2*i]
			r[i] = pcm[base+2*i+1]
		}
		left[frame] = floatFrameToInt16(e.frontEndFrame(l, false))
		right[frame] = floatFrameToInt16(e.side.frontEndFrame(r, false))
	}

	e.setupLBRR()
	e.side.setupLBRR()
	// Placeholder for the VAD and LBRR flags of both channels.
	flagBits := uint((nFrames + 1) * 2)
	placeholder := 256 - (256 >> flagBits) // iCDF[0] = 256 - silk_RSHIFT(256, (nFramesPerPacket + 1) * nChannelsInternal)
	enc.EncodeIcdf(0, []uint8{uint8(placeholder), 0}, 8)
	lbrrSymbols := [2]int{
		e.pendingLBRRSymbol(nFrames),
		e.side.pendingLBRRSymbol(nFrames),
	}
	e.lbrrFlag = lbrrSymbols[0] != 0
	e.side.lbrrFlag = lbrrSymbols[1] != 0
	lbrrStartBits := enc.ECTell()
	for ch, symbol := range lbrrSymbols {
		component := e
		if ch == 1 {
			component = e.side
		}
		component.writePendingLBRRMask(enc, nFrames, symbol)
	}
	// libopus writes stereo LBRR frame-major. A mid-channel redundant frame is
	// preceded by the frame's stereo predictor and, when side LBRR is absent,
	// its mid-only flag. The channel bodies then follow mid before side.
	var midEC, sideEC lbrrECState
	for frame := 0; frame < nFrames; frame++ {
		midPresent := lbrrSymbols[0]&(1<<uint(frame)) != 0
		sidePresent := lbrrSymbols[1]&(1<<uint(frame)) != 0
		if midPresent {
			var pred [2][3]int8
			if frame < len(e.pendingLBRRStereoPred) {
				pred = e.pendingLBRRStereoPred[frame]
			}
			encodeStereoPred(enc, pred)
			if !sidePresent {
				midOnly := 0
				if frame < len(e.pendingLBRRStereoMidOnly) && e.pendingLBRRStereoMidOnly[frame] {
					midOnly = 1
				}
				enc.EncodeIcdf(midOnly, silkStereoOnlyCodeMidICDF[:], 8)
			}
			e.writeLBRRFrame(enc, e.pendingLBRR[frame], frame > 0 && lbrrSymbols[0]&(1<<uint(frame-1)) != 0, &midEC)
		}
		if sidePresent {
			e.side.writeLBRRFrame(enc, e.side.pendingLBRR[frame], frame > 0 && lbrrSymbols[1]&(1<<uint(frame-1)) != 0, &sideEC)
		}
	}
	lbrrBits := enc.ECTell() - lbrrStartBits
	e.updateLBRRUsage(lbrrBits)

	e.beginLBRRPacket()
	e.side.beginLBRRPacket()
	e.lastSNRVBRStream = false
	vadFlags := [2][]bool{make([]bool, nFrames), make([]bool, nFrames)}
	predIx := make([][2][3]int8, nFrames)
	midOnly := make([]bool, nFrames)
	e.lastStereoTrace = e.lastStereoTrace[:0]
	for i := 0; i < nFrames; i++ {
		tell := enc.ECTell()
		total := e.frameTargetRate(nFrames, i, tell)
		e.targetRateBps = total
		e.side.targetRateBps = total
		st := StereoFrameTrace{PrevDecodeOnlyMiddle: e.prevOnlyMiddle, TotalRate: total, PrevSpeechActQ8: e.speechActivityQ8}
		// silk_stereo_LR_to_MS with the mid channel's previous speech activity.
		ms := e.stereoState.lrToMS(left[i], right[i], fsKHz, e.frameSize, int32(total), e.speechActivityQ8, e.toMono)
		st.Ix, st.MidOnly, st.Rates = ms.ix, ms.midOnly, ms.midSideRates
		st.WidthPrev, st.SmthWidth, st.SilentSideLen, st.PredPrev = e.stereoState.widthPrevQ14, e.stereoState.smthWidthQ14, e.stereoState.silentSideLen, e.stereoState.predPrevQ13
		predIx[i] = ms.ix
		midOnly[i] = ms.midOnly
		mid := int16FrameToFloat(ms.mid)
		side := int16FrameToFloat(ms.side)
		if !ms.midOnly {
			if e.prevOnlyMiddle {
				// Reset side channel encoder memory for first frame with side coding.
				e.side.resetForSideReactivation()
			}
			vadFlags[1][i] = e.side.runFrameVAD(i, side)
		} else {
			vadFlags[1][i] = false
		}
		encodeStereoPred(enc, ms.ix)
		if !vadFlags[1][i] {
			flag := 0
			if ms.midOnly {
				flag = 1
			}
			enc.EncodeIcdf(flag, silkStereoOnlyCodeMidICDF[:], 8)
		}
		vadFlags[0][i] = e.runFrameVAD(i, mid)

		e.curFrame = i
		e.channelRateBps = int(ms.midSideRates[0])
		e.frameMaxBits, e.frameUseCBR = e.frameBitBudget(nFrames, i)
		if ms.midSideRates[1] > 0 && e.frameMaxBits > 0 {
			// Give mid up to 1/2 of the max bits for that frame.
			e.frameUseCBR = false
			e.frameMaxBits -= e.maxBits / (nFrames * 2)
		}
		st.Tell[0], st.SpeechActQ8[0], st.FirstAfterRst[0] = enc.ECTell(), e.frameVAD[i].speechActivityQ8, e.firstFrameAfterReset
		e.encodeRangeFrame(enc, mid, vadFlags[0][i], i > 0)
		e.recordRateTrace(nFrames, tell, lbrrBits)
		e.lastSNRVBRStream = e.lastSNRVBRStream || e.lastSNRVBRFrame
		if ms.midSideRates[1] > 0 {
			st.SideCoded = true
			st.Tell[1], st.SpeechActQ8[1], st.FirstAfterRst[1] = enc.ECTell(), e.side.frameVAD[i].speechActivityQ8, e.side.firstFrameAfterReset
			e.side.curFrame = i
			e.side.channelRateBps = int(ms.midSideRates[1])
			e.side.frameMaxBits, e.side.frameUseCBR = e.side.frameBitBudget(nFrames, i)
			// CODE_INDEPENDENTLY for the first frame, CODE_INDEPENDENTLY_NO_LTP_SCALING
			// after a skipped side frame, CODE_CONDITIONALLY otherwise.
			e.side.codeNoLTPScaling = i > 0 && e.prevOnlyMiddle
			e.side.encodeRangeFrame(enc, side, vadFlags[1][i], i > 0 && !e.prevOnlyMiddle)
			e.side.codeNoLTPScaling = false
			e.lastSNRVBRStream = e.lastSNRVBRStream || e.side.lastSNRVBRFrame
		} else {
			e.side.appendMissingLBRRFrame()
		}
		e.prevOnlyMiddle = ms.midOnly
		e.lastStereoTrace = append(e.lastStereoTrace, st)
	}
	e.channelRateBps = 0
	e.side.channelRateBps = 0

	// Insert the VAD and LBRR flags at the beginning of the bitstream.
	var flags uint32
	for ch := 0; ch < 2; ch++ {
		for i := 0; i < nFrames; i++ {
			flags <<= 1
			if vadFlags[ch][i] {
				flags |= 1
			}
		}
		flags <<= 1
		if lbrrSymbols[ch] != 0 {
			flags |= 1
		}
	}
	enc.PatchInitialBits(flags, flagBits)

	e.finishLBRRPacket(nFrames)
	e.side.finishLBRRPacket(nFrames)
	e.finishPacketBitReservoir(nFrames, enc.ECTell())
	e.pendingLBRRStereoPred = append(e.pendingLBRRStereoPred[:0], predIx...)
	e.pendingLBRRStereoMidOnly = append(e.pendingLBRRStereoMidOnly[:0], midOnly...)
	return nil
}

// floatFrameToInt16 converts front-end output (exact int16/32768 values) back
// to int16 samples; int16FrameToFloat is the inverse.
func floatFrameToInt16(x []float64) []int16 {
	out := make([]int16, len(x))
	for i, v := range x {
		out[i] = floatToInt16Sample(v)
	}
	return out
}

func int16FrameToFloat(x []int16) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = float64(v) / 32768.0
	}
	return out
}

func encodeStereoPred(enc *entcode.Encoder, ix [2][3]int8) {
	n := 5*int(ix[0][2]) + int(ix[1][2])
	enc.EncodeIcdf(n, silkStereoPredJointICDF[:], 8)
	for i := 0; i < 2; i++ {
		enc.EncodeIcdf(int(ix[i][0]), silkUniform3ICDF[:], 8)
		enc.EncodeIcdf(int(ix[i][1]), silkUniform5ICDF[:], 8)
	}
}

// encodeRangeFrame writes one structurally valid SILK frame. Slice 3 adds the
// first voiced path: simple pitch/LTP decisions are encoded before the seed,
// and pulse coding is driven from a short-term residual instead of raw samples.
func (e *Encoder) encodeRangeFrame(enc *entcode.Encoder, signal []float64, vadActive, conditionalGain bool) {
	initialState := e.snapshotFrameState()
	e.curLTP = nil
	e.pitchResidual = nil
	// The VAD, like silk_encode_do_VAD_FLP, sees the caller's frame; every
	// analysis and quantisation stage below works on the 5 ms delayed coded
	// frame taken from the look-ahead buffer.
	vadSA := e.frameVADResult(signal)
	signal = e.pushInputFrame(signal)
	// silk_HP_variable_cutoff runs before this frame's VAD result is
	// installed, so it sees the previous frame's activity and quality.
	e.hpVariableCutoff()
	e.speechActivity = vadSA.speechActivity
	e.inputTilt = vadSA.inputTilt
	e.speechActivityQ8 = vadSA.speechActivityQ8
	e.inputTiltQ15 = vadSA.inputTiltQ15
	e.inputQuality = vadSA.inputQuality
	e.inputQualityB = vadSA.inputQualityBand
	e.inputQualityBandQ15 = vadSA.inputQualityBandQ15

	signalType := SignalTypeInactive
	pitchLag := e.prevPitchLag
	pitchGain := 0.0
	e.curPitchLagIndex = 0
	e.curPitchContourIndex = 0
	// silk_find_pitch_lags_FLP whitens every frame (the residual feeds the
	// quantizer-offset measure and the LTP analysis); the pitch core only
	// runs for frames with voice activity.
	voiced, lagIndex, contourIndex, ltpCorr := e.silkFindPitchLags(signal, e.speechActivity, vadActive)
	if vadActive {
		signalType = SignalTypeUnvoiced
		if voiced {
			signalType = SignalTypeVoiced
			e.curPitchLagIndex = lagIndex
			e.curPitchContourIndex = contourIndex
			pitchGain = ltpCorr
		}
	}
	quantOffset := 0

	// libopus codes TYPE_NO_VOICE_ACTIVITY frames through the same analysis
	// and quantizer as unvoiced frames; only the coded type differs.
	cb := getNLSFCB(e.lpcOrder)
	var domainGainTargets []int
	var domainConfig []lpcInPreConfig
	{
		bootstrap := e.analyzeNLSF(signal, cb, signalType)
		quantOffset = e.estimateQuantOffsetType(signal, bootstrap.lpcQ12, signalType, pitchLag, pitchGain)
		pitchLags := make([]int, e.nSubframes)
		for sf := range pitchLags {
			pitchLags[sf] = pitchLag
		}
		var ltpCoeffsQ14 [][5]int16
		ltpPredCodGain := 0.0
		if signalType == SignalTypeVoiced {
			pitchLags = e.reconstructCurrentPitchLags()
			ltpSum := e.ltpSumLogGainQ7
			_, _, ltpCoeffsQ14, ltpPredCodGain = e.selectLTPGainsVQWithGain(signal, bootstrap.lpcQ12, pitchLags)
			e.ltpSumLogGainQ7 = ltpSum
		}
		// silk_noise_shape_analysis_FLP runs once per frame; its AR/tilt/LF/
		// harmonic shaping drives every NSQ pass of this frame.
		e.pendingShape32 = e.noiseShapeFLP32Trace(signal, signalType, pitchLags)
		e.haveShape32 = true
		e.haveLambda32 = false
		gainTargets, shape := e.shapeGainAnalysis(signal, bootstrap.lpcQ12, nil, signalType, quantOffset, pitchLags, ltpCoeffsQ14, pitchGain)
		domainGainTargets = gainTargets
		gainIndices := e.resolveGainIndices(gainTargets, conditionalGain)
		invGains := invGainsFromIndices(gainIndices)
		codingQuality := shape.CodingQuality
		if e.haveShape32 {
			// find_pred_coefs_FLP scales LPC_in_pre by 1.0f / Gains[i], the
			// unquantised noise-shape gains, and find_LPC's minInvGain reads
			// the exact coding_quality.
			for k := range invGains {
				invGains[k] = f32(1.0 / e.pendingShape32.gains[k])
			}
			codingQuality = e.pendingShape32.codingQuality
		}
		domainConfig = []lpcInPreConfig{{
			input:           e.lpcInPreInput(signal),
			subframeLengths: equalSubframeLengths(e.frameSize, e.nSubframes),
			invGains:        invGains,
			ltpCoefs:        ltpQ14ToFloat(ltpCoeffsQ14),
			pitchLags:       pitchLags,
			ltpPredCodGain:  ltpPredCodGain,
			codingQuality:   codingQuality,
			voiced:          signalType == SignalTypeVoiced,
		}}
	}
	nlsf := e.analyzeNLSF(signal, cb, signalType, domainConfig...)
	if len(domainConfig) == 0 {
		quantOffset = e.estimateQuantOffsetType(signal, nlsf.lpcQ12, signalType, pitchLag, pitchGain)
	}
	e.curLTPScaleIndex = 0
	if signalType == SignalTypeVoiced && len(domainConfig) == 1 {
		e.curLTPScaleIndex = silkLTPScaleCtrl(!conditionalGain && !e.codeNoLTPScaling, domainConfig[0].ltpPredCodGain,
			e.packetLossPerc, e.packetFrames, e.lbrrFlag, e.frameSNRdBQ7())
	}

	// VBR: encode_frame_FLP runs its quantiser loop once, so the coded gains
	// are silk_process_gains_FLP's (exact port); the Go budget search only
	// serves CBR. CBR still computes the process_gains result because
	// silk_LBRR_encode_FLP quantises the redundant copy from those gains
	// (the loop's gain adjustments never reach the LBRR frame).
	e.exactGainSymbols = nil
	var exactGains *silkProcessGainsResult
	if e.haveShape32 && len(domainConfig) == 1 {
		exactGains = e.exactProcessGains(signal, signalType, quantOffset, nlsf, domainConfig[0], conditionalGain)
	}
	var gainIndices, pitchLags []int
	var ltpCoeffsQ14 [][5]int16
	var ltpScaleQ14 int16
	var frameSeed int32
	var pulses []int16
	rateScale := 1.0
	if exactGains != nil {
		// silk_encode_frame_FLP: code the frame from the process_gains gains
		// and, when the packet has a bit budget, iterate the quantiser loop.
		quantOffset = exactGains.quantOffset
		e.lastSNRVBRFrame = false
		e.restoreFrameState(initialState)
		r := e.encodeFrameLoop(enc, initialState, signal, vadActive, signalType, quantOffset, conditionalGain,
			nlsf, cb, pitchLag, pitchGain, exactGains, e.frameMaxBits, e.frameUseCBR)
		gainIndices, pitchLags, ltpCoeffsQ14, ltpScaleQ14 = r.gainIndices, r.pitchLags, r.ltpCoeffsQ14, r.ltpScaleQ14
		frameSeed, pulses, quantOffset = r.frameSeed, r.pulses, r.quantOffset
	} else {
		plan := e.selectRateControlPlan(initialState, signal, vadActive, signalType, quantOffset, conditionalGain, nlsf, pitchLag, pitchGain, domainGainTargets)
		e.lastSNRVBRFrame = plan.snrVBR
		rateScale = plan.rateScale

		e.restoreFrameState(initialState)
		e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)
		gainIndices = e.encodeGains(enc, signalType, plan.gainTargets, conditionalGain)
		e.encodeNLSF(enc, cb, signalType, nlsf)
		if e.nSubframes == 4 {
			enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
		}

		ltpCoeffsQ14 = make([][5]int16, e.nSubframes)
		ltpScaleQ14 = silkLTPScalesTable[0]
		pitchLags = make([]int, e.nSubframes)
		for sf := range pitchLags {
			pitchLags[sf] = pitchLag
		}
		if signalType == SignalTypeVoiced {
			ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, signal, nlsf.lpcQ12, pitchGain, conditionalGain)
		}

		if len(plan.gainIndices) == len(gainIndices) {
			gainIndices = plan.gainIndices
		}
		// silk_encode_frame_FLP: indices.Seed = frameCounter++ & 3 seeds the
		// delayed-decision states; the winner's initial seed is what gets coded.
		frameSeed = int32(e.frameCounter & 3)
		e.frameCounter++
		e.nsqSeed = frameSeed
		e.traceNLSFQ15 = nlsf.nlsfQ15
		pulses = e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, gainIndices,
			signalType, quantOffset, frameSeed, pitchLags, ltpCoeffsQ14, ltpScaleQ14, plan.rateScale)
		enc.EncodeIcdf(int(e.nsqSeed), silkUniform4ICDF[:], 8)
		e.encodePulses(enc, pulses, signalType, quantOffset)
	}
	e.pendingTrace.Shape32 = e.pendingShape32
	e.pendingTrace.NLSFTargetQ15 = append([]int16(nil), e.traceNLSFTargetQ15...)
	e.pendingTrace.InterpFactor = nlsf.interpFactor
	e.pendingTrace.MinInvGain = e.traceMinInvGain
	e.pendingTrace.LPCInPre = e.traceLPCInPre
	e.pendingTrace.InvGains = e.traceInvGains
	e.lastTrace = e.pendingTrace

	// Low-Bitrate Redundancy: generate (but do not yet emit) a coarse redundant
	// copy of this frame. It is buffered and written at the front of the next
	// packet (the one-packet FEC delay). Mirrors silk_LBRR_encode_FLP: reuse the
	// regular side information, bump the gains, and re-run the quantizer.
	var leakBefore string
	if lbrrDebug {
		leakBefore = e.leakFingerprint()
	}
	// The LBRR copy starts from silk_process_gains_FLP's gain symbols and
	// LastGainIndex (in VBR these are the coded gains; in CBR the Go budget
	// search may have coded other gains for the regular frame).
	lbrrGainSymbols, lastGainIndex := e.lastGainSymbols, e.prevGainIdx
	if len(gainIndices) > 0 {
		lastGainIndex = gainIndices[len(gainIndices)-1]
	}
	if exactGains != nil && len(exactGains.symbols) == e.nSubframes {
		lbrrGainSymbols = exactGains.symbols
		lastGainIndex = exactGains.absIndices[e.nSubframes-1]
	}
	e.generateLBRRFrame(signal, signalType, quantOffset, lbrrGainSymbols, lastGainIndex, conditionalGain, nlsf,
		pitchLags, ltpCoeffsQ14, ltpScaleQ14, frameSeed, rateScale, initialState)
	if lbrrDebug {
		if after := e.leakFingerprint(); after != leakBefore {
			fmt.Fprintf(os.Stderr, "[LBRR LEAK]\n  before=%s\n  after =%s\n", leakBefore, after)
		}
	}

	e.prevNLSF = nlsfQ15ToRadians(nlsf.nlsfQ15)
	if len(e.prevNLSFQ15) == len(nlsf.nlsfQ15) {
		copy(e.prevNLSFQ15, nlsf.nlsfQ15)
	} else {
		e.prevNLSFQ15 = append([]int16(nil), nlsf.nlsfQ15...)
	}
	if len(gainIndices) > 0 {
		e.prevGainIdx = gainIndices[len(gainIndices)-1]
	}
	e.prevEnergy = computeEnergy(signal)
	e.prevSignalType = signalType
	if signalType == SignalTypeVoiced && len(pitchLags) > 0 {
		e.prevLagForPitch = pitchLags[len(pitchLags)-1]
	} else {
		e.prevLagForPitch = 0
	}
	e.advanceInputBuffer()
	e.firstFrameAfterReset = false
}

// silkLAShapeMs mirrors LA_SHAPE_MS: the fixed look-ahead the SILK encoder keeps
// past the coded frame regardless of the complexity-dependent la_shape.
const silkLAShapeMs = 5

// ltpMemLength returns ltp_mem_length in samples.
func (e *Encoder) ltpMemLength() int {
	return peLtpMemLengthMs * (e.sampleRate / 1000)
}

// laShapeLength returns LA_SHAPE_MS*fs_kHz, the look-ahead region of xBuf.
func (e *Encoder) laShapeLength() int {
	return silkLAShapeMs * (e.sampleRate / 1000)
}

// pushInputFrame appends the caller's frame at x_frame + LA_SHAPE_MS*fs_kHz
// like silk_encode_frame_FLP and returns the coded frame x_frame[0:frame],
// i.e. the previous frame's last 5 ms followed by the first 15 ms of input.
func (e *Encoder) pushInputFrame(input []float64) []float64 {
	ltpMem := e.ltpMemLength()
	la := e.laShapeLength()
	want := ltpMem + la + e.frameSize
	if len(e.xBuf) != want {
		e.xBuf = make([]float64, want)
	}
	e.pitchHist = e.xBuf[:ltpMem]
	copy(e.xBuf[ltpMem+la:], input[:e.frameSize])
	e.addAntiDenormalOffsets()
	if cap(e.lastXBuf) < len(e.xBuf) {
		e.lastXBuf = make([]float32, len(e.xBuf))
	}
	e.lastXBuf = e.lastXBuf[:len(e.xBuf)]
	for i, v := range e.xBuf {
		e.lastXBuf[i] = float32(v * 32768)
	}
	return e.xBuf[ltpMem : ltpMem+e.frameSize]
}

// codedFrameLookahead returns the LA_SHAPE_MS samples that follow the coded
// frame in xBuf (the first 5 ms of the most recent input frame).
func (e *Encoder) codedFrameLookahead() []float64 {
	ltpMem := e.ltpMemLength()
	if len(e.xBuf) < ltpMem+e.frameSize+e.laShapeLength() {
		return nil
	}
	return e.xBuf[ltpMem+e.frameSize : ltpMem+e.frameSize+e.laShapeLength()]
}

// advanceInputBuffer shifts xBuf by one frame once the frame is coded, so the
// history ends at the coded frame and the old look-ahead heads the next frame
// (silk_encode_frame_FLP: silk_memmove(x_buf, &x_buf[frame_length], ...)).
func (e *Encoder) advanceInputBuffer() {
	if len(e.xBuf) < e.frameSize {
		return
	}
	copy(e.xBuf, e.xBuf[e.frameSize:])
	clear(e.xBuf[len(e.xBuf)-e.frameSize:])
}

func (e *Encoder) snapshotFrameState() encoderFrameState {
	return encoderFrameState{
		prevPitchLag:    e.prevPitchLag,
		prevLagIndex:    e.prevLagIndex,
		prevGainQ16:     e.prevGainQ16,
		lpcState:        append([]int32(nil), e.lpcState...),
		ltpState:        append([]int32(nil), e.ltpState...),
		nsq:             e.nsq.clone(),
		shapeHarmSmooth: e.shapeHarmSmooth,
		shapeTiltSmooth: e.shapeTiltSmooth,
		ltpSumLogGainQ7: e.ltpSumLogGainQ7,
	}
}

func (e *Encoder) restoreFrameState(st encoderFrameState) {
	e.prevPitchLag = st.prevPitchLag
	e.prevLagIndex = st.prevLagIndex
	e.prevGainQ16 = st.prevGainQ16
	if len(st.lpcState) == len(e.lpcState) {
		copy(e.lpcState, st.lpcState)
	}
	if len(st.ltpState) == len(e.ltpState) {
		copy(e.ltpState, st.ltpState)
	}
	e.nsq.copyFrom(st.nsq)
	e.shapeHarmSmooth = st.shapeHarmSmooth
	e.shapeTiltSmooth = st.shapeTiltSmooth
	e.ltpSumLogGainQ7 = st.ltpSumLogGainQ7
}

func (e *Encoder) selectRateControlPlan(
	initial encoderFrameState,
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
	domainGainTargets []int,
) rateControlPlan {
	baseTargets := e.analysisGainIndices(signal)
	if len(domainGainTargets) == e.nSubframes {
		baseTargets = append([]int(nil), domainGainTargets...)
	}
	baseIndices := e.resolveGainIndices(baseTargets, conditionalGain)
	if signalType == SignalTypeInactive {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}
	// When trellis is explicitly disabled, voiced SILK-only frames keep the
	// proven no-rate-control heuristic-gain path. Hybrid frames still run the
	// budget search so their homebrew NSQ cannot consume the shared SILK+CELT
	// packet budget.
	if signalType == SignalTypeVoiced && !e.voicedUsesTrellis() && !e.hybridMode {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}

	// Seed the rate-control search from excitation-normalized gains rather than
	// the mis-scaled dB heuristic. Unvoiced frames have no LTP prediction so the
	// gain normalizes the signal directly (Q5d); voiced frames take the
	// noise-shape + process_gains pipeline so the gain matches the spectral
	// envelope and bounds the residual, instead of the heuristic that flooded the
	// shell coder with pulses (Step 4).
	if len(domainGainTargets) == e.nSubframes {
		// Mono SILK-only find_LPC_FLP-domain migration: these gains were already
		// computed before NLSF analysis so Burg could run in the gain-scaled
		// domain. Keep the same targets for the coded gain plan.
	} else if signalType == SignalTypeVoiced {
		pitchLags := e.reconstructCurrentPitchLags()
		_, _, ltpCoeffsQ14 := e.selectLTPGainsVQ(signal, nlsf.lpcQ12, pitchLags)
		baseTargets = e.shapeGainIndices(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, signalType, quantOffset, pitchLags, ltpCoeffsQ14, pitchGain)
	} else {
		// Unvoiced keeps the Q5d excitation-normalised gains whether it runs the
		// homebrew or the trellis NSQ. The trellis is used with *neutral* shaping
		// for unvoiced (see closedLoopNSQWithRateScale), so its objective is
		// broadband error like homebrew's; the excitation-RMS gain (RMS ≈ 2
		// pulse-units) is the operating point tuned for that — it sets the right
		// pulse density to spend the rate budget on broadband SNR. The
		// spectral-envelope shape gains would normalise the excitation sparser and
		// leave the budget search under-spending the noise frame.
		baseTargets = e.excitationGainIndicesResidual(signal, nlsf.lpcQ12)
	}
	baseIndices = e.resolveGainIndices(baseTargets, conditionalGain)

	targetBits := e.silkFrameTargetBits()
	if targetBits <= 0 {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}

	if signalType == SignalTypeVoiced && e.useSNRTargetVBR && !e.stereoComponent {
		e.restoreFrameState(initial)
		snrRateScale := 1.0
		totalBits, gainIndices := e.estimateFrameCandidateBits(
			signal, vadActive, signalType, quantOffset, conditionalGain,
			baseTargets, nlsf, pitchLag, pitchGain, snrRateScale)
		if totalBits <= targetBits {
			e.restoreFrameState(initial)
			return rateControlPlan{gainTargets: baseTargets, gainIndices: gainIndices, rateScale: snrRateScale, snrVBR: true}
		}
	}

	return e.selectBudgetRateControlPlan(initial, signal, vadActive, signalType, quantOffset, conditionalGain, nlsf, pitchLag, pitchGain, baseTargets, baseIndices, targetBits)
}

func (e *Encoder) selectBudgetRateControlPlan(
	initial encoderFrameState,
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
	baseTargets []int,
	baseIndices []int,
	targetBits int,
) rateControlPlan {

	gainBoosts := []int{0, 2, 4, 6, 8, 10, 12}
	rateScales := []float64{1, 2, 4, 8, 16, 32, 64, 128, 512}
	if e.complexity < 4 {
		gainBoosts = []int{0, 4, 8, 12}
		rateScales = []float64{1, 4, 16, 64, 512}
	}
	if e.hybridMode {
		// The low band shares a hard packet ceiling with CELT. Some sustained
		// voiced frames need a much stronger pulse penalty than SILK-only quality
		// control permits, so keep searching until a compact fallback is found.
		gainBoosts = []int{0, 4, 8, 12, 16, 20, 24, 32, 40, 48}
		rateScales = []float64{1, 4, 16, 64, 256, 1024, 4096}
	} else if e.lbrrBitsPerFrame > 0 {
		// LBRR is part of the configured SILK packet budget, not additive
		// overhead. Search beyond the ordinary quality-control range when the
		// redundant copy leaves a small regular-frame budget.
		gainBoosts = []int{0, 2, 4, 6, 8, 10, 12, 16, 20, 24}
		rateScales = []float64{1, 2, 4, 8, 16, 32, 64, 128, 512, 1024, 4096}
	}

	best := rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	maxInt := int(^uint(0) >> 1)
	bestOver := maxInt
	bestTotal := maxInt
	inputRMS := math.Sqrt(computeEnergy(signal))
	minOutputRMS := inputRMS / 20
	if minOutputRMS < 0.006 {
		minOutputRMS = 0.006
	}

	bestBudget := rateControlPlan{}
	hasBudget := false
	bestBudgetBits := -1

	for _, boost := range gainBoosts {
		targets := boostedGainTargets(baseTargets, boost)
		e.restoreFrameState(initial)
		headerBits, gainIndices, pitchLags, ltpCoeffsQ14, ltpScaleQ14 := e.estimateFrameHeaderBits(
			signal, vadActive, signalType, quantOffset, conditionalGain, targets, nlsf, pitchLag, pitchGain)
		for _, scale := range rateScales {
			e.restoreFrameState(initial)
			pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, gainIndices,
				signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, scale)
			// Prefer preserving SILK-layer activity, especially for SILK-only and
			// unvoiced hybrid frames where CELT cannot replace the low band. If no
			// such candidate fits, strict CBR falls back to the fullest candidate
			// within budget below rather than violating the packet-size contract.
			floorOK := pulsesMeetActivityFloor(pulses, e.frameSize)
			rmsOK := e.currentFrameOutputRMS() >= minOutputRMS
			pulseBits := e.estimatePulseBits(pulses, signalType, quantOffset)
			totalBits := headerBits + pulseBits + 8
			if rmsOK && totalBits <= targetBits {
				if !hasBudget || totalBits > bestBudgetBits {
					bestBudget = rateControlPlan{gainTargets: targets, gainIndices: gainIndices, rateScale: scale}
					bestBudgetBits = totalBits
					hasBudget = true
				}
			}
			preserveLowBand := !e.hybridMode || signalType != SignalTypeVoiced
			if preserveLowBand && (!floorOK || !rmsOK) {
				continue
			}
			over := totalBits - targetBits
			if over <= 0 {
				e.restoreFrameState(initial)
				return rateControlPlan{gainTargets: targets, gainIndices: gainIndices, rateScale: scale}
			}
			if over < bestOver || (over == bestOver && totalBits < bestTotal) {
				bestOver = over
				bestTotal = totalBits
				best = rateControlPlan{gainTargets: targets, gainIndices: gainIndices, rateScale: scale}
			}
		}
	}

	if hasBudget && e.rateMode == RateModeCBR {
		e.restoreFrameState(initial)
		return bestBudget
	}

	e.restoreFrameState(initial)
	return best
}

func (e *Encoder) estimateFrameCandidateBits(
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	gainTargets []int,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
	rateScale float64,
) (bits int, gainIndices []int) {
	enc := entcode.NewEncoder((e.silkFrameTargetBits() + 7) / 8)
	e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)
	gainIndices = e.encodeGains(enc, signalType, gainTargets, conditionalGain)
	cb := getNLSFCB(e.lpcOrder)
	e.encodeNLSF(enc, cb, signalType, nlsf)
	if e.nSubframes == 4 {
		enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
	}

	ltpCoeffsQ14 := make([][5]int16, e.nSubframes)
	ltpScaleQ14 := silkLTPScalesTable[0]
	pitchLags := make([]int, e.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	if signalType == SignalTypeVoiced {
		ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, signal, nlsf.lpcQ12, pitchGain, conditionalGain)
	}

	e.nsqSeed = 0
	pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, gainIndices,
		signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, rateScale)
	enc.EncodeIcdf(int(e.nsqSeed), silkUniform4ICDF[:], 8)
	e.encodePulses(enc, pulses, signalType, quantOffset)
	enc.Flush()
	return len(enc.Bytes()) * 8, gainIndices
}

func pulsesMeetActivityFloor(pulses []int16, frameSize int) bool {
	nonZero := 0
	absSum := 0
	for _, p := range pulses {
		if p == 0 {
			continue
		}
		nonZero++
		if p < 0 {
			absSum += int(-p)
		} else {
			absSum += int(p)
		}
	}
	minNonZero := frameSize / 8
	if minNonZero < 16 {
		minNonZero = 16
	}
	minAbsSum := frameSize
	if minAbsSum < 64 {
		minAbsSum = 64
	}
	return nonZero >= minNonZero && absSum >= minAbsSum
}

func (e *Encoder) currentFrameOutputRMS() float64 {
	if e.frameSize <= 0 || len(e.ltpState) < e.frameSize {
		return 0
	}
	start := len(e.ltpState) - e.frameSize
	sum := 0.0
	for _, s := range e.ltpState[start:] {
		v := float64(s) / 32768.0
		sum += v * v
	}
	return math.Sqrt(sum / float64(e.frameSize))
}

func (e *Encoder) silkFrameTargetBits() int {
	bits := e.bitrate * e.frameMs / 1000
	if e.channels == 2 || e.stereoComponent {
		bits /= 2
	}
	bits -= e.lbrrBitsPerFrame
	if bits < 16 {
		bits = 16
	}
	return bits
}

func isShortLagVoiced(fsKHz, pitchLag int) bool {
	return pitchLag > 0 && pitchLag < 5*fsKHz
}

func boostedGainTargets(base []int, boost int) []int {
	out := make([]int, len(base))
	for i, v := range base {
		out[i] = clampInt(v+boost, 0, NLevelsQGain-1)
	}
	return out
}

func (e *Encoder) estimateFrameHeaderBits(
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	gainTargets []int,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
) (bits int, gainIndices []int, pitchLags []int, ltpCoeffsQ14 [][5]int16, ltpScaleQ14 int16) {
	enc := entcode.NewEncoder((e.silkFrameTargetBits() + 7) / 8)
	e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)
	gainIndices = e.encodeGains(enc, signalType, gainTargets, conditionalGain)
	cb := getNLSFCB(e.lpcOrder)
	e.encodeNLSF(enc, cb, signalType, nlsf)
	if e.nSubframes == 4 {
		enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
	}
	ltpCoeffsQ14 = make([][5]int16, e.nSubframes)
	ltpScaleQ14 = silkLTPScalesTable[0]
	pitchLags = make([]int, e.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	if signalType == SignalTypeVoiced {
		ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, signal, nlsf.lpcQ12, pitchGain, conditionalGain)
	}
	enc.EncodeIcdf(0, silkUniform4ICDF[:], 8)
	enc.Flush()
	return len(enc.Bytes()) * 8, gainIndices, pitchLags, ltpCoeffsQ14, ltpScaleQ14
}

func (e *Encoder) estimatePulseBits(pulses []int16, signalType, quantOffset int) int {
	enc := entcode.NewEncoder((e.silkFrameTargetBits() + 7) / 8)
	e.encodePulses(enc, pulses, signalType, quantOffset)
	enc.Flush()
	return len(enc.Bytes()) * 8
}

func (e *Encoder) analyzePitch(signal []float64) (int, float64) {
	fsKHz := e.sampleRate / 1000
	minLag := PitchEstMinLagMs * fsKHz
	maxLag := PitchEstMaxLagMs * fsKHz
	if len(signal) <= minLag {
		return e.prevPitchLag, 0
	}
	if maxLag >= len(signal) {
		maxLag = len(signal) - 1
	}

	energy := 0.0
	for _, v := range signal {
		energy += v * v
	}
	if energy <= 1e-12 {
		return e.prevPitchLag, 0
	}

	bestLag := minLag
	bestCorr := 0.0
	for lag := minLag; lag <= maxLag; lag++ {
		corr, currentEnergy, lagEnergy := 0.0, 0.0, 0.0
		windowLen := len(signal) - lag
		for i := 0; i < windowLen; i++ {
			current := signal[i+lag]
			delayed := signal[i]
			corr += current * delayed
			currentEnergy += current * current
			lagEnergy += delayed * delayed
		}
		if currentEnergy <= 1e-12 || lagEnergy <= 1e-12 {
			continue
		}
		norm := corr / math.Sqrt(currentEnergy*lagEnergy)
		if norm > bestCorr {
			bestCorr = norm
			bestLag = lag
		}
	}
	if bestCorr < 0 {
		bestCorr = 0
	}
	if bestCorr > 1 {
		bestCorr = 1
	}
	return bestLag, bestCorr
}

// encodePitchAndLTP encodes the absolute lag index and pitch contour index
// selected by silk_find_pitch_lags_FLP (e.curPitchLagIndex /
// e.curPitchContourIndex), then the LTP gains. It reconstructs the per-subframe
// pitch lags exactly as the decoder does (from the encoded indices) so the NSQ
// uses the same lags the decoder will, and returns them alongside the LTP taps.
func (e *Encoder) encodePitchAndLTP(enc *entcode.Encoder, signal []float64, lpcQ12 []int16, pitchGain float64, conditionalGain bool) ([][5]int16, int16, []int) {
	fsKHz := e.sampleRate / 1000

	step := fsKHz >> 1
	if step < 1 {
		step = 1
	}
	coreLagIndex := e.curPitchLagIndex
	if coreLagIndex < 0 {
		coreLagIndex = 0
	}
	lagIndex := coreLagIndex / step
	lagLowBits := coreLagIndex % step
	if lagIndex >= len(silkPitchLagICDF) {
		lagIndex = len(silkPitchLagICDF) - 1
		lagLowBits = step - 1
	}

	contourIndex := e.curPitchContourIndex
	contourMax := contourCBSize(fsKHz, e.nSubframes)
	if contourIndex < 0 || contourIndex >= contourMax {
		contourIndex = 0
	}

	// Reconstruct the per-subframe lags the way the decoder will, from the
	// encoded indices (so encoder and decoder stay bit-for-bit in sync).
	recLag := lagIndex*step + lagLowBits
	encodeLagIndex(enc, fsKHz, recLag, conditionalGain && e.prevSignalType == SignalTypeVoiced, e.prevLagIndex)
	encodePitchContour(enc, fsKHz, e.nSubframes, contourIndex)
	pitchLags := e.reconstructCurrentPitchLags()

	ltpPerIdx, ltpGainIndices, ltpCoeffsQ14 := e.selectLTPGainsVQ(signal, lpcQ12, pitchLags)
	enc.EncodeIcdf(ltpPerIdx, silkLTPPerIndexICDF[:], 8)
	for sf := 0; sf < e.nSubframes; sf++ {
		ltpGainIdx := ltpGainIndices[sf]
		switch ltpPerIdx {
		case 0:
			enc.EncodeIcdf(ltpGainIdx, silkLTPGainICDF0[:], 8)
		case 1:
			enc.EncodeIcdf(ltpGainIdx, silkLTPGainICDF1[:], 8)
		default:
			enc.EncodeIcdf(ltpGainIdx, silkLTPGainICDF2[:], 8)
		}
	}
	// silk_LTP_scale_ctrl_FLP: the scale index is coded for independently
	// coded frames only (CODE_INDEPENDENTLY, not the NO_LTP_SCALING variant)
	// and is 0 otherwise.
	ltpScaleIndex := 0
	if !conditionalGain && !e.codeNoLTPScaling {
		ltpScaleIndex = e.curLTPScaleIndex
		enc.EncodeIcdf(ltpScaleIndex, silkLTPScaleICDF[:], 8)
	}

	e.prevPitchLag = pitchLags[e.nSubframes-1]
	e.prevLagIndex = recLag

	// Capture the coded pitch/LTP indices so the LBRR generator can replay this
	// frame's side information into the next packet without recomputation.
	e.capLagIndex = recLag
	e.capContour = contourIndex
	e.capLTPPerIdx = ltpPerIdx
	e.capLTPGainIdx = append([]int(nil), ltpGainIndices...)
	e.capLTPScaleIndex = ltpScaleIndex

	return ltpCoeffsQ14, silkLTPScalesTable[ltpScaleIndex], pitchLags
}

// reconstructCurrentPitchLags rebuilds the per-subframe pitch lags from the
// current frame's quantized lag/contour indices the same way the decoder does
// (mirrors the lag math in encodePitchAndLTP). Gain analysis uses it so the LTP
// residual it measures lines up with the lags actually written to the bitstream.
func (e *Encoder) reconstructCurrentPitchLags() []int {
	fsKHz := e.sampleRate / 1000
	minLag := PitchEstMinLagMs * fsKHz
	maxLag := PitchEstMaxLagMs * fsKHz
	step := fsKHz >> 1
	if step < 1 {
		step = 1
	}
	coreLagIndex := e.curPitchLagIndex
	if coreLagIndex < 0 {
		coreLagIndex = 0
	}
	lagIndex := coreLagIndex / step
	lagLowBits := coreLagIndex % step
	if lagIndex >= len(silkPitchLagICDF) {
		lagIndex = len(silkPitchLagICDF) - 1
		lagLowBits = step - 1
	}
	contourIndex := e.curPitchContourIndex
	contourMax := contourCBSize(fsKHz, e.nSubframes)
	if contourIndex < 0 || contourIndex >= contourMax {
		contourIndex = 0
	}
	baseLag := minLag + lagIndex*step + lagLowBits
	contourOffsets := silkPitchContourOffsets(contourIndex, e.nSubframes, fsKHz)
	pitchLags := make([]int, e.nSubframes)
	for sf := 0; sf < e.nSubframes; sf++ {
		pitchLags[sf] = clampInt(baseLag+contourOffsets[sf], minLag, maxLag)
	}
	return pitchLags
}

// ltpCoeffsForIndices builds the per-subframe Q14 LTP coefficient set from the
// quantized periodicity/gain indices (the codebook entries the decoder reads).
func ltpCoeffsForIndices(ltpPerIdx, ltpGainIdx, nSubframes int) [][5]int16 {
	ltpCoeffsQ14 := make([][5]int16, nSubframes)
	for sf := 0; sf < nSubframes; sf++ {
		for k := 0; k < 5; k++ {
			switch ltpPerIdx {
			case 0:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ0[ltpGainIdx][k]) << 7
			case 1:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ1[ltpGainIdx][k]) << 7
			default:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ2[ltpGainIdx][k]) << 7
			}
		}
	}
	return ltpCoeffsQ14
}

// contourCBSize returns the number of pitch-contour codebook entries for the
// given sample rate and subframe count (matching the decoder ICDF tables).
func contourCBSize(fsKHz, nSubframes int) int {
	switch {
	case fsKHz == 8 && nSubframes == 4:
		return 11
	case fsKHz == 8:
		return 3
	case nSubframes == 4:
		return 34
	default:
		return 12
	}
}

// encodePitchContour encodes the pitch contour index using the ICDF matching the
// decoder's silkPitchContourOffsets selection.
func encodePitchContour(enc *entcode.Encoder, fsKHz, nSubframes, contourIndex int) {
	switch {
	case fsKHz == 8 && nSubframes == 4:
		enc.EncodeIcdf(contourIndex, silkPitchContourNBICDF[:], 8)
	case fsKHz == 8:
		enc.EncodeIcdf(contourIndex, silkPitchContour10msNBICDF[:], 8)
	case nSubframes == 4:
		enc.EncodeIcdf(contourIndex, silkPitchContourICDF[:], 8)
	default:
		enc.EncodeIcdf(contourIndex, silkPitchContour10msICDF[:], 8)
	}
}

func encodePitchLagLowBits(enc *entcode.Encoder, fsKHz, lagLowBits int) {
	switch fsKHz {
	case 8:
		enc.EncodeIcdf(clampInt(lagLowBits, 0, 3), silkUniform4ICDF[:], 8)
	case 12:
		enc.EncodeIcdf(clampInt(lagLowBits, 0, 5), silkUniform6ICDF[:], 8)
	default:
		enc.EncodeIcdf(clampInt(lagLowBits, 0, 7), silkUniform8ICDF[:], 8)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func encodeFlatPitchContour(enc *entcode.Encoder, fsKHz, nSubframes int) {
	switch {
	case fsKHz == 8 && nSubframes == 4:
		enc.EncodeIcdf(0, silkPitchContourNBICDF[:], 8)
	case fsKHz == 8:
		enc.EncodeIcdf(0, silkPitchContour10msNBICDF[:], 8)
	case nSubframes == 4:
		enc.EncodeIcdf(0, silkPitchContourICDF[:], 8)
	default:
		enc.EncodeIcdf(0, silkPitchContour10msICDF[:], 8)
	}
}

func selectLTPGain(pitchGain float64) (int, int) {
	target := pitchGain * 128.0
	bestPer, bestIdx := 0, 0
	bestErr := math.Inf(1)
	consider := func(perIdx, gainIdx int, taps [5]int8) {
		err := 0.0
		for k, tap := range taps {
			want := 0.0
			if k == 2 {
				want = target
			}
			diff := float64(tap) - want
			err += diff * diff
		}
		if err < bestErr {
			bestErr = err
			bestPer = perIdx
			bestIdx = gainIdx
		}
	}
	for i, taps := range silkLTPGainVQ0 {
		consider(0, i, taps)
	}
	for i, taps := range silkLTPGainVQ1 {
		consider(1, i, taps)
	}
	for i, taps := range silkLTPGainVQ2 {
		consider(2, i, taps)
	}
	return bestPer, bestIdx
}

func (e *Encoder) analysisExcitation(signal []float64, lpcQ12 []int16, signalType, pitchLag int, pitchGain float64) []float64 {
	if signalType == SignalTypeInactive {
		return make([]float64, len(signal))
	}

	residual := make([]float64, len(signal))
	for i := range signal {
		pred := 0.0
		for j := 0; j < e.lpcOrder && j < i; j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * signal[i-j-1]
		}
		residual[i] = signal[i] - pred
	}
	if signalType != SignalTypeVoiced || pitchGain <= 0 || pitchLag <= 0 {
		return residual
	}

	excitation := make([]float64, len(residual))
	copy(excitation, residual)
	ltpGain := pitchGain
	if ltpGain > 0.8 {
		ltpGain = 0.8
	}
	for i := pitchLag; i < len(excitation); i++ {
		excitation[i] -= ltpGain * residual[i-pitchLag]
	}
	return excitation
}

func (e *Encoder) analyzeNoiseShape(signal []float64, signalType int, pitchGain float64) silkShapeAnalysis {
	out := silkShapeAnalysis{subframes: make([]silkShapeSubframe, e.nSubframes)}
	if e.nSubframes == 0 {
		return out
	}
	subframeLen := e.frameSize / e.nSubframes
	frameEnergy := computeEnergy(signal)
	frameRMS := math.Sqrt(frameEnergy + 1e-18)
	for sf := 0; sf < e.nSubframes; sf++ {
		start := sf * subframeLen
		end := start + subframeLen
		if end > len(signal) {
			end = len(signal)
		}
		if start >= end {
			out.subframes[sf] = defaultShapeSubframe(signalType, pitchGain)
			continue
		}

		energy, lag1, diffEnergy := 0.0, 0.0, 0.0
		prev := signal[start]
		for i := start; i < end; i++ {
			x := signal[i]
			energy += x * x
			if i > start {
				lag1 += x * prev
				d := x - prev
				diffEnergy += d * d
			}
			prev = x
		}
		n := float64(end - start)
		rms := math.Sqrt(energy/n + 1e-18)
		tilt := 0.0
		if energy > 1e-12 {
			tilt = lag1 / energy
		}
		tilt = clampFloat(tilt, -0.75, 0.75)
		hfRatio := 0.0
		if energy > 1e-12 {
			hfRatio = diffEnergy / (4.0 * energy)
		}
		hfRatio = clampFloat(hfRatio, 0, 1)

		activity := clampFloat(rms/(frameRMS+1e-9), 0.35, 2.0)
		shape := defaultShapeSubframe(signalType, pitchGain)
		shape.tilt = 0.18 * tilt
		shape.lf = clampFloat(-0.11*tilt, -0.10, 0.10)
		shape.hf = clampFloat(0.14*(hfRatio-0.22), -0.05, 0.13)
		shape.lambda *= clampFloat(1.10-0.12*activity+0.10*hfRatio, 0.82, 1.24)
		if signalType == SignalTypeVoiced {
			shape.feedback = clampFloat(0.42+0.10*pitchGain+0.04*math.Max(tilt, 0), 0.40, 0.58)
			shape.harmonic = clampFloat(0.10+0.42*pitchGain, 0, 0.55)
			shape.lambda *= clampFloat(0.96-0.08*pitchGain, 0.86, 1.0)
		} else {
			shape.feedback = clampFloat(0.25+0.10*math.Max(tilt, 0)+0.09*hfRatio, 0.22, 0.42)
			shape.harmonic = 0
			shape.lambda *= clampFloat(1.0+0.12*hfRatio, 1.0, 1.12)
		}
		out.subframes[sf] = shape
	}
	return out
}

func defaultShapeSubframe(signalType int, pitchGain float64) silkShapeSubframe {
	shape := silkShapeSubframe{
		feedback: 0.30,
		lambda:   1.0,
	}
	if signalType == SignalTypeVoiced {
		shape.feedback = 0.50
		shape.harmonic = clampFloat(0.10+0.42*pitchGain, 0, 0.55)
		shape.lambda = 0.92
	}
	return shape
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (e *Encoder) encodeTypeOffset(enc *entcode.Encoder, vadActive bool, signalType, quantOffset int) {
	typeIx := signalType*2 + quantOffset
	if vadActive {
		symbol := typeIx - 2
		if symbol < 0 {
			symbol = 0
		}
		if symbol > 3 {
			symbol = 3
		}
		enc.EncodeIcdf(symbol, silkTypeOffsetVADICDF[:], 8)
		return
	}
	symbol := typeIx
	if symbol < 0 {
		symbol = 0
	}
	if symbol > 1 {
		symbol = 1
	}
	enc.EncodeIcdf(symbol, silkTypeOffsetNoVADICDF[:], 8)
}

func (e *Encoder) analysisGainIndex(signal []float64) int {
	energy := computeEnergy(signal)
	return analysisGainIndexFromEnergy(energy)
}

func (e *Encoder) analysisGainIndices(signal []float64) []int {
	return e.gainIndicesFromEnergy(signal, analysisGainIndexFromEnergy)
}

// excitationGainIndicesResidual normalises the unvoiced excitation gain to the
// short-term LPC *residual* energy rather than the raw signal energy. The gain
// targets the quantized excitation level (RMS ≈ 2 pulse-units; the legacy dB
// heuristic in analysisGainIndices is ~1000× too low and floods the shell coder).
// For noise-like input the LPC predictor is nearly flat, so the residual matches
// the signal energy and unvoiced-noise quality is preserved. For a harmonic frame
// that the pitch analyser misclassifies as
// unvoiced (e.g. the first frame after reset, where the zero-history pitch search
// is unreliable), the LPC residual is far smaller than the signal: gating the
// gain on the signal energy would set it for the full tonal amplitude while the
// sharp LPC synthesis resonance amplifies further, producing a runaway (clipping)
// output that then poisons every subsequent voiced frame's LTP state. Using the
// residual energy keeps the synthesised level bounded.
func (e *Encoder) excitationGainIndicesResidual(signal []float64, lpcQ12 []int16) []int {
	targets := make([]int, e.nSubframes)
	if e.nSubframes == 0 {
		return targets
	}
	subLen := e.frameSize / e.nSubframes
	resNrg := e.ltpResidualEnergyPerSubframe(signal, lpcQ12, SignalTypeUnvoiced, nil, nil)
	const int16Scale = 32768.0 * 32768.0
	for sf := 0; sf < e.nSubframes; sf++ {
		energy := 0.0
		if subLen > 0 {
			energy = resNrg[sf] / (int16Scale * float64(subLen))
		}
		targets[sf] = excitationGainIndexFromEnergy(energy)
	}
	return targets
}

// shapeGainIndices derives per-subframe target gain indices from the libopus
// gain pipeline (silk_noise_shape_analysis_FLP gains + silk_process_gains_FLP
// soft limit), with the voiced SNR-target VBR backoff applied before gain_mult.
// The shape gains come from the noise-shaping spectral envelope (never
// near-zero, so steady voiced frames stay stable); the soft limit then floors
// the gain using the LPC+LTP residual energy so the quantized signal is bounded.
// Used for voiced frames where the dB heuristic mis-scaled the gain and flooded
// the shell coder with pulses (Step 4). The shape-smoothing state is saved and
// restored so this acts as a pure analysis pass.
func (e *Encoder) shapeGainAnalysis(signal []float64, lpcQ12 []int16, lpcInterpQ12 []int16, signalType, quantOffset int, pitchLags []int, ltpCoeffsQ14 [][5]int16, pitchGain float64) ([]int, silkNoiseShapeAnalysis) {
	harmSmooth, tiltSmooth := e.shapeHarmSmooth, e.shapeTiltSmooth
	shape := e.analyzeNoiseShapeFLP(signal, lpcQ12, signalType, quantOffset, pitchLags, pitchGain, e.speechActivity)
	e.shapeHarmSmooth, e.shapeTiltSmooth = harmSmooth, tiltSmooth

	resNrg := e.processGainsResidualEnergy(signal, lpcQ12, lpcInterpQ12, signalType, pitchLags, ltpCoeffsQ14, shape.Gains)
	subLen := e.frameSize / e.nSubframes
	invMaxSqr := 0.0
	if subLen > 0 {
		invMaxSqr = math.Pow(2.0, 0.33*(21.0-shape.SNRdB)) / float64(subLen)
	}
	silkTraceSNR("process_gains fs=%dkHz signal=%d snr=%.3fdB sub_len=%d inv_max_sqr=%.9f",
		e.sampleRate/1000, signalType, shape.SNRdB, subLen, invMaxSqr)

	// Gain reduction when the LTP coding gain is high (silk_process_gains_FLP):
	// a strong long-term predictor leaves a small residual, so the synthesis
	// gain can be scaled down, sparing pulses on steady voiced frames. The soft
	// limit below still floors the gain by the residual energy, so the reduction
	// only takes hold where the prediction is genuinely good (ratio_bytes ~2x→~1x).
	gainScale := 1.0
	if signalType == SignalTypeVoiced {
		ltpCodGainDB := e.ltpPredCodGainDB(signal, lpcQ12, e.ltpResidualEnergyPerSubframe(signal, lpcQ12, signalType, pitchLags, ltpCoeffsQ14), pitchLags, ltpCoeffsQ14)
		gainScale = 1.0 - 0.5*silkSigmoid(0.25*(ltpCodGainDB-12.0))
		silkTraceSNR("process_gains voiced ltp_cod_gain=%.3fdB gain_scale=%.6f", ltpCodGainDB, gainScale)
	}

	targets := make([]int, e.nSubframes)
	for sf := 0; sf < e.nSubframes; sf++ {
		gain := shape.Gains[sf] * gainScale
		// Soft limit on the ratio of residual energy to squared gain
		// (silk_process_gains_FLP): raises the gain when the prediction residual
		// is large, capping the number of pulses the NSQ has to spend.
		gain = math.Sqrt(gain*gain + resNrg[sf]*invMaxSqr)
		if gain > 32767 {
			gain = 32767
		}
		silkTraceSNR("process_gains sf=%d shape_gain=%.3f res_nrg=%.3f final_gain=%.3f gain_index=%d",
			sf, shape.Gains[sf], resNrg[sf], gain, silkQuantizeGainIndex(gain*65536.0))
		targets[sf] = silkQuantizeGainIndex(gain * 65536.0)
	}
	return targets, shape
}

func (e *Encoder) shapeGainIndices(signal []float64, lpcQ12 []int16, lpcInterpQ12 []int16, signalType, quantOffset int, pitchLags []int, ltpCoeffsQ14 [][5]int16, pitchGain float64) []int {
	targets, _ := e.shapeGainAnalysis(signal, lpcQ12, lpcInterpQ12, signalType, quantOffset, pitchLags, ltpCoeffsQ14, pitchGain)
	return targets
}

// ltpResidualEnergyPerSubframe returns the per-subframe LPC (+LTP for voiced)
// open-loop prediction residual energy in the int16-magnitude domain (sum of
// squares), used by the process_gains soft limit. Mirrors the residual libopus
// measures in silk_residual_energy_FLP.
func (e *Encoder) ltpResidualEnergyPerSubframe(signal []float64, lpcQ12 []int16, signalType int, pitchLags []int, ltpCoeffsQ14 [][5]int16) [silkMaxNBSubframes]float64 {
	var nrgs [silkMaxNBSubframes]float64
	if e.nSubframes == 0 {
		return nrgs
	}
	subLen := e.frameSize / e.nSubframes

	hist := e.pitchHist
	buf := make([]float64, len(hist)+len(signal))
	copy(buf, hist)
	copy(buf[len(hist):], signal)
	res := make([]float64, len(buf))
	for i := range buf {
		pred := 0.0
		for j := 0; j < e.lpcOrder && j <= i-1; j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * buf[i-j-1]
		}
		res[i] = buf[i] - pred
	}

	frameStart := len(hist)
	const int16Scale = 32768.0 * 32768.0
	for sf := 0; sf < e.nSubframes; sf++ {
		lag := 0
		var b [5]float64
		if signalType == SignalTypeVoiced {
			lag = pitchLags[clampInt(sf, 0, len(pitchLags)-1)]
			if sf < len(ltpCoeffsQ14) {
				for k := 0; k < 5; k++ {
					b[k] = float64(ltpCoeffsQ14[sf][k]) / 16384.0
				}
			}
		}
		sum := 0.0
		for i := 0; i < subLen; i++ {
			idx := frameStart + sf*subLen + i
			if idx >= len(res) {
				break
			}
			v := res[idx]
			if lag > 0 {
				for k := 0; k < 5; k++ {
					src := idx - lag + 2 - k
					if src >= 0 && src < len(res) {
						v -= b[k] * res[src]
					}
				}
			}
			sum += v * v
		}
		nrgs[sf] = sum * int16Scale
	}
	return nrgs
}

// ltpPredCodGainDB returns the LTP prediction coding gain in dB, the quantity
// silk_quant_LTP_gains reports as pred_gain_dB_Q7 / 128. libopus computes it as
// -3*log2(res_nrg) where res_nrg is the LTP residual energy normalised by the
// LPC residual energy (averaged over subframes). We reuse the open-loop LPC+LTP
// residual (passed in as ltpNrg) and an LPC-only residual pass for the
// denominator. A perfectly periodic (steady voiced) frame drives this high, so
// the process_gains reduction shrinks its gains the most.
func (e *Encoder) ltpPredCodGainDB(signal []float64, lpcQ12 []int16, ltpNrg [silkMaxNBSubframes]float64, pitchLags []int, ltpCoeffsQ14 [][5]int16) float64 {
	if e.nSubframes == 0 {
		return 0
	}
	lpcNrg := e.ltpResidualEnergyPerSubframe(signal, lpcQ12, SignalTypeUnvoiced, pitchLags, ltpCoeffsQ14)
	ratioSum := 0.0
	for sf := 0; sf < e.nSubframes; sf++ {
		denom := lpcNrg[sf]
		if denom < 1e-9 {
			denom = 1e-9
		}
		r := ltpNrg[sf] / denom
		// The optimal predictor never increases the residual; clamp so a crude
		// per-subframe LTP gain cannot push the coding gain negative.
		if r > 1.0 {
			r = 1.0
		}
		if r < 1e-9 {
			r = 1e-9
		}
		ratioSum += r
	}
	ratio := ratioSum / float64(e.nSubframes)
	if ratio < 1e-9 {
		ratio = 1e-9
	}
	return -3.0 * silkLog2(ratio)
}

func (e *Encoder) gainIndicesFromEnergy(signal []float64, fromEnergy func(float64) int) []int {
	targets := make([]int, e.nSubframes)
	if len(signal) == 0 {
		for i := range targets {
			targets[i] = 10
		}
		return targets
	}

	frameTarget := fromEnergy(computeEnergy(signal))
	subframeLen := e.frameSize / e.nSubframes
	for sf := range targets {
		start := sf * subframeLen
		end := start + subframeLen
		if start >= len(signal) {
			targets[sf] = frameTarget
			continue
		}
		if end > len(signal) {
			end = len(signal)
		}
		targets[sf] = fromEnergy(computeEnergy(signal[start:end]))
		if targets[sf] > frameTarget {
			targets[sf] = frameTarget
		}
	}
	return targets
}

func analysisGainIndexFromEnergy(energy float64) int {
	if energy <= 1e-12 {
		return 10
	}
	gainDB := LinearToDB(math.Sqrt(energy) + 1e-12)
	idx := int(math.Round((gainDB + 30.0) / 1.5))
	if idx < 10 {
		idx = 10
	}
	if idx > 40 {
		idx = 40
	}
	return idx
}

func excitationGainIndexFromEnergy(energy float64) int {
	if energy <= 1e-12 {
		return 10
	}
	// gainQ16 ≈ rms · 2^30 keeps the excitation RMS near 2 pulse-units, matching
	// libopus's operating point: small pulses the shell coder represents cheaply,
	// landing the per-frame size near the 24 kbps budget so rate control does not
	// have to throttle (legacy idx≈10 saturated the ±1024 clamp and blew budget).
	targetQ16 := math.Sqrt(energy) * float64(int64(1)<<30)
	return silkQuantizeGainIndex(targetQ16)
}

// silkQuantizeGainIndex returns the gain index whose dequantized Q16 gain is
// closest (in the log domain) to targetQ16, over the full SILK gain range.
func silkQuantizeGainIndex(targetQ16 float64) int {
	if targetQ16 < 1 {
		targetQ16 = 1
	}
	logTarget := math.Log(targetQ16)
	best := 0
	bestErr := math.Inf(1)
	for idx := 0; idx < NLevelsQGain; idx++ {
		g := float64(silkGainDequantQ16(idx))
		if g < 1 {
			g = 1
		}
		errAbs := math.Abs(math.Log(g) - logTarget)
		if errAbs < bestErr {
			bestErr = errAbs
			best = idx
		}
	}
	return best
}

func (e *Encoder) encodeGains(enc *entcode.Encoder, signalType int, targetIndices []int, conditional bool) []int {
	absIndices := make([]int, e.nSubframes)
	e.lastGainSymbols = make([]int, e.nSubframes)
	if sym := e.exactGainSymbols; len(sym) == e.nSubframes {
		// silk_encode_indices with the symbols silk_gains_quant produced.
		copy(e.lastGainSymbols, sym)
		if conditional {
			enc.EncodeIcdf(sym[0], silkDeltaGainICDF[:], 8)
		} else {
			enc.EncodeIcdf(sym[0]>>3, silkGainICDF[signalType][:], 8)
			enc.EncodeIcdf(sym[0]&7, silkUniform8ICDF[:], 8)
		}
		for sf := 1; sf < e.nSubframes; sf++ {
			enc.EncodeIcdf(sym[sf], silkDeltaGainICDF[:], 8)
		}
		copy(absIndices, targetIndices)
		return absIndices
	}
	prevIdx := e.prevGainIdx
	targetIdx := gainTargetAt(targetIndices, 0)
	if conditional {
		targetIdx, e.lastGainSymbols[0] = e.encodeGainDelta(enc, prevIdx, targetIdx)
	} else {
		if targetIdx < prevIdx-16 {
			targetIdx = prevIdx - 16
		}
		gainMSB := targetIdx >> 3
		gainLSB := targetIdx & 7
		if gainMSB > 7 {
			gainMSB = 7
			gainLSB = 7
			targetIdx = NLevelsQGain - 1
		}
		enc.EncodeIcdf(gainMSB, silkGainICDF[signalType][:], 8)
		enc.EncodeIcdf(gainLSB, silkUniform8ICDF[:], 8)
		e.lastGainSymbols[0] = targetIdx
	}
	absIndices[0] = targetIdx

	for sf := 1; sf < e.nSubframes; sf++ {
		targetIdx, e.lastGainSymbols[sf] = e.encodeGainDelta(enc, targetIdx, gainTargetAt(targetIndices, sf))
		absIndices[sf] = targetIdx
	}
	return absIndices
}

func (e *Encoder) resolveGainIndices(targetIndices []int, conditional bool) []int {
	absIndices := make([]int, e.nSubframes)
	prevIdx := e.prevGainIdx
	targetIdx := gainTargetAt(targetIndices, 0)
	if conditional {
		targetIdx = quantizedGainDelta(prevIdx, targetIdx)
	} else {
		if targetIdx < prevIdx-16 {
			targetIdx = prevIdx - 16
		}
		if targetIdx >= NLevelsQGain {
			targetIdx = NLevelsQGain - 1
		}
		if targetIdx < 0 {
			targetIdx = 0
		}
	}
	absIndices[0] = targetIdx

	for sf := 1; sf < e.nSubframes; sf++ {
		targetIdx = quantizedGainDelta(targetIdx, gainTargetAt(targetIndices, sf))
		absIndices[sf] = targetIdx
	}
	return absIndices
}

func gainTargetAt(targetIndices []int, sf int) int {
	if len(targetIndices) == 0 {
		return 10
	}
	if sf < 0 {
		sf = 0
	}
	if sf >= len(targetIndices) {
		sf = len(targetIndices) - 1
	}
	return clampInt(targetIndices[sf], 0, NLevelsQGain-1)
}

// encodeGainDelta codes the clamped gain delta and returns the resulting
// absolute index and the coded symbol.
func (e *Encoder) encodeGainDelta(enc *entcode.Encoder, prevIdx, targetIdx int) (int, int) {
	delta := targetIdx - prevIdx
	if delta < MinDeltaGainQuant {
		delta = MinDeltaGainQuant
	}
	if delta > MaxDeltaGainQuant {
		delta = MaxDeltaGainQuant
	}
	enc.EncodeIcdf(delta-MinDeltaGainQuant, silkDeltaGainICDF[:], 8)
	return applyQuantizedGainDelta(prevIdx, delta), delta - MinDeltaGainQuant
}

func quantizedGainDelta(prevIdx, targetIdx int) int {
	delta := targetIdx - prevIdx
	if delta < MinDeltaGainQuant {
		delta = MinDeltaGainQuant
	}
	if delta > MaxDeltaGainQuant {
		delta = MaxDeltaGainQuant
	}
	return applyQuantizedGainDelta(prevIdx, delta)
}

func applyQuantizedGainDelta(prevIdx, delta int) int {
	dblStepThresh := 2*MaxDeltaGainQuant - NLevelsQGain + prevIdx
	if delta > dblStepThresh {
		prevIdx += 2*delta - dblStepThresh
	} else {
		prevIdx += delta
	}
	if prevIdx < 0 {
		prevIdx = 0
	}
	if prevIdx >= NLevelsQGain {
		prevIdx = NLevelsQGain - 1
	}
	return prevIdx
}

func (e *Encoder) defaultNLSFIndex(signalType int, cb *nlsfCBParams) int {
	if cb.order == 16 {
		return 9
	}
	if signalType == SignalTypeInactive {
		return 0
	}
	return 10
}

func (e *Encoder) analyzeNLSF(signal []float64, cb *nlsfCBParams, signalType int, preConfig ...lpcInPreConfig) nlsfAnalysis {
	if signalType == SignalTypeInactive && len(preConfig) == 0 {
		cb1Idx := e.defaultNLSFIndex(signalType, cb)
		rawIdx := make([]int, cb.order)
		nlsfQ15 := reconstructNLSFQ15(cb, cb1Idx, rawIdx)
		lpcQ12 := nlsfToLPCLibopus(nlsfQ15, cb.order)
		return nlsfAnalysis{
			cb1Idx:       cb1Idx,
			rawIdx:       rawIdx,
			nlsfQ15:      nlsfQ15,
			lpcQ12:       lpcQ12,
			lpcQ12Interp: nil,
			interpFactor: 4,
		}
	}

	x := signal
	subfrLength := len(signal)
	nbSubfr := 1
	minInvGain := lpcMinInvGain(0, 1, e.firstFrameAfterReset)
	useInterpolated := false

	if len(preConfig) > 0 {
		cfg := preConfig[0]
		input := signal
		if len(cfg.input) > 0 {
			input = cfg.input
		}
		lpcInPre := buildLPCInPre(input, cfg.subframeLengths, cfg.invGains, cfg.ltpCoefs, cfg.pitchLags, cb.order, cfg.voiced)
		if len(cfg.subframeLengths) > 0 && len(lpcInPre) > 0 {
			sfLen := cfg.subframeLengths[0] + cb.order
			uniform := sfLen > cb.order
			for _, n := range cfg.subframeLengths {
				if n+cb.order != sfLen {
					uniform = false
					break
				}
			}
			if uniform && len(lpcInPre) >= sfLen*len(cfg.subframeLengths) {
				// silk_find_LPC_FLP runs on LPC_in_pre in int16 scale; Burg's
				// absolute 1e-9f regulariser makes the analysis scale-dependent,
				// so present the same values libopus sees.
				x = make([]float64, len(lpcInPre))
				for i, v := range lpcInPre {
					x[i] = v * 32768
				}
				subfrLength = sfLen
				nbSubfr = len(cfg.subframeLengths)
				minInvGain = lpcMinInvGain(cfg.ltpPredCodGain, cfg.codingQuality, e.firstFrameAfterReset)
				useInterpolated = e.nSubframes == 4 && e.silkComplexityConfig().useInterpolatedNLSFs
			}
		}
	}

	targetNLSFQ15, interpFactor := silkFindLPCFLP(x, minInvGain, subfrLength, nbSubfr, cb.order, useInterpolated, e.firstFrameAfterReset, e.prevNLSFQ15)
	e.traceNLSFTargetQ15 = append(e.traceNLSFTargetQ15[:0], targetNLSFQ15...)
	e.traceMinInvGain = minInvGain
	cb1Idx, rawIdx, nlsfQ15, predCoefQ12 := e.silkProcessNLSFs(cb, targetNLSFQ15, e.prevNLSFQ15, interpFactor, signalType)

	var lpcQ12Interp []int16
	if interpFactor < 4 {
		lpcQ12Interp = predCoefQ12[0]
	}

	return nlsfAnalysis{
		cb1Idx:       cb1Idx,
		rawIdx:       rawIdx,
		nlsfQ15:      nlsfQ15,
		lpcQ12:       predCoefQ12[1],
		lpcQ12Interp: lpcQ12Interp,
		interpFactor: interpFactor,
	}
}

func interpolatedLPCForTransmittedNLSF(prevQ15, transmittedQ15 []int16, interpFactor int, cb *nlsfCBParams) []int16 {
	if interpFactor >= 4 || len(prevQ15) != cb.order || len(transmittedQ15) != cb.order {
		return nil
	}
	interpNLSF := interpolateNLSFQ15(prevQ15, transmittedQ15, interpFactor, cb)
	return nlsfToLPCLibopus(interpNLSF, cb.order)
}

// interpolateNLSFQ15 reproduces the decoder's quantized-NLSF interpolation
// (decoder.go: prev + ((factor*(curr-prev))>>2)) followed by stabilization, so
// the encoder's subframe-0/1 LPC matches what the decoder reconstructs for the
// chosen interpolation factor.
func interpolateNLSFQ15(prevQ15, currQ15 []int16, factor int, cb *nlsfCBParams) []int16 {
	out := make([]int16, cb.order)
	for i := 0; i < cb.order; i++ {
		prev := int32(prevQ15[i])
		curr := int32(currQ15[i])
		out[i] = int16(prev + ((int32(factor) * (curr - prev)) >> 2))
	}
	silkNLSFStabilize(out, cb.deltaMinQ15, cb.order)
	return out
}

func (e *Encoder) lpcNLSFTargetQ15(signal []float64, cb *nlsfCBParams, domain ...*lpcBurgDomain) ([]int16, bool) {
	burgSignal := signal
	subfrLength := len(signal)
	nbSubfr := 1
	minInvGain := lpcMinInvGain(0, 1, e.firstFrameAfterReset)
	if len(domain) > 0 && domain[0] != nil {
		burgSignal = domain[0].signal
		subfrLength = domain[0].subfrLength
		nbSubfr = domain[0].nbSubfr
		minInvGain = domain[0].minInvGain
	}
	if len(burgSignal) <= cb.order || subfrLength <= cb.order || nbSubfr <= 0 || len(burgSignal) < subfrLength*nbSubfr {
		return nil, false
	}
	// Burg-method LPC over the whole frame (single subframe), then accurate
	// A2NLSF root finding — the libopus silk_find_LPC_FLP path. minInvGain
	// bounds the prediction gain with the libopus LTP/coding-quality formula.
	a, _ := silkBurgModifiedFLP(burgSignal, minInvGain, subfrLength, nbSubfr, cb.order)
	target := silkA2NLSFFLP(a, cb.order)
	if len(target) != cb.order {
		return nil, false
	}
	silkNLSFStabilize(target, cb.deltaMinQ15, cb.order)
	return target, true
}

func bestNLSFStage1(signal []float64, cb *nlsfCBParams) int {
	bestIdx := 0
	bestCost := math.Inf(1)
	rawIdx := make([]int, cb.order)
	for idx := 0; idx < cb.nEntries; idx++ {
		nlsfQ15 := reconstructNLSFQ15(cb, idx, rawIdx)
		lpcQ12 := nlsfToLPCLibopus(nlsfQ15, cb.order)
		cost := lpcResidualEnergy(signal, lpcQ12)
		if cost < bestCost {
			bestCost = cost
			bestIdx = idx
		}
	}
	return bestIdx
}

func refineNLSFResidual(signal []float64, cb *nlsfCBParams, cb1Idx int) []int {
	return refineNLSFResidualFrom(signal, cb, cb1Idx, nil)
}

func refineNLSFResidualFrom(signal []float64, cb *nlsfCBParams, cb1Idx int, seed []int) []int {
	rawIdx := make([]int, cb.order)
	copy(rawIdx, seed)
	for i := range rawIdx {
		rawIdx[i] = clampInt(rawIdx[i], -3, 3)
	}
	var trialBuf [silkMaxLPCOrder]int
	var nlsfBuf, lpcBuf [silkMaxLPCOrder]int16
	trial := trialBuf[:cb.order]
	bestCost := nlsfResidualCostWithScratch(signal, cb, cb1Idx, rawIdx, nlsfBuf[:], lpcBuf[:])

	for pass := 0; pass < 3; pass++ {
		improved := false
		for i := 0; i < cb.order; i++ {
			bestVal := rawIdx[i]
			for _, candidate := range []int{-3, -2, -1, 0, 1, 2, 3} {
				if candidate == rawIdx[i] {
					continue
				}
				copy(trial, rawIdx)
				trial[i] = candidate
				cost := nlsfResidualCostWithScratch(signal, cb, cb1Idx, trial, nlsfBuf[:], lpcBuf[:])
				if cost < bestCost {
					bestCost = cost
					bestVal = candidate
				}
			}
			if bestVal != rawIdx[i] {
				rawIdx[i] = bestVal
				improved = true
			}
		}
		if !improved {
			break
		}
	}
	return rawIdx
}

func nlsfResidualCostWithScratch(signal []float64, cb *nlsfCBParams, cb1Idx int, rawIdx []int, nlsfBuf, lpcBuf []int16) float64 {
	nlsfQ15 := reconstructNLSFQ15Into(nlsfBuf, cb, cb1Idx, rawIdx)
	lpcQ12 := nlsfToLPCLibopusInto(lpcBuf, nlsfQ15, cb.order)
	return lpcResidualEnergy(signal, lpcQ12)
}

func topNLSFCB1ByTarget(cb *nlsfCBParams, targetQ15 []int16, n int) []int {
	if n > cb.nEntries {
		n = cb.nEntries
	}
	bestIdx := make([]int, 0, n)
	bestCost := make([]float64, 0, n)
	rawZero := make([]int, cb.order)
	for idx := 0; idx < cb.nEntries; idx++ {
		nlsfQ15 := reconstructNLSFQ15(cb, idx, rawZero)
		cost := nlsfTargetDistortion(cb, idx, nlsfQ15, targetQ15)
		insert := len(bestCost)
		for insert > 0 && cost < bestCost[insert-1] {
			insert--
		}
		if insert >= n {
			continue
		}
		bestIdx = append(bestIdx, 0)
		bestCost = append(bestCost, 0)
		copy(bestIdx[insert+1:], bestIdx[insert:])
		copy(bestCost[insert+1:], bestCost[insert:])
		bestIdx[insert] = idx
		bestCost[insert] = cost
		if len(bestIdx) > n {
			bestIdx = bestIdx[:n]
			bestCost = bestCost[:n]
		}
	}
	return bestIdx
}

func rawNLSFResidualForTarget(cb *nlsfCBParams, cb1Idx int, targetQ15 []int16) []int {
	rawIdx := make([]int, cb.order)
	predQ8 := nlsfPredQ8ForCB1(cb, cb1Idx)
	desiredResQ10 := make([]int32, cb.order)
	for i := 0; i < cb.order; i++ {
		cb1Val := int32(cb.cb1Q8[cb1Idx*cb.order+i]) << 7
		wghtQ9 := int32(cb.cb1WghtQ9[cb1Idx*cb.order+i])
		desiredResQ10[i] = int32(int64(int32(targetQ15[i])-cb1Val) * int64(wghtQ9) >> 14)
	}

	const nlsfQuantLevelAdjQ10 = int32(102)
	nextOutQ10 := int32(0)
	for i := cb.order - 1; i >= 0; i-- {
		predQ10 := (nextOutQ10 * int32(predQ8[i])) >> 8
		bestRaw := 0
		bestErr := int64(1<<63 - 1)
		bestOut := int32(0)
		for raw := -3; raw <= 3; raw++ {
			out := int32(raw) << 10
			if out > 0 {
				out -= nlsfQuantLevelAdjQ10
			} else if out < 0 {
				out += nlsfQuantLevelAdjQ10
			}
			out = predQ10 + int32((int64(out)*int64(cb.quantStepSizeQ16))>>16)
			err := int64(out - desiredResQ10[i])
			if err < 0 {
				err = -err
			}
			if err < bestErr {
				bestErr = err
				bestRaw = raw
				bestOut = out
			}
		}
		rawIdx[i] = bestRaw
		nextOutQ10 = bestOut
	}
	return rawIdx
}

func nlsfPredQ8ForCB1(cb *nlsfCBParams, cb1Idx int) []uint8 {
	predQ8 := make([]uint8, cb.order)
	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		predQ8[i] = cb.predQ8[i+int((entry&1))*int(cb.order-1)]
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*int(cb.order-1)+1]
	}
	return predQ8
}

func nlsfTargetDistortion(cb *nlsfCBParams, cb1Idx int, nlsfQ15, targetQ15 []int16) float64 {
	if len(targetQ15) != cb.order || len(nlsfQ15) != cb.order {
		return math.Inf(1)
	}
	cost := 0.0
	for i := 0; i < cb.order; i++ {
		diff := float64(int(nlsfQ15[i]) - int(targetQ15[i]))
		w := float64(cb.cb1WghtQ9[cb1Idx*cb.order+i]) / 512.0
		cost += w * diff * diff
	}
	return cost
}

func reconstructNLSFQ15(cb *nlsfCBParams, cb1Idx int, rawIdx []int) []int16 {
	nlsfQ15 := make([]int16, cb.order)
	return reconstructNLSFQ15Into(nlsfQ15, cb, cb1Idx, rawIdx)
}

func reconstructNLSFQ15Into(nlsfQ15 []int16, cb *nlsfCBParams, cb1Idx int, rawIdx []int) []int16 {
	nlsfQ15 = nlsfQ15[:cb.order]
	if cb1Idx < 0 {
		cb1Idx = 0
	}
	if cb1Idx >= cb.nEntries {
		cb1Idx = cb.nEntries - 1
	}

	const nlsfQuantLevelAdjQ10 = int32(102)
	var predQ8 [silkMaxLPCOrder]uint8
	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		predQ8[i] = cb.predQ8[i+int((entry&1))*int(cb.order-1)]
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*int(cb.order-1)+1]
	}

	var resQ10 [silkMaxLPCOrder]int32
	outQ10 := int32(0)
	for i := cb.order - 1; i >= 0; i-- {
		idx := 0
		if i < len(rawIdx) {
			idx = clampInt(rawIdx[i], -nlsfQuantMaxAmplitudeExt, nlsfQuantMaxAmplitudeExt)
		}
		predQ10 := (outQ10 * int32(predQ8[i])) >> 8
		outQ10 = int32(idx) << 10
		if outQ10 > 0 {
			outQ10 -= nlsfQuantLevelAdjQ10
		} else if outQ10 < 0 {
			outQ10 += nlsfQuantLevelAdjQ10
		}
		outQ10 = predQ10 + int32((int64(outQ10)*int64(cb.quantStepSizeQ16))>>16)
		resQ10[i] = outQ10
	}

	for i := 0; i < cb.order; i++ {
		cb1Val := int32(cb.cb1Q8[cb1Idx*cb.order+i])
		wghtQ9 := int32(cb.cb1WghtQ9[cb1Idx*cb.order+i])
		div := int32(0)
		if wghtQ9 != 0 {
			div = (resQ10[i] << 14) / wghtQ9
		}
		tmp := div + (cb1Val << 7)
		if tmp < 0 {
			tmp = 0
		}
		if tmp > 32767 {
			tmp = 32767
		}
		nlsfQ15[i] = int16(tmp)
	}
	silkNLSFStabilize(nlsfQ15, cb.deltaMinQ15, cb.order)
	return nlsfQ15
}

func lpcResidualEnergy(signal []float64, lpcQ12 []int16) float64 {
	if len(signal) == 0 {
		return 0
	}
	energy := 0.0
	for i := range signal {
		pred := 0.0
		for j := 0; j < len(lpcQ12) && j < i; j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * signal[i-j-1]
		}
		err := signal[i] - pred
		energy += err * err
	}
	return energy / float64(len(signal))
}

func nlsfQ15ToRadians(nlsfQ15 []int16) []float64 {
	out := make([]float64, len(nlsfQ15))
	for i, v := range nlsfQ15 {
		out[i] = float64(v) / 32768.0 * math.Pi
	}
	return out
}

func (e *Encoder) encodeNLSF(enc *entcode.Encoder, cb *nlsfCBParams, signalType int, analysis nlsfAnalysis) {
	cb1Idx := clampInt(analysis.cb1Idx, 0, cb.nEntries-1)
	offset := (signalType >> 1) * cb.nEntries
	enc.EncodeIcdf(cb1Idx, cb.cb1ICDF[offset:offset+cb.nEntries], 8)

	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		ecIx0 := ((int(entry) >> 1) & 7) * 9
		ecIx1 := ((int(entry) >> 5) & 7) * 9
		encodeNLSFResidualIndex(enc, nlsfRawIndex(analysis.rawIdx, i), cb.cb2ICDF[ecIx0:ecIx0+9])
		encodeNLSFResidualIndex(enc, nlsfRawIndex(analysis.rawIdx, i+1), cb.cb2ICDF[ecIx1:ecIx1+9])
	}
}

func nlsfRawIndex(rawIdx []int, i int) int {
	if i >= len(rawIdx) {
		return 0
	}
	return clampInt(rawIdx[i], -nlsfQuantMaxAmplitudeExt, nlsfQuantMaxAmplitudeExt)
}

func encodeNLSFResidualIndex(enc *entcode.Encoder, idx int, icdf []uint8) {
	switch {
	case idx >= nlsfQuantMaxAmplitude:
		enc.EncodeIcdf(2*nlsfQuantMaxAmplitude, icdf, 8)
		enc.EncodeIcdf(clampInt(idx-nlsfQuantMaxAmplitude, 0, len(silkNLSFExtICDF)-1), silkNLSFExtICDF[:], 8)
	case idx <= -nlsfQuantMaxAmplitude:
		enc.EncodeIcdf(0, icdf, 8)
		enc.EncodeIcdf(clampInt(-idx-nlsfQuantMaxAmplitude, 0, len(silkNLSFExtICDF)-1), silkNLSFExtICDF[:], 8)
	default:
		enc.EncodeIcdf(idx+nlsfQuantMaxAmplitude, icdf, 8)
	}
}

type pulseBlock struct {
	pulses   [shellCodecFrameLength]int16
	shellAbs [shellCodecFrameLength]int
	sum      int
	nLShifts int
}

func (e *Encoder) encodePulses(enc *entcode.Encoder, pulses []int16, signalType, quantOffset int) {
	blocks := makePulseBlocks(pulses, e.frameSize)

	row := signalType >> 1
	if row > 1 {
		row = 1
	}
	rateLevelIdx := selectPulseRateLevel(row, blocks)
	enc.EncodeIcdf(rateLevelIdx, silkRateLevelsICDF[row][:], 8)

	for i := range blocks {
		encodePulseBlockSum(enc, rateLevelIdx, blocks[i])
	}
	for i := range blocks {
		if blocks[i].sum > 0 {
			encodeShellBlock(enc, blocks[i].shellAbs)
		}
	}
	for i := range blocks {
		encodePulseLSBs(enc, blocks[i])
	}
	encodePulseSigns(enc, blocks, signalType, quantOffset)
}

func (e *Encoder) simpleNSQ(excitation []float64, gainIndices []int, signalType, quantOffset int, seed int32) []int16 {
	pulses := make([]int16, e.frameSize)
	if signalType == SignalTypeInactive || len(excitation) == 0 {
		return pulses
	}

	uvIdx := 0
	if signalType == SignalTypeVoiced {
		uvIdx = 1
	}
	offsetQ14 := int32(silkQuantizationOffsetsQ10[uvIdx][quantOffset]) << 4
	subframeLen := e.frameSize / e.nSubframes
	shape := 0.35
	if signalType == SignalTypeVoiced {
		shape = 0.45
	}

	err := 0.0
	for i := 0; i < e.frameSize && i < len(excitation); i++ {
		sf := i / subframeLen
		if sf >= len(gainIndices) {
			sf = len(gainIndices) - 1
		}
		gainQ10 := silkGainDequantQ16(gainIndices[sf]) >> 6
		if gainQ10 <= 0 {
			continue
		}

		target := excitation[i] + shape*err
		if target > 2.0 {
			target = 2.0
		} else if target < -2.0 {
			target = -2.0
		}

		desiredQ14 := int32(math.Round(target * (float64(int64(1)<<39) / float64(gainQ10))))
		seed = 196314165*seed + 907633515
		pulse := chooseNSQPulse(desiredQ14, offsetQ14, seed < 0)
		pulses[i] = pulse

		reconQ14 := decodedExcitationQ14(int(pulse), offsetQ14, seed < 0)
		recon := float64(reconQ14) * float64(gainQ10) / float64(int64(1)<<39)
		err = target - recon
		seed += int32(pulse)
	}
	return pulses
}

func (e *Encoder) closedLoopNSQ(
	signal []float64,
	lpcQ12 []int16,
	gainIndices []int,
	signalType, quantOffset int,
	seed int32,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
) []int16 {
	return e.closedLoopNSQWithRateScale(signal, lpcQ12, nil, gainIndices,
		signalType, quantOffset, seed, pitchLags, ltpCoeffsQ14, ltpScaleQ14, 1)
}

// voicedUsesTrellis reports whether voiced frames take the Step 4 trellis NSQ.
func (e *Encoder) voicedUsesTrellis() bool {
	return e.useTrellisNSQ
}

// unvoicedUsesTrellis reports whether unvoiced frames take the full
// delayed-decision trellis NSQ instead of the single-state homebrew quantizer.
// Like voiced, unvoiced is paired with the co-designed noise-shape envelope
// gains (shapeGainIndices); the trellis perceptual shaping only beats homebrew's
// broadband SNR when the gains are co-designed with it (Step 3/4 lesson).
//
// Phase 6 widens this beyond mono SILK-only. The trellis path already neutralises
// stereo-component and unvoiced spectral shaping before NSQ, preserving the
// broadband-SNR objective while retaining delayed-decision rate/distortion
// search. Hybrid still supplies its own CELT high band; this gate only decides
// the SILK low-band excitation quantizer.
func (e *Encoder) unvoicedUsesTrellis() bool {
	return e.useTrellisNSQ
}

// TrellisNSQ reports whether voiced SILK-only frames may use the trellis NSQ.
func (e *Encoder) TrellisNSQ() bool {
	return e.useTrellisNSQ
}

// LastFinalRange returns the pre-flush entropy range of the last standalone stream.
func (e *Encoder) LastFinalRange() uint32 {
	return e.lastFinalRange
}

// SetTrellisNSQ enables or disables the voiced SILK-only trellis NSQ.
func (e *Encoder) SetTrellisNSQ(enabled bool) {
	e.useTrellisNSQ = enabled
}

// LastStreamSNRVBR reports whether the most recently encoded SILK stream used
// the voiced SNR-target natural-size path.
func (e *Encoder) LastStreamSNRVBR() bool {
	return e.lastSNRVBRStream
}

// SetHybridMode marks subsequent frames as the SILK low band of a hybrid packet
// (see hybridMode). The hybrid encoder sets it before encoding and clears it
// after so the same SILK encoder instance can also serve SILK-only packets.
func (e *Encoder) SetHybridMode(on bool) {
	e.hybridMode = on
	if e.side != nil {
		e.side.SetHybridMode(on)
	}
}

// closedLoopNSQWithRateScale quantizes the excitation with the FLP
// noise-shaping analysis (Q3) feeding the delayed-decision trellis NSQ (Q4),
// the libopus core ported in noise_shape.go / nsq_del_dec.go. The legacy
// single-state quantizer is retained as closedLoopNSQHomebrew for reference and
// A/B debugging. The rate-control search lever (rateScale) is mapped onto the
// trellis rate weight Lambda: a larger rateScale raises Lambda, suppressing
// pulses, mirroring the homebrew pulseRatePenalty semantics the search expects.
func (e *Encoder) closedLoopNSQWithRateScale(
	signal []float64,
	lpcQ12 []int16,
	lpcQ12Interp []int16,
	gainIndices []int,
	signalType, quantOffset int,
	seed int32,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
	rateScale float64,
) []int16 {
	// The delayed-decision trellis with the co-designed process_gains gains is a
	// clear win when its perceptual shaping is paired with the co-designed
	// noise-shape envelope gains (Step 4). Both voiced and unvoiced take it with
	// those gains; inactive frames produce no excitation (the near-silent path)
	// and stay on the homebrew zero-pulse branch. Dispatch by type.
	e.pendingTrace = FrameTrace{SignalType: signalType, QuantOffset: quantOffset}
	if signalType != SignalTypeVoiced {
		// wrappers_FLP.c hands the NSQ LTP_scale_Q14 = 0 for non-voiced frames.
		ltpScaleQ14 = 0
	}
	useTrellis := false
	switch signalType {
	case SignalTypeVoiced:
		useTrellis = e.voicedUsesTrellis()
	case SignalTypeUnvoiced, SignalTypeInactive:
		// libopus quantizes TYPE_NO_VOICE_ACTIVITY frames like unvoiced ones.
		useTrellis = e.unvoicedUsesTrellis()
	}
	if !useTrellis {
		return e.closedLoopNSQHomebrew(signal, lpcQ12, lpcQ12Interp, gainIndices,
			signalType, quantOffset, seed, pitchLags, ltpCoeffsQ14, ltpScaleQ14, rateScale)
	}
	if len(signal) == 0 {
		e.updateSilentSynthesisState()
		e.updateSilentNSQState()
		return make([]int16, e.frameSize)
	}
	if rateScale < 1 {
		rateScale = 1
	}

	x16 := make([]int16, e.frameSize)
	for i := 0; i < e.frameSize && i < len(signal); i++ {
		x16[i] = clamp16(int32(math.Round(signal[i] * 32768.0)))
	}

	var gainsQ16 [silkMaxNBSubframes]int32
	var pitchL [silkMaxNBSubframes]int
	for sf := 0; sf < e.nSubframes; sf++ {
		gIdx := gainIndices[clampInt(sf, 0, len(gainIndices)-1)]
		gq := silkGainDequantQ16(gIdx)
		if gq < 1 {
			gq = 1
		}
		gainsQ16[sf] = gq
		// libopus hands the NSQ pitchL = 0 for non-voiced frames (only
		// NSQ->lagPrev observes it); the lag itself is only read for voiced.
		lag := 0
		if signalType == SignalTypeVoiced {
			lag = e.prevPitchLag
			if sf < len(pitchLags) && pitchLags[sf] > 0 {
				lag = pitchLags[sf]
			}
			if lag < 1 {
				lag = 1
			}
		}
		pitchL[sf] = lag
	}

	pitchGain := estimatePitchGainFromLTP(ltpCoeffsQ14)
	shape := e.analyzeNoiseShapeFLP(signal, lpcQ12, signalType, quantOffset, pitchLags, pitchGain, e.speechActivity)
	if e.haveShape32 {
		// wrappers_FLP.c conversions of the float32 noise_shape_analysis_FLP
		// outputs: AR_Q13, LF_shp_Q14, Tilt_Q14, HarmShapeGain_Q14, Lambda_Q10.
		e.applyShape32(&shape, signalType, quantOffset)
	}

	lambdaQ10 := shape.Lambda_Q10
	if rateScale > 1 {
		lambdaQ10 = int32(float64(shape.Lambda_Q10) * (1.0 + 0.5*math.Log2(rateScale)))
	}
	if lambdaQ10 < 64 {
		lambdaQ10 = 64
	}

	// silk_NSQ_wrapper_FLP: the delayed-decision quantizer runs with more than
	// one state or with warping; otherwise the plain silk_NSQ.
	var pulses []int16
	if cfg := e.silkComplexityConfig(); cfg.nStatesDelayedDecision <= 1 && shape.Warping_Q16 == 0 {
		pulses = e.silkNSQPlain(x16, lpcQ12, lpcQ12Interp, ltpCoeffsQ14, shape, gainsQ16, pitchL,
			lambdaQ10, ltpScaleQ14, signalType, quantOffset, seed)
	} else {
		pulses = e.silkNSQDelDec(x16, lpcQ12, lpcQ12Interp, ltpCoeffsQ14, shape, gainsQ16, pitchL,
			lambdaQ10, ltpScaleQ14, signalType, quantOffset, seed)
	}
	e.recordNSQTrace(lpcQ12, lpcQ12Interp, e.traceNLSFQ15, gainIndices, gainsQ16, pitchL, shape, lambdaQ10,
		ltpCoeffsQ14, ltpScaleQ14, signalType, quantOffset, seed, pulses)
	return pulses
}

func (e *Encoder) updateSilentNSQState() {
	ltpMemLen := silkLTPMemLengthMs * (e.sampleRate / 1000)
	if len(e.nsq.xq) == ltpMemLen+e.frameSize {
		copy(e.nsq.xq, e.nsq.xq[e.frameSize:])
		for i := ltpMemLen; i < len(e.nsq.xq); i++ {
			e.nsq.xq[i] = 0
		}
		copy(e.nsq.sLTPShpQ14, e.nsq.sLTPShpQ14[e.frameSize:])
		for i := ltpMemLen; i < len(e.nsq.sLTPShpQ14); i++ {
			e.nsq.sLTPShpQ14[i] = 0
		}
	}
	for i := range e.nsq.sLPCQ14 {
		e.nsq.sLPCQ14[i] = 0
	}
	for i := range e.nsq.sAR2Q14 {
		e.nsq.sAR2Q14[i] = 0
	}
	e.nsq.sLFARShpQ14 = 0
	e.nsq.sDiffShpQ14 = 0
}

func (e *Encoder) closedLoopNSQHomebrew(
	signal []float64,
	lpcQ12 []int16,
	lpcQ12Interp []int16,
	gainIndices []int,
	signalType, quantOffset int,
	seed int32,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
	rateScale float64,
) []int16 {
	pulses := make([]int16, e.frameSize)
	if signalType == SignalTypeInactive || len(signal) == 0 {
		e.updateSilentSynthesisState()
		e.syncTrellisNSQState()
		return pulses
	}
	if rateScale < 1 {
		rateScale = 1
	}
	// NLSF interpolation: subframes 0,1 use the interpolated LPC set. This path
	// handles unvoiced frames (no mid-frame re-whitening — the voiced rewhitening
	// block below is gated on TYPE_VOICED), and voiced frames only reach here when
	// the trellis is disabled, in which case selectNLSFInterpolation already
	// forced interpFactor==4 (lpcQ12Interp==nil) so there is no LPC-set switch.
	interpActive := len(lpcQ12Interp) >= e.lpcOrder
	lpcForSubframe := func(sf int) []int16 {
		if interpActive && sf < 2 {
			return lpcQ12Interp
		}
		return lpcQ12
	}

	uvIdx := 0
	if signalType == SignalTypeVoiced {
		uvIdx = 1
	}
	offsetQ14 := int32(silkQuantizationOffsetsQ10[uvIdx][quantOffset]) << 4
	subframeLen := e.frameSize / e.nSubframes

	ltpMemLen := len(e.ltpState)
	sLTPQ15 := make([]int32, ltpMemLen+e.frameSize)
	outBufQ0 := make([]int32, ltpMemLen+e.frameSize)
	copy(outBufQ0, e.ltpState)
	sLTPBufIdx := ltpMemLen

	sLPCQ14 := make([]int32, silkMaxLPCOrder+subframeLen)
	copy(sLPCQ14[:silkMaxLPCOrder], e.lpcState)
	output := make([]int16, e.frameSize)
	shaping := e.analyzeNoiseShape(signal, signalType, estimatePitchGainFromLTP(ltpCoeffsQ14))
	errHist := make([]float64, ltpMemLen+e.frameSize)
	shapeErr := 0.0
	prevShapeErr := 0.0
	lfShapeErr := 0.0
	pulseRatePenalty := 96.0 * float64(int64(1)<<20)
	if signalType == SignalTypeVoiced {
		pulseRatePenalty = 28.0 * float64(int64(1)<<20)
	}
	pulseRatePenalty *= rateScale

	for sf := 0; sf < e.nSubframes; sf++ {
		start := sf * subframeLen
		aQ12 := lpcForSubframe(sf)
		gainIdx := gainIndices[clampInt(sf, 0, len(gainIndices)-1)]
		gainQ16 := silkGainDequantQ16(gainIdx)
		gainQ10 := gainQ16 >> 6
		if gainQ10 <= 0 {
			gainQ10 = 1
		}
		shape := shapeForSubframe(shaping, sf, signalType)
		invGainQ31 := silkInverse32VarQ(gainQ16, 47)
		gainAdjQ16 := int32(1 << 16)
		if gainQ16 != e.prevGainQ16 {
			gainAdjQ16 = silkDIV32VarQ(e.prevGainQ16, gainQ16, 16)
			for i := 0; i < silkMaxLPCOrder; i++ {
				sLPCQ14[i] = silkSMULWW(gainAdjQ16, sLPCQ14[i])
			}
		}
		e.prevGainQ16 = gainQ16

		lag := e.prevPitchLag
		if sf < len(pitchLags) && pitchLags[sf] > 0 {
			lag = pitchLags[sf]
		}
		if signalType == SignalTypeVoiced {
			if sf == 0 {
				startIdx := sLTPBufIdx - lag - e.lpcOrder - 2
				if startIdx < 0 {
					startIdx = 0
				}
				filterLen := sLTPBufIdx - startIdx
				if filterLen > 0 {
					sLTP := make([]int16, filterLen)
					silkLPCAnalysisFilter(sLTP, outBufQ0[startIdx:startIdx+filterLen], aQ12, filterLen, e.lpcOrder)
					invGainQ31 = silkSMULWB(invGainQ31, ltpScaleQ14) << 2
					for i := 0; i < lag+2 && i < filterLen; i++ {
						sLTPQ15[sLTPBufIdx-i-1] = silkSMULWB(invGainQ31, sLTP[filterLen-i-1])
					}
				}
			} else if gainAdjQ16 != 1<<16 {
				for i := 0; i < lag+2 && sLTPBufIdx-i-1 >= 0; i++ {
					idx := sLTPBufIdx - i - 1
					sLTPQ15[idx] = silkSMULWW(gainAdjQ16, sLTPQ15[idx])
				}
			}
		}

		for i := 0; i < subframeLen && start+i < e.frameSize && start+i < len(signal); i++ {
			lpcPredQ10 := int32(e.lpcOrder >> 1)
			for j := 0; j < e.lpcOrder; j++ {
				lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPCQ14[silkMaxLPCOrder+i-j-1], aQ12[j])
			}
			predQ14 := silkLShiftSat32(lpcPredQ10, 4)

			ltpPredQ14 := int32(0)
			if signalType == SignalTypeVoiced {
				ltpPredQ13 := int32(2)
				var coeffs [5]int16
				if sf < len(ltpCoeffsQ14) {
					coeffs = ltpCoeffsQ14[sf]
				}
				for k := 0; k < 5; k++ {
					ltpIdx := sLTPBufIdx - lag + 2 - k
					if ltpIdx >= 0 && ltpIdx < len(sLTPQ15) {
						ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[ltpIdx], coeffs[k])
					}
				}
				ltpPredQ14 = ltpPredQ13 << 1
			}

			harmonicErr := 0.0
			if signalType == SignalTypeVoiced && lag > 0 {
				errIdx := ltpMemLen + start + i - lag
				if errIdx >= 0 && errIdx < len(errHist) {
					harmonicErr = errHist[errIdx]
				}
			}
			hfErr := shapeErr - prevShapeErr
			target := signal[start+i] +
				shape.feedback*shapeErr +
				shape.tilt*prevShapeErr +
				shape.lf*lfShapeErr +
				shape.hf*hfErr +
				shape.harmonic*harmonicErr
			if target > 1.5 {
				target = 1.5
			} else if target < -1.5 {
				target = -1.5
			}
			desiredQ14 := int32(math.Round(target * (float64(int64(1)<<39) / float64(gainQ10))))
			desiredExcQ14 := desiredQ14 - predQ14 - ltpPredQ14

			seed = 196314165*seed + 907633515
			pulse := chooseNSQPulseShaped(desiredExcQ14, offsetQ14, seed < 0, pulseRatePenalty*shape.lambda)
			pulses[start+i] = pulse

			excQ14 := decodedExcitationQ14(int(pulse), offsetQ14, seed < 0)
			presQ14 := excQ14
			if signalType == SignalTypeVoiced {
				presQ14 += ltpPredQ14
				sLTPQ15[sLTPBufIdx] = presQ14 << 1
				sLTPBufIdx++
			}
			v := silkAddSat32(presQ14, predQ14)
			sLPCQ14[silkMaxLPCOrder+i] = v
			pxq := silkRShiftRound(int64(silkSMULWW(v, gainQ10)), 8)
			output[start+i] = clamp16(pxq)

			recon := float64(output[start+i]) / 32768.0
			prevShapeErr = shapeErr
			shapeErr = signal[start+i] - recon
			lfShapeErr = 0.94*lfShapeErr + shapeErr
			errHist[ltpMemLen+start+i] = shapeErr
			seed += int32(pulse)
		}

		copy(sLPCQ14[:silkMaxLPCOrder], sLPCQ14[subframeLen:subframeLen+silkMaxLPCOrder])
	}

	copy(e.lpcState, sLPCQ14[:silkMaxLPCOrder])
	if e.frameSize <= ltpMemLen {
		mvLen := ltpMemLen - e.frameSize
		copy(e.ltpState[:mvLen], e.ltpState[e.frameSize:])
		for i := 0; i < e.frameSize; i++ {
			e.ltpState[mvLen+i] = int32(output[i])
		}
	}
	e.syncTrellisNSQState()
	return pulses
}

func estimatePitchGainFromLTP(ltpCoeffsQ14 [][5]int16) float64 {
	best := 0.0
	for _, coeffs := range ltpCoeffsQ14 {
		sum := 0.0
		for _, c := range coeffs {
			if c > 0 {
				sum += float64(c) / 16384.0
			}
		}
		if sum > best {
			best = sum
		}
	}
	return clampFloat(best, 0, 1)
}

func shapeForSubframe(analysis silkShapeAnalysis, sf, signalType int) silkShapeSubframe {
	if sf >= 0 && sf < len(analysis.subframes) {
		shape := analysis.subframes[sf]
		if shape.lambda <= 0 {
			shape.lambda = 1
		}
		return shape
	}
	return defaultShapeSubframe(signalType, 0)
}

func (e *Encoder) updateSilentSynthesisState() {
	for i := range e.lpcState {
		e.lpcState[i] = 0
	}
	if len(e.ltpState) == 0 || e.frameSize > len(e.ltpState) {
		return
	}
	mvLen := len(e.ltpState) - e.frameSize
	copy(e.ltpState[:mvLen], e.ltpState[e.frameSize:])
	for i := mvLen; i < len(e.ltpState); i++ {
		e.ltpState[i] = 0
	}
}

func chooseNSQPulse(desiredQ14, offsetQ14 int32, flipSign bool) int16 {
	rawTarget := desiredQ14
	if flipSign {
		rawTarget = -rawTarget
	}
	base := int(math.Round(float64(rawTarget-offsetQ14) / 16384.0))

	bestPulse := 0
	bestErr := int64(1<<63 - 1)
	for p := base - 3; p <= base+3; p++ {
		candidate := clampInt(p, -1024, 1024)
		exc := decodedExcitationQ14(candidate, offsetQ14, flipSign)
		err := int64(exc) - int64(desiredQ14)
		if err < 0 {
			err = -err
		}
		if err < bestErr {
			bestErr = err
			bestPulse = candidate
		}
	}
	return int16(bestPulse)
}

func chooseNSQPulseShaped(desiredQ14, offsetQ14 int32, flipSign bool, pulseRatePenalty float64) int16 {
	rawTarget := desiredQ14
	if flipSign {
		rawTarget = -rawTarget
	}
	base := int(math.Round(float64(rawTarget-offsetQ14) / 16384.0))

	bestPulse := 0
	bestCost := math.Inf(1)
	candidates := make([]int, 0, 16)
	for p := base - 4; p <= base+4; p++ {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, 0, -1, 1, -2, 2)
	for _, p := range candidates {
		candidate := clampInt(p, -1024, 1024)
		exc := decodedExcitationQ14(candidate, offsetQ14, flipSign)
		err := float64(int64(exc) - int64(desiredQ14))
		absPulse := math.Abs(float64(candidate))
		cost := err*err + absPulse*absPulse*pulseRatePenalty
		if candidate == 0 {
			cost *= 0.98
		}
		if cost < bestCost {
			bestCost = cost
			bestPulse = candidate
		}
	}
	return int16(bestPulse)
}

func decodedExcitationQ14(pulse int, offsetQ14 int32, flipSign bool) int32 {
	exc := int32(pulse) << 14
	if exc > 0 {
		exc -= 80 << 4
	} else if exc < 0 {
		exc += 80 << 4
	}
	exc += offsetQ14
	if flipSign {
		exc = -exc
	}
	return exc
}

func makePulseBlocks(pulses []int16, frameSize int) []pulseBlock {
	iter := frameSize >> log2ShellCodecFrameLen
	if iter*shellCodecFrameLength < frameSize {
		iter++
	}

	blocks := make([]pulseBlock, iter)
	for blockIdx := range blocks {
		blockStart := blockIdx * shellCodecFrameLength
		for i := 0; i < shellCodecFrameLength; i++ {
			pos := blockStart + i
			if pos >= len(pulses) {
				continue
			}
			p := pulses[pos]
			blocks[blockIdx].pulses[i] = p
			if p < 0 {
				blocks[blockIdx].shellAbs[i] = int(-p)
			} else {
				blocks[blockIdx].shellAbs[i] = int(p)
			}
		}

		// silk_encode_pulses: scale the block down until every level of the
		// shell tree fits its table (pairs <= 8, quads <= 10, octets <= 12,
		// total <= 16), not only the total.
		for {
			sum, scaleDown := shellSumsFit(blocks[blockIdx].shellAbs[:])
			if !scaleDown {
				blocks[blockIdx].sum = sum
				break
			}
			blocks[blockIdx].nLShifts++
			for i := range blocks[blockIdx].shellAbs {
				blocks[blockIdx].shellAbs[i] >>= 1
			}
		}
	}
	return blocks
}

// shellSumsFit combines the 16 absolute pulses pairwise like combine_and_check
// with silk_max_pulses_table {8, 10, 12, 16}; it returns the block sum and
// whether any level exceeded its limit (scale_down).
func shellSumsFit(abs []int) (int, bool) {
	limits := [4]int{8, 10, 12, 16}
	cur := append([]int(nil), abs...)
	scaleDown := false
	for level := 0; level < 4; level++ {
		next := make([]int, len(cur)/2)
		for i := range next {
			next[i] = cur[2*i] + cur[2*i+1]
			if next[i] > limits[level] {
				scaleDown = true
			}
		}
		cur = next
	}
	return cur[0], scaleDown
}

// selectPulseRateLevel mirrors silk_encode_pulses' rate-level search: the
// integer Q5 bit costs of silk_rate_levels_BITS_Q5 and
// silk_pulses_per_block_BITS_Q5 (scaled-down blocks cost the escape entry),
// with the first minimum winning ties.
func selectPulseRateLevel(row int, blocks []pulseBlock) int {
	bestLevel := 0
	minSumBitsQ5 := int32(math.MaxInt32)
	for rateLevelIdx := 0; rateLevelIdx < nRateLevels-1; rateLevelIdx++ {
		sumBitsQ5 := silkRateLevelsBitsQ5[row][rateLevelIdx]
		for _, block := range blocks {
			if block.nLShifts > 0 {
				sumBitsQ5 += silkPulsesPerBlockBitsQ5[rateLevelIdx][silkMaxPulses+1]
			} else {
				sumBitsQ5 += silkPulsesPerBlockBitsQ5[rateLevelIdx][block.sum]
			}
		}
		if sumBitsQ5 < minSumBitsQ5 {
			minSumBitsQ5 = sumBitsQ5
			bestLevel = rateLevelIdx
		}
	}
	return bestLevel
}

func encodePulseBlockSum(enc *entcode.Encoder, rateLevelIdx int, block pulseBlock) {
	if block.nLShifts == 0 {
		enc.EncodeIcdf(block.sum, silkPulsesPerBlockICDF[rateLevelIdx][:], 8)
		return
	}

	enc.EncodeIcdf(silkMaxPulses+1, silkPulsesPerBlockICDF[rateLevelIdx][:], 8)
	for shift := 1; shift < block.nLShifts; shift++ {
		enc.EncodeIcdf(silkMaxPulses+1, silkPulsesPerBlockICDF[nRateLevels-1][:], 8)
	}
	offset := 0
	if block.nLShifts == 10 {
		offset = 1
	}
	enc.EncodeIcdf(block.sum, silkPulsesPerBlockICDF[nRateLevels-1][offset:], 8)
}

func encodeShellBlock(enc *entcode.Encoder, absPulses [shellCodecFrameLength]int) {
	splitEncode := func(first, total int, table []uint8) {
		if total <= 0 {
			return
		}
		off := int(silkShellCodeTableOffsets[total])
		enc.EncodeIcdf(first, table[off:off+total+1], 8)
	}

	var p1 [8]int
	var p2 [4]int
	var p3 [2]int
	for i := 0; i < 8; i++ {
		p1[i] = absPulses[2*i] + absPulses[2*i+1]
	}
	for i := 0; i < 4; i++ {
		p2[i] = p1[2*i] + p1[2*i+1]
	}
	for i := 0; i < 2; i++ {
		p3[i] = p2[2*i] + p2[2*i+1]
	}

	splitEncode(p3[0], p3[0]+p3[1], silkShellCodeTable3[:])
	splitEncode(p2[0], p3[0], silkShellCodeTable2[:])
	splitEncode(p1[0], p2[0], silkShellCodeTable1[:])
	splitEncode(absPulses[0], p1[0], silkShellCodeTable0[:])
	splitEncode(absPulses[2], p1[1], silkShellCodeTable0[:])
	splitEncode(p1[2], p2[1], silkShellCodeTable1[:])
	splitEncode(absPulses[4], p1[2], silkShellCodeTable0[:])
	splitEncode(absPulses[6], p1[3], silkShellCodeTable0[:])
	splitEncode(p2[2], p3[1], silkShellCodeTable2[:])
	splitEncode(p1[4], p2[2], silkShellCodeTable1[:])
	splitEncode(absPulses[8], p1[4], silkShellCodeTable0[:])
	splitEncode(absPulses[10], p1[5], silkShellCodeTable0[:])
	splitEncode(p1[6], p2[3], silkShellCodeTable1[:])
	splitEncode(absPulses[12], p1[6], silkShellCodeTable0[:])
	splitEncode(absPulses[14], p1[7], silkShellCodeTable0[:])
}

func encodePulseLSBs(enc *entcode.Encoder, block pulseBlock) {
	if block.nLShifts == 0 {
		return
	}
	for _, p := range block.pulses {
		absQ := int(p)
		if absQ < 0 {
			absQ = -absQ
		}
		for bit := block.nLShifts - 1; bit >= 0; bit-- {
			enc.EncodeIcdf((absQ>>bit)&1, silkLSBICDFDec[:], 8)
		}
	}
}

func encodePulseSigns(enc *entcode.Encoder, blocks []pulseBlock, signalType, quantOffset int) {
	signRow := signalType*2 + quantOffset
	if signRow < 0 {
		signRow = 0
	}
	if signRow > 5 {
		signRow = 5
	}

	for _, block := range blocks {
		p := block.sum & 0x1F
		if p == 0 {
			continue
		}
		col := p
		if col > 6 {
			col = 6
		}
		icdf2 := [2]uint8{silkSignICDF[signRow][col], 0}
		for _, pulse := range block.pulses {
			if pulse == 0 {
				continue
			}
			symbol := 1
			if pulse < 0 {
				symbol = 0
			}
			enc.EncodeIcdf(symbol, icdf2[:], 8)
		}
	}
}

// Reset resets the encoder state
// resetForSideReactivation resets the side encoder when side coding resumes
// after mid-only frames. libopus (silk_Encode) clears the coding state but
// keeps the channel's resampler, so the front-end delay line survives.
// resetForSideReactivation is silk_Encode's partial reset of the side channel
// for the first frame with side coding after mid-only frames: the shaping
// state, the NSQ state, the previous NLSFs, the pitch lag and gain history and
// first_frame_after_reset. Everything else (VAD, input buffers, high-pass,
// frame counter, LTP correlation) carries on.
func (e *Encoder) resetForSideReactivation() {
	e.shapeHarmSmooth32 = 0
	e.shapeTiltSmooth32 = 0
	e.shapeHarmSmooth = 0
	e.shapeTiltSmooth = 0
	e.prevGainIdx = 10
	e.nsq = newSilkNSQState(e.frameSize, silkLTPMemLengthMs*(e.sampleRate/1000))
	for i := range e.lpcState {
		e.lpcState[i] = 0
	}
	for i := range e.ltpState {
		e.ltpState[i] = 0
	}
	for i := range e.prevNLSFQ15 {
		e.prevNLSFQ15[i] = 0
	}
	e.prevNLSF = nlsfQ15ToRadians(e.prevNLSFQ15)
	e.prevPitchLag = 100
	e.prevGainQ16 = 65536
	e.prevSignalType = SignalTypeInactive
	e.firstFrameAfterReset = true
}

func (e *Encoder) Reset() {
	e.frameVAD = nil
	e.curFrame = 0
	e.noSpeechCounter = 0
	e.silkVAD.reset()
	e.speechActivity = 1.0
	e.inputTilt = 0
	e.speechActivityQ8 = 0
	e.inputTiltQ15 = 0
	e.inputQuality = 1.0
	for i := range e.inputQualityB {
		e.inputQualityB[i] = 1.0
	}
	e.inputQualityBandQ15 = [silkVADNBands]int{}
	e.variableHPSmth1Q15 = variableHPSmth1Initial()
	e.prevEnergy = 1.0
	for i := range e.prevLPC {
		e.prevLPC[i] = 0
	}
	for i := range e.prevNLSF {
		e.prevNLSF[i] = math.Pi * float64(i+1) / float64(e.lpcOrder+1)
	}
	if len(e.prevNLSFQ15) != e.lpcOrder {
		e.prevNLSFQ15 = make([]int16, e.lpcOrder)
	}
	for i := range e.prevNLSFQ15 {
		e.prevNLSFQ15[i] = int16((float64(i+1) / float64(e.lpcOrder+1)) * 32768.0)
	}
	e.prevPitchLag = 100
	e.prevLagIndex = 0
	e.prevGains = []float64{1.0, 1.0, 1.0, 1.0}
	e.prevGainIdx = 10
	e.prevGainQ16 = 65536
	e.prevSignalType = SignalTypeUnvoiced
	for i := range e.lpcState {
		e.lpcState[i] = 0
	}
	for i := range e.ltpState {
		e.ltpState[i] = 0
	}
	clear(e.xBuf)
	e.pitchHist = e.xBuf[:e.ltpMemLength()]
	e.encInputDelay = nil
	e.lastXBuf = nil
	e.shapeHarmSmooth32 = 0
	e.shapeTiltSmooth32 = 0
	e.pitchPredGain = 0
	e.nBitsExceeded = 0
	e.nBitsUsedLBRR = 0
	e.targetRateBps = 0
	e.frameCounter = 0
	e.prevLagForPitch = 0
	e.ltpCorrState = 0
	e.pitchResidual = nil
	e.curLTP = nil
	e.firstFrameAfterReset = true
	e.curPitchLagIndex = 0
	e.curPitchContourIndex = 0
	e.ltpSumLogGainQ7 = 0
	e.nsq = newSilkNSQState(e.frameSize, silkLTPMemLengthMs*(e.sampleRate/1000))
	e.nsqSeed = 0
	e.shapeHarmSmooth = 0
	e.shapeTiltSmooth = 0
	e.lastSNRVBRFrame = false
	e.lastSNRVBRStream = false
	e.lastFinalRange = 0
	e.pendingLBRR = nil
	e.curLBRR = nil
	e.pendingLBRRFrames = 0
	e.pendingLBRRStereoPred = nil
	e.lbrrEnabledPrev = false
	e.lbrrGainIncreases = 0
	e.lbrrPrevLastGainIndex = 0
	e.lbrrFlag = false
	e.lbrrBitsPerFrame = 0
	e.capLagIndex = 0
	e.capContour = 0
	e.capLTPPerIdx = 0
	e.capLTPGainIdx = nil
	e.capLTPScaleIndex = 0
	e.curLTPScaleIndex = 0
	e.lastGainSymbols = nil
	e.codeNoLTPScaling = false
	e.channelRateBps = 0
	e.pendingLBRRStereoMidOnly = nil
	e.lambda32 = 0
	e.haveLambda32 = false
	e.frameMaxBits = 0
	e.frameUseCBR = false
	e.streamChannels = e.channels
	e.prevStreamChannels = 0
	e.toMono = false
	e.stereoState.reset()
	e.prevOnlyMiddle = false
	if e.side != nil {
		e.side.Reset()
	}
}

// computeEnergy computes signal energy
func computeEnergy(signal []float64) float64 {
	if len(signal) == 0 {
		return 0
	}
	energy := 0.0
	for _, s := range signal {
		energy += s * s
	}
	return energy / float64(len(signal))
}

// QuantizeSubframeGains quantizes subframe gains
func QuantizeSubframeGains(gains []float64) ([]float64, []int) {
	quantized := make([]float64, len(gains))
	indices := make([]int, len(gains))

	for i, g := range gains {
		gainDB := LinearToDB(g)

		step := 3.0
		index := int(math.Round(gainDB / step))

		if index < -20 {
			index = -20
		}
		if index > 13 {
			index = 13
		}

		indices[i] = index
		quantized[i] = DBToLinear(float64(index) * step)
	}

	return quantized, indices
}

// runFrameVAD runs the fixed-point VAD on packet frame index frame (in frame
// order, once per frame) and returns the frame's VAD flag as
// silk_encode_do_VAD_FLP derives it: active when speech_activity_Q8 reaches
// SILK_FIX_CONST(SPEECH_ACTIVITY_DTX_THRES, 8). The result is kept for the
// frame encode so the VAD state advances exactly once per frame.
func (e *Encoder) runFrameVAD(frame int, pcm []float64) bool {
	const activityThresholdQ8 = 13 // SILK_FIX_CONST(0.05, 8)
	if frame == 0 {
		e.frameVAD = e.frameVAD[:0]
	}
	res := e.silkVADGetSAQ8(pcm)
	for len(e.frameVAD) <= frame {
		e.frameVAD = append(e.frameVAD, silkVADResult{})
	}
	e.frameVAD[frame] = res
	if res.speechActivityQ8 < activityThresholdQ8 {
		e.noSpeechCounter++
		if e.noSpeechCounter > silkMaxConsecutiveDTX+silkNBSpeechFramesBeforeDTX {
			e.noSpeechCounter = silkNBSpeechFramesBeforeDTX
		}
		return false
	}
	e.noSpeechCounter = 0
	return true
}

// frameVADResult returns the stored VAD result for the frame being encoded,
// falling back to a direct evaluation when the frame was not pre-analysed
// (callers outside the packet loop).
func (e *Encoder) frameVADResult(signal []float64) silkVADResult {
	if e.curFrame >= 0 && e.curFrame < len(e.frameVAD) {
		return e.frameVAD[e.curFrame]
	}
	return e.silkVADGetSAQ8(signal)
}
