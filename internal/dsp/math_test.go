package dsp

import (
	"math"
	"testing"
)

func TestComplexOperations(t *testing.T) {
	c1 := Complex{Real: 3.0, Imag: 4.0}
	c2 := Complex{Real: 1.0, Imag: 2.0}

	// Test addition
	sum := c1.Add(c2)
	if sum.Real != 4.0 || sum.Imag != 6.0 {
		t.Errorf("Add failed: got %v, want {4, 6}", sum)
	}

	// Test subtraction
	diff := c1.Sub(c2)
	if diff.Real != 2.0 || diff.Imag != 2.0 {
		t.Errorf("Sub failed: got %v, want {2, 2}", diff)
	}

	// Test multiplication
	prod := c1.Mul(c2)
	// (3+4i)(1+2i) = 3 + 6i + 4i + 8i² = 3 + 10i - 8 = -5 + 10i
	if prod.Real != -5.0 || prod.Imag != 10.0 {
		t.Errorf("Mul failed: got %v, want {-5, 10}", prod)
	}

	// Test magnitude
	mag := c1.Abs()
	expected := 5.0
	if math.Abs(mag-expected) > 1e-10 {
		t.Errorf("Abs failed: got %v, want %v", mag, expected)
	}

	// Test conjugate
	conj := c1.Conj()
	if conj.Real != 3.0 || conj.Imag != -4.0 {
		t.Errorf("Conj failed: got %v, want {3, -4}", conj)
	}
}

func TestMathUtils(t *testing.T) {
	// Test IsPowerOf2
	if !IsPowerOf2(16) {
		t.Error("IsPowerOf2(16) should be true")
	}
	if IsPowerOf2(15) {
		t.Error("IsPowerOf2(15) should be false")
	}

	// Test NextPowerOf2
	if NextPowerOf2(15) != 16 {
		t.Error("NextPowerOf2(15) should be 16")
	}
	if NextPowerOf2(16) != 16 {
		t.Error("NextPowerOf2(16) should be 16")
	}

	// Test Log2
	if Log2(16) != 4 {
		t.Error("Log2(16) should be 4")
	}

	// Test BitReverse
	// For 3 bits: 0b101 (5) reversed is 0b101 (5)
	if BitReverse(5, 3) != 5 {
		t.Errorf("BitReverse(5, 3) = %d, want 5", BitReverse(5, 3))
	}
	// For 4 bits: 0b0001 (1) reversed is 0b1000 (8)
	if BitReverse(1, 4) != 8 {
		t.Errorf("BitReverse(1, 4) = %d, want 8", BitReverse(1, 4))
	}
}

func TestDotProduct(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6}
	expected := 1.0*4.0 + 2.0*5.0 + 3.0*6.0 // = 4 + 10 + 18 = 32
	result := Dot(a, b)
	if math.Abs(result-expected) > 1e-10 {
		t.Errorf("Dot product failed: got %v, want %v", result, expected)
	}
}

func TestEnergy(t *testing.T) {
	x := []float64{1, 2, 3}
	expected := 1.0*1.0 + 2.0*2.0 + 3.0*3.0 // = 14
	result := Energy(x)
	if math.Abs(result-expected) > 1e-10 {
		t.Errorf("Energy failed: got %v, want %v", result, expected)
	}
}

func TestRMS(t *testing.T) {
	x := []float64{1, 2, 3}
	expected := math.Sqrt(14.0 / 3.0)
	result := RMS(x)
	if math.Abs(result-expected) > 1e-10 {
		t.Errorf("RMS failed: got %v, want %v", result, expected)
	}
}

func BenchmarkDot(b *testing.B) {
	x := make([]float64, 1024)
	y := make([]float64, 1024)
	for i := range x {
		x[i] = float64(i)
		y[i] = float64(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Dot(x, y)
	}
}

func BenchmarkComplexMul(b *testing.B) {
	c1 := Complex{Real: 3.0, Imag: 4.0}
	c2 := Complex{Real: 1.0, Imag: 2.0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c1.Mul(c2)
	}
}
