package opus

import "fmt"

// SoftClipFloat32 applies libopus-style soft clipping to interleaved float PCM
// in place. channels must be 1 or 2. The mem slice must have at least channels
// elements and is updated with the per-channel clipping state for continuity
// across calls.
func SoftClipFloat32(pcm []float32, channels int, mem []float32) error {
	if channels != 1 && channels != 2 {
		return fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedChannels, channels)
	}
	if len(pcm)%channels != 0 {
		return fmt.Errorf("%w: PCM length %d is not divisible by channels %d", ErrBadArg, len(pcm), channels)
	}
	if len(mem) < channels {
		return fmt.Errorf("%w: soft clip memory has %d entries, need %d", ErrBadArg, len(mem), channels)
	}
	if len(pcm) == 0 {
		return nil
	}
	for i, x := range pcm {
		if x > 2 {
			pcm[i] = 2
		} else if x < -2 {
			pcm[i] = -2
		}
	}
	frameSize := len(pcm) / channels
	for ch := 0; ch < channels; ch++ {
		a := mem[ch]
		x0 := pcm[ch]
		if a != 0 {
			offset := ch
			if x0*a >= 0 {
				for i := 0; i < frameSize; i++ {
					x := pcm[offset]
					pcm[offset] = x + a*x*x
					offset += channels
				}
			} else {
				for i := 0; i < frameSize; i++ {
					x := pcm[offset]
					if x*a < 0 {
						break
					}
					pcm[offset] = x + a*x*x
					offset += channels
				}
			}
		}
		mem[ch] = 0
		peak := float32(1)
		peakPos := -1
		offset := ch
		for i := 0; i < frameSize; i++ {
			x := pcm[offset]
			abs := x
			if abs < 0 {
				abs = -abs
			}
			if abs > peak {
				peak = abs
				peakPos = i
			}
			offset += channels
		}
		if peakPos < 0 {
			continue
		}
		x := pcm[peakPos*channels+ch]
		a = (float32(1) - peak) / (peak * peak)
		if x < 0 {
			a = -a
		}
		offset = ch
		for i := 0; i <= peakPos; i++ {
			x = pcm[offset]
			pcm[offset] = x + a*x*x
			offset += channels
		}
		if pcm[peakPos*channels+ch] > 1 {
			pcm[peakPos*channels+ch] = 1
		} else if pcm[peakPos*channels+ch] < -1 {
			pcm[peakPos*channels+ch] = -1
		}
		for i := peakPos + 1; i < frameSize; i++ {
			x = pcm[offset]
			if x*a >= 0 {
				break
			}
			pcm[offset] = x + a*x*x
			offset += channels
		}
		mem[ch] = a
	}
	return nil
}
