# libopus完成度 再監査レポート（第2回）

監査日: 2026-06-23  
対象: `github.com/darui3018823/opus`  
ブランチ: `dev/silk-burg-a2nlsf`  
HEAD: `572d09d feat(projection): add ambisonics APIs`  
比較対象: 同梱libopus 1.6.1  
前回監査: `.claude/memory/audits/libopus-completeness-2026-06-20.md`

## 結論

libopus代替としての推定総合完成度は、前回の約68%から
**約86%**へ上昇した。

この3日間で、前回P0は全件、P1は実質全件が解消された。さらに、
当時P2/P3に分類していたmultistream、surround、projection、
Ogg Opus、24-bit PCM、packet extensionsまで実装されている。

追加差分はmain比で次の規模だった。

- 63ファイル変更
- 10,632行追加
- 365行削除

コード量だけでなく、通常テスト、race、libopus相互運用、公式ベクタ、
短時間fuzz、coverage、benchmarkの実行結果も良好だった。

ただし、「APIが存在する」ことと「libopus相当の品質・制御・障害時挙動」
は分けて評価する必要がある。現在の残課題はAPI開放よりも、
SILK/Hybrid/FEC/PLC品質、モード・レート制御、最新libopus機能、
新規パーサの堅牢性、CIと文書の追随に移っている。

## 再採点

| 領域 | 前回 | 今回 | 判定 |
|---|---:|---:|---|
| 単一ストリームデコーダ | 93% | 95% | 公式ベクタ継続合格、実FEC追加 |
| CELTエンコーダ | 78% | 84% | 2.5/5/10 ms、CTL、性能改善 |
| SILK/Hybridエンコーダ | 58% | 72% | stereo/hybrid LBRRとモード範囲拡張 |
| 単一ストリーム公開API | 45% | 90% | 前回P0/P1をほぼ全解消 |
| Multistream/Surround/Projection | 0～限定 | 83% | APIとlibopus相互運用あり |
| Packet/Repacketizer/Extensions | 限定 | 88% | 公開APIとoracle比較あり |
| Ogg Opus | 0% | 86% | page/packet/header/tag/streamを実装 |
| テスト・堅牢性 | 88% | 92% | 422テスト、race、opusref、fuzz |
| 実行性能 | 70% | 82% | 割当量と実行時間を大幅削減 |
| 総合 | 約68% | **約86%** | 高品質なPure Go Opus実装段階 |

## 前回P0の解消状況

### P0.1 小さい出力バッファでDecoder状態を進めない

**解消済み。**

`Decode`と`Decode24`はパケットを事前検査し、必要な出力長を確認してから
実復号へ進む。状態を持つ連続パケットで、失敗後の再試行を確認する
回帰テストも追加されている。

### P0.2 PLCとFECの分離

**解消済み。**

- `DecodePLC(pcm, frameSize)`を公開
- `DecodeFEC(data, pcm)`はSILK LBRRを実際に復号
- CELT-only FEC要求は`ErrUnimplemented`
- PLCは現在CELT-only対応

API名と動作の重大な不一致は解消した。

### P0.3 Application検証

**解消済み。**

- `NewEncoder`は不正applicationを`ErrBadArg`で拒否
- `SetApplication`は`error`を返し、不正値では状態を変更しない

### P0.4 Version整合

**解消済み。**

- リポジトリ直下`VERSION`
- `go generate ./...`
- 生成された`version_gen.go`
- CIで生成差分を検査

現在の公開Versionは`1.1.1`でタグと一致する。

### P0.5 最大サイズ定数

**解消済み。**

- `FrameSize120ms`
- `MaxFrameSize = 5760`
- `MaxFrameBytes = 1275`
- `MaxPacketFrames = 48`
- 保守的な`MaxPacketSize`

フレーム、フレームバイト、パケットの意味が分離された。

## 前回P1の解消状況

### 2.5/5/10 msエンコード

**解消済み。**

全サンプルレート・mono/stereoの通常テストがあり、
libopusが生成パケットを復号する`opusref`テストも通過した。

### Mono SILK LBRR/FEC復号

**解消済み。**

20/40/60 msの復号が追加された。さらに前回範囲を超えて、
stereo SILKとmono/stereo hybrid LBRRも実装された。

### `BitrateAuto` / `BitrateMax`

**解消済み。**

設定値と実効値を分ける`Bitrate` / `EffectiveBitrate`がある。

### libopus相当デフォルト

**設計を伴って解消済み。**

既存利用者の動作を壊さないため、`NewEncoder`はlegacy defaultを維持し、
`NewEncoderWithProfile(..., EncoderProfileLibopus)`で次を選択できる。

- automatic bitrate
- complexity 9
- constrained VBR

### float32 API

**解消済み。**

- `EncodeFloat32`
- `DecodeFloat32`
- multistream/projectionにもfloat32 APIあり

### Packet inspection

**解消済み。**

- config
- mode
- bandwidth
- channels
- frame count
- samples per frame
- total samples

### Sentinel error整合

**大幅改善。**

constructorや主要公開メソッドは`ErrBadArg`,
`ErrUnsupportedSampleRate`, `ErrUnsupportedChannels`,
`ErrBufferTooSmall`等をwrapする。

全公開経路の完全なerror taxonomy監査は今後も価値があるが、
前回の重大な不統一は改善されている。

## 前回P2/P3から実装された項目

### Common CTL / getter

次が公開された。

- sample rate / channels
- final range
- lookahead
- pitch
- decoder gain
- max bandwidth
- VBR constraint
- in-DTX
- force channels
- LSB depth
- prediction disabled
- phase inversion disabled

### Repacketizer / packet pad

実装済み。

- `Repacketizer.Cat`
- `Out`
- `OutRange`
- `PacketPad`
- `PacketUnpad`

### 性能

Bluestein FFT planを再利用し、前回benchmarkから大幅改善した。

今回のWindows amd64、Intel Core i7-11700、20 ms benchmark:

| Operation | 前回 | 今回 |
|---|---:|---:|
| Encode time | 約1.76 ms | 約0.35 ms |
| Decode time | 約2.17 ms | 約0.18 ms |
| Encode bytes/op | 約458 KB | 約229 KB |
| Decode bytes/op | 約422 KB | 約192 KB |
| Encode allocs/op | 48 | 30 |
| Decode allocs/op | 47 | 33 |

実時間性能は十分高い。割当量はまだ多いが、前回の約半分になった。

### Stereo/Hybrid LBRR

実装済み。mono/stereo SILK-onlyとhybridでLBRR encode/decode経路がある。

### Multistream

実装済み。

- self-delimited framing
- int16/int24/float32/float64
- aggregate bitrate
- per-stream access
- duplicate/silent mapping
- libopus双方向相互運用

### Surround

実装済み。

- mapping family 0
- mapping family 1、1～8 channels
- mapping family 255
- 5.1/6.1/7.1 LFE識別
- stream bitrate allocation

### Projection / Ambisonics

実装済み。

- RFC 8486 family 2
- family 3 projection matrix
- mapping matrix API
- predefined libopus matrices
- family 2/3 libopus相互運用

### Ogg Opus

`oggopus` packageとして実装済み。

- Ogg page parse/write
- CRC
- lacing/continued packet
- OpusHead
- OpusTags
- granule position
- BOS/EOS
- high-level Reader/Writer

### 24-bit PCM

single-stream、multistream、projectionで実装済み。

### Packet extensions

opaque extension parse/generate APIが追加され、
libopus oracleとの比較も通過した。

これはDRED/QEXTのbitstream運搬を扱うが、DRED/QEXT DSP自体を
実装したものではない。

## 実行した検証

すべて成功した。

```text
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go test -count=1 -tags opusref ./...
go test -count=1 -cover ./...
go generate ./...
git diff --exit-code
```

個別に次のlibopus相互運用もverbose実行した。

- official RFC 8251 vectors
- short CELT frames
- mono SILK FEC
- mono/stereo multistream
- projection family 2/3
- predefined projection matrices
- packet extensions
- Go DecodeFEC vs libopus

### Coverage

| Package | Statement coverage |
|---|---:|
| root `opus` | 82.0% |
| `internal/celt` | 83.0% |
| `internal/dsp` | 81.5% |
| `internal/entcode` | 79.2% |
| `internal/extensions` | 91.4% |
| `internal/resampler` | 88.4% |
| `internal/silk` | 80.0% |
| `oggopus` | 86.7% |

### Fuzz

短時間ローカル実行:

- `FuzzDecode`: 約33,000 executions、pass
- `FuzzDecodeFloat`: 約70,000 executions、pass

既存corpusが増えているため、baseline gathering時間を含む。

### Test volume

- `func Test...`: 約422
- fuzz target: 2
- tracked Go source: 約50,291 lines

## 重要な残課題

## P0

今回の監査で、即時の状態破壊や明白な公開API虚偽に相当する
新規P0は確認しなかった。

ただしリリース前には、以下のP1を必須扱いにするのが妥当。

## P1: FEC品質とテスト基準

FEC APIとgrammarは実装されているが、libopusとの復元比較は
ケースによりかなり弱い。

観測値:

- mono 20 ms: `+Inf dB`
- mono 40 ms: `33.80 dB`
- mono 60 ms: `1.72 dB`
- stereo SILK: `0.05 dB`
- mono hybrid: `28.62 dB`
- stereo hybrid: `0.00 dB`

現在のstereo/hybridテスト閾値は`-2 dB`であり、
主に「非無音で壊れず、後続状態が処理できる」ことを確認する水準。

したがって現状は:

- FEC bitstream interoperability: 実装済み
- FEC API: 実装済み
- libopus相当のFEC復元品質: 未達

推奨ラベル:

- `priority/P1`
- `area/fec-plc`
- `compat/libopus`
- `area/quality`

必要作業:

- stereo/hybrid FECを元信号およびlibopus出力と比較
- channel別SNR、mid/side energy、相関、clippingを計測
- 60 msで欠落LBRR slotを単純PLCに落とす挙動を改善
- 現在の緩い閾値を段階的に引き上げる
- FEC後の通常decode stateを全modeで連続検証

## P1: SILK/Hybrid PLC

公開`DecodePLC`はCELT-only対応で、SILK-only/hybridは
`ErrUnimplemented`。

音声通話用途ではFECが存在しないpacket lossも通常発生するため、
libopus代替を名乗る上で重要。

推奨ラベル:

- `priority/P1`
- `area/fec-plc`
- `area/decoder`
- `compat/libopus`

## P1: opusref CIの拡張

ローカル`go test -tags opusref ./...`は全成功したが、
`.github/workflows/opusref.yml`が実行するのは主に旧3項目:

- SILK AB
- SILK-only conformance
- official decoder vectors

新規の次のテストをCIに明示追加すべき。

- short frames
- mono/stereo/hybrid FEC
- multistream
- projection family 2/3
- packet extensions

またpush対象branchは`main`と`dev/silk-encoder-phase4`で、
現在の`dev/silk-burg-a2nlsf`を直接pushしても起動しない。
PRではmain向けなら起動する。

推奨ラベル:

- `priority/P1`
- `area/ci`
- `compat/libopus`

## P1: 文書の同期

`docs/CURRENT_IMPLEMENTATION.md`は内容の大半が更新されているが、
`Last reviewed: 2026-06-20`のままで、Known Gapsには
projection未実装との古い記載が残る。

README英語版にも次の古い記載が残る。

- mapping family 2 projection未実装
- projection/ambisonics未実装
- Ogg Opus未実装

実コードと矛盾している。

推奨ラベル:

- `priority/P1`
- `area/docs`

## P1: 新規パーサ群のfuzz

現在のfuzz targetはsingle-stream `Decode`と`DecodeFloat`の2つのみ。

追加対象:

- packet inspection
- repacketizer
- packet extensions
- multistream self-delimited split
- Ogg page reader
- Ogg packet reader
- OpusHead/Tags
- projection matrix bytes

新規10,000行超のうち、入力parserが多いため優先度は高い。

推奨ラベル:

- `priority/P1`
- `area/testing`
- `area/security`

## P2: Full SILK/Hybrid mode/rate/quality policy

モード範囲は広がったが、libopusの完全なmode/rate controllerではない。

残る差:

- voice-oriented routing
- simplified rate controller
- SILK NSQ/shapingの完全同等性
- voiced/onset packet size
- full stereo/surround psychoacoustic decisions
- hybridは基本20 ms geometry

推奨ラベル:

- `priority/P2`
- `area/encoder`
- `area/quality`
- `compat/libopus`

## P2: Multistream/Surround CTLとpsychoacoustic parity

core encode/decodeとmappingはあるが、libopus multistream CTL全体や、
surroundのenergy mask解析までは同等でない。

推奨ラベル:

- `priority/P2`
- `area/multistream`
- `area/surround`
- `compat/libopus`

## P2: 未公開の一般utility

libopus 1.6.1との比較で残る一般機能:

- packet has LBRR
- PCM soft clipping helper
- multistream packet pad/unpad
- expert frame duration CTL
- decoder current bandwidth getter
-一部multistream/projection CTL

Go APIとして必要性を評価して追加する。

## P2/P3: DRED/QEXT等

packet extensionの運搬は実装済みだが、次は未実装。

- DRED encoder/decoder
- DRED parse/process/decode
- QEXT DSP
- DNN blob
- OSCE BWE
- ignore-extensions CTL

libopus 1.6.1の最新機能まで含めた完全互換を目標にするならP2。
従来Opus core互換を目標にするならP3。

## P2: 追加性能改善

前回より大幅改善したが、1 packetあたり約192～229 KBは依然大きい。

次の候補:

- PCM変換buffer再利用
- multistream/projection temporary buffer pool
- append成長の事前容量確保
- long-lived stream benchmark
- channel数別alloc regression

## P3: Encoder bit-exact

相互運用に必須ではなく、引き続き低優先。

品質・rate・mode・FEC・PLCを先に揃えるべき。

## リリース判断

### 現在すでに強い用途

- Pure Go single-stream Opus decoder
- CELT中心の一般音声encode
- 限定条件下のSILK/hybrid voice encode
- multistream/surround encode/decode
- ambisonics/projection
- packet inspection/repacketization/extensions
- Ogg Opus page/packet/container処理

### 条件付き

- WebRTC/VoIP用途
  - FECは存在するがstereo/hybrid/60 ms品質差に注意
  - SILK/hybrid PLC未実装
- 大量同時stream
  - real-time速度は高いがalloc量を測定すべき

### まだ避けるべき主張

- libopus完全drop-in replacement
- libopusと同等の全encoder品質
- 全packet loss条件でlibopus同等
- libopus 1.6.1のDRED/QEXTを含む完全実装
- bit-exact encoder

## 次の推奨順序

1. FEC品質テストを厳格化し、stereo/hybrid/60 ms差を改善
2. SILK/hybrid PLC
3. 新規opusrefテストをCIへ追加
4. CURRENT_IMPLEMENTATIONとREADMEの古い未実装記載を修正
5. Ogg/extensions/multistream/projection fuzz target追加
6. full SILK/hybrid mode-rate-quality policy
7. multistream/surround CTL parity
8. packet-has-LBRR、soft clip等のutility
9. DRED/QEXT
10. さらなるallocation削減

## 最終判定

Agentの作業量と成果は非常に大きい。前回は「高品質なsingle-stream
decoderと発展途上encoder」だったが、現在は
**広いAPIと周辺機能を備えたPure Go Opusライブラリ**まで進んでいる。

今回確認できた範囲では、追加機能は単なるstubではなく、
通常テストとlibopus相互運用で動作している。

一方で、完成度の残り約14%は、小さなAPI数の問題ではない。
FEC/PLC、SILK/Hybrid品質、full mode/rate control、最新DRED系、
parser robustnessという、難度と実運用影響が高い部分である。

したがって今後は機能数を増やすより、品質閾値、障害時挙動、
長期CI、fuzz、実音声corpusで完成度を固める段階に入ったと判断する。
