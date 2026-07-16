# libopus完成度 再監査レポート（第3回）

監査日: 2026-07-01  
対象: `github.com/darui3018823/opus`  
ブランチ: `main`  
HEAD: `8142def Merge pull request #18 from darui3018823/dev/find-lpc-flp-phase1-6`  
describe: `v1.2.0-11-g8142def`  
公開Version: `1.2.0`  
比較対象: libopus 1.6.1  
前回監査: `.claude/memory/audits/libopus-completeness-2026-06-23.md`

## 結論

今回の監査では、完成度を2つに分けて評価する。

- Pure Go Opusライブラリとしての完成度: **約88%**
- libopus完全代替としての完成度: **約74%**

前回レポートでは「libopus代替として約86%」と評価していたが、今回の
採点では「標準Opusを扱えるPure Go実装」と「libopusのAPI、encoder
policy、PLC、CTL、周辺機能まで含む完全代替」を分けた。

機能後退ではない。むしろ6/23以降、SILK encoderのfind_LPC FLP Phase
2-6、FEC品質改善、opusref CI拡張、fuzz target追加、README/roadmapの
同期などが進んでいる。

ただし、libopusとして厳密に見ると、残りは小さなAPI穴ではない。
残課題の中心は次に移っている。

- full SILK/hybrid mode-rate-quality policy
- SILK-only/hybrid public PLC
- libopus CTL/API parity
- CELT encoder bit-exact性または同等品質の証明
- surround/multistreamの全CTLとpsychoacoustic parity
- Ogg Opusのseek/chained/multiplex対応
- DRED/QEXT等の最新libopus 1.6系機能

したがって、現時点の正確な表現は次の通り。

> デコーダ、packet/container、multistream/projection、互換性基盤は
> かなり成熟している。エンコーダも標準Opus packetを出しlibopusで
> decodeできる実用域にある。ただし、libopusの完全なdrop-in replacement
> ではなく、未完の主戦場はencoder policy、SILK/hybrid、PLC、CTL互換にある。

## 前回からの差分

前回監査HEAD `572d09d` から今回HEAD `8142def` までの差分:

- 43ファイル変更
- 2,862行追加
- 314行削除

主な差分:

- `docs/CURRENT_IMPLEMENTATION.md` が2026-06-24基準に更新
- `VERSION` / `version_gen.go` が `1.2.0` に更新
- `opusref.yml` にshort-frame、FEC、multistream、projection、
  packet extension oracleが明示追加
- `fuzz.yml` がdecode/float decode/extensions/multistream/repacketizer/Ogg
  の6 targetへ拡張
- SILK encoderにNLSF encode、LPC input preparation、residual energy、
  delayed-decision NSQ境界、FEC/PLC continuity周辺の追加
- `oggopus/fuzz_test.go` 追加
- README/README_ja/ROADMAPの現状追随

## 再採点

| 領域 | 前回評価 | 今回評価 | 判定 |
|---|---:|---:|---|
| 単一ストリームデコーダ | 95% | 96% | 公式ベクタ12/12、libopus参照比較、hybrid復元まで強い |
| CELTエンコーダ | 84% | 85% | 標準packet出力とlibopus decode互換あり、bit-exactではない |
| SILK/Hybridエンコーダ | 72% | 78% | find_LPC/NSQ/FEC改善、ただし限定voice pathと実験gateが残る |
| 単一ストリーム公開API | 90% | 90% | 主要APIはあるが全CTL互換ではない |
| Multistream/Surround/Projection | 83% | 85% | core APIとlibopus相互運用は強い、全CTL/psychoacousticは未達 |
| Packet/Repacketizer/Extensions | 88% | 91% | oracle比較とfuzz拡張で堅くなった、DRED/QEXT DSPは範囲外 |
| Ogg Opus | 86% | 87% | 単一logical streamは実用、seek/chained/multiplexなし |
| テスト・堅牢性 | 92% | 94% | opusref CIとfuzz targetが拡張、通常/参照テストも良好 |
| 実行性能 | 82% | 82% | 前回から大きな新測定なし、allocationはまだ改善余地あり |
| Pure Go Opusライブラリ総合 | 約86% | **約88%** | 広い実装を持つ高品質Pure Go Opus |
| libopus完全代替総合 | 約86%相当の表現 | **約74%** | 採点軸を厳密化。encoder/PLC/CTLが支配的な残課題 |

## 現在かなり強い領域

### デコーダ

デコーダは今回も最も強い領域。

- 公式RFC 8251ベクタ12/12合格
- libopus 1.6.1参照比較あり
- CELT/SILK/hybrid decode
- hybridのSILK low band + CELT high band復元
- final range、pitch、gain、phase inversion制御あり

通常decodeについては、libopus相当の信頼を置きやすい。
残る弱点は通常decodeではなくpacket loss周辺、特にpublic PLCである。

### Packet / Repacketizer / Extensions

RFC 6716 packet framing、padding、repacketizer、packet extensionの公開APIが
揃っている。packet extensionはlibopus oracle比較もあり、DRED/QEXT payload
のtransportとしては有用。

ただしDRED/QEXTのcodec/DSPそのものは未実装であり、最新libopus機能の
完全実装ではない。

### Multistream / Surround / Projection

multistream、surround、projection、Ambisonicsは、前回時点ですでに広く実装済み。
今回も大きな後退はない。

- Appendix B self-delimited framing
- mapping family 0/1/255
- RFC 8486 family 2/3
- predefined libopus projection matrices
- bidirectional libopus interoperability tests

ただしlibopus multistream CTL全体、surround psychoacoustic energy-mask解析、
任意projection encoder matrix生成は未達。

### Ogg Opus

単一logical streamのOgg Opus処理は実装済み。

- page parse/write
- CRC
- lacing/continued packet
- OpusHead/OpusTags
- Reader/Writer
- Ogg parser fuzz

一方、opusfile相当のseek、chained stream orchestration、
multiplexed stream demuxはない。

## 6/23以降の前進

### SILK encoder find_LPC FLP Phase 2-6

SILK encoderは6/23以降もかなり進んでいる。

- residual energy pathのlibopus layout追随
- process gainsの境界改善
- delayed-decision NSQ boundaryのfull-Q16 gain scaling
- unvoiced SILK framesのtrellis NSQ適用範囲拡大
- mono AB scoreboardの維持
- stereo/hybridのopusref対象拡大

一方で、`OPUS_SILK_TRANSPARENT_NLSF=1` はdefault-offの実験gateとして
残されている。これは「未実装だから隠している」というより、
測定上、同等bitrateでnet winにならなかったため採用しない判断である。

### FEC/LBRR品質

前回の最大懸念だったFEC品質は改善している。

現在のsnapshotでは:

- mono/stereo SILK-only、mono/stereo hybridでLBRR encode/decodeあり
- hybrid FECは冗長SILK low bandを復元
- stereo/hybrid FEC recoveryに20 dB級のguard
- FEC後のnormal decode continuity guardあり

ただしmono 60 msの一部slotはVAD-inactiveでLBRRが存在せず、SILK PLC fallback
になる。これは仕様上の単純なバグではないが、libopus bit-exact PLCではない。

### CI / Fuzz

前回課題だったopusref CIとfuzzは明確に前進した。

`opusref.yml` は以下を明示実行する。

- SILK encoder AB
- SILK-only conformance
- short-frame interoperability
- SILK/hybrid FEC interoperability
- multistream interoperability
- projection interoperability
- packet extension oracle
- decoder reference

`fuzz.yml` は以下の6 targetをamd64/arm64でnightly/manual実行する。

- `FuzzDecode`
- `FuzzDecodeFloat`
- `FuzzPacketExtensions`
- `FuzzMultistreamDecode`
- `FuzzRepacketizer`
- `FuzzOggParsers`

## 実行した検証

今回の監査スレッドで確認した結果:

```text
go vet ./...                         PASS
go test -count=1 ./...               PASS
```

サブエージェント側で追加確認:

```text
go test -count=1 -v -run TestOfficialVectors .     PASS
go test -tags opusref -count=1 ./...                PASS
go test -tags "opusref opusextsrc" -count=1 \
  -run '^TestPacketExtensionsLibopusOracle$' .      PASS
```

公式ベクタは12/12実行され、最大RMSEは `testvector12` の `0.000809`、
閾値 `< 0.001` 内だった。

今回の簡易集計:

- `func Test...`: 434
- `func Fuzz...`: 6

この数字は、前回の約422 tests / 2 fuzz targetsから増えている。

## 重要な残課題

## P0

今回の監査で、即時の状態破壊、明白な公開API虚偽、公式decode失敗に相当する
新規P0は確認しなかった。

ただし、libopus完全代替を名乗るリリースでは以下のP1を必須扱いにするのが妥当。

## P1: Full SILK/Hybrid mode-rate-quality policy

現在のSILK/hybrid encoderは実用的な標準packetを出せるが、
libopus同等の総合encoder policyではない。

現状:

- SILK-onlyは低bitrate speech intent中心
- hybridは24/48 kHz voice path中心
- `ApplicationVOIP` / `SignalVoice` / bitrate / bandwidth / FEC条件で狭くgate
- voiced/onset packet sizeやrate-controlに簡略化が残る
- transparent NLSFはdefault-off実験gate
- homebrew/legacy fallbackが一部残る

libopus代替として一番重い残課題はここ。

推奨ラベル:

- `priority/P1`
- `area/encoder`
- `area/silk`
- `area/hybrid`
- `area/quality`
- `compat/libopus`

## P1: SILK/Hybrid public PLC

公開`DecodePLC`は現在CELT-only対応で、SILK-only/hybridは
`ErrUnimplemented`。

FECは進んだが、FECが存在しないlossやVAD-inactive frameではPLCが重要になる。
VoIP/WebRTC系用途でlibopus代替を名乗るなら必須。

推奨ラベル:

- `priority/P1`
- `area/fec-plc`
- `area/decoder`
- `compat/libopus`

## P1: CTL/API parity棚卸し

Goらしい個別method APIはかなり揃ったが、libopusの全CTL相当ではない。

不足または要評価:

- `opus_encoder_ctl` / `opus_decoder_ctl` の網羅性
- expert frame duration
- packet has LBRR
- soft clipping helper
- current bandwidth getter
- multistream packet pad/unpad
- multistream/projection CTL群
- ignore extensions / DRED/QEXT関連CTL

完全drop-in replacementを目標にしないなら全部は不要。
ただし「libopusとして」という表示をするなら、未対応CTL一覧を明示すべき。

推奨ラベル:

- `priority/P1`
- `area/api`
- `compat/libopus`

## P1: 実音声corpusでのencoder品質評価

現在の品質guardはかなり増えているが、synthetic fixture中心である。
SILK/hybridの完成度判断には実音声corpusが必要。

追加したいもの:

- clean speech / noisy speech / music / mixed content
- onset/plosive-heavy speech
- stereo speech/music
- bitrate sweep
- packet loss sweep
- libopus encodeとのmatched-bitrate A/B
- subjective proxy: PESQ/POLQA相当は難しくても、multi-metric scoreboard

推奨ラベル:

- `priority/P1`
- `area/testing`
- `area/quality`
- `compat/libopus`

## P2: CELT encoder parity

CELT encoderは標準packetを出し、libopus decode cross-checkも通る。
しかしbit-exactではなく、VBR/rate controlやanalysisはlibopus完全同等ではない。

相互運用だけなら現在で十分強い。品質・bit allocation・edge case parityを
詰めるなら継続課題。

推奨ラベル:

- `priority/P2`
- `area/celt`
- `area/encoder`
- `compat/libopus`

## P2: Multistream/Surround psychoacoustic parity

core encode/decode、mapping、interoperabilityはある。
残るのはlibopus multistream CTL全体とsurround energy-mask解析。

推奨ラベル:

- `priority/P2`
- `area/multistream`
- `area/surround`
- `compat/libopus`

## P2: Ogg Opus high-level機能

単一logical stream Reader/Writerはある。
opusfile相当を目指すなら次が必要。

- seeking
- chained stream orchestration
- multiplexed stream demux
- duration/index helper
- metadata editing utilities

推奨ラベル:

- `priority/P2`
- `area/oggopus`

## P2/P3: DRED/QEXT codec/DSP

packet extension transportはあるが、次は未実装。

- DRED encode/decode
- DRED process/decode API
- QEXT DSP
- DNN blob
- OSCE BWE

libopus 1.6.1完全追随を目標にするならP2。
Opus core互換を目標にするならP3。

## P2/P3: performance/allocation

前回までに大幅改善しているが、allocation削減余地は残る。

候補:

- PCM変換buffer再利用
- multistream/projection temporary buffer pool
- packet assemblyの事前容量確保
- long-lived stream benchmark
- channel数別alloc regression

## リリース判断

### 現在すでに強い用途

- Pure Go Opus decoder
- 標準Opus packet inspection / repacketization / padding
- Ogg Opus単一logical streamのread/write
- CELT中心の一般encode
- 条件付きSILK-only/hybrid voice encode
- multistream/surround/projection/Ambisonics
- libopus decode可能な標準packet生成
- CGOなし通常利用

### 条件付きで使える用途

- VoIP/WebRTC風用途
  - FEC/LBRRはある
  - ただしpublic SILK/hybrid PLC未実装
  - full libopus mode/rate policyではない
- 高チャンネル/空間音声
  - core mappingとinteroperabilityはある
  - full surround psychoacoustic parityではない
- 最新libopus 1.6系extension用途
  - packet transportはある
  - DRED/QEXT DSPはない

### まだ避けるべき主張

- libopus完全drop-in replacement
- libopus encoderと同等の全品質・全mode decision
- 全CTL互換
- 全packet loss条件でlibopus同等
- opusfile相当のOgg Opus high-level機能
- DRED/QEXTを含むlibopus 1.6.1完全実装
- bit-exact encoder

## 次の推奨順序

1. SILK/hybrid public PLC
2. SILK/hybrid mode-rate-quality policyの未対応表を作り、優先順に潰す
3. 実音声corpusのmatched-bitrate A/B scoreboard
4. libopus CTL/API parity matrixを作成
5. CELT encoder quality/rate-control parityの追加測定
6. multistream/surround CTLとpsychoacoustic parity
7. Ogg seek/chained/multiplexed対応の設計
8. DRED/QEXT codec/DSPの採否判断
9. allocation削減とlong-running benchmark

## 最終判定

このリポジトリは、すでに「趣味実装」や「decoderだけの実験」ではない。
Pure Go Opusライブラリとしてはかなり広く、テストも厚く、libopusとの
相互運用確認も継続的に回っている。

一方で、libopusとしての完成度を厳密に問うなら、残り約25%は重い。
特にencoder policy、SILK/hybrid、PLC、CTL互換は、API数を少し足せば
終わる種類の問題ではない。

したがって今回の評価は次で固定する。

- Pure Go Opus実装として: **約88%**
- libopus完全代替として: **約74%**

今後は機能を広げるより、libopusとの差分を表に落とし込み、
実音声corpus、packet loss、rate-control、CTL互換の順に品質を固める段階。
