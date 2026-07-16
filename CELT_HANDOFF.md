# CELT デコーダー bit-exact 化 — 引き継ぎメモ (2026-05-30 更新)

> **Historical handoff.** The unresolved state below was superseded: the
> decoder now passes all 12 official RFC 8251 vectors. Use
> `docs/CURRENT_IMPLEMENTATION.md` for current status; keep this file only for
> its diagnostic history.

---

## ★ UPDATE 2026-05-30 セッション3 — resynth RMSE 調査（未解決）

### 状況
- **TestRangeVectors tv01〜tv12 全パス** は前回セッションで達成済み。ビットストリーム復号は完全一致。
- **TestOfficialVectors 全12本が RMSE 閾値超**：

| ベクター | RMSE | 主因候補 |
|---------|------|---------|
| tv02-06, tv12 | ≈0.02 | SILK 未実装（silence 出力）|
| tv07 | 0.441 | 純 CELT FB 20ms stereo |
| tv08-09 | 0.38-0.40 | 混在 CELT |
| tv10 | 0.54 | 混在 |
| tv11 | 0.72 | 混在（SILK 多め？）|
| tv01 | 0.69 | 混在 |

### 今回の主な調査内容

#### 試みた修正（効果なし or 逆効果）
- **transient IMDCT 係数デインターリーブ修正**を試みた
  - 仮説: quantBand(B=M) → interleaveHadamard 後は `coeffs[j*M+k]` が sub-frame k の bin j
  - `coeffs[k*nBase:(k+1)*nBase]` → `coeffs[j*M+k]` に変更
  - 結果: **RMSE が 0.441 → 0.460 に悪化**。よって元のコード（連続ブロック）の方がまだ近い。
  - **暫定結論**: 係数インターリーブが想定と異なるか、別の箇所がより大きな影響を持つ。

#### libopus MDCT の重要な発見
`$env:TEMP\opussrc\opus-1.5.2\celt\celt_decoder.c` の実ソースを読んで判明：

```c
// celt_synthesis 内 (transient 時)
if (isTransient) {
    B = M;
    NB = mode->shortMdctSize;  // = 120
    shift = mode->maxLM;       // = 3 (NOT maxLM-LM=0!)
} else {
    B = 1;
    NB = mode->shortMdctSize<<LM;  // = 960
    shift = mode->maxLM-LM;         // = 0
}
// transient sub-frame b:
clt_mdct_backward(&mode->mdct, &freq[b], out_syn[c]+NB*b,
                  mode->window, overlap, shift, B, arch);
// 入力: &freq[b] (b=0..7, ストライド8でアクセス)
// N = l->n >> shift = 960 >> 3 = 120 (shortMdctSize)
```

- **transient**: shift=3 → N=120, stride=M=8, 入力 `&freq[b]`。stride アクセスで freq[b], freq[b+8], ..., freq[b+8*59] を読む（60値=N/2）。
- **non-transient**: shift=0 → N=960, stride=1, 入力 `freq`。freq[0..479] を読む（480値=N/2）。

#### **libopus MDCT の根本的な違い**
libopus の clt_mdct_backward は **N/2 入力**から **N 出力**を生成する（テスト test_unit_mdct.c より確認済み）：
- 非 transient (N=960): freq[0..479] (480値) → 960 time samples
- transient sub-frame b (N=120): freq[b+j*8] for j=0..59 (60値) → 120 time samples

一方、**我々の IMDCT は N=960 全入力**を使う：
- `DenormalizeBands` が coeffs[0..799] を埋める（bands 0-20）
- coeffs[800..959] は zero-pad
- `IMDCT(coeffs[0..959])` → libopus が無視する coeffs[480..799]（bands 19-20 の上位）も含めてしまう

この「upper half bands（NBase bins 60-99 相当）の不正使用」が RMSE 高騰の一因の可能性がある。

#### stereoMerge の確認（正しいと確認）
libopus bands.c の `stereo_merge` float ビルド:
```c
dual_inner_prod(Y, X, Y, N, &xp, &side, arch);
xp = MULT16_32_Q15(mid, xp);      // mid * dot(X,Y)
mid2 = SHR16(mid, 1);              // = mid/2 (float ビルドでは SHR16 は NO-OP なので mid/2)
// Wait: float ビルドでは SHR16(a,n) = a（シフトはダミー）
```

**重要**: float ビルドでは `SHR16(mid, 1) = mid`（no-op）。よって `mid2 = mid`（÷2 しない）。

我々の Go コード: `mid2 := 0.5 * mid` — **これは間違い可能性がある**。
- libopus float ビルドの `SHR16(a, shift)` は `arch.h` で `#define SHR16(a,shift) (a)` → mid2 = mid
- 我々は mid/2 → **stereoMerge の El, Er 計算が libopus と違う**

→ **要修正**: `mid2 := mid`（÷2 なし）に変更して RMSE を確認。

#### libopus decode_mem 構造（OLA の仕組み）
- out_syn は `decode_mem + DECODE_BUFFER_SIZE - N` を指す（**スタックではなく永続バッファ**）
- OPUS_MOVE(decode_mem, decode_mem+N, ...) でフレーム毎にシフト
- clt_mdct_backward の TDAC は out[0..59] を前フレームからの carry-over として使う
- **transient の各 sub-frame は独立した OLA 状態を持つ**（前 sub-frame でなく前フレームの同 sub-frame から引き継ぐ）
- 我々のコード: subTail をフレーム内でチェーン → 正確には sub-frame k ごとに独立した overlap を保持すべき（M 個の overlap バッファが必要）

### 次セッションの試みリスト（優先順）

1. **stereoMerge の `mid2` 修正**: `mid2 := 0.5 * mid` → `mid2 := mid` (libopus float SHR16はno-op)
2. **IMDCT に渡す coeffs の上位半分をゼロクリア**: 非 transient なら coeffs[N/2..N-1]=0、transient なら sub-frame 入力の j>=60 部分=0 にして RMSE 変化を確認
3. **transient sub-frame 独立 OLA**: M 個の overlap バッファを `Decoder` に追加
4. **band 19-20 の non-transient での扱い**: libopus は freq[480..959]=0 を前提。我々は bands 19-20 を DenormalizeBands で書くが、IMDCT に渡す前にゼロにすべき？

### 確認済みの正しい実装（手を付けない）
- range coder / bitstream 復号 → TestRangeVectors 全パス ✓
- cwrsiLibopus, PVQ, expRotation → range 一致から正確と判断
- computeAllocation (rate_alloc.go) → 前セッションで修正済み ✓
- stereoMerge の El/Er 符号 → 要再確認（mid2 の問題）

---

# CELT デコーダー bit-exact 化 — 引き継ぎメモ (2026-05-29)

次セッションが調査をやり直さずに済むよう、現状・手法・次の一手を詳細に残す。
**まずこのファイルと `IMPLEMENTATION_STATUS.md`、メモリ `MEMORY.md` を読むこと。**

---

## ★ UPDATE 2026-05-29 セッション2 — stereo allocation 完全修正

### 決定的な気づき
**CELT final range はパケット単位で独立**。各パケットは range coder を init し直し、coarse energy の
Laplace 復号は固定モデルなので `prevEnergies` は range に効かない。→ §0/§4 の「複数フレーム状態継続が
range ズレの原因」は **誤り**だった。range ズレ=そのパケットが踏む未検証経路のバグ。検証は
新規デコーダ(config,ch 一致)で1パケットずつで十分(`TestPktCategorize` / 改修後の `TestRangeVectors`)。

### `internal/celt/rate_alloc.go` `computeAllocation` で直したバグ(libopus interp_bits2pulses と照合)
1. **`log2FracTable` が index6 以降ほぼ全滅(決め手)**。正:
   `0,8,13,16,19,21,23,24,26,27,28,29,30,31,32,32,33,34,34,35,36,36,37,37`。
   `intensity_rsv=LOG2_FRAC_TABLE[end-start]` を狂わせ stereo の `total` 予算が +2 ずれ → pulses 破綻。
2. **`skipStart` を `start`(=0) の代わりに誤用**。skip ループ percoeff/rem/band_width、intensity
   ベース、`intensity<=start`、phase7/8 ループ範囲は全部 `start`。`skipStart` は break `j<=skip_start` 専用。
3. **bust チェックの stereo 誤り**: 正 `if(C*eb>bits[j]>>3) eb=bits[j]>>stereo>>3;`(閾値に stereo 無し、Cで割らない)。
4. extra_fine 再バランス + `balance=excess` を N>1/N==1 両分岐の外へ移動。

### 結果
- **config-31 (FB 20ms) transient mono 36/36 + stereo 36/36 完全一致**(tv07)。`TestOracleTraceStereo` PASS。
- `TestRangeVectors` を全 CELT パケット検証(config,ch ごとに decoder 生成)へ改修。

### 残課題(優先順)
1. **cfg=17 (NB, LM=1)** が mono/stereo とも失敗 = 別 LM 経路のバグ。
2. **tv11 pkt5**(dual_stereo=1, 非transient, codedBands=20=band skip)で **alloc_trim Go=8 vs oracle=7**。
   dynalloc 直後は完全一致(tellf=830 rng=02914078)なのに次の alloc_trim 復号で割れる。要調査。
3. **tv10 silence/極小パケット** `got=80000000 want=01000000`。

### 道具
- 診断テスト: `internal/celt/diag_stereo_test.go`(env `TV`/`PKTIDX` で対象切替, `ALLOCDBG` で alloc 内部dump),
  `diag_pktcat_test.go`(config-31 を mono/stereo 別に集計)。
- オラクル `rate.c` に `RATEDBG` env で bits1/bits2/lo/hi/total dump を追加済み(`$env:TEMP\opussrc\...\celt\rate.c`、
  `#include <stdio.h>,<stdlib.h>` も追加)。`pwsh scripts/oracle/build.ps1` で反映。
  これで「libopus total=25352 vs Go 25354」を発見 = log2FracTable バグの決め手。

---
（以下、セッション1のメモ。state-continuity の記述は上記の通り訂正される）

---

## 0. 30秒で現状把握

- 目標: 純Go Opus を libopus と bit-exact にし、RFC8251 ベクター12本を RMSE<0.001 で通す。
- **達成済み**: CELT の **TV07 pkt0**（transient/FB/mono/20ms、バンド分割+PVQ含む）の
  デコードが libopus と **完全ビット一致**（final range `0x2c33ee00`）。
  ヘッダ→アロケーション→fine energy→PVQ 全バンドの range coder 状態が全ステージ一致。
- **未達**: ①複数フレーム（pkt1以降）の range が後半でズレる ②音声リシンセシス（collapse mask 不一致）。
- 内部テストは全部グリーン。`cmd_diag` の重複 `main` ビルドエラーは**既存**で無関係。

### すぐ叩くコマンド
```powershell
# Set-Location C:\Users\daruks\vsc\Programs\Go\opus
go test github.com/darui3018823/opus/internal/celt -run TestOracleTrace -v   # TV07 pkt0 完全一致を確認 (PASS)
go test github.com/darui3018823/opus/internal/celt -run TestRangeVectors -v  # 複数フレーム進捗 (informational)
go test github.com/darui3018823/opus/internal/celt github.com/darui3018823/opus/internal/entcode
```
注意: PowerShell ツールの cwd がズレることがある。ズレたら `Set-Location C:\Users\daruks\vsc\Programs\Go\opus`。
パッケージは相対 `./...` でなく **モジュールパス** `github.com/darui3018823/opus/internal/celt` で指定すると安定。

---

## 1. 今セッションで入れた変更（ファイル別）

| ファイル                                  | 変更                                                                                                                                                                                                                                                                                                             |
|---------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `internal/entcode/decoder.go`         | **最重要修正**: `nbitsTotal` フィールド追加。`Tell()`/`TellFrac()` を pos/endOffs 由来から `nbitsTotal` 由来に変更。`normalize()` で `+=SymBits`、`DecodeBits()` で `+=nbits`。`ECTell()`（=ec_tell=Tell()+1）と `TellFrac()`（=ec_tell_frac）追加。                                                                                               |
| `internal/celt/decoder.go`            | デコード順を libopus 厳守に: silence(logp15)→postfilter→isTransient→intra→coarse→tf→spread→dynalloc→alloc_trim→alloc→fine→PVQ→anti-collapse。`celtTFDecode` が `tf_res[]` を返す。`decodeDynalloc` を frac-tell + cap[i] bound に修正。`decodeBandCoeffs` を `QuantAllBands` 呼び出しに置換。stereo M/S マージは quant_all_bands 内に移動（旧ブロック削除）。 |
| `internal/celt/postfilter.go`         | `DecodePostFilterParams` を libopus 準拠に全面書き換え（`ec_dec_bit_logp(1)`→octave(uint6)→pitch(ec_dec_bits(4+octave))→qg(ec_dec_bits(3))→tapset icdf、**1回だけ**読む）。                                                                                                                                                       |
| `internal/celt/rate_alloc.go`         | `computeAllocation` の返り値を `(pulses[Q3予算], eBits, balance, intensity, codedBands, dualStereo)` に変更（Kへの変換phase9を削除）。`cap[]` を `(caps[nbEBands*(2*LM+C-1)+j]+64)*C*N>>2` に修正。budget は Q3 をそのまま使用。                                                                                                                 |
| `internal/celt/quant_pvq.go`          | **新規**。quant_all_bands 一式の移植（後述）。                                                                                                                                                                                                                                                                              |
| `internal/celt/diag_oracle_test.go`   | **新規**。TV07 pkt0 をステージ毎にトレースしオラクルと比較（final range をアサート）。`qabDebug`/`qabLog`/`qabDP` で per-band/per-decode_pulses ダンプ。                                                                                                                                                                                          |
| `internal/celt/range_vectors_test.go` | **新規**。各ベクター全パケットの final range を .bit 期待値と比較（informational）。                                                                                                                                                                                                                                                   |
| 削除                                    | `internal/celt/diag_bisect_test.go`（旧手動bisect、オラクルに置換）。                                                                                                                                                                                                                                                        |
| `scripts/oracle/`                     | **新規**。計装済み libopus オラクル一式（後述）。                                                                                                                                                                                                                                                                                |

---

## 2. 決定的だったバグ（再発防止メモ）

1. **entcode の bit カウント凍結**（今回の山場）:
   旧 `Tell`/`TellFrac` は `pos*8 + endOffs*8 - nendBits` で消費ビットを算出。だが
   パケット終端で前方/後方読みポインタが衝突すると `readByte`/`readByteFromEnd` が
   ポインタを進めず 0 を返すため、カウンタが**凍結**して過小カウント。libopus は
   `nbits_total` を normalize(+8/byte) と ec_dec_bits(+nbits) で**無条件加算**して追跡。
   → これが PVQ 後半バンド(TV07 band13/14)の `b` 予算を壊していた。`nbitsTotal` で解決。
2. **libopus `pulses[]` は K ではなく Q3 ビット予算**。K は quant_all_bands 内で
   `b=max(0,min(16383,min(remaining_bits+1, pulses[i]+curr_balance)))`、`curr_balance=balance/min(3,codedBands-i)` で動的に算出 → `bits2pulses`→`get_pulses`。
3. **raw bits (ec_dec_bits) は rng を変えない**（後方バッファから読む）。fine energy・
   anti-collapse・pitch・qg・PVQの低位ビットは raw。だが `nbitsTotal`/`endOffs` には効くので
   tellf に影響 → 間接的に後続 `b` に影響。anti-collapse は **PVQの後** に raw 1bit で読む（旧コードは前で logp 読みしてた＝バグ）。
4. **Tell() の規約**: 当実装の `Tell()` = libopus `ec_tell` − 1（init で 0 を返す）。
   libopus と同じ guard を書くときは **`ECTell()`**（=Tell()+1）を使う。新コードは全部 ECTell()/TellFrac() に統一済み。
5. **デコード順は厳密**。range coder は順序依存。silence と postfilter の位置・読み方が
   旧コードで間違っていた（postfilter は isTransient より前、logp=1）。

---

## 3. 計装済み libopus オラクル（最強の武器）

`.bit` は **最終 range しか**持たない＝どこで最初にズレるか分からない。そこで libopus を
計装して各ステージ/各バンド/各 decode_pulses の `ec_tell`・`ec_tell_frac`・`rng` を吐かせ、
Go 側トレースと diff する。これが今回の全進捗を支えた。

- 計装済みソースとビルドスクリプトは **`scripts/oracle/`** に永続保存済み:
  `oracle.c`（自作main）, `celt_decoder_instr.c`, `bands_instr.c`, `cwrs_instr.c`, `build.ps1`。
- 再ビルド: `pwsh scripts/oracle/build.ps1` → `$env:TEMP\opusoracle\oracle.exe`
  （opus-1.5.2 ソースが無ければ自動 DL。gcc/curl/tar 必要。**-DCUSTOM_MODES は付けない**＝静的モード使用）。
- 実行: `& $env:TEMP\opusoracle\oracle.exe <testvectorNN.bit> <pktIndex>`
  → stderr に `[stage] tell tellf rng` / `QB band.. -> tellf rng` / `DP n=.. k=.. V=.. tellf a->b` / `RESULT finalrng==expected`。
- 重要: libopus 1.5.x と 1.6.x で CELT decode 経路と全テーブルは凍結＝ground truth として等価。
- 詳細はメモリ `[[opus-libopus-instrumented-oracle]]`（`opus_libopus_oracle.md`）。

### Go 側トレースの出し方
`internal/celt/diag_oracle_test.go` の `TestOracleTrace` が pkt0 をステージ毎に出力し、
`qabDebug=true` で QB band / DP のダンプも出す。別パケット/別ベクターを見たいときは
このテストを複製して `testvectorNN`/pktIndex を変える（複数フレームは状態継続が必要、下記）。

---

## 4. 次の一手（タスク #6, #7）

### タスク #6: 複数フレームの range 一致（最優先）
現象: `TestRangeVectors` で各 CELT ベクターの**先頭付近は一致、後半でズレる**（例 TV07 36/72）。
切り分け済みの原因候補:
1. **ハーネス制約**: `range_vectors_test.go` は非CELT/config切替パケットを**デコードせずスキップ**するため、
   フレーム間状態（特に `prevEnergies`）が途切れる。→ まず「状態を切らさず全パケット処理」する検証に変える。
   ただし当 celt.Decoder は単一 config 固定。config 切替は band 数/LM/ch が変わるのでデコーダ再構成が要る。
2. **フレーム間エネルギー予測 `prevEnergies` の更新**が libopus `oldBandE` と一致していない可能性。
   libopus は coarse 後の oldBandE に加え **unquant_energy_finalise**（anti-collapse後の fine priority bits）でも更新し、
   silence/transient で特別処理する。当実装 `decoder.go` は coarse のみ copy していて finalise 未実装。
   → `internal/celt/decoder.go` の `copy(d.prevEnergies2,...)` 周辺と libopus `unquant_energy_finalise`/`celt_decode_with_ec` 末尾を比較。
3. **pkt0 で通らない経路のバグ**: stereo（quant_band_stereo/compute_theta の stereo分岐/intensity/dual_stereo）、
   dynalloc boost>0、band split の深い再帰、N=2 stereo special。TV07 pkt0 は mono/boost=0 だったので未検証。

**やり方**: オラクルで対象パケットを `oracle.exe tvNN.bit <pkt>` 実行して per-stage/per-band ground truth を取り、
Go 側を同条件（=直前フレームまで状態継続デコード）でトレースして最初にズレるステージを特定 → そこだけ直す。
**まず単純な「同一 config が連続するベクター/区間」で `prevEnergies` 更新の一致を取る**のが安い。
候補: 全 config 同一のベクターを探す（TOC を見て config が一定の区間）。

### タスク #7: 音声リシンセシス（range一致後）
final range は一致するが `quant_band` の collapse mask (`xcm`) がオラクルと違う
（例: Go band12 xcm=94 vs libopus 214）。range に効かない resynth 計算がズレている。
疑い: `cwrsiLibopus`（ベクトル復元）, `expRotation`, `haar1`/`deinterleave`/`interleave`, `bit_deinterleave_table` 適用,
`stereoMerge`, fold(seed) 経路。
**やり方**: オラクルに「正規化後 X[] ダンプ」を追加（celt_decoder_instr.c の quant_all_bands 後で X を出す）し、
Go の `decodeBandCoeffs` の X[] と直接比較。denormalize→IMDCT→OLA は別レイヤ（dsp）。
注意: `stereoMerge` の `mid2 = 0.5*mid` は要再確認（float版 SHR16(mid,1) の解釈）。`quantBandN1` の
`lowbandOut[0]=X[0]`（SHR16(X,4) の float 解釈）も要確認。

---

## 5. ハマりどころ / 既知の注意

- `internal/celt/tables_alloc.go`: `BITRES = 8` は誤称で実体は **1<<BITRES**。シフトは全部リテラル `<<3` を使う。
  `CacheIndex50` は `[]int16`（負値=無効あり）、`CacheBits50`/`CacheCaps50` は `[]uint8`。
- `cwrsV(n,k)` は uint64。`decodePulses` で `ft=uint32(v)`（V>2^32 はlibopusでは起きない＝Kが境界内）。
- celt_norm = float64。float ビルドでは Q15 スケーリングは単純乗算に縮退（MULT16_16_Q15→a*b 等）。
- `quant_pvq.go` の `qabDebug` はテスト専用フラグ。本番は false。
- TV10 で `got=80000000 want=01000000` が出るのは 1バイト（payload空）パケットを decodeLoss していて
  lastFinalRange が初期値のまま＝別途 silence/極小パケット処理が要る（range_vectors ハーネスの粗さ）。
- 既存の `cmd_diag` 重複 main ビルドエラーは無関係。`go build`/`go test` はモジュールパス指定で個別に。

---

## 6. メモリ参照
- `MEMORY.md` → `project_opus_status.md`（詳細状況）, `opus_libopus_oracle.md`（オラクル手順）,
  `project_laplace_fix.md`, `feedback_cgo_powershell.md`（CGO は PowerShell で）。
