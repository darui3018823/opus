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
| **CELT エンコーダ** | 2026-09-18: CELT-only (48 kHz mono/stereo、CBR/CVBR、complexity 0〜10) が plain-C libopus 1.6.1 と byte 一致。hybrid・非 48 kHz 入力・無音 shortcut が残 |
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
  - (2026-09-16) libopus `silk_resampler` の encoder 方向（`delay_matrix_enc`）を移植し Opus 層の SILK 入力に採用。全 rate ペアで `silk_resampler(forEnc=1)` と一致、end-to-end 遅延は全モードで libopus と一致（48k 312 / 24k 155 / 16k 104）。oracle の段階トレースで frame 0 の分岐点を特定: NLSF/PredCoef 一致、Gains_Q16 は subframe 1 以降で差（Q5 gain loop）、unvoiced の AR_Q13/Tilt/LF は Go 側が意図的にゼロ（Q3）、Lambda_Q10 が 1–2 差
  - (2026-09-16) SILK front end を libopus に合わせた: `FLOAT2INT16` 量子化、等レート resampler の `delay_matrix_enc` 遅延（8/12/16k で 6/7/10 sample）+ `inputBuf+1` の 1 sample、±1e-6f anti-denormal offset。計装した libopus 1.6.1 encoder（`scripts/oracle/build_encoder.ps1`）との end-to-end oracle で、8/12/16k mono fixture の frame 0 は conditioned input / `x_buf` / `speech_activity_Q8` / HP state が bit 一致。packet は frame 0 から不一致（以降の解析・rate control 段）。24/48k の down-sampling FIR 未移植、Go の digital-silence shortcut（1 byte frame）は libopus と状態が分岐（policy 判断）
  - (2026-09-16 解消) 入力 HP 前処理（`hp_cutoff` / `dc_reject` / `variable_HP_smth1+2` / float API guard）を移植。libopus float 本体との単体 oracle で bit 一致。scoreboard は raw 入力基準のため HP の位相（cutoff 軌跡 60–100 Hz に基音が近い）で SNR gap が 1–3 dB libopus 側へ動いた（8k steady -2.68→+1.01 など）。loudness gate は 8k -2.33 / 12k -1.87 / 16k -0.59 dB。位相非依存の距離尺度が計器側の残作業
- **libopus 参照：** `silk/float/find_LTP_FLP.c`, `silk/quant_LTP_gains.c`, `silk/VQ_WMat_EC.c`, `silk/float/pitch_analysis_core_FLP.c`
- **スコアボード影響（2026-09-15, gap_SNR_matched 負=Go 優位）：** 8k steady-voiced -3.27→-1.86、16k steady-voiced -0.81→+0.41（libopus に僅差負け）、8k speech-harmonic -5.85→-6.94、16k onset -1.31→-1.57。loudness gate は 3 セル FAIL→12k のみ FAIL（8k -1.47 dB / 16k -1.33 dB は PASS）。Go 側の残差・lag が libopus と一致しない段階で量子化器だけ exact にした結果であり、pitch exactness 後に再評価

### Q3/Q4 残：ノイズシェーピング解析 + 本格遅延決定 NSQ

- **実装済み：** Q3a/Q4a — 単一候補の shaping 制御フィード、遅延決定トレリス NSQ の骨格（libopus 構造に忠実）
- **2026-09-17 着手（CELT Phase 5 step 1）：** `celt_encode_with_ec` の instrumented oracle（`--celt-enc`）と Go 側 `celt.FrameTrace` を追加（`TestCELTEncoderOracle`）。pre-emphasis を float32 + `preemph[0]=0.8500061035f` に修正 → CELT 解析入力 `in` が全 frame bit 一致。残差：MDCT が float32 KISS FFT との丸め差（int16 スケールで 1e-4）、coarse energy の intra 判定/Laplace、complexity ≥ 5 の pitch prefilter（Go 未実装）
- **2026-09-17 計測（CELT-only 起点）：** CBR CELT payload を `cbr_bytes-1` に修正（1 byte 長かった）。実 libopus と CELT-only（48k mono、CBR、FB 強制）比較：サイズ/TOC は一致、byte 一致 packet は 0。complexity 0 では 160B 中 62B 一致など band coding は近いが frame 先頭の判定（transient/tf/初期状態）が一部 frame で分岐、complexity 5/10 は pitch pre/postfilter 判定で byte 1 から分岐。自動 bandwidth では Go の信号解析による SWB 縮小と libopus の rate ベース FB 判定が食い違う（policy 判断をユーザーへ）。次は `celt_encode_with_ec` の oracle 化
- **2026-09-23 完了（libopus DTX + 40/60 ms の解析 + 帯域エネルギー）：** `ModePolicyLibopus` で libopus の DTX を移植（`encoder_dtx.go`: digital silence 判定・peak signal energy・フレーム毎の activity・`decide_dtx_mode` による TOC のみパケット・SILK 自身の DTX（`useDTX`/`inDTX`、全チャネル DTX で 0 byte → CELT 処理や delay buffer 更新の前に TOC だけ返す）・Opus の activity で SILK VAD を下げる処理）。40/60 ms パケットで tonality 解析を 20 ms フレーム毎に読み出すよう修正（complexity >= 7 で frame 0 から不一致だった）。`compute_band_energies` の 1e-27 を内積の後に加えるよう修正（無音直後の極小エネルギー帯域で分岐していた）。`TestDTXOracle` 48 cell 全 byte 一致（DTX パケット数も一致）
- **2026-09-18 完了（decide_fec narrowing + CELT loss 設定）：** `decideLibopusMode` が `decide_fec` を opus_encode_native と同じ位置で実行（5 % 超の loss では bandwidth を narrowing、CELT-only は `LBRR_coded` を 0 に）し、SILK 側はその 1 回の判定を使用。さらに `OPUS_SET_PACKET_LOSS_PERC` を CELT encoder へ伝播（`celt.Encoder.SetLossRate`: coarse energy の intra bias と prefilter tapset に効く）。これが無かったため FEC 有効時の hybrid packet が coarse energy で分岐していた（= 既知の「decide_fec in hybrid」ギャップの正体）。`TestAutoModeOracle` に FEC cell 48 個（5/20/40 % loss × 12/16/24/48 kbps × 16/48 kHz 入力 × mono/stereo）を追加、全 byte 一致で gate
- **2026-09-18 完了（8〜24 kHz 入力 + 無音フロー）：** libopus policy では CELT 層が入力をリサンプルせず `st->upsample` のゼロ挿入で 48 kHz モードを回す（`celtUpsample` / `celt.Encoder.SetUpsample`、MDCT bin のスケール・クリア込み）。`celt_encode_with_ec` の無音フロー（silence flag・VBR 2 バイト・`tell` の擬似消費 `AddTellBits`・prefilter 無効・drift 調整スキップ・`oldBandE`=−28）を移植し、Go 独自のエネルギー shortcut を撤去（DTX は同じ 2 バイト経路を使用）。`encodeRange` のフレームバッファを再利用化（stereo 20 ms: 177→139 alloc、無音: 70→32）。`TestAutoModeOracle` に 8/12/16/24 kHz 入力 cell、`TestAutoModeTransitionOracle` に同レートの遷移 cell を追加し全 gate
- **2026-09-18 完了（40/60 ms パケット）：** `encodeFloat` を libopus 1.6.1 と同じくパケット判定 + `opus_encode_frame_native` 相当（`encodeDecidedFrame`）に分割し、`encode_multiframe_packet`（`encoder_multiframe.go`: 20 ms フレーム毎の符号化、nonfinal_frame、最終フレームのみ to_celt、先頭フレームの冗長、各フレームの prefill、curr_max、repacketize/CBR padding）を移植。SILK-only 40/60 ms は native SILK 複数フレームパケット。`TestAutoModeOracle` に 40/60 ms cell、`TestAutoModeTransitionOracle` に 40/60 ms 遷移 cell を追加、全 gate
- **2026-09-18 完了（SILK 内部レート遷移）：** `silk_LP_variable_cutoff`（遷移 LP、biquad_alt_stride1、Transition_LP テーブル）と `silk_control_audio_bandwidth`（mode ±、switchReady、maxBits の冗長分）を SILK encoder に移植、Opus 層は libopus 準拠の単一フレーム SILK-only パス（`encodeSILKOnlyPacketLibopus`）と `silk_bw_switch` → 先頭冗長 + prefill 2（top-level 状態を引き継ぐ再初期化、8↔12↔16 kHz）。副産物: unvoiced/inactive frame で `sum_log_gain_Q7` を 0 に戻していなかったバグ修正（Go policy の perf digest 更新）。`TestAutoModeTransitionOracle` に 8k→24k と 160 frame の 24k→8k を追加、全 gate
- **2026-09-18 完了（mode 遷移 108/108 一致）：** bitrate スケジュール oracle (`--auto-enc` の per-frame bitrate) で SILK↔hybrid↔CELT 全方向を検証し、`opus_encode_native` の遷移処理を移植: to_celt 遅延 + 末尾 5 ms 冗長 CELT frame、CELT→SILK/hybrid の先頭冗長 frame + `silk_InitEncoder` + 10 ms prefill (`silk.Encoder.Prefill`)、mode 変化時の CELT reset + 2.5 ms prefill + `CELT_SET_PREDICTION(0)`、冗長分の bits_target/maxBits 差し引き、SILK-only 冗長のレイアウト、SILK `allowBandwidthSwitch`。副産物: stereo band-limited frame の `quant_coarse_energy` コピー長バグ、`patch_transient_decision` の Go 独自 voice 閾値撤廃。`TestAutoModeTransitionOracle` 108 cell 全 gate。残: 8〜24 kHz CELT/hybrid、SILK 内部レート遷移 (sLP)、無音 shortcut、既定 policy 切替判断
- **2026-09-18 完了（自動モード 60/60 一致）：** `combFilterMaxPeriod` 1022→1024（libopus `COMBFILTER_MAXPERIOD`）で prefilter 履歴と pitch_buf の 1 サンプルずれ・探索範囲不足を修正（VOIP CELT-only の pitch 近接差の正体）。stereo 入力の mono stream 符号化（`celt.Encoder.SetStreamChannels`＝CELT_SET_CHANNELS、CC=2/C=1 の MDCT 平均、C 基準の silence 窓・equiv_rate・tf_chan・energy bias/error・oldBandE ミラー、Opus 層の TOC・SILK downmix・SILK rate/redundancy・stereo width・prev_channels）。`TestAutoModeOracle` 60/60 cell 全 gate。残: SILK↔CELT 遷移冗長（to_celt 遅延・CELT→SILK の SILK 再初期化）、8〜24 kHz CELT/hybrid、無音 shortcut、既定 policy の切替判断
- **2026-09-18 完了（hybrid byte 一致・libopus mode policy をスイッチで移植）：** hybrid のサイジング（cbr_bytes、bits_target、SILK 分と maxBits、CELT 分、`CELT_SET_SILK_INFO`、hybrid tf/VBR 規則、HB_gain fade、SILK の stereo width fade、SILK レート 80 kb/s 上限撤廃）で `TestHybridEncoderOracle` 36 cell 全一致。`decideLibopusMode`（equiv_rate、voice_est、compute_stereo_width、mode_thresholds、bandwidth 閾値・cap・detected_bandwidth、SILK↔hybrid）を `SetLibopusModePolicy(true)` で選択可（既定は Go policy のまま＝ユーザー判断待ち。切替で既存 encoder テスト ~15 件の期待 mode が変わる）。`TestAutoModeOracle` 49/60 cell 一致。残: stereo 入力の mono hybrid/CELT stream、VOIP CELT-only の pitch 近接判定（frame 11）、SILK→CELT 遷移冗長、8〜24 kHz CELT/hybrid、無音 shortcut
- **2026-09-18 完了（CELT-only encoder byte 一致）：** `celt_encode_with_ec` を段階ごとに float32 忠実移植（MDCT/KISS FFT、band energy、tone/transient/dynalloc/tf 解析、coarse energy の delayedIntra・two-pass、spread、trim、band skip、`op_pvq_search`、theta_rdo、fine energy、pitch prefilter、compute_vbr + reservoir、stereo fade、tonality analysis + MLP）。`TestCELTEncoderOracle` = 48 kHz mono 66 cell + stereo 18 cell × 20 frame が全て一致・gate。実 libopus（msys2、SIMD RTCD）は float 加算順が異なり byte 一致しない（`TestCGOEncodeRefCELTByteExact` は報告のみ）
- **2026-09-17 完了（complexity 0〜10 全て byte 一致）：** complexity 掃引テスト（`TestCGOEncodeRefSILKComplexity`）で complexity 1 だけ pulses が分岐 → libopus は nStates==1 かつ warping 0 のとき delayed-decision ではなく plain `silk_NSQ` を使う（Q12/Q20 の丸め・飽和が異なる）。`nsq_plain.go` に `silk_NSQ` / `silk_nsq_scale_states` / feedback loop / `silk_noise_shape_quantizer` を移植し wrapper の分岐を再現。8/12/16k × complexity 0〜10 全 12 packet 一致
- **2026-09-17 完了（自動 bandwidth 判定 → SILK 内部レート）：** `opus_encode_native` の bandwidth 閾値テーブル（voice_est 補間、FB→NB の hysteresis 付き走査、MB→WB 昇格、入力レート上限）を移植し、最初の SILK packet 前に SILK 内部レートを決定（NB は 12/16/48k→8k resampler 経由）。`TestCGOEncodeRefSILKAutoBandwidth`（12/16/48k × 6〜12 kbps）全 packet byte 一致。未移植: 途中切替（`allowBandwidthSwitch` / sLP 遷移フィルタ / 冗長 frame）と decide_fec の帯域縮小
- **2026-09-17 完了（stream channel 判定 / stereo 入力の mono stream 符号化）：** `opus_encode_native` の mono/stereo 判定（stereo_music/voice_threshold を voice_est で補間、±1000 hysteresis、toMono による 1 packet 遅延）と silk_Encode の nChannelsInternal=1 経路（`RES2INT16(L+R)` を丸め半分、最初の mono frame は両 resampler 出力の平均、stereo 復帰時は side を再初期化して mono 側 resampler 状態をコピー・predictor/norm/width を再始動、遷移 packet の LBRR flag クリア）を移植。**実 libopus encoder と stereo 16/20 kbps・32→16→32 kbps 遷移（8/16/48k 入力）全 packet byte 一致、`TestCGOEncodeRefSILKByteExact` 99 セル中 84 一致**。残 15 = 24/48k mono 入力で hybrid（14）、decide_fec の帯域縮小（1）。voice_est は signal hint / application のみ（complexity ≥ 7 の tonality analysis は未移植）
- **2026-09-16 完了（CBR SILK も byte 一致）：** `encode_frame_FLP` の quantiser loop（gainMult_Q8 の bracketing/補間、gain lock、Lambda×1.5 + quantOffset 0、damage control、fitting pass の復元）を `encode_frame_loop.go` に移植、range coder に Clone/Restore、`SetMaxBits` に silk_Encode の frame 別スケール。Opus 層は CBR packet を `cbr_bytes = (bits+4)/8`（TOC 込み）に、SILK rate を `bits_to_bitrate(cbr_bytes*8-8)`、`opus_packet_pad` のレイアウト（単 frame は code 3 + padding、長さ無し）で padding、hybrid の SILK maxBits も libopus 準拠。**実 libopus encoder と CBR mono 15 セル・CBR stereo 15 セル（同 channel 数）全 14 packet byte 一致**（CVBR/FEC/CBR 合計 119 セル中 90 = policy 差以外の全セル）。CBR FEC テストの利得ゲートは +3 dB に戻した（Go 19.7 vs 15.6 dB）
- **2026-09-16 完了（stereo SILK も byte 一致）：** `silk_stereo_LR_to_MS` を固定小数点で正確移植（libopus 関数を cgoref で直接呼ぶ oracle で一致確認）、stereo packet フローを `silk_Encode` 準拠に（VAD/LBRR flag の placeholder + `ec_enc_patch_initial_bits`、frame 毎の mid/side rate 分配、mid-only 判定と side の部分リセット + `CODE_INDEPENDENTLY_NO_LTP_SCALING`）、`silk_sigmoid` を double 評価、hybrid の SILK rate を `compute_silk_rate_for_hybrid` に。**実 libopus encoder と mono+stereo 66 セル中 49 セル = libopus が同じ channel 数で SILK-only を選ぶ全セルで 14 packet byte 一致**。残 17 セルは policy（24/48k mono 入力で hybrid、16〜20 kbps stereo で mono downmix）
- **2026-09-16 完了（in-band FEC / 実 libopus encoder と byte 一致）：** `silk_LBRR_encode_FLP`（regular frame の gain symbol + `LBRR_GainIncreases`、`silk_gains_dequant`、同じ seed / Lambda / pre-frame NSQ 状態）、`silk_setup_LBRR`、`curr_nBitsUsedLBRR` の計測位置、LBRR frame の conditional coding（lag delta、LTP_scaleIndex）、`silk_LTP_scale_ctrl_FLP`、Opus 層の `decide_fec` / `compute_equiv_rate`、sine window の float32 PI 除算を移植。oracle は 24 kbps / 24 kbps+loss 20% / 32 kbps の全 60 shared-state セルで byte 一致、**`TestCGOEncodeRefSILKByteExact`（cgoref の実 libopus encoder、auto mode）で 30 セル中 24 セルが 14 packet byte 一致**（残 6 = libopus が 24/48k 入力 32 kbps で hybrid を選ぶ mode policy）。残: 無音 shortcut、stereo、CBR gain loop、decide_fec の帯域縮小、mode/bandwidth policy
- **2026-09-16 完了（SILK-only VBR byte 一致）：** payload 長 `(tell+7)>>3` + 末尾 0 strip、NSQ seed = `frameCounter&3`、find_LPC を int16 スケールで実行（Burg の `1e-9f` 正則化がスケール依存）、inactive frame も unvoiced と同じ解析/NSQ 経路、rate level を Q5 bit テーブルで選択。**8/12/16/24/48k mono CVBR 24 kbps の 12 frame 全 packet が、無音 shortcut を踏まない全 15 fixture で libopus 1.6.1 と byte 一致**（`TestSILKEncoderInputPipelineOracle` がゲート）。残: 無音 shortcut policy（libopus は無音も符号化）、LBRR/stereo/CBR/hybrid の oracle 化
- **2026-09-16 完了（Q5 VBR）：** `process_gains_FLP` / `silk_gains_quant` / `residual_energy_FLP` を float32 移植し、VBR/CVBR では（libopus と同様に 1 pass で）その gain をそのまま符号化。`encode_pulses` の `combine_and_check`（shell 各段の上限 8/10/12/16）を再現、48k mono の homebrew NSQ fallback を撤去。**reset 直後の最初の SILK packet が 13/16 oracle セルで libopus と byte 一致**。残り: frame 0 の ±1 byte（8k noise / 12k steady / 24k harmonic、entropy coding の隅）、frame 1 以降の NLSF（状態依存の LPC 入力）と NSQ seed、CBR ループ
- **2026-09-16 完了（Q3）：** `silk_noise_shape_analysis_FLP` を float32 忠実移植（`noise_shape_flp32.go`）。計装 libopus encoder の `SHAPE_*_FLP` dump と AR/Gains/LF/Tilt/Harm/quality が bit 一致（入力が共有される frame で）。libopus の TargetRate（bit reservoir、LBRR 平均、TOC 分の bitrate 減算）も移植し `SNR_dB_Q7` が一致。NSQ の shaping を全 frame 種でこの結果に切替（unvoiced/stereo の Go 独自 neutral shaping を撤去）→ **loudness gate 全 rate PASS（8k -0.64 / 12k -0.25 / 16k -0.01 dB）**
- **未実装：**
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
