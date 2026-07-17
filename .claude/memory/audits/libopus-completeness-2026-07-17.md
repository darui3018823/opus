# libopus完成度 再監査レポート（第4回）

監査日: 2026-07-17
対象: `github.com/darui3018823/opus`
ブランチ: `main`
HEAD: `c5f3d058ad2289918d424f4e2dc98ae1356bce9d`
describe: `v1.2.0-105-gc5f3d05`
公開Version: `1.2.0`
比較対象: libopus 1.6.1
前回監査: `.claude/memory/audits/libopus-completeness-2026-07-01.md`

## 結論

今回も完成度を2つに分けて評価する。

- Pure Go Opusライブラリとしての完成度: **約93%**
- libopus 1.6.1完全代替としての完成度: **約81%**

前回はそれぞれ約88% / 約74%だった。今回は採点軸を変えたのではなく、
前回P1だった複数の機能差が実際に解消されたため上方修正する。

特に大きいのは次の進展である。

- single-stream SILK-only / hybrid public PLC
- multistream / surround PLC・FEC
- expert frame duration 2.5–120 ms
- CTL/helper parityの明文化と主要API穴の実装
- Ogg Opus granule seek、pre-skip/end-trim、chained logical stream
- real-corpus matched-bitrate scoreboardとhybrid VBR allocation修正
- stateful decoder/encoder/Ogg fuzz、opusref differential fuzz
- 公開benchmark、allocation削減、release/documentation整備

現時点の正確な表現は次の通り。

> Pure Goで標準Opusをencode/decodeし、packet、multistream、surround、
> projection、Ogg Opus、PLC/FECまで扱うライブラリとしては完成域に近い。
> decoderと相互運用基盤は特に強い。一方、libopusの完全代替としては、
> encoderのmode/rate/quality policy、CELT music品質、surround解析、
> 最新extension DSP、性能・allocationにまだ重い差がある。

ここでの百分率はAPI個数の単純比ではない。実装範囲、相互運用、品質証拠、
堅牢性、残課題の難度を含む監査上の推定値である。

## 前回からの差分

前回監査HEAD `8142def` から今回HEAD `c5f3d05` まで:

- 94 commits
- 111 files changed
- 15,564行追加
- 1,384行削除
- source上の `func Test...`: 434 → 502
- source上の `func Fuzz...`: 6 → 10

主な差分:

- `DecodePLC` がCELT-onlyだけでなくSILK-only / hybridに対応
- `PacketHasLBRR`、`SoftClipFloat32`、multistream pad/unpad、各種getterを追加
- `docs/CTL_PARITY.md` にlibopus 1.6.1 parity matrixを追加
- `docs/MODE_RATE_POLICY_DIFF.md` にencoder policy差分を明文化
- `docs/REAL_CORPUS_SCOREBOARD.md` と実音声A/B harnessを追加
- hybrid VBR/CVBRがallocation前にshared entropy coderをshrinkするよう修正
- multistream/surroundにpublic PLC/FECを追加し、libopus LBRRと相互運用確認
- expert frame durationをsingle/multistream/surround/projectionへ伝播
- Ogg Opusにpacket timing、`SeekPCM`、chained stream readerを追加
- decoder/encoder operation-sequence fuzzとOgg Reader/Writer fuzzを追加
- public performance benchmarkと複数のSILK allocation削減を追加
- native OS/architecture CI、race/release/version/documentation guardを整備

## 再採点

| 領域 | 前回評価 | 今回評価 | 判定 |
|---|---:|---:|---|
| 単一ストリームデコーダ | 96% | **98%** | 公式ベクタ12/12、libopus参照、全mode PLC/FEC、stateful fuzz |
| CELTエンコーダ | 85% | **86%** | 標準packetと相互運用は強い。music corpus差と非bit-exact性が残る |
| SILK/Hybridエンコーダ | 78% | **84%** | speech corpus、transition、FEC、hybrid VBRが前進。policyは未完成 |
| 単一ストリーム公開API | 90% | **93%** | 主要CTL/helperが揃った。PLC/FEC semantics等に厳密差が残る |
| Multistream/Surround/Projection | 85% | **92%** | PLC/FEC、expert duration、相互運用が前進。surround解析等が残る |
| Packet/Repacketizer/Extensions | 91% | **95%** | core helperはほぼ揃う。DRED/QEXTはopaque transportのみ |
| Ogg Opus | 87% | **94%** | seek/chaining/timingを実装。multiplexed demux等が残る |
| テスト・堅牢性 | 94% | **98%** | 502 tests、10 fuzz targets、stateful/differential coverage |
| 実行性能 | 82% | **86%** | baselineと改善証拠あり。ただしSILK encodeの割当は依然大きい |
| 文書・release readiness | 未分離 | **95%** | API doc、examples、version/release/CI契約が整備済み。小さなstale記述あり |
| Pure Go Opusライブラリ総合 | 約88% | **約93%** | core利用範囲では完成域に近い |
| libopus完全代替総合 | 約74% | **約81%** | 前回P1を複数解消。厳密CTL/loss semanticsも保守的に反映 |

## 現在かなり強い領域

### 単一ストリームデコーダ

デコーダは引き続き最も強い領域である。

- RFC 8251公式ベクタ12/12合格
- libopus 1.6.1 reference suite合格
- CELT / SILK-only / hybrid decode
- mono/stereo、5つのOpus sample rate
- int16、signed 24-bit-in-int32、float32、float64 PCM
- final range、pitch、gain、bandwidth、phase inversion controls
- CELT / SILK-only / hybrid public PLC
- SILK-only / hybrid LBRR/FEC
- mode transition redundancyとcrossfade
- stateful decoder sequence fuzz
- opusref differential fuzzによるaccept/reject、duration、finite-output guard

前回の最大の穴だったpublic SILK/hybrid PLCは解消した。

ただしSILK PLCはlibopusとbit-exactではない。また、random accepted packetの
waveform/final-rangeまで常にlibopusと一致することはhard oracleにしていない。
公式ベクタ・mode別reference testと、random packetの構造安全性は分けて評価すべきである。

### Packet / Repacketizer / Extensions

libopus core helper相当はかなり揃った。

- packet config/mode/bandwidth/channels/frames/samples inspection
- `PacketHasLBRR`
- `SoftClipFloat32`
- repacketizer `Cat` / `NumFrames` / `Out` / `OutRange` / `Reset`
- single-stream / multistream pad・unpad
- 48 frames、120 ms、1275-byte frame limitのvalidation
- packet extension count/parse/generate
- libopus extension oracleとのrandomized byte一致

DRED ID 126とQEXT ID 124を含むpayload transportはできるが、DRED neural
recovery、QEXT DSP、DNN blob、OSCE BWEは実装していない。

### Multistream / Surround / Projection

multistreamはAppendix B self-delimited framing、PCM API variants、mapping、
per-stream state、aggregate bitrate、reset、final rangeに加え、今回PLC/FECと
expert durationまで揃った。libopus生成two-stream LBRRのFEC結果は、監査中の
fixtureでGo/libopus sample-identicalだった。

surroundはfamily 0/1/255、Vorbis channel order、LFE、duration-dependent rate
allocationを持つ。projection/AmbisonicsはRFC 8486 family 2/3、ACN/SN3D、
非音場stereo、first–fifth orderのlibopus matrixと双方向interopを持つ。

残る中心はfull surround psychoacoustic energy-mask analysis、全CTLのaggregate
convenience wrapper、arbitrary custom projection encoder matrix generationである。

### Ogg Opus

前回の「seek/chainedなし」は現行には当てはまらない。

- CRC-checked Ogg page read/write
- lacing、continued packet reconstruction
- OpusHead / OpusTags
- pre-skip / EOS end-trim metadata
- granule-position bisection `SeekPCM`
- RFC 7845 80 ms decoder pre-roll
- chained logical streamの自動継続
- linkごとのheader/tags/serial/index
- seek/chainingを含むReader/Writer stateful fuzz

各Writerは1 logical streamを出力する。Readerはchainingを処理するが、
multiplexed/interleaved logical-stream demux、physical chain全体のglobal index、
opusfile相当のdecode-to-PCM convenience APIは未実装である。

## 7/1以降の重要な前進

### Public PLC/FEC

single-stream PLCはCELT-only限定からSILK-only/hybridまで拡張された。
FEC後やdigital silence後のstateもhardeningされている。

multistream/surroundでは、全childをpreflightしてから状態を進めるため、
途中のchild failureで先行streamだけstateが進む問題を避けている。
FEC時にCELT elementary streamがPLCへfallbackする経路もテストされている。

制限として、public PLC/FEC convenience APIは主にint16で、float/24-bit版を
全familyに同じ粒度で揃えたAPIではない。さらに厳密なlibopus semanticsとの差がある。

- `DecodeFEC` はlost durationをcaller引数で受けず、次packetのdurationから推定する
- `DecodeFEC` は複数Opus frameをpackedしたpacketを現在拒否する
- `DecodePLC` は初回packet前を `ErrInvalidState` とし、libopusのzero concealmentと異なる
- `DecodePLC` の許容duration集合はlibopusのPLC frame-size semanticsより狭い
- PLC/FEC成功後にdecoder `FinalRange` を更新せず、直前値を保持する

したがって「single/multistreamの基本的なPLC/FEC経路がある」は正しいが、loss APIの
完全互換はまだ達成していない。

### CTL/API parity

`docs/CTL_PARITY.md` によって、曖昧だった「全CTL互換」の境界が明確になった。

core CTLの多くは個別のGo methodとして対応する。特に今回、expert frame
duration、packet LBRR、soft clip、current bandwidth、multistream padding等が
追加された。

一方、以下はpartialまたはout-of-scopeである。

- application/signal/VBR/CVBRのlibopus同等policy semantics
- 全generic CTLのaggregate multistream/projection wrapper
- `Lookahead` は現在のGo codec delayを返すが、libopus CTLの数値semanticsとは異なる
- `LSBDepth` は入力精度hintを保持するがcodec decisionへの反映は限定的
- PLC/FEC後の `FinalRange` semantics
- `OPUS_SET/GET_IGNORE_EXTENSIONS`
- DRED/QEXT/DNN/OSCE関連CTLとDSP

したがって「Goとして主要制御を持つ」は正しいが、「C API drop-in」ではない。

### Real-corpus scoreboardとhybrid VBR

実音声corpusのmatched-bitrate A/B harnessが追加された。2026-07-15の再実行記録は
140/140 cells成功で、5つのspeech-oriented classでは平均matched gapがほぼ0 dB、
byte totalも前回比1.004で、speech pathはかなり強い結果だった。

同時に、baselineで見つかったhybrid target overflowは、CELT allocation前に
shared entropy coderをshrinkする修正で解消された。6-frame final-range regressionと
libopus cross-decode guardがある。

ただしcorpusはlocal `testdata/` 依存でCI必須ではない。またsynthetic
stereo-chords musicでは24–64 kbpsで約+7.7～+9.7 dB、mixed low-bitrate cellでは
最大約+5.6 dBのlibopus advantageが記録されている。speech parityをencoder全体の
quality parityと読み替えてはいけない。

### Robustness / CI / release readiness

source上のtest/fuzz entry pointは502/10へ増えた。新規coverageには以下がある。

- stateful decoder sequence
- stateful encoder setter/input sequence
- Ogg Opus Writer-to-Reader、chaining、timing、seek
- local opusref decoder differential fuzz
- expert frame durationのlibopus cross-check
- multistream FEC interop
- hybrid CVBR final-range/interop

CI定義はLinux/macOS/Windows、amd64/arm64のnative matrix、nightly/manual fuzz、
Ubuntu opusref、race、generated-file driftへ拡張された。公開API documentation guard、
executable examples、release checklist、version generator validationも追加された。

文書は概ね現状と一致するが、`docs/ROADMAP.md` にはseek/chained Ogg supportを
未完扱いする古い1行が残る。`docs/CURRENT_IMPLEMENTATION.md` をstatus authorityとする
運用により実装判断は誤らないが、次回roadmap更新で修正すべきである。

## 実行した検証

主担当が現行HEADで実行:

```text
go vet ./...                                      PASS
go test -count=1 ./...                            PASS
go test -count=1 -tags opusref ./...              PASS
go test -count=1 -run '^TestOfficialVectors$ -v . PASS (12/12)
go test -run '^$' -bench '^BenchmarkPerf/' \
  -benchtime=1x -benchmem .                        PASS
```

公式ベクタの最大RMSEは `testvector12` の `0.000809` で、閾値 `< 0.001` 内。

Sub Agentが独立に実行:

```text
go test -race -count=1 ./...                                PASS
multistream/surround/projection/Ogg/packet targeted tests       PASS
libopus multistream/projection/FEC interoperability             PASS
packet extensions libopus oracle (`opusref opusextsrc`)         PASS
single-stream PLC/FEC/SILK/hybrid/expert/CVBR targeted tests    PASS
```

statement coverageの監査値は全体79.1%、root 83.3%、CELT 81.8%、SILK 78.5%、
Ogg Opus 88.5%だった。

source集計:

```text
func Test...: 502
func Fuzz...: 10
```

この集計はbuild tagを含むsource entry point数であり、1回の通常suiteで全てが
実行されるという意味ではない。

## 重要な残課題

## P0

新規のP0、公式decode failure、明白なstate corruption、公開APIの重大な虚偽は
確認しなかった。

## P1: Full encoder mode-rate-quality policy

libopus完全代替として最大の残課題は引き続きここである。

現状のSILK/hybridは標準packet、transition redundancy、PLC/FEC、speech corpusで
強い結果を持つ。しかしtop-level selectionはconservativeなvoice hand-gateである。

主な差:

- analysis-derived voice/music decisionの統合不足
- mode threshold hysteresisの不足
- `SignalMusic`でpredictive modeを使わない
- SILK internal sample-rate switchingの簡略化
- stereo width policy未実装
- full SILK target-rate/control loop未移植
- application/signal/VBR/CVBR semanticsの部分互換

推奨ラベル:

- `priority/P1`
- `area/encoder`
- `area/silk`
- `area/hybrid`
- `area/quality`
- `compat/libopus`

## P1: CELT/music real-corpus quality gap

CELT encoderは相互運用と標準packet生成では強いが、real-corpus baselineには
特定music cellで7–10 dB級のmatched-bitrate gapがある。

mode gateを広げる前に、CELT music pathのTF/allocation/analysis/rate-control差を
fixtureと実音の両方で切り分ける必要がある。平均値だけでなくworst cellを
release guardにするのがよい。

推奨ラベル:

- `priority/P1`
- `area/celt`
- `area/encoder`
- `area/quality`
- `area/testing`
- `compat/libopus`

## P1: PLC/FEC API semanticsの厳密化

public SILK/hybrid PLCの未実装という前回課題は解消した。しかしlibopus代替を
名乗るには、loss APIのduration/state/control semanticsを揃える必要がある。

優先項目:

- lost durationを明示できるFEC API設計
- packed multi-frame SILK/hybrid FEC
- 初回decode前PLCの扱い
- PLCで許容するframe-size集合
- PLC/FEC後の `FinalRange`
- float32/float64/24-bit convenience APIの採否
- single-streamとmultistream/surroundでの同一契約

既存APIを壊せない場合は、v1 additive APIまたはv2候補として整理する。

推奨ラベル:

- `priority/P1`
- `area/decoder`
- `area/fec-plc`
- `area/api`
- `compat/libopus`

## P2: Performance / allocation

benchmark harnessと複数のallocation改善は前進である。一方、公開baselineでは
SILK stereo encodeが数MB・1万回超allocation/op、hybrid stereoも数MB級だった。
最新passで減っているが、long-lived realtime stream用途では依然大きな差である。

優先候補:

- SILK speculative analysis/NSQ scratchのstate-owned再利用
- packet/PCM conversion buffer reuse
- multistream/projection temporary buffer reuse
- long-running stream benchmark
- latency、GC pause、channel別allocation regression

## P2: Surround / aggregate CTL parity

core multistream interoperabilityは強い。残る差はfull surround
psychoacoustic energy-mask analysisとaggregate CTL convenience APIである。
encoder policy差が各elementary streamにも伝播する点を含めて評価すべきである。

## P2/P3: DRED/QEXT/OSCE

packet extension transportはあるがcodec/DSPはない。

- DRED encode/decode/process
- QEXT DSP
- DNN blob
- OSCE BWE
- ignore-extensions decoder policy

libopus 1.6.1全機能追随ならP2。RFC 6716 core OpusとPure Go portabilityを主目的に
するならP3または明示的out-of-scopeでよい。

## P3: Ogg multiplex / global indexing / external corpus

seekとchainingは解消済み。残る高水準機能はmultiplexed demux、physical chain
全体のglobal seek/index、metadata editing、外部実装で生成した大規模Ogg corpusとの
相互運用である。opusfile完全代替を名乗らない限りrelease blockerではない。

## P3: Arbitrary projection encoder matrices

既定libopus matrixとfamily 2/3相互運用は十分強い。任意custom encoder matrix生成は
未公開であり、高度なprojection用途に限った残差である。

## リリース判断

### 現在すでに強い用途

- Pure Go Opus decoder
- CELT中心の一般encode
- voice-oriented SILK-only/hybrid encode
- single/multistream/surround PLC・FEC
- packet inspection / repacketization / padding
- packet extension transport
- Ogg Opus read/write、seek、chained reading
- multistream/surround/projection/Ambisonics
- CGOなしの通常利用

### 条件付きで強い用途

- VoIP/WebRTC風用途
  - PLC/FEC、speech corpus結果は前回より大幅に強い
  - full libopus mode/rate policyとrealtime allocationは未達
- music encode
  - 標準packetと相互運用は強い
  - worst corpus cellの品質差が残る
- 高チャンネル/空間音声
  - mappingとinteropは強い
  - full surround psychoacoustic analysisではない
- 最新libopus extension
  - payload transportは可能
  - neural/DSP機能はない

### まだ避けるべき主張

- libopus 1.6.1完全drop-in replacement
- libopus encoderと同等の全mode decision・全quality
- bit-exact encoderまたは全random packetでbit-exact decoder
- 全CTL semantics互換
- opusfile完全代替
- DRED/QEXT/OSCEを含む最新extension完全実装
- libopus同等のCPU・allocation性能

## 次の推奨順序

1. CELT/music worst-corpus gapのroot causeを固定fixture化して縮める
2. SILK/hybrid mode-rate policyをreal-corpus gateごとに1変更ずつ進める
3. PLC/FECのduration、packed packet、FinalRange semanticsを厳密化する
4. stereo widthとSILK internal-rate policyを実装・測定する
5. SILK/hybrid encode allocationをstate-owned buffer中心に削減する
6. multistream/surround aggregate CTLとenergy-mask analysisを詰める
7. float/24-bit PLC/FEC convenience APIの採否を決める
8. DRED/QEXT/OSCEを実装対象か明示的out-of-scopeか決める
9. Ogg multiplex/global indexとcustom projection matrixの優先度を決める

## 最終判定

このリポジトリは、Pure Go Opusライブラリとして「主要機能が揃っている」段階から、
「欠けているものを明示して品質と性能を詰める」段階へ進んだ。

前回P1だったPLC、CTL/helper、real-corpus評価は大きく前進し、Oggとmultistreamの
高水準機能も埋まった。厳密なPLC/FEC・CTL semanticsの差を保守的に織り込んでも、
Pure Goライブラリとして約93%、libopus完全代替として約81%への上方修正は妥当である。

ただし残る約19%は軽いAPI穴ではない。encoder policy、CELT music品質、
psychoacoustic control、最新extension DSP、realtime performanceという、
libopusの成熟度そのものに相当する部分である。

今後は機能一覧を増やすより、worst-case corpus、policy gate、allocationを
測定可能な小単位で改善するのが最短である。
