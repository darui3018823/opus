# Opus のメモリ効率・実行性能監査
Date: 2026/11/22
Author: ChatGPT-6.2-Sol with high effort

---

## 結論

- **生成時メモリで最優先なのは CELT デコーダの遅延生成。** 試作で48 kHz stereo の生成が 1,439,393 → 95,264 B、5.1ch の生成が 5,757,721 → 381,208 B。stereo CELT を64パケット復号した後の保持ヒープも、1個あたり 1,525,620 → 257,789 B に減った。マルチストリーム FEC は毎回子デコーダを複製するため、同じ原因が復元時にも増幅される。
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
| API-01 | P1 | **CELT デコーダを必要なときに生成する。** 今は帯域4種 × LM4種 × ch2種を先に用意し、PostFilter の 2,048 要素の float64 履歴だけで計 768 KiB。通常・hybrid・PLC・FEC の選択は出力 ch 数が基準なので、まず未使用の出力 ch 軸と全形状の先行生成を除く。初回フレームの p99 と使用形状数に応じた常駐量を別測定。mono の既定位相設定、全帯域・LM 切替、最初の PLC、冗長の状態を検証。解析フックを既存の32形状へ登録するオラクルは、遅延生成時にも登録できる入口へ移す。 [生成](../../../opus.go#L3444) · [CELT 履歴](../../../internal/celt/decoder.go#L183) · [オラクル登録](../../../celt_scalar_oracle_scan_test.go#L133) |
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
| API-19 | P2 | **符号化の低レート汎用 resampler を Legacy CELT の初回利用まで生成しない。** 非48 kHz では既定の libopus ポリシーでも汎用 resampler を先に用意するが、同ポリシーの CELT は zero-stuffing を使う。Legacy の初回パケット、チャネル構成、方針切替による Reset を検証。固定アプリで使用しない codec 状態を後から作る案は CTL と prefill の参照を洗い出して別評価。 [生成](../../../opus.go#L391) · [CELT 入力](../../../opus.go#L2444) · [方針切替](../../../encoder_mode_policy.go#L499) |

### 生成・float32 入力の実測

| 対象 | 条件 | µs/op | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| `NewDecoder(48000, 2)` | 現行 | 553.0 | 1,439,393 | 3,230 |
| `NewSurroundDecoder(48000, 6, 1)` | 現行、子4個 | 2,551.6 | 5,757,721 | 12,925 |
| `EncodeFloat32` | 現行、48 kHz・20 ms・stereo CELT | 190.5 | 19,388 | 18 |
| `EncodeFloat32` | `inputScratch` の試作、同入力 | 173.9 | 3,003 | 17 |

生成は `-benchtime=250ms -count=5 -cpu=1`。生成した値をパッケージ変数に保存し、毎 op の生成コストを計った。float32 は既定ポリシーの stereo CELT ベンチと同じ 64 フレームを事前に float32 化し、`EncodeFloat32` を計測する。Go の `-overlay` で `floatPCM := make([]float64, expectedSize)` を `floatPCM := e.inputScratch(expectedSize)` に置換した試作と、変更前を別のテストバイナリにして交互に計測した。timing は Windows 上の負荷・ターボ状態を固定していない。Ogg などの独立測定と時間差を直接比較しない。 [ベンチ入力](../../../perf_libopus_policy_test.go#L23)

API-01 は eager CELT 生成だけを除き、出力 ch・帯域・LM の最初の要求で生成する Go overlay を作った。clone は使用済み形状から複製し、SILK の初期化と冗長フレームは現行のまま。200 ms を5組交互に測定した中央値。試作は公式ベクトルのダイジェスト・phase inversion・選択した PLC/FEC テストを通過した。オラクルのフック登録移行、全形状通過後の常駐量、初回生成の p99 は今後の実装で検証する。

| 生成・初回経路 | µs/op 変更前 → 後 | B/op 変更前 → 後 | allocs/op |
| --- | ---: | ---: | ---: |
| 48 kHz stereo の生成 | 532.1 → 257.6 | 1,439,393 → 95,264 | 3,230 → 94 |
| 5.1ch サラウンドの生成 | 2,256.2 → 1,018.9 | 5,757,721 → 381,208 | 12,925 → 381 |
| 48 kHz stereo の生成と初回 CELT 復号 | 619.0 → 359.1 | 1,525,424 → 242,712 | 3,249 → 227 |

同じ試作の保持ヒープは、`NewDecoder(48000, 2)` を32個生存させ、GC 後の `HeapAlloc` と生成前の GC 後の基準値との差を個数で割った。出力 int16 バッファと生成済みの CELT 64パケットは計測前に準備した。3回の中央値で未使用時が 1,424,004 → 95,236 B/decoder、64パケット復号後が 1,525,620 → 257,789 B/decoder。単一の帯域・LM を使う測定なので、複数形状を使うアプリの保持量は使用した形状数に依存する。

## CELT・DSP・サラウンド

| ID | 優先 | 候補と検証条件 |
| --- | --- | --- |
| CELT-01 | P1 | **復号の coarse energy・TF・dynalloc を束ねて再利用。** TF と dynalloc を各 `[21]int` のローカル配列に、coarse energy を遅延確保するデコーダの scratch にする試作で、mono は 626 → 98 B/op、stereo は 807 → 103 B/op、両方 4 → 1 allocs/op。coarse energy はフックへ渡るため単純なローカル配列だと escape する。監査フックのスライスの有効期間と、有効帯域外をゼロにする処理を確認。 [復号](../../../internal/celt/decoder.go#L298) · [coarse energy](../../../internal/celt/quant_energy.go#L266) · [TF・dynalloc](../../../internal/celt/decoder.go#L1106) |
| CELT-02 | P1 | **tonality FFT の作業配列をスタックに置く。** 480点の `OpusFFTScaled32` に `[480]opusComplex`、より長い入力には現行経路を使う試作で、complexity 9 の 20 ms stereo が 8,321 → 4,225 B/op、20 → 19 allocs/op。アルゴリズムと蝶演算の順序を維持。 [FFT](../../../internal/dsp/fft_opus.go#L314) · [tonality](../../../internal/celt/tonality.go) |
| CELT-03 | P1 | **CELT の詳細トレースを必要時だけ収集。** `traceBandLogE` の複製や `In`、`Freq`、`OldBandE` などの保存が通常符号化でも実行される。SILK の capture 制御と同様にオラクルや診断の利用箇所に明示的に有効化を入れる。マルチストリームや短い prefill を通る診断も含める。 [トレース](../../../internal/celt/encoder.go#L1060) |
| CELT-04 | P2 | **coarse energy の intra/inter 試行用バッファを保持。** `oldEBandsIntra` と残差を最大 2×21 の領域にし、独立した coder snapshot に `CloneInto` を使う。単一路だけ評価する入力では不要な保存を避けられるか確認。tie、予算境界、entropy overrun の差分を検証。 [試行](../../../internal/celt/quant_energy_flp32.go#L150) |
| CELT-05 | P2 | **高 complexity の theta RDO で試行用の係数・fold・coder を再利用。** ステレオ帯域ごとに状態を複製する。coarse energy とは別の生存範囲を持つ scratch で、両候補と復元用を分離する。比較した合成入力では主要な割り当て元に出なかったため、RDO を実際に繰り返す音源で評価。 [theta RDO](../../../internal/celt/quant_pvq.go#L1301) |
| CELT-06 | P2 | **復号のバンド係数コピー、逆正規化、MDCT組立を統合。** float32 の量子化係数を float64 の Band.Coeffs に広げ、その後２段で走査する。上流の float32 値から既存と同じ丸めで MDCT 対象を作る。観測フック、clone、Loss/anti-collapse、チャネルと帯域の制約が参照する値を洗い出す。過去の残項目であり、現行の公式ベクトル profile でも `DenormalizeBands` が約1.9% フラット。 [復号処理](../../../internal/celt/decoder.go#L393) · [合成](../../../internal/celt/decoder.go#L813) |
| CELT-07 | P2 | **CWRS の pulse 生成と二乗和を一つの走査で処理。** `cwrsiLibopusInto` が全要素を必ず代入する有効な n/k では出力の前置 clear を省き、後段の正規化用の整数二乗和を生成時に得る。0/1 次元、K の上限、index の処理、collapse mask、加算の型を検証。 [CWRS](../../../internal/celt/pvq.go#L440) · [pulse の正規化](../../../internal/celt/quant_pvq.go#L150) |
| CELT-08 | P2 | **PVQ U 表を再配置して cache locality を測る。** 現行の 64×320×uint32 は 80 KiB。大半が飽和値なので、飽和域のランレングスを行範囲として表現し、CWRS のアクセス順に合わせた row offset を試す。予備集計では row 0 を除く上三角の非飽和値は 1,801。境界分岐の増加と旧表との CWRSi/符号化性能の両方を比べる。 [PVQ U 表](../../../internal/celt/pvq.go#L353) · [recurrence テスト](../../../internal/celt/pvq_table_test.go) |
| CELT-09 | P2 | **postfilter の履歴を毎フレーム全体移動する処理を避ける。** 2,048 要素を滑らせる代わりに、ring と連続ビューで現行の tap 参照順を保つ。履歴値が全経路で float32 に収まると確認できた場合だけ要素幅の縮小も試す。pitch/PLC の古い履歴、前後の gain/pitch のフェード、Reset と状態コピーを比較する。 [フィルタ履歴](../../../internal/celt/postfilter.go#L268) · [PLC の履歴参照](../../../internal/celt/plc.go#L28) |
| CELT-10 | P2 | **CELT PLC の雑音・周期合成に宛先付きの変換と作業領域を使う。** 雑音側の正規化・MDCT バッファ、周期側の励起・FIR・IIR を対象にする。tilt、loss の減衰、複数回の loss 後の復帰、hybrid の足し合わせを公式 PCM と照合。 [雑音](../../../internal/celt/plc.go#L174) · [周期合成](../../../internal/celt/plc.go#L243) |
| CELT-11 | P2 | **静的 MDCT の未使用 scratch と重複した回転表を減らす。** 960点の forward で静的分岐が読まない `fwdFC` と `fwdZ` が論理 15,360 B。generic のときだけ取得する。幾何ごとの同一ビットの MDCT 回転表と4計画の共通 FFT twiddle は不変データとして共有し、インスタンスの変更可能な scratch は個別に持つ。 [forward](../../../internal/dsp/mdct_celt.go#L255) · [MDCT 回転表](../../../internal/dsp/mdct_celt.go#L27) · [FFT 計画](../../../internal/dsp/fft_opus.go#L90) |
| CELT-12 | P2 | **pre-emphasis の結果を次段の overlap 付きバッファへ直接格納。** `frame.pe` の読み返し・全コピーを無くすと、20 ms stereo で論理 7,680 B の中間領域が不要になる。float32 の丸め位置、入力・出力 alias、チャンネルごとの記憶値を比較。 [pre-emphasis](../../../internal/celt/encoder.go#L578) |
| CELT-13 | P2 | **patch transient の短い帯域配列をスタックに置く。** `spreadOld` は有効帯域数で最大21。高 complexity の符号化 profile では alloc_space の 4.0%。状態や比較境界は維持。 [patch transient](../../../internal/celt/celt_analysis.go#L164) |
| CELT-14 | P3 | **prefilter で新旧 gain がゼロの場合のデータ移動を減らす。** raw 履歴と filtered overlap の必要分を更新しつつ、同じデータの前後コピーや scan が不要な範囲をプロファイルする。非ゼロからゼロへの fade、NaN、閾値近傍を比較。 [prefilter](../../../internal/celt/prefilter_flp32.go#L609) |
| CELT-15 | P3 | **計算量の大きい相関・拡散判断を独立した lag/要素方向で測る。** `pitchXcorr32` は lag ごとの加算順を固定したまま複数 lag を処理できる。拡散判断は x²×N の個別計算と閾値の整数カウントを測定する。浮動小数点の加算順、FMA、NaN の比較則を維持し、complexity 9/10 の amd64 と arm64 を別計測。 [相関](../../../internal/celt/prefilter_flp32.go#L15) · [拡散判断](../../../internal/celt/analysis_flp32.go#L582) |
| CELT-16 | P1 | **サラウンド分析の履歴コピーと MDCT 一時出力を再利用。** `Clone` した分析器で毎回演算し、成功後に commit する。履歴のみを別バンクへ移し、作業領域を共有または固定化する。関数内の `CLTMDCTForward` とコピーは `CLTMDCTForwardInto` の行先を指定し、エネルギーマスクの子別一時領域を再利用する。失敗後に次の正常パケットを入力した時の分析履歴を比較。 [分析の staging](../../../surround.go#L241) · [MDCT と集計](../../../internal/celt/surround_analysis.go#L65) |
| CELT-17 | P2 | **モノのスペクトルを読むだけの stereo 合成で共有する。** 現在はチャネルごとに係数を複製する。IMDCT の読取り専用入力を2回使い、出力と overlap は現行どおり独立にする。短ブロックの overlap コピーと stride 付き集約も別実験にする。 [合成](../../../internal/celt/decoder.go#L451) · [短ブロック](../../../internal/celt/decoder.go#L487) · [IMDCT](../../../internal/dsp/mdct_celt.go#L378) |
| CELT-18 | P2 | **fold 用 scratch の容量と初期化を狭める。** `M*100` 要素のうち初期化後に必要な範囲を、帯域幅の最大と最後の帯域で検証する。LM3 で800→176要素にできれば論理 2,496 B 減。hybrid、theta 試行、上下帯域の fold を検証。 [fold scratch](../../../internal/celt/quant_pvq.go#L1177) |
| CELT-19 | P3 | **割り当ての Q3→pulse を小さな逆引き表にする。** 6段の検索に対し、ユニークな行と最大ブレークポイントのみを表にする。全域・全 LM の等価性を照合し、表の常駐量と CPU を一緒に測る。 [pulse cache](../../../internal/celt/rate_alloc.go#L885) |
| CELT-20 | P3 | **FFT の段情報と anti-collapse の不変・未使用計算を調べる。** stride/length を計画生成時に計算し、同じ蝶演算順を維持。anti-collapse は全ブロックが存在している帯域で exp/sqrt が使われるか、固定幾何で正規化係数を先に用意できるか検証する。 [FFT 段](../../../internal/dsp/fft_opus.go#L162) · [anti-collapse](../../../internal/celt/decoder.go#L927) |

| 試作 | µs/op 変更前 → 後 | B/op 変更前 → 後 | allocs/op |
| --- | ---: | ---: | ---: |
| CELT-01、`BenchmarkPerf/decode/celt` mono | 36.0 → 34.6 | 626 → 98 | 4 → 1 |
| CELT-01、同 stereo | 65.8 → 61.5 | 807 → 103 | 4 → 1 |
| CELT-02、既定ポリシー 128 kbps stereo VBR、complexity 9 | 200.3 → 196.0 | 8,321 → 4,225 | 20 → 19 |
| CELT-07、CWRS のクリア省略と pulse 二乗和、20 ms stereo | 59.2 → 56.8 | 変更なし | 変更なし |
| CELT-16、MDCT の行先指定のみ、5.1ch/320 kbps | 702.9 → 699.8 | 135,113 → 85,949 | 94 → 88 |

CELT-01 は 750 ms、CELT-02・CELT-07・CELT-16 は 500 ms の5組の交互測定の中央値。CELT-01 は `TestOfficialVectorDecodeDigests`、`TestPerfPredictivePacketRegression`、`./internal/celt` が通過。CELT-02 は128パケットと FinalRange の SHA-256 が試作前後で一致し、既定ポリシーのダイジェストと `./internal/dsp ./internal/celt` が通過。CELT-07 は `./internal/celt` と公式ベクトルのダイジェストが通過。CELT-16 の 48 kHz・20 ms の試作は `TestSurround*` と `TestVorbisSurround*` が通過した。時間差はこの環境・入力の目安。特に CELT-16 は割り当て削減が主な確認結果。

## SILK・再標本化

| ID | 優先 | 候補と検証条件 |
| --- | --- | --- |
| SILK-01 | P1 | **NLSF 量子化の残差・重み・候補を固定長の作業領域に移す。** 入力次数と survivor 数の上限を使い、候補が勝った場合だけ出力へコピー。Top-K を直接維持する場合は等スコアで元の低いインデックスを残す。試作で 2.1〜4.4 KB/op、17〜35 allocs/op 削減。浮動小数点の速度効果は測定窓によって変化した。 [NLSF 探索](../../../internal/silk/nlsf_encode.go#L112) |
| SILK-02 | P1 | **ステレオ符号化の L/R 取り出し、M/S 変換、再拡張での確保を減らす。** channel stride を受ける front-end と、`lrToMS` / `int16FrameToFloat` の宛先付き版を使う。WB・20 ms の L/R float64 と M/S float64 だけで各 5,120 B。フレーム列を保持する領域と1フレームだけの作業領域を分ける。LBRR、side-skip、prefill、幅と mono 遷移を比較。 [ステレオ符号化](../../../internal/silk/encoder.go#L657) · [M/S](../../../internal/silk/stereo_pred.go#L47) |
| SILK-03 | P1 | **使われていない復号用 resampler を生成・複製しない。** `silkRateInfo` の generic と固定点 resampler 群は構築・clone・Reset でのみ参照され、再標本化は物理 ch の `Decoder.silkRS` を使う。48 kHz stereo 生成の試作で 48,000 B と 51 allocs 減。別途、生成経路を持たない `celtResampler` と使用側の分岐も調べる。 [生成](../../../opus.go#L3483) · [実際の resampler](../../../opus.go#L4996) · [clone](../../../opus.go#L5909) |
| SILK-04 | P2 | **gain・pitch・LTP の 2/4 subframe 配列を処理単位で再利用。** `silkGainsQuant` と試行ごとの `gainQ16` を固定長にし、voiced 時は使わない既定 LTP 配列の確保を省く。再試行で下限候補、LBRR、最終履歴が alias しないことを確認。 `silkGainsQuant` は符号化プロファイルで約10%の alloc_objects。 [gain](../../../internal/silk/process_gains_flp32.go#L22) · [レート制御](../../../internal/silk/encode_frame_loop.go#L99) |
| SILK-05 | P2 | **voiced LTP の作業領域と係数レイアウトを平坦にする。** 行列 scratch、フレーム内で使い回す結果、`[][]float64` の Q14→FLP 変換を `[4][5]` か読取り時の同値変換へ。量子化結果の再計算を省く実装は既にあるため、結果の所有権とコピーに絞る。オラクルとの中間値、最終コードブックの残差エネルギーを比較。 [LTP VQ](../../../internal/silk/ltp_quant.go#L215) · [LPC 入力](../../../internal/silk/lpc_in_pre.go#L147) |
| SILK-06 | P2 | **LBRR の前後パケットで作業領域を交換する。** 最大3フレーム/ch のパルスとインデックスを別バンクに保有し、送信する旧 LBRR と生成中の新 LBRR を分ける。DTX の無音 carrier、Reset、連続 FEC、LBRR 抑止時に読まれる世代を比較。 [LBRR](../../../internal/silk/lbrr.go#L159) |
| SILK-07 | P2 | **PLC と CNG の励起・合成出力を再利用。** 過去の通常復号の scratch 化と同じ観点で PLC の作業配列を調べ、固定長の LPC predictor を共通化できるか確認。喪失の長さや PLC/CNG の glue、mono/stereo の状態を比較。 [PLC・CNG](../../../internal/silk/plc.go#L163) |
| SILK-08 | P2 | **合成の励起変換を減らす。** 元の励起を最初から `excQ14` に書き、voiced の LTP 加算結果は別の局所値にする。合成領域の clear は全読み込みが上書き後の場合だけ省く。元の励起を必要とする PLC/CNG・オラクルを優先して検証。 [合成](../../../internal/silk/decoder.go#L2492) |
| SILK-09 | P2 | **通常 SILK の int16 出力を短絡する。** 物理 ch の resampler 出力を float64 へ変換・インターリーブ後に float32、int16 へ変換する。gain=0、soft-clip memory=0、通常の SILK-only で飽和済みの出力から宛先へ直接書ける範囲を調べる。冗長 CELT、モード遷移、PLC/FEC、オーバーラン時は現行の順を使う。 [resampleSILK](../../../opus.go#L5018) · [インターリーブ](../../../opus.go#L5244) · [soft clip](../../../softclip.go#L131) |
| SILK-10 | P2 | **固定点 resampler の tap 配置と出力添字を調べる。** `iirFIRInterpol` は tv02/12 で 17.6%（フラット）。位相順に8 tap を並べ、使う入出力比ごとに Q16 の進み方と端数を証明した上でパターン化を試す。downFIR は18/24/36 固定窓と事前サイズ指定を別試作。過去に試した同じループの小変更は再実施しない。 [補間](../../../internal/silk/resampler.go#L388) · [downFIR](../../../internal/silk/resampler.go#L420) |
| SILK-11 | P2 | **Legacy 用汎用 resampler の channel と FIR の一時配列を減らす。** stereo の分離・結果の再インターリーブを宛先指定で扱い、係数を位相別に連続配置する。係数表を同一設定間で共有し、状態メモリは個別にする。Kaiser の分母計算のループ外移動も測る。非48 kHz の Legacy 往復、短い入力、分割境界と NaN/Inf で出力、フィルタ履歴を比較。 [係数](../../../internal/resampler/resampler.go#L125) · [処理](../../../internal/resampler/resampler.go#L210) · [呼出し](../../../opus.go#L2459) |
| SILK-12 | P2 | **pitch scratch の全域クリアと float64 保管を個別に減らす。** pitch の作業領域は既に再利用している。読み出しまでに全要素が書かれるものを洗い出し、NB で不要な 8 kHz 一時配列と、float32 に丸めた相関保存領域に絞る。積和の精度と順序は維持。 [pitch 探索](../../../internal/silk/pitch_core_flp32.go#L264) |
| SILK-13 | P2 | **Burg と LPC の結果用配列を渡す。** LPC の範囲内で固定長の Burg 結果、Q16 係数、P/Q 作業領域を使う。他の次数を受ける内部 API には既存の扱いを残す。短フレームの interp と predictor 閾値を比較。 [LPC](../../../internal/silk/find_lpc.go#L19) · [Burg](../../../internal/silk/burg.go#L18) |
| SILK-14 | P2 | **トレース停止時に残る診断用バッファを減らす。** `lastXBuf`、診断専用 NLSF target・stereo trace の消費側を明示 capture へ移してから書込を抑止する。エンコーダの実履歴や LBRR の配列を維持する。 [frame history と診断](../../../internal/silk/encoder.go#L1083) · [NLSF 診断](../../../internal/silk/encoder.go#L1972) |
| SILK-15 | P2 | **complexity 0/1 の plain NSQ に固定 scratch を適用。** delayed-decision 側の再利用は済んでいる。plain 側の int16/int32 変換、出力、整形作業領域を計測してから個別に再利用。再量子化の巻き戻しと共用集合、10/20 ms を比較。 [plain NSQ](../../../internal/silk/nsq_plain.go#L46) |
| SILK-16 | P3 | **delayed-decision の算術と反復を別々に計測する。** 同じ符号化結果でも candidate 選択・RNG・遅延履歴の更新がクリティカル。候補状態数1/2/3/4別にプロファイルし、独立候補間を対象に整数カーネルの専門化、または固定状態数でのスカラーループ展開を比較する。過去の境界チェック削減や copy 範囲短縮を前提とする。低 complexity と最高 complexity、voiced/unvoiced、packet budget と LBRR 条件のバイト一致を検証。 [delayed-decision NSQ](../../../internal/silk/nsq_del_dec.go#L229) |
| SILK-17 | P1 | **予備 NLSF 探索を fallback が必要な場合だけ行う。** 現在は noise-shape の入力準備前にフルの `analyzeNLSF` があり、実際の量子化用にもう一度実行する。予備 LPC を使うのは有効な pitch residual が足りない場合の推定経路。利用側の条件を使って遅延実行し、rate switch と短フレームを確認。SILK-01 との削減量には重複がある。 [予備・本解析](../../../internal/silk/encoder.go#L925) · [offset](../../../internal/silk/noise_shape.go#L244) · [LTP](../../../internal/silk/ltp_quant.go#L280) |
| SILK-18 | P2 | **同じ LPC 入力の正規化を一度にする。** `analyzeNLSF` と `exactProcessGains` が同じ frame config から LPC 入力と scale を組み立てる箇所がある。用途に必要な長さと lifetime を確認して結果を借用し、fallback とオラクル入力を比較。 [NLSF 内の組立](../../../internal/silk/encoder.go#L1942) · [gain 内の組立](../../../internal/silk/process_gains_flp32.go#L155) |
| SILK-19 | P2 | **旧 float LPC/NLSF/エネルギー履歴の未使用計算を除く。** `prevNLSF` へ書く radians 変換と `prevEnergy` へ書く全フレーム集約は、符号化結果に使う読み出しが見つからなかった。内部テストの診断経路を確認し、固定点の `prevNLSFQ15` と実際のエネルギーを使う処理を維持。 [書込み](../../../internal/silk/encoder.go#L1045) · [Reset](../../../internal/silk/encoder.go#L3156) |
| SILK-20 | P2 | **shell と binary iCDF の小さい復号を専門化。** 16要素 shell をパルスの出力先へ直接書き、ノードごとの entropy 順序を維持。符号・LSB の2要素 iCDF は実際の iCDF スケール `(rng>>8)*icdf0` で比較する。現行 `DecodeBitLogp` とは丸め則が異なる。12 kHz・10 ms の追加サンプルと破損時の range を照合。 [shell](../../../internal/silk/decoder.go#L2206) · [符号](../../../internal/silk/decoder.go#L2321) |

### SILK の試作結果

| ワークロード | B/op 変更前 → NLSF 作業配列のみ → 作業配列と Top-K | allocs/op |
| --- | ---: | ---: |
| SILK mono / 12 kbps | 6,912 → 5,643 → 4,727 | 107 → 96 → 90 |
| SILK stereo 入力 / 12 kbps | 6,781 → 5,534 → 4,641 | 105 → 94 → 88 |
| Hybrid mono / 24 kbps | 6,473 → 5,178 → 4,259 | 75 → 64 → 57 |
| Hybrid stereo / 36 kbps | 30,385 → 27,805 → 25,963 | 174 → 151 → 139 |

SILK-01 は `BenchmarkPerfDefault` の600 ms×3を交互に実行した第2セッションの中央値。既定と Legacy の64フレーム・パケット/FinalRange ダイジェストが通過した。CPU の時間差は2セッションで向きが変わったため未確定。SILK-03 の unused resampler 除去だけを試した場合、48 kHz stereo 生成は 1,439,393 → 1,391,393 B/op、3,230 → 3,179 allocs/op。16 kHz stereo は 1,425,521 → 1,391,393 B/op、3,217 → 3,179 allocs/op。公式ダイジェストと選択した packed FEC、復元失敗時の状態、PLC のテストが通過した。

SILK-02 のバッファ再利用のみの試作は 500 ms×3 の交互測定で、既定 hybrid stereo が 30,403 → 13,755 B/op、174 → 161 allocs/op、Legacy SILK stereo が 29,346 → 12,708 B/op、188 → 175 allocs/op。常駐 scratch が増えるため、stride 化やスタック利用は後続の余地。パケットダイジェストと対応する stereo/hybrid 系テストが通過した。

SILK-16 は候補を RD で比較した後に、採用する候補だけの励起・LPC/AR/LF 更新を計算する方式を試作した。昇格候補の中間値を seed/履歴と同じ候補から引き継いだ。既定と Legacy のダイジェスト、および `./internal/silk` が通過。600 ms×3 の中央値は SILK mono が 420.8 → 392.6 µs、hybrid stereo が 607.4 → 635.2 µs。複雑度と信号により符号化全体の速度差が分かれたため、候補選択の局所テストと広い実音源で再測定する。 [候補比較](../../../internal/silk/nsq_del_dec.go#L535)

SILK-17 は同じ600 ms×3の計測で、既定 SILK mono が 6,935 → 5,668 B/op、108 → 94 allocs/op、hybrid stereo が 30,373 → 27,839 B/op、174 → 146 allocs/op。4ワークロードで時間の中央値は約2.4〜6.7%減。両ポリシーのダイジェストと `./internal/silk` が通過した。SILK-10 の補間テーブル試作は約9.6 KiB の常駐表を追加して tv02 で 34.17 → 33.58 ms、tv12 で 38.54 → 38.26 ms（700 ms×3 の中央値）。PCM ダイジェストは一致し、時間差は未確定。適用時は短い位相周期や隣接出力の再利用を優先して比較する。

## Ogg コンテナ

| ID | 優先 | 候補と検証条件 |
| --- | --- | --- |
| OGG-01 | P1 | **CRC を 256 エントリの表へ。** 現行の1バイトあたり8段のビットループを、1 KiB の MSB-first 表へ置換。多項式 `0x04c11db7`、初期値0、最終 XOR なし、ヘッダの22〜25バイトをゼロとして計算。反射 CRC をそのまま置換に使うと互換性が変わる。 [CRC](../../../oggopus/page.go#L208) |
| OGG-02 | P1 | **`ReadPage` で読むバッファをそのままページに渡す。** header・laces・body から分割して CRC を計算し、読み込み時に所有した laces/body を返す。現在の連結用配列と `ParsePage` の再コピーを削る。短い read、CRC と version/flags のエラー順、`ParsePage` のコピー契約を比較。 [ReadPage](../../../oggopus/page.go#L168) |
| OGG-03 | P1 | **`SeekPCM` の二分探索を毎回収束させる。** 上限の分岐で `high = offset` としており、`offset == high` なら同じ midpoint と CRC/読み込みを最大64回繰り返す。 `scanNextPage(mid, end)` の最初の有効ページが `offset` なら、その間にページ開始はないため `high = mid` で探索区間を縮められる。target 前後、granule -1、複数 serial の link 境界、I/O エラー時の復帰を確認。 [link 末尾探索](../../../oggopus/seek.go#L107) · [ページ探索](../../../oggopus/seek.go#L153) |
| OGG-04 | P1 | **`SeekPCM` の 32 KiB 探索バッファを再利用。** `scanNextPage` ごとの確保を探査単位に移動。次に、読み取ったバッファで候補を検証し、位置と granule/serial/flags だけが要る段階では本文の所有コピーを避ける。分割された capture pattern、短い read、誤認した capture、pre-roll と chain を比較。キャッシュ済み link で毎回行う `SeekEnd` も別測定する。 [seek](../../../oggopus/seek.go#L39) · [走査](../../../oggopus/seek.go#L207) |
| OGG-05 | P1 | **`PacketReader` の1パケットを連続範囲としてコピーする。** lace 単位の `partial` 追記と完了時コピー、完了キューの再コピーを減らす。継続時の所有済みバッファを渡し、大きいパケットの backing array を空の scratch に保持し続けない。既読のキューポインタをクリア。0、255の倍数、孤立した continuation、16 MiB 制限、EOS 途中の sticky error とページ単位のエラーを検証。 [抽出](../../../oggopus/packet.go#L156) |
| OGG-06 | P2 | **上位 Reader の pending キューを使い回す。** Ogg Packet のページバッチからもう一度キューを増やして duration を再走査する。ヘッド添字と消費済みスロットのクリア、または確定したバッチの消費を試す。EOS trim、pre-skip、chain の切替、ページ途中のエラーと結果の所有権を維持。 [ページ処理](../../../oggopus/stream.go#L222) |
| OGG-07 | P2 | **`PacketWriter` の送出ページを再利用可能な連続領域にする。** 所有済みの laces/body を複製してから marshal が連結する。同期書込まで借用し、固定最大ヘッダ分を予約して1回の `writeAll` で送出する。短い write、途中継続、EOS の granule、およびページの再試行を照合。 [ページ送出](../../../oggopus/packet.go#L366) |
| OGG-08 | P2 | **`ParsePage` のコピーを検証後に移す。** CRC と version/flags/長さを現行順で検査し、成功時にだけ caller-owned な laces/body を確保する。コピーを1領域にまとめる案は、少量の laces だけを保持したときに本文も生存するため、常駐量と合わせて測る。 [ページ解析](../../../oggopus/page.go#L125) |
| OGG-09 | P3 | **OpusHead/Tags のデータ検証をコピー前に済ませる。** `ParseHead` の行列を含むレイアウト検証、`ParseTags` の UTF-8 と各サイズ検証を入力ビュー上で先に行う。コメントが多数ある例では文字列の backing をまとめる案も測る。抽出した短いコメントが大きなページを保持する場合や Extra の所有権も評価する。 [Head](../../../oggopus/metadata.go#L148) · [Tags](../../../oggopus/metadata.go#L247) |

### Ogg の試作結果

1ページ51,282 B（200 B パケット×255）、インメモリの Reader と `io.Discard`、150 ms×3、`-cpu=1` の中央値。全ての CRC 検証は有効。seek は200 B・TOC `0xf8`・960 sample 単位のパケットを64個ずつ128ページに格納し、最初に `SeekPCM(0)` で link 末尾を求めた後、内部位置への反復 seek を計測した。各試作を載せた Go overlay で `go test ./oggopus` が通過した。

| 対象 | 変更前 µs/op / B/op / allocs | 試作 µs/op / B/op / allocs |
| --- | ---: | ---: |
| `ReadPage` | 1,480.0 / 172,576 / 6 | CRC のみ 139.9 / 172,576 / 6 |
| `ReadPage` | 1,480.0 / 172,576 / 6 | CRC＋直接返却 123.5 / 57,632 / 3 |
| `Page.MarshalBinary` | 1,490.2 / 57,344 / 1 | CRC のみ 111.1 / 57,344 / 1 |
| `PacketReader` 255パケット | 1,497.0 / 266,784 / 264 | CRC＋直接返却＋連続抽出 143.0 / 131,152 / 259 |
| `PacketWriter` 4ページ | 5,919.4 / 約670,569 / 34 | CRC のみ 523.6 / 約670,569 / 34 |
| `SeekPCM` | 16,913.1 / 3,393,356 / 323 | CRC＋直接返却 1,604.3 / 2,168,643 / 187 |
| `SeekPCM`、上記に収束修正を追加 | 1,604.3 / 2,168,643 / 187 | 567.0 / 757,980 / 66 |
| `SeekPCM`、さらに探索 scratch を再利用 | 567.0 / 757,980 / 66 | 517.0 / 257,001 / 51 |

CRC 単体の同一ページの microbenchmark は 37.2 → 436.5 MB/s。大ページかつインメモリにおける結果で、実ファイルや小ページでは I/O・ページサイズにより比率が変わる。

## パケット・拡張・range coder

| ID | 優先 | 候補と検証条件 |
| --- | --- | --- |
| PKT-01 | P1 | **`Repacketizer.Out` を実際の出力長で確保。** 現在は上限値から cap を取り、201 B パケット1個の再出力で 65,536 B を確保する。TOC・長さ・padding を組み立てて正確な出力長を計算する一時試作は 208 B/op。返すスライスは所有権を維持。code 0〜3、拡張の出力範囲、padding、上限と整数境界を比較。 [outRange](../../../repacketizer_libopus.go#L235) · [容量](../../../repacketizer_libopus.go#L410) |
| PKT-02 | P1 | **`PacketExtensionsCount` を iterator で検証・カウントする。** 現在は Parse と同様に拡張 payload をコピーする。内部 iterator の span を Count では数えるだけにし、Parse では公開時に一度コピー。1,000 B 拡張の Count 試作は 1,096 → 24 B/op、3 → 1 allocs。残るフレーム参照の確保は解析統合の対象。repeat、last-long、malformed のエラーを比較。 [公開 Count/Parse](../../../extensions.go#L42) · [内部 Parse/Count](../../../internal/extensions/extensions.go#L145) |
| PKT-03 | P2 | **拡張生成を frame ごとの走査と複製に分けない。** 同期的な Generate の間は payload を借りられる。frame 安定順にエントリをまとめ、48 個の小さな固定表で repeat と padding を計画し、指定された出力長を一度確保する。任意の順序の入力、repeat、全 frame 範囲と既存の C オラクルと比較。 [公開 Generate](../../../extensions.go#L99) · [内部 Generate](../../../internal/extensions/extensions.go#L298) |
| PKT-04 | P2 | **Pad/Unpad の同期呼出しでフレーム参照を借用。** `Repacketizer.Cat` の長期保有用コピーを単発 Pad/Unpad でも実行している。単発処理では完了時に一度だけ所有出力を作る。マルチストリームは validation 後に各 stream を最終出力へ直接書く。入力の非破壊、opaque extension の保持、長さエラーと short padding を比較。 [Pad/Unpad](../../../repacketizer.go#L79) · [マルチストリーム](../../../repacketizer.go#L163) |
| PKT-05 | P2 | **`Repacketizer.Cat` が所有する参照を1パケットの backing にまとめる。** 現在は各 frame と padding を個別にコピーする。1回のコピーを区切る案は、OutRange 後や Reset で意図しないデータを保持しないか評価。入力の後編集、連続 Cat、Out の後編集が他の出力へ影響しないようにする。 [Cat](../../../repacketizer_libopus.go#L216) |
| PKT-06 | P2 | **通常／自区切り parsing のフレームレイアウトを共通化。** `PacketParse`、`packetParseImpl`、`packetExtensionLayout`、`PacketHasLBRR` には同一データの検証・分割が重なる。呼出し側が固定長の格納先を指定し、使う項目だけ書く。公開 `PacketParse` の借用 frame、per-frame 上限、duration 制限の API 差、padding の扱いを保持。 [PacketParse](../../../packet_parse.go#L12) · [共通化対象](../../../repacketizer_libopus.go#L48) · [LBRR](../../../packet_lbrr.go#L12) |
| PKT-07 | P2 | **code 0 の単一フレーム検査で48個の参照配列を作らない。** 現在 `inspectPacket` は code 0 でも 1,152 B をスタックで初期化することを逆アセンブルで確認。長さ範囲の直接検査で `PacketGetNumSamples` は 22.32 → 10.58 ns/op（両方0 alloc）。絶対時間は小さく、PKT-06 で同じ一経路にできるなら共通化を優先する。 [パケット検査](../../../packet.go#L149) |
| PKT-08 | P2 | **VBR/コード3の最終出力を一度で確保。** 現行初期 cap には payload の合計がない経路がある。フレーム長のエンコードも1〜2バイトを宛先へ直接書く。TOC や padding run、長さ251/252/1275、VBR/CBR の選択パターンを比較。 [VBR](../../../opus.go#L3688) · [コード3](../../../opus.go#L3821) |
| EC-01 | P1 | **range decoder の割り当てを減らす。** CELT-01 後の通常 CELT 復号に残る1回と、SILK/hybrid のコールサイトを対象にする。`NewDecoder` はコンパイラの今回の inlining budget を超える。初期化メソッドで短命の値をスタックに置くか、独立した作業領域を再利用。packet の参照を長期保持しないこと、SILK と hybrid の CELT が同じ range を読むこと、冗長フレームのネストを確認。 [range の生成](../../../internal/entcode/decoder.go#L29) · [SILK 使用例](../../../opus.go#L4466) |
| EC-02 | P2 | **range encoder の raw bits を出力先でまとめる。** 現状は coder の先頭、raw bits の末尾、完成 `Bytes` と外側パケットの領域を別々に持つ。まず `AppendBytes` 相当で最終パケットか中間 scratch に直接書き込む。逆方向に raw bits を書く単一バッファは Shrink、carry、raw-only、snapshots の差分をより広く検証してから。 [出力](../../../internal/entcode/encoder.go#L43) · [raw tail](../../../internal/entcode/encoder.go#L333) |
| EC-03 | P3 | **重複する整数除算を reuse する。** `DecodeUint` や `DecodeLaplace` で symbol の scale を `DecodeUpdate` へ渡す。amd64 で重複の DIVL を確認。1,000 symbols/op の試作は `DecodeUint` 10,383 → 10,125 ns、Laplace 25,379 → 25,032 ns（両方0 alloc、時間窓は重なる）。分母ゼロ対策と範囲の clamp、破損データでの consumed bits を維持し実信号で再評価。 [DecodeUpdate](../../../internal/entcode/decoder.go#L221) · [Laplace](../../../internal/entcode/laplace.go#L96) |
| EC-04 | P3 | **`TellFrac` の補正表を不変の共有値にする。** encoder/decoder に同じ8要素のリテラルがあり、この Go/amd64 ではインライン展開箇所で32 B のスタックコピーが発生した。表を共有し arithmetic を維持、既存の symmetry テストと実 codec のプロファイルで判断。 [encoder](../../../internal/entcode/encoder.go#L171) · [decoder](../../../internal/entcode/decoder.go#L75) |

PKT-01 の単一フレーム Out は 5,651 → 75.8 ns/op、65,536 → 208 B/op（1 alloc）。同じ試作の255 B `PacketPad` は 279.6 → 251.5 ns、`PacketUnpad` は 211.8 → 259.5 ns（616 → 568 B）。まず非 padding の出力長に絞ると測定結果から判断しやすい。PKT-02 は Count のみの試作で 1,005 → 894 ns/op。PKT-01/02/07 は関連 packet/extension のテストが、EC-03 は `./internal/entcode` が通過した。合成 microbenchmark の時間差を codec 全体の効果に換算しない。

## 実装順と比較条件

1. **生成と FEC。** API-01 と SILK-03 を独立して比較し、使用済み形状数ごとの保持ヒープと初回復号を記録する。その後に API-03 を、2〜8 子ストリーム、後段の子で失敗する FEC、実際に LBRR を復元する FEC、CELT の PLC fallback で測る。
2. **小さく切れる試作。** API-04、CELT-01/02、CELT-16 の MDCT 部分、PKT-01/02、OGG-01/02 を個別コミットで再検証する。大規模な state や公開シグネチャの変更を必要としない箇所から、効果の再現を確認できる。
3. **符号化の予測処理。** SILK-17 の有無で SILK-01 を再測定し、SILK-02 の scratch 分を別計上する。既定 complexity 5 に加え 0/1/9/10、低レートで mono に入る stereo 入力と真の stereo、DTX、LBRR、10〜120 ms を分ける。
4. **コンテナとストリームの構造。** OGG-03/04/05 の順に個別比較し、小ページ、大ページ、継続 metadata、chain、短い read で測る。API-05/07/09 は 5.1、7.1、Ambisonics 9/16/36 ch と duplicate/silent mapping で、仕事量が大きい箇所から確かめる。
5. **CPU の詰め。** CELT-07/08、SILK-09/10/16、EC-03/04 は一変更ずつ、codec 全体の profile と時間で判断する。Go 1.27.4 の amd64/arm64 で命令と bounds check を確認し、命令の選び方を変えても演算順と出力が維持されることを確認。計測は中間バッファ・解析削減を入れた後に採り直す。

### コマンドと合格条件

同一ツールチェーン・入力・CPU 条件でビルドしたベースと候補のバイナリを交互に測り、比較は `benchstat` か同等の集計で行う。数十 stream の GC 後保持ヒープ、短い stream の生成と初回、長い実ストリームの p50/p99、フレームあたりの B/op と allocs/op を用途別にとる。既存のベンチ CI は amd64/arm64 の短い smoke 測定なので、統計比較向けの反復測定を分ける。診断コマンドを長い vector の経時測定に使う場合、`cmd_diagamp` の毎パケット最大長出力バッファは先に再利用してアプリ側の割り当てを計測から外す。 [CI](../../../.github/workflows/bench.yml) · [診断コマンド](../../../cmd_diagamp/main.go#L15)

```text
go test -run '^$' -bench '^BenchmarkPerfDefault(DecodeFEC)?$' -benchmem -benchtime=300ms -count=5 -cpu=1 .
go test -run '^$' -bench '^BenchmarkDecodeOfficialVectors$' -benchmem -benchtime=1x -count=5 -cpu=1 .
go test -run '^$' -bench '^BenchmarkPerf$/^decode$/^celt$' -benchmem -benchtime=750ms -count=5 -cpu=1 .
go test -run 'TestOfficialVectorDecodeDigests|TestPerf(DefaultPolicy|Predictive)PacketRegression' -count=1 .
go test -count=1 ./...
go test -race ./...
```

pprof は `go test -c -o <tmp>/opus.test.exe .` でバイナリを作り、対象の `-test.bench` を絞って CPU と alloc-space を別実行する。たとえば CELT 復号は `^BenchmarkDecodeOfficialVectors$/^tv(01|09|10)$`。CPU は `-test.benchtime=1s -test.cpuprofile=<file>`、割り当ては `-test.benchtime=2x -test.memprofile=<file>`。生成・解析の計測を切り離す際は、固定モードの `BenchmarkPerf/decode/celt` も併用する。

codec を変更するときは packet bytes と各 packet の FinalRange、各対応レート・ch の PCM と FinalRange、次パケットの状態まで現行と比較する。状態コピー、float32 化、解析 pass 省略は特に Reset、帯域/LM/ch/モード切替、PLC/FEC、冗長フレーム、長い VBR/CBR でオラクルを追加する。 `opusref` の libopus 1.6.1 scalar、公式ベクトル、実音源、arm64 の既存 FMA チェックをコード採用時に適用する。 Ogg・packet は現行の CRC、エラー順、エラー時状態、所有権と byte-exact 差分を比較する。 [reference CI](../../../.github/workflows/opusref.yml) · [arm64 検証](../../../.github/workflows/test.yml) · [実音源](../../../docs/REAL_CORPUS_SCOREBOARD.md)

今回の監査では未改変コードに対する `go fmt ./...`、`go vet ./...`、CGO 無効の `go build ./...` と `go test -count=1 ./...`、CGO 有効の `go test -race ./...` が通過した。各試作は一時 Go overlay で検証した。フル `opusref`、arm64、実音源での全試作比較は未実施で、上の数値は実装の採否を決めるための基準とする。
