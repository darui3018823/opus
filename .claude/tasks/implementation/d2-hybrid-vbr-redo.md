# Codex Task: D-2 Iteration 0 REDO — hybrid VBR サイズ決定を libopus 忠実に

branch: `codex/d2-hybrid-target-clamp` に積む (PR #21 は draft 化済み)。
先行 commit: `e330c8b` (spill 修正) + `99a4bc7` (cross-decode guard)。

## 何が分かったか (証拠つき・再調査不要)

1. **spill 修正 (`e330c8b`) は非 conformant**。overshoot frame で
   `enc.FinalRange() != dec.FinalRange()` (Claude 再現済み: 44 kbps VOIP mono +
   `hybridCVBROnsetFixture`、frame 0 len=116 で enc `00d7a354` vs dec `014a321c`、
   非 overshoot frame は一致)。原因は Codex bot / Gemini の指摘どおり:
   CELT は `targetBytes` 基準で allocation 済みなのに、emitted length (= decoder の
   allocation 基準) を事後に書き換えているため。
   **audio の cross-decode SNR では検出できない** (両 decoder が同じ誤読をするため)。
2. **floor 事前引き上げ (Claude が試して棄却) も壊れる**。
   CELT encode 前に `targetBytes = max(adaptive, (tell+40+7)>>3)` へ引き上げる版は
   libopus cross-decode が SNR 0.098 dB へ崩壊 (corpus test01 48kbps、~30 frame が
   floor 発動、frame 255 付近から状態汚染)。**原因未特定**。バックアップ:
   scratchpad の `opus_go_floor_variant.go.bak` (Claude セッションの
   `C:\Users\daruks\AppData\Local\Temp\claude\...\scratchpad\`、消えていたら
   このファイルの diff 説明から再構成可)。容疑: frameBytes > maxBytes が
   呼び出し側の packetization / CVBR 予約の前提を破る、または entcode の
   capacity 拡大方向の Shrink と Bytes() の相互作用。
3. **libopus 1.6.1 の正しい構造** (`src/opus_encoder.c`, ec_enc_shrink 周辺):
   ```c
   nb_compr_bytes = (max_data_bytes-1)-redundancy_bytes;
   ec_enc_shrink(&enc, nb_compr_bytes);
   /* その後 celt_encode_with_ec() が VBR の最終サイズを内部で決定し、
      allocation 計算の前に自分で ec_enc_shrink する */
   ```
   つまり **hybrid の VBR サイズ決定は CELT encoder 内部** (celt/celt_encoder.c の
   celt_encode_with_ec 内 VBR ブロック: total_bits 計算 → nbCompressedBytes 決定 →
   ec_enc_shrink → allocation)。我々の `hybridAdaptiveTargetBytes` による
   事前 target 決定はこの構造からの逸脱で、今回の問題の根。

## やること

**celt.EncodeHybrid (internal/celt/encoder.go encodeRange) に libopus の
VBR サイズ決定を移植する**:

1. libopus `celt/celt_encoder.c` の `celt_encode_with_ec` の VBR ブロック
   (`if (vbr)` 〜 `ec_enc_shrink(enc, nbCompressedBytes)`) を読み、
   shared-ec (hybrid) 経路での nbCompressedBytes 決定を Go 側 encodeRange に移植。
   - 参照: https://github.com/xiph/opus/blob/v1.6.1/celt/celt_encoder.c
   - 移植対象は VBR target 計算 (base_target, tf_calibration, stereo/tonality
     補正は既存実装の範囲で可) → nbAvailableBytes clamp → **ec_enc_shrink →
     その後に allocation** という順序の一致が本質。
   - shrink 後の値が decoder の見る packet 長になるよう、opus.go 側は
     `targetBytes` 事前計算 (`hybridAdaptiveTargetBytes`) と `enc.Shrink` を
     hybrid VBR 経路から撤去し、`maxBytes` をそのまま CELT へ渡す。
     CBR / redundancy 経路は現状維持。
2. `hybridAdaptiveTargetBytes` は VBR 経路で不要になるはず。dead code になるなら
   削除 (他経路で使っていれば残す)。
3. spill 容認ブロック (`targetBytes = len(frame)`) は削除し、
   `len(frame) > targetBytes` は無条件 hard error に戻す
   (CELT 内部で shrink するので構造上起きなくなる)。

## 受け入れ基準 (全部必須・順に)

1. **final range 一致 (最重要・新規テスト)**: `TestHybridCVBROnsetFinalRange`
   (通常タグ) を追加 — `hybridCVBROnsetFixture` を 44 kbps VOIP mono で
   6 frame encode→decode し、**全 frame で `enc.FinalRange() == dec.FinalRange()`**。
   現行 spill 実装ではこのテストは fail する (frame 0)。これが red→green になること。
2. 既存 `TestHybridCVBROnsetLibopusConsistency` / `TestHybridCVBROnsetBudgetOvershoot`
   PASS 維持 (VBR なので nominal 超えは依然起こりうる、起きなくなったら
   Overshoot テストの期待を実態に合わせて調整可・理由を報告)。
3. `TestHybridRangeExact` など既存 hybrid テスト全 PASS。
4. `go test -count=1 ./...` + `go test -count=1 -tags opusref ./...` (PowerShell) 全 PASS、
   RFC vector 12/12 維持。
5. scoreboard フル実行 **140/140 ok** かつ speech 系クラスの gap/bytes が
   iter0 記録と同水準 (bytes 比併記で報告)。
6. docs (`CURRENT_IMPLEMENTATION.md` / `MODE_RATE_POLICY_DIFF.md` /
   `REAL_CORPUS_SCOREBOARD.md` の該当記述) を新実装に合わせて更新。

## ガードレール

- RC のビット読み書き順・SILK 側は触らない。変更は hybrid の CELT budget
  決定と celt encodeRange の VBR ブロックに限定。
- CELT-only 経路の挙動を変えない (VBR 移植は shared-ec/hybrid 分岐に限定するか、
  CELT-only にも波及する場合は AB で bytes/SNR 不変を確認して報告)。
- 判定に迷ったら数字を添えて停止・報告 (Claude が判定)。
