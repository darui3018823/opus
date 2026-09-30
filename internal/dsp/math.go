// Package dsp provides digital signal processing utilities for the Opus codec.
package dsp

import "math"

// Complex represents a complex number for FFT operations
type Complex struct {
	Real, Imag float64
}

// Add adds two complex numbers
func (c Complex) Add(other Complex) Complex {
	return Complex{
		Real: c.Real + other.Real,
		Imag: c.Imag + other.Imag,
	}
}

// Sub subtracts another complex number
func (c Complex) Sub(other Complex) Complex {
	return Complex{
		Real: c.Real - other.Real,
		Imag: c.Imag - other.Imag,
	}
}

// Mul multiplies two complex numbers
func (c Complex) Mul(other Complex) Complex {
	return Complex{
		Real: float64(c.Real*other.Real) - float64(c.Imag*other.Imag),
		Imag: float64(c.Real*other.Imag) + float64(c.Imag*other.Real),
	}
}

// Abs returns the magnitude of the complex number
func (c Complex) Abs() float64 {
	return math.Sqrt(float64(c.Real*c.Real) + float64(c.Imag*c.Imag))
}

// Conj returns the complex conjugate
func (c Complex) Conj() Complex {
	return Complex{Real: c.Real, Imag: -c.Imag}
}

// MulScalar multiplies by a real scalar
func (c Complex) MulScalar(s float64) Complex {
	return Complex{Real: c.Real * s, Imag: c.Imag * s}
}

// Math utilities

// IsPowerOf2 checks if n is a power of 2
func IsPowerOf2(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

// NextPowerOf2 returns the next power of 2 >= n
func NextPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	return n + 1
}

// Log2 returns the base-2 logarithm of n (for power of 2)
func Log2(n int) int {
	log := 0
	for n > 1 {
		n >>= 1
		log++
	}
	return log
}

// BitReverse reverses the bits of n for the given number of bits
func BitReverse(n, bits int) int {
	result := 0
	for i := 0; i < bits; i++ {
		result = (result << 1) | (n & 1)
		n >>= 1
	}
	return result
}

// Dot computes the dot product of two float64 slices
func Dot(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	sum := 0.0
	for i := range a {
		sum += float64(a[i] * b[i])
	}
	return sum
}

// Energy computes the energy (sum of squares) of a signal
func Energy(x []float64) float64 {
	sum := 0.0
	for _, v := range x {
		sum += float64(v * v)
	}
	return sum
}

// RMS computes the root mean square of a signal
func RMS(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	return math.Sqrt(Energy(x) / float64(len(x)))
}

// Abs returns the absolute value of a float64
func Abs(x float64) float64 {
	return math.Abs(x)
}

// Sin returns the sine of x (in radians)
func Sin(x float64) float64 {
	return math.Sin(x)
}

// Pi is the mathematical constant π
const Pi = math.Pi
