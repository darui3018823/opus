package silk

import (
	"fmt"
	"os"
)

// silkTraceSNREnabled is read once; callers test it before building the
// trace arguments so a disabled trace costs nothing per frame.
var silkTraceSNREnabled = os.Getenv("OPUS_SILK_TRACE_SNR") == "1"

func silkTraceSNR(format string, args ...any) {
	if !silkTraceSNREnabled {
		return
	}
	fmt.Fprintf(os.Stderr, "silk_snr_trace: "+format+"\n", args...)
}
