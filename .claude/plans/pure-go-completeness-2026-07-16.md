# Pure Go Opus 完成度ロードマップ (2026-07-16)

> **Status update (2026-07-17):** Phase 0–2 are complete and merged into
> `main` by PR #22 (`60cb602`). Phase 3-1, the unified performance benchmark
> harness and recorded baseline, is complete locally on
> `codex/phase3-perf-harness`.

前プラン: `.claude/plans/post-audit-2026-07-15.md` (A/B/C/D-1 + D-2 iter0 完了・main マージ済)。
本プランはその後継。**目標の再定義**: libopus の完全代替 (bit-exact / 全 CTL / 全品質 parity) は
追わない。追うのは **「最良の Pure Go Opus ライブラリ」としての完成度** = 機能の完備・
堅牢性・性能・リリース品質。encoder 品質の parity 追撃は凍結棚へ (根拠は下記)。

体制: **Claude = 指示塔** (タスク切り出し・検分・判定・push/PR)、**Codex = 実装**。
着手時に Claude が Phase ごとに `.claude/tasks/<type>/<topic>.md` を切り出す。

## なぜこの再定義か (根拠)

- 現状評価 (2026-07-15 ユーザー報告): libopus 代替 ~78-80%、**Pure Go として ~90%**。
- speech 系 encoder は matched bytes で libopus と実質互角 (real corpus 140/140、avg gap ±0.2dB)。
  残る大負けは CELT/music セルのみで、これは監査時から「見送り」確定領域。
- D-2 policy-gate 路線は iter1 (SILK entry 閾値) が棄却され、停止条件
  (候補 1-2 で net win なし) に接近。ここに追加投資するより、
  ライブラリとしての穴 (multistream FEC/PLC・Ogg seek・堅牢性・性能・v1 リリース) を
  埋める方が 1 iteration あたりの価値が高い。

## 共通 iteration protocol (D-2 方式の一般化)

全 Phase 共通。**1 iteration = 1 論点 = 1 branch = 1 検証 = 1 判定**。

1. main から branch: `codex/<phase>-<短い切り口名>`。
2. 実装 (1 論点のみ。複数論点を混ぜない)。
3. 共通検証 (すべて PowerShell):
   - `go vet ./...`
   - `go test -count=1 ./...`
   - `go test -count=1 -tags opusref ./...` (12/12 vector 維持が絶対条件)
4. Phase 固有の計器で測定 (各 Phase の節を参照)。
5. 判定基準を満たせば採用、満たさなければ棄却 (branch は削除せず放置)。
   迷ったら数字を添えて報告し Claude の判定を待つ。
6. 採否とも `.claude/<phase>_iteration_log.md` に追記:
   iteration 番号 / branch / 変更概要 / 測定要点 / 判定 / 理由。
7. **未 push のまま報告** → Claude 検分 → 採用分を Phase 統合 branch へ積む →
   Phase の停止条件到達で一括 PR。

### 共通ガードレール (全 Phase・違反したら即手を止める)

- RC (range coder) のビット読み書き順は変更禁止 (FEC/conformance 破壊の実績)。
- 12/12 official vector + opusref 全 PASS を全 iteration で維持。
- encoder 挙動 (packet bytes) を変える変更は enc/dec final-range 一致を必ず確認
  (cross-decode だけでは不十分 — D-2 iter0 の教訓)。
- `OPUS_SILK_TRANSPARENT_NLSF` / `OPUS_SILK_RC_SNR` の env-gate は触らない。
- `go test -tags opusref` は PowerShell でのみ実行 (Bash では CGO 不可)。

---

## Phase 0: D-2 クローズアウト (短期・最初にやる)

宙に浮いている D-2 iter2 (branch `codex/d2-mode-hysteresis`, `0f17758`) を決着させ、
policy-gate 路線を正式に閉じる。

- 0-1. iter2 の full scoreboard を最後まで実行 (`OPUS_REAL_CORPUS=1`、~150s、140 セル)。
  判定基準: hysteresis は挙動安定化 (境界 bitrate での mode ちらつき低減) が目的なので、
  **全クラス neutral (gap 悪化 +0.3dB 以内・bytes 比悪化なし) なら採用**。
  対象セル改善は要求しない。
- 0-2. 採用なら iter2 を PR (D-2 最終成果)。棄却なら branch 放置。
  いずれも親プランのステータスと関連 task brief に結論を書き、
  `docs/MODE_RATE_POLICY_DIFF.md` へ「D-2 は iter0(採用)/iter1(棄却)/iter2(採否) で終結、
  残差分は凍結」と明記。
- 0-3. 候補 3-5 (SILK internal rate / stereo width / overhead 補正) は**着手せず凍結棚へ**
  (停止条件どおり)。

完了条件: 親プランと関連 task brief に終結宣言があり、docs が現状と一致している。

## Phase 1: 機能ギャップの完備 (P1・ライブラリとしての穴埋め)

「実装されていない公開機能」を潰す。品質測定 (scoreboard) は不要な領域なので
測定地獄にならず、Codex iteration に最適。計器 = unit テスト + opusref 相互運用テスト。

優先順 (1 iteration = 1 機能):

- **1-1. Multistream/Surround の `DecodeFEC`** — 単一 stream の SILK LBRR 抽出は
  実装済みなので、self-delimited framing を剥がして per-stream に配る配管。
  Phase A (public PLC) と同型の低リスク・高価値タスク。
  計器: Go round-trip + libopus 相互運用 (loss pattern で libopus encode → Go DecodeFEC)。
- **1-2. Multistream/Surround の `DecodePLC`** — 同上。per-stream PLC を束ねる。
  計器: energy 減衰・非ゼロ・継続性 guard (Phase A のテストパターン流用)。
- **1-3. Ogg Opus seek** — granulepos ベースの時刻 seek (bisection)。
  `oggopus.Reader` に `Seek(sample int64)` 相当を追加。pre-skip / end-trim の正確な扱い。
  計器: 既知位置 seek → decode 一致、境界 (先頭/末尾/page 境界) テスト。
- **1-4. Ogg chained stream** — EOS 後の次 logical stream 継続 (Reader の再初期化配管)。
  計器: 2+ chain の合成 fixture round-trip。
- **1-5. expert frame duration 相当** (Phase B の積み残し CTL) — encoder framing 制御の公開。
- **1-6. (optional) Ogg multiplexed demux / projection custom encoder matrix** —
  需要が出たら。デフォルトは見送りのまま docs に明記。

判定基準: 新機能はテスト付きで全 suite green。既存挙動の byte 変化ゼロ
(decoder/encoder の既存経路に触れないこと)。

停止条件: 1-1〜1-5 消化で Phase 完了。1-6 は採否をユーザーに確認。

## Phase 2: 堅牢性 (fuzz 拡張・敵対入力保証)

Pure Go ライブラリの売り = 「untrusted 入力で panic しない」を保証として明文化する。
既存: nightly fuzz 6 本 (Decode/DecodeFloat/PacketExtensions/MultistreamDecode/
Repacketizer/OggParsers)。

- **2-1. Stateful sequence fuzz** — 単発 packet でなく packet **列** を fuzz:
  mode/bandwidth/channel 切替 + PLC/FEC 呼び出しを interleave した decoder 状態機械の fuzz。
  (decoder は状態持ちなので単発 fuzz では踏めないパスが多い。)
- **2-2. oggopus Reader/Writer の end-to-end fuzz** (page parser 単体は済)。
- **2-3. Encoder 入力 fuzz** — 極端 PCM (NaN/Inf は float API、フルスケール矩形、DC) +
  全 setter 組合せで encode が panic せずエラーを返すこと。
- **2-4. Differential fuzz (opusref・ローカルのみ)** — fuzz corpus を Go と libopus の
  両 decoder に食わせ、片方だけ受理/拒否するケースと出力乖離を収集。
  crash でなくても conformance バグの検出器になる。
- **2-5.** 見つけた crash/divergence は 1 fix = 1 iteration で潰し、再現 corpus を
  `testdata/fuzz/` seed へ昇格。
- 仕上げ: docs に「入力安全性の保証」節を追加 (何を保証し何を保証しないか)。

計器: `go test -fuzz` の実行時間 x crash 0。判定基準: 新 fuzz ターゲットは
30 分 fuzz で crash 0 になるまで fix を積んでから採用。

## Phase 3: 性能 (測定駆動の最適化)

計器を先に固定してから 1 hotspot = 1 iteration。**benchstat なしの「速くなったはず」は禁止**。

- **3-1. ベンチ harness 統一** — 代表 workload のベンチを 1 箇所に整備:
  encode/decode x mono/stereo x 20ms x (CELT/SILK/hybrid) x 48k、
  `-benchmem` 込み。baseline を `docs/PERF_BASELINE.md` に記録。
  (既存ベンチは散在しており全体像が出ない。)
- **3-2 以降. 最適化 iteration** — 候補 (プロファイル取ってから決める):
  - alloc 削減の続き (Bluestein plan cache 方式の他経路展開、decode 経路の scratch 再利用)
  - MDCT/FFT/PVQ の hot loop (bounds-check 回避、slice 化)
  - resampler の per-sample 呼び出しコスト
- 判定基準: benchstat で **≥5% 改善 (時間 or alloc)**、かつ挙動不変の証明 =
  代表入力で encoder は packet bytes 完全一致・decoder は PCM 完全一致
  (final-range 一致で担保)。不変を崩す「最適化」は品質変更であり本 Phase の対象外。
- 停止条件: プロファイル上位の hotspot を消化し、追加 iteration の期待改善が
  5% を切ったら終了。

## Phase 4: リリース品質 / DX (v1.0 への整地)

- **4-1. godoc 総点検** — 全公開 API に使用可能な doc comment、package doc、
  `example_test.go` (Encode/Decode/Ogg read-write/multistream の 4 本)。
- **4-2. README 刷新** — 現状 (`docs/CURRENT_IMPLEMENTATION.md`) と矛盾しない
  対応表・制限事項・保証 (conformance 12/12、fuzz 保証、並行性契約) を明記。
- **4-3. CI matrix 拡充** — linux/mac/windows x amd64/arm64 の通常 suite
  (opusref は既存 Ubuntu workflow のまま)。
- **4-4. semver / v1.0.0 タグ** — 公開 API の凍結宣言。破壊的変更が要るなら
  このタイミングで最後に整理 (エラー値・命名の一貫性チェックを 1 iteration で)。

判定基準: 機械的に検証できるものはツールで (`go vet`, lint, link check)。
v1.0 タグ発行は**ユーザーの明示承認必須**。

---

## 凍結棚 (再開条件付き)

- **encoder 品質 parity (music/CELT セル、bit-exact)**: scoreboard で大負けは
  music/CELT のみ・speech は互角。**再開条件 = 実利用からの品質報告、または
  Phase 1-4 完了後に余力がある場合のみ**。
- **D-2 候補 3-5 (SILK internal rate / stereo width / overhead 補正)**: iter1 棄却の
  実績どおり期待値低。再開条件 = scoreboard で対象セルの具体的な負けが示されたとき。
- **DRED / QEXT の DSP 実装**: transport のみ対応を docs に明記済み。見送り確定。
- **NSQ/gain co-optimization 大改修**: find_LPC_FLP の結論どおり期待値低。

## 進行ルール

1. 着手順: **0 → 1 → 2 → 3 → 4**。ただし Phase 1 と 2 は独立なので、
   レビュー待ちに他 Phase の小粒 iteration を挟むのは可。
   Phase 3 の最適化 iteration だけは harness (3-1) 完成前の着手禁止。
2. Phase ごとに統合 branch (`codex/feature-gaps` など) + 一括 PR。main 直 push しない。
3. 全 PR 前に PowerShell で `go test ./...` + `go test -tags opusref ./...`。
4. Codex への指示書は Phase 着手時に `.claude/tasks/<type>/<topic>.md` として切り出す。
5. Phase 完了ごとに本ファイルのステータスへ ✅ + commit hash を追記し、
   memory (`MEMORY.md` / `project_pure_go_roadmap.md`) を更新する。

## ステータス

- [x] Phase 0: D-2 クローズアウト — iter2 採用・D-2 終結 (`0f17758`, 2026-07-16)
- [x] Phase 1: 機能ギャップ (1-1〜1-5 完了、最終レビュー修正 `85eb9bb`, 2026-07-16。optional 1-6 は承認待ちのため未着手)
- [x] Phase 2: 堅牢性 — 2-1〜2-6 完了、PR #22 で main マージ済 (`60cb602`, 2026-07-17)
- [ ] Phase 3: 性能 (3-1 harness, 3-2 NSQ restore, 3-3 noise-shape scratch, and 3-4 noise-shape buffer optimizations qualified locally)
- [ ] Phase 4: リリース品質 (godoc / README / CI matrix / v1.0)
