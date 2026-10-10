package audio

import (
	"math"
	"sync"
	"testing"
)

type testSink struct {
	mu     sync.Mutex
	chunks []Chunk
	levels []float32
}

func (s *testSink) OnLevel(l float32) { s.levels = append(s.levels, l) }
func (s *testSink) OnChunk(c Chunk)   { s.chunks = append(s.chunks, c) }
func (s *testSink) OnPCM([]int16)     {}
func (s *testSink) OnError(string)    {}

const sr = 16000

func speech(secs float64) []float32 {
	out := make([]float32, int(sr*secs))
	for n := range out {
		t := float64(n) / sr
		env := 0.6 + 0.4*math.Sin(2*math.Pi*4*t)
		out[n] = float32(0.12 * env * (math.Sin(2*math.Pi*200*t) + 0.5*math.Sin(2*math.Pi*1300*t)))
	}
	return out
}

func silence(secs float64) []float32 {
	out := make([]float32, int(sr*secs))
	for n := range out {
		out[n] = float32(0.0003 * (float64(n*7919%101)/50 - 1))
	}
	return out
}

func run(parts [][]float32, cfg ChunkConfig) (*testSink, StopResult) {
	s := &testSink{}
	p := NewProcessor(sr, s, cfg)
	for _, part := range parts {
		for i := 0; i < len(part); i += 480 {
			p.Push(part[i:min(i+480, len(part))])
		}
	}
	return s, p.Finish()
}

func small() ChunkConfig { return ChunkConfig{2 * sr, 11, 6 * sr} }

func TestPureSilence(t *testing.T) {
	s, r := run([][]float32{silence(2)}, DefaultChunkConfig())
	if !r.IsSilent || len(s.chunks) != 0 || math.Abs(r.DurationSecs-2) > 0.05 {
		t.Fatal(r.IsSilent, r.DurationSecs)
	}
	for _, l := range s.levels {
		if l >= 0.05 {
			t.Fatal(l)
		}
	}
}

func TestBlipVsSpeech(t *testing.T) {
	if _, r := run([][]float32{silence(0.5), speech(0.1), silence(0.5)}, DefaultChunkConfig()); !r.IsSilent {
		t.Fatal("blip")
	}
	_, r := run([][]float32{silence(0.3), speech(1), silence(0.3)}, DefaultChunkConfig())
	if r.IsSilent || r.Tail.Index != 0 || len(r.Tail.Samples) != len(r.AllSamples) {
		t.Fatal(r.IsSilent)
	}
	if _, r := run([][]float32{speech(0.6)}, DefaultChunkConfig()); r.IsSilent {
		t.Fatal("start")
	}
}

func TestLevels(t *testing.T) {
	s, _ := run([][]float32{silence(0.5), speech(1), silence(1)}, DefaultChunkConfig())
	var peak float32
	for _, l := range s.levels {
		peak = max(peak, l)
	}
	if peak <= 0.6 || peak > 1 || s.levels[len(s.levels)-1] >= 0.1 {
		t.Fatal(peak)
	}
}

func TestCutsAtPause(t *testing.T) {
	s, r := run([][]float32{speech(1.5), silence(0.2), speech(1), silence(0.6), speech(1)}, small())
	if len(s.chunks) != 1 || s.chunks[0].Index != 0 || r.Tail.Index != 1 || r.IsSilent {
		t.Fatal(len(s.chunks))
	}
	if secs := float64(len(s.chunks[0].Samples)) / sr; secs <= 3 || secs >= 3.2 {
		t.Fatal(secs)
	}
	if len(s.chunks[0].Samples)+len(r.Tail.Samples) != len(r.AllSamples) {
		t.Fatal("lost samples")
	}
	if s, r := run([][]float32{speech(0.8), silence(0.6), speech(0.8)}, small()); len(s.chunks) != 0 || r.Tail.Index != 0 {
		t.Fatal("early cut")
	}
}

func TestForcedCut(t *testing.T) {
	s, r := run([][]float32{speech(13.5)}, small())
	if len(s.chunks) != 2 || len(s.chunks[0].Samples) < 6*sr || len(s.chunks[0].Samples) >= 6*sr+frameLen || r.Tail.Index != 2 {
		t.Fatal(len(s.chunks))
	}
	total := len(r.Tail.Samples)
	for _, c := range s.chunks {
		total += len(c.Samples)
	}
	if total != len(r.AllSamples) {
		t.Fatal("total")
	}
}

func TestSilentTailDropped(t *testing.T) {
	s, r := run([][]float32{speech(2.5), silence(0.6), silence(1)}, small())
	if len(s.chunks) != 1 || len(r.Tail.Samples) != 0 || len(r.AllSamples) <= 4*sr {
		t.Fatal(len(s.chunks), len(r.Tail.Samples))
	}
}

func TestDownsamplingEndToEnd(t *testing.T) {
	p := NewProcessor(48000, &testSink{}, DefaultChunkConfig())
	in := make([]float32, 48000)
	for n := range in {
		in[n] = float32(0.3 * math.Sin(2*math.Pi*1000*float64(n)/48000))
	}
	for i := 0; i < len(in); i += 1024 {
		p.Push(in[i:min(i+1024, len(in))])
	}
	r := p.Finish()
	if d := len(r.AllSamples) - 16000; d > 600 || d < -600 || r.IsSilent {
		t.Fatal(len(r.AllSamples))
	}
}

func TestDeviceKinds(t *testing.T) {
	for n, k := range map[string]string{"Michiko's AirPods Pro": "bluetooth", "MacBook Pro Microphone": "builtin", "MacBook Pro麦克风": "builtin",
		"USB Audio Device": "usb", "BlackHole 2ch": "virtual", "Microsoft Teams Audio": "virtual", "Scarlett 2i2": "unknown"} {
		if ClassifyDevice(n) != k {
			t.Error(n)
		}
	}
}
