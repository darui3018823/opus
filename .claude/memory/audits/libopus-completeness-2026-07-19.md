# libopus完成度 再監査レポート（第5回）

監査日: 2026-07-19
対象: `github.com/darui3018823/opus`
ブランチ: `main`
HEAD: `d9beea737a67c131e78694cdc0cd5b58499966b6`
describe: `v1.4.0-0-gd9beea7`
公開Version: `1.4.0`
比較対象: libopus 1.6.1
前回監査: `.claude/memory/audits/libopus-completeness-2026-07-17.md`

## 結論

今回もPure Go製品としての完成度と、libopus代替としての完成度を分ける。
さらに、前回まで曖昧だった「core codec代替」と「標準Opusにlibopus 1.6.1の
公開extension機能を加えた代替」も分離する。

- Pure Go Opusライブラリとしての完成度: **約95%**
- RFC 6716/core libopus機能代替としての完成度: **約89%**
- 標準Opus＋libopus 1.6.1公開extension機能の代替: **約83%**

前回と同じ主観尺度だけを継続した場合、約93% / 約81%から
**約95% / 約85%**への上方修正に相当する。ただし前回の「完全代替」値は、
Oggやrelease readinessを加点し、DRED/QEXT/OSCEをout-of-scopeとして実質的に
減点しない尺度だった。今回はOggをlibopus本体の代替点から外し、libopus 1.6.1の
公開extension機能も分母に含めるため、拡張込み代替の主値を83%とした。
ただし `opus_custom.h` のOpus Custom APIは今回も監査対象外であり、文字どおりの
「libopus全public header代替」を表す値ではない。

今回の最大の進展は次の5点である。

- PLC/FECのduration、packed packet、初期状態、FinalRange、PCM型別APIを整備
- CELT/musicの最悪matched-bitrate差を、byte totalを増やさず約9.7 dBから約5.7 dBへ縮小
- surroundにstatefulなchannel-role energy-mask解析とallocation-trim連携を追加
- SILK/hybrid encode allocationを代表workloadで27–58%（B/op）削減
- high bitrate、Signal state、silent LBRR、transition PLC/FECなど複数の境界不具合を修正

現時点の正確な表現は次の通り。

> Pure Goで標準Opusをencode/decodeし、packet、multistream、surround、
> projection、Ogg Opus、PLC/FECまで扱う製品としては完成域にある。
> decoder、loss recovery、framing、相互運用は特に強い。一方、拡張込み代替には、
> encoderのmode/rate/quality policy、CELT musicの低中rate品質、
> realtime predictive encode cost、surround maskの残りconsumer、DRED/QEXT/OSCEが残る。

百分率はAPI個数の単純比ではない。標準bitstream、相互運用、品質証拠、
loss semantics、堅牢性、性能、残課題の難度を含む監査上の推定値である。
Encoderのlibopusとのbit-exact性は完成条件にしていない。

## 監査方法と採点境界

実装判断は `docs/CURRENT_IMPLEMENTATION.md` と現行コードを優先し、
`docs/CTL_PARITY.md`、実装後のiteration log、テスト、benchmarkを補助証拠にした。

3つの総合値の境界は次の通り。

| 評価 | 含むもの | 含まないもの |
|---|---|---|
| Pure Goライブラリ | codec、packet、multistream、projection、Ogg、文書、release readiness | C ABI互換 |
| core代替 | RFC 6716 codec、主要CTL、loss recovery、multistream/projection、packet helper | DRED/QEXT/OSCE、Ogg、C ABI |
| libopus 1.6.1拡張込み代替 | coreに加え、1.6.1公開extension/DRED機能と厳密なCTL semantics | C言語ABI、`opus_custom.h` のOpus Custom API |

Ogg multiplexや独自custom projection matrixはPure Go製品の拡張候補ではあるが、
libopus本体の公開API代替点では減点しない。

## 前回からの差分

前回監査HEAD `c5f3d05` から今回HEAD `d9beea7` まで:

- 42 commits（非merge 40）
- 56 files changed
- 5,237行追加、388行削除
- source上の `func Test...`: 502 → 531
- source上の `func Fuzz...`: 10 → 10
- 公開Version: 1.2.0 → 1.4.0

主な差分:

- `DecodeFECWithDuration` とPLC/FECのint16/24-bit/float32/float64 APIを追加
- 初回zero PLC、全2.5 ms刻み、packed first-frame FEC、PLC prefixを実装
- loss recoveryをsingle/multistream/surroundでtransactionalにした
- constrained-VBR startup dampingでCELT tonal/musicの初期packet不足を修正
- surround channel-role masking解析とCELT allocation trim連携を実装
- SILK scratch stack化とdelayed-decision state再利用を実装
- 64-frame predictive packet/final-range digestと256-frame benchmarkを追加
- pending LBRRのsilence carrier、transition redundancy後のPLC/FEC stateを修正
- numeric high bitrateと`BitrateMax`のper-constituent-frame ceilingを修正
- `SignalAuto`とApplication由来のeffective hintを分離
- packed `PacketHasLBRR`をrecoverable first frame semanticsへ修正

## 再採点

| 領域 | 7/17評価 | 今回評価 | 判定 |
|---|---:|---:|---|
| 単一ストリームデコーダ | 98% | **99%** | 公式ベクタ、全mode PLC/FEC、loss semantics、transactionality |
| CELTエンコーダ | 86% | **89%** | music worstを大幅縮小。24/32 kbps残差とanalysis差が残る |
| SILK/Hybridエンコーダ | 84% | **85%** | 品質・interopは強い。mode/rate policyは未採用のまま |
| 単一ストリーム公開API/CTL | 93% | **95%** | loss APIは前進。Lookahead/LSBDepth等はsemantic partial |
| Multistream/Surround/Projection | 92% | **94%** | loss APIとmask trimが前進。残りmask consumer/CTLがある |
| Core Packet/Repacketizer | 95% | **97%** | first-frame LBRR semanticsを修正。core helperはほぼ揃う |
| Ogg Opus | 94% | **94%** | 今回は実質変更なし。Pure Go製品評価のみへ加点 |
| DRED/QEXT/OSCE | 未分離 | **約20%** | opaque transportのみ。decode/process/DSP/CTLは未実装 |
| テスト・堅牢性 | 98% | **98%** | tests +29、全suite/race合格。一方coverageは78.3%へ微減 |
| 実行性能（製品尺度） | 86% | **89%** | allocation大幅減。ただしstereo predictive costは依然重い |
| 文書・release readiness | 95% | **95%** | release toolingは前進。CTL表に過大なSupported判定が残る |
| Pure Go Opusライブラリ総合 | 約93% | **約95%** | 5つのpost-audit領域中4領域で採用改善 |
| core libopus機能代替 | 未分離 | **約89%** | core codec/loss/framingは強い |
| libopus 1.6.1拡張込み代替 | 約81% | **約83%** | extensionを分母へ明示。旧尺度継続値は約85% |

## 最大の前進

### PLC/FEC semantic parity

前回のP1だったloss-recovery APIのsemantic contractはほぼ解消した。

- missing durationを明示する `DecodeFECWithDuration`
- decode前/Reset後のzero PLC
- positiveな2.5 ms刻みから120 msまでのPLC
- packed count-code 1/2/3 carrierのfirst-frame FEC
- lossが長い場合のPLC prefix + FEC suffix
- FEC不在時のexplicit APIによるPLC fallback
- PLCのFinalRange=0、SILK/hybrid/multistreamのrecovery range契約
- int16、signed 24-bit-in-int32、float32、float64
- single-stream、multistream、surroundで共通のduration契約
- error時にdecoder stateとcaller bufferを変更しないtransactional recovery

旧 `DecodeFEC(data, pcm)` はv1互換のため、carrier total durationを推定し、
CELT-onlyで従来のerror contractを保持する。明示duration版との契約分裂は、
将来のv2 API整理候補である。

追加hardeningとして、trailing SILK-to-CELT redundancy後のPLC modeと、
直前packet framingに基づくFEC eligibilityを別stateとして管理するようになった。
digital silence packetも直前active packetのpending LBRRを運び、後続へ誤って
持ち越さない。

### CELT/music matched-bitrate品質

静かなtonal streamでは、簡略化されたCELT activity targetと空のCVBR reservoirが
組み合わさり、初期packetが公称targetの約1/4まで縮んでいた。libopus式の
約2/3 blendをbyte targetへ適用し、総byte数を変えずに初期budget配分を修正した。

full 140-cell記録では:

- music worst: +9.69 dB → +5.68 dB
- mixed worst: +5.64 dB → +1.71 dB
- 5つのspeech-oriented class aggregate: -0.0463 dBのまま
- loss-0 own-byte total: 全classで不変

stereo-chords loss-0の24/32 kbpsは約+5.7/+5.2 dB残る。
TF-estimateをallocation trimへ加える追加修正は正しい方向だが効果は小さい。
次の実測候補はtonality slope、stereo saving、broader dynamic allocationである。

### Surround psychoacoustic analysis

mapping family 1の3ch以上に、libopus由来のstateful 21-band解析を追加した。
left/center/right/LFE role、pre-emphasis、MDCT band energy、spectral spreading、
energy mask aggregationを行い、elementary CELT encoderのallocation trimへ
mask slopeを渡す。Resetとpacket assembly failureでも履歴整合性を保つ。

同一child packet bytesのfixture結果:

| Fixture | before | after | libopus |
|---|---:|---:|---:|
| 5.1 role-rich weighted SNR | 3.337 dB | **8.368 dB** | 13.718 dB |
| 7.1 role-rich weighted SNR | 2.531 dB | **6.023 dB** | 6.180 dB |

LFEは不変、active-channelの最大悪化は0.04 dB未満だった。一方、libopusが
energy maskを使うper-band dynalloc、mask-aware VBR、SILK/hybrid rate offset、
full LFE CELT policyは未実装である。isolated per-band dynalloc候補は4 fixtureすべてを
0.001–0.013 dB悪化させたため棄却された。

### SILK/Hybrid allocationとruntime

inverse prediction gain、NLSF-to-LPC、NLSF reconstructionのbounded scratchを
stack化し、最大4つのdelayed-decision NSQ candidate stateをencoder-owned scratchへ
移した。64-frame digestはpacket bytesとFinalRangeの不変を保証する。

Phase 4 fresh baselineからのallocation変化:

| Workload | B/op変化 | allocs/op変化 |
|---|---:|---:|
| SILK mono | -57.5% | -73.8% |
| SILK stereo | -32.8% | -50.6% |
| Hybrid mono | -33.7% | -70.7% |
| Hybrid stereo | -26.9% | -57.3% |

今回の1秒×5実測medianは、SILK mono 252 KB/1,708 allocs、SILK stereo
1.38 MB/4,839、hybrid mono 372 KB/1,032、hybrid stereo 1.27 MB/3,464だった。
allocationは既存記録と一致する。時間値はmachine load差が大きいため、別日の絶対値で
改善率を主張しない。

256-frame benchmarkのretained live heapは既存記録で432–4,992 bytesであり、
frame数に比例するstate growthは観測されていない。ただし20 ms stereo predictive
encodeの絶対CPU時間とGC pressureはCELTより大きく、libopus拡張込み代替尺度では
performanceをまだ強く減点する。

## 追加で解消した境界不具合

- `BitrateMax`を40–120 ms packet全体ではなく各20 ms constituent frameへ適用
- numeric high bitrateを750 kbit/s/channelでCTL clampし、実payloadはRFC ceilingへ制限
- `SignalAuto` request stateとApplication由来のeffective hintを分離
- explicit Voice/MusicをApplication変更、forced mono、Reset後も保持
- packed `PacketHasLBRR`をFECで回収可能なfirst frameだけの検査へ修正
- CELT decoder geometry間state copyでPLC band boundsを保持
- pending LBRRをdigital-silence carrierで運び、inactive LBRRをexpire

6000 bit/s未満を一時的に許可した実験候補は、Goが127-byte packetを出す一方で
libopusが1–15 bytesを出したため棄却された。現行 `SetBitrate` はこの範囲を拒否する。

## 実行した検証

現行HEADで独立に実行:

```text
go vet ./...                                      PASS
go test -count=1 ./...                            PASS
go test -count=1 -tags opusref ./...              PASS
go test -race -count=1 ./...                      PASS
go test -count=1 -run '^TestOfficialVectors$ -v . PASS (12/12)
predictive benchmark 1s x 5                       PASS
```

公式ベクタの最大RMSEは `testvector12` の `0.000809` で、閾値 `< 0.001` 内。

source集計:

```text
func Test...: 531
func Fuzz...: 10
```

statement coverage実測:

| 対象 | 7/17 | 今回 | 差 |
|---|---:|---:|---:|
| aggregate | 79.1% | 78.3% | -0.8 pt |
| root | 83.3% | 83.2% | -0.1 pt |
| CELT | 81.8% | 79.2% | -2.6 pt |
| SILK | 78.5% | 77.8% | -0.7 pt |
| Ogg Opus | 88.5% | 88.5% | ±0.0 pt |

production Goにも実装statementが増え、29 test entryも追加されているため、
coverage率低下だけで堅牢性後退とは断定しない。ただし、とくにCELTの新規分岐は
次回のtargeted coverage候補である。

`OPUS_REAL_CORPUS` は今回のfresh verificationでは設定していない。full 140-cell結果は
2026-07-17/18の保存CSVとiteration記録に基づく。通常/opusref suiteのPASSを、
opt-in corpusの今回再実行と読み替えてはいけない。

## 監査で見つかった文書・parity上の注意

`docs/CTL_PARITY.md` は主要API把握には有用だが、いくつかの「Supported」は
surface parityをsemantic parityとして扱っている。

- `Lookahead` は常に `sampleRate/400` を返し、libopusのapplication/config依存delayと一致しない
- `LSBDepth` は値を保持・伝播するが、コードコメントどおりcodec decisionには使わない
- `SetPacketLossPerc` は範囲外値をclampするvoid setterで、libopus CTLのerror契約と異なる
- packet inspection helperは完全framingを検証し、libopus helperよりstrictな場合がある
- DREDはCTLだけでなくparse/process/decode APIとneural recovery全体が未実装

したがって`Lookahead`と`LSBDepth`は監査上Partialとした。今後のparity表は
API surface、semantic parity、evidenceを分けると誤読が減る。

## 重要な残課題

### P0

新規のP0、公式decode failure、明白なstate corruption、公開APIの重大な虚偽は
確認しなかった。

### P1: Encoder mode-rate-quality policy

libopus代替の最大の残課題である。

- analysis-derived voice/music decisionの統合不足
- mode threshold hysteresis不足
- `SignalMusic`でpredictive modeを使わない
- SILK internal sample-rate switchingの簡略化
- stereo width policy未実装
- broader DTX/VBR/CVBR control loopの部分互換
- 500–5999 bit/sのcompact low-rate packet未対応

post-audit Phase 3では2候補を測定したが、unified predictive thresholdは
stereo-speech bytesを63.3%増やし、broadband exitはonset/source gapを
0.11/0.09 dB悪化させbytesも2%/1%増やした。両方を棄却した判断は妥当である。

### P1: CELT/music 24/32 kbps品質

worst gapは大幅に縮小したが、stereo-chordsに約5.7/5.2 dB残る。
現在の単一seedとsynthetic/SNR中心の証拠を、複数外部corpus、知覚指標、
損失条件へ拡張する必要もある。

### P1: Realtime predictive encode cost

allocation削減率は大きいが、SILK/hybrid stereoの絶対CPU・allocationはまだ重い。
次候補はNLSF/LPC output ownership、stereo trellis、CELT transform working storageである。

### P1: Extension scope decision

DRED/QEXT payload transportだけを行うのか、libopus 1.6.1のDRED parse/process/decode、
QEXT/OSCE/DNN/ignore-extensionsまで追うのかを明示する必要がある。実装しない場合は、
製品主張を「core libopus replacement」に限定するのが正確である。

### P2: Surround / aggregate CTL

allocation-trim consumerは実装済み。per-band dynalloc、mask-aware VBR、
SILK/hybrid rate offset、full LFE CELT policy、aggregate CTL convenienceが残る。

### P2: Coverageと品質evidence

test entryは増えたがcoverage率は微減した。CELT/surround/loss semantic分岐の
targeted coverage、継続可能な外部corpus、知覚品質metricが次の証拠強化候補である。

### P3: Pure Go製品の追加機能

Ogg multiplexed demux、physical chain全体のglobal index、metadata editing、
追加projection convenienceは製品価値がある。ただしlibopus本体代替とは別枠である。

## リリース判断

### 現在すでに強い用途

- Pure Go Opus decoder
- CELT中心の一般encode
- voice-oriented SILK-only/hybrid encode
- single/multistream/surround PLC・FEC
- packet inspection / repacketization / padding
- Ogg Opus read/write、seek、chained reading
- multistream/surround/projection/Ambisonics
- CGOなしの通常利用

### 条件付きで強い用途

- VoIP/WebRTC風用途: loss recoveryは強いがfull mode policyとstereo realtime costが未達
- music encode: 標準packetと相互運用は強いが24/32 kbps worst cellが残る
- 高チャンネル音声: mapping/mask trimは強いがfull surround policyではない
- 最新extension: opaque transportは可能だがrecovery/DSPはない

### 避けるべき主張

- 標準Opus＋libopus 1.6.1公開extension機能の完全代替
- Opus Customを含むlibopus全public headerの代替
- libopus C APIのdrop-in replacement
- libopus encoderと同等の全mode/rate/quality policy
- 全CTL semantics互換
- DRED/QEXT/OSCEを含むextension完全実装
- libopus同等のCPU・allocation性能

Encoderが非bit-exactであること自体は避けるべき主張ではない。標準準拠bitstream、
cross-decode、rate adherence、qualityが成立すれば、encoder bit-exact性は代替要件ではない。

## 次の推奨順序

1. 製品スコープを「core代替」か「1.6.1 extension込み代替」か明文化する
2. CTL表をsurface/semantics/evidenceへ分け、Lookahead/LSBDepth等を訂正する
3. CELT 24/32 kbps gapをtonality/stereo saving/dynallocの1仮説ずつ測る
4. predictive stereoのNLSF/LPC/trellis allocationとCPUを削減する
5. policy変更は新しい具体的corpus targetができるまで再開しない
6. surround mask-aware VBR、SILK rate offset、full LFEを独立に測る
7. external corpus、知覚指標、loss条件とCI可能なquality gateを増やす
8. DRED/QEXT/OSCEを実装するか明示的に非対象とする
9. Ogg multiplex/global indexをPure Go製品の別roadmapとして扱う

## 最終判定

この2日間の差分は、機能数を増やしただけではない。前回P1だったPLC/FEC契約を
ほぼ閉じ、CELT music、surround、predictive allocationをそれぞれ測定可能なfixtureと
非回帰guard付きで改善した。Pure Goライブラリとして約95%、core libopus代替として
約89%は妥当である。

一方、「標準Opus＋libopus 1.6.1 extension完全代替」を名乗る場合、残る約17%は
軽いAPI穴ではない。
encoderの成熟したpolicy/quality、realtime cost、surroundの残りpsychoacoustic control、
DRED/QEXT/OSCEという別世代の機能である。この分母を明示した83%評価の方が、
旧尺度の85%より製品主張として安全で説明可能である。Opus Customを含む文字どおりの
libopus全公開API代替は今回採点していない。

次の段階は、core代替として主張を固めるのか、libopus 1.6.1 extensionまで追うのかを決め、
選んだ分母に対して品質・性能・extension scopeを測定可能な小単位で詰めることである。
