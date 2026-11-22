メモリ効率化・高速化 候補監査レポート
Date: 2026/11/22
Author: Claude Opus 5.5 with Medium effort

———

## 0. 概要

- **目的**: リポジトリ全体 (公開 API 層・`internal/celt`・`internal/silk`・`internal/dsp`・
  `internal/entcode`・`oggopus`) を読み、まだ取れるメモリ削減と高速化の候補を洗い出す。
- **状態**: 調査のみ。コードは変更していない。2026-11-22 時点の `main` (`e867170`) が対象。
- **進め方**: 下の実測 (CPU/heap プロファイル、構築時サイズ、API ごとの B/op) と
  コードの読解を組み合わせた。ほかのエージェントが `.claude/` に置いたレポートは読んでいない。
- **前提**: どの候補も、実施するときは既存の bit-exact 系テスト
  (`TestPerfPredictivePacketRegression`、`TestPerfDefaultPolicyPacketRegression`、
  `go test -tags opusref ./...`、RFC ベクタ 12/12) で出力が変わらないことを確かめてから
  採用すること。本レポートの「期待効果」は実測からの見積もりで、改善後の値はまだ測っていない。

### 最優先 (費用対効果が大きい順)

| # | 内容 | 実測された問題 | 見込み |
|---|---|---|---|
| M1 | `DecodeFEC*` が呼ぶたびに `NewDecoder` 全体を作ってディープコピーする | 1 回あたり **約 1 ms・2.7 MB 確保・約 3,600 alloc** (通常の Decode は約 30 µs・1 KB) | 約 30 倍速く、確保はほぼ 0 |
| M2 | `Decoder` が CELT decoder を 4 帯域 × 4 LM × 2 ch = **32 個**前もって作る | `NewDecoder` 1 個で **1.42 MB・3,230 alloc**。5.1 の MS decoder で **5.7 MB** | 1/16〜1/32 (数十 KB 台) |
| M3 | `EncodeFloat32` が呼ぶたびに float64 バッファを `make` する | stereo 20 ms で毎回 +15 KB (int16 版は 1.7 KB/op) | 約 −15 KB/op |
| M4 | `DecodePLC` が CELT PLC 内で毎回大きなバッファを確保する | **82 KB/op**、9 alloc | ほぼ 0 |
| M5 | `Repacketizer.Out` が上限なしで `cap=64 KiB` を確保する | **66 KB/op** | 実サイズ分 (数百 B) だけ |
| M6 | Surround、Ambisonics、Multistream が毎フレーム PCM/マスク/パケットを確保する | 5.1 encode **184 KB/op・110 alloc**、FOA **139 KB/op** | 1 桁以上の削減 |
| P1 | SILK の del-dec NSQ 内ループ | 既定ポリシーの encode CPU の **34%** | 10〜25% (NSQ 部分) |

---

## 1. 測定環境と方法

- CPU: 11th Gen Intel Core i7-11700 (Windows 11, amd64)、Go 1.27.4
- 使ったコマンド (出力は scratchpad に置いた。リポジトリには残していない):

```bash
go test -run '^$' -bench '^BenchmarkPerfDefault/encode' -benchtime 2000x -cpuprofile cpu.prof .
go test -run '^$' -bench '^BenchmarkPerf$/decode' -benchtime 20000x -cpuprofile cpu3.prof .
go test -run '^$' -bench '^BenchmarkPerfDefault' -benchtime 300x -benchmem -memprofile mem.prof -memprofilerate 1 .
go tool pprof -top -cum ...   # 下に要約
```

- 構築時サイズと API ごとの B/op は、`replace` で本リポジトリを参照する使い捨てモジュール
  (scratchpad 内) から `runtime.MemStats` で測った。

### 1.1 構築時と常駐の heap (1 インスタンスあたり、GC 後に残った量)

| 対象 | 残留 B | alloc 数 |
|---|---:|---:|
| `NewDecoder(48000,1)` | 1,424,548 | 3,230 |
| `NewDecoder(48000,2)` | 1,424,544 | 3,230 |
| `NewDecoder(16000,1)` | 1,410,671 | 3,217 |
| `NewEncoder(48000,1,VOIP)` | 48,856 | 34 |
| `NewEncoder(48000,2,VOIP)` | 88,405 | 50 |
| Encoder 48k mono、50 フレーム後 | 272,288 | — |
| Encoder 48k stereo、50 フレーム後 | 498,936 | — |
| Decoder 48k stereo、50 パケット後 | 1,578,017 | — |
| `NewMultistreamDecoder` 5.1 (4 stream) | 5,695,996 | 12,923 |

参考までに libopus の `opus_decoder_get_size(2)` はおよそ数十 KB。Go 版の decoder はその
**数十倍**ある。

### 1.2 API ごとの 1 呼び出しあたりのコスト

| API | µs/op | B/op | allocs/op |
|---|---:|---:|---:|
| `Encoder.EncodeFloat32` stereo Audio 20 ms | 160.7 | 18,512 | 16.7 |
| `Encoder.Encode(int16)` 同条件 | 153.6 | 1,684 | 9.4 |
| `Encoder.EncodeFloat32` stereo LowDelay 10 ms | 92.9 | 9,892 | 15.3 |
| `Encoder.EncodeFloat32` 16k mono VOIP | 441.0 | 8,805 | 100.5 |
| `Decoder.Decode(int16)` stereo CELT | 53.1 | 800 | 4.0 |
| `Decoder.DecodeFloat32` stereo CELT | 57.0 | 8,992 | 5.0 |
| `Decoder.DecodeFloat` stereo CELT | 54.2 | 17,194 | 5.0 |
| `Decoder.DecodePLC` stereo (CELT の後) | 46.1 | **81,830** | 9.1 |
| `Decoder.DecodeFEC` (SILK 24k、Decode と交互) | 約 1,000 | **約 2.7 MB** | 約 7,000 |
| `SurroundEncoder.EncodeFloat32` 5.1 | 570.3 | **183,881** | 109.8 |
| `SurroundDecoder.Decode` 5.1 | 165.2 | 52,944 | 27.0 |
| `AmbisonicsEncoder.EncodeFloat32` FOA | 497.4 | **138,584** | 49.0 |
| `Repacketizer` Cat×2 + Out | 10.6 | **66,080** | 7.0 |

DecodeFEC の行は「Decode 100 回と DecodeFEC 100 回で 102 ms・1.38 MB/pkt」という測定値から
DecodeFEC 1 回分を割り出した値 (Decode 200 回だけなら 6 ms・1 KB/pkt)。

### 1.3 CPU プロファイル要約

**Encode (`BenchmarkPerfDefault/encode`、ModePolicyLibopus、6 ワークロード、cum%)**

| 関数 | cum% | flat% |
|---|---:|---:|
| `silk.(*Encoder).encodeRangeFrame` | 70.4 | — |
| `silk.(*Encoder).closedLoopNSQWithRateScale` | 37.2 | — |
| `silk.(*Encoder).silkNoiseShapeQuantizerDelDec` | **33.7** | 13.5 |
| `silkSMULWB` (インライン化済み、NSQ 内の乗算) | 12.2 | 12.2 |
| `celt.(*Encoder).encodeRange` | 19.6 | 1.5 |
| `silk.(*Encoder).analyzeNLSF` | 14.8 | — |
| `silkNLSFEncode` / `silkNLSFDelDecQuant` | 6.6 / 4.6 | 3.8 |
| `silkNoiseShapeAnalysisFLP32` | 6.6 | — |
| `silkInnerProductFLP32` | 6.6 | 4.9 |
| `silkWarpedAutocorrelationFLP32` | 4.9 | 4.9 |
| `silkFindPitchLags` | 5.6 | — |
| `celt.quantAllBands*` | 5.9 | — |
| `celt.runPrefilter` | 4.6 | — |

**Decode (`BenchmarkPerf/decode`、cum% / flat%)**

| 関数 | cum% | flat% |
|---|---:|---:|
| `celt.decodeCELTRange` | 41.7 | 1.6 |
| `celt.quantAllBandsImpl` / `quantBand` | 16.2 / 15.4 | — |
| `silk.(*Decoder).synthesize` | 14.6 | 5.6 |
| `opus.resampleSILK` | 14.9 | — |
| `dsp.imdctRawStatic` | 10.7 | 3.0 |
| `silk.iirFIRInterpol` | 8.6 | 7.2 |
| `silk.(*Decoder).decodePulses` | 8.7 | — |
| `celt.cwrsiLibopusInto` | 5.4 | 3.8 |
| `entcode.DecodeIcdf` | 5.6 | 3.5 |
| `celt.applyDeemphasis` | 4.9 | 4.9 |
| `float32ToInt16` + `interleaveSILKOutInto` | 5.4 | — |

---

## 2. メモリ削減の候補

### M1. `DecodeFEC*` が毎回 decoder を丸ごと複製する 【最重要】

- 場所: `opus.go:5645` (`DecodeFECFloat` → `cloneState`)、`opus.go:5860` (`cloneState` が
  `NewDecoder` を呼び、32 個の CELT decoder と全 SILK decoder に `CopyAllStateFrom` する)、
  `multistream.go:896-911`。
- 問題:
  - 1 呼び出しで約 1 ms・2.7 MB を確保する。FEC はパケットロスが起きたときに呼ばれる API なので、
    負荷が最も厳しい場面でコストが跳ね上がる。
  - Multistream では stream ごとに `cloneState` した上で、子の `DecodeFECFloat` がさらに
    `cloneState` する (**二重複製**)。
- 提案 (上から順に効果が大きい):
  1. 前段の検証で失敗の可能性を出し切る。`validateFECState` は現在もほぼそれをしている。
     そのうえで、状態を書き換え始めた後にエラーになる経路を洗い出して、そこを無くすか、
     そこで変わる小さな状態だけを退避する。全体の複製は不要になる。
  2. 1 が難しければ、staging 用の decoder を 1 個遅延生成して使い回す
     (`CopyAllStateFrom` で再利用バッファへコピーする)。確保はなくなり、コピーのコストだけが残る。
     M2 とあわせれば、コピーする量も 1/16 以下になる。
  3. Multistream 側は、子の `DecodeFECFloat` を「staging 済み」の内部関数
     (`decodeFECFloat` に gain を掛けて状態を確定する部分) に置き換え、二重複製をやめる。
- 効果: DecodeFEC が約 30 µs 級になり (約 30 倍速)、確保はほぼ 0。
- parity リスク: 低。出力は変えず、トランザクション性 (失敗したら状態を変えない) を保てばよい。
  `plc_fec_semantics_test.go` と `multistream_plc_fec_semantics_test.go` で回帰を確認する。

### M2. Decoder が 32 個の CELT decoder を前もって作る 【最重要】

- 場所: `opus.go:3454-3470`。使う側は `opus.go:4196-4202`
  (`celtDecoders[bw][lm][d.channels-1]`) で、切り替え時に `CopyStateFrom(lastCeltDec)` する。
- 内訳: 1 個あたり `PostFilter.buf` が `decodeBufferSize`=2048 個の float64、つまり
  16 KB × チャネル数ある (`internal/celt/postfilter.go:30`)。ほかに Mode、BandConfig、
  CELTMode×2 (window のコピーと遅延生成される twiddle)、BandProcessor×2 (21 個の `*Band` と
  その `Coeffs`)、overlap、energy の履歴がある。heap profile では `NewPostFilter` だけで
  alloc_space の 8.8% を占めた。
- 段階的な提案:
  - **M2a (即効・リスク低)**: 出力チャネル数が `d.channels` でない側 (`ch-1 != d.channels-1`)
    の 16 個は、見たところ fallback (`opus.go:4201`) 以外では使われていない。作らなければ
    メモリはほぼ半分になる。clone、Reset、`SetPhaseInversionDisabled` のループは nil を
    スキップしているので、そのまま動くはず。
  - **M2b (中)**: (bw, lm) ごとの decoder を初めて使うときに作る。今も切り替え時に
    `CopyStateFrom` で状態を移しているので、遅延生成しても意味は変わらない。ただし生成時に
    `SetDownsample`、phase inversion、stream channels などの設定を `Decoder` 側の値から適用する。
    よくあるストリームは使う構成が 1〜2 個なので、約 1/16 になる。
  - **M2c (大)**: libopus と同じく CELT decoder を 1 個にし、LM と end band をパケットごとに
    引数で渡す。`CopyStateFrom` が不要になり、構成の切り替えも速くなる。
    `highBandE` まわりの扱い (`internal/celt/decoder.go:64-68`) は今すでに libopus 寄りなので、
    単一化には向いた下地がある。
- 効果: 1.42 MB → 約 0.1〜0.2 MB (M2b)、さらに下げる余地あり (M2c)。5.1 の MS decoder は
  5.7 MB → 1 MB 未満。サーバー用途 (同時に多数のストリームを扱う) では一番効く。
- parity リスク: M2a と M2b は低 (状態を移す経路は同じ)。M2c は中で、opusref のフル実行が必要。

### M3. 共有できる不変データをインスタンスごとに複製している

- 場所:
  - `internal/celt/decoder.go:138-144`: `NewModeEx` → `buildBandConfig` (`BandStart` と
    `BandSizes` を毎回 make)、`celtWindow(overlap)` (120 個の float32 を毎回コピー)、
    `dsp.NewCELTMode` (window の float64 版を毎回作り、twiddle も mode ごとに遅延生成)。
  - CELT encoder 側も同じ (`internal/celt/encoder.go:374-393`)。
- 提案: (frameSize, sampleRate, numBands, channels) を鍵にした不変の Mode、BandConfig、
  window、twiddle テーブルをパッケージ全体で共有する (`sync.Once` か、生成コードの静的テーブル)。
  `CELTMode` のうち scratch 部分 (`fwdF`、`bwdBuf` など) と不変部分 (`Window`、`trig`) を
  別の型に分け、不変部分だけを共有する。
- 効果: インスタンスごとに数 KB と数十 alloc を削れる。M2 をやらない場合は 32 倍に効く。
- parity リスク: なし (値は同じ)。

### M4. `BandProcessor` がポインタのスライスと Band ごとの Coeffs を持つ

- 場所: `internal/celt/bands.go:18-48`。`[]*Band` があり、21 個の Band それぞれが
  `Coeffs []float64` を別々に make している。
- 提案: `bands []Band` (値の配列) にし、`Coeffs` は 1 本の連続バッファのスライスにする。
  `CopyAllStateFrom` (`decoder.go:736-743`) は copy 1 回で済むようになる。
- 効果: alloc 数を 1 インスタンスあたり約 44 減らせる (32 decoder なら約 1,400)。
  キャッシュ局所性も良くなる。

### M5. CELT の decode history (`PostFilter.buf`) が float64

- 場所: `internal/celt/postfilter.go:30`、`internal/celt/plc.go:28-35`。PLC のたびに float64 から
  float32 へ全 2048 サンプルを変換している。
- 提案: libopus の `decode_mem` は `celt_sig`=float32 なので、`buf` を `[]float32` にする。
  そうすればメモリは半分になり、PLC での変換コピーもなくなる。
- 前提の確認: `buf` に書き込む値がすべて float32 に丸め済みであれば bit 一致を保てる。
  基本演算 (+ − × ÷ √) では、float32 の値を double で計算して丸めても float32 で直接計算しても
  結果は同じになる (double rounding が無害な条件)。ただし FMA 融合には注意する
  (CI の `No fused multiply-add` チェックが検出してくれる)。
- 効果: CELT decoder 1 個あたり 8 KB × ch (M2 をやらなければ全体で約 400 KB)。PLC も少し速くなる。

### M6. `EncodeFloat32` が呼ぶたびに float64 バッファを確保する

- 場所: `opus.go:595` の `floatPCM := make([]float64, expectedSize)`。
  int16 版と 24bit 版はすでに `e.inputScratch` を再利用している。
- 提案: `e.inputScratch(expectedSize)` に置き換える。int16 版のコメントにあるとおり、
  encoder は保持したいデータを自分でコピーしているので安全。
- 効果: stereo 20 ms で −15 KB/op (18.5 KB → 約 3 KB)。1 行の修正で済む。
- 補足: `TestEncodeInt16AllocsMatchFloat` はあるが、Float32 版の alloc 回帰テストはないので追加する。

### M7. Multistream、Surround、Projection の encode が毎フレーム確保する

- 場所:
  - `multistream.go:447/464/481`: int16、24bit、float32 から float64 への変換バッファ。
  - `multistream.go:518/521`: `packets` と stream ごとの `streamPCM`。
  - `multistream_libopus.go:208/212/215`: `out` (cap 1275)、`streamPCM`、`streamAnalysis`。
  - `surround.go:242`: `e.analyzer.Clone()` (毎フレーム analyzer を丸ごと複製し、
    overlap と preemph をコピー)。
  - `surround.go:250`: stream ごとの `streamMask`。`surround.go:268`: `rates`。
  - `projection.go:269/286/299/638/648/696` と `mapping_matrix.go:97/117`: 行列演算の出力が毎回 make。
- 提案:
  - encoder 構造体に scratch を持たせて使い回す (`streamPCM[stream]`、`packets[:0]`、
    `rates`、`masks`)。
  - Surround の analyzer は「current と next の 2 面」にし、確定したら交換する
    (`Clone` をやめて `CopyInto` に置き換え、確保をなくす)。
  - mapping matrix の乗算を `MultiplyInto(dst, ...)` 形式にする。
- 効果: 5.1 encode 184 KB/op → 1 桁 KB、FOA 139 KB/op → 1 桁 KB を目標にできる。

### M8. Multistream と Projection の decode が毎フレーム確保する

- 場所: `multistream.go:698` (`out`)、`:731/811/922` (float32 版の `out`)、`:793`、
  `:1105/1116` (`splitMultistreamPackets` の `packets` と、最後の stream のデータのコピー)、
  `:1245/1259`。`projection.go:638/648`。
- 提案: decoder に `out` を持たせて再利用する (返したスライスは次の呼び出しまで有効、という
  単一 stream decoder と同じ契約にする)。最後の stream はコピーせずサブスライスで渡す
  (decoder は入力を保持しない)。
- 効果: 5.1 decode 53 KB/op → 数 KB。

### M9. `DecodeFloat32`、`DecodeFloat`、`DecodePLCFloat32` の戻り値が毎回 make

- 場所: `opus.go:4303` 付近、`:5323` 付近。
- 状況: 既存 API を据え置く判断はすでに出ている。
- 提案: **新しい API を追加する** (既存の挙動は変えない):
  `DecodeFloat32Into(data []byte, pcm []float32) (int, error)`、`DecodePLCFloat32Into`、
  Multistream 版も同様。中身は既存の `decodeSoftClipped32` 系に、出力先を渡すだけ。
- 効果: DecodeFloat32 の 9 KB/op が 0 になる (DecodeFloat は 17 KB/op)。

### M10. CELT PLC がフレームごとに大きなバッファを確保する

- 場所: `internal/celt/plc.go`
  - `:28-36`: `mem` (C×2048 float32) と `lp` (1024 float32)。PLC のたびに 8〜16 KB + 4 KB。
  - `:45` `xx`、`:71` `rden`、`:75` `yy`、`:123` `etmp`、`:174` `samples`、`:197` `X`、`:208` `freq`、
    `:245` `exc`、`:263` `firTmp`、`:280` `ext`、`:317` `frame`、`:342` `output`。
  - opus 層: `opus.go:5373/5377/5406` (`decodePLCFloat` の `out`)、`concealOnlyPacket`。
- 提案: `Decoder` に `plcScratch` 構造体を持たせ、すべて再利用する。M5 で `buf` を float32 に
  すれば、`mem` のコピー自体が不要になる (`pitchDownsample32` に `buf` の末尾を直接渡す)。
- 効果: DecodePLC 82 KB/op → ほぼ 0。

### M11. 冗長 (redundancy) フレームのたびに CELT decoder を新しく作る

- 場所: `opus.go:4693` (`decodeRedundancy` → `celt.NewDecoderEx(FrameSize5ms, ...)`)。
- 提案: 5 ms の redundancy 用 decoder を (endBand, ch) ごとに 1 個だけ遅延生成して持ち、
  `Reset` か `CopyStateFrom` で使い回す。M2c (単一 decoder) をやれば、それで代替できる。
- 効果: SILK と CELT の遷移があるたびに約 40 KB 確保していたのがなくなる
  (heap profile では `decodeRedundancy` が 420 KB/300 op)。

### M12. Decoder の SILK 用の汎用 resampler が使われていないように見える

- 場所: `opus.go:3483-3489` (`resampler.NewResampler(silkRate, sampleRate, ch, ...)` で
  `info.resampler` を作る)。参照しているのは clone (`:5910`) と Reset (`:5959`) だけで、
  decode 経路では bit-exact な `silk.Resampler` を使っている。
- 提案: 本当に使われていないことを確かめたうえで削除する。もし fallback として必要なら遅延生成にする。
- 効果: Decoder 1 個あたり最大 6 個の resampler (と、その FIR 状態) を削れる。

### M13. SILK decoder を 3 レート × 2 ch 分、resampler を合計 9 個前もって作る

- 場所: `opus.go:3474-3507`。
- 提案: レートごとに遅延生成する。libopus は SILK decoder を 1 個だけ持ち、内部レートを
  切り替える。`silkActiveRi` による切り替えが今もあるので、遅延生成は無理なく入る。
  stereo の info は mono 用、L 用、R 用の 3 本の resampler を持つが、L と R を必要になったときに
  作るだけでも削れる。
- 効果: 数百 KB 級 (M2 に次ぐ大きさ)。

### M14. `Repacketizer.Out` / `OutRange` が 64 KiB を確保する

- 場所: `repacketizer_libopus.go:271` の `data := make([]byte, 0, min(maxlen, 1<<16))`。
  unbounded の maxlen は `outRangeUnbounded` で巨大な値になる。
- 提案: `totSize` (と、padding や extension の長さ) を先に計算してから、その分だけ確保する。
  あるいは `OutTo(dst []byte)` / `AppendOut` を追加する。
- 効果: 66 KB/op → 実サイズ分だけ (数百 B)。

### M15. SILK encoder がフレームごとに作る小さなスライス (約 100 alloc/frame)

heap profile で確認した主な確保箇所 (alloc_objects の上位):

| 場所 | 中身 | 置き換え先 |
|---|---|---|
| `internal/silk/nlsf_encode.go:328` | `raw := make([]int, order)` (DelDecQuant、1 フレームに複数回) | `[silkMaxLPCOrder]int` |
| `internal/silk/process_gains_flp32.go:23-24` | `ind` と `absInd` | `[silkMaxNBSubframes]int` |
| `internal/silk/encoder.go:1819-1820` | `absIndices` と `e.lastGainSymbols` | 固定長配列か、既存スライスの再利用 |
| `internal/silk/encode_frame_loop.go:96-97,137,139` | `symbols`、`absIdx`、`res.ltpCoeffsQ14`、`res.pitchLags` | scratch |
| `internal/silk/encoder.go:927,1336` | `pitchLags` | `[4]int` |
| `internal/silk/encoder.go:1301` | `capLTPGainIdx` の append コピー | 既存の容量を再利用 |
| `internal/silk/ltp_quant.go:306-308` | `coeffs`、gainIndices のコピー | scratch |
| `internal/silk/nlsf_encode.go:67,117,127,137` | `out`、`target`、`survivors`、`bestRaw` | 固定長配列か scratch |
| `internal/silk/burg.go:20,242` | `A`、`P`、`Q` | 固定長 (D≤24、dd≤12) |
| `internal/silk/nsq_del_dec.go:289` | サブフレームのループ内で `tmp := make([]int16, e.lpcOrder)` | `[16]int16` |
| ほか | `silkA2NLSF`、`ltpQ14ToFloat`、`nlsfToLPCLibopus`、`reconstructNLSFQ15`、`silkNLSFWeightsLaroia`、`silkFloatToQ16`、`stereoEncState.lrToMS`、`int16FrameToFloat`、`floatFrameToInt16` | 同上 |

- 提案: SILK の次数とサブフレーム数には上限がある (16、4、24) ので、ほとんどは固定長配列に
  置き換えられ、エスケープ解析でスタックに置かれる。戻り値をスライスで返す関数は、
  `*[N]T` か呼び出し側のバッファに書き込む形へ変える。
- 効果: SILK 16k mono VOIP の 100 alloc/op → 10 前後。GC の頻度が下がり、多数ストリーム時の
  p99 レイテンシが良くなる。

### M16. CELT encoder がフレームごとに作るスライス

- 場所:
  - `internal/celt/encoder.go:1060`: `traceBandLogE := append([]float64(nil), logE...)`
    (トレースが無効でも毎フレームコピーする)。
  - `internal/celt/encoder.go:1509`: `padded := make([]byte, targetBytes)`。
  - `internal/celt/quant_energy.go:179-180,294-297`: `quantLogE` と `prev`。
  - `internal/celt/rate_alloc.go:55-67`: `Bits`、`EBits`、`FinePriority`、`bits1`、`bits2`、`thresh`、`cap`。
  - `internal/celt/celt_analysis.go:65,172,346-352,501,507,780`: `tmp`、`spreadOld`、`offsets`、
    `importance`、`follower`、`noiseFloor`、`metric`/`path0`/`path1`、`dynalloc`。
- 提案: トレースのコピーは `if tracing` のときだけにする。それ以外は `allocScratch` や
  `analysisScratch` のような既存の scratch パターンにまとめる (帯域数は 21 以下なので
  固定長配列でもよい)。
- 効果: CELT encode の 17〜28 alloc/op を 1 桁にできる。

### M17. 静的テーブルを init で作っている

- 場所: `internal/celt/pvq.go:401` (`pvqUTable` 64×320×uint32 = 80 KB を init 時に計算)。
  `GODEBUG=inittrace=1` では celt パッケージの init が 87 KB・34 alloc だった。
- 提案: `go generate` で生成する定数配列 (rodata) にする。あるいは libopus の `CELT_PVQ_U_ROW`
  のように疎な三角形の表にする (約 5 KB)。
- 効果: 起動時に約 87 KB と約 0.2 ms を削れる。小規模なバイナリや WASM 向けに効く。

### M18. `math/big` への依存 (バイナリサイズ)

- 場所: `internal/celt/exp2.go`。丸めの境界ぎりぎりのときだけ 256 bit の Taylor 展開を使っている。
- 提案: 境界に当たる入力の集合は有限 (`quantLogE + eMeans` が取りうる値の範囲) なので、
  事前に計算したテーブルか double-double (2 つの float64) の評価に置き換えて `math/big` を外す。
- 効果: バイナリサイズ (math/big と strconv の一部) が減る。性能への影響はほぼない。優先度は低い。

---

## 3. 高速化の候補

### P1. SILK del-dec NSQ 内ループのマイクロ最適化 (encode CPU の 34%)

- 場所: `internal/silk/nsq_del_dec.go:455-643`
- 個別の手:
  1. **ループ内の `e.nsq.*` をローカル変数に移す**: `e.nsq.sLTPShpQ14`、`e.nsq.sLTPShpBufIdx`、
     `e.nsq.sLTPBufIdx` をサンプルごとにポインタ越しに読み書きしている (`:480,615-626`)。
     スライスへの書き込みとエイリアスしうるため、コンパイラはレジスタに置けない。
     ローカル変数で回し、ループの後でまとめて書き戻す。
  2. **`% silkDecisionDelay` (=40、2 の冪でない) をやめる**: `:568-572` の剰余 2 回と負数補正を、
     `sIdx--; if sIdx < 0 { sIdx += 40 }` と `lastIdx := sIdx + dd; if lastIdx >= 40 { lastIdx -= 40 }`
     に置き換える。`:330`、`:392` も同様。
  3. **整形 AR ループの次数を固定する**: `shapingLPCOrder` は complexity によって 12/14/16/20/24 の
     偶数のどれか。`switch` で 16、24 などに分け、`*[N]int32` / `*[N]int16` で境界チェックをなくす
     (`:508-524`)。`check_bce` では、この関数の内ループに 25 件の境界チェックが残っていた。
  4. **状態ごとのデータを SoA にするか、ポインタを先に取る**: `delDec[k]` (約 1 KB の構造体) を
     4 状態ぶん順に回ると、`randState[lastIdx]` などがキャッシュラインをまたぐ。
     勝者選択に使う `rdQ10`、`randState`、`seed` だけを `[4]int32` の SoA に分けると、
     分岐と比較が軽くなる。
  5. **`copyDelayedDecisionSuffix` のコピーを減らす**: 5 本の 40 要素配列と sAR2 をすべてコピー
     している (`:698-714`)。libopus も同じなので必須の処理だが、40 要素の巡回バッファのうち
     まだ有効な範囲 (decisionDelay ぶん) だけコピーすれば足りる (要検証)。
  6. **`nsqQuantCandidates` の `lambdaQ10 > 2048` 分岐をフレーム単位で外に出す**: lambda は
     サブフレーム内で一定なので、関数を 2 種類用意して切り替える。
- 期待: NSQ 部分で 10〜25% (encode 全体で 3〜8%)。
- parity リスク: 低 (整数演算の順序を変えない限り bit 一致)。golden digest で確認できる。

### P2. SILK レートループの NSQ 回数を計測し、必要なら削る

- 場所: `internal/silk/encode_frame_loop.go` (最大 6 反復)、`closedLoopNSQWithRateScale`。
- 提案: まず反復回数のヒストグラムを取る (計測用の一時カウンタで十分)。CBR や maxBits に
  張り付いたケースで 2 回以上になる割合が高いなら、libopus と同じ順序を保ったまま
  早期終了できる条件 (gains ID が同じなら NSQ を再実行しない、という libopus 既存の短絡を含む)
  がすべて移植されているか照合する。
- 期待: CBR 系のワークロードで encode が最大で数十 % 速くなる (VBR では 1 反復なので効果は小さい)。

### P3. LBRR (FEC) フレームの NSQ

- 場所: `internal/silk/lbrr.go`、`generateLBRRFrame`。
- 提案: `SetInbandFEC(true)` かつロス率 > 0 のとき、LBRR 用に NSQ がもう 1 回走る。libopus と
  同じ処理なので削れないが、P1 の最適化がそのまま 2 倍効く。計測して、FEC 有効時専用の
  ベンチマーク (`BenchmarkPerfDefault/encode/fec`) を追加する。

### P4. Legacy policy の帯域検出が素朴な FFT を毎フレーム実行する

- 場所: `bandwidth_detect.go:126-169`、`internal/dsp/fft.go:11-56` (`FFT` がバタフライごとに
  `math.Cos`/`math.Sin` を呼ぶので N log N 回の三角関数計算)、`RealFFT` (複素配列を別に確保)。
  さらに Hann 窓をフレームごとに `math.Cos` で再計算し、`mono`、`buf`、`power`、FFT の出力の
  計 4〜5 回確保している。
- 提案: すでにある `opusFFTPlan` (twiddle は事前計算済み) か、2 の冪の実数 FFT プランを
  キャッシュして使う。Hann 窓は長さごとにキャッシュする。scratch は encoder に持たせる。
- 期待: 検出処理が 10 倍以上速くなり、確保は 0 になる。Legacy policy の encode 全体では数 %。
- parity リスク: Legacy policy の判定は閾値の比較なので、FFT の誤差が閾値付近の判定を変える
  可能性はゼロではない。golden digest (`TestPerfPredictivePacketRegression`) で確認する。

### P5. CELT stereo の theta RDO で range coder 全体を Clone している

- 場所: `internal/celt/quant_pvq.go:1304-1319` (`enc.Clone()` を 2 回、`xSave`、`ySave`、
  `xSave2`、`ySave2`、`normSave2` を append で確保)。`internal/entcode/encoder.go:186`
  (`Clone` はこれまでに書いた `buf` 全体と `endBytes` をコピーする)。
- 問題: stereo の帯域 (i < intensity) ごとに、O(書き込み済みのバイト数) のコピーと 7 回の確保が起きる。
- 提案:
  - range coder の「軽量 checkpoint」型を作る: `len(buf)`、`len(endBytes)`、スカラー状態だけを
    保存する。carry は `rem` と `ext` で保留されているので、書き込み済みの `buf` は確定している。
    復元は truncate とスカラーの書き戻しだけで済む。勝った側の結果 (encSave2) だけは、保存位置
    以降のバイト列をコピーする必要がある (libopus の `bytes_save` と同じやり方)。
  - `xSave` などの保存先は、`ctx` に置いた `[maxN]float32` の scratch を再利用する。
- 期待: stereo CELT encode で数 %。確保は帯域数 × 7 だけ減る。

### P6. CELT decoder の deemphasis と出力変換を 1 パスにまとめる

- 場所: `internal/celt/decoder.go:767-792` (`applyDeemphasis`、flat 4.9%)。この後に opus 層で
  float32 化、gain、softclip の判定、int16 化を行い、合計で 3〜4 回データを走査している。
- 提案: libopus の `deemphasis()` は、downsample、出力形式への変換、interleave を 1 ループで
  まとめて行う。同じ融合を入れて、float64 の中間配列を書き出さないようにする。
  deemphasis 自体は IIR で直列依存があるので、ベクトル化はできない。
- 期待: decode で 3〜6%。メモリ帯域も減る。

### P7. SILK decoder の出力経路で 16→48 kHz 変換と interleave をまとめる

- 場所: `silk.iirFIRInterpol` (flat 7.2%)、`opus.resampleSILK` (cum 14.9%)、
  `interleaveSILKOutInto`、`float32ToInt16`。
- 提案:
  - 比率が整数 (8→48、12→48、16→48) のときは `indexQ16` の刻みが周期的なので、
    `tableIndex` の系列 (16→48 なら 0、4、8 の繰り返し) を展開した特化版を用意する。
    `(indexQ16 & 0xFFFF) * 12 >> 16` の計算と `c1` の逆参照がなくなる。
  - stereo で L/R を別々に resample してから interleave している部分は、resampler が
    stride 付きで直接 interleave 先に書き込めるようにする。
- 期待: SILK decode で 5〜10%。

### P8. `float64` を内部表現に使っている (全体)

- 観察: ほぼ全段 (CELT の X、band energy、PCM のパイプライン、SILK の FLP 解析) が
  `[]float64` で保持しつつ、各所で `float64(float32(x))` (`silkFloat32Value`、`f32`) で
  丸めている。heap とメモリ帯域が 2 倍になるうえ、変換命令も増える。
- 提案: libopus の float ビルドに合わせて、モジュールごとに段階的に `float32` で保持するよう移す。
  2.M5 に書いたとおり、+ − × ÷ √ の単独演算は float32 で直接計算しても double で計算して
  丸めても同じ結果になる。ただし次の 2 点に注意する。
  - (a) double で累積している箇所 (`silkEnergyFLP32`、`silkInnerProductFLP32` など。libopus も
    double で累積する) は double のまま残す。
  - (b) 式の途中で丸めていなかった箇所を float32 にすると結果が変わる。CI の
    `No fused multiply-add` ゲートは arm64 の FMA を検出してくれる。
- 期待: 長期的には encode と decode でそれぞれ 10〜20%、常駐メモリも 30〜50% 減る。
  ただし改修の範囲は最大級。
- 進め方: 葉の関数 (`internal/dsp`、CELT の band 処理) から始め、1 関数ずつ golden digest で確かめる。

### P9. `celtExp2RoundedFloat32` を帯域ごとに呼んでいる

- 場所: `internal/celt/decoder.go:359`、`plc.go:211`。毎回 `math.Exp2`、`Nextafter32`、
  境界の判定を行う。
- 提案: 入力は `quantLogE + EMean(i)` (Q 刻みの有限集合ではない) なのでテーブル化は難しい。
  一方、境界判定の前に「明らかに境界から遠い」ことを安く判定する手はある
  (たとえば `|y - result64|` と ULP の比較だけを先に行う)。
- 期待: 小さい (decode で 1% 未満)。優先度は低い。

### P10. `decodePulses` と `cwrsi` の U テーブル参照

- 場所: `internal/celt/pvq.go:353-363` (`celtPVQU` で n と k を入れ替え、範囲判定、2 次元表引き)、
  `cwrsiLibopusInto` (flat 3.8%)。
- 提案: libopus と同じく、行ポインタ (`pvqUTable[n][:]`) をループの外で取り、内ループで
  1 次元の参照にする。k が n 以上か未満かでループを分けて、入れ替え分岐をなくす。
- 期待: CELT decode で 1〜3%。

### P11. `entcode.DecodeIcdf` と `normalize`

- 場所: `internal/entcode/decoder.go`。
- 提案: `normalize` の 1 バイト読み出し (`readByte`) の境界チェックを、末尾の番兵か事前の長さ
  判定でなくす。`DecodeIcdf` は `ftb` を定数として呼ぶ箇所がほとんどなので、`logp` ごとの
  特化版 (インライン化しやすい小さな関数) を用意する。
- 期待: decode で 2〜4%。

### P12. CELT IMDCT (`imdctRawStatic`、cum 10.7%)

- 場所: `internal/dsp/fft_opus.go`、`mdct_celt.go`。
- 提案: KISS FFT の bfly2/3/4/5 は Go としてはもう良い形。twiddle の stride 参照を、ステージ
  ごとに連続配列へ並べ替えておく (libopus の SIMD 版と同じデータ配置) と、キャッシュと
  境界チェックの面で有利になる。bit 一致を保つには加算の順序を変えないこと。
- 期待: decode で 2〜4%。

### P13. Multistream と Projection の stream を並列に encode/decode する (オプトイン)

- 場所: `multistream.go:518-530` (stream を逐次処理)、`multistream_libopus.go:208-`。
- 提案: stream 同士は独立 (MS の bitrate 配分は前段で決まる) なので、`SetParallelism(n)` の
  ようなオプトイン API を足し、goroutine で並列化する。既定は逐次のままにする (決定性と
  スケジューリングのオーバーヘッドのため)。
- 期待: 5.1 や高次 Ambisonics の 1 パケットあたりのレイテンシが stream 数に応じて短くなる
  (5.1 で最大 3〜4 倍)。総スループットは変わらない。

### P14. encode 出力の所有権と、パケット組み立てのコピー

- 場所: `opus.go:620-634` (padding するときに `PacketParse`、`packOpusFramesCode3`、
  `append([]byte{toc|3}, payload...)` で 3 回コピー)、`internal/celt/encoder.go:1509`
  (`padded`)、`packOpusFramesCode3Run`。
- 提案: libopus の `opus_encode(..., data, max_data_bytes)` に相当する
  `EncodeTo(dst []byte, pcm, frameSize) (int, error)` / `AppendEncode` を追加する。内部では
  TOC とフレームを 1 個のバッファに直接書き込む。padding も最初の組み立て時にまとめて行う。
- 期待: 1 パケットあたり 1〜3 回の確保とコピーがなくなる。

### P15. `strictSpeechLikeFrame` などテスト用信号の生成がプロファイルを乱している

- 場所: `opus_silk_encoder_test.go:1791`、`perfMusicFrame`。heap profile の alloc_space で 18.8% と
  5.6% を占めていた。
- 提案: ベンチマーク本体には関係ないが、`BenchmarkPerfDefault` が入力を毎回生成している
  なら、生成を `b.ResetTimer()` の前に移す (perfInputFrames は前に移っている。DecodeFEC 系の
  ベンチマークで生成が計測区間に入っていないかを確認する)。
- 効果: 測定精度が上がる (コードの性能そのものではない)。

### P16. ベンチマークの穴を埋める (回帰を防ぐため)

- 現状のベンチマークは、Legacy policy の `BenchmarkPerf`、既定 policy の `BenchmarkPerfDefault`、
  `BenchmarkPerfDefaultDecodeFEC` (FEC 付きパケットの通常 decode)。
- 足りないもの: **`DecodeFEC` API そのもの**、`DecodePLC`、`EncodeFloat32`、Multistream と
  Surround と Projection の encode/decode、Repacketizer、oggopus の Reader/Writer、
  そして `NewDecoder`/`NewEncoder` の構築コスト。今回見つけた M1、M2、M6、M10、M14 は
  どれもこの穴にあった。
- 提案: 上の各 API に `b.ReportAllocs()` 付きのベンチマークを足す。あわせて
  `TestCoreAllocationRegression` 形式の alloc 上限テストも足す (たとえば Decoder の構築は
  200 KB 以下、DecodeFEC と DecodePLC と EncodeFloat32 は 0〜数 alloc)。

---

## 4. oggopus

### O1. Ogg CRC がビットごとの計算

- 場所: `oggopus/page.go:208-239` (`checksum`、`checksumWithZeroedField`)。1 バイトあたり 8 回の
  ループを回している。
- 提案: 256 要素のテーブル方式 (8 倍前後速い)。さらに slicing-by-8 (16 倍前後) も使える。
  `checksumWithZeroedField` は、ゼロにするフィールドの範囲 22〜26 を別に処理し、ループ内の
  分岐をなくす。
- 期待: ページの読み書きにかかる CPU が数倍〜10 倍速くなる。大きなファイルを走査したり
  シークしたりするときに効く。

### O2. `ReadPage` が 5 回確保し、二重にコピーする

- 場所: `oggopus/page.go:166-198` (`header`、`rest`、`body`、`encoded` を確保し、それを
  `ParsePage` が `Segments` と `Data` へもう一度コピーする)。
- 提案: 27 + 255 バイトの header 用 scratch を 1 つ用意し、body は 1 回だけ確保する。
  CRC は header、lacing、body を順に流して計算する (連結バッファは要らない)。
  `Page.Segments` と `Page.Data` はその確保済みバッファのサブスライスにする。
  `ParsePage` が「caller-owned copy」を返す契約はそのまま保つ (ReadPage の内部では
  コピーしない経路を使う)。
- 期待: 1 ページあたりの確保 5 → 1〜2、コピー 2 → 1。

### O3. `PacketReader` が完了したパケットごとにコピーして返す

- 場所: `oggopus/packet.go:162,178` (`completed` スライスとパケットごとの append コピー)、
  `:373-374` (writer でも Segments と Data をコピー)。
- 提案: 継続していないパケットはページバッファのサブスライスで返す (所有権の契約をドキュメントに
  明記するか、`NextInto` を追加する)。
- 期待: 確保回数と GC 負荷が減る。

### O4. seek の `scanNextPage` が毎回 32 KiB を確保する

- 場所: `oggopus/seek.go:208-209`。二分探索の反復ごとに呼ばれる。
- 提案: Reader に 32 KiB のバッファを持たせて再利用する。
- 期待: 1 回のシークで 64 回 × 32 KiB の確保がなくなる。

---

## 5. 構造上の提案 (中長期)

| # | 内容 | 根拠 |
|---|---|---|
| S1 | 単一の CELT decoder と単一の SILK decoder にする (libopus と同じ構成) | M2、M11、M13 をまとめて解決し、切り替え時の `CopyStateFrom` も不要になる |
| S2 | 「不変の Mode と可変の scratch」を型で分ける | M3。共有テーブルを安全に使えるようにする |
| S3 | decoder のトランザクション性を「失敗しない前段検証 + 書き換え」に統一する | M1。`cloneState` を全廃できる |
| S4 | `...Into` / `Append...` 形式の、呼び出し側バッファを使う API を一式そろえる | M9、M14、P14。既存 API は互換のため残し、内部では新 API を呼ぶ |
| S5 | float32 で保持する方式への段階移行 | P8、M5 |
| S6 | alloc 予算テストで回帰を防ぐ | P16。性能改善が後続の変更で崩れないようにする |

---

## 6. 推奨の着手順

1. **すぐ着手できる (半日〜1 日、parity リスクはほぼない)**: M6 (EncodeFloat32 の 1 行)、
   M14 (Repacketizer の確保サイズ)、M2a (使わない側の 16 個の CELT decoder を作らない)、
   M12 (未使用の resampler を削除。使われていないことの確認が要る)、M16 のトレース用コピー、
   O1 (CRC のテーブル化)、O4。
2. **効果が大きい (1〜3 日)**: M1 (DecodeFEC の staging をやめる)、M2b (CELT decoder の遅延生成)、
   M10 (PLC の scratch)、M7 と M8 (MS、Surround、Projection の scratch)、M11。
3. **CPU (数日、golden digest で確認)**: P1 (NSQ のマイクロ最適化)、P5 (theta RDO の checkpoint)、
   P6 と P7 (出力経路の融合)、P4 (Legacy の FFT)。
4. **大きな改修**: S1 (単一 decoder 化)、S5 (float32 での保持)。
5. **並行して進める**: P16 と S6 (ベンチマークと alloc 予算テストの追加)。上の各項目の効果を
   この仕組みで測って記録する。

## 7. 受け入れ条件 (各候補を採用するとき)

- `go test ./...` と `go test -tags opusref ./...` (PowerShell で実行) がすべて PASS する。
- `TestPerfPredictivePacketRegression` と `TestPerfDefaultPolicyPacketRegression` の digest が変わらない。
- RFC 6716/8251 の公式ベクタが 12/12。
- `go vet ./...`、`go fmt ./...`、CI の FMA ゲートが通る。
- 前後の比較を `benchstat` で取り、改善を数値で記録する (目安は 10 回以上の実行)。

## 8. 検証コマンド

```bash
go test -run '^$' -bench 'BenchmarkPerf' -benchmem -count 10 . | tee new.txt
benchstat old.txt new.txt
go test -run 'TestPerfPredictivePacketRegression|TestPerfDefaultPolicyPacketRegression|TestCoreAllocationRegression|TestEncodeInt16AllocsMatchFloat' .
go build -gcflags='github.com/darui3018823/opus/internal/silk=-d=ssa/check_bce/debug=1' ./internal/silk 2>&1 | grep nsq_del_dec.go
GODEBUG=inittrace=1 go run <import-only program>
```
