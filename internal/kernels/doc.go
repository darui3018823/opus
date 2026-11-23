// Package kernels holds the codec's vectorised inner loops.
//
// Every kernel has a pure Go reference implementation and may have
// architecture-specific versions. An accelerated version must return the
// same bits as the reference for every input, including subnormals, signed
// zeros and infinities, so that packets, final ranges and decoded PCM do
// not depend on the CPU or on the build. A NaN result must stay NaN, but its
// payload and sign are not part of the contract: the scalar Go code itself
// does not pin them, because the compiler may order a commutative add either
// way. In practice this means:
//
//   - vectorise across independent outputs (lags, channels, samples) and
//     never reassociate a float32 sum;
//   - never fuse a multiply and an add (no FMA);
//   - keep the reference's operation order inside each lane.
//
// The accelerated paths are selected at init from the CPU features. Build
// with the purego tag to compile only the reference implementations.
package kernels
