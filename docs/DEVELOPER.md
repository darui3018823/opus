# Developer Guide

## Getting Started

### Prerequisites
- Go 1.24.11 or later (see `go.mod`)
- Basic understanding of digital signal processing
- Familiarity with audio codecs (helpful but not required)

### Clone and Build
```bash
git clone https://github.com/darui3018823/opus.git
cd opus
go mod download
go build ./...
```

### Run Tests
```bash
# All tests
go test ./...

# Specific package
go test -v ./internal/dsp/

# With coverage
go test -cover ./...

# Benchmarks
go test -bench=. ./internal/dsp/
```

## Project Structure

```
opus/
├── opus.go / constants.go / errors.go       # Single-stream public API
├── multistream.go / surround.go             # Multistream and surround APIs
├── projection.go / mapping_matrix.go        # Projection/Ambisonics APIs
├── packet.go / repacketizer.go              # Packet operations
├── oggopus/                                  # Ogg and Ogg Opus APIs
├── internal/
│   ├── celt/ / silk/                         # Codec implementations
│   ├── dsp/ / entcode/ / resampler/         # Shared codec machinery
│   ├── extensions/                           # Packet-extension internals
│   └── cgoref/                               # Optional libopus oracle
├── docs/
│   └── CURRENT_IMPLEMENTATION.md             # Authoritative status snapshot
└── scripts/                                  # Generation and diagnostic helpers
```

See `CURRENT_IMPLEMENTATION.md` for the complete current API and package map.

## Code Style

### General Guidelines
- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions focused and small
- Document all exported types and functions
- Add inline comments for complex algorithms

### Example: Function Documentation
```go
// Forward performs the forward MDCT transform.
// Input: 2*N samples, Output: N coefficients.
//
// The MDCT is a lapped transform that provides perfect reconstruction
// when used with proper windowing and overlap-add. This implementation
// uses the Vorbis window by default.
//
// Algorithm:
// 1. Apply window to input
// 2. Pre-rotation and folding
// 3. Perform FFT
// 4. Post-rotation to get MDCT coefficients
func (m *MDCT) Forward(input []float64) ([]float64, error) {
    // Implementation validates the input length and returns an error when invalid.
    return nil, nil
}
```

### Naming Conventions
- **Acronyms**: Keep uppercase (FFT, MDCT, CELT, SILK)
- **Local variables**: Short names for simple loops (i, j, k)
- **Complex operations**: Descriptive names (twiddleFactor, bandEnergy)
- **Constants**: CamelCase with context (WindowVorbis, SampleRate48kHz)

## Testing Principles

### Unit Tests
Every major function should have tests:
```go
func TestFFTRoundtrip(t *testing.T) {
    input := generateTestSignal()
    spectrum, err := FFT(input)
    if err != nil {
        t.Fatal(err)
    }
    roundTrip, err := IFFT(spectrum)
    if err != nil {
        t.Fatal(err)
    }
    for i := range input {
        if math.Abs(input[i].Real-roundTrip[i].Real) > 1e-6 ||
            math.Abs(input[i].Imag-roundTrip[i].Imag) > 1e-6 {
            t.Fatalf("sample %d differs: got=%v want=%v", i, roundTrip[i], input[i])
        }
    }
}
```

### Table-Driven Tests
For multiple test cases:
```go
func TestWindowTypes(t *testing.T) {
    tests := []struct {
        name       string
        windowType int
        size       int
    }{
        {"Hann64", WindowHann, 64},
        {"Vorbis128", WindowVorbis, 128},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            window := Window(tt.windowType, tt.size)
            // Validate...
        })
    }
}
```

### Benchmarks
Measure performance for optimization:
```go
func BenchmarkFFT1024(b *testing.B) {
    input := make([]Complex, 1024)
    // Initialize input...
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        FFT(input)
    }
}
```

## Algorithm Implementation

### Porting from libopus

When porting C code from libopus:

1. **Understand the algorithm first**
   - Read comments and documentation
   - Trace through with example values
   - Identify key operations

2. **Go idioms**
   - Use slices instead of pointers
   - Prefer clear code over micro-optimizations
   - Handle errors explicitly

3. **Numerical considerations**
   - Use float64 for primary math
   - Use int32/uint32 for exact integer operations
   - Be aware of overflow/underflow

### Example: C to Go Translation

**C code (libopus)**:
```c
void compute_mdct_forward(const float *x, float *X, int N) {
    for (int k = 0; k < N; k++) {
        float sum = 0;
        for (int n = 0; n < 2*N; n++) {
            sum += x[n] * cos(M_PI/N * (n + 0.5) * (k + 0.5));
        }
        X[k] = sum;
    }
}
```

**Illustrative Go shape** (the production implementation uses an optimized
FFT-backed path):
```go
// Forward computes the MDCT of the input signal.
func (m *MDCT) Forward(input []float64) ([]float64, error) {
    n := m.size
    if len(input) != 2*n {
        return nil, errors.New("MDCT input must contain 2*N samples")
    }
    output := make([]float64, n)
    
    for k := 0; k < n; k++ {
        sum := 0.0
        for i := 0; i < 2*n; i++ {
            angle := math.Pi / float64(n) * (float64(i) + 0.5) * (float64(k) + 0.5)
            sum += input[i] * math.Cos(angle)
        }
        output[k] = sum
    }
    
    return output, nil
}
```

**Notes**:
- Clear variable names (n instead of N)
- Explicit type conversions (float64())
- Return value instead of modifying pointer
- Make allocations explicit

## Debugging Tips

### Printf Debugging
```go
// Temporary debug output
fmt.Printf("DEBUG: fft size=%d, input[0]=%v\n", len(input), input[0])
```

### Delve Debugger
```bash
# Install
go install github.com/go-delve/delve/cmd/dlv@latest

# Debug tests
dlv test ./internal/dsp -- -test.run TestFFT

# Set breakpoint
(dlv) break fft.go:42
(dlv) continue
(dlv) print input
```

### Profiling
```bash
# CPU profile
go test -cpuprofile=cpu.prof -bench=.
go tool pprof cpu.prof

# Memory profile
go test -memprofile=mem.prof -bench=.
go tool pprof mem.prof

# Interactive
(pprof) top10
(pprof) list FFT
```

## Performance Optimization

### General Strategy
1. **Measure first** - Don't optimize without data
2. **Hot paths** - Focus on frequently called code
3. **Allocations** - Reduce garbage collector pressure
4. **Algorithm** - Sometimes a better algorithm > micro-optimizations

### Common Optimizations

**Precompute constants**:
```go
// Bad: Recompute every iteration
for i := 0; i < n; i++ {
    x := math.Sin(2 * math.Pi * float64(i) / float64(n))
}

// Good: Precompute
angles := make([]float64, n)
for i := 0; i < n; i++ {
    angles[i] = 2 * math.Pi * float64(i) / float64(n)
}
for i := 0; i < n; i++ {
    x := math.Sin(angles[i])
}
```

**Reuse buffers**:
```go
// Bad: Allocate every call
func process(input []float64) []float64 {
    output := make([]float64, len(input))
    // ...
    return output
}

// Good: Accept preallocated buffer
func process(input []float64, output []float64) {
    // Use output directly
}
```

**Inlining**:
Go decides inlining automatically; there is no supported `//go:inline`
directive. Inspect compiler decisions when needed:

```bash
go test -gcflags='-m=2' ./internal/dsp
```

## Common Pitfalls

### Floating Point Precision
```go
// Don't use == for floats
if x == 0.5 { // BAD
}

// Use tolerance
const epsilon = 1e-9
if math.Abs(x - 0.5) < epsilon { // GOOD
}
```

### Slice Gotchas
```go
// Slice header is copied, not data
a := []int{1, 2, 3}
b := a  // Same underlying array
b[0] = 99
fmt.Println(a[0])  // 99!

// Use copy for independence
b := make([]int, len(a))
copy(b, a)
```

### Integer Overflow
```go
// int32 can overflow
var x int32 = 2000000000
var y int32 = 2000000000
z := x + y  // Overflow!

// Use int64 or check bounds
var x int64 = 2000000000
var y int64 = 2000000000
z := x + y  // OK
```

## Contributing Workflow

### Development Cycle
1. **Create feature branch**
   ```bash
   git checkout -b feature/resampler
   ```

2. **Write tests first** (TDD)
   ```bash
   # Write failing test
   go test ./internal/resampler/
   ```

3. **Implement feature**
   - Small, incremental commits
   - Run tests frequently
   - Document as you go

4. **Validate**
   ```bash
   go test ./...
   go test -race ./...
   go vet ./...
   ```

5. **Commit and push**
   ```bash
   git add .
   git commit -m "feat(resampler): add polyphase resampler"
   git push origin feature/resampler
   ```

## Resources

### Opus Specification
- [RFC 6716](https://tools.ietf.org/html/rfc6716) - Opus Codec Specification
- [Opus Website](https://opus-codec.org/) - Official documentation
- [libopus Source](https://github.com/xiph/opus) - Reference implementation

### DSP Resources
- "Understanding Digital Signal Processing" by Richard Lyons
- "The Scientist and Engineer's Guide to DSP" (free online)
- [DSP StackExchange](https://dsp.stackexchange.com/)

### Go Resources
- [Effective Go](https://golang.org/doc/effective_go.html)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Go Blog](https://blog.golang.org/)

## Getting Help

### Internal Documentation
- Read `docs/CURRENT_IMPLEMENTATION.md` for authoritative implementation status
- Read `docs/ARCHITECTURE.md` for design decisions
- Check `docs/ROADMAP.md` for historical milestones and forward-looking work
- Look at existing tests for examples

### Code Comments
Most algorithms have detailed inline comments:
```go
// Cooley-Tukey FFT algorithm
// This implements the decimation-in-time radix-2 FFT
// Time complexity: O(N log N)
// Space complexity: O(N)
```

### Ask Questions
- Open GitHub issues for discussions
- Add TODO comments for future work
- Document assumptions and limitations

---

Happy coding! Remember: **No compromises on correctness.**
