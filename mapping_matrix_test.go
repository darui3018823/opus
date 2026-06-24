package opus

import (
	"errors"
	"testing"
)

func TestMappingMatrixSimpleMultiply(t *testing.T) {
	matrix, err := NewMappingMatrix(4, 3, 0, []int16{
		0, 32767, 0, 0,
		32767, 0, 0, 0,
		0, 0, 0, 32767,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := []float64{
		1, 0, -1,
		0.9, -0.1, -0.9,
	}
	got, err := matrix.multiplyFloat64(input, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 1, 0, -1, -0.1, 0.9, 0, -0.9}
	for i := range want {
		if diff := got[i] - want[i]; diff < -1e-4 || diff > 1e-4 {
			t.Fatalf("sample %d = %g, want %g", i, got[i], want[i])
		}
	}

	roundTrip, err := NewMappingMatrixFromBytes(matrix.Rows(), matrix.Cols(), matrix.Gain(), matrix.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for i, coefficient := range matrix.Coefficients() {
		if roundTrip.Coefficients()[i] != coefficient {
			t.Fatalf("coefficient %d changed", i)
		}
	}
}

func TestMappingMatrixRejectsInvalidDimensionsAndData(t *testing.T) {
	tests := []struct {
		rows, cols int
		data       []int16
	}{
		{0, 1, nil},
		{1, 256, make([]int16, 256)},
		{255, 255, make([]int16, 255*255)},
		{2, 3, make([]int16, 5)},
	}
	for _, tc := range tests {
		if _, err := NewMappingMatrix(tc.rows, tc.cols, 0, tc.data); !errors.Is(err, ErrBadArg) {
			t.Fatalf("NewMappingMatrix(%d,%d,%d coefficients) error = %v", tc.rows, tc.cols, len(tc.data), err)
		}
	}
	if _, err := NewMappingMatrixFromBytes(1, 1, 0, []byte{1}); !errors.Is(err, ErrBadArg) {
		t.Fatalf("odd byte matrix error = %v", err)
	}
}

func TestPredefinedProjectionMatrixMetadata(t *testing.T) {
	tests := []struct {
		channels, gain int
	}{
		{4, 0},
		{6, 0},
		{9, 3050},
		{11, 3050},
		{16, 0},
		{18, 0},
		{25, 0},
		{27, 0},
		{36, 0},
		{38, 0},
	}
	for _, tc := range tests {
		matrices, err := predefinedAmbisonicsMatrices(tc.channels)
		if err != nil {
			t.Fatalf("%d channels: %v", tc.channels, err)
		}
		if matrices.mixing.Rows() != tc.channels || matrices.mixing.Cols() != tc.channels {
			t.Fatalf("%d-channel mixing dimensions = %dx%d", tc.channels, matrices.mixing.Rows(), matrices.mixing.Cols())
		}
		if matrices.demixing.Gain() != tc.gain {
			t.Fatalf("%d-channel demixing gain = %d, want %d", tc.channels, matrices.demixing.Gain(), tc.gain)
		}
		if len(matrices.demixing.Bytes()) != 2*tc.channels*tc.channels {
			t.Fatalf("%d-channel demixing byte size = %d", tc.channels, len(matrices.demixing.Bytes()))
		}
	}
}
