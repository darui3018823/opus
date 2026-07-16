# Codex Task: Phase B — CTL/API parity matrix + 安い穴埋め

親プラン: `.claude/plans/post-audit-2026-07-15.md` Phase B。
ブランチ: `codex/ctl-parity` を main (merge `7b899dd` 以降) から切ること。
役割: Codex = 実装。判定に迷う点はこのファイルの Claude 向け質問欄に書いて止めるより、
まず本文の判断基準に従うこと。

## ゴール (2本立て)

1. **`docs/CTL_PARITY.md` 新規作成**: libopus 1.6.1 の全 CTL を列挙し、
   本ライブラリの対応状況を機械的に分類した表。監査レポートの
   「libopus を名乗るなら未対応 CTL 一覧を明示すべき」への直接回答。
2. **安い API 穴埋め**: 数時間級で終わるものだけ実装 (下記 B-2)。
   重いもの・設計判断が要るものはやらずに matrix に「未対応」と書くだけでよい。

## B-1: CTL parity matrix

ソース: MSYS2 でインストール済みの libopus 1.6.1 ヘッダを使う
(`opus_defines.h` / `opus_multistream.h` / `opus_projection.h`。
場所は `pkg-config --cflags opus` か MSYS2 の include ディレクトリから特定)。

表の形式 (Markdown table):

| libopus CTL | 区分 | Go API | 状態 | 備考 |
- 区分 = encoder / decoder / multistream / projection / generic
- 状態は 3 値: **対応** (対応する Go API 名を明記) / **未対応** / **対象外**
- 対象外の基準: DRED/QEXT/OSCE 等の未実装 codec 機能に属する CTL、
  fixed-point 専用、deprecated。対象外には理由を備考に一言書く。
- GET/SET はペアで 1 行にせず別行にする (片方だけ実装済みのケースがあるため)。

既存の公開 API 一覧の起点 (2026-07-15 時点、Claude が grep 済み):
Encoder: SetForceChannels / SetLSBDepth / SetPredictionDisabled / SetPhaseInversionDisabled /
SetBitrate / SetComplexity / SetVBR / SetVBRConstraint / SetPacketPadding / SetDTX /
SetInbandFEC / SetPacketLossPerc / SetApplication / SetSignalType / SetMaxBandwidth /
SetBandwidth (+対応する getter 群は要確認)。
Decoder: GetLastPacketDuration / SetGain / SetPhaseInversionDisabled。
これ以外にも package-level 関数 (PacketGetMode 等) や multistream/projection の
method があるので、`gh grep` でなく実コードを網羅的に走査すること。

## B-2: 安い実装 (それぞれ独立 commit)

優先順。各項目、実装 + unit テスト + matrix の該当行を「対応」に更新、で 1 commit。

1. **`Decoder.GetBandwidth()`** (`OPUS_GET_BANDWIDTH` 相当):
   直近に decode した packet の帯域を返す。`lastPacketConfig` が Phase A で
   既に保存されている (opus.go) ので、config→bandwidth 変換だけ。
   packet 未受信時は libopus 同様のエラー値挙動を Go らしく (error 返却) 設計。
   `Encoder.GetBandwidth()` も encoder の確定帯域から返せるなら併せて。
2. **packet-has-LBRR helper** (`OPUS_PACKET_HAS_LBRR` 相当):
   package-level `PacketHasLBRR(data []byte) (bool, error)`。
   SILK ヘッダの LBRR flag 検査は DecodeFEC の inspect 経路に既にあるので流用。
3. **`PCMSoftClip`** (`opus_pcm_soft_clip` 相当):
   libopus `opus_pcm_soft_clip_impl` (opus.c) を float64/float32 向けに忠実移植。
   package-level 関数 + declip memory を持つ薄い状態型のどちらが良いかは
   libopus API 形状 (softclip_mem[channels]) に合わせて判断。
   libopus と同一入力での数値比較テストを opusref タグで追加すると強い (任意)。
4. **multistream packet pad/unpad** (`opus_multistream_packet_pad` / `_unpad` 相当):
   repacketizer 基盤があるので薄い。libopus の multistream padding 仕様
   (最終 stream にのみ padding を付ける) に注意。
5. **expert frame duration** (`OPUS_SET_EXPERT_FRAME_DURATION` 相当):
   encoder framing 制御の公開。**他より重いと判断したら実装せず matrix に
   未対応で記載して skip してよい** (判断根拠を PR 説明に書く)。

## B-3: docs 連携

- `docs/CURRENT_IMPLEMENTATION.md` の API 節から `docs/CTL_PARITY.md` へリンク。
- README / README_ja の「libopus との互換性」相当の節に 1 行で matrix への参照を追加。
- DRED/QEXT は「packet transport のみ対応、codec/DSP は対象外」と CTL_PARITY.md の
  冒頭に明記する (親プラン「見送り確定」の docs 落とし込み)。

## ガードレール

- RC (range coder)・encoder 品質経路・mode 判定には触らない。純追加のみ。
- 既存 API の挙動変更禁止。既存テストが 1 本でも赤くなったら手を止めて原因を書く。
- 検証は PowerShell で: `go vet ./...` / `go test -count=1 ./...` /
  `go test -count=1 -tags opusref ./...` (CGO は PowerShell でしか動かない)。
- コミットは項目ごとに分割。message は既存の conventional 形式
  (`feat(api): ...` / `docs: ...`)。

## 完了条件

- `docs/CTL_PARITY.md` が存在し、libopus 1.6.1 の CTL が漏れなく分類されている。
- B-2 の 1〜4 (5 は任意) が実装され、それぞれ unit テストを持つ。
- 全テスト green (通常 + opusref)。作業ツリークリーン、未 push でよい
  (push/PR は Claude 側で検分後に実施)。

## 報告フォーマット

完了時に以下を報告: ブランチ名 / commit 一覧 / matrix の集計
(対応 N / 未対応 N / 対象外 N) / skip した項目と理由 / 検証結果。
