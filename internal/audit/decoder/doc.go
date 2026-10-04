// Package audit compares the decoder with a plain-C libopus 1.6.1 decoder
// oracle over the official vectors, random loss, FEC, multistream, spliced
// and malformed streams, at every output rate and format.
//
// The tests are opt-in: build the oracle with
// scripts/oracle/decoder_audit/build.ps1 and run
//
//	OPUS_LIBOPUS_AUDIT=1 go test ./internal/audit/decoder -v
//
// Each case logs a RESULT line (exact operations and samples, return,
// range, duration, bandwidth and pitch mismatches) and fails when it is
// not bit-exact.
package audit
