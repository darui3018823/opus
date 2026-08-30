package opus_test

import (
	"fmt"

	"github.com/darui3018823/opus"
)

func ExampleInspectPacket() {
	// A stereo, fullband CELT packet containing one 10 ms frame.
	packet := []byte{byte(30<<3) | 0x04, 0}
	info, err := opus.InspectPacket(packet, opus.SampleRate48kHz)
	if err != nil {
		panic(err)
	}
	fmt.Printf("config=%d channels=%d frames=%d samples=%d\n", info.Config, info.Channels, info.FrameCount, info.SampleCount)
	// Output:
	// config=30 channels=2 frames=1 samples=480
}

func ExampleEncoder_Encode() {
	encoder, err := opus.NewEncoder(opus.SampleRate48kHz, 1, opus.ApplicationAudio)
	if err != nil {
		panic(err)
	}

	// frameSize is samples per channel. A mono 20 ms input therefore contains
	// 960 interleaved values; stereo would contain 1920.
	pcm := make([]int16, opus.FrameSize20ms)
	packet, err := encoder.Encode(pcm, opus.FrameSize20ms)
	if err != nil {
		panic(err)
	}
	duration, err := opus.PacketGetNumSamples(packet, opus.SampleRate48kHz)
	if err != nil {
		panic(err)
	}
	fmt.Printf("encoded %d samples per channel\n", duration)
	// Output:
	// encoded 960 samples per channel
}

func ExampleDecoder_Decode() {
	encoder, err := opus.NewEncoder(opus.SampleRate48kHz, 2, opus.ApplicationAudio)
	if err != nil {
		panic(err)
	}
	packet, err := encoder.Encode(make([]int16, opus.FrameSize20ms*2), opus.FrameSize20ms)
	if err != nil {
		panic(err)
	}

	decoder, err := opus.NewDecoder(opus.SampleRate48kHz, 2)
	if err != nil {
		panic(err)
	}
	pcm := make([]int16, opus.MaxFrameSize*2)
	samples, err := decoder.Decode(packet, pcm)
	if err != nil {
		panic(err)
	}
	pcm = pcm[:samples*decoder.Channels()]
	fmt.Printf("decoded %d samples per channel into %d interleaved values\n", samples, len(pcm))
	// Output:
	// decoded 960 samples per channel into 1920 interleaved values
}

func ExampleMultistreamEncoder_Encode() {
	const (
		channels       = 2
		streams        = 2
		coupledStreams = 0
	)
	mapping := []byte{0, 1}
	encoder, err := opus.NewMultistreamEncoder(
		opus.SampleRate48kHz,
		channels,
		streams,
		coupledStreams,
		mapping,
		opus.ApplicationAudio,
	)
	if err != nil {
		panic(err)
	}
	packet, err := encoder.Encode(make([]int16, opus.FrameSize20ms*channels), opus.FrameSize20ms)
	if err != nil {
		panic(err)
	}

	decoder, err := opus.NewMultistreamDecoder(
		opus.SampleRate48kHz,
		channels,
		streams,
		coupledStreams,
		mapping,
	)
	if err != nil {
		panic(err)
	}
	pcm := make([]int16, opus.MaxFrameSize*channels)
	samples, err := decoder.Decode(packet, pcm)
	if err != nil {
		panic(err)
	}
	pcm = pcm[:samples*channels]
	fmt.Printf(
		"decoded %d samples per channel from %d streams into %d channels\n",
		samples,
		decoder.Streams(),
		decoder.Channels(),
	)
	// Output:
	// decoded 960 samples per channel from 2 streams into 2 channels
}
