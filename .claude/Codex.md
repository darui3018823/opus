# Codex Handoff Notes

## CELT denormalize/IMDCT split, 2026-05-30

User asked to stop at cause collection; do not start production decoder fixes from this note alone.

Current working-tree diagnostics:
- `scripts/oracle/celt_decoder_instr.c` now dumps `[XD] ch=... band=...` immediately after libopus `denormalise_bands()` in `celt_synthesis()`.
- Rebuild oracle with `pwsh scripts/oracle/build.ps1`; executable is `$env:TEMP\opusoracle\oracle.exe`.
- `internal/celt/diag_denorm_test.go` contains Go-side helpers to apply fine/final-fine energy and dump denormalized MDCT coefficients in the same `[XD]` format.
- `internal/celt/diag_oracle_test.go` and `internal/celt/diag_stereo_test.go` print `[XD]` after PVQ, anti-collapse raw bit, and final fine energy.

Observed on TV07 pkt0:
- `[XB]` normalized PVQ output matches oracle and Go closely.
- `[XD]` denormalized MDCT output still mismatches, so the first visible mismatch is before IMDCT/overlap-add.
- Initial `[XD]` comparison was misleading until the diagnostic harness reset convention was corrected: libopus reset has `oldBandE=0`, `oldLogE/oldLogE2=-28`. The Go diagnostics now use that convention.

Likely next diagnostic split:
- Add/dump normalized `X[]` after anti-collapse and final fine, immediately before denormalize.
- If post-anti-collapse `X[]` matches but `[XD]` differs, suspect denormalize/scaling.
- If post-anti-collapse `X[]` already differs, suspect anti-collapse or energy/final-fine state.

Useful commands:
```powershell
pwsh scripts/oracle/build.ps1
& "$env:TEMP\opusoracle\oracle.exe" "testdata\opus_newvectors\testvector07.bit" 0 2>&1 | Select-String -Pattern '^\[XB\]|^\[XD\]'
go test ./internal/celt -run '^TestOracleTrace$' -count=1 -v 2>&1 | Select-String -Pattern '^\[XB\]|^\[XD\]|fine_final|anticollapse'
```
