# Codex Task: Phase D-2 — mode-rate-quality gate を測定駆動で 1 つずつ拡張

> **Status: superseded on 2026-07-16.** The final iteration-0 fix is
> `ccd3e82`; later gate expansion was stopped when the parent plan changed.
> Keep this as experiment history and do not execute it as a current task.

親プラン: `.claude/plans/post-audit-2026-07-15.md` Phase D。
作業台: `docs/MODE_RATE_POLICY_DIFF.md` (D-1 差分表) + `docs/REAL_CORPUS_SCOREBOARD.md` (baseline)。
前提 main: PR #20 マージ後 (merge `ff70c75` 以降)。

## ⚠ このタスクの性質 (最重要)

D-2 は**再帰 (iteration) 型**のタスク。1 iteration = 1 変更 = 1 branch = 1 測定 = 1 判定。
**複数の gate 変更を 1 branch に混ぜることを禁止する**。過去の transparent NLSF で
「SNR だけ見ると勝ちに見えて bytes 込みでは負け」の罠を踏んでおり、混ぜると
どの変更が後退を入れたか切り分け不能になる。

## Iteration protocol (全 iteration 共通)

1. main から branch を切る: `codex/d2-<短い切り口名>`。
2. 変更を実装する (1 論点のみ)。
3. 検証 (すべて PowerShell):
   - `go vet ./...`
   - `go test -count=1 ./...`
   - `go test -count=1 -tags opusref ./...`
4. scoreboard 測定:
   ```powershell
   $env:OPUS_REAL_CORPUS = "1"
   go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
   ```
   baseline (docs/REAL_CORPUS_SCOREBOARD.md の Baseline 節) と比較し、
   **クラス別 avg/min/max gap_snr_matched_db と own_bytes の変化**を必ず両方見る。
5. 判定基準:
   - **採用**: 対象セルが改善し、他セルの gap 悪化が +0.3dB 以内、
     bytes 比が悪化しない (own_bytes 増は per-bit で正当化できる場合のみ)。
   - **棄却**: 上記を満たさない → branch を放置 (削除せず) して結果だけ記録し、次へ。
   - 判定に迷う場合は数字を添えて報告し、Claude の判定を待つ。
6. 採用なら: docs (MODE_RATE_POLICY_DIFF.md の該当行を更新) + 結果報告。
   push/PR は Claude が検分後に行うので **未 push のまま報告**。
7. 各 iteration の結果 (採用/棄却とも) をこの task brief と親プランのステータスに追記:
   iteration 番号 / branch / 変更概要 / scoreboard 差分の要点 / 判定 / 理由。

## Iteration 0 (必須・最初): hybrid encode-size error の修正

これは gate 拡張ではなく**純バグ修正**。baseline で 2 セルが
`hybrid frame 0 exceeds target: 121-123 > 120 bytes` の own_encode_error になる。

- 症状: 48 kHz mono hybrid、低〜中 bitrate + 特定 loss 設定で、hybrid frame が
  target 上限 (120 bytes) を数 byte 超えると encode がエラーを返す。
- 期待挙動: libopus は target 超過で失敗しない。CELT 高域側の配分を縮めて
  target 内に収める (または CBR/CVBR の clamp 経路に落とす) べき。
- 調査起点: `opus.go` の hybrid encode 経路で "exceeds target" を出している箇所と、
  その手前の `hybridAdaptiveTargetBytes()` / SILK 消費 bytes の見積もり。
- 完了条件: scoreboard 全 134 セルが status=ok。既存テスト + opusref 全 PASS。
  回帰テスト (エラーを再現する rate/loss 設定の encode が成功すること) を追加。

## Iteration 1 以降の候補 (優先順・1 iteration に 1 つ)

`docs/MODE_RATE_POLICY_DIFF.md` の Partial/Unsupported 行から、期待効果と
リスクの比で並べた候補。**着手前に必ず対象セルを scoreboard で特定してから**:

1. **SILK entry の bitrate 閾値を libopus の mode threshold 相当へ**
   (現状: mono <= 40 kbps / stereo <= 48 kbps の固定)。libopus
   `opus_encoder.c` の mode_thresholds + hysteresis を参照。
   対象セル: clean/noisy speech の 32-48 kbps 帯。
2. **mode 判定の hysteresis** (前 frame mode による閾値オフセット)。
   mode 切替の境界 bitrate でのちらつき低減。
3. **SILK internal rate 制御** (`silk_control_audio_bandwidth` 相当の
   NB/MB/WB 切替)。現状は入力 rate 固定。対象: 16 kbps 帯の低 rate セル。
4. **stereo width 縮小** (低 bitrate stereo)。対象: stereo_speech 16-24 kbps。
5. **user_bitrate_to_bitrate 相当の overhead 補正**。

音質系の候補 (音声クラスは既に互角) より、**エラー/挙動系 (Iteration 0) と
mode 選択系 (1-2) が先**。music/mixed の CELT セルは対象外 (プランの見送り事項)。

## ガードレール (違反したら即手を止める)

- RC (range coder) のビット読み書き順は変更禁止 (FEC/conformance 破壊の実績)。
- 12/12 official vector と opusref 全 PASS を全 iteration で維持。
- `OPUS_SILK_TRANSPARENT_NLSF` / `OPUS_SILK_RC_SNR` の env-gate には触らない。
- decoder 側は変更しない (D-2 は encoder policy のみ)。
- scoreboard harness 自体の変更は測定の連続性を壊すので、バグ修正以外は禁止。

## 停止条件

- 候補 1-2 を消化して net win が出ない場合は、そこで止めて Claude に報告
  (D-2 全体の継続可否は測定結果を見て判断する)。
- 同一 iteration で 3 回作り直しても判定基準を満たせない場合も同様。
