# Gap Analysis — libopus パリティに向けた残課題

Last updated: 2026-09-15

このドキュメントは `docs/CURRENT_IMPLEMENTATION.md` を読んだうえで、
libopus 1.6.1 と比べて **現在まだ足りていないもの** を優先度ごとに整理したものです。
実装の権威ある状態記述は `CURRENT_IMPLEMENTATION.md` が持ちます。

---

## 現在地のまとめ

| 領域 | 状態 |
|---|---|
| **デコーダ** | 完成。全 12 公式ベクター PASS、libopus 1.6.1 比較 PASS。SILK PLC/CNG/glue は 2026-09-15 に libopus 移植でサンプル一致。CELT PLC は未一致 |
| **CELT エンコーダ** | 動作するが libopus 比 ~5–6 dB のギャップが一部 CELT 音楽セルに残る |
| **SILK エンコーダ** | 構造完成。voiced/speech で libopus 比 2.3–6.6 dB のギャップが残る |
| **SILK/hybrid モード選択** | 保守的な voice-gate のみ。libopus 統合制御ループに未達 |
| **マルチストリーム/サラウンド** | コア API 完成。surround mask consumers の一部未実装 |
| **Ogg Opus コンテナ** | シングル論理ストリームは完成。多重化ストリーム demux なし |

---

## 🔴 最優先：SILK エンコーダ品質（Q1–Q7 残存）

SILK エンコーダの品質ギャップ根本原因はここにあります。

### Q1 残：LPC/NLSF 解析の C 中間値検証

- **状態：** `LPC_in_pre` 以降の encoder-side oracle を実装済み。2026-09-13 に libopus 1.6.1 と 13 fixture の全 checkpoint が exact match
- **実装済み：**
  - `silk_find_LPC_FLP` の主要処理（全フレーム Burg AR + 後半 10ms Burg AR + 第1ハーフ残差基準 + `k=3..0` 補間探索 + `silkLPCAnalysisFilterFLP` 残差判定）
  - `silk_A2NLSF` — 多項式求根（チェビシェフ級数）、ソート、安定化
  - `silk_NLSF2A_FLP` — NLSF Q15 → float AR（`silk/float/wrappers_FLP.c` / `silk/NLSF2A.c` 準拠）
  - `silk_interpolate` — NLSF サブフレーム間補間（`silk/interpolate.c` 準拠）
  - `silk_process_NLSFs` / `silk_NLSF_encode` — Laroia 重み計算、補間時合成重み、RD 量子化探索、predCoefQ12 復元（`silk/process_NLSFs.c`, `silk/NLSF_encode.c` 準拠）
  - legacy 全探索（`guardedFaithfulBurgNLSFAnalysis`, `bestNLSFAnalysis` 等）の撤廃
  - `opusref` oracle で `LPC_in_pre`、Burg LPC/残差、A2NLSF、補間係数、stage-1/residual index、量子化/補間 NLSF、`PredCoef_Q12` を exact 比較
  - 8/12/16 kHz、voiced/unvoiced、reset/steady、2/4 subframe、補間あり/なし、complexity の全 survivor/gate 帯を検証。stereo mid と hybrid low-band の直接 Q1-domain fixture も含む
  - `silk_float` の丸め位置と演算順序、NLSF fixed-width 演算、complexity 0–3/4–10 の補間 gate を C に合わせた
  - active stereo/hybrid production path でも raw signal fallback を使わず `LPC_in_pre` domain を構築
- **未検証・残作業：**
  - full libopus encoder と同じ PCM から、Q1 より前の pitch/LTP/gain/stereo/hybrid state が同じ値に到達するかの end-to-end 中間値比較
  - base `b70e7a0` 比で 8 kHz speech-like-harmonic の matched loudness 差が -1.50 dB（PASS）から -1.59 dB（FAIL）へ変化。libopus residual-rate table 修正を戻すと Q1 residual index が不一致になるため、threshold は変更せず後続 gain/rate-control 課題として残す
  - Q1 oracle の境界、対応表、fixture 実測値は [`SILK_Q1_ORACLE.md`](SILK_Q1_ORACLE.md) を参照
- **libopus 参照：** `silk/float/find_LPC_FLP.c`, `silk/process_NLSFs.c`, `silk/NLSF_encode.c`, `silk/interpolate.c`

### Q2：ピッチ/LTP の完全ポート

- **実装済み：** `silk_find_pitch_lags_FLP` + `silk_pitch_analysis_core_FLP` によるピッチ検索は実装済み（float64 近似）。絶対ラグ/ピッチコンター符号化も実装。
- **2026-09-15 完了：** `silk_find_LTP_FLP`（float32 演算順序・double 累算）と `silk_quant_LTP_gains` / `silk_VQ_WMat_EC`（Q17/Q15/Q8 固定小数点）を忠実移植。`TestSILKQ2LTPOracle`（opusref, 8 fixture）で XX/xX の float32 bit、periodicity/codebook index、`sum_log_gain_Q7`、`pred_gain_dB` が libopus 1.6.1 と完全一致。libopus は最後に評価したコードブックの残差エネルギーから pred gain を報告する癖も含めて一致
- **2026-09-15 完了（pitch）：** `silk_find_pitch_lags_FLP` + `silk_pitch_analysis_core_FLP` を float32 忠実移植（`pitch_core_flp32.go`）。`TestSILKQ2PitchOracle`（opusref, 10 fixture）で autocorr/Schur/LPC/残差/閾値/LTPCorr/lag/contour/pitchL が完全一致。packet digest・scoreboard は不変
- **2026-09-15 完了（quant offset）：** unvoiced の quantOffsetType 判定を `res_pitch` の 2 ms セグメント energy variation（silk_float）で行うよう libopus に合わせた
- **未検証・残作業：**
  - (2026-09-15 解消) encoder は `x_buf = ltp_mem | frame | LA_SHAPE_MS` の先読みバッファから frame を切り出すようになり、pitch 解析は実 `la_pitch` 窓、LTP 残差は白色化した先読み、noise shape 窓は実 `la_shape` を読む
  - (2026-09-15 解消) VAD は `silk_VAD_GetSA_Q8_c` の固定小数点移植（`TestSILKVADOracle` で 5×40 frame 全一致）。first frame after reset は libopus どおり pitch 解析を行わず unvoiced。副作用で 8k speech-harmonic loudness が -1.62 dB（gate ±1.5 を 0.12 dB 超過）に戻り、unvoiced first frame のビット消費（rate control 未忠実）が原因
  - (2026-09-15 解消) LTP は pitch 解析残差 `res_pitch` から frame あたり 1 回だけ量子化し、`sum_log_gain_Q7` も 1 回更新するよう再編。AB gate は全 PASS（loudness 8k -1.25 / 12k -1.18 / 16k -0.92 dB）。16k steady-voiced (+1.19) と 16k onset (+2.20) は libopus に負けており、noise shaping / gain loop の exactness で再評価
  - (2026-09-15 解消) SILK の 5 ms (`LA_SHAPE_MS`) 先読み遅延と Opus 層の `delay_compensation` (Fs/250) を実装（`Lookahead()` = Fs/400 + Fs/250）。libopus decoder で測った end-to-end 遅延は CELT 312 = 312、SILK/hybrid 278 vs 312（Go の SILK 入力 resampler が libopus `silk_resampler` より 34 sample 短い）。残りは HP 前処理（`hp_cutoff`/`dc_reject`/`variable_HP_smth`）と SILK API resampler 移植
  - framing 変更後の loudness gate は 8k -1.71 / 12k -1.16 / 16k -1.76 dB（8k/16k が ±1.5 を超過）。先読みの有無を個別に切り替えても ±0.5 dB 動くだけで、gain loop（Q5）未忠実が原因
  - (2026-09-16) SILK front end を libopus に合わせた: `FLOAT2INT16` 量子化、等レート resampler の `delay_matrix_enc` 遅延（8/12/16k で 6/7/10 sample）+ `inputBuf+1` の 1 sample、±1e-6f anti-denormal offset。計装した libopus 1.6.1 encoder（`scripts/oracle/build_encoder.ps1`）との end-to-end oracle で、8/12/16k mono fixture の frame 0 は conditioned input / `x_buf` / `speech_activity_Q8` / HP state が bit 一致。packet は frame 0 から不一致（以降の解析・rate control 段）。24/48k の down-sampling FIR 未移植、Go の digital-silence shortcut（1 byte frame）は libopus と状態が分岐（policy 判断）
  - (2026-09-16 解消) 入力 HP 前処理（`hp_cutoff` / `dc_reject` / `variable_HP_smth1+2` / float API guard）を移植。libopus float 本体との単体 oracle で bit 一致。scoreboard は raw 入力基準のため HP の位相（cutoff 軌跡 60–100 Hz に基音が近い）で SNR gap が 1–3 dB libopus 側へ動いた（8k steady -2.68→+1.01 など）。loudness gate は 8k -2.33 / 12k -1.87 / 16k -0.59 dB。位相非依存の距離尺度が計器側の残作業
- **libopus 参照：** `silk/float/find_LTP_FLP.c`, `silk/quant_LTP_gains.c`, `silk/VQ_WMat_EC.c`, `silk/float/pitch_analysis_core_FLP.c`
- **スコアボード影響（2026-09-15, gap_SNR_matched 負=Go 優位）：** 8k steady-voiced -3.27→-1.86、16k steady-voiced -0.81→+0.41（libopus に僅差負け）、8k speech-harmonic -5.85→-6.94、16k onset -1.31→-1.57。loudness gate は 3 セル FAIL→12k のみ FAIL（8k -1.47 dB / 16k -1.33 dB は PASS）。Go 側の残差・lag が libopus と一致しない段階で量子化器だけ exact にした結果であり、pitch exactness 後に再評価

### Q3/Q4 残：ノイズシェーピング解析 + 本格遅延決定 NSQ

- **実装済み：** Q3a/Q4a — 単一候補の shaping 制御フィード、遅延決定トレリス NSQ の骨格（libopus 構造に忠実）
- **未実装：**
  - `silk_noise_shape_analysis_FLP` の完全ポート（AR/MA シェーピング係数、スペクトルチルト、HF/LF シェーピング、ハーモニックシェーピングゲイン）
  - `silk_prefilter_FLP` — 入力プリフィルタ
  - NSQ 候補数のスケーリング（現在 2 候補固定、libopus は complexity に応じて変化）
- **libopus 参照：** `silk/float/noise_shape_analysis_FLP.c`, `silk/float/NSQ_del_dec_FLP.c`
- **目標指標：** speech-harmonic fixture で `gap_SNR_matched` を 6.x → 3.x dB 程度に削減

### Q5 残：ゲイン処理/レートコントロールループ

- **実装済み：** Q5a/b/c — unvoiced ノイズのバイト爆発抑制、silence パディング除去
- **未実装：**
  - `silk_process_gains_FLP` の完全ポート — ゲイン量子化フィードバックループ
  - `silk_encode_frame` レベルのビット予算フィードバック（`ratio_bytes → 1.0` の達成）
  - voiced フレームの CBR 精度（現在は自然サイズまたは budget search）
- **libopus 参照：** `silk/float/process_gains_FLP.c`, `silk/float/encode_frame_FLP.c`
- **目標指標：** 全 fixtures で `ratio_bytes_matched ≈ 1.0`

### Q7：complexity スケーリング

- **未実装：**
  - `SetComplexity(0..10)` による NSQ 候補数・ピッチ探索深度の実際のスケーリング（現在は complexity 値を受け付けるが内部は固定）
- **目標指標：** complexity 0 で軽量、complexity 10 で `gap_SNR` 最小

---

## 🟠 CELT エンコーダ残存品質ギャップ

### ステレオ 24/32 kbps コーラスギャップ（~5.4–5.7 dB 残存）

- **経緯：** Post-audit CVBR 修正 → TF trim → tonality slope trim まで実施済み。
- **残作業候補：**
  - **Stateful stereo saving** — libopus の `stereo_saving` 状態による M/S ゲイン適応。CURRENT_IMPLEMENTATION.md の Known Gaps に明記。
  - **per-band dynalloc の surround パリティ** — 現在 surround では dynalloc は未適用。
- **計測済みギャップ：** 24 kbps → 5.726 dB、32 kbps → 4.948 dB（`TestCELTMusicChordsMatchedBitrateReproducer`）
- **採用基準：** 実コーパス全 140 セルで own-byte 総計が増えず、かつ gap が改善すること。

### CELT ビット完全一致

- 目標外（RFC はデコーダのみ規定）だが、参考指標として cross-check 精度向上は歓迎。

---

## 🟡 SILK/hybrid モード選択パリティ

[`docs/MODE_RATE_POLICY_DIFF.md`](MODE_RATE_POLICY_DIFF.md) に詳細な差分表あり。主要ギャップ：

| 項目 | libopus | 現在 Go | 優先度 |
|---|---|---|---|
| 音楽信号の predictive mode | 解析ベース閾値で SILK/hybrid に入り得る | `SignalMusic` で強制 CELT | 低（Phase 3 で 2 候補とも不採用） |
| SILK/hybrid モード閾値のヒステリシス | 閾値テーブル + 前フレーム状態 | 遷移冗長性のみ | 中 |
| ステレオ幅削減 | 低ビットレートで stereo width 縮小 | 未実装 | 中 |
| SILK 内部レート制御 | `silk_control_audio_bandwidth()` で NB/MB/WB 動的切替 | 入力 sample rate 固定 | 中 |
| 統合 voice_ratio 解析 | voice/music 比推定を使った mode 判定 | VOIP application / SignalVoice フラグのみ | 中 |

> **Note:** Phase D-3 で 2 候補を測定し、いずれも net per-bit 改善なしと判定済み。
> 次の policy 変更は必ず実コーパス scoreboard の具体的ターゲットを先に設定すること。

---

## 🟡 マルチストリーム/サラウンド残作業

Known Gaps（`CURRENT_IMPLEMENTATION.md`）より：

| 項目 | 状態 |
|---|---|
| **surround per-band dynalloc** | 未実装（mask 連携なし） |
| **mask-aware VBR** | 未実装 |
| **SILK/hybrid surround レートオフセット** | 未実装 |
| **LFE special CELT policy** | 未実装（LFE への LFE 専用 shaping なし） |
| **libopus multistream CTL 完全パリティ** | 一部 GET/SET が不完全 |

---

## 🟡 Ogg Opus コンテナ残作業

| 項目 | 状態 |
|---|---|
| 多重化ストリーム demux | 未実装 |
| chained stream を超えた multiplexed demux | 未対応 |

---

## 🟢 テスト/インフラ面

現状すでに十分ですが、さらに強化できるもの：

| 項目 | 現状 | 追加候補 |
|---|---|---|
| `go test -race` | 全通過 ✅ | — |
| 公式ベクター 12/12 | 全通過 ✅ | — |
| opusref cross-check | 全通過 ✅ | — |
| 実コーパス scoreboard | opt-in のみ | CI への組み込み検討 |
| 知覚品質指標 | なし | PESQ/ViSQOL の自動化 |

---

## 推奨着手順序

```
Q1 残（LPC/NLSF 完全ポート）
  → Q2（LTP 5タップ量子化）
    → Q3/Q4 残（noise shape 解析 + 本格 NSQ）
      → Q5 残（gain ループ完全化）
        → Q7（complexity スケーリング）
          → CELT stateful stereo saving
            → surround dynalloc/mask 拡張
```

**理由：** LPC → ピッチ/LTP → NSQ の順に依存関係があり、
量子化器（Q4）の改善は入力（Q1–Q3）が正確になって初めて意味を持つ。
CELT ステレオギャップは SILK 品質が安定してから測定すること。

---

## スコアボード現在値（参考）

`TestOpusSILKABAgainstLibopusEncoder`（16 kHz, 24 kbps, CVBR, complexity 5）:

| fixture | gap_SNR_matched (現在) | 目標 |
|---|---|---|
| silence | 0.0 dB | 0.0 dB |
| unvoiced-noise | ~2.7 dB | < 1.0 dB |
| steady-voiced | ~3.9 dB | < 1.5 dB |
| speech-like-harmonic | ~6.2 dB | < 2.0 dB |
| onset | ~2.2 dB | < 1.0 dB |

`TestCELTMusicChordsMatchedBitrateReproducer`（stereo CELT）:

| bitrate | gap_SNR_matched (現在) |
|---|---|
| 24 kbps | 5.726 dB |
| 32 kbps | 4.948 dB |

---

*このドキュメントは実装の進行に合わせて手動更新してください。*
*実装の権威は常に `docs/CURRENT_IMPLEMENTATION.md` にあります。*
