package audit

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("OPUS_LIBOPUS_AUDIT") == "" {
		fmt.Println("skipping the libopus decoder audit: set OPUS_LIBOPUS_AUDIT=1")
		os.Exit(0)
	}
	if _, err := os.Stat(oraclePath()); err != nil {
		fmt.Println("skipping the libopus decoder audit: run scripts/oracle/decoder_audit/build.ps1")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
