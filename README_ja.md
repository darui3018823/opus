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
フレーム単位で一致します。**エンコーダー**は CELT quality pipeline と、限定的な
モノラル低ビットレート SILK-only 音声経路を実装し、libopus でデコード可能な
標準 Opus パケットを出力します（[ステータス](#ステータス)参照）。

> 補足: エンコーダーは libopus のエンコーダーとはビット精度一致しません。
> CELT 経路は Pure Go での音声・音楽エンコードに利用できます。SILK 経路は
> モノラル低ビットレート音声に限定され、ハイブリッドエンコードは未実装です。

## ステータス

| 領域 | 状態 |
|------|------|
| **デコーダー** | ✅ 公式 RFC 8251 ベクター 12/12 合格（RMSE < 0.001）。libopus 1.6.1 と一致。SILK / CELT / ハイブリッド（SILK+CELT）を再構成済み（ハイブリッド SILK→CELT redundancy 含む）。 |
| **エンコーダー** | ✅ CELT quality pipeline（Phase 1+2）と、native 8/12/16 kHz の低ビットレート音声向け限定モノラル SILK-only エンコード。libopus 1.6.1 がデコードできる標準 Opus パケットを出力。64 kbps の目安 SNR: 440 Hz 約 48 dB、1 kHz 約 47 dB、ステレオ約 43 dB。libopus とはビット精度一致**ではない**。ハイブリッドエンコードは未実装。 |
| **CGO** | ランタイム依存なし。libopus ラッパーは参照テスト専用で `opusref` ビルドタグ下にのみ存在。 |
| **CI** | `test` / `race` / `bench` / `fuzz` を **amd64・arm64** で実行。 |

正確な現状は [docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md)
（コードから導出したスナップショット）を参照してください。

## インストール

```bash
go get github.com/darui3018823/opus
```

Go 1.24 以降が必要です（`go.mod` 参照）。

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

// 帯域は信号内容から自動検出される。必要なら明示指定できる:
//   enc.SetBandwidth(opus.BandwidthWideband) // wideband に固定
//   enc.SetBandwidth(opus.BandwidthAuto)     // 自動選択へ戻す

// Application とは独立した信号種別ヒント:
//   enc.SetSignalType(opus.SignalVoice)

// 20ms の整数倍、最大 120ms まで multi-frame packet として出力可能:
//   packet, err := enc.Encode(pcm1920, 1920) // 40ms
```

## サポート設定

- **サンプルレート**: 8 / 12 / 16 / 24 / 48 kHz。エンコーダーは非 48kHz 入力を
  内部で 48kHz にリサンプルし、デコーダーは出力を要求レートにリサンプルします。
- **チャンネル**: モノラル・ステレオ。
- **デコーダーのフレームサイズ**: 全 Opus 長（2.5/5/10/20/40/60 ms）を TOC バイトに
  従いパケット単位で選択。
- **エンコーダーのフレームサイズ**: 20ms の整数倍（20ms〜120ms）。それ以外は
  エラーになります。
- **エンコーダー帯域**: 信号内容に基づく自動検出、または
  `SetBandwidth` / `SetMaxBandwidth` による明示指定。NB/WB/SWB/FB に対応。
- **エンコーダーモード選択**: 通常の音声・音楽、restricted-low-delay、
  40 kbps 超のビットレートでは CELT を使います。限定的な SILK-only 経路は、
  モノラルまたはステレオの音声で、`ApplicationVOIP` または `SignalVoice` が有効、
  ビットレートが 40 kbps 以下、かつ `SetBandwidth` / `SetMaxBandwidth` が選択
  された SILK 帯域を許す場合に選択されます。8/12/16 kHz 入力は NB/MB/WB に対応し、
  24/48 kHz の音声入力は WB SILK へダウンサンプリングされます。`SignalMusic` は
  CELT を維持します。
- **アプリケーションタイプ**（帯域しきい値や transient 判定を調整）:
  - `opus.ApplicationVOIP` — voice 向け、狭めの帯域しきい値
  - `opus.ApplicationAudio` — music/general 向け
  - `opus.ApplicationRestrictedLowDelay`
- **信号種別ヒント**: `opus.SignalAuto` / `opus.SignalVoice` /
  `opus.SignalMusic`。ビットストリーム形式は変えず、エンコーダーの判断だけを調整します。

## 公開 API

公開バージョン定数はリポジトリの [`VERSION`](VERSION) から生成されます。

### エンコーダー

```go
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error)

func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error)
func (e *Encoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error)

func (e *Encoder) Bitrate() int
func (e *Encoder) Complexity() int
func (e *Encoder) VBR() bool
func (e *Encoder) Application() Application

func (e *Encoder) SetBitrate(bitrate int) error       // 6000–510000 bps
func (e *Encoder) SetComplexity(complexity int) error // 0–10
func (e *Encoder) SetVBR(vbr bool)
func (e *Encoder) SetVBRConstraint(constrained bool)  // true = CVBR
func (e *Encoder) SetApplication(application Application) error
func (e *Encoder) SetSignalType(signal SignalType)
func (e *Encoder) SignalType() SignalType
func (e *Encoder) SetBandwidth(bw int) error          // Auto/NB/WB/SWB/FB
func (e *Encoder) SetMaxBandwidth(bw int) error
func (e *Encoder) Bandwidth() int
func (e *Encoder) SetDTX(dtx bool)
func (e *Encoder) DTX() bool
func (e *Encoder) SetInbandFEC(enabled bool)             // mono SILK-only
func (e *Encoder) InbandFEC() bool
func (e *Encoder) SetPacketLossPerc(perc int)            // 0〜100 にクランプ
func (e *Encoder) PacketLossPerc() int
func (e *Encoder) SetPacketPadding(n int)
func (e *Encoder) Reset() error
```

### デコーダー

```go
func NewDecoder(sampleRate, channels int) (*Decoder, error)

func (d *Decoder) Decode(data []byte, pcm []int16) (int, error)
func (d *Decoder) DecodeFloat(data []byte) ([]float64, error)
func (d *Decoder) DecodePLC(pcm []int16, frameSize int) (int, error) // CELT-only
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error)   // ErrUnimplemented
func (d *Decoder) Reset() error
func (d *Decoder) GetLastPacketDuration() int
```

`EncodeFloat32` / `DecodeFloat32` は**ありません**。float64 版を使用してください。

## アーキテクチャ

```
github.com/darui3018823/opus/
├── opus.go / constants.go / errors.go  # 公開 API（Encoder/Decoder）
├── internal/
│   ├── opus_framing.go                  # TOC バイトの解析/生成（RFC 6716 §3）
│   ├── dsp/                             # FFT、MDCT/IMDCT、窓関数、数学
│   ├── entcode/                         # レンジエンコーダー/デコーダー
│   ├── resampler/                       # Opus レートのサンプルレート変換
│   ├── celt/                            # CELT デコーダーパリティ + 簡易エンコーダー
│   ├── silk/                            # SILK デコーダー/エンコーダー、テーブル、補助
│   └── cgoref/                          # libopus 参照ラッパー（ビルドタグ: opusref）
└── docs/                                # 設計・ステータス文書
```

**デコードフロー**: Opus パケット → TOC 解析 → CELT または SILK/ハイブリッド経路 →
レンジデコード + 再構成 → 必要に応じてリサンプル/チャンネル調整 → PCM。

**エンコードフロー**: PCM → モード選択 → SILK-only モノラル音声エンコード、または
必要なら 48kHz へリサンプルして CELT エンコード（MDCT、バンド処理、PVQ）→
レンジコーダー → TOC 付加 → Opus パケット。

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
```

`FuzzDecode` / `FuzzDecodeFloat` は、デコーダーが任意入力で panic しないことを検証
します。`fuzz` CI ワークフローが毎晩および手動で実行します。

## 継続的インテグレーション

GitHub Actions ワークフロー 4 本。いずれも **amd64（`ubuntu-latest`）** と
**arm64（`ubuntu-24.04-arm`）** のマトリクスで実行します:

- **`test.yml`** — `go vet`、`go test ./...`、公式 RFC 8251 ベクター。
- **`race.yml`** — `go test -race ./...`。
- **`bench.yml`** — `go test -bench=. -benchmem`（結果を artifact 化）。
- **`fuzz.yml`** — 毎晩 + 手動の `go test -fuzz`（ターゲット別）。

## ドキュメント

- **[docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md)** — API・内部構造・テスト・既知の差分のコード由来スナップショット（正本）。
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — 設計判断と libopus 解析。
- **[docs/ROADMAP.md](docs/ROADMAP.md)** — 開発フェーズとマイルストーン。
- **[docs/DEVELOPER.md](docs/DEVELOPER.md)** — コードスタイル、移植ガイダンス、プロファイリング。
- **[IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md)** — 仕様差分リストと準拠/テスト計画。

## 制限事項

- SILK-only エンコードは低ビットレート音声に限定されています。24/48 kHz 入力は
  WB SILK へダウンサンプリングされるため、高域保持には今後のハイブリッド
  エンコード対応が必要です。
- エンコーダーは libopus とビット精度一致ではありませんが、準拠デコーダー
  （libopus を含む）がデコードできる標準 Opus パケットを出力します。
- mono SILK-only エンコードは、`SetInbandFEC(true)` と 0 より大きい
  `SetPacketLossPerc` により、準拠した LBRR/in-band FEC を送出できます。
  stereo/hybrid LBRR は未実装です。
- `DecodePLC` は現状 CELT-only ストリームに対応します。SILK/hybrid PLC と
  パケット FEC 抽出は未実装で、`DecodeFEC` は `ErrUnimplemented` を返します。
- マルチストリーム・サラウンド・Ogg Opus コンテナ API はありません。
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
