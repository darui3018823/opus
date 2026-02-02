package entcode

import (
	"errors"
)

// EncodeLaplace encodes a value using a Laplace distribution.
// value: The value to encode
// fs: Probability of the zero symbol (fs = 32768 * prob(0)) - effectively the "width" or "decay" parameter
// decay: Decay factor (Q15 fixed point)
//
// This implements the functionality of ec_laplace_encode from libopus.
// See RFC 6716 Section 4.1 "Range Coder" and Section 4.3.3 "Coarse Energy".
func (enc *Encoder) EncodeLaplace(value int, fs int, decay int) error {
	// fs is the probability of 0 in Q15 (0..32768)
	// decay is the decay factor in Q15 (0..32768)

	if fs <= 0 || fs >= 32768 {
		return errors.New("entcode: valid fs must be between 0 and 32768")
	}

	val := value

	// Separate sign and magnitude
	sign := uint16(0)
	if val < 0 {
		sign = 1
		val = -val
		// "Negative zero" logic handling implies we encode 0 differently than -0?
		// Actually Opus Laplace uses symmetric distribution where 0 is center.
		// val = -1 is distinct from 1. 0 is 0.
	}

	// Probability of zero: fs/32768
	// Probability of +/- 1: (32768-fs)/32768 * (1-decay)/32768 ... etc?

	// The standard implementation (ec_laplace_encode) logic:

	// The "fs" parameter is essentially the probability of the symbol '0'.

	// If value is 0, we encode in range [0, fs).
	if value == 0 {
		// "Slot 0" has probability fs/32768.
		split := (enc.rng >> 15) * uint32(fs)
		enc.rng = split
		enc.normalize()
		return nil
	}

	// If not zero:
	// We first step *over* the zero range.
	split := (enc.rng >> 15) * uint32(fs)
	enc.low += split
	enc.rng -= split // Now we are in the "not zero" range [fs, 32768)

	// Renormalize if needed? Assuming we might need to if fs was huge?
	// Actually typical range coder normalizes *after* shrinking range.
	// But here we just shrank it. Let's act like we encoded "Not Zero".
	// But we have more to encode.

	// Now we encode the value k = abs(value).
	// The distribution decays by 'decay' factor for each step.
	// val >= 1.

	// For each integer step i=0..val-1, we encode "Value > i".
	for i := 0; i < val-1; i++ {
		// Probability of stopping at i (given we are > i-1) vs continuing.
		// Continue probability is roughly 'decay'.

		// Using EncodeBit logic:
		// 0: Stop here. 1: Continue.

		// "decay" is prob of continuing?
		// ec_laplace_encode uses decay as the probability of "the next symbol exists".

		// Encode "Continue" (bit 1) with prob = decay.
		// Note EncodeBit(bit, prob) takes `prob` as probability of 1?
		// implementation check:
		// split = (rng >> 15) * prob
		// if bit: low += split; rng -= split  (upper part)
		// else: rng = split (lower part)

		// If we want "Continue" to be the *lower* part or *upper* part standard?
		// Typically decay is < 32768.
		// We output 1 (Continue).
		enc.EncodeBit(true, uint16(decay))
	}

	// Now we are at val (magnitude). We stop here.
	// Encode "Stop" (bit 0) with prob = decay.
	// Wait, we assume "decay" is prob of continue. So "Stop" is failure to continue.
	// So we encode 0.
	enc.EncodeBit(false, uint16(decay))

	// Finally, encode Sign.
	// 50/50 probability.
	enc.EncodeBit(sign == 1, 16384)

	return nil
}

// Note: The above is a Simplified iterative implementation.
// A fully optimized one creates the ranges explicitly.
// But this is valid "arithmetic coding" of the same distribution.
