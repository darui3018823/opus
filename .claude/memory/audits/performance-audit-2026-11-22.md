# Opus のメモリ効率・実行性能監査
Date: 2026/11/22
Author: ChatGPT-6.2-Sol with high effort

---

## 結論

- **生成時メモリで最優先なのは CELT デコーダの遅延生成。** 48 kHz ステレオの `NewDecoder` は 1,439,393 B、3,230 allocs、5.1ch の `NewSurroundDecoder` は 5,757,721 B、12,925 allocs を生成ごとに割り当てる。出力 ch 数と使われた形状だけを生成する変更から着手する。マルチストリーム FEC は毎回子デコーダを再生成するため、同じ原因が復元時にも増幅される。
- **小さな変更から始めるなら `EncodeFloat32` の変換バッファ再利用。** 既存の `Encode` と同じ `inputScratch` を使う一行の試作で、48 kHz・20 ms ステレオ CELT が 19,388 → 3,003 B/op、18 → 17 allocs/op。6 組の交互測定で時間の中央値は 190.5 → 173.9 µs/op だった。
- **Ogg は CRC とページのコピー削減を先行させる。** 約 50 KiB のページでテーブル CRC と分割読み込みの試作を合わせると、`ReadPage` が 1,480 → 124 µs、172,576 → 57,632 B/op。51,282 B のページを対象とした変更前後の実測。
- **定常処理は CELT の復号時割り当てと SILK の符号化・再標本化を狙う。** CELT は coarse energy、TF、dynalloc、range decoder の順に割り当てを追う。SILK は NLSF 探索とステレオ入力の割り当てを削り、CPU は NSQ と補間器をプロファイルしながら個別に試す。

## 対象と評価方法

監査時点のコミットは `e8671704ee703fdd0ebcdebfa87695f2fb2b1be0`。公開 API、単一ストリームの方針決定・入力調整・パケット化、マルチストリーム・サラウンド・投影、CELT・SILK・DSP・resampler・range coder、Opus フレーミング・拡張・Ogg を対象にした。参照 C ラッパーと診断コマンドは測定・互換性検証の入口として確認した。実装の状態は現行コードおよび実装スナップショットを根拠にした。 [実装スナップショット](../../../docs/CURRENT_IMPLEMENTATION.md)

過去の計測では PVQ の `sync.Map` の廃止、静的 CELT MDCT、定常復号の PCM scratch、SILK のレート制御スナップショット、丸めを維持した float32 化、`Encode`/`Encode24` の変換 scratch などがすでに導入されている。今回の候補は残存箇所と未着手の新しい経路を中心にした。過去に効果が出なかった postfilter の単純な二分割、NSQ の追加の境界チェック除去、CELT 回転のビュー化は再実施の優先度を下げる。 [2026-10-15 の最適化](../../tasks/implementation/performance-optimization-2026-10-15.md) · [C/Go プロファイル比較](../../tasks/investigation/plainc-parity-and-profile-2026-10-16.md) · [過去ベースライン](../../../docs/PERF_BASELINE.md)

以下の **P1** は先に実装候補にする項目、**P2** は対象ワークロードを絞って試す項目、**P3** は適用範囲や互換性との兼ね合いを測ってから判断する項目。論理サイズは配列要素の合計、B/op は実行中の累積ヒープ割り当て、pprof はサンプリング値を示す。常駐量を見積もる際は、ワークロードを終えた多数のインスタンスを生存させて GC 後のヒープも測る。

## 現行の測定値

Go 1.27.4、Windows/amd64、Intel Core i7-11700、`-cpu=1`。既定ポリシーの符号化は従来のコンストラクタ設定（complexity 5、CBR）からビットレートと信号ヒントを指定し、48 kHz・20 ms の合成 PCM を使う。値は `-benchtime=300ms -count=5` の中央値。12 kbps の stereo は入力 ch 数を表し、符号化される ch 数は方針決定に従う。LBRR 行は LBRR を持つパケットを含むストリームの通常の `Decode` を測る。 [ベンチマークの定義](../../../perf_libopus_policy_test.go)

| ワークロード | µs/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| CELT mono / 96 kbps 符号化 | 111.1 | 1,876 | 16 |
| CELT stereo / 128 kbps 符号化 | 166.8 | 2,991 | 17 |
| SILK mono / 12 kbps 符号化 | 456.4 | 6,877 | 105 |
| SILK stereo / 12 kbps 符号化 | 392.1 | 6,814 | 104 |
| Hybrid mono / 24 kbps 符号化 | 222.1 | 6,475 | 74 |
| Hybrid stereo / 36 kbps 符号化 | 585.6 | 30,591 | 173 |
| LBRR あり VoIP mono / 20 kbps 通常復号 | 25.3 | 1,473 | 4 |
| LBRR あり VoIP stereo / 24 kbps 通常復号 | 50.3 | 4,297 | 2 |
| LBRR あり VoIP mono / 32 kbps 通常復号 | 53.6 | 639 | 4 |

公式ベクトルは 48 kHz・ステレオ int16 出力で、1 op はデコーダ生成を含むベクトル全体。値は `-benchtime=1x -count=5` の中央値。各計測の入力パケット列は共通。 [ベンチマークの定義](../../../perf_vectors_test.go#L211)

| ベクトル | ms/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| tv01 | 157.14 | 6,395,864 | 26,178 |
| tv02 | 33.67 | 1,740,936 | 4,476 |
| tv03 | 34.30 | 1,739,896 | 4,288 |
| tv04 | 49.60 | 1,788,488 | 4,565 |
| tv05 | 95.05 | 3,129,368 | 11,489 |
| tv06 | 97.05 | 3,055,672 | 11,634 |
| tv07 | 79.06 | 4,775,368 | 21,462 |
| tv08 | 79.73 | 3,527,320 | 10,929 |
| tv09 | 124.88 | 3,410,768 | 11,880 |
| tv10 | 145.75 | 6,522,920 | 24,682 |
| tv11 | 103.05 | 3,139,784 | 9,654 |
| tv12 | 38.99 | 2,677,888 | 6,782 |

### プロファイル

- **CELT の比率が高い tv01/09/10** の CPU は 3.66 s のサンプル。フラットで `cwrsiLibopusInto` 6.3%、`celtPVQU` 5.2%、de-emphasis 4.4%、`computeAllocationShared` 3.3%。累積で `decodeBandCoeffs` 48.6%、`CLTMDCTBackwardInto` 15.0%。同じ3本の alloc_space では coarse energy 25.0%、TF 12.9%、dynalloc 8.6%、range decoder 7.7%。デコーダ生成 20.0%（累積）とテスト入力読み込み・解析の分も含む。
- **SILK mono/12 kbps、stereo 入力/12 kbps、hybrid stereo/36 kbps** の符号化は 3.65 s のサンプルで NSQ delayed-decision 15.9%（フラット）/43.8%（累積）、warped autocorrelation 6.0%、NLSF delayed-decision quantizer 3.8%、LPC analysis filter 2.5%。割り当ては NLSF 探索、ステレオフレーム staging と型変換、gain quantization に残る。
- **SILK tv02/12** の CPU は 5.41 s のサンプル。フラットで `iirFIRInterpol` 17.6%、`synthesize` 8.1%、`DecodeIcdf` 6.8%、`interleaveSILKOutInto` 6.3%、`softClipIsIdentity` 4.1%。復号後の int16 変換も累積で約 7.6%。alloc_space は未使用の CELT 形状を含む生成時割り当ての比率が高い。

## 公開 API・ストリーム制御

| ID | 優先 | 候補と検証条件 |
| --- | --- | --- |
| API-01 | P1 | **CELT デコーダを必要なときに生成する。** 今は帯域4種 × LM4種 × ch2種を先に用意し、PostFilter の 2,048 要素の float64 履歴だけで計 768 KiB。通常・hybrid・PLC・FEC の選択は出力 ch 数が基準なので、まず未使用の出力 ch 軸と全形状の先行生成を除く。初回フレームの p99 と使用形状数に応じた常駐量を別測定。mono の既定位相設定、設定変更後に新規作成した形状、全帯域・LM 切替、最初の PLC、冗長フレームの状態引き継ぎを検証。 [生成](../../../opus.go#L3444) · [CELT 履歴](../../../internal/celt/decoder.go#L183) · [選択](../../../opus.go#L4194) |
| API-02 | P2 | **帯域・LM が切り替わっても履歴を共有できる CELT 構造へ。** API-01 後も多形状を通る入力では同じ履歴が複製される。固定出力 ch の論理履歴と形状別 scratch を分ける。帯域を再拡大した場合の外側の logE、stereo/mono 遷移、PLC と postfilter の連続性を現行の `CopyStateFrom` と照合。 [状態コピー](../../../internal/celt/decoder.go#L623) · [帯域・LM 選択](../../../opus.go#L4194) |
| API-03 | P1 | **マルチストリーム FEC のトランザクションを軽くする。** 各子を `NewDecoder` から複製し、全子成功後に公開済みポインタの指す値を更新する。必要な論理状態だけを独立した staging へコピーし、再利用する場合は最大常駐コストも測る。CELT の `CopyAllStateFrom` の構造体代入は mode の変換 scratch を共有し得るため、コピー境界を明示する。後段の子が失敗した時の全子状態、FEC 前置 PLC、子ハンドルの同一性を検証。 [FEC](../../../multistream.go#L876) · [clone](../../../opus.go#L5860) · [CELT clone](../../../internal/celt/decoder.go#L721) |
| API-04 | P1 | **`EncodeFloat32` で `inputScratch` を使う。** 20 ms stereo の一行試作で 19,388 → 3,003 B/op（約84.5%減）、18 → 17 allocs/op。6組の 300 ms 交互測定で時間の中央値は約8.7%減。64 フレームの float64 入力とのパケット・FinalRange 比較、および既定と Legacy のパケットダイジェストが通過。固定 expert duration で長い呼出し入力を与えた場合の高水位も実装前に確認する。 [変換](../../../opus.go#L582) · [既存 scratch](../../../opus.go#L534) · [expert duration](../../../opus.go#L2141) |
| API-05 | P1 | **multistream の PCM 変換と抽出バッファを使い回す。** 3形式の公開変換、各子への `streamPCM`、projection の `streamAnalysis` を別用途ごとに再利用。48 kHz・20 ms・6ch の全 PCM float64 変換は論理 46,080 B、子への抽出を合計するとさらに同量。音声の実フレームと tonality の呼出し全長、投影前後の PCM、整数入力の精度・丸めを維持する。 [変換・Legacy](../../../multistream.go#L437) · [既定ポリシー](../../../multistream_libopus.go#L208) · [投影](../../../projection.go#L314) |
| API-06 | P2 | **mapping の逆引き・出力宛先表を生成時に作る。** 符号化は毎フレーム ch ごとに `findMappedChannel` で走査し、復号は子ごとに全出力 ch を走査する。構成済みの固定 mapping から最初の入力 ch と子ごとの全出力宛先を作成。重複する入力 mapping の先勝ち、重複出力、255 無音、未割当のエラーを維持。多数 ch で測る。 [符号化](../../../multistream_libopus.go#L217) · [復号](../../../multistream.go#L693) · [mapping](../../../multistream.go#L1032) |
| API-07 | P1 | **マルチストリームを一度だけ解析し、連結用の中間パケットを減らす。** `Decode`/`Decode24` と投影 API の長さ検査、実復号が自区切りの解析・パケット再構築を繰り返す。フレーム範囲・消費バイト数・duration を持つ内部表現を渡し、通常パケットへの再構築を省く。最後の子の payload も同期で読む間は借用可能。エラー順、子の状態更新開始点、padding と extension の扱いを比較する。 [子の分割](../../../multistream.go#L1102) · [投影の事前検査](../../../projection.go#L478) |
| API-08 | P2 | **マルチストリーム符号化の再パケット化を出力先に直接書く。** `msRepacketize` の検査と再分割を統合し、内部フレーム長情報から正しい長さで親出力に追記。コード 0〜3、長さの 251/252 境界、最終 CBR padding、100 ms の予約と MaxPacketSize、既存のパケット所有権を照合。 [再パケット化](../../../multistream_libopus.go#L280) |
| API-09 | P2 | **マルチストリームの整数・float32 出力の全 ch float64 一時配列を減らす。** 子ごとの PCM と mapping を利用して必要な形式へ変換するか、出力転送に使う scratch を再利用する。短い出力や後段の不正パケットでも既存の出力書込タイミングを維持。soft clip は子ごとの履歴、float/24-bit はそれぞれの変換則を守る。 [出力 mapping](../../../multistream.go#L693) · [int24](../../../multistream.go#L965) |
| API-10 | P2 | **projection の行列演算の行・列を局所化し、形式ごとに最終出力へ蓄積する。** フレームを外側に、列を内側の加算順にして同一フレームの出力を連続的に触る。Q15 列ビューを先に作り、float32 は出力へ、float64 は1フレームの float32 scratch から拡張する案。列の加算順、Float32 の積の丸め、整数の wrap と gain を維持。9/16/36 ch を測定。 [mix](../../../mapping_matrix.go#L93) · [demix](../../../projection.go#L635) |
| API-11 | P2 | **投影行列を一度だけコピーする。** `NewMappingMatrixFromBytes` と `subset` はそれぞれ所有済みの係数をもう一度 `NewMappingMatrix` で複製する。寸法と完全長を先に確認し、内部の所有済みバッファを採用。公開 `NewMappingMatrix` と公開 getter のコピー契約は維持。 [行列の生成](../../../mapping_matrix.go#L22) · [部分行列](../../../mapping_matrix.go#L82) |
| API-12 | P2 | **単一ストリームのパケット解析を1回にまとめる。** int16/int24 は長さ検査の `PacketGetNumSamples` と `decodePacketFloat` の `inspectPacket`、さらにモード別の frame split を通る。検証済み TOC・duration・frame 範囲を内部で引き回す。短いバッファと発見順が競合する不正パケットで、エラー・FinalRange・次回の状態を対象に検証。 [出力サイズ検査](../../../opus.go#L3585) · [復号の再解析](../../../opus.go#L4132) |
| API-13 | P2 | **PLC/FEC の組立バッファを通常復号と同様に再利用する。** FEC の前置 PLC と取れた SILK、複数 PLC 区間と短縮された CELT 出力の間で append/pad が重なる。専用の借用可能な出力か明示的な宛先を使い、公開 float 出力へ渡すときに所有権を移す。前置 PLC と遷移 PLC が互いの scratch を上書きしないようにする。 [PLC](../../../opus.go#L5336) · [FEC 前置処理](../../../opus.go#L5680) |
| API-14 | P2 | **冗長フレーム用の CELT デコーダを再利用する。** `decodeRedundancy` は短い CELT デコーダを新規生成する。1個の専用 context を適切にリセットし、`lastCeltDec` として残る trailing redundancy を書き換える前に状態を移す。両向きのモード遷移、5 ms クロスフェード、範囲値の XOR、8〜48 kHz を対象にする。 [冗長フレーム復号](../../../opus.go#L4688) |
| API-15 | P2 | **Legacy 帯域推定の FFT 計画と作業領域を再利用する。** モノ化、padding、スペクトル、power の配列と汎用 FFT の回転計算が残る。入力長の上限を使い、既存と同じ回転係数・演算順の計画と行先指定を試す。閾値前後の帯域ヒステリシス、sparsity、全サンプルレートを比較。 [帯域推定](../../../bandwidth_detect.go#L120) · [FFT](../../../internal/dsp/fft.go) |
| API-16 | P2 | **入力調整・固定多フレームの余分なコピーを削る。** 連続フレームと短い prefill の存続範囲を確認し、delay 加工の保存領域、固定配列で収まるフレーム参照、最終 TOC を含む一回のパケット確保を個別に試す。CELT/SILK/Hybrid、VBR/CBR、2.5〜120 ms、1バイトの PLC、expert duration の再送を比較。 [delay 加工](../../../opus.go#L2416) · [多フレーム](../../../encoder_multiframe.go#L27) · [prefill](../../../encoder_transition.go#L149) |
| API-17 | P3 | **PCM の呼出し先指定 API を検討する。** `DecodeFloat`/`DecodeFloat32` は caller-owned 出力を返すため1つの割り当てが必要。既存の共有内部出力を使って `DecodeFloatInto` 相当を設計すると、繰り返し呼出し側が回避できる。先に出力容量の検査、ゼロ長パケットの既存契約、出力の所有権、FEC/PLC/投影までの統一性を決める。 [既存 float API](../../../opus.go#L4055) · [float32 出力](../../../opus.go#L4301) |
| API-18 | P3 | **hot path の float32 と float64 の往復を境界ごとに調べる。** 入力 high-pass・delay・CELT pre-analysis を一度に変更するより、既に float32 に丸めたコンディショニング済みバッファなど局所から始める。raw float64 の Legacy・解析入力を含むため、数値表現を一律に変換せず、NaN/Inf、subnormal、大小振幅と閾値近傍を含めて比較。 [入力調整](../../../encoder_input.go#L153) · [CELT 入力](../../../opus.go#L2444) |

### 生成・float32 入力の実測

| 対象 | 条件 | µs/op | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| `NewDecoder(48000, 2)` | 現行 | 553.0 | 1,439,393 | 3,230 |
| `NewSurroundDecoder(48000, 6, 1)` | 現行、子4個 | 2,551.6 | 5,757,721 | 12,925 |
| `EncodeFloat32` | 現行、48 kHz・20 ms・stereo CELT | 190.5 | 19,388 | 18 |
| `EncodeFloat32` | `inputScratch` の試作、同入力 | 173.9 | 3,003 | 17 |

生成は `-benchtime=250ms -count=5 -cpu=1`。生成した値をパッケージ変数に保存し、毎 op の生成コストを計った。float32 は既定ポリシーの stereo CELT ベンチと同じ 64 フレームを事前に float32 化し、`EncodeFloat32` を計測する。Go の `-overlay` で `floatPCM := make([]float64, expectedSize)` を `floatPCM := e.inputScratch(expectedSize)` に置換した試作と、変更前を別のテストバイナリにして交互に計測した。timing は Windows 上の負荷・ターボ状態を固定していない。Ogg などの独立測定と時間差を直接比較しない。 [ベンチ入力](../../../perf_libopus_policy_test.go#L23)
