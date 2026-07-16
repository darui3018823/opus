# Pure Go Opus コーデック

[![Go Reference](https://pkg.go.dev/badge/github.com/darui3018823/opus.svg)](https://pkg.go.dev/github.com/darui3018823/opus)
[![Test](https://github.com/darui3018823/opus/actions/workflows/test.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/test.yml)
[![Race](https://github.com/darui3018823/opus/actions/workflows/race.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/race.yml)
[![Fuzz](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml)
[![License](https://img.shields.io/badge/license-BSD--2--Clause-blue.svg)](LICENSE)

日本語 | [English](README.md)

[Opus オーディオコーデック](https://opus-codec.org/)（RFC 6716 / RFC 8251）の
**ランタイム CGO 依存なし**の Pure Go 実装です。**デコーダー**は公式 RFC 8251
テストベクター 12 個すべてに合格し（RMSE < 0.001）、libopus 1.6.1 リファレンスと
フレーム単位で一致します。**エンコーダー**は CELT quality pipeline、限定的な
低ビットレート SILK-only 音声経路、初期 hybrid 音声経路を実装し、libopus で
デコード可能な標準 Opus パケットを出力します（[ステータス](#ステータス)参照）。

> 補足: エンコーダーは libopus のエンコーダーとはビット精度一致しません。
> CELT 経路は Pure Go での音声・音楽エンコードに利用できます。SILK 経路は
> voice向けに限定され、ハイブリッドエンコードは高ビットレートの
> 24/48 kHz voice packet に限定されています。

## ステータス

| 領域 | 状態 |
|------|------|
| **デコーダー** | ✅ 公式 RFC 8251 ベクター 12/12 合格（RMSE < 0.001）。libopus 1.6.1 と一致。SILK / CELT / ハイブリッド（SILK+CELT）を再構成済み（ハイブリッド SILK→CELT redundancy 含む）。 |
| **エンコーダー** | ✅ CELT quality pipeline（Phase 1+2）、低ビットレート voice 向け SILK-only、24/48 kHz 高ビットレート voice 向け初期 hybrid encode。libopus 1.6.1 がデコードできる標準 Opus packet を出力。64 kbps の目安 SNR: 440 Hz 約 48 dB、1 kHz 約 47 dB、stereo 約 43 dB。libopus とはビット精度一致**ではない**。 |
| **CGO** | ランタイム依存なし。libopus ラッパーは参照テスト専用で `opusref` ビルドタグ下にのみ存在。 |
| **CI** | `test` / `race` / `bench` / `fuzz` を **amd64・arm64** で実行。 |

正確な現状は [docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md)
（コードから導出したスナップショット）を参照してください。

## インストール

```bash
go get github.com/darui3018823/opus
```

Go 1.24.11 以降が必要です（`go.mod` 参照）。

## 使い方

### デコード（int16）

```go
package main

import (
	"log"

	"github.com/darui3018823/opus"
)

func main() {
	// 48kHz・ステレオ
	dec, err := opus.NewDecoder(48000, 2)
	if err != nil {
		log.Fatal(err)
	}

	// packet は 1 つの Opus パケット（ファイルやネットワークから取得）
	var packet []byte

	// デコード先 PCM バッファ。48kHz における最大フレーム 120ms は
	// 1 チャンネルあたり 5760 サンプル。想定フレームに応じて余裕を持たせる。
	pcm := make([]int16, 5760*2)

	n, err := dec.Decode(packet, pcm)
	if err != nil {
		log.Fatal(err)
	}
	// pcm[:n*2] にインターリーブされたステレオ（1ch あたり n サンプル）が入る。
	_ = n
}
```

### デコード（float64）

```go
// DecodeFloat は新規確保したインターリーブ []float64 を返す。
samples, err := dec.DecodeFloat(packet)
if err != nil {
	log.Fatal(err)
}
_ = samples
```

### エンコード

```go
enc, err := opus.NewEncoder(48000, 2, opus.ApplicationAudio)
if err != nil {
	log.Fatal(err)
}
enc.SetBitrate(128000)
enc.SetComplexity(10)
enc.SetVBR(true) // 可変ビットレート（既定は CBR）

// 20ms フレーム = 48kHz で 1ch あたり 960 サンプル（インターリーブステレオ）
pcm := make([]int16, 960*2)
// ... pcm を埋める ...

packet, err := enc.Encode(pcm, 960)
if err != nil {
	log.Fatal(err)
}
_ = packet

// float64 入力も可能:
//   packet, err := enc.EncodeFloat(make([]float64, 960*2), 960)
// float32 入力には EncodeFloat32 を使用できる。

// 帯域は信号内容から自動検出される。必要なら明示指定できる:
//   enc.SetBandwidth(opus.BandwidthWideband) // wideband に固定
//   enc.SetBandwidth(opus.BandwidthAuto)     // 自動選択へ戻す

// Application とは独立した信号種別ヒント:
//   enc.SetSignalType(opus.SignalVoice)

// 短い CELT packet と multi-frame packet を出力可能:
//   packet, err := enc.Encode(pcm480, 480)   // 48 kHz で 10ms
//   packet, err := enc.Encode(pcm1920, 1920) // 40ms
```

## サポート設定

- **サンプルレート**: 8 / 12 / 16 / 24 / 48 kHz。エンコーダーは非 48kHz 入力を
  内部で 48kHz にリサンプルし、デコーダーは出力を要求レートにリサンプルします。
- **チャンネル**: モノラル・ステレオ。
- **デコーダーのフレームサイズ**: 全 Opus 長（2.5/5/10/20/40/60 ms）を TOC バイトに
  従いパケット単位で選択。
- **エンコーダーのフレームサイズ**: CELT の 2.5/5/10ms、および 20ms の整数倍
  （20ms〜120ms）。
- **エンコーダー帯域**: 信号内容に基づく自動検出、または
  `SetBandwidth` / `SetMaxBandwidth` による明示指定。NB/WB/SWB/FB に対応。
- **エンコーダーモード選択**: 通常音声・音楽・restricted-low-delay、および
  hybridの有効範囲を超える高レート音声ではCELTを使います。voice境界は
  チャンネル数とLBRRを考慮し、SILK-onlyはmono 40 kbps、stereo 48 kbps
  までを基本とし、FEC有効時は範囲を拡張します。24/48 kHz音声は中間レートで
  hybridを使い、それ以上ではCELTへ戻ります。
- **アプリケーションタイプ**（帯域しきい値や transient 判定を調整）:
  - `opus.ApplicationVOIP` — voice 向け、狭めの帯域しきい値
  - `opus.ApplicationAudio` — music/general 向け
  - `opus.ApplicationRestrictedLowDelay`
- **信号種別ヒント**: `opus.SignalAuto` / `opus.SignalVoice` /
  `opus.SignalMusic`。ビットストリーム形式は変えず、エンコーダーの判断だけを調整します。

## 公開 API

公開バージョン定数はリポジトリの [`VERSION`](VERSION) から生成されます。

`MaxFrameSize` は48 kHz・1チャンネル当たり5760サンプル（120 ms）です。
`MaxFrameBytes` は圧縮済み1フレームの1275バイト上限、`MaxPacketSize` は
paddingなしsingle-streamパケット用の保守的な格納上限です。明示的なpacket
paddingを追加した場合はこれを超え得ます。

### エンコーダー

```go
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error)
func NewEncoderWithProfile(sampleRate, channels int, application Application, profile EncoderProfile) (*Encoder, error)

func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error)
func (e *Encoder) Encode24(pcm []int32, frameSize int) ([]byte, error)
func (e *Encoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error)
func (e *Encoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error)

func (e *Encoder) Bitrate() int
func (e *Encoder) EffectiveBitrate() int
func (e *Encoder) Complexity() int
func (e *Encoder) VBR() bool
func (e *Encoder) VBRConstraint() bool
func (e *Encoder) Application() Application
func (e *Encoder) SampleRate() int
func (e *Encoder) Channels() int
func (e *Encoder) Lookahead() int
func (e *Encoder) FinalRange() uint32
func (e *Encoder) InDTX() bool

func (e *Encoder) SetBitrate(bitrate int) error       // 6000–510000 bps
func (e *Encoder) SetComplexity(complexity int) error // 0–10
func (e *Encoder) SetVBR(vbr bool)
func (e *Encoder) SetVBRConstraint(constrained bool)  // true = CVBR
func (e *Encoder) SetApplication(application Application) error
func (e *Encoder) SetSignalType(signal SignalType)
func (e *Encoder) SignalType() SignalType
func (e *Encoder) SetBandwidth(bw int) error          // Auto/NB/WB/SWB/FB
func (e *Encoder) SetMaxBandwidth(bw int) error
func (e *Encoder) MaxBandwidth() int
func (e *Encoder) Bandwidth() int
func (e *Encoder) GetBandwidth() int
func (e *Encoder) SetDTX(dtx bool)
func (e *Encoder) DTX() bool
func (e *Encoder) SetInbandFEC(enabled bool)             // SILK-only/hybrid
func (e *Encoder) InbandFEC() bool
func (e *Encoder) SetPacketLossPerc(perc int)            // 0〜100 にクランプ
func (e *Encoder) PacketLossPerc() int
func (e *Encoder) SetPacketPadding(n int)
func (e *Encoder) SetForceChannels(channels int) error
func (e *Encoder) ForceChannels() int
func (e *Encoder) SetLSBDepth(depth int) error
func (e *Encoder) LSBDepth() int
func (e *Encoder) SetPredictionDisabled(disabled bool)
func (e *Encoder) PredictionDisabled() bool
func (e *Encoder) SetPhaseInversionDisabled(disabled bool)
func (e *Encoder) PhaseInversionDisabled() bool
func (e *Encoder) Reset() error
```

`NewEncoder` は従来の 64 kbit/s・complexity 5・CBR を維持します。
自動 bitrate・complexity 9・constrained VBR の既定値には
`EncoderProfileLibopus` を指定します。

### 並行利用と所有権

`Encoder` と `Decoder` は状態を保持し、同一インスタンスを複数 goroutine
から同時に利用することはできません。論理 Opus ストリームごとに1つの
インスタンスを作成し、パケット順序を維持してください。同一インスタンスの
getter、設定変更、`Reset` を含む全メソッドは、必要に応じて mutex などを使い
呼び出し側で直列化します。別々のインスタンスは並行して利用できます。

初回利用後の `Encoder` / `Decoder` を値コピーしないでください。エンコード・
デコードメソッドが呼び出し側の PCM、packet、出力先 slice を借用するのは
メソッドが戻るまでです。返された圧縮 packet と PCM slice の所有権は
呼び出し側にあります。

### デコーダー

```go
func NewDecoder(sampleRate, channels int) (*Decoder, error)

func (d *Decoder) Decode(data []byte, pcm []int16) (int, error)
func (d *Decoder) Decode24(data []byte, pcm []int32) (int, error)
func (d *Decoder) DecodeFloat(data []byte) ([]float64, error)
func (d *Decoder) DecodeFloat32(data []byte) ([]float32, error)
func (d *Decoder) DecodePLC(pcm []int16, frameSize int) (int, error) // CELT/SILK-only/hybrid PLC
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error)   // SILK LBRR
func (d *Decoder) Reset() error
func (d *Decoder) GetLastPacketDuration() int
func (d *Decoder) Bandwidth() int
func (d *Decoder) GetBandwidth() int
func (d *Decoder) SampleRate() int
func (d *Decoder) Channels() int
func (d *Decoder) FinalRange() uint32
func (d *Decoder) Pitch() int
func (d *Decoder) SetGain(gainQ8 int) error
func (d *Decoder) Gain() int
func (d *Decoder) SetPhaseInversionDisabled(disabled bool)
func (d *Decoder) PhaseInversionDisabled() bool
```

### マルチストリームとサラウンド

```go
func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application Application) (*MultistreamEncoder, error)
func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*MultistreamDecoder, error)

func NewSurroundEncoder(sampleRate, channels, mappingFamily int, application Application) (*SurroundEncoder, error)
func NewSurroundDecoder(sampleRate, channels, mappingFamily int) (*SurroundDecoder, error)
```

マルチストリーム packet は RFC 6716 の self-delimited framing を使用し、
libopus 1.6.1 との相互運用テストを通過しています。サラウンドは mapping
family 0、1（Vorbis順、最大7.1）、255に対応します。

### Projection と Ambisonics

```go
func NewProjectionEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error)
func NewProjectionDecoder(sampleRate, channels, streams, coupledStreams int, demixingMatrix []byte) (*ProjectionDecoder, error)
func NewAmbisonicsEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error)
func NewAmbisonicsDecoder(sampleRate, channels, mappingFamily, streams, coupledStreams int, mapping, demixingMatrix []byte) (*AmbisonicsDecoder, error)
```

RFC 8486 mapping family 2/3 に対応します。family 2 は ACN/SN3D Ambisonics
channel mapping、family 3 は libopus 1.6.1 の projection mixing/demixing
matrix を使用し、両 family とも libopus 相互運用テストがあります。

### パケット操作

```go
func NewRepacketizer() *Repacketizer
func (r *Repacketizer) Cat(packet []byte) error
func (r *Repacketizer) NumFrames() int
func (r *Repacketizer) Out() ([]byte, error)
func (r *Repacketizer) OutRange(begin, end int) ([]byte, error)
func PacketPad(packet []byte, newLen int) ([]byte, error)
func PacketUnpad(packet []byte) ([]byte, error)
func MultistreamPacketPad(packet []byte, streams, newLen int) ([]byte, error)
func MultistreamPacketUnpad(packet []byte, streams int) ([]byte, error)
func PacketHasLBRR(packet []byte) (bool, error)
func SoftClipFloat32(pcm []float32, channels int, mem []float32) error
func PacketExtensionsCount(packet []byte) (int, error)
func PacketExtensionsParse(packet []byte) ([]PacketExtension, error)
func PacketExtensionsGenerate(packet []byte, extensions []PacketExtension, paddingBytes int) ([]byte, error)
```

packet extension は code-3 padding 内で搬送します。DRED/QEXT payload は
opaque data として扱い、neural/DSP codec 自体は実装していません。
CTL/helper の対応状況は [docs/CTL_PARITY.md](docs/CTL_PARITY.md)、
SILK/hybrid policy の差分は
[docs/MODE_RATE_POLICY_DIFF.md](docs/MODE_RATE_POLICY_DIFF.md) を参照してください。

### Ogg Opus コンテナ

`github.com/darui3018823/opus/oggopus` package は、CRC 検証付き Ogg page
parse/write、packet continuation と lacing、`OpusHead`/`OpusTags` metadata、
単一 logical stream の Ogg Opus reader/writer を提供します。

## アーキテクチャ

```
github.com/darui3018823/opus/
├── opus.go / multistream.go / surround.go / projection.go
├── extensions.go / repacketizer.go         # packet 操作
├── oggopus/                                # Ogg page / Ogg Opus API
├── internal/
│   ├── opus_framing.go                  # TOC バイトの解析/生成（RFC 6716 §3）
│   ├── dsp/                             # FFT、MDCT/IMDCT、窓関数、数学
│   ├── entcode/                         # レンジエンコーダー/デコーダー
│   ├── resampler/                       # Opus レートのサンプルレート変換
│   ├── celt/                            # CELT エンコーダー/デコーダー
│   ├── silk/                            # SILK デコーダー/エンコーダー、テーブル、補助
│   └── cgoref/                          # libopus 参照ラッパー（ビルドタグ: opusref）
└── docs/                                # 設計・ステータス文書
```

**デコードフロー**: Opus パケット → TOC 解析 → CELT または SILK/ハイブリッド経路 →
レンジデコード + 再構成 → 必要に応じてリサンプル/チャンネル調整 → PCM。

**エンコードフロー**: PCM → モード選択 → CELT、SILK-only、または hybrid
エンコード。CELT は必要に応じて 48 kHz へリサンプルし、SILK-only は
24/48 kHz voice を WB SILK へ変換でき、hybrid は SILK low band と CELT high
band を結合します → レンジコーダー → TOC 付加 → Opus パケット。

## ビルドとテスト

```bash
go build ./...
go vet ./...
go test ./...                 # ライブラリ各パッケージ + 公式ベクター（存在時）
go test -race ./...
go test -bench=. -benchmem -run='^$' ./...
```

公式 RFC 8251 テストベクターはリポジトリに含まれていません（`testdata/` は
git-ignore）。必要なテストはベクター不在時に `t.Skip` します。ローカルで実行するには、
`testdata/opus_newvectors/` に展開されるよう取得してください:

```bash
curl -fSL -o /tmp/v.tar.gz https://opus-codec.org/docs/opus_testvectors-rfc8251.tar.gz
mkdir -p testdata && tar -xzf /tmp/v.tar.gz -C testdata/
go test -run TestOfficialVectors ./...
```

### libopus 参照比較（任意）

`TestCGORef` は各ベクターを本コーデックと libopus の両方でデコードしフレーム単位で
比較します。C ツールチェーンと libopus が必要で、通常ビルドを CGO-free に保つため
`opusref` ビルドタグ下に分離しています:

```bash
go test -tags opusref -run TestCGORef .
```

Windows では、動作する MinGW/MSYS2 ツールチェーンを用意し PowerShell から CGO
ビルドを実行してください。

### ファジング

```bash
go test -run='^$' -fuzz='^FuzzDecode$' -fuzztime=60s .
go test -run='^$' -fuzz='^FuzzOggParsers$' -fuzztime=60s ./oggopus
```

fuzz suite は single-stream decode、packet extensions、multistream
self-delimited framing、repacketizer/padding、Ogg Opus parser を対象にします。
`fuzz` CI workflow が全 target を毎晩および手動で実行します。

## 継続的インテグレーション

主要 GitHub Actions ワークフロー 4 本は **amd64（`ubuntu-latest`）** と
**arm64（`ubuntu-24.04-arm`）** のマトリクスで実行します:

- **`test.yml`** — `go vet`、`go test ./...`、公式 RFC 8251 ベクター。
- **`race.yml`** — `go test -race ./...`。
- **`bench.yml`** — `go test -bench=. -benchmem`（結果を artifact 化）。
- **`fuzz.yml`** — 毎晩 + 手動の `go test -fuzz`（ターゲット別）。

加えて **`opusref.yml`** が Ubuntu 上で libopus 相互運用・品質チェックを行い、
**`claude.yml`** が issue/PR automation を提供します。

## ドキュメント

- **[docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md)** — API・内部構造・テスト・既知の差分のコード由来スナップショット（正本）。
- **[docs/CTL_PARITY.md](docs/CTL_PARITY.md)** — libopus 1.6.1 CTL/helper 対応表。
- **[docs/REAL_CORPUS_SCOREBOARD.md](docs/REAL_CORPUS_SCOREBOARD.md)** — opt-in 実音声 matched-bitrate A/B scoreboard。
- **[docs/MODE_RATE_POLICY_DIFF.md](docs/MODE_RATE_POLICY_DIFF.md)** — SILK/hybrid mode-rate-quality policy の差分と測定ガード。
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — 設計判断と libopus 解析。
- **[docs/ROADMAP.md](docs/ROADMAP.md)** — 開発フェーズとマイルストーン。
- **[docs/DEVELOPER.md](docs/DEVELOPER.md)** — コードスタイル、移植ガイダンス、プロファイリング。
- **[IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md)** — 準拠・追跡計画。食い違う場合は正本を優先。

## 制限事項

- SILK/hybridエンコードはvoice向けであり、libopusの全mode境界・品質判断との
  完全な一致には未到達です。
- エンコーダーは libopus とビット精度一致ではありませんが、準拠デコーダー
  （libopus を含む）がデコードできる標準 Opus パケットを出力します。
- SILK-only と hybrid エンコードは、`SetInbandFEC(true)` と 0 より大きい
  `SetPacketLossPerc` により、mono/stereo の LBRR/in-band FEC を送出できます。
- `DecodeFEC` は次のパケットの LBRR から SILK-only/hybrid を回復します。
  hybrid の回復内容は冗長 SILK low band です。`DecodePLC` は CELT-only、
  SILK-only、hybrid の損失を、対応 mode の正常 decode 後に補償します。
- Projection family 3 は libopus 1.6.1 の定義済み matrix を使用し、任意の
  custom encoder matrix 生成は未対応です。
- Ogg Opus package は単一 logical stream を対象とし、seek、chained stream
  orchestration、multiplexed stream demux は提供しません。
- マルチストリーム/サラウンドは、libopus の全 multistream CTL と完全な
  surround energy-mask analysis には未対応です。
- VBR/CVBR と application/signal hint は CELT エンコーダーの判断を調整しますが、
  libopus と同等の完全なモード選択・レート制御ではありません。

## コントリビューション

PR を出す前に以下を確認してください:

1. `go build ./...`、`go vet ./...`、`go test ./...` が通ること。
2. コードが `gofmt` 済みであること。
3. 新しい挙動にテストがあること。

## ライセンス

BSD 2-Clause License — 詳細は [LICENSE](LICENSE) を参照してください。

## 謝辞

- **[libopus](https://github.com/xiph/opus)** — Xiph.Org Foundation によるリファレンス実装。
- **[RFC 6716](https://datatracker.ietf.org/doc/html/rfc6716)** / **[RFC 8251](https://datatracker.ietf.org/doc/html/rfc8251)** — Opus 仕様とその更新。

## サポート

問題・質問は [GitHub issue tracker](https://github.com/darui3018823/opus/issues) をご利用ください。
