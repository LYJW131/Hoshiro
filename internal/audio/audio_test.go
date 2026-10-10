package audio

import (
	"math"
	"testing"
)

func sine(freq float64, rate uint32, secs float64, amp float32) []float32 {
	n := int(float64(rate) * secs)
	out := make([]float32, n)
	for i := range out {
		out[i] = amp * float32(math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return out
}

func rms(x []float32) float32 {
	var s float32
	for _, v := range x {
		s += v * v
	}
	return float32(math.Sqrt(float64(s / float32(max(len(x), 1)))))
}

func crossings(x []float32) int {
	n := 0
	for i := 1; i < len(x); i++ {
		if x[i-1] < 0 && x[i] >= 0 {
			n++
		}
	}
	return n
}

func TestSine48kTo16kKeepsFrequency(t *testing.T) {
	out := Resample(sine(1000, 48000, 1, 0.5), 48000, 16000)
	if d := len(out) - 16000; d > 2 || d < -2 {
		t.Fatal(len(out))
	}
	mid := out[1000 : len(out)-1000]
	hz := float64(crossings(mid)) / (float64(len(mid)) / 16000)
	if math.Abs(hz-1000) >= 10 {
		t.Fatal(hz)
	}
	if r := rms(mid); math.Abs(float64(r)-0.5/math.Sqrt2) >= 0.01 {
		t.Fatal(r)
	}
}

func TestAliasingSuppressed(t *testing.T) {
	out := Resample(sine(12000, 48000, 1, 0.5), 48000, 16000)
	if r := rms(out[1000 : len(out)-1000]); r >= 0.003 {
		t.Fatal(r)
	}
}

func TestStreamingMatchesOneShot(t *testing.T) {
	in := sine(500, 44100, 1, 0.4)
	whole := Resample(in, 44100, 16000)
	r := NewResampler(44100, 16000)
	var out []float32
	for i := 0; i < len(in); i += 441 {
		out = r.Process(in[i:min(i+441, len(in))], out)
	}
	out = r.Flush(out)
	if d := len(out) - len(whole); d > 3 || d < -3 {
		t.Fatal(d)
	}
	for i := 0; i < min(len(whole), len(out))-50; i++ {
		if math.Abs(float64(whole[i]-out[i])) >= 1e-3 {
			t.Fatal(i)
		}
	}
}

func TestUpsampleAndPassthrough(t *testing.T) {
	in := sine(300, 8000, 0.5, 0.3)
	if out := Resample(in, 8000, 16000); len(out) < 7998 || len(out) > 8002 {
		t.Fatal(len(out))
	}
	if same := Resample(in, 16000, 16000); len(same) != len(in) {
		t.Fatal("passthrough")
	}
}

func TestWAVRoundTrip(t *testing.T) {
	s := make([]int16, 16000)
	for i := range s {
		s[i] = int16(math.Sin(float64(i)*0.05) * 12000)
	}
	b := EncodeWAV(s, 16000)
	if string(b[:4]) != "RIFF" || len(b) != 44+32000 {
		t.Fatal(len(b))
	}
	back, err := Decode16kMono(b)
	if err != nil || len(back) != len(s) {
		t.Fatal(err)
	}
	for i := range s {
		if back[i] != s[i] {
			t.Fatal(i, back[i], s[i])
		}
	}
	if _, err := Decode16kMono([]byte("not a wav")); err == nil {
		t.Fatal("garbage")
	}
}
