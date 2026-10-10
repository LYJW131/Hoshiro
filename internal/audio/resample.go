package audio

import "math"

// TargetSampleRate is the rate of everything handed to the rest of the app.
const TargetSampleRate = 16_000

const (
	zeros    = 16.0
	tableRes = 256
	cutoff   = 0.92
)

// Resampler is a streaming windowed-sinc resampler (mono f32), anti-aliased for downsampling.
type Resampler struct {
	passthrough bool
	step        float64
	half        int
	table       []float32
	buf         []float32
	pos         float64
}

func sinc(x float64) float64 {
	if math.Abs(x) < 1e-9 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

func NewResampler(in, out uint32) *Resampler {
	pass := in == out || in == 0 || out == 0
	step := 1.0
	if !pass {
		step = float64(in) / float64(out)
	}
	scale := math.Min(float64(out)/float64(max(in, 1)), 1) * cutoff
	half := int(math.Ceil(zeros / scale))
	n := half*tableRes + 2
	table := make([]float32, n)
	for i := range n {
		x := float64(i) / tableRes
		u := math.Min(x/float64(half), 1)
		w := 0.42 + 0.5*math.Cos(math.Pi*u) + 0.08*math.Cos(2*math.Pi*u)
		table[i] = float32(scale * sinc(scale*x) * w)
	}
	return &Resampler{passthrough: pass, step: step, half: half, table: table, buf: make([]float32, half), pos: float64(half)}
}

func (r *Resampler) weight(x float64) float32 {
	a := math.Abs(x) * tableRes
	i := int(a)
	if i+1 >= len(r.table) {
		return 0
	}
	frac := float32(a - float64(i))
	return r.table[i] + (r.table[i+1]-r.table[i])*frac
}

// Process appends resampled output for input to out.
func (r *Resampler) Process(input []float32, out []float32) []float32 {
	if r.passthrough {
		return append(out, input...)
	}
	r.buf = append(r.buf, input...)
	for {
		i0 := int(math.Floor(r.pos))
		if i0+r.half >= len(r.buf) {
			break
		}
		var acc, wsum float32
		for j := i0 - r.half + 1; j <= i0+r.half; j++ {
			w := r.weight(float64(j) - r.pos)
			acc += w * r.buf[j]
			wsum += w
		}
		if wsum > 1e-6 || wsum < -1e-6 {
			out = append(out, acc/wsum)
		} else {
			out = append(out, 0)
		}
		r.pos += r.step
	}
	if keep := max(int(math.Floor(r.pos))-r.half, 0); keep > 0 {
		r.buf = append(r.buf[:0], r.buf[keep:]...)
		r.pos -= float64(keep)
	}
	return out
}

// Flush emits the samples still held back by the filter delay.
func (r *Resampler) Flush(out []float32) []float32 {
	if r.passthrough {
		return out
	}
	return r.Process(make([]float32, r.half+2), out)
}

// Resample is the one-shot form.
func Resample(input []float32, in, out uint32) []float32 {
	r := NewResampler(in, out)
	res := r.Process(input, make([]float32, 0, len(input)*int(out)/int(max(in, 1))+8))
	res = r.Flush(res)
	expected := int(uint64(len(input)) * uint64(out) / uint64(max(in, 1)))
	return res[:min(max(expected, 1), len(res))]
}

func F32ToI16(x float32) int16 {
	v := math.Round(float64(x) * 32768)
	return int16(math.Min(math.Max(v, -32768), 32767))
}
