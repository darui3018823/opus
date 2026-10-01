//go:build ignore

package main

import (
	"fmt"
	"go/format"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type tableSpec struct {
	cType      string
	cName      string
	cCount     int
	valueCount int
	goName     string
}

func main() {
	headerPath := filepath.Join("..", "..", "libopus", "celt", "static_modes_float.h")
	source, err := os.ReadFile(headerPath)
	if err != nil {
		panic(err)
	}

	tables := []tableSpec{
		{cType: "celt_coef", cName: "window120", cCount: 120, valueCount: 120, goName: "libopusWindow120Bits"},
		{cType: "kiss_twiddle_cpx", cName: "fft_twiddles48000_960", cCount: 480, valueCount: 960, goName: "libopusFFTTwiddleBits"},
		{cType: "celt_coef", cName: "mdct_twiddles960", cCount: 1800, valueCount: 1800, goName: "libopusMDCTTwiddleBits"},
	}
	numberPattern := regexp.MustCompile(`[-+]?(?:\d+\.\d*|\.\d+|\d+)(?:[eE][-+]?\d+)?f`)

	var output strings.Builder
	output.WriteString("// Code generated from libopus 1.6.1 static_modes_float.h; DO NOT EDIT.\n\n")
	output.WriteString("package dsp\n\n")
	for _, table := range tables {
		pattern := regexp.MustCompile(fmt.Sprintf(
			`(?s)static const %s %s\[%d\] = \{(.*?)\};`,
			regexp.QuoteMeta(table.cType), regexp.QuoteMeta(table.cName), table.cCount,
		))
		match := pattern.FindSubmatch(source)
		if match == nil {
			panic(fmt.Sprintf("table %s not found", table.cName))
		}
		values := numberPattern.FindAllString(string(match[1]), -1)
		if len(values) != table.valueCount {
			panic(fmt.Sprintf("table %s has %d values, want %d", table.cName, len(values), table.valueCount))
		}
		fmt.Fprintf(&output, "var %s = [...]uint32{\n", table.goName)
		for i, value := range values {
			parsed, err := strconv.ParseFloat(strings.TrimSuffix(value, "f"), 32)
			if err != nil {
				panic(err)
			}
			if i%8 == 0 {
				output.WriteString("\t")
			}
			fmt.Fprintf(&output, "0x%08x,", math.Float32bits(float32(parsed)))
			if i%8 == 7 || i == len(values)-1 {
				output.WriteString("\n")
			} else {
				output.WriteString(" ")
			}
		}
		output.WriteString("}\n\n")
	}

	formatted, err := format.Source([]byte(output.String()))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("mdct_twiddles_gen.go", formatted, 0o644); err != nil {
		panic(err)
	}
}
