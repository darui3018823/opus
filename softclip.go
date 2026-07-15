package opus

import "fmt"

// SoftClipFloat32 applies libopus-style soft clipping (opus_pcm_soft_clip) to
// interleaved float PCM in place, folding samples outside [-1, 1] back into
// range with a smooth non-linearity instead of hard clamping. The mem slice
// must have at least channels elements; it carries the per-channel clipping
// state so consecutive frames join without discontinuities and must be zeroed
// only at stream start or reset.
func SoftClipFloat32(pcm []float32, channels int, mem []float32) error {
	if channels < 1 {
		return fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedChannels, channels)
	}
	if len(pcm)%channels != 0 {
		return fmt.Errorf("%w: PCM length %d is not divisible by channels %d", ErrBadArg, len(pcm), channels)
	}
	if len(mem) < channels {
		return fmt.Errorf("%w: soft clip memory has %d entries, need %d", ErrBadArg, len(mem), channels)
	}
	n := len(pcm) / channels
	if n == 0 {
		return nil
	}
	// Clamp to [-2, 2], the domain of the non-linearity. Its derivative is
	// zero at ±2, so no discontinuity is introduced.
	for i, v := range pcm {
		if v > 2 {
			pcm[i] = 2
		} else if v < -2 {
			pcm[i] = -2
		}
	}
	for c := 0; c < channels; c++ {
		x := pcm[c:]
		a := mem[c]
		// Continue applying the previous frame's non-linearity to avoid any
		// discontinuity at the frame boundary.
		for i := 0; i < n; i++ {
			v := x[i*channels]
			if v*a >= 0 {
				break
			}
			x[i*channels] = v + a*v*v
		}

		curr := 0
		x0 := x[0]
		for {
			i := curr
			for ; i < n; i++ {
				if x[i*channels] > 1 || x[i*channels] < -1 {
					break
				}
			}
			if i == n {
				a = 0
				break
			}
			peakPos := i
			start, end := i, i
			maxval := abs32(x[i*channels])
			// Bound the correction by the zero crossings surrounding the
			// clipped region, tracking the largest peak inside it.
			for start > 0 && x[i*channels]*x[(start-1)*channels] >= 0 {
				start--
			}
			for end < n && x[i*channels]*x[end*channels] >= 0 {
				if abs32(x[end*channels]) > maxval {
					maxval = abs32(x[end*channels])
					peakPos = end
				}
				end++
			}
			// Special case: clipping before the first zero crossing.
			special := start == 0 && x[i*channels]*x[0] >= 0

			// Compute a such that maxval + a*maxval^2 = 1, slightly boosted
			// so rounding never leaves output outside ±1.
			a = (maxval - 1) / (maxval * maxval)
			a += a * 2.4e-7
			if x[i*channels] > 0 {
				a = -a
			}
			for j := start; j < end; j++ {
				v := x[j*channels]
				x[j*channels] = v + a*v*v
			}

			if special && peakPos >= 2 {
				// Linear ramp from the first sample to the peak to avoid a
				// discontinuity at the start of the frame.
				offset := x0 - x[0]
				delta := offset / float32(peakPos)
				for j := curr; j < peakPos; j++ {
					offset -= delta
					v := x[j*channels] + offset
					if v > 1 {
						v = 1
					} else if v < -1 {
						v = -1
					}
					x[j*channels] = v
				}
			}
			curr = end
			if curr == n {
				break
			}
		}
		mem[c] = a
	}
	return nil
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
