# Post-Audit 改善マスタープラン (2026-07-15)

> **2026-07-16 更新**: D-2 は iter1 棄却・iter2 (hysteresis) 測定中断の時点で
> 路線を再定義した。後継プランは
> `.claude/plans/pure-go-completeness-2026-07-16.md`。
> (libopus parity 追撃を凍結し、Pure Go ライブラリとしての完成度を優先)。
> 現在の状態は `docs/CURRENT_IMPLEMENTATION.md` を正とする。

役割分担: **Claude = 指示塔 (このファイルの管理・タスク切り出し・判定)、Codex = 実装 (コード読解・編集・測定)**。
各 Phase を着手するとき、Claude がこのプランから作業性質に合う
`.claude/tasks/<type>/<topic>.md` 形式の個別指示書を切り出して Codex に渡す。

根拠: `.claude/memory/audits/libopus-completeness-2026-07-01.md` (第3回監査、HEAD `8142def`)。
現状スコア: Pure Go Opus として約88% / libopus 完全代替として約74%。

## このセッションでコードを見て確認済みの事実

プラン作成時 (2026-07-15、main = `8142def`) に Claude が直接 grep で確認したもの。
Codex は着手時に再確認してよいが、下記は当時点で正:

1. **公開 `DecodePLC` は CELT-only**。`opus.go:3364` が入口、`opus.go:3376` で
   SILK-only/hybrid は `ErrUnimplemented` を返す。CELT 経路は `d.lastCeltDec.DecodePLC()`
   (`internal/celt/decoder.go:635`) を呼んでいる。
2. **SILK の内部 PLC は実装済みで実運用中**。`internal/silk/decoder.go` に
   `concealPacketLoss` (:2255)、`concealPacketLossMono` (:583)、`concealFECFrames` (:591)、
   `updatePLCState` (:2322) があり、FEC fallback・stereo mid/side 経路 (:556, :570, :756 など)
   から既に呼ばれている。**つまり public SILK PLC の本体は書かなくてよく、残りは配管**。
3. **公開 CTL 相当 API の現在の顔ぶれ** (`opus.go`):
   Encoder: SetForceChannels / SetLSBDepth / SetPredictionDisabled / SetPhaseInversionDisabled /
   SetBitrate / SetComplexity / SetVBR / SetVBRConstraint / SetPacketPadding / SetDTX /
   SetInbandFEC / SetPacketLossPerc / SetApplication / SetSignalType / SetMaxBandwidth / SetBandwidth。
   Decoder: GetLastPacketDuration / SetGain / SetPhaseInversionDisabled。
   → 監査の指摘どおり current-bandwidth getter、packet-has-LBRR、soft clip、
   expert frame duration、multistream pad/unpad が不在。
4. `DecodeFEC` は CELT-only packet で `ErrUnimplemented` (`opus.go:3430`)、
   multistream FEC も未対応 (`opus.go:3443`)。これは仕様として妥当 (CELT に LBRR はない) で、
   本プランの対象外。

## 過去の教訓 (Codex への必須申し送り)

- **transparent NLSF の教訓**: SNR だけ見て bytes を見ないと「勝ちに見える罠」を踏む
  (Phase 5 は全セル bytes +60〜148% で per-bit では負け)。encoder 品質系の変更は
  **必ず bytes 比を併記した matched-bitrate 比較**で判定する。
- **RC (range coder) 周りは触るな**。過去に FEC を破壊した実績あり。
- **find_LPC_FLP の easy-win 路線は完全に尽きている**。encoder 品質への追加投資は
  Phase C の測定基盤ができるまで凍結。
- `go test -tags opusref` は **PowerShell tool でのみ実行可** (Bash では CGO が動かない)。
- libopus FLP 参照ソース: `$TEMP\opussrc\opus-1.5.2\silk\float\`。
  oracle ビルド: `pwsh scripts/oracle/build.ps1`。

---

## Phase A: SILK-only / hybrid の public `DecodePLC` (P1・最優先)

**なぜ最優先か**: 監査 P1 筆頭。内部 PLC が完成済みなので工数対効果が全項目中最大。
これ 1 つで「VoIP/WebRTC 用途で libopus 代替を名乗れない」最大の理由が消える。
encoder 品質と違い測定地獄にならず、着地条件が明確。

### タスク分解 (Codex 向け)

A-1. `opus.go` の `DecodePLC` で、直前パケットが SILK-only だった場合に
     `internal/silk` の conceal 経路へルーティングする。
     - 直前 decode に使った SILK decoder インスタンス (lastSilkDec 相当) の特定が最初の調査点。
     - SILK 内部レート (8/12/16 kHz) → 公開レートへの resampler 経路と channel 調整は
       通常 decode 経路のものを流用する。
     - frameSize は SILK の frame 長 (10/20/40/60 ms) との整合検証が要る。
A-2. hybrid: 既存 hybrid decode 経路をなぞり「SILK PLC + CELT PLC の時間領域和」を実装。
     - CELT 側は既存 `celt.Decoder.DecodePLC()` を利用。
     - 加算前の resample/位相整合は通常 hybrid 経路と同一にすること。
A-3. PLC 後に通常 `Decode` へ戻ったときの状態継続。
     - `updatePLCState` が呼ばれること、次フレームの gain/pitch 継続が壊れないこと。
     - FEC 側に既にある continuity guard テストのパターンを流用する。
A-4. テスト:
     - unit: SILK-only mono/stereo、hybrid mono/stereo で loss → PLC → 通常復帰。
       energy が急減衰しつつ非ゼロ (無音埋めでない) こと、クリックが出ないこと。
     - opusref: libopus の PLC と loss-pattern 比較。**bit-exact は要求しない**。
       SNR 床 + 継続性 guard (既存 FEC テストの 10dB/20dB 床方式に倣う)。
     - `opus.go:3363` 付近の doc comment と `docs/CURRENT_IMPLEMENTATION.md` の
       「PLC は CELT-only」記述の更新を忘れない。

### 完了条件

- SILK-only/hybrid で `DecodePLC` が `ErrUnimplemented` を返さない。
- `go test ./...` + `go test -tags opusref ./...` (PowerShell) 全 PASS、12/12 vector 維持。
- docs 更新済み。

## Phase B: CTL/API parity matrix + 安い穴埋め (P1・小粒の勝ち)

**2 本立て**: (1) 未対応 CTL 一覧表を docs に落とす (監査の「libopus を名乗るなら明示せよ」への
直接回答)、(2) 数時間級で埋まるものだけ先に実装。

### タスク分解

B-1. parity matrix 作成: `docs/CTL_PARITY.md` (新規)。
     libopus 1.6.1 の `opus_defines.h` / `opus_multistream.h` / `opus_projection.h` の
     全 CTL を列挙し、対応する Go API / 未対応 / 対象外 (DRED 等) を 3 値で分類。
     Codex に libopus ヘッダを読ませて機械的に作らせる。
B-2. 安い実装 (それぞれ独立 commit):
     - `Decoder.GetBandwidth` / `Encoder.GetBandwidth` 相当 (直近パケットの帯域 getter。
       decoder は TOC から即答できる)。
     - packet-has-LBRR helper (packet parse 層に既にある情報の公開)。
     - `opus_pcm_soft_clip` 相当の公開 helper (独立した小関数、libopus の
       `opus_pcm_soft_clip_impl` を参照移植)。
     - multistream packet pad/unpad (repacketizer 基盤が既にあるので薄い)。
     - expert frame duration 相当 (encoder framing 制御の公開)。→ 他より重ければ後回し可。
B-3. `docs/CURRENT_IMPLEMENTATION.md` と README の「対応 CTL」記述を matrix にリンク。

### 完了条件

- matrix が存在し、実装した getter/helper に unit テストがある。全テスト green。

## Phase C: 実音声 corpus の matched-bitrate scoreboard (P1・次の投資の前提)

**機能追加ではなく測定インフラ**。encoder policy (Phase D) より先に必須。
synthetic fixture ベースの測定は find_LPC_FLP で限界が実証済み。

### タスク分解

C-1. corpus 取得スクリプト: LibriSpeech / VCTK 等 CC ライセンスの短 clip を
     `testdata/` (git-ignore 済み) に落とす PowerShell or Go スクリプト。
     clean speech / noisy speech / onset・plosive 多め / stereo speech / music / mixed を各数本。
C-2. 既存 gap_SNR_matched 方式の AB harness を実音声セルへ拡張。
     **bytes 比を必ず併記** (per-bit 判定)。bitrate sweep (16/24/32/48/64 kbps 級) と
     packet loss sweep (0/5/10/20%) を軸に持つ。
C-3. 出力はスコアボード (markdown or CSV)。CI には載せない (testdata 非同梱のため)、
     ローカル/手動実行の diagnostic テストとして `t.Skip` ゲート。

### 完了条件

- 1 コマンドで libopus との matched-bitrate A/B スコアボードが出る。
- 現状の SILK/hybrid encoder のベースラインが数字で記録される (これが Phase D の出発点)。

## Phase D: SILK/hybrid mode-rate-quality policy (P1 だが Phase C の後)

最重量級。**いきなり潰しにいかない**。

D-1. まず未対応表: libopus の mode decision / rate control (`silk/float` 原典 +
     `opus_encoder.c` の mode 選択) と現実装の差分表を Codex に作らせる。
D-2. Phase C のスコアボードで per-bit 勝敗を見ながら 1 gate ずつ拡張。
     **測定なしで gate を広げない** (transparent NLSF の教訓)。
D-3. 現状の狭い gate (`ApplicationVOIP` / `SignalVoice` / bitrate / bandwidth 条件) の
     緩和候補を優先度順に並べ、1 つずつ独立 branch で検証。

## 後回し・見送り (理由付き)

- **CELT encoder parity (P2)**: 相互運用は既に強く scoreboard も勝ち。限界効用が低い。
- **Ogg seek/chained/multiplex (P2)**: 有用だが独立機能。Phase A/B 後の気分転換枠。
- **DRED/QEXT DSP (P2/P3)**: DNN blob 込みの巨大工事。「transport のみ対応」と docs に
  明記して**採否を「見送り」で確定**させるのが正しい処理 (Phase B-3 で docs に書く)。
- **multistream psychoacoustic parity / allocation 削減**: 実害の報告が出るまで着手しない。

## 進行ルール

1. 着手順: **A → B → C → D**。A と B は独立なので B の安い実装を A のレビュー待ちに挟むのは可。
   **通し実行の可否 (2026-07-15 決定)**: B → C → D-1 (未対応表の作成まで) は測定依存が無いので
   連続実行してよい (branch/PR は Phase ごとに分ける)。**D-2 以降の gate 拡張だけは通し禁止**:
   C の scoreboard で per-bit 勝敗を測りながら 1 gate = 1 branch で進める (透過 NLSF の教訓)。
2. 各 Phase は独立 branch (`dev/plc-silk-hybrid` など) + PR。main 直 push しない。
3. 全 PR 前に PowerShell で `go test ./...` + `go test -tags opusref ./...` を回す。
4. Codex への指示書は本ファイルを参照させつつ、Phase ごとに
   `.claude/tasks/<type>/<topic>.md` として切り出す。
5. Phase 完了ごとに本ファイルの完了条件へ ✅ と commit hash を追記し、
   memory (`MEMORY.md` / 関連 project ファイル) を更新する。

## ステータス

- [x] **Phase A: SILK/hybrid public PLC — 完了・main マージ済** (PR #19, merge `7b899dd`,
  2026-07-15)。Codex 実装 `d0f5828` + レビュー対応 `e04940d`。
  実装メモ: `lastPacketConfig/Channels` を Decode/DecodeFEC で保存し PLC ルーティングに使用、
  hybrid は SILK+CELT PLC の時間領域和 (±1 clamp)、stereo は mid/side 独立 conceal +
  保持predictor で M/S→LR。
  レビュー対応 (Codex bot P2×2、Claude 検証で両方 CONFIRMED → 修正):
  (1) DecodeFEC の prevMode を framing 定数へ正規化 (FEC直後PLC が ErrInvalidState だった)、
  (2) digital-silence packet で SILK 合成履歴 (LPC/LTP/stereo unmix delay) をクリアし
  silence 継続を silence で conceal (stereo unmix delay の漏れは修正中に発見した追加バグ)。
  回帰テスト 2 本追加、full suite + opusref 全 PASS。
- [x] **Phase B: CTL parity matrix + 安い穴埋め — 完了** (Codex commit `6ad3375`,
  2026-07-15)。`docs/CTL_PARITY.md` を追加し、GetBandwidth、packet-has-LBRR、
  soft clip、multistream pad/unpad を実装。`go test -count=1 ./...` PASS。
  **Claude レビュー (2026-07-15)**: GetBandwidth / PacketHasLBRR / multistream pad-unpad は
  承認。**softclip.go は単一 peak 近似で libopus と乖離** (逆符号領域の増幅バグ +
  frame 継続条件の逆転) → Claude が忠実移植に書き直し (`f9f9420`)、
  多領域/継続/多ch 回帰テスト追加、1/2ch 制限も撤廃 (libopus は任意 ch)。
- [x] **Phase C: 実音声 corpus scoreboard — 完了** (Codex commit `2e02209`,
  2026-07-15)。real-corpus fetch script、`opusref` diagnostic scoreboard、
  `docs/REAL_CORPUS_SCOREBOARD.md` を追加。通常テスト、skip gate、1秒 smoke を確認。
  **Claude が baseline 取得・記録済み (`61cdb68`)**: 134 セル中 132 ok、
  speech 系は libopus と実質互角 (avg gap ±0.2dB)、大負けは CELT/music セルのみ
  (synthetic chords stereo +7.7〜9.7dB = D-2 対象外)。**2 セルで
  `hybrid frame 0 exceeds target: 121-123 > 120 bytes` の own_encode_error =
  D-2 の最初の修正対象**。詳細 `docs/REAL_CORPUS_SCOREBOARD.md` の Baseline 節。
- [ ] Phase D: mode-rate-quality policy
  - [x] **D-1: mode/rate/quality 差分表 — 完了** (Codex commit `b85da59`,
    2026-07-15)。`docs/MODE_RATE_POLICY_DIFF.md` で libopus 1.6.1 と現実装の
    mode decision / bandwidth / rate-control / SILK quality / transition 差分を整理。
    `go test -count=1 ./...` + `go test -count=1 -tags opusref ./...` PASS。
  - [ ] D-2: Phase C scoreboard を見ながら 1 gate ずつ拡張。
    **着手順 (baseline より)**: (0) hybrid encode-size error 修正 (バグ、gate 拡張ではない)
    → (1) 差分表の SILK entry/hysteresis 系 gate を 1 つずつ。
    - [x] **Iteration 0: hybrid encode-size error 修正 — 完了** (Codex branch
      `codex/d2-hybrid-target-clamp`, 2026-07-15)。非 redundancy の VBR/CVBR
      hybrid frame で range coder final flush が adaptive target を数 byte 超えた場合に
      encode error にせず実 frame size として出力。`go vet ./...`、
      `go test -count=1 ./...`、`go test -count=1 -tags opusref ./...` PASS。
      real-corpus scoreboard は 140/140 `status=ok`。

**B〜D-1 = PR #20 で main マージ済 (merge `ff70c75`, 2026-07-15, admin bypass はユーザー明示許可)。**
**D-2 指示書 = `.claude/tasks/investigation/d2-policy-gates.md` 切り出し済み** (iteration protocol:
1 gate = 1 branch = 1 測定、Iteration 0 = hybrid encode-size error 修正、
結果はこのプランのステータスと関連 task brief に記録、未 push で報告 → Claude 検分)。
PR #20 レビュー対応: Gemini high (softclip ゼロ交差 `>=` → `>`) は **libopus v1.6.1 原典照合で
false positive・reply 済み** (upstream も `>=0`)。Gemini medium (WAV parser 32-bit overflow) は
修正 `b11b8ba`。Codex P3 (Glob `**` は非再帰で nested corpus を skip) は CONFIRMED →
WalkDir 化 `df2ebdd` (デフォルト corpus では同一 7 ファイル発見をスモークで確認)。
