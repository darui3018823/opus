# CELT RMSE confirmation report (2026-05-30)

> **Historical measurement.** These RMSE values were later superseded; the
> decoder now passes all 12 official RFC 8251 vectors. See
> `CURRENT_IMPLEMENTATION.md` for current verification status.

## Summary

Claude の指摘どおり、IMDCT 出力レイアウト修正後の `tv07`/`tv08` は大幅に改善済みだった。追加確認で、CELT 合成後の deemphasis が未適用だったため、`internal/celt/decoder.go` に libopus 相当の 1 次 IIR deemphasis を追加した。

この修正で CELT を含むベクターはさらに改善したが、公式ベクターの `RMSE < 0.001` には未達。ここから先は postfilter の libopus 互換実装、CELT 合成状態、SILK/Hybrid の未完成部分が絡むため、短時間で安全に直せる範囲を超える。

## Current RMSE

`go test -run '^TestOfficialVectors$' -count=1 -v`

| Vector | Current RMSE | Notes |
| --- | ---: | --- |
| tv01 | 0.040775 | SILK-only; Go 側 SILK 出力との差が主因 |
| tv02 | 0.021029 | SILK-only |
| tv03 | 0.020673 | SILK-only |
| tv04 | 0.021015 | SILK-only |
| tv05 | 0.018636 | SILK-only |
| tv06 | 0.019545 | SILK-only |
| tv07 | 0.006411 | CELT-only; deemphasis 追加で改善 |
| tv08 | 0.005196 | CELT+SILK 混在; CELT 部分は改善 |
| tv09 | 0.005149 | CELT+SILK 混在; CELT 部分は改善 |
| tv10 | 0.029305 | 混在; SILK/Hybrid と CELT 後段の両方 |
| tv11 | 0.035004 | 混在; SILK/Hybrid と CELT 後段の両方 |
| tv12 | 0.019153 | SILK-only |

deemphasis 追加前からの主な改善:

| Vector | Before | After |
| --- | ---: | ---: |
| tv07 | 0.016243 | 0.006411 |
| tv08 | 0.015222 | 0.005196 |
| tv09 | 0.015331 | 0.005149 |
| tv10 | 0.042622 | 0.029305 |
| tv11 | 0.105263 | 0.035004 |

## CELT entropy path

`go test ./internal/celt -run '^TestRangeVectors$' -count=1 -v`

Result: PASS.

CELT パケットの final range は一致している。

| Vector | CELT packets | Matched |
| --- | ---: | ---: |
| tv07 | 4186 | 4186 |
| tv08 | 841 | 841 |
| tv09 | 966 | 966 |
| tv10 | 655 | 655 |
| tv11 | 204 | 204 |

つまり、残差の主戦場は range decode/PVQ bitstream 消費ではなく、合成後段と未完成 SILK/Hybrid 側。

## Additional validation

`go test -tags cgo -run '^TestCGORef$' -count=1 -v`

Result: FAIL, but official `.dec` 比較と同じ RMSE 傾向。libopus 1.6.1 参照でも `tv07=0.00641`, `tv08=0.00520`, `tv09=0.00515`。

`go test ./internal/celt -count=1`

Result: PASS.

`go test ./...` 相当の全体確認は、既存の `cmd_diag` 二重 `main` と公式/cgo RMSE 閾値で失敗する。

## Implemented fix

`internal/celt/decoder.go`

- CELT decoder に channel ごとの deemphasis memory を追加。
- IMDCT と comb postfilter 後、PCM スケール前に `y[n] = x[n] + mem; mem = 0.85 * y[n]` を適用。
- `CopyStateFrom` と `Reset` で deemphasis state をコピー/初期化。

## Remaining work

1. postfilter を libopus の `comb_filter` と同じ状態遷移に寄せる。
   現在の Go 実装は decoded pitch/taps をフレーム全体に適用する簡易形。libopus は前回/今回/次回パラメータを保持し、先頭 `shortMdctSize` と残りで窓付きに切り替える。

2. CELT 合成状態の oracle 比較を postfilter 後/deemphasis 後まで延長する。
   IMDCT raw は oracle と一致済みなので、次は `comb_filter` と deemphasis の境界で差分を取るのが最短。

3. SILK-only / Hybrid の未完成部分を分離して評価する。
   `tv01-06`, `tv12` は CELT final range 以前に SILK 側の問題。CELT 改善とは別トラックで扱うべき。

