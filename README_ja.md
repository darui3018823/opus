# Pure Go Opus コーデック

[![Go Reference](https://pkg.go.dev/badge/github.com/darui3018823/opus.svg)](https://pkg.go.dev/github.com/darui3018823/opus)
[![Test](https://github.com/darui3018823/opus/actions/workflows/test.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/test.yml)
[![Race](https://github.com/darui3018823/opus/actions/workflows/race.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/race.yml)
[![Fuzz](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml)
[![License](https://img.shields.io/badge/license-BSD--2--Clause-blue.svg)](LICENSE)

日本語 | [English](README.md)

`github.com/darui3018823/opus` は、runtime CGO 依存のない、状態を持つ Pure Go
Opus コーデックライブラリです。single-stream、multistream、surround、
projection/Ambisonics、packet 変換、Ogg Opus API を提供します。

デコーダーはリポジトリの RMSE 基準で公式 RFC 8251 ベクター 12 本すべてに合格し、
libopus 1.6.1 と相互検証されています。エンコーダーは標準準拠の CELT、SILK-only、
hybrid packet を生成しますが、libopus と bit-exact ではなく、libopus のすべての
mode/rate/quality 判断を再現するものではありません。実装状況の正本は
[docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md) です。

## インストール

```bash
go get github.com/darui3018823/opus
```

現在の [`go.mod`](go.mod) は Go 1.24.11 を指定しています。

## 最小の encode/decode 例

```go
package main

import (
	"fmt"
	"log"

	"github.com/darui3018823/opus"
)

func main() {
	const channels = 2
	encoder, err := opus.NewEncoder(
		opus.SampleRate48kHz,
		channels,
		opus.ApplicationAudio,
	)
	if err != nil {
		log.Fatal(err)
	}

	// frameSize は 1 channel 当たりの sample 数。PCM は interleaved。
	pcm := make([]int16, opus.FrameSize20ms*channels)
	packet, err := encoder.Encode(pcm, opus.FrameSize20ms)
	if err != nil {
		log.Fatal(err)
	}

	decoder, err := opus.NewDecoder(opus.SampleRate48kHz, channels)
	if err != nil {
		log.Fatal(err)
	}
	decoded := make([]int16, opus.MaxFrameSize*channels)
	samplesPerChannel, err := decoder.Decode(packet, decoded)
	if err != nil {
		log.Fatal(err)
	}
	decoded = decoded[:samplesPerChannel*channels]
	fmt.Println(samplesPerChannel, len(decoded)) // 960 1920
}
```

encode、decode、multistream、Ogg Opus の実行可能な例は
[package documentation](https://pkg.go.dev/github.com/darui3018823/opus) に含まれます。

## 対応表

| 領域 | 対応状況 |
|---|---|
| サンプルレート | 8、12、16、24、48 kHz |
| single-stream channel | mono / stereo |
| PCM API | interleaved int16、signed 24-bit-in-int32、float32、float64 |
| packet duration | CELT 2.5/5/10 ms、20 ms の整数倍で 120 ms まで |
| coding mode | CELT encode/decode、voice 向け SILK-only/hybrid encode、SILK/hybrid decode |
| loss 対応 | CELT/SILK/hybrid PLC、SILK LBRR in-band FEC encode/decode |
| multistream/surround | RFC self-delimited framing、family 0/1（7.1 まで）/255、PLC/FEC |
| projection/Ambisonics | RFC 8486 family 2/3、family 3 の 1st〜5th order 定義済み matrix |
| packet 操作 | 検査、repacketize、padding、soft clip、LBRR 検出、extension |
| Ogg Opus | CRC/lacing、header/tag、timing trim、chain 読取り、link 単位 seek、single-link 書込み |
| runtime 依存 | Pure Go。CGO/libopus は `opusref` の任意テストのみ |

`MaxFrameSize` は 48 kHz で 1 channel 当たり 5760 sample（120 ms）です。
`MaxFrameBytes` は圧縮済み frame の 1275-byte 上限です。`MaxPacketSize` は
padding なし single-stream packet の保守的な上限であり、明示的な padding では
超える場合があります。

## 状態とメモリの正しい扱い

encoder、decoder、multistream、surround、projection、repacketizer、Ogg
reader/writer は状態を保持し、同一 instance の並行利用はできません。論理 stream
ごとに instance を用意し、packet 順序を維持し、getter、control、child stream
access、seek、`Reset` を含む全操作を直列化してください。別 instance は並行利用
できます。初回利用後に instance を値コピーしないでください。

呼出し側が渡す PCM、packet、mapping、matrix、destination slice は、各 API の
記載範囲でのみ借用されます。codec method は input を call 中だけ借用し、返された
packet / PCM slice は呼出し側の所有です。Ogg reader/writer は instance の lifetime
中 `io.Reader` / `io.Writer` を借用しますが、close はしません。

`Reset` は codec 履歴と直近 packet の観測値を消去し、bitrate、application、
output gain、phase-inversion などの設定は保持します。同じ設定の新しい stream に
再利用するか、必要な control を明示的に再設定してください。

## 準拠性と参照テスト

CI は未改変の RFC 8251 vector archive を取得し、公式 decoder vector 12 本を
すべて実行します。vector data は commit されないため、ローカルの
`testdata/opus_newvectors/` がなければ該当テストは skip します。

```bash
go test -count=1 ./...
go test -count=1 -run TestOfficialVectors .
```

任意の `opusref` テストには C toolchain と libopus が必要です。decoder output、
相互運用、final range、FEC/PLC、multistream、projection、選択した encoder quality
を libopus 1.6.1 と比較します。これは参照テストであり runtime 依存ではありません。

```bash
go test -count=1 -tags opusref ./...
```

## 入力安全性と fuzzing

公開 decoder と packet/container parser は、不正入力で panic せず error を返す設計
です。状態を持つ decoder sequence、PLC/FEC の混在、encoder control と極端な PCM
（浮動小数点の edge case を含む）、packet extension、multistream framing、
repacketizer、Ogg Reader/Writer round trip に専用 fuzz target があります。CI は
Linux amd64 / arm64 で全 target を nightly 実行します。

これは境界付き API の保証であり、fuzzing が正しさを証明するという意味では
ありません。呼出し側は packet、stream、CPU、memory の application-level budget
を制限してください。fuzz coverage は音質、timing の可用性、あらゆる DoS pattern
への耐性を保証しません。security issue は public issue ではなく
[SECURITY.md](SECURITY.md) の手順で報告してください。

ローカル実行例:

```bash
go test -run='^$' -fuzz='^FuzzDecoderSequence$' -fuzztime=60s .
go test -run='^$' -fuzz='^FuzzOggOpusReaderWriter$' -fuzztime=60s ./oggopus
```

## 現在の制限

- encoder output は標準準拠ですが libopus と bit-exact ではありません。
- SILK/hybrid encode は voice 向けであり、libopus の全 mode boundary、rate-control
  判断、quality heuristic は実装していません。
- DRED/QEXT packet extension は opaque に搬送するだけで、codec/DSP は未実装です。
- projection family 3 は libopus 1.6.1 の定義済み matrix を使い、任意の custom
  encoder matrix 生成は提供しません。
- Ogg Opus reader は chained logical stream に対応しますが multiplexed physical
  stream の demux はしません。各 Writer は 1 logical stream を出力します。
- libopus のすべての single-stream / multistream CTL に公開 equivalent があるわけ
  ではありません。`SetLSBDepth` は互換 hint として保持しますが、現在 codec 判断
  には影響しません。

## 開発

```bash
go generate ./...
git diff --exit-code
go fmt ./...
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go test -run='^$' -bench '^BenchmarkPerf/' -benchmem .
```

通常 CI は Linux amd64 と native Linux arm64 で test します。`opusref` workflow は
libopus を利用する Ubuntu に限定しています。正確な現行 matrix は workflow file
を参照してください。

## ドキュメント

- [Go API reference](https://pkg.go.dev/github.com/darui3018823/opus)
- [実装状況の正本](docs/CURRENT_IMPLEMENTATION.md)
- [CTL / helper 対応表](docs/CTL_PARITY.md)
- [performance baseline と benchmark 方法](docs/PERF_BASELINE.md)
- [architecture](docs/ARCHITECTURE.md)
- [mode/rate policy 差分](docs/MODE_RATE_POLICY_DIFF.md)
- [real-corpus scoreboard](docs/REAL_CORPUS_SCOREBOARD.md)
- [developer guide](docs/DEVELOPER.md)
- [security policy](SECURITY.md)
- [contribution guide](CONTRIBUTING.md)

## ライセンス

BSD 2-Clause License。詳細は [LICENSE](LICENSE) を参照してください。
