# Codex タスク: 16k speech-harmonic の peak オーバーシュート / SNR 負けを詰める

> **Status: completed on 2026-06-25.** The low-F0 first-frame pitch issue was
> addressed by `88875e6`. This investigation is historical; do not execute it
> as a current task.

## ゴール
SILK encoder AB スコアボードで**唯一残った libopus への負け** =
16k `speech-like-harmonic` の `gap_SNR_matched=+1.16dB` を解消（理想は ≤0、
最低でも +0.3dB 以下）。**他セルを回帰させないこと**が最優先。

## 採点盤（PowerShell 必須・cgo）
```
go test -count=1 -tags opusref -run 'TestOpusSILKABAgainstLibopusEncoder' -v .
```
読む指標: 各ケース末尾の `gap_SNR_matched`（正 = libopus に負け）と
`ratio_bytes_matched`（≈1 が公平比較の前提）。fixture 定義は
`opus_silk_ab_test.go`（`speech-like-harmonic`）。

## 現状の症状（直近計測・確定。再計測で裏取りしてよい）
16k speech-like-harmonic:
- own:   cfg=9 bytes=211 rms=0.09753 **peak=0.4479** SNR=7.77dB scale=0.8846
- libopus: bytes=563 rms=0.09360 **peak=0.2226** SNR=10.88dB scale=0.9666
- libopus_matched: bytes=254 rms=0.08696 peak=0.2245 SNR=8.93dB scale=1.0139
- **gap_SNR_matched=+1.16dB**, ratio_bytes_matched=0.831
- loudness own-minus-libopus-matched = **+1.00dB**（own の方が大きい）

比較（同じ harmonic の他レートは正常）:
- 8k:  own peak=0.2506 SNR=13.35dB  gap_SNR_matched=-3.99（勝ち）
- 12k: own peak=0.2290 SNR=11.06dB  gap_SNR_matched=-2.39（勝ち）

⇒ **16k だけ peak が ~2× にオーバーシュートし、SNR が 7.77dB に崩落**。8k/12k は健全。
本セルは own の方が +1dB ラウドで scale<1（デコード出力が基準より大きく、
評価系が縮めている）= **どこかのサブフレームで励起/予測が暴れている**兆候。

## 仮説（プロジェクトメモリ由来・未確定。Codex が計測で確定させる）
長年「speech-harmonic の残課題 = **first-voiced-frame の LTP scale / rewhitening 系**」
と記録されてきた症状と一致。無音→有声の最初の有声フレームで pitchHist=0（LTP 予測不可）
のため、LTP scale 選択や rewhitening、または first-frame の gain/excitation 正規化が
16k(=最長 lag・最大 shapingLPCOrder) で過大パルスを生む可能性。

## やること（順序）
1. **局所化を先に**: 16k harmonic を 1 パケットずつデコードし、**どのフレーム/サブフレームで
   peak 0.4479 が出るか**を特定（first voiced frame か、特定 subframe か）。8k/12k と何が
   分岐するか（lag 長 / shapingLPCOrder / interp factor / LTP scale index / gain index）を
   並べて差分を取る。**原因をデータで確定してから**コードを触る。
2. 確定した原因に対して**最小限の修正**。候補領域（要確認、思い込み禁止）:
   - `internal/silk/encoder.go`: voiced gain / first-frame 処理 / LTP scale 選択
   - `internal/silk/nsq_del_dec.go`: トレリス（rewhitening, k==2 reset, gain flush）
   - `internal/silk/ltp_quant.go`: LTP gain VQ / sum_log_gain
   - `internal/silk/pitch_flp.go`: `firstFrameAfterReset`, lag 再構成
   - `firstFrameAfterReset` / `prevNLSFQ15` / `ltpScaleQ14` / `silkLTPScaleICDF` 周辺
3. libopus FLP 参照（`$TEMP\opussrc\opus-1.5.2\silk\float\`、oracle build.ps1 が DL）の
   該当処理と突き合わせる。NLSF interp / unvoiced trellis / Burg LPC は移植済なので、
   差分は first-frame の scale / rewhitening / gain 系に絞られるはず。

## 制約・ガード（必須）
- 修正は **mono SILK-only 限定**のゲートを維持（stereo/hybrid は別 conformance、触らない）。
- **他スコアボードセルを回帰させない**: 修正後に全 15 セルを再計測し、勝っている 13 セルの
  `gap_SNR_matched` が悪化しないことを確認。特に 8k/12k harmonic と onset（first-frame 共有）。
- **opusref 全 PASS 維持**（公式 12/12 + encoder conformance + stereo/hybrid）:
  ```
  go vet ./...
  go test -count=1 ./...
  go test -count=1 -tags opusref ./...
  ```
- cgo は **PowerShell でのみ**実行（Bash tool では libopus がリンク不可）。
- magic-number マージンでの誤魔化しは不可。原理的な修正（libopus 忠実）を優先。効かない
  場合は「なぜ効かないか」を計測で示し、深追い（find_LPC_FLP ドメイン移植）が必要なら
  その旨を報告して止める（このタスクで大改修には踏み込まない）。

## やらないこと
- find_LPC_FLP のパイプライン順序再編（大改修）は**対象外**。本タスクは first-frame の
  scale/peak に絞った小修正。これで届かないと判明したら報告して終了。
- stereo/hybrid・FEC・閾値・docs は対象外。
- スコアボードで既に勝っているセルのチューニングは対象外。
