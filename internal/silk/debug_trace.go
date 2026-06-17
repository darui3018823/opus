package silk

import (
	"fmt"
	"os"
)

func silkTraceSNR(format string, args ...any) {
	if os.Getenv("OPUS_SILK_TRACE_SNR") != "1" {
		return
	}
	fmt.Fprintf(os.Stderr, "silk_snr_trace: "+format+"\n", args...)
}
