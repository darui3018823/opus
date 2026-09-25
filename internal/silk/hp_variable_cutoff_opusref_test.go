//go:build opusref

package silk

import (
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestSILKHPVariableCutoffOracle drives the Go silk_HP_variable_cutoff port
// and the libopus body with the same previous-frame state sequences and
// requires variable_HP_smth1_Q15 to match exactly at every step.
func TestSILKHPVariableCutoffOracle(t *testing.T) {
	if got, want := variableHPSmth1Initial(), cgoref.SILKHPSmth1Initial(); got != want {
		t.Fatalf("initial variable_HP_smth1_Q15: Go=%d C=%d", got, want)
	}
	for _, fsKHz := range []int{8, 12, 16} {
		e := &Encoder{sampleRate: fsKHz * 1000, variableHPSmth1Q15: variableHPSmth1Initial()}
		smthC := cgoref.SILKHPSmth1Initial()
		state := uint32(fsKHz)
		next := func(n int) int {
			state = state*1664525 + 1013904223
			return int(state>>8) % n
		}
		for step := 0; step < 400; step++ {
			e.prevSignalType = SignalTypeVoiced
			if next(5) == 0 {
				e.prevSignalType = SignalTypeUnvoiced
			}
			e.prevLagForPitch = 2*fsKHz + next(16*fsKHz)
			e.inputQualityBandQ15[0] = next(32768)
			e.speechActivityQ8 = next(256)
			e.hpVariableCutoff()
			smthC = cgoref.SILKHPVariableCutoff(e.prevSignalType, fsKHz, e.prevLagForPitch,
				e.inputQualityBandQ15[0], e.speechActivityQ8, smthC)
			if e.variableHPSmth1Q15 != smthC {
				t.Fatalf("%d kHz step %d: variable_HP_smth1_Q15 Go=%d C=%d (type=%d lag=%d q=%d sa=%d)",
					fsKHz, step, e.variableHPSmth1Q15, smthC, e.prevSignalType, e.prevLagForPitch,
					e.inputQualityBandQ15[0], e.speechActivityQ8)
			}
		}
		t.Logf("%d kHz: 400 steps exact, final smth1=%d (%d Hz)", fsKHz, e.variableHPSmth1Q15, silkLog2Lin(e.variableHPSmth1Q15>>8))
	}
}
