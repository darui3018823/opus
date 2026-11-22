# Opus Pure-Go 実装 メモリ効率化・高速化 最適化機会 網羅レポート
Date: 2026/11/22
Author: Gemini 4 Neon with Medium effort

———

## 1. エグゼクティブサマリー & ベースライン計測

本レポートは、本リポジトリ（Pure-Go Opus 実装 `github.com/darui3018823/opus`）の全パッケージ（ルート `opus`、`internal/entcode`、`internal/dsp`、`internal/resampler`、`internal/celt`、`internal/silk`、`internal/extensions`、`oggopus`）をコード精読および `pprof` CPU/メモリプロファイリングによって網羅的に監査し、**更なるメモリ効率化（アロケーション削減・常駐メモリ削減）と高速化（CPU サイクル削減・キャッシュ効率向上）** が見込める **52 項目** の具体的改善案をまとめたものである。

### ベースライン計測結果（11th Gen Intel Core i7-11700 @ 2.50GHz, Windows/amd64）

| ベンチマーク | 時間 (ns/op) | メモリ (B/op) | アロケーション (allocs/op) |
|---|---:|---:|---:|
| `BenchmarkPerfDefault/encode/celt/mono/96k` | 104,542 | 1,897 | 16 |
| `BenchmarkPerfDefault/encode/celt/stereo/128k` | 167,015 | 3,044 | 17 |
| `BenchmarkPerfDefault/encode/silk/mono/12k` | 373,355 | 6,890 | 105 |
| `BenchmarkPerfDefault/encode/silk/stereo/12k` | 400,053 | 6,880 | 104 |
| `BenchmarkPerfDefault/encode/hybrid/mono/24k` | 230,989 | 6,520 | 74 |
| `BenchmarkPerfDefault/encode/hybrid/stereo/36k` | 648,012 | 31,058 | 175 |
| `BenchmarkPerf/encode/celt/mono/48k/20ms` | 213,732 | 55,883 | 20 |
| `BenchmarkPerf/encode/celt/stereo/48k/20ms` | 311,933 | 56,930 | 21 |
| `BenchmarkPerf/encode/silk/mono/48k/20ms` | 438,597 | 6,969 | 109 |
| `BenchmarkPerf/encode/silk/stereo/48k/20ms` | 697,638 | 29,404 | 188 |
| `BenchmarkPerf/encode/hybrid/mono/48k/20ms` | 333,220 | 60,729 | 79 |
| `BenchmarkPerf/encode/hybrid/stereo/48k/20ms` | 564,363 | 83,721 | 154 |
| `BenchmarkPerf/decode/celt/mono/48k/20ms` | 37,417 | 633 | 4 |
| `BenchmarkPerf/decode/celt/stereo/48k/20ms` | 59,978 | 824 | 4 |
| `BenchmarkPerf/decode/silk/mono/48k/20ms` | 27,665 | 100 | 1 |
| `BenchmarkPerf/decode/silk/stereo/48k/20ms` | 51,163 | 113 | 1 |
| `BenchmarkPerf/decode/hybrid/mono/48k/20ms` | 45,569 | 642 | 4 |
| `BenchmarkPerf/decode/hybrid/stereo/48k/20ms` | 89,495 | 857 | 4 |

### 最重要ボトルネック Top 5（インパクト大・低リスク）
1. **SILK エンコードのフレーム毎ヒープアロケーション（105〜188 allocs/op, 最大 31 KB/op）**: `silkNLSFEncode`（生存候補ループ内 `make` + `sort.SliceStable`）、`stereoEncState.lrToMS`（7 スライス確保）、`selectLTPGainsVQWithGain`、`silkGainsQuant`、`burg.go`、`lpc_in_pre.go` 等のスタック配列化・スクラッチ再利用でほぼ **0 allocs/op** まで削減可能。
2. **レガシー帯域検出 `signalSpectrumPower` の 53 KB/frame & 11,200 回超の三角関数呼び出し**: `bandwidth_detect.go` が毎フレーム `dsp.RealFFT`（キャッシュなし `dsp.FFT` 経由）＋960 回の `math.Cos` を実行しており、CELT エンコード時間全体の **約 50%（+110 µs/frame, +53 KB/op）** を消費。
3. **CELT エンコードにおける `FrameTrace` の無条件構築 & `entcode.Encoder` の多重アロケーション**: `internal/celt/encoder.go` がテスト専用の `e.lastTrace` を本番フレームでも毎回コピー（約 35 KB/frame の memcpy + `traceBandLogE` 確保）。また `entcode.Encoder` の `endBytes` が `nil` から段階的 `append` 成長（CELT ステレオ全アロケーションの **26.1%**）。
4. **CELT / SILK デコードの残り 1〜4 allocs/op の完全ゼロ化**: `entcode.NewDecoder`（全デコード共通 1 alloc）と CELT の `UnquantizeCoarseEnergy` / `celtTFDecode` / `decodeDynalloc`（3 allocs）をデコーダ構造体埋め込み＋固定長配列化することで、デコード内部パスを **完全 0 allocs/op** にできる。
5. **SILK NSQ (`silkNoiseShapeQuantizerDelDec`)・CELT PVQ (`opPVQSearch`)・プリフィルタ (`pitchXcorr32`)・レンジコーダ (`entcode`) のホットループ最適化**: SILK エンコード CPU の **48.5%** を占める `silkNoiseShapeQuantizerDelDec`（サンプル毎の `% 40` 除算 2 回、状態コピー、境界チェック）、CELT ステレオ CPU の **33.9%** を占める `opPVQSearch` + `runPrefilter`、レンジコーダの二重除算・不要な `err` チェック等を改善することで大幅なスループット向上が得られる。

---

## 2. `internal/entcode`（レンジコーダ・エントロピー符号化）— 6 項目

### [OPT-01] `entcode.Decoder` の構造体再利用（`Init` メソッド導入）
- **対象**: [internal/entcode/decoder.go](internal/entcode/decoder.go#L29-L51), [internal/celt/decoder.go](internal/celt/decoder.go#L199)
- **問題**: パケットデコード毎に `entcode.NewDecoder(data)` が `*entcode.Decoder` をヒープ確保する。SILK デコード（`1 allocs/op, 100 B/op`）の唯一の残存アロケーションであり、CELT デコード（`4 allocs/op`）の 1 つでもある。
- **改善案**: `func (dec *Decoder) Init(data []byte)` を追加し、`opus.Decoder` / `celt.Decoder` / `silk.Decoder` 内に `entcode.Decoder` を値埋め込みで保持して再利用する。
- **期待効果**: 全デコードパスで **-1 alloc/op（-100 B/op）**。SILK デコードは **0 allocs/op** に到達可能。
- **ビット一致リスク**: なし（完全に等価）。

### [OPT-02] `entcode.Encoder` の再利用（`Reset`）と単一フラットバッファ化 / `endBytes` 事前確保
- **対象**: [internal/entcode/encoder.go](internal/entcode/encoder.go#L43-L92), [internal/entcode/encoder.go](internal/entcode/encoder.go#L333-L335)
- **問題**:
  1. `NewEncoder(capacity)` が毎フレーム `*Encoder` と `buf` をヒープ確保する（CELT ステレオ allocs の **10.1%**）。
  2. `endBytes` が `nil` で初期化され、`writeByteAtEnd` の `append` で 1→2→4→8→16→32→64→128 と何度も再確保が発生する（CELT ステレオ全 allocs の **26.1%** でワースト 1 位）。
  3. `Bytes()` が raw bits 存在時に `out := make([]byte, n)` でさらに別バッファを確保して `copy` する。
- **改善案**: libopus の `ec_enc` と同じく、単一フラットバッファ（前方からレンジ符号、末尾から raw bits を書き込む）または再利用可能な `Reset(capacity int)` + `BytesInto(dst []byte) []byte` を導入し、エンコーダ構造体に埋め込む。
- **期待効果**: CELT / SILK / Hybrid エンコードで **-4〜8 allocs/op**、`writeByteAtEnd` の再確保コスト完全解消。
- **ビット一致リスク**: なし（`Shrink` 時の末尾移動を `copy` で正確に実装すれば完全一致）。

### [OPT-03] `entcode.Decoder.Decode` + `DecodeUpdate` の二重 32bit 整数除算の排除
- **対象**: [internal/entcode/decoder.go](internal/entcode/decoder.go#L221-L250)
- **問題**: `Decode(ft)` で `r := dec.rng / ft` を計算した後、直後に呼ばれる `DecodeUpdate(fl, fh, ft)` で再び `r := dec.rng / ft` を実行している。ハードウェア 32bit 除算（`DIV`）はレイテンシが大きく、PVQ や Laplace の全シンボルで 2 倍の除算コストが発生している。
- **改善案**: libopus の `ec_dec.ext` と同様に `dec.ext = r` をキャッシュし、`DecodeUpdate` ではキャッシュした `dec.ext` を使用する（`DecodeUpdate` 単体呼び出しがないか確認のうえ適用）。
- **期待効果**: `Decode` + `DecodeUpdate` ペアの除算回数を **50% 削減**。
- **ビット一致リスク**: なし（ビット完全一致）。

### [OPT-04] Laplace 符号化・復号における 2 の冪専用高速パス（`EncodeBin` / `DecodeBin`）導入
- **対象**: [internal/entcode/laplace.go](internal/entcode/laplace.go#L85), [internal/entcode/laplace.go](internal/entcode/laplace.go#L96), [internal/entcode/laplace.go](internal/entcode/laplace.go#L128)
- **問題**: `EncodeLaplace` / `DecodeLaplace` は常に `ft = 32768 (1<<15)` だが、汎用の `enc.Encode(..., 32768)` / `dec.Decode(32768)` / `dec.DecodeUpdate(..., 32768)` を呼んでおり、定数伝播されずランタイム除算 `rng / 32768` が実行されている。
- **改善案**: libopus の `ec_encode_bin(fl, fh, bits)` / `ec_decode_bin(bits)` を実装し、`r := rng >> bits` のビットシフトに置き換える。
- **期待効果**: CELT コースエネルギー符号化・復号の全バンド・全チャネルで除算命令を 1 サイクルのシフト命令に置換。
- **ビット一致リスク**: なし（`rng` は `uint32` のため `rng / (1<<15) == rng >> 15` は厳密に同一）。

### [OPT-05] `entcode.Decoder` の未使用 `err` フィールドチェック削除 & `DecodeIcdf` の最適化
- **対象**: [internal/entcode/decoder.go](internal/entcode/decoder.go#L24), [internal/entcode/decoder.go](internal/entcode/decoder.go#L163-L225), [internal/entcode/decoder.go](internal/entcode/decoder.go#L343-L362)
- **問題**: `dec.err` はファイル内のどこからも一度も代入されていない（常に `nil`）にもかかわらず、`DecodeIcdf`（デコード CPU の **5.2%**）、`DecodeBitLogp`、`Decode`、`DecodeBit` の全シンボル処理で毎回 `if dec.err != nil` を分岐評価している。また `DecodeIcdf` のループ内 `icdf[ret]` に毎回境界チェックが入る。
- **改善案**: 未使用の `if dec.err != nil` 分岐を削除し、`DecodeIcdf` は `for ret, v := range icdf` 形式にすることで Go コンパイラの BCE（Bounds Check Elimination）を有効化する。
- **期待効果**: 毎フレーム数百回呼ばれる `DecodeIcdf` / `DecodeBitLogp` の分岐・境界チェック削減。
- **ビット一致リスク**: なし。

### [OPT-06] `TellFrac` の補正テーブルをパッケージ変数へ移動
- **対象**: [internal/entcode/decoder.go](internal/entcode/decoder.go#L76), [internal/entcode/encoder.go](internal/entcode/encoder.go#L172)
- **問題**: `correction := [8]uint32{...}` が関数内ローカル配列として定義されている。
- **改善案**: パッケージレベルの `var tellFracCorrection = [8]uint32{...}` に昇格し、かつ `b` は `[0, 7]` であるため `correction[b&7]` で境界チェックを除去する。
- **期待効果**: CELT レート配分・PVQ で頻繁に呼ばれる `TellFrac` のマイクロ高速化。

---

## 3. `internal/dsp`（FFT・MDCT・窓関数・基本演算）— 9 項目

### [OPT-07] `dsp.OpusFFTScaled32` の毎呼び出しヒープアロケーション排除
- **対象**: [internal/dsp/fft_opus.go](internal/dsp/fft_opus.go#L314-L327)
- **問題**: トナリティ解析等で呼ばれる `OpusFFTScaled32` が毎回 `fout := make([]opusComplex, n)`（480 点で 3,840 B）をヒープ確保している。
- **改善案**: `var buf [480]opusComplex` のスタック配列を使い、`n <= 480` のときは `fout := buf[:n]` とする（または呼び出し元スクラッチ受け渡し版を用意）。
- **期待効果**: トナリティ解析実行フレーム毎の 3.84 KB ヒープ確保を **0 allocs** に削減。

### [OPT-08] `CELTMode.cltMDCTForward` の static パスにおける未使用 `fwdFC`/`fwdZ` スクラッチ確保の排除
- **対象**: [internal/dsp/mdct_celt.go](internal/dsp/mdct_celt.go#L264-L269)
- **問題**: 標準 48kHz CELT（N=120/240/480/960）は常に `static == true` で実行されるにもかかわらず、`if len(m.fwdF) != N2` ブロックで `m.fwdFC`（`[]Complex`, 7.68 KB）と `m.fwdZ`（`[]Complex`, 7.68 KB）を無条件確保している。これらは `static == false` パスでしか使われない。
- **改善案**: `fwdFC` / `fwdZ` の確保を `if !static` 側に移動する。
- **期待効果**: `CELTMode` 1 インスタンスあたり **-15.36 KB** の常駐メモリ削減（マルチストリーム/サラウンドではストリーム数倍）。

### [OPT-09] `CELTMode.twiddles()` および `opusFFTPlan.twiddles` の重複テーブル共有化
- **対象**: [internal/dsp/mdct_celt.go](internal/dsp/mdct_celt.go#L27-L35), [internal/dsp/fft_opus.go](internal/dsp/fft_opus.go#L115-L121)
- **問題**:
  1. `CELTMode.twiddles()` がインスタンス毎に `make([]float32, m.N)` を確保して `libopusMDCTTwiddleBits` から復元している（ロング＋ショートモードで 1 エンコーダ/デコーダ毎に約 4.3 KB）。
  2. `buildOpusFFTPlan` が 4 つの静的サイズ（60/120/240/480）それぞれに全く同一の 480 要素 `twiddles` スライスを 4 重確保している（4 × 3.84 KB = 15.36 KB）。
- **改善案**: パッケージ初期化時に不変の共有テーブル（MDCT 4 サイズ分・FFT 1 テーブル分）を 1 回だけ構築して参照共有する。
- **期待効果**: エンコーダ/デコーダ生成時のアロケーション削減・L1/L2 キャッシュ共有率向上・常駐メモリ削減。

### [OPT-10] `CELTMode` の窓関数 `float32` 直接保持と `cltMDCTForward` のクロージャ撤去・BCE
- **対象**: [internal/dsp/mdct_celt.go](internal/dsp/mdct_celt.go#L38-L44), [internal/dsp/mdct_celt.go](internal/dsp/mdct_celt.go#L279-L312), [internal/dsp/mdct_celt.go](internal/dsp/mdct_celt.go#L395-L402)
- **問題**: `NewCELTMode` が `[]float32` 窓を `[]float64` にコピーし、毎フレームの `cltMDCTForward` / `CLTMDCTBackwardInto` で全要素を `float32(w[k])` に再変換している。さらに `cltMDCTForward` 内で `x := func(k int) float32` / `win := func(k int) float32` のローカルクロージャを定義してホットループから呼んでいる。
- **改善案**: `CELTMode` に `window32 []float32` を直接保持（または不変の 120 点窓を共有）し、クロージャを排除して直接スライス参照＋事前境界チェック（`_ = in[N2+ov-1]`, `_ = w[ov-1]`）を行う。
- **期待効果**: MDCT 前処理・窓掛けの命令数・型変換削減およびキャッシュ半減。

### [OPT-11] `CELTMode.CLTMDCTBackwardInto` の不要な全フレーム `copy(dst, buf[:N])` 削減
- **対象**: [internal/dsp/mdct_celt.go](internal/dsp/mdct_celt.go#L384-L411)
- **問題**: `m.bwdBuf`（長さ `N+half`）に `imdctRawInto(X, buf[half:])` を書き、先頭 `ov` サンプルを窓掛けし、`buf[N:N+half]` を `carry` へコピーした後、さらに `copy(dst, buf[:N])` で 960 要素（7.68 KB）丸ごとコピーしている。
- **改善案**: `imdctRawStatic` のポストローテーションで直接 `dst` と `carry`（末尾 `half` 分）へ出力し、先頭 `ov` サンプルのみ `dst` 上でインプレース窓掛けする。
- **期待効果**: CELT デコード毎フレーム（チャネル毎）の **7.68 KB の memcpy 削減** と `bwdBuf`（8.16 KB）の不要化。

### [OPT-12] `opusBfly2` の `switch j` アンロールと `bfly3`/`bfly4`/`bfly5` の BCE 最適化
- **対象**: [internal/dsp/fft_opus.go](internal/dsp/fft_opus.go#L190-L308)
- **問題**: `opusBfly2` は `m == 4` 固定なのに `for j := 0; j < 4` 内で `switch j` を実行している。また `bfly3`/`bfly4`/`bfly5` は内側ループで `fout[i0+k*m]` と `p.twiddles[j*fstride*k]` に毎回スライス境界チェックが発行されている。
- **改善案**: `opusBfly2` の 4 バタフライを完全アンロール（分岐ゼロ化）し、`bfly3`/`bfly4`/`bfly5` でグループ毎に `sub := fout[base : base+mm]` や `tw := p.twiddles[:...]` で BCE を効かせる。
- **期待効果**: CELT の順/逆 MDCT コア FFT 処理の高速化。

### [OPT-13] `dsp.BitReverse` / `dsp.Log2` の `math/bits` 組み込み命令化 & `FFTConfig` ビット反転テーブル事前計算
- **対象**: [internal/dsp/math.go](internal/dsp/math.go#L72-L89), [internal/dsp/fft.go](internal/dsp/fft.go#L143-L202)
- **問題**: `BitReverse` がビット単位の `for` ループ（10 反復）で実装されており、`FFTConfig.ExecuteInPlace` が毎回全点分 `BitReverse(i, cfg.bits)` を再計算している。`Log2` もループ実装。
- **改善案**: `bits.Reverse(uint(n)) >> (bits.UintSize - b)` および `bits.Len(uint(n)) - 1` を使用し、`FFTConfig` に `bitrev []int` を事前計算して保持する。
- **期待効果**: 汎用 FFT（帯域検出等）のビット反転ステージを大幅に高速化。

### [OPT-14] `dsp.FFT` の内側ループ内 `math.Cos`/`math.Sin` 計算の排除
- **対象**: [internal/dsp/fft.go](internal/dsp/fft.go#L35-L55)
- **問題**: `dsp.FFT` が最内ループで `math.Cos(angle)` / `math.Sin(angle)` を `O(N log N)` 回呼んでいる（1024 点で **5,120 回 × 2**）。
- **改善案**: 2 の冪サイズに対するキャッシュ済み `FFTConfig`（または外側の `k` ループで回転因子を 1 回計算/漸化式更新）を使うように変更する。
- **期待効果**: `dsp.FFT` / `dsp.RealFFT` の **10 倍以上の高速化**。

### [OPT-15] `dsp.MDCT.Forward` / `inverseFft` の未使用事前計算 `twiddles` 活用 & `inverseDirect` の高速化
- **対象**: [internal/dsp/mdct.go](internal/dsp/mdct.go#L44-L50), [internal/dsp/mdct.go](internal/dsp/mdct.go#L86-L197)
- **問題**: `NewMDCT` で `mdct.twiddles` を事前計算して確保しているにもかかわらず、`Forward` / `inverseFft` がそれを一切参照せず毎呼び出し `math.Cos`/`math.Sin` を再計算している。また `inverseDirect` は $O(N^2)$ で $2N^2$ 回の `math.Cos` を呼んでいる。
- **改善案**: 事前計算済み回転因子テーブルを実際に使用し、`inverseDirect` は `AnyFFT` ベースまたは DCT-IV 変換へ置換する。
- **期待効果**: 汎用 MDCT の大幅高速化および無駄なメモリ確保の有効活用。

---

## 4. `internal/resampler`（汎用ポリフェーズ FIR リサンプラ）— 5 項目

### [OPT-16] `Resampler.Process` / `processChannel` のフレーム毎ヒープアロケーション全廃
- **対象**: [internal/resampler/resampler.go](internal/resampler/resampler.go#L210-L260)
- **問題**: 呼び出し毎に `output`、チャネル毎の `chInput`（モノラル時でも全コピー）、チャネル毎の `chOutput` の計 `1 + 2*numChannels` 回のヒープ確保が発生する。
- **改善案**: `ProcessInto(dst, input []float64) []float64` と再利用スクラッチバッファを導入し、`numChannels == 1` の場合はデインターリーブ/インターリーブを完全スキップするモノラル高速パスを追加する。
- **期待効果**: 非 48kHz 入出力時のリサンプリングにおけるアロケーションを **0 allocs/op** に削減。

### [OPT-17] ポリフェーズ係数 `r.coeffs` の転置によるキャッシュ局所性改善
- **対象**: [internal/resampler/resampler.go](internal/resampler/resampler.go#L123-L170), [internal/resampler/resampler.go](internal/resampler/resampler.go#L297-L300)
- **問題**: `r.coeffs` が `[tap][phase]` 順（`j*r.oversample + phaseIdx`）で格納されており、内側 FIR ループが `oversample`（最大 64 × 8B = 512 バイト）のストライドアクセスになりキャッシュミスが多発する。
- **改善案**: `[phase][tap]` 順に転置格納し、内側ループで `phaseCoeffs := r.coeffs[phaseIdx*r.filterLen : (phaseIdx+1)*r.filterLen]` を連続メモリアクセスする。
- **期待効果**: FIR 畳み込みのキャッシュ効率とスループットを大幅に改善。

### [OPT-18] `processChannel` 内側 FIR ループの条件分岐撤去（連続リング/スクラッチ化）
- **対象**: [internal/resampler/resampler.go](internal/resampler/resampler.go#L281-L301)
- **問題**: 毎タップで `tapPos >= 0 && tapPos < inputLen`、`tapPos < 0`、`memIdx >= 0 && memIdx < len(r.mem[ch])`、`coeffIdx < len(r.coeffs)` の 4 段階の条件分岐を実行している。
- **改善案**: スクラッチバッファの先頭に `r.mem[ch]`、続けて `input`、末尾にゼロパディングを配置して連続化し、内側ループを分岐なしの純粋な内積ループ（4x アンロール可能）にする。
- **期待効果**: 分岐予測ミス・境界チェック排除により FIR 処理速度を **2〜3 倍高速化**。

### [OPT-19] サンプル毎の整数除算・剰余（3 回/sample）の排除
- **対象**: [internal/resampler/resampler.go](internal/resampler/resampler.go#L272-L275), [internal/resampler/resampler.go](internal/resampler/resampler.go#L306-L308)
- **問題**: 出力 1 サンプル毎に `phaseIdx` 計算と `intPos += frac / stepDen; frac %= stepDen` で 3 回の整数除算を行っている。Opus の対応レート（8/12/16/24/48kHz）では `stepDen` は高々 6 である。
- **改善案**: `for frac >= stepDen { frac -= stepDen; intPos++ }` とし、`phaseIdx` は長さ `stepDen` の事前計算ルックアップテーブルで O(1) 参照する。
- **期待効果**: 出力サンプル毎の除算命令を完全撤去。

### [OPT-20] `generateCoeffs` での `besselI0(beta)` 重複計算排除と係数テーブルキャッシュ共有
- **対象**: [internal/resampler/resampler.go](internal/resampler/resampler.go#L152), [internal/resampler/resampler.go](internal/resampler/resampler.go#L181), [internal/resampler/resampler.go](internal/resampler/resampler.go#L349)
- **問題**: `kaiserWindow` 内で定数 `besselI0(beta)` を全タップ（最大 5,120 回）再計算している。また同一 `(inRate, outRate, quality)` の複数リサンプラ間で不変な `coeffs`（最大 40 KB）を共有せず、`CopyStateFrom` でも複製している。
- **改善案**: `invI0Beta := 1.0 / besselI0(beta)` をループ外に出し、`(inRate, outRate, quality)` キーで不変係数スライスを共有キャッシュする。
- **期待効果**: リサンプラ初期化高速化およびマルチストリーム時の常駐メモリ削減。

---

## 5. `internal/celt`（CELT エンコーダ・デコーダ）— 12 項目

### [OPT-21] 本番フレームにおける `FrameTrace` の無条件構築・大量コピーのゲート化
- **対象**: [internal/celt/encoder.go](internal/celt/encoder.go#L1060), [internal/celt/encoder.go](internal/celt/encoder.go#L1078-L1106), [internal/celt/encoder.go](internal/celt/encoder.go#L1190-L1196), [internal/celt/encoder.go](internal/celt/encoder.go#L1224-L1225), [internal/celt/encoder.go](internal/celt/encoder.go#L1371-L1381), [internal/celt/encoder.go](internal/celt/encoder.go#L1421-L1426)
- **問題**: テスト・オラクル検証用の `e.lastTrace` が本番エンコードでも毎フレーム無条件に構築されている。特に `traceBandLogE := append([]float64(nil), logE...)`（毎フレームヒープ確保）、`appendTraceRows(prev.In, bufs[:CC])`（ステレオ 17.3 KB コピー）、`appendTraceRows(prev.Freq, traceCoeffs[:ch])`（ステレオ 15.4 KB コピー）、`bandTells` 記録など、**毎フレーム約 35 KB の不要なメモリコピー**が発生している。
- **改善案**: `e.traceEnabled bool` フラグ（デフォルト `false`、テスト用セッタ `SetTraceEnabled(true)` で有効化）で全ての `e.lastTrace` 書き込みと `traceBandLogE` 確保をガードする。
- **期待効果**: CELT / Hybrid エンコードの不要 memcpy 約 35 KB/frame 削減、`-1 alloc/op`、エンコーダ常駐メモリも約 40 KB 削減。

### [OPT-22] CELT デコーダの残り 3 アロケーション排除（`UnquantizeCoarseEnergy` / `celtTFDecode` / `decodeDynalloc`）
- **対象**: [internal/celt/decoder.go](internal/celt/decoder.go#L298-L315), [internal/celt/decoder.go](internal/celt/decoder.go#L1107), [internal/celt/decoder.go](internal/celt/decoder.go#L1170), [internal/celt/quant_energy.go](internal/celt/quant_energy.go#L294-L297)
- **問題**: `BenchmarkPerf/decode/celt/*` の `4 allocs/op`（633〜824 B/op）のうち 3 つが、毎フレーム `make([]float64, numBands*ch)`、`make([]int, numBands)`、`make([]int, numBands)` を確保するこの 3 関数である（残り 1 つは [OPT-01] の `entcode.NewDecoder`）。
- **改善案**: `celt.Decoder` 内に `[2*NumBands48000]float64`、`[NumBands48000]int`、`[NumBands48000]int` の固定長配列（または `*Into` ヘルパ）を持たせる。`quant_energy.go` の `prev := make([]float32, channels)` も `[2]float32` スタック配列にする。
- **期待効果**: [OPT-01] と合わせて CELT デコード内部パスが **0 allocs/op（0 B/op）** に到達。

### [OPT-23] `quantCoarseEnergy` の `enc.Clone()` 2 回 + スライス 2 回のヒープ確保排除
- **対象**: [internal/celt/quant_energy_flp32.go](internal/celt/quant_energy_flp32.go#L158-L173)
- **問題**: 2 パス粗エネルギー量子化で毎回 `encStart := enc.Clone()`、`encIntra := enc.Clone()`、`oldEBandsIntra := make(...)`、`errIntra := make(...)` をヒープ確保している（CELT ステレオ全 allocs の **15.9%**）。
- **改善案**: `celt.Encoder` に 2 つの `entcode.Encoder` スクラッチを保持して既存の `CloneInto` を使用し、`oldEBandsIntra` / `errIntra` は `[2*NumBands48000]float64` スタック配列にする。
- **期待効果**: CELT エンコードで **-6〜8 allocs/op**。

### [OPT-24] `quantAllBands` ステレオ `thetaRdo` ループ内のバンド毎 9 アロケーション排除
- **対象**: [internal/celt/quant_pvq.go](internal/celt/quant_pvq.go#L1299-L1342)
- **問題**: ステレオ複雑度高設定時の `thetaRdo` で、各バンド毎に `enc.Clone()` × 2、`xSave` / `ySave` / `xSave2` / `ySave2` / `normSave2` の `append([]float32(nil), ...)` × 5 の計 **9 回以上のヒープ確保** がバンド数分繰り返される。
- **改善案**: `quantAllBandsScratch` に `encSave`, `encSave2`（`entcode.Encoder`）と `[maxPVQN]float32` 配列 5 本を持たせ、`CloneInto` と `copy` で再利用する。
- **期待効果**: `thetaRdo` 有効時のステレオ CELT エンコードで **数十 allocs/op 削減**。

### [OPT-25] `opPVQSearch` の `math.Floor` 除去と `signBuf` 縮小（CELT CPU 最大ホットスポット 13.0%）
- **対象**: [internal/celt/pvq_encode.go](internal/celt/pvq_encode.go#L94-L197)
- **問題**: `pprof` で CELT ステレオエンコードの **単体 1 位（13.04% flat）** を占める `opPVQSearch` において:
  1. 141 行目 `iy[j] = int(math.Floor(float64(float32(rcp * x[j]))))` は `rcp > 0, x[j] >= 0` が保証されているため非負値の切り捨てであり、`math.Floor` 呼び出し＋`float32→float64` 変換が不要。
  2. 100 行目 `var signBuf [maxPVQN]int`（1,408 B）は 0/1 フラグのみ格納するため `[maxPVQN]uint8`（176 B）で十分（スタックゼロ初期化コスト 1/8）。
  3. 172〜182 行目の内側パルス探索ループは `x[j]`/`y[j]` の比較を 2〜4 要素アンロールまたは境界チェック集約することで ILP（命令レベル並列性）を高められる。
- **改善案**: `int(rcp * x[j])` への置換（ビット完全一致）、`signBuf` の `uint8` 化、内側探索ループの最適化。
- **期待効果**: CELT エンコード全体の **3〜6% 高速化**。

### [OPT-26] `celt.Encoder.encodeRange` の `X` (`[]float64`) → `Xq` (`[]float32`) 二重保持・コピーの解消
- **対象**: [internal/celt/encoder.go](internal/celt/encoder.go#L578), [internal/celt/encoder.go](internal/celt/encoder.go#L865), [internal/celt/encoder.go](internal/celt/encoder.go#L1400-L1408)
- **問題**: `X` は全要素が `float64(float32(...))` で丸められているのに `[]float64`（ステレオ 15.36 KB）で保持され、直後の PVQ 前に全要素を `Xq []float32`（7.68 KB）にコピー変換している。
- **改善案**: 正規化スペクトル `X` を最初から `[]float32` で保持し、参照箇所（`tfAnalysis` / `spreadingDecision32` / `stereoAnalysis` / `allocTrimAnalysis32`）を `[]float32` 入力に統一する。
- **期待効果**: 毎フレームの `float64→float32` 全スペクトル変換コピー撤去、キャッシュ占有量半減。

### [OPT-27] `celt.Encoder.encodeRange` のパディング二重確保排除
- **対象**: [internal/celt/encoder.go](internal/celt/encoder.go#L1502-L1513)
- **問題**: `out := enc.Bytes()` で確保した後、`len(out) < targetBytes` の場合に再度 `padded := make([]byte, targetBytes)` を確保してコピーしている。さらに上位 `opus.go` で `append([]byte{toc}, compressed...)` と 3 回目の確保・コピーが発生する。
- **改善案**: `EncodeInto(samples []float64, dst []byte)` 形式にし、呼び出し元が先頭 1 バイト（TOC 用）を空けたスライス `dst[1:]` を渡して直接書き込む設計にする。
- **期待効果**: パケット出力周りの **-2〜3 allocs/op** およびコピー回数 3→1 削減。

### [OPT-28] `celtWindow` の共有不変テーブル化
- **対象**: [internal/celt/constants.go](internal/celt/constants.go#L180-L210), [internal/celt/encoder.go](internal/celt/encoder.go#L364), [internal/celt/encoder.go](internal/celt/encoder.go#L385)
- **問題**: `NewEncoder` / `NewDecoderEx` が `celtWindow(120)` を呼び出すたびに新しい `[]float32` をヒープ確保してコピーしている（`NewEncoder` では 2 回呼んでいる）。
- **改善案**: 標準の 120 点窓はパッケージレベルの不変スライスをそのまま返す。
- **期待効果**: エンコーダ/デコーダ生成時の無駄なアロケーション削減。

### [OPT-29] `patchTransientDecision` の不要アロケーション確認・排除
- **対象**: [internal/celt/celt_analysis.go](internal/celt/celt_analysis.go#L65), [internal/celt/celt_analysis.go](internal/celt/celt_analysis.go#L172), [internal/celt/celt_analysis.go](internal/celt/celt_analysis.go#L346-L352)
- **問題**: `pprof` で `patchTransientDecision` が CELT ステレオ全 allocs の **4.61%** を占めている。`celt_analysis.go` 内の `tmp`、`spreadOld`、`offsets`/`importance`/`follower`/`noiseFloor` 等が最大サイズ 21〜42 要素の小さな配列にもかかわらずヒープ確保されている。
- **改善案**: すべて固定長スタック配列（`[NumBands48000]float64` 等）または `dynallocScratch` に収める。
- **期待効果**: CELT エンコードで **-1〜2 allocs/op**。

### [OPT-30] `celt.Decoder.applyDeemphasis` と `cwrsiLibopusInto` の高速化（デコード CPU 上位）
- **対象**: [internal/celt/decoder.go](internal/celt/decoder.go#L450-L530), [internal/celt/pvq.go](internal/celt/pvq.go#L440-L517)
- **問題**: デコード `pprof` で `cwrsiLibopusInto`（**8.89% cum**）、`celtPVQU`（**2.96% flat**）、`applyDeemphasis`（**3.70% flat**）が上位を占める。
  1. `cwrsiLibopusInto` の `for { k--; p = celtPVQU(k, n); if p <= i { break } }` が毎回 `celtPVQU` を呼び出し、`n > k` 判定・範囲チェックを行っている。テーブル範囲内（`n < 64 && k < 320`）では行スライス `row := pvqUTable[n]` 等を直接参照できる。
  2. `applyDeemphasis` は `float64` スライスを読み書きしながら内部で毎回 `float32` キャストを繰り返している。
- **改善案**: `cwrsiLibopusInto` のテーブル直接参照パス導入（+ 二分探索または行キャッシュ）と、`applyDeemphasis` の BCE・不要キャスト削減。
- **期待効果**: CELT デコード全体の **5〜10% 高速化**。

### [OPT-31] `celt.SurroundAnalyzer.Analyze` の毎フレームヒープ確保排除
- **対象**: [internal/celt/surround_analysis.go](internal/celt/surround_analysis.go#L91-L99)
- **問題**: サラウンド解析で毎フレーム `bandLogE`（`channels*21`）、`in`（`n+overlap`）、`freq`（`freqSize`）の 3 スライスを新規確保している。
- **改善案**: `SurroundAnalyzer` 構造体に再利用スクラッチバッファを持たせる。
- **期待効果**: サラウンドエンコード毎フレームの **-3 allocs/op（約 20 KB/frame）**。

### [OPT-32] `celt.Decoder.decodeLoss` (PLC) の 14 個の一時スライス確保のスクラッチ化
- **対象**: [internal/celt/plc.go](internal/celt/plc.go#L28-L342)
- **問題**: パケットロス補完（PLC）実行時に `mem`, `lp`, `xx`, `rden`, `yy`, `etmp`, `samples`, `X`, `freq`, `exc`, `firTmp`, `ext`, `frame`, `output` の 14 個以上のスライスを毎回ヒープ確保している。
- **改善案**: スタック配列（小サイズのもの）および遅延初期化 `plcScratch` 構造体に集約する。
- **期待効果**: 連続パケットロス時の GC 負荷とジッタを大幅に低減。

---

## 6. `internal/silk`（SILK エンコーダ・デコーダ）— 11 項目

### [OPT-33] `silkNLSFEncode` & `silkNLSFDelDecQuant` の約 50〜60 allocs/frame 全廃とテーブル事前計算
- **対象**: [internal/silk/nlsf_encode.go](internal/silk/nlsf_encode.go#L112-L238), [internal/silk/nlsf_encode.go](internal/silk/nlsf_encode.go#L328)
- **問題**:
  1. `silkNLSFEncode` が毎フレーム `target`, `errs`, `survivors`, `bestRaw` をヒープ確保し、`sort.SliceStable` でクロージャ＋リフレクション swapper を確保している。
  2. さらに `for _, cb1 := range survivors` ループ内（最大 32 回/frame）で毎回 `resQ10 := make([]int16, order)`、`wAdjQ5 := make([]int16, order)`、および `silkNLSFDelDecQuant` 内で `raw := make([]int, order)` を確保している（**これだけで 1 フレーム数十回のヒープ確保！**）。
  3. `silkNLSFDelDecQuant`（CPU **4.55% flat**）が全生存候補ループの中で毎回同一の `out0Table` / `out1Table` を再計算している（`quantStepSizeQ16` はコードブック定数）。
- **改善案**:
  - すべて `[silkMaxLPCOrder]int16` / `[silkMaxLPCOrder]int` / `[32]int64` / `[32]int` のスタック配列に変更。
  - `sort.SliceStable` を libopus 同等のインプレース挿入ソート（上位 `nSurvivors` 件のみ選択）に置換。
  - `out0Table` / `out1Table` をコードブック（`nlsfCBParams`）初期化時に事前計算して保持。
- **期待効果**: SILK / Hybrid エンコードで **-30〜60 allocs/op** + NLSF 量子化 CPU **20〜30% 高速化**。

### [OPT-34] `silkNoiseShapeQuantizerDelDec` のホットループ最適化（SILK エンコード CPU の 48.5%）
- **対象**: [internal/silk/nsq_del_dec.go](internal/silk/nsq_del_dec.go#L455-L620)
- **問題**: `pprof` で SILK モノラルエンコード CPU の **48.48%（cum）**、Hybrid ステレオの **30.77%** を占める最重要ホットスポットにおいて:
  1. 568〜572 行目: 毎サンプル `sIdx = (sIdx - 1) % silkDecisionDelay` と `lastIdx := (sIdx + decisionDelay) % silkDecisionDelay` を実行。`silkDecisionDelay == 40`（非 2 の冪）のため、毎サンプル 2 回のハードウェア整数除算（`IDIV`）が発行されている。
  2. 459, 480, 612, 616, 620 行目: 毎サンプルでスライス長境界チェックを反復しているが、サブフレーム単位で事前チェックして高速パス（境界チェックなし）とエッジパスに分離可能。
  3. 499〜566 行目: `shapingLPCOrder`（通常 12 または 16）の `sAR2` ノイズシェーピングループを `order == 16` / `order == 12` で固定長配列ポインタ化（`silkLPCPredictQ10` と同様のアンロール）することでループ制御・境界チェックを削減可能。
  4. 600 行目 `copyDelayedDecisionSuffix`: 状態コピーの範囲・構造体レイアウトを最適化してコピー量を最小化。
- **改善案**: `% 40` を条件減算/加算に置換、サブフレーム単位 BCE、`shapingLPCOrder == 16` 高速パス導入。
- **期待効果**: SILK エンコード全体の **10〜20% 高速化**。

### [OPT-35] `stereoEncState.lrToMS` の毎フレーム 7 スライス確保の完全排除（Hybrid ステレオ確保量の 16%）
- **対象**: [internal/silk/stereo_pred.go](internal/silk/stereo_pred.go#L47-L76), [internal/silk/stereo_pred.go](internal/silk/stereo_pred.go#L191)
- **問題**: `pprof`（`alloc_space`）で Hybrid ステレオエンコードの **単体ワースト 1 位（15.96% = 6 MB）**。毎フレーム `mid`, `side`, `lpMid`, `hpMid`, `lpSide`, `hpSide`, `out` の 7 スライスを確保している。
- **改善案**: `stereoEncState` 内に `[silkMaxFrameLength+2]int16` 等の固定長配列バッファを保持して再利用する。
- **期待効果**: ステレオ SILK / Hybrid エンコードで **-7 allocs/op（約 -5 KB/op）**。

### [OPT-36] `selectLTPGainsVQWithGain` / `silkQuantLTPGains` / `silkFindLTPFLP` の大量アロケーション排除
- **対象**: [internal/silk/ltp_quant.go](internal/silk/ltp_quant.go#L89-L90), [internal/silk/ltp_quant.go](internal/silk/ltp_quant.go#L215-L232), [internal/silk/ltp_quant.go](internal/silk/ltp_quant.go#L306-L375), [internal/silk/encoder.go](internal/silk/encoder.go#L1301)
- **問題**: `pprof`（`alloc_objects`）で SILK モノラルエンコードの **単体ワースト 1 位（22.36% cum）**。`nbSubfr <= 4` なのに `XX`, `xX`, `xxQ17`, `xXQ17`, `gainIndices`, `tempIdx`, `coeffs`, `buf`, `res` 等をすべてヒープ確保している。
- **改善案**: `[silkMaxNBSubframes][25]float64`、`[silkMaxNBSubframes][5]float64`、`[silkMaxNBSubframes]int` 等のスタック配列とエンコーダスクラッチに置換する。
- **期待効果**: 有声 SILK フレーム毎に **-10〜15 allocs/op**。

### [OPT-37] `silkGainsQuant` / `exactProcessGains` の毎フレーム 9 スライス確保と無条件トレースコピー排除
- **対象**: [internal/silk/process_gains_flp32.go](internal/silk/process_gains_flp32.go#L23-L24), [internal/silk/process_gains_flp32.go](internal/silk/process_gains_flp32.go#L74-L165), [internal/silk/gain.go](internal/silk/gain.go#L30-L152)
- **問題**: `pprof` で SILK モノラル全 allocs の **11.63%**。`ind`, `absInd`, `res`, `gains`, `gainsQ16`, `gainsUnqQ16` 等を毎フレーム確保しており、さらに 161・165 行目でトレース用 `e.traceLPCInPre` / `e.traceInvGains` を無条件確保している。
- **改善案**: `[silkMaxNBSubframes]` 固定長配列化し、`traceLPCInPre` / `traceInvGains` は `frameTracesEnabled()` ガード下でのみ記録する。
- **期待効果**: SILK エンコードで **-8〜10 allocs/op**。

### [OPT-38] `silkBurgModifiedFLP` / `find_lpc.go` / `nlsfToLPCLibopus` / `lpc_in_pre.go` / `residual_energy_flp.go` の一時スライス確保排除
- **対象**: [internal/silk/burg.go](internal/silk/burg.go#L20-L353), [internal/silk/find_lpc.go](internal/silk/find_lpc.go#L28-L124), [internal/silk/lpc_in_pre.go](internal/silk/lpc_in_pre.go#L66-L195), [internal/silk/residual_energy_flp.go](internal/silk/residual_energy_flp.go#L37-L64)
- **問題**: LPC 次数は最大 `silkMaxLPCOrder == 16`、サブフレーム数は最大 `4` という極小固定上限があるにもかかわらず、これらの関数群が毎フレーム合計 **20 回以上** `make([]float64, order)` / `make([]int32, order)` / `make([]int16, order)` をヒープ確保している。
- **改善案**: すべて `[silkMaxLPCOrder]` / `[silkMaxNBSubframes]` スタック配列または `Encoder` スクラッチバッファに変更する。
- **期待効果**: SILK / Hybrid エンコードで **-20〜25 allocs/op**。

### [OPT-39] `silk.Encoder` ステレオ変換・フロントエンド遅延線・状態スナップショット・レート制御ループのアロケーション排除
- **対象**: [internal/silk/encoder.go](internal/silk/encoder.go#L672-L679), [internal/silk/encoder.go](internal/silk/encoder.go#L850-L858), [internal/silk/encoder.go](internal/silk/encoder.go#L1049), [internal/silk/encoder.go](internal/silk/encoder.go#L1094), [internal/silk/encoder.go](internal/silk/encoder.go#L1145-L1146), [internal/silk/front_end.go](internal/silk/front_end.go#L94), [internal/silk/encode_frame_loop.go](internal/silk/encode_frame_loop.go#L99-L139)
- **問題**:
  1. `encodeMultiStereoWithEncoder`（Hybrid ステレオ `alloc_space` **12.0% + `int16FrameToFloat` 6.7% + `floatFrameToInt16` 2.7%**）が毎フレーム `left`/`right`/`l`/`r` と `int16↔float64` 変換スライスを確保している。
  2. `front_end.go:94` が `joined := append(append([]float64(nil), e.encInputDelay...), q...)` で毎フレーム 2 回確保している。
  3. `encoder.go:1049` が `e.prevNLSFQ15 = append([]int16(nil), ...)`（`copy` で十分）、`1094` 行目が `e.lastXBuf` を毎フレーム無条件確保、`1145-1146` 行目が `captureFrameState` で `lpcState`/`ltpState` を毎回新規確保。
  4. `encode_frame_loop.go:99-100, 137, 139` がレート制御ループおよび再試行イテレーション毎にスライスを確保。
- **改善案**: すべてエンコーダ内の再利用スクラッチ・固定長配列・`copy` へ置き換える。
- **期待効果**: ステレオ SILK / Hybrid エンコードで **-15〜25 allocs/op（約 -12 KB/op）**。

### [OPT-40] `xcorrKernelFLP` の `float32` レジスタ化とクロージャ・ポインタ参照排除
- **対象**: [internal/silk/pitch_core_flp32.go](internal/silk/pitch_core_flp32.go#L55-L130)
- **問題**: 4 ラグ同時相関コア `xcorrKernelFLP` が `[]float64` を読み込みつつ、ローカルクロージャ `mac := func(acc, a, b float64) float64 { return f32(acc + f32(a*b)) }` 経由で 1 MAC あたり 4 回の `float64↔float32` 変換を行い、さらにポインタ `sum[0..3]` を毎回読み書きしている。
- **改善案**: ローカル変数 `var s0, s1, s2, s3 float32` でレジスタ保持し、クロージャを排除して直接 `s0 += tmp * y0`（`float32` 同士）を計算し、最後に `sum[0..3]` へ書き戻す。
- **期待効果**: SILK ピッチ解析相関コアの大幅高速化（ビット完全一致）。

### [OPT-41] `silkWarpedAutocorrelationFLP32` & `silkLPCAnalysisFilterFLP32` の最適化（Hybrid CPU の 15.4%）
- **対象**: [internal/silk/noise_shape_flp32.go](internal/silk/noise_shape_flp32.go#L52-L73)
- **問題**: `pprof` で Hybrid ステレオエンコードの **9.23%（`silkWarpedAutocorrelationFLP32`）** + **6.15%（`silkLPCAnalysisFilterFLP32`）** を占める。`silkWarpedAutocorrelationFLP32` は内側ループで毎反復 `state[i:i+3:i+3]`・`c[i:i+2:i+2]` とスライスヘッダ生成＋`state[0]` の再ロードを行っている。
- **改善案**: `s0 := tmp1`（更新後の `state[0]`）をローカル変数に保持し、`order == 16`（最も一般的）の固定長アンロール版を追加する。
- **期待効果**: ノイズシェーピング解析の高速化。

### [OPT-42] `silk.Decoder.synthesize` の内側ループ分岐ホイストと BCE（デコード CPU の 15.6%）
- **対象**: [internal/silk/decoder.go](internal/silk/decoder.go#L2626-L2659)
- **問題**: デコード `pprof` で最大ホットスポット（**15.56% cum**）の `synthesize` において:
  1. `if voiced` がサンプル毎の内側ループ内にある（サブフレーム単位で不変）。
  2. 有声時に `if ltpBase >= 4 && ltpBase < len(sLTPQ15)` も毎サンプル判定している。
- **改善案**: `if voiced` をサブフレームループ外（`for i := range presQ14` の外側）にホイストし、LTP 範囲が全サブフレームで収まる場合の高速ループを分離する。
- **期待効果**: SILK デコードの **5〜10% 高速化**。

### [OPT-43] `silk.Resampler.iirFIRInterpol` の BCE 強化（デコード CPU の 4.4%）
- **対象**: [internal/silk/resampler.go](internal/silk/resampler.go#L395-L417)
- **問題**: デコード `pprof` で 4.44% を占める `iirFIRInterpol` において、毎サンプル `bp := buf[indexQ16>>16:]; bp = bp[:8]` でスライス再構築と境界チェックが行われ、`silkResamplerFracFIR12` も 2 回インデクシングしている。
- **改善案**: `idx := int(indexQ16 >> 16)` に対して事前境界チェックまたは `(*[8]int16)(buf[idx : idx+8])` キャストを活用し、係数ルックアップとループを最適化する。
- **期待効果**: SILK 48kHz リサンプリング出力の高速化。

---

## 7. ルート `opus` パッケージ（API・帯域検出・マルチストリーム・サラウンド・プロジェクション）— 7 項目

### [OPT-44] `bandwidth_detect.go` の 53 KB/frame 確保 & 11,200 回三角関数呼び出しの抜本改善
- **対象**: [bandwidth_detect.go](bandwidth_detect.go#L126-L170)
- **問題**: `BenchmarkPerf`（レガシー/自動帯域検出パス）で 1 フレームあたり **52.7 KB のヒープ確保** と **約 11,200 回の `math.Sin`/`math.Cos` 呼び出し**（Hann 窓 960 回 + `dsp.FFT` 5,120×2 回）を実行しており、CELT エンコード時間を 2 倍（+110 µs）に悪化させている。
- **改善案**:
  1. `Encoder` 内に再利用スクラッチ（`bwFFTWork []dsp.Complex`）を保持する。
  2. サイズ毎（120/240/480/960 等）の Hann 窓テーブルと `dsp.FFTConfig`（回転因子事前計算済み）をパッケージレベルでキャッシュ共有する。
  3. モノラルダウンミックス＋Hann 窓掛けを直接 `bwFFTWork`（`Complex{Real: ..., Imag: 0}`）へ 1 パスで書き込み、`cfg.ExecuteInPlace(bwFFTWork)` を実行し、`power` スライスを作らずにそのまま `emax` / `sum` / `sumSquares` / `topBin` を計算する。
- **期待効果**: 帯域検出有効時のエンコードで **-52.7 KB/op（-4〜5 allocs/op）**、**約 -100 µs/op（約 2 倍高速化）**。

### [OPT-45] `Encoder.EncodeFloat32` の `inputScratch` 未使用バグ修正
- **対象**: [opus.go](opus.go#L595)
- **問題**: `Encode`（525 行目）と `Encode24`（556 行目）は `e.inputScratch(expectedSize)` を使って 0 allocs にしているのに、`EncodeFloat32` だけが `floatPCM := make([]float64, expectedSize)` を直接呼んでおり、毎呼び出し **7.68 KB（モノラル）〜 15.36 KB（ステレオ）** を無駄にヒープ確保している。
- **改善案**: `floatPCM := e.inputScratch(expectedSize)` に 1 行修正する。
- **期待効果**: `EncodeFloat32` 利用時に **-1 alloc/op（-7.68〜15.36 KB/op）**。

### [OPT-46] `Decoder.decodeSoftClipped32` / `finishOutput` と `softClipIsIdentity` の 2 パス統合
- **対象**: [opus.go](opus.go#L4086-L4122), [softclip.go](softclip.go#L25-L29), [softclip.go](softclip.go#L131-L144)
- **問題**: デコード `pprof` で `decodeSoftClipped32`（2.96% flat）+ `softClipIsIdentity`（2.96% flat）+ `libopusFloat32ToInt16`（2.22% flat）が合計 **8.1%** を占める。`decodeSoftClipped32` で `pcm` 全要素をループして `f[i] = float32(v)` に変換した直後に、`SoftClipFloat32` が `softClipIsIdentity` で再び `f` 全要素を走査し、さらにその直後に呼び出し元が `f` 全要素を `int16` 変換する **3 パス走査** になっている。
- **改善案**: `float64→float32` 変換ループ内で同時に `needsClip := v < -1 || v > 1 || v != v` を判定し、`!needsClip && mem == 0` の一般的なケースでは `SoftClipFloat32` 呼び出しを完全スキップ（さらに `Decode` 向けにはそのまま 1 パスで `int16` 変換）する。
- **期待効果**: `Decode`（int16 API）のメモリ帯域消費削減と **4〜6% 高速化**。

### [OPT-47] `opus.go` の `strictSpeechLikeFrame` / `packOpusFramesCode3Run` / TOC 結合のヒープ確保排除
- **対象**: [opus.go](opus.go#L1093), [opus.go](opus.go#L1130), [opus.go](opus.go#L1155), [opus.go](opus.go#L3779), [encoder_mode_policy.go](encoder_mode_policy.go)
- **問題**: `pprof` で `strictSpeechLikeFrame`（Hybrid ステレオ `alloc_space` **5.40%**）や `packOpusFramesCode3Run`（SILK モノラル `alloc_objects` **5.81%**）、および各所の `append([]byte{toc}, compressed...)` が毎フレーム確保を発生させている。
- **改善案**: `packOpusFramesCode3Run` はスタック配列 `[8]byte` を使い、`strictSpeechLikeFrame` はエンコーダスクラッチまたはインプレース計算に変更、TOC バイト付与はエンコーダ出力バッファ先頭 1 バイト予約で 0 コピー化する。
- **期待効果**: 全モードのエンコードで **-2〜3 allocs/op**。

### [OPT-48] `MultistreamEncoder` / `MultistreamDecoder` のストリーム毎バッファ確保 & 線形チャネル探索排除
- **対象**: [multistream_libopus.go](multistream_libopus.go#L208-L232), [multistream.go](multistream.go#L447-L481), [multistream.go](multistream.go#L518-L521), [multistream.go](multistream.go#L896)
- **問題**:
  1. `MultistreamEncoder.Encode` / `Encode24` / `EncodeFloat32` が毎回 `floatPCM := make([]float64, required)` を確保し、さらにストリームループ内で毎回 `streamPCM`・`streamAnalysis` を確保している。
  2. `findMappedChannel(e.mapping, byte(mapped))` を毎フレーム・毎ストリーム・毎チャネルで O(channels) 線形探索している（`e.mapping` は構築後不変）。
- **改善案**: `MultistreamEncoder` に再利用スクラッチ（`floatPCM`, `streamPCM`, `streamAnalysis`）と構築時事前計算の逆マッピングテーブル `codedToInput []int` を保持する。
- **期待効果**: 5.1ch / 7.1ch / Ambisonics マルチストリームエンコードで **数十 allocs/op 削減**。

### [OPT-49] `SurroundEncoder.analyzeSurroundFrame` の毎フレーム `analyzer.Clone()` 撤去
- **対象**: [surround.go](surround.go#L241-L265)
- **問題**: 毎フレーム `next := e.analyzer.Clone()` を呼び出し、`SurroundAnalyzer` 全体（全チャネル分の `overlap` スライス等）をディープコピーしてから `Analyze` を実行している。またストリーム毎に `streamMask := make([]float64, ...)` を確保し、`findMappedChannel` を線形探索している。
- **改善案**: `Analyze` の引数バリデーションを先に済ませて `e.analyzer` をインプレース更新し、`streamMask` はスタック配列 `[2*celt.NumBands48000]float64` を使用する。
- **期待効果**: サラウンドエンコード毎フレームの大量ディープコピー＆ヒープ確保を完全排除。

### [OPT-50] `MappingMatrix` / `ProjectionDecoder` の行列乗算最適化と `float32` 事前スケーリング
- **対象**: [mapping_matrix.go](mapping_matrix.go#L93-L128), [projection.go](projection.go#L635-L690)
- **問題**:
  1. `ProjectionDecoder.demixFloat` が毎フレーム `acc []float32` と `out []float64` の両方を確保し、最内ループ内で毎回 `float32(1.0/32768) * float32(d.demixing.data[...])` を再計算している。
  2. `MappingMatrix.multiplyFloat32` が列優先配列 `m.data[col*m.rows+row]` に対し `row` 外側・`col` 内側のストライドアクセスを行っている。
- **改善案**: `MappingMatrix` 構築時に `float32` スケール済み（`1/32768` 掛け済み）行優先配列をキャッシュし、スクラッチバッファを再利用する。
- **期待効果**: Ambisonics / Projection エンコード・デコードのメモリ確保半減および行列積の大幅高速化。

---

## 8. `oggopus`（Ogg コンテナ層）— 2 項目

### [OPT-51] Ogg CRC-32 の 1 ビット計算からテーブルルックアップ（256 エントリ / Slicing-by-8）への置換
- **対象**: [oggopus/page.go](oggopus/page.go#L208-L239)
- **問題**: `checksum` および `checksumWithZeroedField` が 1 バイトあたり 8 回のビットループで CRC-32 を計算しており、さらに `checksumWithZeroedField` は全バイトに対して `if i >= 22 && i < 26` を判定している。
- **改善案**: 標準の 256 エントリ Ogg CRC-32 テーブル（`[256]uint32`, 1 KB、多項式 `0x04c11db7` MSB-first）または Slicing-by-4/8 テーブルを導入し、`checksumWithZeroedField` は `page[:22]` → `[4]byte{}` → `page[26:]` の 3 区間更新に分割する。
- **期待効果**: Ogg ページの読み書き CRC 計算が **8〜20 倍高速化**。

### [OPT-52] `oggopus.ReadPage` の 6 回確保・3 重コピーの解消
- **対象**: [oggopus/page.go](oggopus/page.go#L168-L199)
- **問題**: `ReadPage` が `header`、`rest`、`body` を別々に確保して読み込んだ後、`encoded` を新規確保して 3 つをコピー結合し、さらに `ParsePage(encoded)` 内で `Segments` と `Data` を再確保してコピーしている（計 6 allocs, 3 重コピー）。
- **改善案**: `var header [27]byte` をスタックに置き、`segments := make([]byte, segmentCount)` と `data := make([]byte, bodyLen)` へ直接 `io.ReadFull` し、それらをそのまま `Page` に格納しつつ分割 CRC 検証を行う。
- **期待効果**: Ogg ページ読み込みあたり **-4 allocs/page**、ペイロードコピー量 **66% 削減**。

---

## 9. アーキテクチャ / API レベルの追加提案（中長期）

上記の 52 項目（主に内部実装改善・後方互換性 100% 維持）に加え、さらなる極限性能を狙う場合の中長期施策：

1. **ゼロアロケーション `*Into` パブリック API の追加**:
   - `Encoder.EncodeInto(pcm []int16, frameSize int, dst []byte) (int, error)` / `EncodeFloat32Into(...)`
   - `Decoder.DecodeInto(data []byte, dst []int16) (int, error)` / `DecodeFloat32Into(...)`
   - 現在のパブリック API は仕様上呼び出し元所有の戻り値スライスを毎回新規確保せざるを得ない（例: `opus.go:4060`）。`*Into` 版を追加することで、ストリーミング用途（VoIP / サーバーサイドトランスコーディング）で **エンドツーエンド完全 0 allocs/op** を達成できる。
2. **Pure-Go 境界を維持した `amd64`/`arm64` アセンブリ（SIMD）カーネルの段階導入**:
   - 本リポジトリは CGO フリー（Pure Go）だが、Go 標準アセンブリ（`.s` ファイル、`//go:build amd64` / `arm64` + 汎用 Go フォールバック）であれば CGO なしで AVX2 / SSE4.1 / NEON を活用可能。
   - 最優先ターゲット（pprof 実測上位）:
     - `celt.pitchXcorr32` / `silk.xcorrKernelFLP`（SSE/AVX2 4-lag 並列積和）
     - `celt.opPVQSearch` 内側探索ループ
     - `silk.silkLPCPredictQ10` / `silk.silkLPCAnalysisFilter` / `silk.silkNoiseShapeQuantizerDelDec` の AR2 フィルタ
     - `dsp.opusFFTPlan` の `bfly4` / `bfly3` / `bfly5`

---

## 10. 推奨実装ロードマップ（フェーズ別優先度）

| フェーズ | 対象項目 | 期待インパクト | リスク |
|---|---|---|---|
| **Phase 1: クイックウィン（アロケーション激減）** | OPT-01, OPT-02, OPT-21, OPT-22, OPT-23, OPT-44, OPT-45, OPT-51, OPT-52 | CELT デコード **0 allocs**、SILK デコード **0 allocs**、帯域検出 **-53 KB / 2x 高速化**、`EncodeFloat32` **-15 KB**、Ogg CRC **10x 高速化** | 極低（ビット完全一致） |
| **Phase 2: SILK エンコード アロケーション全廃** | OPT-33, OPT-35, OPT-36, OPT-37, OPT-38, OPT-39 | SILK / Hybrid エンコードの **105〜188 allocs/op → < 10 allocs/op**（95% 削減）、GC 停止時間大幅縮小 | 低（ビット完全一致） |
| **Phase 3: ホットループ CPU 最適化** | OPT-03, OPT-04, OPT-05, OPT-07〜OPT-14, OPT-24, OPT-25, OPT-30, OPT-34, OPT-40〜OPT-43, OPT-46 | エンコード・デコード全般で **15〜35% の純粋 CPU 高速化** | 低〜中（`go test -tags opusref` で全検証） |
| **Phase 4: マルチストリーム・リサンプラ・API** | OPT-16〜OPT-20, OPT-26, OPT-27, OPT-31, OPT-32, OPT-48〜OPT-50 + `*Into` API | マルチストリーム / サラウンド / Ambisonics / 非 48kHz リサンプリングの大幅効率化＋エンドツーエンド 0 allocs | 低 |
