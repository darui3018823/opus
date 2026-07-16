# タスク: Opus encoder の mode transition polish

## リポジトリ / 環境
- repo: `github.com/darui3018823/opus` (pure-Go Opus codec, working dir = リポジトリroot)
- branch: `dev/silk-encoder-phase4` (このブランチ上で作業)
- libopus FLP 参照ソース: `$env:TEMP\opussrc\opus-1.5.2\` (oracle が DL 済。`opus-1.5.2\src\opus_encode.c` の `opus_encode_native` を熟読すること)
- **重要 — CGO/参照比較は PowerShell でのみ動く** (Bash tool では libopus がリンクできない):
  `go test -tags opusref -run TestCGORef .`
  `go test -tags opusref -run TestCGOEncodeRef .`
  `go test -tags opusref -run TestOpusSILKABAgainstLibopusEncoder .`
- 通常テスト: `go test ./...` / `go build ./...` / `go vet ./...`

## ゴール
libopus `opus_encode_native` に忠実な mode transition smoothing を完成させる。
これは SILK encoder phase の Q7 cleanup で残った最後の項目。

## 現状 (済んでいること)
- **hybrid→CELT transition redundancy は実装・コミット済** (`382d4ba`)。
  - `Encoder.prevMode` (framing.Mode*, init -1) と `redundancyCelt` (専用 5ms/240@48k fullband CELT encoder) を追加済。
  - hybrid 連続中に CELT-only へ切替わるとき、その遷移パケットを **1 パケット分 hybrid のまま遅延保持** し、
    末尾に trailing 5ms の fullband CELT redundant frame (`celt_to_silk=0`) を付与、次パケットで実 CELT-only へ。
  - redundancy header (flag logp12 + celt_to_silk logp1 + uint(bytes-2,256)) を SILK と CELT main の間に書込み、
    CELT main を `maxBytes-redundancyBytes` に shrink、redundant frame を tail に append (frame 全体 maxBytes 維持=CBR packing 不変)。
  - `computeRedundancyBytes` = libopus `compute_redundancy_bytes` 移植済。
  - 検証済: 公式12/12緑, build/vet/`go test ./...`緑, **opusref 全PASS**。
    `TestCGOEncodeRefHybridRedundancyTransition` で libopus1.6.1 が遷移パケットを復号確認。

## やるべきこと (mode transition polish 本体)
**主目標 = CELT→SILK (および CELT→hybrid) transition redundancy (`celt_to_silk=1`) の実装。**
現状これは encoder/decoder 共に未対応で、遷移時の音は PLC 依存になっている。

libopus では mode が CELT-only → SILK/hybrid に切替わるとき、新しい SILK フレームの **先頭** に
redundant CELT frame (`celt_to_silk=1`) を置き、デコーダがその CELT 出力で SILK の立ち上がりを
クロスフェードして滑らかにする (hybrid→CELT の "trailing" とは前後逆の "leading" redundancy)。

具体的に Codex が調査・実装する範囲 (ファイル調査は自分でやってよい):
1. `opus.go` の encoder 側遷移ロジック (`encodeFloat`/`encodeHybridPacket`/`computeRedundancyBytes`/
   `redundancyEncoder`/`prevMode`/`canDeferToHybrid`) を読み、`opus_encode_native` の
   `redundancy`/`celt_to_silk`/`to_celt` 分岐と突き合わせる。CELT→SILK 方向の leading redundancy を追加。
2. decoder 側 (hybrid/SILK packet path、redundancy header の読取り) で `celt_to_silk=1` を解釈し、
   redundant CELT frame をデコードして先頭でクロスフェード合成する処理を実装。
   既に hybrid→CELT (trailing, celt_to_silk=0) の redundancy 復号は decoder にあるはずなので、その対称形。
3. libopus の redundancy header 配置 / bit 順 / `compute_redundancy_bytes` / overlap・prefill の扱いに忠実に。
   redundant frame は overlap 履歴なしの専用 reset 済 CELT encoder で符号化 (hybrid→CELT 実装に倣う)。

## 制約 / 受入条件 (regression を一切出さないこと)
- **公式 RFC8251 ベクタ 12/12 PASS 維持** (`go test` の official-vector suite, RMSE<0.001)。
- `go build ./...` / `go vet ./...` / `go test ./...` 全緑。
- **opusref 全 PASS 維持** (PowerShell で `go test -tags opusref ...`)。新規遷移について
  libopus1.6.1 が生成パケットを正しく復号することを確認する CGO テストを追加。
- 既存の `TestEncoderVoiceModeTransitionsStrict` / `TestEncoderHybridToCELTRedundancy` /
  `TestComputeRedundancyBytes` を壊さない (必要なら拡張)。
- conformance に関わる stereo/hybrid の trellis NSQ gating (`voicedUsesTrellis()` = mono SILK-only 限定) には触れない。
- 既知の baseline 失敗 `TestCGOEncodeRefHybridMultiFrameStrict` は無関係 (git stash で baseline 同一失敗を確認可)。

## 進め方
- 小さく no-op で入れる sub-slice 化は過去に何度も no-op になった教訓あり。CELT→SILK redundancy は
  encoder+decoder をセットで完全移植し、CGO 往復テストで end-to-end 検証する。
- 不明点や設計判断は実装前に簡潔に質問してよい (指示塔=Claude が承認する)。
- 完了時は変更点・追加テスト・opusref 結果を要約して報告すること。
