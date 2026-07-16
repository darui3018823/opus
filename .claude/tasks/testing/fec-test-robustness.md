# Codex タスク: SILK FEC テストの位置ハック撤去（堅牢化）

> **Status: completed on 2026-06-25.** Implemented by `a55d79b`; the tests now
> gate quality assertions on parsed LBRR presence. This brief is historical;
> do not execute it as a current task.

## ゴール
`opus_cgo_silk_fec_test.go` の `TestCGODecodeFECMatchesLibopus` にある脆い
位置ハードコード特別扱いを撤去し、「**実際に LBRR が積まれた subframe にだけ**
SNR 床を課す」形に書き換える。テストの主張を fixture 非依存にする。

## 背景（調査済み・前提として確定。再調査不要）
- 対象テストは Go の `DecodeFEC` 出力と libopus `decode_fec` 出力を、**同一の
  Go エンコード済みストリーム**上で比較する。`rate=16000, channels=1,
  bitrate=24000, nPackets=9, lost=5`。復元は `packets[lost+1]`（= packets[6]）。
- 現在 60ms ケースの subframe 0 が 0.31dB で、テストは
  `opus_cgo_silk_fec_test.go:372` の `if !(packetMs == 60 && f == 0) && frameSNR < 6`
  で**位置決め打ち**で例外にしている。
- 根本原因（確定済み）: fixture `silkRefSpeechFrame`（同ファイル ~634行）は 3Hz
  振幅エンベロープ付き有声トーン。その谷（t≈0.25–0.30s, env≈0.2）が packet 5 の
  frame 0 に重なり、その frame は **VAD で Inactive 分類**される。
- エンコーダは LBRR present ゲート（`internal/silk/lbrr.go:234`,
  `present = e.speechActivity > lbrrSpeechActivityThres && signalType != SignalTypeInactive`）
  で**正しく LBRR を省略**（libopus 同等セマンティクス）。実証: `OPUS_LBRR_DEBUG=1`
  で packets[6] のマスクは `0b110`（frame 0 が present=false, sig=0=Inactive）。
- デコード側は `internal/silk/decoder.go:490` の `decodeFECChannel` が、マスクの
  ビットが立っていない slot を `concealPacketLoss`（PLC）で補完。Go も libopus も
  同じマスクを読むので両者とも frame 0 を PLC。
- ⇒ 0.31dB は「LBRR 再構成の乖離」ではなく、**near-silent な Inactive frame 上の
  PLC 同士の差**という測定アーティファクト。SILK PLC は libopus と bit-exact では
  ないので、これは仕様上正常。**エンコーダ/デコーダ側の修正対象ではない。**

## やること
1. `opus_cgo_silk_fec_test.go:361-379` 付近の subframe ループを書き換える。
   - 位置ハック `packetMs == 60 && f == 0` を**削除**。
   - 代わりに「**その subframe の LBRR が復元パケットに present だったか**」を判定し、
     - present な subframe: 従来どおり `frameSNR >= 6` を課す（失敗メッセージは
       "present LBRR subframe %d diverged" のままで正しくなる）。
     - absent な subframe: SNR チェックを**スキップ**（位置に関係なく）。t.Logf で
       "subframe %d LBRR absent (PLC) — skipped" 等を残す。
2. present/absent の判定方法を実装する。**ここは調査・設計を任せる。**候補:
   - (a) Go の `DecodeFEC` 経路で読んだ LBRR マスクをテストから観測できるよう、
     `internal/silk` デコーダの trace（`d.trace.LBRRFlags` が
     `internal/silk/decoder.go:241` 周辺に存在）か、それに準ずる手段で公開する。
     ただし FEC 経路（`decodeFECChannel`）が trace を埋めているか要確認。埋めて
     いなければ最小限の観測フックを足す。
   - (b) テスト内で復元パケットのヘッダ＋LBRR マスクを直接パースして present 集合を
     得る。SILK 文法は `internal/silk/decoder.go` の `DecodeFEC` /
     `decodeFECChannel`（mono）参照。
   - どちらでもよいが、**プロダクションのデコード挙動を変えない**こと（観測の追加のみ）。
3. 既存の overall `snr >= 6`（`:377`）は維持。stereo/hybrid 側のテストには触らない。

## 制約・検証
- cgo を使うので**必ず PowerShell**で実行（Bash tool では libopus がリンクできない）。
- 検証コマンド:
  ```
  go test -count=1 -tags opusref -run '^TestCGODecodeFECMatchesLibopus$' -v .
  ```
  20ms/40ms は従来どおり全 subframe present で ≥6dB、60ms は absent slot を
  スキップして PASS することを確認。
- 併せて回帰確認:
  ```
  go vet ./...
  go test -count=1 ./...
  go test -count=1 -tags opusref -run 'FEC' -v .
  ```
- `OPUS_LBRR_DEBUG=1` を付けるとエンコーダが emit するマスクが stderr に出る
  （`internal/silk/lbrr.go:305` 付近）。present 判定の答え合わせに使える。

## やらないこと
- SILK PLC を libopus に近づける/bit-exact 化する作業は**対象外**。
- エンコーダの present ゲートやマスク生成ロジックの変更は**対象外**（正しく動作している）。
- 緩い 3dB 床（`:158/161/286`）の引き上げは**別タスク**。このタスクでは触らない。
