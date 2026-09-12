package celt

import (
	"math"
	"math/big"
)

const celtExp2Precision = 256

var celtLn2 = func() *big.Float {
	v, _, err := big.ParseFloat(
		"0.693147180559945309417232121458176568075500134360255254120680009493393621969694715605863326996418687",
		10, celtExp2Precision, big.ToNearestEven,
	)
	if err != nil {
		panic(err)
	}
	return v
}()

// celtExp2RoundedFloat32 returns correctly rounded float32 2**x. The normal
// path uses math.Exp2. If its double result lies extremely close to a float32
// rounding boundary, a 256-bit Taylor evaluation resolves the tie. This
// mirrors the float result produced by libopus' celt_exp2_db while avoiding a
// platform-libm dependency in the Pure Go decoder.
func celtExp2RoundedFloat32(x float32) float32 {
	y := math.Exp2(float64(x))
	result := float32(y)
	if result == 0 || math.IsInf(float64(result), 0) || math.IsNaN(y) {
		return result
	}

	result64 := float64(result)
	var adjacent float32
	if y >= result64 {
		adjacent = math.Nextafter32(result, float32(math.Inf(1)))
	} else {
		adjacent = math.Nextafter32(result, 0)
	}
	midpoint := result64 + (float64(adjacent)-result64)*0.5
	if math.Abs(y-midpoint) > math.Abs(y)*1e-12 {
		return result
	}
	return celtExp2BigFloat32(x)
}

func celtExp2BigFloat32(x float32) float32 {
	x64 := float64(x)
	integer := int(math.Floor(x64))
	fraction := new(big.Float).SetPrec(celtExp2Precision).SetFloat64(x64 - float64(integer))
	z := new(big.Float).SetPrec(celtExp2Precision).Mul(
		fraction,
		new(big.Float).SetPrec(celtExp2Precision).Set(celtLn2),
	)

	one := new(big.Float).SetPrec(celtExp2Precision).SetInt64(1)
	sum := new(big.Float).SetPrec(celtExp2Precision).Set(one)
	term := new(big.Float).SetPrec(celtExp2Precision).Set(one)
	for n := int64(1); n <= 80; n++ {
		term.Mul(term, z)
		term.Quo(term, new(big.Float).SetPrec(celtExp2Precision).SetInt64(n))
		sum.Add(sum, term)
	}

	scaled := new(big.Float).SetPrec(celtExp2Precision).SetMantExp(sum, integer)
	result, _ := scaled.Float32()
	return result
}
