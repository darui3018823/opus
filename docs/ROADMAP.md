# Implementation Roadmap

## Overview

This document tracks the implementation progress of the Pure Go Opus library and provides guidance for each development phase.

## Current Status: Phase 2 Foundation ✅ (updated for Opus 1.3.1 parity)

### Completed Components

#### 1. Project Structure
- ✅ Go module initialized
- ✅ Package hierarchy established
- ✅ Constants and error types defined
- ✅ Documentation structure created

#### 2. DSP Foundation (`internal/dsp/`)

**FFT Implementation** ✅
- Cooley-Tukey radix-2 decimation-in-time algorithm
- Bit-reversal ordering
- Forward and inverse transforms
- Real FFT with symmetry exploitation
- FFTConfig with precomputed twiddle factors
- Test coverage: 100% pass
- Benchmarks: ~2-3 µs for 128-point, ~25 µs for 1024-point

**MDCT/IMDCT** ✅
- Based on FFT implementation
- Vorbis window integration
- Overlap-add support for streaming
- Forward and inverse transforms validated
- Test coverage: roundtrip, impulse response, sine wave
- Benchmarks: ~6 µs for 128-coeff, ~45 µs for 512-coeff

**Window Functions** ✅
- Hann window (smooth windowing)
- Hamming window (spectral analysis)
- Blackman window (low sidelobe)
- Sine window (audio coding)
- Vorbis window (CELT/MDCT specific)
- All windows tested for symmetry and range
- Overlap-add utilities implemented

**Math Utilities** ✅
- Complex number operations (add, sub, mul, conjugate, abs)
- Vector operations (dot product, energy, RMS)
- Bit manipulation (bit reverse, power of 2 checks, log2)
- Clamping and range functions
- Test coverage: comprehensive

#### 3. Entropy Coding (`internal/entcode/`)

**Range Coder** ✅ (Basic)
- Range encoder with normalization
- Range decoder with symbol extraction
- Bit-level coding/decoding working
- Integer-based implementation (32-bit precision)
- Test coverage: bit coding, roundtrip
- ⚠️ Note: Symbol and uint encoding need refinement for full libopus compatibility

### Remaining Phase 2 Tasks (Opus 1.3.1 focus)

**Range Coder Parity** 🔄 (Highest Priority)
- Make symbol/uint ICDF paths bit-exact with libopus 1.3.1.
- Add regression vectors comparing encoded bitstreams to reference.

**Polyphase Resampler** 🔄
- Complete up/down paths for all Opus sample rates (8/12/16/24/48 kHz).
- Validate aliasing/response; add table-driven tests.

**Test Vectors & Compliance Harness** 🔄
- Integrate official 1.3.1 test vectors (CELT/SILK/hybrid) across frame sizes (2.5–60 ms, then 120 ms).
- Automate matrix for sample rate × channels × application × frame size; include TOC edge cases.
- Add corrupted-frame and padding tests; fuzz decoder/parser.

**Compatibility Table & API Shims** 🔄
- Document/publicize API/behavior deltas vs libopus/opusfile/layeh.com/gopus.
- Add automated assertions for a selected subset.

## Phase 3: CELT Implementation (Next)

### Part 1: CELT Decoder (Weeks 3-4)

**Priority: Implement decoder first** (easier to validate)

#### Frame Structure
```go
type CELTFrame struct {
    Mode          int      // CELT mode
    Bandwidth     int      // Effective bandwidth
    FrameSize     int      // Frame size in samples
    Transient     bool     // Transient flag
    IntraFrame    bool     // Intra-frame flag
    Fine          []int    // Fine energy
    FineQuant     []int    // Fine quantization
    Bands         [21]Band // Frequency bands
}
```

#### Implementation Steps
1. **Packet Parsing**
   - Parse TOC byte
   - Extract frame data
   - Initialize range decoder

2. **Band Decoding**
   - Decode band energies
   - Decode fine energy
   - Decode band flags

3. **PVQ Decoding**
   - Implement cwrs (combinatorial math)
   - Decode PVQ indices
   - Reconstruct band coefficients

4. **Post-Processing**
   - Inverse quantization
   - Band denormalization
   - IMDCT
   - Overlap-add
   - Output PCM

#### Validation
- Decode libopus-encoded files
- Compare PCM output sample-by-sample
- Measure PESQ/POLQA scores (target: >4.0)
- Test with various frame sizes and bitrates

### Part 2: CELT Encoder (Weeks 5-7)

#### Analysis Path
1. **Transient Detection**
   - Energy-based detection
   - Look-ahead analysis
   - Set frame boundaries

2. **MDCT Analysis**
   - Apply window
   - Forward MDCT
   - Band grouping

3. **Energy Computation**
   - Compute band energies
   - Normalize bands
   - Log domain representation

#### Quantization Path
1. **Bit Allocation**
   - Distribute bits across bands
   - Consider psychoacoustic model
   - Handle transients

2. **PVQ Quantization**
   - Quantize each band
   - Generate PVQ indices
   - Track rate/distortion

3. **Encoding**
   - Range encode all symbols
   - Pack into frame
   - Generate TOC

#### Validation
- Encode → decode roundtrip
- Compare with libopus output
- Bitrate accuracy
- Quality metrics

### Part 3: CELT Refinement (Week 8)

- Pitch prediction (optional enhancement)
- Fine quantization optimization
- Performance profiling
- Bug fixes and edge cases

## Phase 4: SILK Implementation (Weeks 9-18)

### Part 1: SILK Decoder (Weeks 9-11)

**Core Components**:
1. Frame parsing
2. NLSF decoding
3. Pitch synthesis
4. LPC synthesis
5. PCM generation

**Testing**: Decode libopus SILK frames, validate speech quality

### Part 2: SILK Encoder (Weeks 12-16)

**Analysis**:
1. Voice Activity Detection
2. Pitch analysis (long-term prediction)
3. LPC analysis (short-term prediction)
4. Residual generation

**Quantization**:
1. NLSF quantization
2. Pitch lag encoding
3. Excitation quantization
4. Noise shaping

### Part 3: Hybrid Mode (Weeks 17-18)

**Integration**:
1. SILK for low frequencies (0-8 kHz)
2. CELT for high frequencies (8-20 kHz)
3. Combined bit allocation
4. Seamless switching

## Phase 5: Validation & Testing (Weeks 19-20)

### Week 19: Conformance
- Download official Opus test vectors
- Implement test harness
- Validate all modes
- RFC 6716 compliance check

### Week 20: Robustness
- Fuzz testing with random data
- Invalid packet handling
- Boundary conditions
- Memory safety validation

## Phase 6: Optimization (Weeks 21-23)

### Profiling
```bash
go test -cpuprofile=cpu.prof -bench=.
go tool pprof cpu.prof
```

### Hot Path Targets
1. FFT/MDCT (most CPU intensive)
2. Range coder (frequent operations)
3. Band processing
4. Memory allocations

### Optimization Techniques
- Loop unrolling
- Precomputation
- Buffer reuse (sync.Pool)
- Inline hints
- SIMD considerations

### Performance Targets
- FFT: 70-90% of libopus
- MDCT: 70-90% of libopus
- Overall encode: 60-80% of libopus
- Overall decode: 70-85% of libopus

## Quality Assurance

### Continuous Testing
```bash
# Run all tests
go test ./...

# With coverage
go test -cover ./...

# With race detector
go test -race ./...

# Benchmarks
go test -bench=. -benchmem ./...
```

### Code Quality
- golangci-lint for static analysis
- go vet for common mistakes
- gofmt for formatting
- godoc for documentation

### Git Workflow
- Feature branches for each major component
- Comprehensive commit messages
- Regular progress commits
- PR reviews (self-review in this case)

## Documentation Requirements

For each major component:
1. **Algorithm description** - What it does
2. **Implementation notes** - How it works
3. **Complexity analysis** - Performance characteristics
4. **Test coverage** - What's validated
5. **Known limitations** - Current state

## Success Metrics

### Quantitative
- [ ] All unit tests passing
- [ ] Test coverage >80%
- [ ] Official test vectors passing
- [ ] PESQ score >4.0 for speech
- [ ] Performance within 30% of libopus

### Qualitative
- [ ] Code is readable and maintainable
- [ ] No panics on invalid input
- [ ] API is ergonomic
- [ ] Documentation is comprehensive
- [ ] No compromises on algorithm correctness

## Current Focus

**Immediate priorities (Phase 2 completion)**:
1. ✅ FFT, MDCT, windows complete
2. 🔄 Implement polyphase resampler
3. 🔄 Refine range coder
4. ✅ Comprehensive testing
5. 📝 Document algorithms

**Next milestone**: Begin CELT decoder implementation (Phase 3)

## Notes

- **No shortcuts**: Every algorithm implemented fully
- **Test-driven**: Write tests before or alongside implementation
- **Incremental**: Small, verifiable steps
- **Document**: Explain complex algorithms inline
- **Validate**: Compare with libopus at every stage

---

Last Updated: 2026-01-02
Status: Phase 2 Foundation (75% complete)
