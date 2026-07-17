package oggopus_test

import (
	"bytes"
	"fmt"

	"github.com/darui3018823/opus"
	"github.com/darui3018823/opus/oggopus"
)

func ExampleWriter() {
	encoder, err := opus.NewEncoder(opus.SampleRate48kHz, 1, opus.ApplicationAudio)
	if err != nil {
		panic(err)
	}
	encoded, err := encoder.Encode(make([]int16, opus.FrameSize20ms), opus.FrameSize20ms)
	if err != nil {
		panic(err)
	}

	var container bytes.Buffer
	writer, err := oggopus.NewWriter(
		&container,
		1,
		oggopus.Head{
			Version:         1,
			Channels:        1,
			PreSkip:         uint16(encoder.Lookahead()),
			InputSampleRate: opus.SampleRate48kHz,
		},
		oggopus.Tags{Vendor: "example"},
	)
	if err != nil {
		panic(err)
	}
	if err := writer.WritePacket(encoded, oggopus.PacketWriteOptions{
		GranulePosition: opus.FrameSize20ms,
	}); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}

	reader, err := oggopus.NewReader(bytes.NewReader(container.Bytes()))
	if err != nil {
		panic(err)
	}
	packet, err := reader.NextPacket()
	if err != nil {
		panic(err)
	}
	fmt.Printf(
		"vendor=%q duration=%d eos=%t same-packet=%t\n",
		reader.Tags.Vendor,
		packet.Duration48k,
		packet.EOS,
		bytes.Equal(packet.Data, encoded),
	)
	// Output:
	// vendor="example" duration=960 eos=true same-packet=true
}
