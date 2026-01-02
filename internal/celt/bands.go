package celt

import (
	"math"
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
	mode   *Mode
	bands  []*Band
}

// NewBandProcessor creates a new band processor
func NewBandProcessor(mode *Mode) *BandProcessor {
	bp := &BandProcessor{
		mode:  mode,
		bands: make([]*Band, mode.Bands.NumBands),
	}
	
	// Initialize bands
	for i := 0; i < mode.Bands.NumBands; i++ {
		bp.bands[i] = &Band{
			Start:  mode.Bands.BandStart[i],
			Size:   mode.Bands.BandSizes[i],
			Coeffs: make([]float64, mode.Bands.BandSizes[i]),
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

// DecodeBandCoeffs decodes PVQ-quantized coefficients for a band
func (bp *BandProcessor) DecodeBandCoeffs(bandIdx int, pvqIndex uint32, pulses int) {
	if bandIdx < 0 || bandIdx >= len(bp.bands) {
		return
	}
	
	band := bp.bands[bandIdx]
	
	// Decode PVQ index to unit vector
	coeffs := PVQDecode(band.Size, pulses, pvqIndex)
	
	// Copy to band
	copy(band.Coeffs, coeffs)
}

// ApplyFineEnergy applies fine energy adjustments
func (bp *BandProcessor) ApplyFineEnergy(bandIdx int, fineEnergy int) {
	if bandIdx < 0 || bandIdx >= len(bp.bands) {
		return
	}
	
	band := bp.bands[bandIdx]
	
	// Fine energy is typically a small adjustment in dB
	// Each step is approximately 0.5 dB
	adjustment := math.Pow(10.0, float64(fineEnergy)*0.05)
	band.Energy *= adjustment
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
