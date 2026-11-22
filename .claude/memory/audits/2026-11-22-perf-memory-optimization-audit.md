Opus Pure Go パフォーマンス・メモリ効率化 網羅的調査レポート
Date: 2026/11/22
Author: Gemini 4 Neon with High effort

———

## 目次

- [1. エグゼクティブサマリー](#1-エグゼクティブサマリー)
- [2. ベースライン計測とプロファイル分析](#2-ベースライン計測とプロファイル分析)
- [3. 高インパクト施策 Top 15（優先度・リスク・期待効果）](#3-高インパクト施策-top-15優先度リスク期待効果)
- [4. サブシステム別・網羅的改善カタログ（全 81 項目）](#4-サブシステム別網羅的改善カタログ全-81-項目)
  - [4.1 `internal/entcode`（レンジコーダ：EC-01〜EC-07）](#41-internalentcodeレンジコーダec-01ec-07)
  - [4.2 `internal/dsp`（FFT / MDCT / 数学ユーティリティ：DSP-01〜DSP-07）](#42-internaldspfft--mdct--数学ユーティリティdsp-01dsp-07)
  - [4.3 `internal/celt`（CELT コーデック・解析・PLC・サラウンド：CELT-01〜CELT-17）](#43-internalceltcelt-コーデック解析plcサラウンドcelt-01celt-17)
  - [4.4 `internal/silk`（SILK コーデック・NSQ・ピッチ・NLSF・リサンプラ：SILK-01〜SILK-18）](#44-internalsilksilk-コーデックnsqピッチnlsfリサンプラsilk-01silk-18)
  - [4.5 `internal/resampler`（ポリフェーズ FIR リサンプラ：RS-01〜RS-04）](#45-internalresamplerポリフェーズ-fir-リサンプラrs-01rs-04)
  - [4.6 ルート `opus` パッケージ（Encoder / Decoder / FEC / パケット：OPUS-01〜OPUS-11）](#46-ルート-opus-パッケージencoder--decoder--fec--パケットopus-01opus-11)
  - [4.7 Multistream / Surround / Projection / MappingMatrix（MS-01〜MS-07）](#47-multistream--surround--projection--mappingmatrixms-01ms-07)
  - [4.8 `internal/extensions` & `oggopus` コンテナ（OGG-01〜OGG-06）](#48-internalextensions--oggopus-コンテナogg-01ogg-06)
  - [4.9 横断的・アーキテクチャ施策（ARCH-01〜ARCH-04）](#49-横断的アーキテクチャ施策arch-01arch-04)
- [5. 推奨実装フェーズと検証方針](#5-推奨実装フェーズと検証方針)

---

## 1. エグゼクティブサマリー

本レポートは、`github.com/darui3018823/opus`（Pure Go Opus 実装）のランタイムコード全体（`internal/entcode`, `internal/dsp`, `internal/celt`, `internal/silk`, `internal/resampler`, `internal/extensions`, ルート `opus`, `oggopus`）を精読し、`go test -bench -benchmem`、CPU/メモリプロファイル（`pprof`）、およびコンパイラのエスケープ解析（`-gcflags="-m"`）と突合して抽出した **全 81 件** の高速化・メモリ効率化施策を網羅的にまとめたものである。

### 全体所見（ボトルネックの構造）

1. **デコード側の 0-alloc 化は目前（現在 1〜4 allocs/op）**
   - SILK デコードの残り 1 alloc/op は `entcode.NewDecoder` 単体。
   - CELT デコードの残り 4 allocs/op は `entcode.NewDecoder`, `UnquantizeCoarseEnergy`, `celtTFDecode`, `decodeDynalloc` に完全特定できた。
   - 一方で **`NewDecoder` 初期化が 32 個の `celt.Decoder` と 6 個の `silk.Decoder` と 15 個の未使用リサンプラを一括生成して約 4.1 MB を消費**しており、さらに **`DecodeFECFloat` が毎呼び出し `cloneState() -> NewDecoder()` を実行**している（Multistream FEC ではストリームあたり 2 重クローン）。ここはメモリ効率・レイテンシ双方で最大級の改善余地がある。
2. **エンコード側のアロケーション集中点（SILK 104〜187 allocs/op, Hybrid 74〜175 allocs/op, CELT 16〜21 allocs/op）**
   - SILK エンコードは `silkNLSFEncode` / `silkNLSFDelDecQuant`（最大 48+ allocs/frame）、`silkProcessGainsFLP32`、`selectLTPGainsVQWithGain`、`residual_energy_flp.go`、`stereo_pred.go` の小配列ヒープ確保が大半を占める。さらに **`encodeRangeFrame` が `analyzeNLSF`（Burg LPC + `silkA2NLSF` + 16 生存者 NLSF 探索）を 1 フレームにつき 2 回実行**している。
   - CELT エンコードは、常時有効な `FrameTrace` コピー（~35 KB/frame）、`quantCoarseEnergy` の `enc.Clone()`×2、`complexity >= 8` 時の `thetaRdo`（バンドあたり 8〜11 allocs、フレームあたり 100+ allocs）および `OpusFFTScaled32`（3.84 KB/frame）が主要因。`ModePolicyLegacy` では毎フレーム `bandwidth_detect.go` の FFT が ~55 KB/frame を確保している。
3. **CPU ホットスポット（プロファイル実測）**
   - **CELT デコード**: `cwrsiLibopusInto`（16.3%）、`PostFilter.combFilterConst`（7.0%）、`float32ToInt16`（7.0%）、`entcode.Decoder.{Decode,TellFrac}`（9.3%）、`opusFFTPlan.bfly{3,4,5}`。さらに `BandProcessor` の `float32 -> float64 -> amp^2 -> Sqrt -> float64 -> float32` ラウンドトリップと、80 KB の `pvqUTable`（L1d キャッシュ溢れ）が効いている。
   - **CELT エンコード**: `pitchXcorr32`（7.3%）、`quantBand` / `opPVQSearch` / `icwrsLibopus`（31.7% cum）、`haar1`、`spreadingDecision32`、`transientAnalysis32`、`quantCoarseEnergyImpl`。complexity ≥ 7 では `tonality.go` の `gemmAccum`（列ストライドアクセス）と重複エネルギー計算が加わる。
   - **SILK デコード**: `iirFIRInterpol` + `up2HQInternal`（23.1%）、`synthesize` + `silkLPCAnalysisFilter` + `silkSMULWB`（28.9% cum）、`entcode.Decoder.DecodeIcdf` / `normalize`、`stereoMSToLRCore`。
   - **SILK / Hybrid エンコード**: `silkNoiseShapeQuantizerDelDec`（29〜33% cum）、`silkWarpedAutocorrelationFLP32`（15.6%）、`buildLPCInPreInto`（11.9%）、`silkInnerProductFLP32`（8.9%）、`silkFindPitchLagsFLP32`（`xcorrKernelFLP` の `float64↔float32` 変換と 10.88 KB の値返し配列）。
4. **周辺コンポーネント（`oggopus` / `resampler` / `projection` / `multistream`）**
   - `oggopus` の Ogg CRC32 がビット単位ループ（テーブル化で 8〜25× 高速化可能）、`ReadPage` / `PacketWriter.flush` で 1 ページあたり 3 重コピー。
   - `internal/resampler` の多相 FIR 係数テーブルが転置レイアウト（ストライド最大 512 B/tap）かつ内ループ内に境界分岐あり。
   - `ProjectionDecoder.demix*` のループ順序が `(col, i, row)` でバッファ全体を最大 36 周走査（`(i, col, row)` への入れ替えでビット完全一致のままキャッシュ効率を大幅改善可能）。

### ビット完全一致（`ModePolicyLibopus`）に関する設計原則

本リポジトリは `ModePolicyLibopus` において scalar float 版 libopus 1.6.1 とのビット完全一致および `TestPerfPredictivePacketRegression` のゴールデンダイジェスト一致を重視している。したがって各項目に **パリティリスク分類** を明記した：
- **[Exact]**: 演算結果・丸め順序が 100% 同一（メモリ再利用、スタック化、BCE、テーブル化、ループ交換で加算順不変など）。
- **[Exact-Careful]**: 浮動小数点の丸め順序（特に FMA 回避の明示的 `float32(...)` ラップ）を守れば完全一致。
- **[Behavioral/Verify]**: アルゴリズム簡略化や冗長呼び出し削減によりパリティ検証（libopus oracle / ゴールデンテスト）が必須なもの。

---

## 2. ベースライン計測とプロファイル分析

計測環境: `windows/amd64`, `11th Gen Intel(R) Core(TM) i7-11700 @ 2.50GHz`, Go `1.27.4` (`go.mod` `1.24.11`).

### 2.1 ルートパッケージ主要ベンチマーク

#### `BenchmarkPerfDefault`（既定 `ModePolicyLibopus` / complexity 5）

| ベンチマーク | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `encode/celt/mono/96k` | 118,352 | 1,906 | 16 |
| `encode/celt/stereo/128k` | 184,015 | 3,069 | 17 |
| `encode/silk/mono/12k` | 421,062 | 6,921 | 105 |
| `encode/silk/stereo/12k` | 423,604 | 6,901 | 104 |
| `encode/hybrid/mono/24k` | 231,657 | 6,479 | 74 |
| `encode/hybrid/stereo/36k` | 625,623 | 31,183 | 175 |
| `DecodeFEC/voip/mono/20k` | 25,420 | 1,473 | 4 |
| `DecodeFEC/voip/stereo/24k` | 50,660 | 4,322 | 2 |
| `DecodeFEC/voip/mono/32k` | 49,165 | 645 | 4 |

#### `BenchmarkPerf`（`ModePolicyLegacy` エンコード & 共通デコード）

| ベンチマーク | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `encode/celt/mono/48k/20ms` | 218,790 | 55,885 | 20 |
| `decode/celt/mono/48k/20ms` | 34,690 | 634 | 4 |
| `encode/celt/stereo/48k/20ms` | 289,480 | 56,931 | 21 |
| `decode/celt/stereo/48k/20ms` | 60,754 | 824 | 4 |
| `encode/silk/mono/48k/20ms` | 413,396 | 6,961 | 109 |
| `decode/silk/mono/48k/20ms` | 26,338 | 100 | 1 |
| `encode/silk/stereo/48k/20ms` | 652,697 | 29,311 | 187 |
| `decode/silk/stereo/48k/20ms` | 50,245 | 112 | 1 |
| `encode/hybrid/mono/48k/20ms` | 344,695 | 60,720 | 79 |
| `decode/hybrid/mono/48k/20ms` | 48,826 | 641 | 4 |
| `encode/hybrid/stereo/48k/20ms` | 576,347 | 83,748 | 154 |
| `decode/hybrid/stereo/48k/20ms` | 85,619 | 858 | 4 |

#### サブパッケージ・ベンチマーク

| パッケージ / ベンチマーク | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `celt.BenchmarkDecoder` | 16,527 | 631 | 4 |
| `celt.BenchmarkEncoder` | 86,403 | 1,298 | 12 |
| `celt.BenchmarkRateAllocator` | 608.1 | 944 | 8 |
| `silk.BenchmarkNLSFQuantization` | 1,523 | 1,712 | 45 |
| `silk.BenchmarkLPCAnalysis` | 3,357 | 2,432 | 13 |
| `silk.BenchmarkGainQuantization` | 88.8 | 32 | 1 |
| `silk.BenchmarkPitchAnalysis` | 45,214 | 0 | 0 |
| `entcode.BenchmarkDecodeIcdf` | 3,984 | 96 | 1 |
| `dsp.BenchmarkRealFFT1024` | 101,994 | 32,768 | 2 |
| `dsp.BenchmarkMDCTForward512` | 21,819 | 20,480 | 4 |
| `resampler.BenchmarkResampler48to16` | 23,710 | 13,570 | 3 |
| `resampler.BenchmarkResampler16to48` | 65,897 | 19,072 | 3 |

### 2.2 `pprof` 実測ホットスポット要約

- **CELT Stereo Encode (`184 µs`)**: `quantBand` 31.7% cum（うち `opPVQSearch` 4.9%, `icwrsLibopus` 4.9%）、`runPrefilter` 17.1% cum（うち `pitchXcorr32` 7.3%）、`quantCoarseEnergyImpl` 7.3% cum、`haar1` 4.9%、`spreadingDecision32` 4.9%、`transientAnalysis32` 4.9%。アロケーションは `entcode.NewEncoder` / `writeByteAtEnd` / `Bytes` + `quantCoarseEnergy` + `encodeRange` に集中。
- **SILK Stereo Encode (`424 µs`)**: `silkNoiseShapeQuantizerDelDec` 33.3% cum、`buildLPCInPreInto` 11.9%、`silkSMULWB` 9.5%、`silkInnerProductFLP32` 4.8%、`silkWarpedAutocorrelationFLP32` 4.8%。アロケーションは `silkProcessGainsFLP32`, `silkNLSFEncode`, `silkNLSFDelDecQuant`, `reconstructCurrentPitchLags`, `silkBurgModifiedFLP`, `silkFindLTPFLP`。
- **Hybrid Stereo Encode (`626 µs`)**: `silkNoiseShapeQuantizerDelDec` 28.9% cum、`silkWarpedAutocorrelationFLP32` 15.6%、`silkSMULWB` 11.1%、`silkInnerProductFLP32` 11.1% cum、`encodeRange` 8.9% cum、`cltMDCTForward` 4.4% cum。アロケーションは `int16FrameToFloat`（ステレオ変換だけで 17% space）、`silkNLSFEncode` / `silkNLSFDelDecQuant` / `nlsfToLPCLibopus` / `reconstructNLSFQ15` / `silkGainsQuant` / `lrToMS`。
- **CELT Stereo Decode (`61 µs`)**: `quantAllBandsImpl` 39.5% cum（うち `cwrsiLibopusInto` 18.6% cum）、`PostFilter.combFilterConst` 7.0%、`float32ToInt16` 7.0%、`entcode.Decoder.{Decode,TellFrac}` 9.3%。アロケーションは 4 件のみ（`NewDecoder`, `UnquantizeCoarseEnergy`, `celtTFDecode`, `decodeDynalloc`）。また `opus.NewDecoder` 単体で **約 4.14 MB** を確保。
- **SILK Stereo Decode (`52 µs`)**: `synthesize` 28.9% cum（うち `silkLPCAnalysisFilter` 7.7%, `silkSMULWB` 7.7%, `silkLPCPredictQ10` 9.6% cum）、`iirFIRInterpol` 17.3% cum、`up2HQInternal` 9.6%、`decodePulses` 7.7% cum、`stereoMSToLRCore` 3.9%。アロケーションは `entcode.NewDecoder` の 1 件のみ。
- **Hybrid Stereo Decode (`89 µs`)**: `synthesize` 21.3% cum、`CLTMDCTBackwardInto` 14.9% cum（`bfly4` 6.4%, `bfly5` 4.3%, `bfly3` 2.1%）、`DecodeIcdf` + `normalize` 14.9%、`libopusFloat32ToInt16` 4.3%、`cwrsiLibopusInto` 4.3%。

---

## 3. 高インパクト施策 Top 15（優先度・リスク・期待効果）

| 順位 | ID | 対象 | 施策概要 | 期待効果 | リスク |
|---:|---|---|---|---|---|
| 1 | **OPUS-01** | `opus.go` | `NewDecoder` の 32 CELT + 6 SILK + 15 未使用リサンプラの即時確保を遅延化・単一インスタンス化・死コード削除 | 初期化メモリ **~4.1 MB → ~200 KB（-95%）**、初期化時間大幅短縮 | `[Exact]` |
| 2 | **OPUS-02** | `opus.go`, `multistream.go` | `DecodeFECFloat` の毎回 `cloneState() -> NewDecoder()` 廃止（検証先行 or 軽量ロールバック）、Multistream の 2 重クローン解消 | FEC 呼び出し **100× 以上高速化・数 MB/call 削減** | `[Exact]` |
| 3 | **SILK-01** | `silk/nlsf_encode.go`, `silk/burg.go`, `silk/find_lpc.go` | NLSF/LPC 探索のスタック固定配列化（`[16]`）+ `out0Table/out1Table` 定数化 + `sort.SliceStable` の部分挿入ソート置換 | SILK エンコード **-60〜90 allocs/op**、`NLSFQuantization` 45→0 allocs | `[Exact]` |
| 4 | **SILK-02** | `silk/encoder.go` | `encodeRangeFrame` の `analyzeNLSF` 2 重実行（bootstrap + 本番）の排除／統合 | SILK/Hybrid エンコード **CPU 10〜20% 削減** + アロケーション半減 | `[Behavioral/Verify]` |
| 5 | **SILK-03** | `silk/ltp_quant.go`, `process_gains_flp32.go`, `residual_energy_flp.go`, `lpc_in_pre.go`, `stereo_pred.go` | 小スライス（`nbSubfr <= 4`）の固定配列化、スクラッチ再利用、`buildLPCInPre` の重複計算廃止 | SILK/Hybrid エンコード **-40〜80 allocs/op**, -20 KB/op（ステレオ） | `[Exact]` |
| 6 | **CELT-01** | `celt/quant_pvq.go` | `thetaRdo` のバンド毎 `enc.Clone()`×2 + 5 スライス確保を `quantAllBandsScratch` へ移行 | complexity ≥ 8 ステレオ CELT **-100〜150 allocs/op** | `[Exact]` |
| 7 | **CELT-02** | `celt/encoder.go` | 常時有効な `FrameTrace` コピー（~35 KB/frame + 11 スライス）を SILK 同様のオプトインフラグ化 | CELT エンコード **CPU・メモリ帯域大幅削減、Encoder 保持 -35 KB** | `[Exact]` |
| 8 | **CELT-03** | `celt/decoder.go`, `celt/bands.go` | `BandProcessor` の `float32 -> float64 -> amp^2 -> Sqrt -> float64` 往復コピー廃止（`X []float32` インプレース復調） | CELT デコード **CPU 5〜10% 削減** + 86 初期化 allocs 削減 | `[Exact-Careful]` |
| 9 | **CELT-04** | `celt/pvq.go` | 80 KB `pvqUTable` を libopus 方式の圧縮表現（~5 KB, `CELT_PVQ_U_ROW`）へ変更し L1d キャッシュ常駐化 + `cwrsiLibopusInto` 最適化 | CELT デコード最大ホットスポット `cwrsiLibopusInto`（16.3%）**20〜40% 高速化** | `[Exact]` |
| 10 | **EC-01,02,03** | `entcode/` | `ext = rng/ft` キャッシュによる除算半減、Laplace の 2 冪シフト（`EncodeBin/DecodeBin`）、`Reset` + 単一フラットバッファ化 | 全モードデコード **0 alloc 達成の要** + エントロピー符号化 **15〜30% 高速化** | `[Exact]` |
| 11 | **SILK-04** | `silk/nsq_del_dec.go` | `% 40` 剰余の条件折り返し化、`sAR2` ループ展開 & `warp==0` 分岐、BCE、部分クリアによる毎回 ~9.6 KB ゼロクリア削減 | SILK 最大 CPU ホットスポット（29〜33%）**15〜25% 高速化** | `[Exact]` |
| 12 | **CELT-05** | `celt/decoder.go`, `celt/quant_energy.go` | CELT デコード残存 4 allocs（`NewDecoder`, `UnquantizeCoarseEnergy`, `celtTFDecode`, `decodeDynalloc`）の完全ゼロ化 | CELT / Hybrid デコード **4 allocs/op → 0 allocs/op** | `[Exact]` |
| 13 | **CELT-06** | `celt/tonality.go`, `dsp/fft_opus.go` | `gemmAccum` の重み事前転置（`float32` 行連続化）、重複 `binEnergy`・重複 `math.Log`・対称行列 2 重計算の削除、`OpusFFTScaled32` スタック化 | complexity ≥ 7 エンコード **CPU 10〜20% 削減** + 3.84 KB/frame 削減 | `[Exact-Careful]` |
| 14 | **SILK-05,06** | `silk/resampler.go`, `silk/decoder.go` | `downFIRInterpol` の `append` 廃止と BCE、`up2HQInternal` / `ar2` の状態レジスタ化、`synthesize` の不要 7.5 KB クリア削除・分岐外出し、`silkGainDequantQ16` 64 要素 LUT 化 | SILK デコード **15〜25% 高速化** | `[Exact]` |
| 15 | **OGG-01,02,03** | `oggopus/page.go`, `packet.go`, `seek.go` | Ogg CRC32 のテーブル化（8〜25×）、`ReadPage` / `PacketWriter.flush` の 3 重コピー解消、`scanNextPage` バッファ再利用 | Ogg I/O スループット **5〜10× 向上** + メモリ確保大幅削減 | `[Exact]` |

---

## 4. サブシステム別・網羅的改善カタログ（全 81 項目）

### 4.1 `internal/entcode`（レンジコーダ：EC-01〜EC-07）

#### EC-01: `Decoder.Decode` + `DecodeUpdate` の 32-bit 整数除算の 2 重実行を解消 `[Exact]`
- **箇所**: `internal/entcode/decoder.go:221-250`
- **原因**: libopus の `ec_dec` は `Decode` 時に計算した `ext = rng / ft` を構造体フィールドに保持し、`DecodeUpdate` で再利用する。現在の Go 実装では `Decode`（228 行）と `DecodeUpdate`（245 行）の双方で `dec.rng / ft` を計算しており、PVQ・Laplace・theta・Uint 復号のたびに高レイテンシな 32-bit ハードウェア除算が 2 回走る。
- **改善策**: `Decoder` に `ext uint32` フィールドを追加し、`Decode` で保存、`DecodeUpdate` で再利用する。
- **効果**: `entcode.Decoder.Decode` / `DecodeUpdate` の除算回数 50% 削減（CELT デコード全体の数％改善）。

#### EC-02: Laplace 符号化/復号に 2 冪専用 `EncodeBin` / `DecodeBin` を導入 `[Exact]`
- **箇所**: `internal/entcode/laplace.go:85, 96, 128`
- **原因**: `EncodeLaplace` / `DecodeLaplace` は `ft = 32768 (1<<15)` 固定であるにもかかわらず汎用の `Encode` / `Decode` / `DecodeUpdate` を呼び、毎シンボル 32-bit 除算を発生させている（libopus は `ec_encode_bin` / `ec_decode_bin` を使用）。
- **改善策**: `EncodeBin(fl, fh, bits uint32)` / `DecodeBin(bits uint32)`（`ext = rng >> bits`）を実装し、`laplace.go` から呼ぶ。
- **効果**: コースエネルギー符号化/復号のハードウェア除算をビットシフトに置換。

#### EC-03: `Encoder` / `Decoder` の `Reset` API 追加と単一フラットバッファ化 `[Exact]`
- **箇所**: `internal/entcode/decoder.go:29-39`, `internal/entcode/encoder.go:43-92, 185-245`
- **原因**:
  1. `NewDecoder(buf)` は毎フレーム `&Decoder{}` をヒープ確保する（SILK デコード唯一の 1 alloc/op、CELT デコード 4 allocs/op の 1 つ）。
  2. `NewEncoder(capacity)` は `&Encoder{}` + `buf` を確保し、さらに `endBytes` を別スライスで `append` 成長させ（`-gcflags="-m"` で `writeByteAtEnd` がヒープ確保元として確認済み）、`Bytes()` で再度 `make([]byte, n)` して前半＋後半をコピーする。その後、上位（`celt/encoder.go`, `opus.go`）で TOC 1 バイト付与やパディングのためさらに 1〜2 回コピーが発生する。
- **改善策**:
  1. `Decoder.Reset(buf []byte)` と `Encoder.Reset(buf []byte)` を追加し、`celt.Decoder` / `silk.Decoder` / `opus.Decoder` / `celt.Encoder` / `opus.Encoder` に値型または再利用ポインタで埋め込む。
  2. `Encoder` のバッファを libopus 同様に単一フラット配列（先頭からレンジ符号、末尾から生ビットを逆方向に書き込み）にし、`CloneInto(dst *Encoder)` を用意する。
- **効果**: `entcode` 起因の毎フレームアロケーションを完全 0 化。SILK デコードは **1 alloc/op → 0 allocs/op**。

#### EC-04: ホットパスで常に nil の `dec.err` チェックを削除 `[Exact]`
- **箇所**: `internal/entcode/decoder.go:24, 164, 200, 222, 344`
- **原因**: `Decoder.err` はパッケージ内のどこからも代入されておらず常に `nil` だが、`DecodeIcdf`, `DecodeBitLogp`, `Decode`, `DecodeBit` の毎呼び出しで `if dec.err != nil` が評価される。
- **改善策**: 無用な `dec.err` フィールドおよび分岐を削除（`Error()` は互換のため `return nil` のみ残す）。
- **効果**: シンボル単位の分岐・メモリアクセス削減とインライン化コスト低減。

#### EC-05: 2 シンボル専用 ICDF 高速パス `DecodeIcdf2` / `EncodeIcdf2` `[Exact]`
- **箇所**: `internal/entcode/decoder.go:163-196`, `internal/entcode/encoder.go:118-137`, `internal/silk/decoder.go:2309`, `internal/silk/encoder.go:3084`
- **原因**: SILK のパルス符号（`decodeSigns` / `encodePulseSigns`）や LSB 復号では、非ゼロパルス 1 個ごとに `[2]uint8{val, 0}` を組み立ててループ付きの汎用 `DecodeIcdf` / `EncodeIcdf` を呼んでいる。
- **改善策**: `DecodeIcdf2(icdf0 uint8, ftb uint) int` / `EncodeIcdf2(s int, icdf0 uint8, ftb uint)` を追加（ループ・スライス境界チェック一切なし、1 回の比較のみで完結）。
- **効果**: SILK パルス符号化・復号（`DecodeIcdf` + `normalize` がデコード CPU の ~10〜15%）を大幅短縮。

#### EC-06: `DecodeUint` の単一シンボル更新融合 `[Exact]`
- **箇所**: `internal/entcode/decoder.go:270-303`
- **原因**: `DecodeUint` が `Decode(ft)` → `DecodeUpdate(s, s+1, ft)` を別々に呼び、`fh = fl + 1` の特殊性を活かさず 2 重除算と不要な分岐を通っている。
- **改善策**: `EC-01` と合わせて `DecodeUint` 内で直接更新を融合する。

#### EC-07: `TellFrac` 内のローカル定数配列 `[8]uint32` の静的化 & `fmt` 依存分離 `[Exact]`
- **箇所**: `internal/entcode/decoder.go:3, 76, 306-331`, `internal/entcode/encoder.go:172`
- **原因**: `TellFrac`（CELT デコード CPU の 4.65% を占める）が毎回関数ローカルに `correction := [8]uint32{...}` を定義している。また `DecodeUintTrace` のデバッグ関数だけが `"fmt"` をインポートしており、バイナリサイズとビルド依存を膨らませている。
- **改善策**: `tellFracCorrection` をパッケージ変数化。`DecodeUintTrace` はテスト/デバッグビルドタグへ分離。

---

### 4.2 `internal/dsp`（FFT / MDCT / 数学ユーティリティ：DSP-01〜DSP-07）

#### DSP-01: `OpusFFTScaled32` の毎呼び出しヒープ確保（3,840 B/frame）を排除 `[Exact]`
- **箇所**: `internal/dsp/fft_opus.go:318`
- **原因**: `tmp := make([]opusComplex, n)` を毎回ヒープ確保している。`TonalityAnalysis.Run`（complexity ≥ 7）から 20ms ごとに呼ばれるため、毎フレーム 3.84 KB の確保が発生する。
- **改善策**: `n <= 480` はスタック固定配列 `var tmpBuf [480]opusComplex` を使用するか、呼び出し元スクラッチを受け取る `OpusFFTScaled32Into` を提供する。
- **効果**: complexity ≥ 7 エンコードで -3,840 B/frame, -1 alloc/frame。

#### DSP-02: `CELTMode` の未使用スクラッチ（15 KB/インスタンス）削除と回転因子・窓の静的共有 `[Exact]`
- **箇所**: `internal/dsp/mdct_celt.go:27-35, 117-125, 264-282`
- **原因**:
  1. `fwdFC` / `fwdZ`（`2 × 480 × 16 B = 15.36 KB`）は静的 FFT サイズ（120/240/480/960）では一切使われないのに毎インスタンス確保されている。
  2. `m.trig`（`make([]float32, N)`）と `Window []float64` が `NewCELTMode` ごとに再計算・複製される（現状 `NewDecoder` で 32 回分重複確保！）。
  3. `opusFFTPlan` の `twiddles`（60/120/240/480）も 4 つで重複保持（libopus の `fft_twiddles48000_960` は 1 テーブルをストライド共有）。
- **改善策**: `trig`・`Window32`・`opusFFTPlan` を `(N, overlap)` ごとのイミュータブル共有データとしてキャッシュし、可変スクラッチのみをエンコーダ/デコーダ側へ分離。`fwdFC`/`fwdZ` は非静的サイズ時のみ遅延確保。
- **効果**: `NewDecoder` / `NewEncoder` のメモリ使用量と初期化コストを劇的に削減。

#### DSP-03: `opusFFTPlan` バタフライ（`bfly2/3/4/5`）の最適化（BCE・`j==0` スキップ・展開） `[Exact-Careful]`
- **箇所**: `internal/dsp/fft_opus.go:190-308`
- **原因**: Hybrid デコード CPU の ~13% が `bfly3/4/5` に集中している。
  1. `opusBfly2`（190 行）は 4 回ループの中に `switch j { case 0..3 }` を持つ。
  2. `bfly3/4/5` は内側ループで `tw[j*fstride*k]` と乗算インデックスを毎回計算し、スライス境界チェックが多数残る。
  3. `j == 0` は常に回転因子 `(1, 0)` なので複素乗算（乗算 4 回＋加算 2 回）を丸ごと省略できる。
- **改善策**: `opusBfly2` を 4 要素完全展開、`bfly3/4/5` で `j==0` 専用パス分離、インデックスを乗算から加算ステップ化、先頭で `_ = out[end]` の BCE ヒント付与。
- **効果**: MDCT 順変換・逆変換の 15〜25% 高速化。

#### DSP-04: `CLTMDCTForward` / `CLTMDCTBackwardInto` の `float64↔float32` 変換と不要コピー削減 `[Exact]`
- **箇所**: `internal/dsp/mdct_celt.go:250-415`
- **原因**: 内部の回転・FFT・後回転はすべて `float32` で実行されているのに、入出力が `[]float64` であるため境界で毎サンプル変換が発生している。また `CLTMDCTBackwardInto`（409 行）は `bwdBuf[:N]` に計算した後に `dst` へ全コピーしている。さらに `Window []float64` を毎サンプル `float32(win[...])` 変換している。
- **改善策**: `Window32 []float32` を保持し、`float32` 直接入出力版（`CLTMDCTForward32Into` / `CLTMDCTBackward32Into`）を追加して CELT の `X []float32` やオーバーラップバッファへ直接読み書きする。
- **効果**: 960×2 サンプル分の型変換＋コピー削減。

#### DSP-05: `dsp.MDCT` が事前計算済み `twiddles` を無視しホットループで `math.Cos`/`math.Sin` を呼んでいる `[Exact-Careful]`
- **箇所**: `internal/dsp/mdct.go:34-42, 60-130, 145-190`
- **原因**: `NewMDCT` で `twiddles` を計算して保持しているにもかかわらず、`Forward` / `Inverse` / `inverseFft` がそれを一切参照せず、ループ内で `math.Cos` / `math.Sin` を毎回呼んでいる（ベンチマーク `BenchmarkMDCTForward512`: 21.8 µs, 4 allocs, 20 KB/op）。
- **改善策**: 事前計算済み `twiddles` を使用し、内部作業スライスを `MDCT` 構造体またはスクラッチで再利用。

#### DSP-06: `dsp.BitReverse` / `Log2` / `NextPowerOf2` を `math/bits` 組み込み命令に置換 `[Exact]`
- **箇所**: `internal/dsp/math.go:58-89`, `internal/dsp/fft.go:28-55, 155-240`
- **原因**: `BitReverse` がビット単位ループ、`Log2` / `NextPowerOf2` もループ実装。また `FFT` / `RealFFT`（`bandwidth_detect.go` が毎フレーム呼ぶ）はビット反転テーブルをキャッシュせず毎回計算し、複素配列を新規確保する（`BenchmarkRealFFT1024`: 102 µs, 32 KB/op）。
- **改善策**: `bits.Reverse` / `bits.Len` を使い、`FFTConfig` にビット反転テーブルと作業バッファを持たせる。`RealFFT` を N/2 点複素 FFT パッキングに置換すれば演算量とメモリがさらに半減。

#### DSP-07: `fft_any.go` の Bluestein 経路の不要確保と定数再計算 `[Exact]`
- **箇所**: `internal/dsp/fft_any.go`
- **原因**: 非 2 冪 FFT 呼び出しごとにチャープ配列・作業配列を確保している。静的サイズ以外の利用頻度は低いが、サイズ別のプランキャッシュでゼロ確保化可能。

---

### 4.3 `internal/celt`（CELT コーデック・解析・PLC・サラウンド：CELT-01〜CELT-17）

#### CELT-01: `thetaRdo` のバンド毎 8〜11 ヒープ確保（最大 150+ allocs/frame）を 0 化 `[Exact]`
- **箇所**: `internal/celt/quant_pvq.go:1299-1342`
- **原因**: `complexity >= 8`（`EncoderProfileLibopus` の既定は complexity 9！）のステレオ CELT で、結合ステレオの各バンドごとに `enc.Clone()` を 2 回、`append([]float32(nil), ...)` を 5 回（`xSave`, `ySave`, `xSave2`, `ySave2`, `normSave2`）呼び出しており、1 フレームで 100〜150 回以上のヒープ確保が爆発する。
- **改善策**: `quantAllBandsScratch` に `xSave`, `ySave`, `xSave2`, `ySave2`, `normSave2` スライスと `encSave1`, `encSave2`（`entcode.Encoder`）を常駐させ、`enc.CloneInto(&qs.encSave1)` と `copy(...)` で再利用する。
- **効果**: complexity ≥ 8 ステレオエンコードのアロケーションを 100+ → 数件へ劇的削減、GC 圧力大幅低減。

#### CELT-02: 常時有効な CELT `FrameTrace` コピー（~35 KB/frame）をオプトイン化 `[Exact]`
- **箇所**: `internal/celt/encoder.go:1060-1107, 1190-1196, 1371-1382, 1421-1426`
- **原因**: `internal/silk` は `EnableFrameTraces()` でオプトイン制御しているのに対し、`internal/celt` は本番エンコードでも毎フレーム無条件に `traceBandLogE := append([]float64(nil), logE...)` をヒープ確保し、`bufs`（~17 KB）、`traceCoeffs`（~15 KB）など 11 本のスライスをコピーして `e.lastTrace` に保持し続けている。
- **改善策**: SILK と同じ `atomic.Int32` ベースの `EnableFrameTraces()` を導入し、通常時はトレース用バッファの確保もコピーも一切スキップする。
- **効果**: 毎フレーム ~35 KB のメモリコピー＋ヒープ確保削減、`celt.Encoder` インスタンス保持メモリ -35 KB。

#### CELT-03: CELT デコーダの `BandProcessor` 冗長コピーと `amp^2 -> Sqrt` 往復の廃止 `[Exact-Careful]`
- **箇所**: `internal/celt/decoder.go:359-448, 902-916`, `internal/celt/bands.go:65-103`
- **原因**:
  1. `quantAllBandsImpl` がフラットな `X []float32` に係数を書き込んだ後、デコーダは 21 個の独立した `Band.Coeffs ([]float64)` へコピーし、
  2. `amp` をわざわざ二乗して `Band.Energy` に格納し（`decoder.go:360`）、
  3. 直後の `DenormalizeBands()`（`bands.go:71`）で `math.Sqrt(Energy)` を 42 回（ステレオ）呼んで `amp` に戻し、
  4. 再度フラットな `mdctBuf ([]float64)` へコピーして IMDCT へ渡している！
  5. さらに `NewBandProcessor` がデコーダ 1 個あたり 21+ 回のヒープ確保を行う（32 デコーダ分で ~700+ allocs、pprof `celt_dec` の `alloc_objects` 上位）。
- **改善策**: デコード経路から `BandProcessor` を外し、フラット `X []float32` 上で直接 `X[k] *= amp` をインプレース適用してそのまま `CLTMDCTBackward32Into` に渡す。
- **効果**: 毎フレーム 2 回の全スペクトルコピー＋42 回の `math.Sqrt`＋`float32↔float64` 変換を全廃、初期化アロケーションも大幅削減。

#### CELT-04: 80 KB `pvqUTable` の圧縮表現化（~5 KB）と `cwrsiLibopusInto` 最適化 `[Exact]`
- **箇所**: `internal/celt/pvq.go:396-425, 430-560`
- **原因**: `cwrsiLibopusInto` は CELT デコードで **CPU 16.3%（単体 1 位）** を占める最大ホットスポット。要因の 1 つは `pvqUTable` が `[64][320]uint32` = **80 KB** あり（下三角や飽和 `0xFFFFFFFF` で大半が無駄）、CPU の 48 KB L1d キャッシュから溢れて毎回キャッシュミスを起こしていること（libopus の `CELT_PVQ_U_ROW` は非飽和要素のみ詰めて **~5 KB**）。また `cwrsiLibopusInto` の内ループで毎回 `if n > k` の分岐と 2 回の境界チェックが入る。
- **改善策**: libopus と同一のパック配列 `pvqUData` + 行オフセット表 `pvqURow`（計 ~5 KB）に圧縮し、`cwrsiLibopusInto` の内ループでスライス先読み・BCE を行う。
- **効果**: L1d キャッシュミス劇減、`cwrsiLibopusInto` の 20〜40% 高速化（CELT デコード全体で 4〜7% 改善）。

#### CELT-05: CELT デコード残存 4 allocs/op の完全 0-alloc 化 `[Exact]`
- **箇所**:
  1. `internal/celt/decoder.go:199`: `entcode.NewDecoder(frameData)`
  2. `internal/celt/quant_energy.go:294-297`: `UnquantizeCoarseEnergy` の `make([]float64, numBands*channels)` + `make([]float32, channels)`
  3. `internal/celt/decoder.go:1170`: `celtTFDecode` の `make([]int, numBands)`
  4. `internal/celt/decoder.go:1107`: `decodeDynalloc` の `make([]int, numBands)`
- **改善策**: `ecDec entcode.Decoder` の再利用と、`[21*2]float64`, `[2]float32`, `[21]int` の固定長スタック配列または `celt.Decoder` 内スクラッチ化（`numBands <= 21`, `channels <= 2` が不変条件）。
- **効果**: CELT デコード・Hybrid デコードの定常アロケーションを **4 allocs/op → 0 allocs/op**（-634〜858 B/op）。

#### CELT-06: `TonalityAnalysis` の行列積ストライド改善・重複計算削除・配列埋め込み `[Exact-Careful]`
- **箇所**: `internal/celt/tonality.go:98-105, 168-193, 419-438, 512-541, 924-930`
- **原因**:
  1. `gemmAccum`（924-930 行）は `weights[j*colStride+i]` を内側ループ `j` で参照する **ストライドアクセス** になっており、さらに毎反復 `out[i]` へ Read-Modify-Write かつ `float32(weights[...])` 変換を行っている。
  2. 全 236 ビンの `binEnergy` が 419-427 行と 534-541 行で **2 回計算**されている（すでに `t.e[t.eCount][b]` に保存済み）。
  3. `math.Log(E + 1e-10)` が 437 行と 438 行で連続して **2 回実行**されている（18 回の無駄な `math.Log` / 20ms）。
  4. `specVariability`（512-525 行）が対称な 8×8 距離行列を全 64 ペア（対角 `i==j` 含む）計算している（上三角 28 ペアの 2 倍で済む）。
  5. `TonalityAnalysis.Reset`（98-105 行）が固定サイズ 480/480/240/240 の 4 スライスを毎回ヒープ確保している。
  6. `downmixAndResample`（168-193 行）が `tmp` 上を 3〜4 パス走査している。
- **改善策**: 固定重みを `init` 時に転置済み `float32` 配列化してレジスタ累積（加算順は不変のためビット一致）、重複 `binEnergy` / `math.Log` 削除、`specVariability` を上三角 28 ペア化、4 バッファを構造体固定配列化。
- **効果**: complexity ≥ 7 エンコードのトナリティ解析 CPU を **30〜50% 削減**。

#### CELT-07: `Encoder.quantCoarseEnergy` の毎フレーム 4〜8 アロケーション削減 `[Exact]`
- **箇所**: `internal/celt/quant_energy_flp32.go:158-172`
- **原因**: 毎フレーム `enc.Clone()` を 2 回、`make([]float64, len(oldEBands))` と `make([]float64, len(errOut))` を確保（`pprof` で `celt_enc` の 2 番目に大きい `alloc_space` 源）。
- **改善策**: `Encoder` に `coarseEncSave1/2`（`CloneInto`）および `[42]float64` 固定配列（スタックまたは構造体スクラッチ）を持たせる。
- **効果**: CELT エンコード **-4〜8 allocs/op, -1 KB/op**。

#### CELT-08: `opPVQSearch` / `algQuant` のホットループ最適化（スタックゼロ化削減・`math.Floor` 削除） `[Exact-Careful]`
- **箇所**: `internal/celt/pvq_encode.go:94-218`
- **原因**:
  1. パーティション呼び出しごとに `[176]float32`×2 + `[176]int`×2 = **~4.2 KB のスタックゼロ初期化**が毎回走る（`signBuf` は `uint8`/`bool` でよく、`iyBuf` は `int32` または `qs.iy` 再利用でよい）。
  2. 141 行で非負保証の値に対して `math.Floor(float64(float32(rcp * x[j])))` を呼んでいる（非負 `float32` の整数切捨ては `int(v)` と完全一致）。
  3. 172-182 行の `O(K*N)` 内ループ（`celt_enc` の 4.9% flat）は BCE ヒントや 2-way 展開が未適用。
- **改善策**: `signBuf [176]uint8` 化、`qs.iy` 再利用、`int(float32(rcp * x[j]))` 化、内ループ BCE＋2-way 展開。
- **効果**: CELT エンコード PVQ 探索 15〜25% 高速化。

#### CELT-09: `pitchXcorr32` のスライディングレジスタ化（メモリロード 1/4） `[Exact]`
- **箇所**: `internal/celt/prefilter_flp32.go:15-40`
- **原因**: `pitchXcorr32` は CELT エンコード **CPU 7.32%（単体 1 位）**。現在 4 本のずらしたスライス `y0[j], y1[j], y2[j], y3[j]` を毎反復ロードしており、本来 1 要素進めるだけで再利用できる `y` を 4 倍ロードしている。
- **改善策**: libopus の `xcorr_kernel` 同様に `y0, y1, y2, y3` をローカルレジスタでスライドさせ、4 サンプル展開ループで `x[j..j+3]` と乗算累積する（各出力 `sum0..sum3` の加算順を保てばビット完全一致）。
- **効果**: `pitchXcorr32` のメモリトラフィック 1/4、CPU **30〜45% 削減**。

#### CELT-10: `runPrefilter` / `combFilter32` / `pitchSearch32` の不要処理・ゼロ化削減 `[Exact]`
- **箇所**: `internal/celt/prefilter_flp32.go:201, 418-453, 618-657`
- **原因**:
  1. `runPrefilter`（618-657 行）は `gain1 == 0 && e.prefilterGain == 0` のときでも L1 ノルム計算、`combFilter32` コピー、`float64↔float32` 往復コピーを実行している。
  2. `combFilter32`（418-453 行）は tapset 1・2 では `g12 == 0` なのに `g12 * (x0 + x4)` を毎サンプル計算しており、BCE もない。
  3. `pitchSearch32`（201 行）は直後に全上書きする `xLP4Arr`, `yLP4Arr`, `xcorrArr`（計 6 KB）をスタックゼロ化している。
- **改善策**: `gain1 == 0 && prevGain == 0` 時の高速バイパス、`g12 == 0` 特化ループ、BCE 導入、`pitchSearch` 作業配列の構造体スクラッチ化。

#### CELT-11: CELT エンコーダ `X ([]float64)` → `Xq ([]float32)` 二重保持と不要ゼロクリアの統合 `[Exact-Careful]`
- **箇所**: `internal/celt/encoder.go:578-589, 625-649, 1398-1408`
- **原因**: `spreadingDecision32`, `stereoAnalysis`, `allocTrimAnalysis32`, `tfAnalysis` はすべて内部で `float32` 精度で動作しているのに、`X`, `bufs`, `pes` を `[]float64`（2 倍のメモリとキャッシュフットプリント）で保持し、`scratchF64`/`scratchRows` で直後に上書きされる ~47 KB をゼロクリアした上で、最後に `X` を `Xq []float32` へ再コピーしている。また `pcmSilence`（625-649 行）がプリエンファシスとは別に `samples` を再走査し、`totalBoost` が 3 箇所で重複加算されている。
- **改善策**: エンコーダ内部表現を `float32` 基準に統一（`ARCH-01` と連動）、不要 `clear` 削除、`pcmSilence` をプリエンファシスループに融合。

#### CELT-12: `PostFilter.combFilter` / `combFilterConst` の連続バッファ化（分岐全廃） `[Exact]`
- **箇所**: `internal/celt/postfilter.go:206-257`
- **原因**: `PostFilter.combFilterConst` は CELT デコード **CPU 6.98%（2 位タイ）**。1 サンプルごとに `getHistorySample` 相当の負インデックス分岐チェックを最大 7 回実行し、しかも履歴を `[]float64`（2048×8B = 16 KB/ch）で保持している。
- **改善策**: libopus の `decode_mem` 同様に `[histLen + frameSize]float32` の連続バッファに配置して直接インデックス参照（分岐ゼロ、メモリ半減、`float64↔float32` 変換ゼロ）。
- **効果**: `PostFilter` の 30〜50% 高速化、デコーダ 1 個あたり 16 KB 削減。

#### CELT-13: `quant_pvq.go` の不要ゼロクリア・ローカル定数配列・`haar1` 最適化 `[Exact]`
- **箇所**: `internal/celt/quant_pvq.go:298, 324, 655, 856, 908, 1177`, `internal/celt/bands.go`
- **原因**:
  1. `scratch := zeroedFloat32(&qs.scratch, 960)`（1177 行）が毎バンド最大 3.84 KB を不要クリア。
  2. `deinterleaveHadamard` / `interleaveHadamard`（298, 324 行）が毎回スタック `[176]float32` をゼロ化。
  3. `exp2t8`, `bitInterleave`, `bitDeinterleave`（655, 856, 908 行）が関数内ローカル配列。
  4. `haar1`（`celt_enc` CPU 4.88%）がジェネリック `[go.shape.float64]` 経由で呼ばれており、float32 化と BCE でさらに軽量化可能。
- **改善策**: `growFloat32` への置換（必要範囲のみ書き込み）、定数配列のパッケージレベル化、float32 特化 `haar1`。

#### CELT-14: `rate_alloc.go` の毎フレーム 8 アロケーションと重複テーブル参照を解消 `[Exact]`
- **箇所**: `internal/celt/rate_alloc.go:871-873` 周辺（`BenchmarkRateAllocator`: 608 ns, 944 B, **8 allocs/op**）
- **原因**: `ComputeAllocation` が `bandBits` から `pulses` をコピーし、`offsets == nil` 時にスライスを確保、`allocAtLevel`/`psumCoarse`/`psumFine` クロージャが二分探索の各ステップで `EBands48000[j+1]-EBands48000[j]` を再計算し、`celtBits2PulsesQ3`/`celtPulses2BitsQ3` が `quantPartition` と重複して `CacheIndex50` を引き直している。
- **改善策**: `[21]int` 固定配列 / 呼び出し元スクラッチ渡し、`bandWidth[21]` 事前計算、`CacheIndex50` インデックスの受け渡し。
- **効果**: `BenchmarkRateAllocator` **8 allocs/op → 0 allocs/op**、エンコード/デコード双方のビット配分高速化。

#### CELT-15: `patchTransientDecision` / `surroundMaskAnalysis` の毎フレーム小スライス確保排除 `[Exact]`
- **箇所**: `internal/celt/celt_analysis.go:172, 780`
- **原因**: `end <= 21`, `nbEBands <= 21` なのに毎フレーム `make([]float64, end)` / `make([]float64, nbEBands)` をヒープ確保している。
- **改善策**: スタック固定配列 `var tmp [21]float64` に置換。

#### CELT-16: `celt/plc.go` の PLC 1 フレームあたり 15〜20+ ヒープ確保をスクラッチ化 `[Exact]`
- **箇所**: `internal/celt/plc.go`（`DecodePLC` 全体）
- **原因**: パケットロス補完時に `xLP`, `yLP`, `xcorr`, `lpc`, `exc`, `fir`, `out` 等を毎回 `make` しており、ロス連続時に GC 負荷が跳ね上がる。
- **改善策**: `Decoder` に遅延確保の `plcScratch *celtPLCScratch` を持たせる（通常無ロス時は 0 B、初回 PLC 時のみ 1 回確保して再利用）。

#### CELT-17: `surround_analysis.go` のチャネル毎 MDCT 確保・配列確保をゼロ化 `[Exact]`
- **箇所**: `internal/celt/surround_analysis.go:126` 周辺
- **原因**: サラウンド解析が毎フレーム・毎チャネルで `CLTMDCTForwardInto` ではなく `CLTMDCTForward` を呼んで新規スライスを確保しており、さらに `bandLogE`, `in`, `freq`, `surroundChannelPos` も毎フレーム確保している。
- **改善策**: `SurroundAnalyzer` に再利用バッファを保持し `CLTMDCTForwardInto` を使用、`surroundChannelPos` はパッケージ定数化。

---

### 4.4 `internal/silk`（SILK コーデック・NSQ・ピッチ・NLSF・リサンプラ：SILK-01〜SILK-18）

#### SILK-01: `silkNLSFEncode` + `silkNLSFDelDecQuant` の ~60 allocs/frame を完全 0 化 `[Exact]`
- **箇所**: `internal/silk/nlsf_encode.go:65-188, 214-233, 392-435`
- **原因**: `BenchmarkNLSFQuantization` 実測 **45 allocs/op (1,712 B/op)**、`pprof` でも SILK/Hybrid エンコード最大のアロケーション源。
  1. 生存者ループ（complexity 10 で 16 回）の中で毎回 `resQ10`, `wAdjQ5`, `raw` を確保（**48 allocs/frame**）。
  2. `sort.SliceStable` がクロージャ＋リフレクション確保を行い、全 32 要素を完全ソートしている（上位 `nSurvivors` 件の部分挿入ソートで十分）。
  3. `silkNLSFDelDecQuant`（214-233 行）がコードブック定数 `quantStepSizeQ16` だけに依存する 40 要素の `out0Table` / `out1Table` を毎フレーム 16 回再計算している。
  4. `errQ24`, `survivors`, `target`, `bestRaw`, `weightsQW`, `nlsf0TempQ15`, `reconstructNLSFQ15`, `nlsfToLPCLibopus` がすべて `order <= 16` の小スライスをヒープ確保。
- **改善策**: すべて `[silkMaxLPCOrder]`（`[16]`）固定配列化し、`*Into` ヘルパへ変更。`out0Table`/`out1Table` はパッケージ初期化時にコードブック別事前計算。`sort.SliceStable` をインライン部分挿入ソートに置換。
- **効果**: SILK エンコード **-60 allocs/frame**（ステレオ時は **-120 allocs/frame**）＋ NLSF 探索 CPU 大幅短縮。

#### SILK-02: `Encoder.encodeRangeFrame` の `analyzeNLSF` 2 重実行の統合／削除 `[Behavioral/Verify]`
- **箇所**: `internal/silk/encoder.go:925`（bootstrap）および `967`（本番）
- **原因**: 1 フレームの符号化で `e.analyzeNLSF`（Burg LPC + `silkA2NLSF` + 16 生存者 `silkNLSFEncode` + 補間 LPC 探索）を **2 回丸ごと実行**している。libopus の `silk_encode_frame_FLP` は `find_LPC_FLP` + `process_NLSFs` を 1 フレーム 1 回しか実行しない。有声フレームでは `e.pitchResidual` が存在するため `selectLTPGainsVQWithGain` は `bootstrap.lpcQ12` を参照すらしないケースがある。
- **改善策**: `bootstrap` の結果が必要なパス（無声時フォールバック等）を精査し、libopus 同様に「ピッチ解析 → ノイズシェイプ → 予測係数（LTP + LPC/NLSF 1 回） → ゲイン処理 → NSQ」の単一パイプラインに整理する。
- **効果**: SILK / Hybrid エンコードの **CPU 10〜20% 削減**＋残存 NLSF/Burg 計算・アロケーションの半減。

#### SILK-03: `ltp_quant.go` / `process_gains_flp32.go` / `residual_energy_flp.go` / `lpc_in_pre.go` / `encode_frame_loop.go` の小スライス確保一掃 `[Exact]`
- **箇所**:
  - `internal/silk/ltp_quant.go:89-90, 215-232, 303-308, 353`（12+ allocs/frame: `XX`, `xX`, `xxQ17`, `xXQ17`, `gainIndices`, ループ内 `tempIdx`×3, `out`, `&r`, さらにレート制御ループ毎の `coeffs`/`gainIndices` コピー）
  - `internal/silk/process_gains_flp32.go`（7 allocs/frame: `gains`, `gainsQ16`, `gainsUnqQ16`, `symbols`, `absIndices`, `res.gains`, `&res`）
  - `internal/silk/residual_energy_flp.go:37-38, 59-83`（8 allocs/frame + `buildLPCInPre` の重複計算！前半・後半ごとに `res`, `coef` を新規確保し、`equalSubframeLengths`, `invGains`, `ltpQ14ToFloat`, `buildLPCInPre` を再実行）
  - `internal/silk/lpc_in_pre.go`（`equalSubframeLengths`, `invGainsFromIndices`, `ltpQ14ToFloat` で 5 allocs）
  - `internal/silk/encode_frame_loop.go`（毎イテレーション `ltpCoeffsQ14`, `pitchLags`, `gainsQ16`, `silkGainsQuant`×2, `encodeGains`, `reconstructCurrentPitchLags`, `capLTPGainIdx`）
  - `internal/silk/burg.go` & `find_lpc.go`（`A`×2, `silkA2NLSF` の `P`/`Q` (`dd<=8`), `silkFloatToQ16`, `targetNLSFQ15`, 補間ループ内 `nlsfToLPCLibopus`×4）
- **改善策**: `nbSubfr <= 4`, `order <= 16` の不変条件を使って戻り値・作業配列をすべて固定長配列値型（`[4]int`, `[4]int32`, `[4][5]int16`, `[16]int16` 等）または `Encoder` 内スクラッチスライスへ置換。`residual_energy_flp.go` は `findLPC` で構築済みの `lpcInPre` を再利用。
- **効果**: SILK モノラルエンコード **105 allocs/op → < 5 allocs/op**、ステレオ **187 allocs/op → < 10 allocs/op**。

#### SILK-04: `nsq_del_dec.go` & `nsq_plain.go` のホットループ最適化（SILK 最大 CPU ホットスポット） `[Exact]`
- **箇所**: `internal/silk/nsq_del_dec.go:330, 392, 459, 480, 508-524, 568-572, 698-714`, `internal/silk/nsq_plain.go:46-52`
- **原因**: `pprof` で SILK エンコード **33.3% cum**、Hybrid エンコード **28.9% cum** を占める最大ホットスポット。
  1. 毎サンプル `smplBufIdx % silkDecisionDelay`（`% 40`）の整数除算が発生（330, 392, 568-572 行）。
  2. NSQ パス毎に `*dd = nsqDelayedDecision{}`（約 5.8 KB）と `zeroedInt32`/`zeroedInt16`（約 3.8 KB）を全ゼロクリア（レート制御ループで 1 フレーム複数回走る）。
  3. `sAR2` ワーピングループ（508-524 行）が `shapingLPCOrder = 16/24` に展開されておらず、`warp == 0` の特殊化もない。
  4. `sLTPQ15` / `sLTPShpQ14` の境界チェックがサンプル内ループに残存。
  5. `copyDelayedDecisionSuffix`（698-714 行）が状態入替のたびに ~1 KB をコピー（ポインタ／インデックススワップで O(1) 化可能）。
  6. `nsq_plain.go:46-52` が呼び出し毎に 4 スライスをヒープ確保。
- **改善策**: `% 40` を `if idx < 0 { idx += 40 }` 等の加減算に置換、必要フィールドのみ初期化、`sAR2` の 2-way/4-way 展開と `warp==0` 分岐、BCE ヒント、状態配列のインデックス参照化、`nsq_plain.go` の `e.nsqScratch` 再利用。
- **効果**: NSQ の **15〜25% 高速化**（SILK/Hybrid エンコード全体で 5〜8% 高速化）。

#### SILK-05: `silk/resampler.go` の `downFIRInterpol` / `up2HQInternal` / `ar2` / `iirFIR` 最適化 `[Exact]`
- **箇所**: `internal/silk/resampler.go:314-352, 362, 426, 454-464, 466-536`
- **原因**: `pprof` で SILK デコード CPU の **26.9% cum**（`iirFIRInterpol` 17.3% + `up2HQInternal` 9.6%）を占める。
  1. `downFIRInterpol`（466-536 行）は 1 出力サンプルごとに `out = append(out, ...)` を呼び（`iirFIRInterpol` のように事前 `Grow` + インデックス書込をしていない）、さらに `bp` への BCE ヒントがないため最大 36 回の境界チェック/サンプルが発生。
  2. `up2HQInternal`（314-352 行）と `ar2`（454-464 行）は `s.sIIR[0..5]` を毎サンプル構造体メモリからロード・ストアしている。
  3. `iirFIR`（362 行）と `downFIR`（426 行）が全上書き前に `clear(buf)` を呼んでいる。
- **改善策**: `downFIRInterpol` を事前拡張＋直接書込＋`bp = bp[:36]` BCE 化、`up2HQInternal`/`ar2` の状態をローカルレジスタ変数へ退避してループ終了時に書き戻す、不要 `clear` 削除。
- **効果**: SILK リサンプラの **15〜25% 高速化**（SILK デコード全体で 4〜7% 高速化）。

#### SILK-06: `silk/decoder.go` の `synthesize`・`shellDecode`・LUT・ステレオ最適化 `[Exact]`
- **箇所**: `internal/silk/decoder.go:990, 1005, 1228-1251, 1639-1651, 2105-2157, 2250, 2540-2662`
- **原因**:
  1. `synthesize`（SILK デコード CPU **28.9% cum**）が毎フレーム ~7.5 KB のスクラッチを不要クリアし、内ループ内に `if voiced` 分岐と境界チェックを持つ。
  2. `shellDecode`（2250 行）は呼び出し元が `[16]int16` を持っているにもかかわらず `buf := make([]int16, 16)` を確保して 2 回コピーし、15 回のクロージャ呼び出しで `&buf[i]` のアドレスを取っている。
  3. `silkGainDequantQ16`（1639-1651 行）は入力が 0..63 の 64 通りしかないのに毎回計算している。
  4. `silkPitchContourOffsets`（2105-2157 行）は関数内で `[4][34]int`（1,088 B）をローカル構築している。
  5. `stereoMSToLRCore`（1228-1251 行、SILK デコード CPU 3.9%）がループ内に `if i < interpLen` を持ち、さらに `x1`/`x2` を 2 パス走査している。
  6. `decodeMultiStereoEC`（990, 1005 行）が `side16` をクリア後に `pcm` をコピーしている（エイリアス可能）。
- **改善策**: `synthesize` の voiced/unvoiced ループ分離＋BCE＋不要クリア削除、`shellDecode` の直接 `[16]int16` 操作＋クロージャ展開、`silkGainDequantQ16` の 64 要素 `[64]int32` LUT（256 B）、`silkPitchContourOffsets` のパッケージレベル `[4][34]int8`（136 B）化、`stereoMSToLRCore` の補間区間・定常区間の 2 ループ分割。
- **効果**: SILK デコード **10〜18% 高速化**。

#### SILK-07: `pitch_core_flp32.go` の 10.88 KB 値返し・`xcorrKernelFLP` 型変換・`silkLog2FLP` LUT 化 `[Exact-Careful]`
- **箇所**: `internal/silk/pitch_core_flp32.go:55-130, 468` 周辺、`internal/silk/pitch_flp.go:133`
- **原因**: `BenchmarkPitchAnalysis` は 0 alloc だが **45.2 µs/op** と重い。
  1. `silkPAnaCalcCorrSt3FLP32` / `silkPAnaCalcEnergySt3FLP32` が `[4][34][5]float64`（**5,440 B × 2 = 10.88 KB**）を値返ししており、毎フレームスタック上で 10.88 KB のゼロ化＋コピーが発生。
  2. `xcorrKernelFLP`（55-130 行）が `[]float64` 入力に対して毎 MAC `f32()` 変換（4 サンプルごとに 16 回の `float64↔float32` 変換）を行い、`sum[0..3]` をポインタ越しに毎反復読み書きしている。
  3. 468 行で整数 `d ∈ [16..144]` に対し候補毎に `silkLog2FLP(float64(d))`（`math.Log10` 経由）を呼んでいる。
  4. `cbStage2`/`cbStage3`/`lagRange`/`lagCB` がホットループ内のクロージャ。
  5. `silkResamplerDown2_3`（`pitch_flp.go:133`）が 12/16 kHz ピッチ解析ごとに `buf := make([]int32, inLen+4)` をヒープ確保している。
- **改善策**: `[4][34][5]float32` を `pitchScratch` に配置してポインタ渡し、`xcorrKernelFLP` を `[]float32` + ローカルレジスタ累積化、`d ∈ [0..288]` の `log2` テーブル事前計算、クロージャのインライン化、`silkResamplerDown2_3` のスタック配列化。
- **効果**: ピッチ解析 **25〜40% 高速化**。

#### SILK-08: `noise_shape_flp32.go` の `silkWarpedAutocorrelationFLP32` 最適化と定数 `math.Pow` 除去 `[Exact]`
- **箇所**: `internal/silk/noise_shape_flp32.go:52-73, 233`
- **原因**: `silkWarpedAutocorrelationFLP32` は Hybrid エンコード **CPU 15.56%（単体 1 位）**、SILK エンコード **4.76%**。
  1. 内ループ（58-70 行）が毎反復 `state[0]` をメモリ再読込している（ループ不変のローカル変数 `x0 := input[n]` と同じ）。また `order = 16/24` に対する BCE と配列スライス最適化が弱い。
  2. 233 行の `gainAdd := f32(math.Pow(2.0, f32(c32(0.16)*c32(minQGainDB))))` はコンパイル時定数なのに毎フレーム `math.Pow` を呼んでいる。
- **改善策**: `x0` ローカルレジスタ化、`state`/`acc` の固定長配列直接インデックス＋`order == 16` / `order == 24` 特化パス、`gainAdd` をパッケージ定数化。
- **効果**: `silkWarpedAutocorrelationFLP32` **20〜35% 高速化**（Hybrid エンコード全体で 3〜5% 改善）。

#### SILK-09: `stereo_pred.go` と `encodeMultiStereoWithEncoder` のステレオ SILK 15+ allocs/frame を 0 化 `[Exact]`
- **箇所**: `internal/silk/encoder.go:672-686, 740-761`, `internal/silk/stereo_pred.go:48-68, 191`
- **原因**: `pprof`（`hybrid_enc alloc_space`）で **`int16FrameToFloat` 単体が全確保メモリの 17.09%（1 位）**、`lrToMS` が 2.62% を占めている！ステレオ SILK フレームごとに `left`, `right`, `l`, `r`, `mid`, `side`, `lpMid`, `hpMid`, `lpSide`, `hpSide`, `out`, `vadFlags`, `predIx`, `midOnly`, `[]uint8{placeholder, 0}` など 15+ スライスをヒープ確保している。
- **改善策**: `Encoder` にステレオ用スクラッチ（`[320]float64`/`[320]int16` 群）を持たせ、すべての変換を `*Into` でインプレース実行。
- **効果**: ステレオ SILK / Hybrid エンコードで **-15〜20 allocs/frame, -15〜22 KB/frame**。

#### SILK-10: `buildLPCInPreInto` と `silkInnerProductFLP32` のホットループ最適化 `[Exact-Careful]`
- **箇所**: `internal/silk/lpc_in_pre.go`, `internal/silk/burg.go`
- **原因**: `pprof` で `buildLPCInPreInto` が SILK エンコード **CPU 11.90%（2 位）**、`silkInnerProductFLP32` が Hybrid エンコード **CPU 11.11% cum（4 位）** を占める。`[]float64` から毎要素 `float32` 変換しながら 5 タップ LTP 積和を行っており、BCE ヒント・4-way 展開が未適用。
- **改善策**: `lpcInPreInput` を `float32` 化（`ARCH-01` と連動）、LTP 5 タップ係数 `b0..b4` をローカルレジスタへ展開し、BCE ヒント `b = b[:5]` と入力ウィンドウの事前スライスを行う。`silkInnerProductFLP32` も 4-way 展開＋BCE（加算順維持に注意）。
- **効果**: `buildLPCInPreInto` / `silkInnerProductFLP32` で **20〜35% 高速化**。

#### SILK-11: `silk/encoder.go` の不要な診断コピーと `prevNLSF` 確保の削除 `[Exact]`
- **箇所**: `internal/silk/encoder.go:1045, 1093-1099`
- **原因**:
  1. `pushInputFrame`（1093-1099 行）が診断用の `e.lastXBuf` を毎フレーム無条件コピーしている。
  2. 1045 行の `e.prevNLSF = nlsfQ15ToRadians(nlsf.nlsfQ15)` が毎フレームヒープ確保している（`pprof` `hybrid_enc` に 2.18% で出現）。`prevNLSF` はテスト／旧経路以外で参照されないか確認し、必要でも `[16]float64` 固定配列で十分。
- **改善策**: `e.lastXBuf` は `frameTracesEnabled()` ゲート、`prevNLSF` は固定配列化または削除。

#### SILK-12: `encodePulses` / `encodeShellBlock` の値渡し（176 B）と 15 クロージャ解消 `[Exact]`
- **箇所**: `internal/silk/encoder.go:2902-3103`
- **原因**: 176 B の `pulseBlock` 構造体が `selectPulseRateLevel`, `encodePulseBlockSum`, `encodePulseLSBs`, `encodePulseSigns` 間で値コピー／値レンジされており、`shellAbs` が 8 B の `int` 配列、`encodeShellBlock` が `[16]int` 値渡し＋15 回のクロージャ呼び出しを行っている。
- **改善策**: `pulseBlock` をポインタ渡し・`shellAbs` を `[16]uint8`（または `int16`）化、`encodeShellBlock` のクロージャを直接インライン記述。

#### SILK-13: `silkLPCAnalysisFilter`（固定小数点デコード用）の order 10/16 展開と BCE `[Exact]`
- **箇所**: `internal/silk/lpc.go` / `internal/silk/decoder.go`
- **原因**: `pprof` で `silkLPCAnalysisFilter` + `silkLPCPredictQ10` が SILK デコード CPU の **12〜15%**。内ループが動的 `order` で毎タップ境界チェックと `silkSMLAWB` を実行。
- **改善策**: `order == 16` と `order == 10`（SILK で使用される全次数）で係数 `a0..a15` をレジスタ展開し、履歴スライスの境界チェックをループ外へ括り出す。

#### SILK-14: `silkSMULWB` / `silkSMLAWB` / `silkSMULWW` の 64-bit 乗算1命令化検証 `[Exact]`
- **箇所**: `internal/silk/lpc.go`, `internal/silk/decoder.go`, `encoder_input.go:42-56`
- **原因**: `pprof` で `silkSMULWB` / `silkSMLAWB` / `silkSMULWW` が SILK エンコード・デコードともに **flat 7〜11%** を占める。Go コンパイラが 32-bit 上位/下位分割マクロ `(a>>16)*c + ((a&0xFFFF)*c)>>16` を展開している箇所は、amd64/arm64 では `(int64(a) * int64(int16(b))) >> 16` の **単一 64-bit `IMUL` + `SAR`** と数学的に完全等価（オーバーフローしない範囲でビット一致）。
- **改善策**: 32-bit 分割演算を行っている箇所を `int32((int64(a) * int64(int16(b))) >> 16)` に統一し、全入力域での完全一致をユニットテストで確認。
- **効果**: `silkSMULWB`/`silkSMLAWB` 命令数 60% 削減（SILK 全体で 3〜6% 高速化）。

#### SILK-15: `silk/vad_flp.go` の固定長配列化とフィルタ状態レジスタ化 `[Exact]`
- **箇所**: `internal/silk/vad_flp.go`
- **原因**: 4 バンド分割フィルタとサブバンドエネルギー計算が毎フレーム実行されるが、一時配列のクリア・境界チェックが残っている。
- **改善策**: バンド分割の `sAnaState` をローカルレジスタ化し、BCE ヒントを付与。

#### SILK-16: `silk/prefill.go` と `silk/rate_switch.go` のモード切替時一時確保削減 `[Exact]`
- **箇所**: `internal/silk/prefill.go:47-65`, `internal/silk/rate_switch.go:91-135`
- **原因**: `Prefill` が `l`, `r`, `left`, `right`, `mid`, `side` を都度確保。`carryChannelRateSwitch` も一時バッファを毎回確保。モード遷移が頻発する可変ビットレート／音声音楽混在ストリームでスパイク要因になる。
- **改善策**: 既存のエンコーダスクラッチを共用。

#### SILK-17: `silk/frame_size.go` の 10ms↔20ms 切替時バッファ再確保の回避 `[Exact]`
- **箇所**: `internal/silk/frame_size.go:29-36, 54-64`
- **原因**: `SetFrameMs` で 10ms↔20ms が切り替わるたびに `xBuf`, `nsq.xq`, `nsq.sLTPShpQ14` を毎回 `make` して再確保している。
- **改善策**: 最大フレーム長（20ms 分）の容量を常に保持し、スライス長 `[:keep+frameSize]` のリサイズだけで切り替える。

#### SILK-18: レガシー／デッドコード（`pitch.go`, `noise_shape.go` 旧経路）の整理 `[Exact]`
- **箇所**: `internal/silk/pitch.go`, `internal/silk/noise_shape.go`
- **原因**: `PitchAnalyzer`（`pitch.go`）と旧 float64 版 `analyzeNoiseShapeFLP`（`noise_shape.go`）が `pitch_core_flp32.go` / `noise_shape_flp32.go` と並存している。バイナリサイズ肥大と保守時の最適化漏れ要因になる。
- **改善策**: テスト・外部参照を確認し、未使用であれば整理・削除してコードサイズとキャッシュ効率を改善。

---

### 4.5 `internal/resampler`（ポリフェーズ FIR リサンプラ：RS-01〜RS-04）

#### RS-01: 多相 FIR 係数テーブルを位相優先（phase-major）レイアウトへ転置 `[Exact]`
- **箇所**: `internal/resampler/resampler.go:123-170, 297-300`
- **原因**: 現在の係数配置 `r.coeffs[j*r.oversample + phaseIdx]` は内ループ `j` の 1 ステップごとに最大 `64 × 8 B = 512 B` 飛ばしでメモリを読むため、キャッシュラインを 1 タップ 1 本消費する最悪のアクセスパターンになっている（`BenchmarkResampler16to48`: 65.9 µs）。
- **改善策**: `r.coeffs[phaseIdx*r.filterLen + j]` に配置し、内ループを連続スライス `c := r.coeffs[phaseIdx*fLen : (phaseIdx+1)*fLen]` の内積にする。
- **効果**: キャッシュ効率劇的向上、リサンプラ **2〜3× 高速化**。

#### RS-02: FIR 内ループ内の境界分岐を `[mem | input]` 連続バッファ化で全廃 `[Exact]`
- **箇所**: `internal/resampler/resampler.go:281-301`
- **原因**: 1 出力サンプル × 最大 80 タップのすべてで `tapPos >= 0 && tapPos < inputLen`, `tapPos < 0`, `memIdx >= 0 && memIdx < len(r.mem[ch])`, `coeffIdx < len(r.coeffs)` の 4 段階分岐を実行している。
- **改善策**: 構造体に再利用スライス `work []float64`（`[mem | input | zeroPad]`）を持ち、内ループを分岐ゼロの純粋な内積ループ（4-way 展開＋BCE）にする。
- **効果**: 分岐予測ミス・オーバーヘッド全廃、さらに **1.5〜2× 高速化**。

#### RS-03: `Process` / `processChannel` の毎呼び出し 3+ アロケーションと 1 要素成長ループ解消 `[Exact]`
- **箇所**: `internal/resampler/resampler.go:219-242, 258, 303`
- **原因**: `BenchmarkResampler48to16` で **13,570 B/op, 3 allocs/op**、`16to48` で **19,072 B/op, 3 allocs/op**。チャネルごとに `chInput`, `chOutput` を確保し、`Process` のインターリーブ時に `for len(output) <= idx { output = append(output, 0) }` と 1 要素ずつ伸長している。
- **改善策**: `ProcessInto(dst, input []float64) []float64` を追加し、構造体スクラッチでチャネル抽出・出力を行い、`output` を正確な長さで一括確保する。モノラル時はチャネル抽出・インターリーブ自体をスキップ。

#### RS-04: `generateCoeffs` の `besselI0(beta)` 5,120 回重複計算削除と係数テーブルのグローバルキャッシュ `[Exact]`
- **箇所**: `internal/resampler/resampler.go:152, 173-182`
- **原因**: `kaiserWindow` が 1 タップごとに `besselI0(beta)`（最大 50 回の級数展開）を再計算しており（最大 5,120 回無駄）、さらに同一 `(inRate, outRate, quality)` のインスタンス生成ごとに全係数を再計算・複製している。
- **改善策**: `invI0Beta := 1.0 / besselI0(beta)` をループ外へ移動し、`(inRate, outRate, quality)` キーでイミュータブルな係数スライスをグローバルキャッシュ（`sync.Map` 等）で共有する。

---

### 4.6 ルート `opus` パッケージ（Encoder / Decoder / FEC / パケット：OPUS-01〜OPUS-11）

#### OPUS-01: `NewDecoder` の 32 CELT + 6 SILK + 15 未使用リサンプラの一括確保を廃止（-95% メモリ） `[Exact]`
- **箇所**: `opus.go:3309, 3353, 3455-3511`
- **原因**: `pprof` 実測で **`opus.NewDecoder(48000, 2)` 1 回の呼び出しが ~4.14 MB（~17,000 相当オブジェクト）** を確保している。
  1. `celtDecoders[4][4][2]` = **32 個の `celt.Decoder`** を起動時に全生成（モノラルデコーダでも `chIdx==1` の 16 個は絶対使われない！）。
  2. `silkDecoders[3][2]` の各 `silkRateInfo` に `resampler`（計 6 個、各 5,120 回の `besselI0` 付き）と `silkResampler`/`silkResamplerL`/`silkResamplerR`（計 9 個）を生成しているが、**`resampleSILK` は `d.silkRS[0..1]` のみを使用しており、これら 15 個のリサンプラは完全にデッドコード**！
- **改善策**:
  1. 死んでいる `silkRateInfo.{resampler, silkResampler, silkResamplerL, silkResamplerR}` を即座に削除。
  2. `celtDecoders` をオンデマンド遅延生成にする（さらに理想的には libopus 同様に 1 つの `celt.Decoder` で `frameSize`/`numBands` を動的切替し、`CELTMode` を静的共有）。
  3. `silkDecoders` も遅延生成、または 1 つの `silk.Decoder`（モノ/ステレオ共有チャネル状態）へ統合。
- **効果**: `NewDecoder` の常駐メモリ **~4.14 MB → ~150〜250 KB（90〜95% 削減）**。マルチストリーム（5.1ch = 4 ストリーム、5 次 Ambisonics = 19 ストリーム）では **~78 MB → ~3〜4 MB** に圧縮される！

#### OPUS-02: `DecodeFECFloat` の毎回 `cloneState() -> NewDecoder()` を撤廃 `[Exact]`
- **箇所**: `opus.go:5645-5657, 5860-5940`
- **原因**: `DecodeFECFloat` がエラー時の状態ロールバック目的で **毎呼び出し `d.cloneState()` → `NewDecoder()`** を実行し、全 32 CELT デコーダ＋6 SILK デコーダ＋15 リサンプラを新規生成・ディープコピーした上で 1 フレーム復号し、旧デコーダを破棄している！
- **改善策**: `validateFECState` でパケット構文（`splitOpusFrames` 等）まで事前検証しきった上でインプレース復号する（失敗し得る箇所はすべて検証済みなのでロールバック不要、どうしても必要でもアクティブな 1 つの SILK/CELT 状態のみ軽量スナップショット）。
- **効果**: FEC 復号のレイテンシとメモリ確保を **100× 以上改善**。

#### OPUS-03: `EncodeFloat32` が `e.inputScratch` を使わず毎フレーム新規確保している `[Exact]`
- **箇所**: `opus.go:595`
- **原因**: `Encode`（525 行）と `Encode24`（556 行）は `e.inputScratch(expectedSize)` を再利用しているのに、`EncodeFloat32` だけ `floatPCM := make([]float64, expectedSize)` を毎フレーム確保している（モノラル 7.68 KB / ステレオ 15.36 KB）。
- **改善策**: `floatPCM := e.inputScratch(expectedSize)` に 1 行修正。

#### OPUS-04: パケット出力時の 3〜5 重バイトコピー（TOC 1 バイト付与・パディング・フレームパック）の解消 `[Exact]`
- **箇所**: `opus.go:1093, 1130, 1155, 1161, 1717-1727, 1781-1786, 1980`, `encoder_silk_libopus.go:293-344`
- **原因**: `entcode.Encoder.Bytes()` で確保→パディングで再確保→`packOpusFramesPadded` で再確保→`append([]byte{toc}, compressed...)` で TOC 1 バイトを先頭に付けるためだけに **パケット全体を再確保＆コピー**している。
- **改善策**:
  1. `entcode.Encoder` に 1 バイトの先頭予約（TOC 用）を許容する `ResetWithPrefix(buf []byte, prefixLen int)` を持たせ、単一バッファで TOC + レンジ符号＋パディングを完結させる。
  2. さらに呼び出し元バッファへ直接書く `EncodeTo(dst []byte, pcm []int16, frameSize int) (int, error)`（および `EncodeFloatTo` / `EncodeFloat32To`）API を追加すればパケット返却も 0-alloc になる。
- **効果**: エンコード 1 フレームあたり 2〜4 allocs 削減。

#### OPUS-05: 入力 PCM に対する 5〜6 回の個別線形走査を融合 `[Exact-Careful]`
- **箇所**: `opus.go:671, 735-739, 890, 895`, `encoder_input.go:160-176`, `encoder_dtx.go:70-109`
- **原因**: 1 パケットのエンコードで入力 PCM（最大 15.36 KB）に対し以下が別々のループで走っている：
  1. `isSilentPCM(pcm)`（671 行、DTX 有効時）
  2. `isDigitalSilence(raw, lsbDepth)`（735 行）
  3. `trackPeakSignalEnergy` → `frameEnergy(raw)`（736 行）
  4. `stereoWidth.compute(raw, ...)`（739 行）
  5. `frameActivity` → `frameEnergy(raw)`（**2 回目の重複計算！** `encoder_dtx.go:96, 103`）
  6. `conditionInput` の `hpCutoff`/`dcReject` の後に **別パスで `sum += f*f`**（`encoder_input.go:168-172`）
- **改善策**: `frameEnergy` の結果を `trackPeakSignalEnergy` から `frameActivity` へ引き継ぎ重複計算を削除。`dcReject` の出力エネルギー累積をフィルタ内ループに融合（モノラル・ステレオとも加算順が完全一致）。`isDigitalSilence` と `frameEnergy` を 1 パス化。

#### OPUS-06: `encodeHybridPacket` の `celtInputFrame` 最大 3 重呼び出しと不要スライス確保 `[Exact]`
- **箇所**: `opus.go:1524, 1531, 1575, 1590, 1697, 2451`
- **原因**:
  1. 1531 行で `celtInput = e.celtInputFrame(celtChunk)` を呼んだ後、1575 行（`hbGain` フェード後）と 1590 行（ステレオ幅フェード後）で再度 `e.celtInputFrame` を呼んでいる。`celtUpsample() > 1`（8/12/16/24 kHz 入力）や `inputResampler != nil` 時に最大 3 回メモリ確保／リサンプリングが走る（リサンプラ状態も二重進行リスクあり）。
  2. `frames := make([][]byte, 0, nFrames)` を `nFrames == 1` でも確保。
  3. `celtInputFrame`（2451 行）が `up > 1` 時に毎回 `make([]float64, n*up*ch)` を確保。
- **改善策**: `e.celtInputFrame` はフェード完了後の 1 回だけ呼ぶ。`nFrames == 1` の高速パス（`frames` スライス不要）、`celtUpsampleBuf` を `Encoder` 構造体に保持して再利用。

#### OPUS-07: `Decode` のパケット構文解析 2〜4 重実行を 1 回に統合 `[Exact]`
- **箇所**: `opus.go:3534, 3542, 4133, 4182, 4366, 4394, 4719, 4759, 5146`
- **原因**: `Decode(data, pcm)` は
  1. `packetOutputSamples` → `inspectPacket`（`splitOpusFramesInto` 1 回目）
  2. `decodePacketFloat` → `inspectPacket`（2 回目）
  3. `concealOnlyPacket` → `splitOpusFramesInto`（3 回目）
  4. `decodeSILKPacket` / `decodeHybridPacket` → `splitOpusFramesInto`（4 回目）
  と、1 パケットで最大 4 回もヘッダ・フレーム長を解析している。
- **改善策**: `inspectPacketInto` が `[maxPacketFrames][]byte` に分割済みフレームスライスごと返し、`Decode` → `decodePacketFloat` → `decode{CELT,SILK,Hybrid}Packet` でそのまま使い回す。

#### OPUS-08: `decodeSILKPacket` / `decodeHybridPacket` の毎パケット `CopyPrimaryStateFrom` 往復コピー廃止 `[Exact]`
- **箇所**: `opus.go:4389-4391, 4570-4574, 4752-4754, 4968-4972`
- **原因**: モノラル用とステレオ用に別々の `silk.Decoder` インスタンスを持っているため、ステレオパケット毎にデコード前後で `CopyPrimaryStateFrom` を 2 回、モノラルパケット毎に 1 回実行している。
- **改善策**: `OPUS-01` と合わせてチャネル 0 / チャネル 1 状態を共有（libopus と同一構造）し、毎パケットの状態コピーを完全撤廃。

#### OPUS-09: `decodeRedundancy` の毎遷移 `celt.NewDecoderEx` 確保廃止 & `applyGain` の `math.Exp` キャッシュ `[Exact]`
- **箇所**: `opus.go:4295, 4693`
- **原因**:
  1. `decodeRedundancy`（4693 行）がモード遷移のたびに新規 `celt.NewDecoderEx` を丸ごと生成して捨てている（5ms デコーダは既に保持しているのに未使用）。
  2. `applyGain`（4295 行）が `gainQ8 != 0` のとき毎パケット `math.Exp(...)` を再計算している。
- **改善策**: キャッシュ済みの 5ms `celt.Decoder` を再利用（または単一 `celt.Decoder` で 5ms 実行）。`SetGain` 時に `d.gainScale` を事前計算してキャッシュ。

#### OPUS-10: `Decode` 出力変換（`float64 -> float32 -> SoftClip -> float64 -> RoundToEven -> int16`）の短縮 `[Exact]`
- **箇所**: `opus.go:4086-4100, 5516-5555`, `softclip.go:131-144`
- **原因**: `pprof` で `float32ToInt16` が CELT デコード **CPU 6.98%**、`libopusFloat32ToInt16` が Hybrid デコード **4.26%** を占める。`float32` を `float64` に変換してから `math.RoundToEven` を呼んでいる（さらに `decodePacketFloat` の `[]float64` を `softclipScratch []float32` にコピーして `softClipIsIdentity` で全走査し、直後に `float32ToInt16` でもう 1 パス全走査している）。
- **改善策**:
  1. `softclipMem == [2]float32{0, 0}` の定常時、`float64->float32` 変換と `[-1, 1]` 範囲チェックを 1 パスに融合し、全サンプルが `[-1, 1]` 内なら `int16` 変換まで単一パスで完結させる（範囲外を検出した瞬間だけ `softClipFull` へフォールバック）。
  2. amd64/arm64 では `math.RoundToEven` は CPU 命令 1 発（`ROUNDSD`/`FRINTN`）にコンパイルされるが、内部 PCM が `float32` 化（`ARCH-01`）されればコピー自体が消える。

#### OPUS-11: `bandwidth_detect.go`（`ModePolicyLegacy` 55 KB/op の元凶）と `packOpusFramesCode3Run` / `encoder_plc_frame.go` 最適化 `[Exact]`
- **箇所**: `bandwidth_detect.go:136-168`, `opus.go:3821, 3846`, `encoder_plc_frame.go:71`, `packet_lbrr.go:13, 21`
- **原因**:
  1. `bandwidth_detect.go:signalSpectrumPower` が `ModePolicyLegacy` エンコードで **55,885 B/op（~100 µs/op）** を消費：`mono`, `buf`, `dsp.RealFFT`（`NewFFT` + 複素配列）, `power` を毎回確保し、Hann 窓で `math.Cos` を `n` 回呼んでいる。
  2. `packOpusFramesCode3Run`（`opus.go:3821, 3846`）は `capacity` にフレーム本体の長さ合計を含め忘れて再確保を誘発し、`make([]byte, padBytes)` で一時ゼロスライスを確保している。
  3. `encoder_plc_frame.go:71` が関数内で毎回 `map[int]int` を構築している。
  4. `PacketHasLBRR`（`packet_lbrr.go:13, 21`）が `inspectPacket` 後に `splitOpusFrames` を再実行してヒープ確保している。
- **改善策**: `bandwidth_detect` はサイズ別キャッシュ Hann 窓＋再利用 `FFTConfig`（または `opusFFTPlan`）＋スクラッチ化、`packOpusFramesCode3Run` の正確な事前確保と `slices.Grow` によるインプレース 0 拡張、`durations` を `switch` 化、`PacketHasLBRR` のスタック `frameBuf` 化。

---

### 4.7 Multistream / Surround / Projection / MappingMatrix（MS-01〜MS-07）

#### MS-01: `MultistreamDecoder.DecodeFECFloat` の 2 重 `cloneState()` を解消 `[Exact]`
- **箇所**: `multistream.go:899-903`
- **原因**: 899 行で `staged[stream] = d.decoders[stream].cloneState()` した後、903 行で `staged[stream].DecodeFECFloat(...)` を呼び、その中で再度 `cloneState()` が走る。5.1ch（4 ストリーム）で **8 回 × 4.14 MB = ~33 MB** を 1 回の FEC 呼び出しで消費する。
- **改善策**: `OPUS-02` と合わせ、検証後の直接デコード（または単一の内部非クローンヘルパ呼び出し）に変更。

#### MS-02: `MultistreamDecoder.Decode` の 2 重パケット分割・毎ストリームコピー・O(streams×channels) 走査を解消 `[Exact]`
- **箇所**: `multistream.go:693-723, 745-758, 1101-1263`
- **原因**:
  1. `Decode` が `multistreamPacketDuration`（`splitMultistreamPackets` 1 回目）を呼んで結果を捨てた直後に `decodeMapped`（`splitMultistreamPackets` 2 回目）を呼んでいる。
  2. `splitMultistreamPackets` が最終ストリームをコピーし、非最終ストリームごとに `sizes := make([]int, 0, 48)`, `frames := make([][]byte, count)`, `packOpusFrames` + 新規 `packet` バッファ確保を行っている。
  3. `decodeMapped` が毎パケット `d.mapping` を全チャネル走査し、`out := make([]float64, duration*d.channels)` を確保。
- **改善策**: パケット分割は 1 回だけ行い、`[255][]byte` スライス＋自己区切り解除なしで直接デコーダへフレーム配列を渡す。`NewMultistreamDecoder` 時にストリームごとの出力チャネル対応表を事前計算し、デコード用スクラッチ `out` を再利用する。

#### MS-03: `MultistreamEncoder` の毎フレーム入力コピー・ストリームバッファ・`msRepacketize` 再確保削減 `[Exact]`
- **箇所**: `multistream.go:447, 464, 481, 521`, `multistream_libopus.go:79, 208, 212, 215, 264-384`
- **原因**: `Encode`/`Encode24`/`EncodeFloat32` が毎回 `floatPCM` を確保し、`encodeLibopus` が `rates`, `out`, ストリームごとの `streamPCM`, `streamAnalysis` を確保。さらに `msRepacketize` が `inspectPacket` + `splitOpusFrames` + 初期容量 1 の `head := []byte{toc}`（即再確保）+ `encodeOpusFrameLength`（2 バイトスライス確保）を毎ストリーム繰り返している。また `findMappedChannel` を毎フレーム走査。
- **改善策**: 入力チャネルマッピング表をコンストラクタで事前計算、`floatPCM`/`streamPCM`/`streamAnalysis`/`rates` を `MultistreamEncoder` スクラッチに保持、`msRepacketize` を `out` への直接追記（`appendFrameSize` 使用、事前容量計算）に変更。

#### MS-04: `SurroundEncoder.analyzeSurroundFrame` の毎フレーム `analyzer.Clone()` 廃止 `[Exact]`
- **箇所**: `surround.go:241-265`
- **原因**: 毎フレーム `next := e.analyzer.Clone()` で `SurroundAnalyzer` 丸ごとを複製し、終了後に `e.analyzer = next` で差し替えている。さらに毎ストリーム `streamMask := make([]float64, streamChannels*21)` を確保し `findMappedChannel` を走査。
- **改善策**: `SurroundAnalyzer` をインプレース更新する（失敗時のロールバックが必要なら小サイズの `oldBandE` だけ一時退避）。`streamMask` は `[2*21]float64` 固定配列を使用。

#### MS-05: `MappingMatrix.multiplyFloat32` の列ストライド解消（事前転置 `float32` キャッシュ）と出力スクラッチ化 `[Exact]`
- **箇所**: `mapping_matrix.go:116-128`, `projection.go:269, 286, 299, 333`
- **原因**: `MappingMatrix.data` は列優先（`col*m.rows + row`）なのに、`multiplyFloat32` の内側ループが `col` のため、毎 MAC ストライド `m.rows` でジャンプしつつ `int16 -> float32` 変換を行っている（5 次 Ambisonics 36ch で 1.24M MACs/frame）。さらに `ProjectionEncoder.Encode*` が 276 KB の `floatPCM` と 276 KB の `out` を毎フレーム新規確保。
- **改善策**: `MappingMatrix` 構築時に行優先 `[]float32` 配列（または `[]float32` 事前変換）をキャッシュすれば加算順（`col = 0..inputChannels-1`）は 100% 同一のまま完全シーケンシャルアクセス＋型変換ゼロになる。`ProjectionEncoder` に `inputScratch` / `mixedScratch` を持たせる。
- **効果**: 高次 Ambisonics ミキシング **2〜4× 高速化**＋毎フレーム **~550 KB 確保削減**。

#### MS-06: `ProjectionDecoder.demixFloat` / `demixInt16` / `demixInt24` のループ順序交換（キャッシュ 36× 改善） `[Exact]`
- **箇所**: `projection.go:635-690`
- **原因**:
  1. `demixFloat`, `demixInt16`, `demixInt24` の 3 関数すべてが `for col -> for i -> for row` のループ順で、出力配列（5 次 Ambisonics で 138〜276 KB）を `cols` 回（最大 36 周）丸ごと舐め回して L1/L2 キャッシュをスラッシングしている。
  2. `demixFloat` は `acc ([]float32)` と `out ([]float64)` の両方を毎フレーム確保（`DecodeFloat32` ではさらに 3 つ目の `[]float32` を確保！）し、三重ループ内で毎回 `float32(1.0/32768) * float32(d.demixing.data[...])` を計算している。
- **改善策**: ループ順を **`for i -> for col -> for row`** に入れ替える。各 `(i, row)` に対する `col` の加算順序は 100% 同一（**ビット完全一致**）のまま、フレーム `i` の出力行 `out[i*ch : (i+1)*ch]` が完全に L1 レジスタ/キャッシュに留まり、全バッファ走査が 36 周 → 1 周になる。さらに `m * (1.0/32768)` を `float32` 配列に事前計算し、`DecodeFloat32` は `acc` を直接返却（2 回の余分な変換＆確保を削除）。
- **効果**: 高次 Ambisonics デミキシング **3〜6× 高速化**＋メモリ確保 1/3。

#### MS-07: `Repacketizer.Out` / `OutRange` の毎回 64 KB バッファ過剰確保を修正 `[Exact]`
- **箇所**: `repacketizer.go:24, 59`, `repacketizer_libopus.go:241, 271, 410-416`
- **原因**: `Repacketizer.Out()` が `maxlen = outRangeUnbounded(...)`（約 62 KB）を渡し、`repacketizeOutRange`（271 行）が `data := make([]byte, 0, min(maxlen, 1<<16))` を実行するため、**わずか 100 バイトの 2 フレームパケットでも毎回 64 KB をヒープ確保**している！また `Reset()` が `r.frames = nil` で容量を破棄し、`lens := make([]int, count)` も最大 48 要素なのにヒープ確保している。
- **改善策**: 実際の必要サイズ `totSize`（フレーム長合計＋ヘッダ＋パディング）を先に計算してから `make([]byte, 0, totSize)` する。`lens` は `[48]int` スタック配列、`Reset` は `clear(r.frames); r.frames = r.frames[:0]`。
- **効果**: `Repacketizer.Out()` のメモリ確保を **64 KB → 実パケットサイズ（最大 600× 削減）**。

---

### 4.8 `internal/extensions` & `oggopus` コンテナ（OGG-01〜OGG-06）

#### OGG-01: `oggopus` の Ogg CRC32 をビットループからスライシングテーブル化（8〜25× 高速化） `[Exact]`
- **箇所**: `oggopus/page.go:208-239`
- **原因**: `checksum` および `checksumWithZeroedField` が 1 バイトごとに 8 回のビットシフト＋条件分岐（`if crc&0x80000000 != 0`）を実行しており、さらに `checksumWithZeroedField` は全バイトで `if i >= 22 && i < 26` を評価している。
- **改善策**: MSB-first 多項式 `0x04c11db7` の 256 エントリ LUT（1 KB）または Slicing-by-4 / Slicing-by-8 テーブル（4〜8 KB）をパッケージ初期化で構築。`checksumWithZeroedField` は `crcUpdate(0, page[:22])` → 4 ゼロバイト → `crcUpdate(crc, page[26:])` の 3 区間処理に分岐なしで分割。
- **効果**: Ogg ページ読み書きの CRC 計算を **8〜25× 高速化**。

#### OGG-02: `oggopus.ReadPage` の 6 アロケーション・3 重コピーを 1〜2 アロケーション・コピー 0 回へ `[Exact]`
- **箇所**: `oggopus/page.go:168-199`
- **原因**: `ReadPage` が `header(27B)`, `rest(segCount)`, `body(bodyLen)`, `encoded(合計)` の 4 スライスを確保してコピーした挙句、`ParsePage(encoded)` を呼んで `Segments` と `Data` をさらにコピー＆再 `Validate()` している（計 6 allocs, 3 重コピー）。
- **改善策**: `var header [27]byte` をスタック確保し、`segments` と `data` を直接 `io.ReadFull` で読み込んで 3 片に対して分割 CRC を計算して返す。
- **効果**: `ReadPage` のアロケーション 6→2（単一バッファスライス共有なら 6→1）、メモリ帯域 1/3。

#### OGG-03: `PacketWriter.flush` の 3 重コピー廃止とページ出力バッファ再利用 `[Exact]`
- **箇所**: `oggopus/packet.go:367-376`, `oggopus/page.go:92-118`
- **原因**: `flush` が `Segments: append([]byte(nil), w.segments...)`, `Data: append([]byte(nil), w.data...)` とコピーした後、`WritePage` → `MarshalBinary` がさらに `out` を確保して全コピーしている（`w.segments`/`w.data` は `WritePage` 後にしかリセットされないのに不要コピー）。
- **改善策**: `PacketWriter` に再利用可能な `pageBuf []byte` を持たせ、ヘッダ＋セグメント＋データを直接組み立てて CRC 計算→`writeAll` する。
- **効果**: Ogg ページ書込 **3 allocs/page → 0 allocs/page**。

#### OGG-04: `PacketReader` / `Reader` のキュー先頭ポップによるメモリリーク防止と単一ページパケット最適化 `[Exact]`
- **箇所**: `oggopus/packet.go:104-106, 162, 174-184, 197`, `oggopus/stream.go:154-156, 223`
- **原因**:
  1. `r.queue = r.queue[1:]`（`packet.go:105`）および `r.pending = r.pending[1:]`（`stream.go:155`）がポップ済み要素をゼロクリアせず、消費済み `Packet.Data` への参照をバッキング配列に残し続ける。さらに空になってもスライス基底ポインタが前進し続け、次の `append` で毎回再確保する。
  2. `readPage` が単一ページ内で完結する 99% のパケットでも一旦 `r.partial` に `append` してからさらに `append([]byte(nil), r.partial...)` へ 2 重コピーしている。
- **改善策**: 読み出しインデックス `head` を持ち、消費時に `q[head] = Packet{}` で参照を切り、空になったら `q = q[:0]; head = 0` でバッキング配列を再利用。`len(r.partial) == 0 && lace < 255` の高速パスでは `pageData[offset:offset+size]` から直接コピー。

#### OGG-05: `oggopus/seek.go` の二分探索時 32 KB×最大 64 回確保と二重シーク/リード解消 `[Exact]`
- **箇所**: `oggopus/seek.go:207-253`
- **原因**: `scanNextPage` が二分探索の毎ステップ（最大 64 回）で `make([]byte, 32*1024)` を確保（最大 2 MB/シーク）し、バッファ内で `"OggS"` を見つけた後にわざわざ `rs.Seek` + `ReadPage(rs)` で I/O をやり直している。
- **改善策**: 32 KB バッファを `SeekPCM` 側で 1 回確保して引き回し、バッファ内にページ全体が収まっている場合は `ParsePage` でメモリ内検証して余分な `Seek`+`Read` を省略する。

#### OGG-06: `internal/extensions` と `extensions.go` の不要コピー・`Count` 全展開・一時確保削減 `[Exact]`
- **箇所**: `internal/extensions/extensions.go:230-256, 298-300, 325, 414-418`, `extensions.go:70, 105`
- **原因**:
  1. `extensions.Count` が `Parse` を呼んで全 `Extension` と各 `Data` のコピーを確保した挙句、長さだけ返して捨てている。
  2. `opus.PacketExtensionsParse`（`extensions.go:70`）が既にコピー済みの `ext.Data` をさらに 2 重コピーし、`PacketExtensionsGenerate`（105 行）も読み取り専用の `ext.Data` を不要コピーしている。
  3. `extensions.Generate`（298-300, 414-418 行）が `nbFrames <= 48` なのに 3 本の `[]int` をヒープ確保し、`out` を容量 0 から伸長し、`targetLen > 0` 時に `padding` を別確保して `append(padding, out...)` で再コピーしている。
- **改善策**: `Count` をコピー無しの専用イテレータループ化、2 重コピー削除、`frameMin/Max/Repeat` を `[MaxFrames]int` スタック配列化、`Generate` の事前容量見積り＋後方詰めまたは単一確保化。

---

### 4.9 横断的・アーキテクチャ施策（ARCH-01〜ARCH-04）

#### ARCH-01: 内部信号パイプラインの `float32` 統一（帯域・キャッシュ半減） `[Exact-Careful]`
- **対象**: `opus.go`, `encoder_input.go`, `internal/celt/{encoder,decoder,bands,postfilter}.go`, `internal/silk/{encoder,decoder,pitch_flp,pitch_core_flp32,noise_shape_flp32,lpc_in_pre}.go`, `internal/dsp/mdct_celt.go`
- **現状と課題**: 本実装は libopus float 版とのビット完全一致のため、実質的な演算のほとんどを `float32(...)` で丸めながら実行しているにもかかわらず、パッケージ間・関数間のバッファ型が `[]float64` のまま残っている。結果として：
  1. メモリ使用量・L1/L2 キャッシュフットプリント・メモリ帯域が **すべて 2 倍**。
  2. ホットループの内外で大量の `CVTSD2SS` / `CVTSS2SD`（`float64↔float32` 変換命令）が発行されている（`pprof` 上でも `f32`, `float32ToInt16`, `buildLPCInPreInto`, `conditionInput`, `decodeSoftClipped32` に顕著）。
- **改善策**: 公開 API（`EncodeFloat([]float64)` / `DecodeFloat() []float64`）の境界のみ `float64` を維持し、内部パイプライン（`conditionBuf`, `delayBuffer`, CELT の `X`/`bufs`/`overlap`/`PostFilter`, SILK の `xBuf`/`pitchBuf`/`noiseShapeBuf`/`lpcInPre`, `CLTMDCTForward32`/`Backward32`）を段階的に `[]float32` へ移行する。`Encode` (`int16`) / `EncodeFloat32` / `Decode` (`int16`) / `DecodeFloat32` では一切 `float64` を経由せず完結する。
- **効果**: エンコード・デコード全般で **メモリフットプリント半減＋CPU 10〜20% 改善**（`float32` 丸め仕様をむしろ自然に表現でき、コードの可読性も向上）。

#### ARCH-02: ゼロアロケーション出力 API（`EncodeTo` / `DecodeFloat32To` 等）の追加 `[Exact]`
- **対象**: `opus.go`, `multistream.go`, `surround.go`, `projection.go`
- **現状と課題**: 現行の `Encode` / `EncodeFloat` / `EncodeFloat32` は毎回新しい `[]byte` パケットを返し、`DecodeFloat` / `DecodeFloat32` は毎回新しい PCM スライスを返すため、呼び出し側でバッファプールを使っていても最低 1 alloc/op が避けられない（Go のリアルタイム音声サーバ／WebRTC SFU では毎秒数万〜数十万パケットで GC 負荷に直結）。
- **改善策**: `EncodeTo(dst []byte, pcm []int16, frameSize int) (int, error)`、`EncodeFloat32To`、`DecodeFloat32To(data []byte, pcm []float32) (int, error)` 等の caller-owned バッファ書き込み API を追加し、既存 API はその薄いラッパにする。
- **効果**: `EC-03` + `CELT-05` + `SILK-01/03` と組み合わせて、リアルタイム処理経路の **完全 0 allocs/op（エンドツーエンド）** を実現。

#### ARCH-03: 純粋 Go 最適化後の選択的アセンブリ／SIMD ターゲット候補（amd64 AVX2/SSE4.1, arm64 NEON） `[Exact-Careful]`
Pure Go でのアルゴリズム・メモリ・ループ最適化を完了した後、プロファイル上位に残る純粋計算カーネルは以下の 6 関数に収束する。ビルドタグ（`!purego`）で Pure Go フォールバックを維持しつつ AVX2 / NEON 化すれば libopus ネイティブ比で大幅な接近が見込める：
1. **`internal/celt.pitchXcorr32`**（CELT エンコード 1 位）— 4-way 並列内積（FMA を使わず `MULPS`+`ADDPS` でスカラー float32 と完全一致可能）。
2. **`internal/silk.silkNoiseShapeQuantizerDelDec` の `sAR2` / `sLPC` フィルタ**（SILK エンコード 1 位）— 固定小数点 16/24 タップ積和。
3. **`internal/silk.silkWarpedAutocorrelationFLP32` / `silkInnerProductFLP32` / `xcorrKernelFLP`**（Hybrid エンコード 1〜4 位）— 4〜8 レーン浮動小数点積和。
4. **`internal/dsp.opusFFTPlan.bfly{2,3,4,5}` & MDCT 回転**（CELT/Hybrid デコード上位）— 複素バタフライ並列化。
5. **`internal/silk.iirFIRInterpol` / `downFIRInterpol` / `silkLPCAnalysisFilter`**（SILK デコード 1〜3 位）— 固定小数点 FIR/LPC 積和。
6. **`opus.MappingMatrix.multiplyFloat32` / `ProjectionDecoder.demix*`**（高次 Ambisonics）— 行列×ベクトル積。

#### ARCH-04: 最適化の安全網（ベンチマーク網羅とパリティ回帰防止） `[Exact]`
- **現状の空白**:
  - `BenchmarkPerf` は `ModePolicyLegacy`（complexity 5）、`BenchmarkPerfDefault` も complexity 5 中心で、**`EncoderProfileLibopus`（complexity 9：`TonalityAnalysis` + `thetaRdo` が両方動く実戦設定）**、**`NewDecoder` / `NewEncoder` 初期化**、**`DecodeFECFloat` 実パス**、**`Multistream` / `Surround` / `Projection`**、**`oggopus` 読み書き／シーク** のベンチマークが存在しない。
- **改善策**: これらをカバーするベンチマーク群を整備し、各最適化 PR ごとに `TestPerfPredictivePacketRegression`・libopus ビット一致テスト・`benchstat` 比較を必須ゲートとすることで、品質劣化・パリティ逸脱なしに継続的高速化できる体制を確立する。

---

## 5. 推奨実装フェーズと検証方針

| フェーズ | 対象 ID | 主眼 | 見込み成果 |
|---|---|---|---|
| **Phase 1（即効・ゼロリスク・最大メモリ効果）** | `OPUS-01`, `OPUS-02`, `MS-01`, `OPUS-03`, `CELT-02`, `CELT-05`, `EC-03(Decoder)`, `OGG-01..03`, `MS-07` | 初期化メモリ暴発・FEC クローン・デコード 0-alloc・Ogg CRC/コピー修正 | `NewDecoder` **-95% メモリ**、FEC **100×+**、全デコード **0 allocs/op**、Ogg **5〜10×**、CELT エンコード **-35 KB/frame** |
| **Phase 2（エンコード 0-alloc 化）** | `SILK-01`, `SILK-03`, `SILK-09`, `CELT-01`, `CELT-07`, `CELT-14`, `CELT-15`, `DSP-01`, `EC-03(Encoder)`, `OPUS-04`, `OPUS-06`, `MS-02..04` | SILK / Hybrid / CELT(complexity 9) / Multistream のヒープ確保根絶 | SILK エンコード **105→<5 allocs/op**、Hybrid ステレオ **175→<10 allocs/op**、CELT c9 **150+→<5 allocs/op** |
| **Phase 3（CPU ホットスポット集中改修）** | `EC-01,02,04,05`, `CELT-03,04,06,08,09,10,12`, `SILK-04,05,06,07,08,10,13,14`, `DSP-02,03,05,06`, `RS-01..04`, `MS-05,06` | レンジコーダ除算半減、PVQ L1d 常駐化、NSQ・WarpedAC・Pitch・Resampler・FFT・Projection の計算最適化 | エンコード・デコード全般で **CPU 25〜50% 高速化**（Projection / Resampler / Ogg は **3〜10×**） |
| **Phase 4（構造刷新 & 選択的 SIMD）** | `SILK-02`, `ARCH-01`, `ARCH-02`, `ARCH-03` | `analyzeNLSF` 2 重実行統合、内部 `float32` パイプライン統一、`EncodeTo` ゼロコピー API、任意 AVX2/NEON カーネル | さらなる **CPU 20〜40% 改善**＋メモリ半減＋完全 0-alloc リアルタイム API |

### 検証コマンド（各変更後）
- 正当性・パリティ確認: `go test ./...`（特に `TestPerfPredictivePacketRegression` および `OPUS_LIBOPUS_AUDIT=1` 環境下での oracle テスト）
- 性能・アロケーション比較: `go test -run=^$ -bench="BenchmarkPerf" -benchmem -count=5 .` + `benchstat`
- エスケープ回帰確認: `go build -gcflags="-m" ./internal/entcode ./internal/dsp ./internal/celt ./internal/silk`
