# リポジトリ全体 監査レポート

監査日: 2026-10-14
対象: `github.com/darui3018823/opus`
ブランチ: `fix/libopus-parity-audit`
HEAD: `175cd1f59942ab5e3c24b824136cf9ce166238b3`
describe: `v1.5.0-43-g175cd1f`
公開Version: `1.5.0`
比較対象: `./libopus` (libopus 1.6.1)
前回監査: [.claude/memory/audits/libopus-completeness-2026-07-19.md](.claude/memory/audits/libopus-completeness-2026-07-19.md)

---

## 概要

本レポートは、ルート `opus` パッケージ、内部コーデック・DSP パッケージ (`internal/*`)、Ogg コンテナパッケージ (`oggopus/`)、CI/ワークフロー (`.github/workflows/*`)、スクリプト群 (`scripts/*`)、およびドキュメント一式 (`README*.md`, `docs/*`, GoDoc コメント) を対象に、libopus 1.6.1 および RFC 6716 / RFC 3533 / RFC 7845 / RFC 8486 との整合性・境界ケースの安全性・ドキュメント整合性の観点から実施した監査結果である。

---

## 1. ルート `opus` パッケージ・内部コーデック (`internal/*`) のバグおよび境界条件の欠陥

### 1.1 `[High]` `SetExpertFrameDuration` で呼び出し元の `frameSize` より長いフレーム長を固定指定した際のスライス境界外パニック

- **該当箇所**:
  - Single-stream: [opus.go](opus.go) (`Encoder.Encode` L486, `Encoder.Encode24` L510, `Encoder.EncodeFloat32` L532, `Encoder.EncodeFloat64` L549, `Encoder.encodeCallerFrame` L571)
  - Multistream: [multistream.go](multistream.go) (`MultistreamEncoder.Encode` L399-L406, `Encode24` L417-L424, `EncodeFloat32` L435-L442, `EncodeFloat64` L449-L457)
  - Projection: [projection.go](projection.go) (`ProjectionEncoder.Encode*` L227-L303)
- **詳細**:
  1. 各 `Encode*` メソッドは最初に `len(pcm) < frameSize*e.channels` のみを検証し、その後に `selectEncodeFrameSize(frameSize)` ([opus.go](opus.go) L2964-L2970) で `selectedFrameSize` を決定している。`SetExpertFrameDuration(ExpertFrameDuration60ms)`（48 kHz で 2880 サンプル）を設定した状態で `frameSize = 960`（20 ms）、`len(pcm) == 960 * channels` を渡すと、`selectedFrameSize`（2880）に対する `len(pcm)` チェックや `selectedFrameSize > frameSize` の拒否が存在しないため、`pcm[:selectedFrameSize*e.channels]` で **slice bounds out of range パニック**が発生する（`libopus` の `opus_encode_native` は `variable_duration > frame_size` を `OPUS_BAD_ARG` として拒否する）。
  2. 逆に `selectedFrameSize < frameSize` の場合、`Encoder.Encode24` の GoDoc コメント ([opus.go](opus.go) L503-L504) には `pcm must contain at least frameSize*Channels() values unless a fixed expert duration selects a smaller prefix` と記載されているが、実装 ([opus.go](opus.go) L510) は無条件に `len(pcm) < frameSize*e.channels` で `ErrInvalidFrameSize` を返す。
  3. さらに `MultistreamEncoder.Encode*` ([multistream.go](multistream.go) L404, L422, L440, L457) および `ProjectionEncoder.encodeFloatDepth` ([projection.go](projection.go) L302) では、`encodeLibopus` を呼ぶ前に `pcm[:selectedFrameSize*e.channels]` へ切り詰めて渡しているため、`selectedFrameSize < frameSize` のときに `libopus` の `opus_multistream_encode_native` が行う `analysis_frame_size = frame_size` 分の Tonality Analysis が `selectedFrameSize` 分しか実行されない（Single-stream の `encodeCallerFrame` ([opus.go](opus.go) L567-L570) は `pcm[:frameSize*e.channels]` を `analysisPCM` として保持している）。

### 1.2 `[Medium]` `Reset()` における内部状態の復元漏れ（`Encoder`, `celt.Encoder`, `silk.Decoder`）

- **該当箇所**:
  - [opus.go](opus.go) (`Encoder.Reset` L3032-L3101)
  - [internal/celt/encoder.go](internal/celt/encoder.go) (`celt.Encoder.Reset` L1485-L1530)
  - [internal/silk/decoder.go](internal/silk/decoder.go) (`silk.Decoder.Reset` L2422-L2455)
- **詳細**:
  1. **`e.silkSampleRate` と `e.silkInputResamplers` の未復元**: 24 kHz / 48 kHz エンコーダでは `NewEncoder` ([opus.go](opus.go) L258) 時に `e.silkSampleRate = 16000` (`silkEncodeSampleRate(sampleRate)` L392) で初期化されるが、エンコード中に NB/MB が選択されると `rebuildSILKEncoder` ([opus.go](opus.go) L3103-L3121) によって `e.silkSampleRate` が `8000` または `12000` に書き換わる。`Encoder.Reset()` ([opus.go](opus.go) L3041) は `e.silkEncoder.Reset()` を呼ぶだけで `e.silkSampleRate` を初期値 `silkEncodeSampleRate(e.sampleRate)`（16000 Hz）へ戻さず、`silkInputResamplers` も再構築しない。また、`e.toMono` および `e.lbrrCoded` も `Reset()` でクリアされていない。
  2. **`celt.Encoder` の `disablePF` / `forceIntra` の残留**: モード遷移処理 ([encoder_transition.go](encoder_transition.go) L79, L124) は一時的に `celtEncoder.SetPrediction(0)` を呼び出して `disablePF = true` / `forceIntra = true` に変更する。正常終了時は `defer` で復元されるが、`celt.Encoder.Reset()` ([internal/celt/encoder.go](internal/celt/encoder.go) L1485-L1530) 自体は `disablePF` と `forceIntra` をリセットしていない。
  3. **`silk.Decoder` の `noLTPScaling` の残留**: `silk.Decoder.Reset()` ([internal/silk/decoder.go](internal/silk/decoder.go) L2422-L2455) で `d.noLTPScaling` (L212) が `false` にリセットされていない。

### 1.3 `[Medium]` `MultistreamPacketPad` / `MultistreamPacketUnpad` で `streams <= 0` のバリデーション欠落

- **該当箇所**:
  - [repacketizer.go](repacketizer.go) (`MultistreamPacketPad` L116-L141, `MultistreamPacketUnpad` L145-L175)
- **詳細**:
  - `MultistreamPacketPad(packet, streams, newLen)` に `streams <= 0` を渡すと、`for s := 0; s < streams-1; s++` が一度も回らず、`ErrBadArg` を返さずに単一ストリーム（`streams == 1`）としてパディングが成功してしまう（`libopus` の `opus_multistream_packet_pad` は `if (nb_streams < 1) return OPUS_BAD_ARG;`）。
  - `MultistreamPacketUnpad(packet, streams)` に `streams <= 0` を渡すと、`for s := 0; s < streams; s++` がスキップされ、不正な `packet` であってもエラーにならず `nil, nil` を返す。

### 1.4 `[Medium]` `Encoder.Bandwidth()` / `GetBandwidth()` がデフォルトの `ModePolicyLibopus` を無視する

- **該当箇所**: [opus.go](opus.go) (`Encoder.Bandwidth` L2865-L2885)
- **詳細**:
  - 現在 `Encoder` のデフォルトポリシーは `ModePolicyLibopus` ([opus.go](opus.go) L322) であり、実際のエンコード時には `e.libopusBandwidth` ([opus.go](opus.go) L1016) が更新される。
  - しかし `Encoder.Bandwidth()`（および `GetBandwidth()`）は `e.libopusModePolicy` や `e.libopusBandwidth` を一切参照せず、常に旧 Go 独自ポリシーのヘルパ (`shouldEncodeSILKOnly(FrameSize20ms)`, `shouldEncodeHybrid(FrameSize20ms)`, `selectCeltBandwidth()`) で計算した値を返す。

### 1.5 `[Low]` `SoftClipFloat32` における `NaN` 入力による状態メモリ `mem` の恒久汚染

- **該当箇所**: [softclip.go](softclip.go) (`SoftClipFloat32` L11-L114)
- **詳細**:
  - `pcm` に 1 サンプルでも `NaN` が含まれると、`v > 2` / `v < -2` ([softclip.go](softclip.go) L28-L32) はいずれも `false` となり、後続の二次曲線補間計算で状態変数 `a` が `NaN` になる。最後に `mem[c] = a` ([softclip.go](softclip.go) L111) で保存されるため、以降の全フレームで正常な PCM を渡しても該当チャンネルの出力がすべて `NaN` になり続ける。

### 1.6 `[Low]` デコーダ群（`ProjectionDecoder` / `AmbisonicsDecoder` / `SurroundDecoder`）の API 非対称性

- **該当箇所**:
  - [projection.go](projection.go) (`ProjectionDecoder` L351-L661, `AmbisonicsDecoder` L664-L748)
- **詳細**:
  - `ProjectionDecoder` には `DecodeFECWithDuration` / `DecodeFECFloat32WithDuration` はあるが、`Decoder` / `MultistreamDecoder` / `SurroundDecoder` にある 2 引数の `DecodeFEC(data, pcm)` / `DecodeFECFloat32(data, pcm)` が存在しない。また、`Bandwidth()`, `GetBandwidth()`, `GetLastPacketDuration()`, `SetPhaseInversionDisabled()`, `PhaseInversionDisabled()`, `Pitch()`, `Pitch48kHz()` も欠落している。
  - `AmbisonicsDecoder` には `SampleRate()`, `Channels()`, `Streams()`, `CoupledStreams()`, `SetGain()`, `Gain()`, `DecodePLC*`, `DecodeFEC*`, `GetLastPacketDuration()`, `Bandwidth()` が提供されていない。

### 1.7 `[Low]` `internal/resampler.Resampler.Process` における端数サンプルの暗黙切り捨て

- **該当箇所**: [internal/resampler/resampler.go](internal/resampler/resampler.go) (`Resampler.Process` L210-L215)
- **詳細**:
  - `inputLen := len(input) / r.numChannels` としており、`len(input)` が `r.numChannels` の倍数でない場合に末尾の端数サンプルがエラーなしに無視される。

---

## 2. `oggopus/` コンテナパッケージのバグおよび RFC 違反

### 2.1 `[High]` 完了済みパケットがあるページに後続の継続パケット（255 セグメント到達）が乗ると `GranulePosition` が `-1` に上書きされる（RFC 3533 / RFC 7845 違反・自己非互換）

- **該当箇所**: [oggopus/packet.go](oggopus/packet.go) (`PacketWriter.flush` L355-L360)
- **詳細**:
  - 同一 Ogg ページ上で 1 つ以上のパケットが完了した状態（`w.pageGranule >= 0`）で、同じページの残りセグメントに次の大きなパケットの先頭部分が詰め込まれ、セグメント数が上限 255 に達して最後のセグメント長が `255` になった場合、[oggopus/packet.go](oggopus/packet.go) L358-L360 の処理により `granule` が `-1`（`0xFFFFFFFFFFFFFFFF`）へ強制上書きされる。
    ```go
    granule := w.pageGranule
    if w.segments[len(w.segments)-1] == 255 && !eos {
        granule = -1
    }
    ```
  - RFC 3533 §6 および RFC 7845 §4 では、ページヘッダの Granule Position は「そのページ上で完了した最後のパケットの Granule Position」を記録しなければならず、ページ上で 1 つもパケットが完了しなかった場合にのみ `-1` と定められている。
  - さらに、この `PacketWriter`（および `oggopus.Writer`）が出力したストリームを同じパッケージの `oggopus.Reader` ([oggopus/stream.go](oggopus/stream.go) L246-L248) で読み込むと、完了したパケットに対して `pkt.LastPacketOnPage == true` かつ `pkt.PageGranule == -1` となるため、`ErrInvalidOpusStream: completed audio page has no granule position` で**自分自身の出力を拒否**する。
  - **修正方針**: `if !w.hasCompletedOnPage && w.segments[len(w.segments)-1] == 255 && !eos`（ページ上で完了したパケットが無い場合のみ `-1`）とする。

### 2.2 `[High]` `oggopus` が RFC 8486 Mapping Family 3 に未対応、かつ Mapping Family 2 のチャンネル数検証が欠落

- **該当箇所**: [oggopus/metadata.go](oggopus/metadata.go) (`ParseHead` L42-L90, `Head.Validate` L92-L134, `Head.MarshalBinary` L136-L165)
- **詳細**:
  - ルート `opus` パッケージは Mapping Family 2 および 3（`ProjectionEncoder` / `ProjectionDecoder` / `AmbisonicsDecoder` / `SurroundEncoder`）をサポートしているが、`oggopus` の `Head` は `MappingFamily != 0` のすべてを「長さ `Channels` バイトの `ChannelMapping` テーブル（合計 `21 + Channels` バイト）」として処理している。
  - RFC 8486 §3 に規定された Mapping Family 3 の `OpusHead` は `ChannelMapping` テーブルの代わりに `2 * (StreamCount + CoupledCount) * Channels` バイトの Demixing Matrix を持つため、`oggopus.ParseHead` は有効な Mapping Family 3 ストリームをすべて `ErrInvalidHead` で拒否し、`Writer` でも書き出せない。
  - また、Mapping Family 2（Ambisonics）についても `Head.Validate()` でチャンネル数が `(N+1)^2` または `(N+1)^2 + 2` であることの検証が行われていない。

### 2.3 `[Medium]` 複数ページのストリームで `EOS GranulePosition < PreSkip` の不正ストリームが検出されない

- **該当箇所**: [oggopus/stream.go](oggopus/stream.go) (`Reader.readAudioPage` L255-L267)
- **詳細**:
  - [oggopus/stream.go](oggopus/stream.go) L255-L267 の条件分岐が以下のようになっている。
    ```go
    if r.haveAudioGranule {
        ...
    } else if last.EOS {
        if granule < int64(r.Head.PreSkip) {
            return ErrInvalidOpusStream
        }
    }
    ```
  - `else if last.EOS` が `if r.haveAudioGranule` の `else` 節に繋がっているため、**2 ページ以上ある音声ストリーム**では最終 EOS ページ到達時にすでに `r.haveAudioGranule == true` となっており、`granule < int64(r.Head.PreSkip)` の検証がスキップされる（RFC 7845 §4 違反の再生サンプル数 0 未満のストリームが `NextPacket()` でエラーにならず、`SeekPCM(0)` を呼んだ際に [oggopus/seek.go](oggopus/seek.go) L49-L51 で `ErrSeekOutOfRange` という不整合なエラーになる）。

### 2.4 `[Medium]` `SeekPCM` 実行後に空 EOS ページの Granule Position 検証が無効化される

- **該当箇所**: [oggopus/seek.go](oggopus/seek.go) (`Reader.SeekPCM` L42-L48), [oggopus/stream.go](oggopus/stream.go) (`Reader.validateLogicalEOS` L163-L173)
- **詳細**:
  - `SeekPCM` はスキャン時に `r.haveLinkEnd = true` を設定する。一方、`validateLogicalEOS()` は先頭 ([oggopus/stream.go](oggopus/stream.go) L163-L165) で `if r.haveLinkEnd { return nil }` と即座に返るため、一度でも `SeekPCM` を呼んだ後は、終端の空 EOS ページが先行ページの Granule Position と異なる不正な値を持っていても ([oggopus/stream.go](oggopus/stream.go) L169 の `r.packets.eosGranule != r.previousPageGranule`) 検出されなくなる。

### 2.5 `[Medium]` `bisectPageAtOrBefore` が継続ページ（`GranulePosition == -1`）を常に `target` より後ろと誤判定する

- **該当箇所**: [oggopus/seek.go](oggopus/seek.go) (`bisectPageAtOrBefore` L174-L179)
- **詳細**:
  - `SeekPCM` の二分探索中、`scanNextPage` が巨大パケット（> 64 KB）の途中ページ（`page.GranulePosition == -1`）に当たると、`page.GranulePosition >= 0 && page.GranulePosition <= target` が `false` となり `high = offset`（前半探索）へ進んでしまう。その結果、その巨大パケットの完了ページが `<= target` であっても後半区間が切り捨てられ、不正確なシーク位置を返す。

### 2.6 `[Medium]` `scanNextPage` が `io.Reader.Read` の Short Read 境界をまたぐ `"OggS"` を見落とす可能性

- **該当箇所**: [oggopus/seek.go](oggopus/seek.go) (`scanNextPage` L215-L248)
- **詳細**:
  - `n, err := rs.Read(buffer[:readSize])` は単一の `Read` 呼び出しであり、基礎となる `io.ReadSeeker` が `n < 4` バイトの short read（かつ `err == nil`）を返したときに `"OggS"` がチャンク境界をまたぐと、`overlap`（3 バイト）の引き継ぎ計算（`from += int64(max(n-overlap, 1))`）により `"OggS"` の先頭バイトを飛び越えてしまう。

### 2.7 `[Low]` 音声パケットを 1 つも書かずに `Writer.Close()` を呼ぶと不正な Ogg Opus ストリームが出力される

- **該当箇所**: [oggopus/stream.go](oggopus/stream.go) (`NewWriterWithGranule` L359-L365), [oggopus/packet.go](oggopus/packet.go) (`PacketWriter.Close` L324-L339)
- **詳細**:
  - `NewWriter` で `OpusHead` と `OpusTags` を書き出した時点で `w.packets.haveCompletedPacket = true` になるため、音声パケットを 1 つも書かずに `Writer.Close()` を呼ぶと空の EOS ページ（Granule 0）が出力される。このストリームは音声パケットを含まないため RFC 7845 に違反し、`oggopus.Reader` で読むと `ErrInvalidOpusStream` になる。

### 2.8 `[Low]` `PacketReader.readPage` の `LastPacketOnPage` 判定と `r.partial` の無制限メモリ伸長

- **該当箇所**: [oggopus/packet.go](oggopus/packet.go) (`PacketReader.readPage` L169-L185), [oggopus/stream.go](oggopus/stream.go) (`Reader.readLinkHeaders` L82-L96)
- **詳細**:
  1. `LastPacketOnPage: i == len(completed)-1` は、同じページの末尾に未完了の次パケット断片（`len(r.partial) > 0`）が残っている場合でも `true` になる。そのため、`OpusHead` や `OpusTags` のページの末尾セグメントに後続パケットが混入していても `readLinkHeaders` ([oggopus/stream.go](oggopus/stream.go) L84) を通過する。
  2. 悪意ある入力ストリームがセグメント値 `255` のみの継続ページを送り続けると、`r.partial = append(r.partial, segment...)` ([oggopus/packet.go](oggopus/packet.go) L169) が上限なく膨張し、OOM（メモリ枯渇）を引き起こす。

---

## 3. CI / GitHub Actions・スクリプト・リポジトリ衛生状態の問題

### 3.1 `[High]` 全 7 個の GitHub Actions ワークフローが存在しない `@v7` タグを指定している

- **該当箇所**:
  - [.github/workflows/ci.yml](.github/workflows/ci.yml) (L15-L21)
  - [.github/workflows/nightly.yml](.github/workflows/nightly.yml) (L22-L28)
  - [.github/workflows/opusref.yml](.github/workflows/opusref.yml) (L15-L21)
  - [.github/workflows/fuzz.yml](.github/workflows/fuzz.yml) (L20-L53)
  - [.github/workflows/bench.yml](.github/workflows/bench.yml) (L18-L24)
  - [.github/workflows/release.yml](.github/workflows/release.yml) (L15-L21)
  - [.github/workflows/compat.yml](.github/workflows/compat.yml) (L18-L24)
- **詳細**:
  - すべてのワークフローで `actions/checkout@v7`、`actions/setup-go@v7`、`actions/upload-artifact@v7` が指定されているが、GitHub 公式アクションに `@v7` は存在しないため、GitHub Actions 上でジョブ開始時にアクション解決エラーで失敗する。

### 3.2 `[Medium]` `opusref.yml` および `scripts/ci_test.*` で最新の libopus 1.6.1 パリティテストが実行されない

- **該当箇所**:
  - [.github/workflows/opusref.yml](.github/workflows/opusref.yml) (L62)
  - [scripts/ci_test.ps1](scripts/ci_test.ps1) (L31), [scripts/ci_test.sh](scripts/ci_test.sh) (L34), [scripts/ci_test.bat](scripts/ci_test.bat) (L27)
- **詳細**:
  - `opusref.yml` は `libopus 1.6.1` をソースからビルドしているにもかかわらず、`go test -tags opusref ./...` のみを実行しており、`//go:build opusref && opus16` タグを持つ [cgo_ref_libopus16_test.go](cgo_ref_libopus16_test.go) が CI で一度も実行されない。
  - ローカル CI スクリプト（`scripts/ci_test.*`）は `-run TestCGORef` でテスト名をフィルタしているため、`TestLibopus*`、`TestMappingMatrix*`、`TestProjection*` などの cgo リファレンステストがスキップされる。

### 3.3 `[Low]` リポジトリ内の残骸・ハードコードパス

- **該当箇所**:
  - [scripts/oracle/build_encoder.ps1](scripts/oracle/build_encoder.ps1) (L4): ローカルユーザー固有の絶対パス `C:\Users\daruks\AppData\Local\Microsoft\WinGet\Packages\zig.zig_Microsoft.Winget.Source_8wekyb3d8bbwe\zig-x86_64-windows-0.15.2\zig.exe` がハードコードされている。
  - [.gitignore](.gitignore): `__pycache__/` や `*.pyc` が含まれておらず、`scripts/oracle/__pycache__/` がワークツリーに残っている。また `.claude/worktrees/` 配下にサブエージェント用の git worktree が残存している。

---

## 4. GoDoc コメントおよびドキュメント群の実装乖離・矛盾

### 4.1 `[Medium]` GoDoc コメントと実際の挙動の矛盾

1. **`SetLSBDepth` / `DefaultLSBDepth` が「未使用（予約済み）」と誤記されている**:
   - [opus.go](opus.go) L2417-L2419 および [constants.go](constants.go) L106-L108（さらに [README.md](README.md) L219-L220, [README_ja.md](README_ja.md) L199-L200）では「現在のエンコードパイプラインでは `lsbDepth` を使用していない」と記載されているが、実際には `ModePolicyLibopus` ([opus.go](opus.go) L960, L1528) および CELT エンコーダ ([internal/celt/encoder.go](internal/celt/encoder.go) L631, [internal/celt/alloc.go](internal/celt/alloc.go) L420) でアクティブに使用されている。
2. **`SetBitrate` のクランプ範囲の誤記**:
   - [opus.go](opus.go) L2510-L2513 および [constants.go](constants.go) L122-L123 では有効範囲を `[6000, 510000]` と記載しているが、実装 ([opus.go](opus.go) L2525-L2526) は `libopus` 準拠の `[500, 750000*channels]` にクランプしている。
3. **`Decoder.FinalRange()` の GoDoc コメントの誤り**:
   - [opus.go](opus.go) L5679-L5681 では「複数フレームのパケットでは各フレームの FinalRange を XOR した値を返す」と記載されているが、これは `MultistreamDecoder.FinalRange()` の仕様であり、単一ストリームの `Decoder.FinalRange()` は `libopus` と同じく**最後の構成フレームの FinalRange** を上書き保持する ([opus.go](opus.go) L3496, L3601, L4127)。
4. **`DecodeFECWithDuration` の上限記述の誤り**:
   - [opus.go](opus.go) L5309 のコメントに `"through 120 ms"` と残っているが、LBRR が存在しない場合の PLC フォールバックでは 120 ms を超える `frameSize`（2.5 ms の倍数、最大 `math.MaxInt32`）も受け付ける。

### 4.2 `[Low]` ドキュメントファイル間の矛盾・更新漏れ

- **[README.md](README.md) / [README_ja.md](README_ja.md)**:
  - [README.md](README.md) L114-L116 / [README_ja.md](README_ja.md) L106-L108: `DecodePLC` が最大 120 ms までと記載されているが、Single-stream `Decoder` は 120 ms 超の 2.5 ms 倍数フレーム長をサポートしている（一方 `MultistreamDecoder` は 120 ms にクランプ）。
  - [README.md](README.md) L301 / [README_ja.md](README_ja.md) L281 / [docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md) L128 / [docs/LIBOPUS_SCOPE.md](docs/LIBOPUS_SCOPE.md) L45: `SurroundEncoder` / `SurroundDecoder` が Mapping Family `1` と `255` のみ対応と記載されているが、実際には Mapping Family `2` もサポートしている ([surround.go](surround.go) L65)。
- **[docs/CTL_PARITY.md](docs/CTL_PARITY.md)**:
  - [docs/CTL_PARITY.md](docs/CTL_PARITY.md) L109: `ProjectionEncoder` / `ProjectionDecoder` が実装済みであるにもかかわらず、ステータスが `Partial`（「Demixing-matrix helper のみ実装」）のままになっている。
- **[docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md)**:
  - ファイル先頭 (L13) では「`NewEncoder` のデフォルトは `ModePolicyLibopus`」と正しく記載されている一方、後半の L1096 では「デフォルトは Go モード・レートポリシーのまま」と矛盾する古い記述が残っている。
  - L1480 で「`cmd_diag/main.go` は `cmd_diag/toccheck/main.go` に移動された」と記載されているが、実際には [cmd_diag/main.go](cmd_diag/main.go) と [cmd_diag/toccheck/main.go](cmd_diag/toccheck/main.go) の両方が存在している。

---

## 検証結果 (2026-10-14, Claude Code)

各指摘を `./libopus` (1.6.1) の原典、再現テスト、修正前後の比較で検証した。
修正は PR #31 (v1.6.0) に含まれ、回帰テストは修正前に失敗することを確認済み。

| # | 判定 | 根拠 / 対応 |
|---|---|---|
| 1.1 | 誤検知 | `selectEncodeFrameSize` が `frameSize < selected` を `ErrBadArg` で拒否する (single-stream / multistream とも再現で確認)。GoDoc の「smaller prefix」は selected < frameSize の場合で実装と一致 |
| 1.2 | 再現せず | NB→FB 使用後に `Reset` した encoder と新規 encoder の出力が VOIP/AUDIO とも 30 packet byte 一致 |
| 1.3 | 誤検知 | libopus 1.6.1 の `opus_multistream_packet_pad` / `_unpad` (repacketizer.c:392-468) に `nb_streams < 1` チェックは無い。streams=0 で pad は単一パケット扱い、unpad は長さ 0 を返し、Go と同じ |
| 1.4 | 仕様 | `Bandwidth()` は PCM 解析前の policy 選択値を返すと文書化済み (docs/CURRENT_IMPLEMENTATION.md の監査節) |
| 1.5 | 再現せず | NaN 入力後も `mem` は 0 のまま、次フレーム出力は正常 |
| 1.6 | 保留 | API 追加の要望。バグではない |
| 1.7 | 保留 | 内部 API で呼び出し側は常にチャンネル倍数を渡す |
| 2.1 | **実バグ・修正** | 1275 byte × 100 packet の普通のストリームでも Reader が自身の出力を拒否していた (v1.5.0 以前から)。`c20d3b4` |
| 2.2 | 対応 | family 3 demixing matrix (`Head.DemixingMatrix`) と family 2/3 の Ambisonics チャンネル数検証を追加。`44e33a0` |
| 2.3 | 実害なし | 修正前も `end trim exceeds decoded audio` で拒否されていた。挙動を守るテストのみ追加。`5020c99` |
| 2.4 | 保留 | SeekPCM 後は `findLogicalStreamEnd` が終端を確定済み。追加検証は未実施 |
| 2.5 | 誤検知 (性能のみ) | 二分探索後の線形スキャンが granule -1 のページを読み飛ばして補正するため結果は正しい |
| 2.6 | **実バグ・修正** | 1 byte ずつ返す ReadSeeker で SeekPCM が EOF になっていた。`io.ReadFull` に変更。`d4a8937` |
| 2.7 | 対応 | 音声 0 packet での `Writer.Close` を `ErrInvalidOpusStream` で拒否。`2dc4579` |
| 2.8 (1) | 保留 | granule は「ページ上で完了した最後の packet」に付くので現行の意味は正しい |
| 2.8 (2) | 対応 | `MaxPacketSize` (16 MiB) を超える再構成を `ErrPacketTooLarge` で拒否。`e2027c0` |
| 3.1 | 誤検知 | `@v7` で CI は全 job 成功している |
| 3.2 | 対応 | opus16 タグのテストを vendored libopus 1.6.1 の static build で実行する step を追加。`b8812e7`。ファイル名 `cgo_ref_libopus16_test.go` は実在しない (実体は `decoder_output_opusref_test.go`) |
| 3.3 | 誤検知 | `scripts/oracle/*.ps1` にユーザー固有パスは無い。`__pycache__/` は `.gitignore` 済みで存在しない |
| 4.1 / 4.2 | 大半は正しい・修正 | LSB depth・SetBitrate 範囲・`Decoder.FinalRange`・PLC 120 ms 記述・既定 policy・CTL_PARITY projection 行を修正。`6e95012`。Surround が family 1/255 のみという指摘は README に該当記述が無く誤検知 |
