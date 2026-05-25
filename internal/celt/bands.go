package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// Band represents a frequency band in CELT
type Band struct {
	Start  int       // Starting MDCT coefficient index
	Size   int       // Number of coefficients in band
	Energy float64   // Band energy
	Coeffs []float64 // MDCT coefficients for this band
}

// BandProcessor handles band-level operations
type BandProcessor struct {
	mode  *Mode
	bands []*Band
}

// NewBandProcessor creates a new band processor
func NewBandProcessor(mode *Mode) *BandProcessor {
	bp := &BandProcessor{
		mode:  mode,
		bands: make([]*Band, mode.Bands.NumBands),
	}

	// BandStart/BandSizes are stored at LM=0 (NBase-sample) scale.
	// For a frameSize-sample frame, M = frameSize/NBase = 2^LM scales each bin count.
	M := mode.FrameSize / mode.NBase
	if M < 1 {
		M = 1
	}

	for i := 0; i < mode.Bands.NumBands; i++ {
		start := mode.Bands.BandStart[i] * M
		size := mode.Bands.BandSizes[i] * M
		bp.bands[i] = &Band{
			Start:  start,
			Size:   size,
			Coeffs: make([]float64, size),
		}
	}

	return bp
}

// DecodeBandEnergies decodes band energies from bitstream
// This is a simplified version - full implementation would use range decoder
func (bp *BandProcessor) DecodeBandEnergies(energyBits []int) {
	for i := 0; i < len(bp.bands) && i < len(energyBits); i++ {
		// Convert quantized energy to linear scale
		// In CELT, energies are coded in log domain
		// energyBits[i] represents quantized log energy

		// Simple mapping: each bit represents ~3dB
		logEnergy := float64(energyBits[i]) * 0.5 // 0.5 corresponds to ~3dB
		bp.bands[i].Energy = math.Exp(logEnergy)
	}
}

// DenormalizeBands applies energy normalization to band coefficients
func (bp *BandProcessor) DenormalizeBands() {
	for _, band := range bp.bands {
		if band.Energy > 0 {
			// Calculate current energy
			currentEnergy := 0.0
			for _, coeff := range band.Coeffs {
				currentEnergy += coeff * coeff
			}

			if currentEnergy > 0 {
				// Scale coefficients to match target energy
				scale := math.Sqrt(band.Energy / currentEnergy)
				for i := range band.Coeffs {
					band.Coeffs[i] *= scale
				}
			}
		}
	}
}

// AssembleMDCT assembles band coefficients into full MDCT spectrum
func (bp *BandProcessor) AssembleMDCT() []float64 {
	// Calculate total number of MDCT coefficients
	totalCoeffs := 0
	for _, band := range bp.bands {
		totalCoeffs = max(totalCoeffs, band.Start+band.Size)
	}

	mdct := make([]float64, totalCoeffs)

	// Copy band coefficients to MDCT spectrum
	for _, band := range bp.bands {
		copy(mdct[band.Start:band.Start+band.Size], band.Coeffs)
	}

	return mdct
}

// DecodeBandCoeffs decodes PVQ-quantized coefficients for a band.
// Uses CWRS direct decoding matching libopus decode_pulses → ec_dec_uint(V(N,K)) → cwrsi.
func (bp *BandProcessor) DecodeBandCoeffs(dec *entcode.Decoder, bandIdx int, pulses int) {
	if bandIdx < 0 || bandIdx >= len(bp.bands) {
		return
	}

	band := bp.bands[bandIdx]
	if pulses <= 0 || band.Size <= 0 {
		return
	}

	// Read CWRS index from range coder, then decode pulse vector via cwrsiLibopus.
	// libopus cwrsi processes dimensions from N-1 down to 0 (RFC 6716 §5.4.3.3).
	n := band.Size
	v := cwrsV(n, pulses)
	var vClamped uint32
	if v >= uint64(math.MaxUint32) {
		vClamped = math.MaxUint32
	} else {
		vClamped = uint32(v)
	}
	idx := dec.DecodeUint(vClamped)
	y := cwrsiLibopus(n, pulses, idx)

	// Normalize pulse vector to unit energy.
	output := make([]float64, n)
	norm := 0.0
	for i, v := range y {
		output[i] = float64(v)
		norm += output[i] * output[i]
	}
	if norm > 0 {
		scale := 1.0 / math.Sqrt(norm)
		for i := range output {
			output[i] *= scale
		}
	}
	copy(band.Coeffs, output)
}

// ApplyFineEnergy applies fine energy refinement from the end of the packet.
// q2 is the raw decoded value in [0, 2^fb); fb is the number of fine bits.
// Offset = (q2+0.5)/2^fb - 0.5 in log2-amplitude; applied as band.Energy *= 4^offset.
func (bp *BandProcessor) ApplyFineEnergy(bandIdx, q2, fb int) {
	if bandIdx < 0 || bandIdx >= len(bp.bands) || fb <= 0 {
		return
	}
	band := bp.bands[bandIdx]
	offset := (float64(q2)+0.5)/float64(int(1)<<fb) - 0.5
	band.Energy *= math.Exp2(2.0 * offset)
}

// ComputeBandEnergy computes the energy of a band from coefficients
func ComputeBandEnergy(coeffs []float64) float64 {
	energy := 0.0
	for _, c := range coeffs {
		energy += c * c
	}
	return energy
}

// NormalizeBand normalizes band coefficients to unit energy
func NormalizeBand(coeffs []float64) float64 {
	energy := ComputeBandEnergy(coeffs)
	if energy > 0 {
		scale := 1.0 / math.Sqrt(energy)
		for i := range coeffs {
			coeffs[i] *= scale
		}
	}
	return energy
}

// InterpolateBandEnergies interpolates energies for missing bands
func (bp *BandProcessor) InterpolateBandEnergies() {
	// For bands with zero or missing energy, interpolate from neighbors
	for i := 1; i < len(bp.bands)-1; i++ {
		if bp.bands[i].Energy <= 0 {
			// Average of neighbors
			left := bp.bands[i-1].Energy
			right := bp.bands[i+1].Energy
			if left > 0 && right > 0 {
				bp.bands[i].Energy = math.Sqrt(left * right)
			} else if left > 0 {
				bp.bands[i].Energy = left
			} else if right > 0 {
				bp.bands[i].Energy = right
			}
		}
	}
}

// max function removed - using built-in max
