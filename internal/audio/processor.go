package audio

import (
	"log"
	"math"
	"strings"
)

// Chunk is a piece of a recording cut at a natural pause, 16 kHz mono.
type Chunk struct {
	Index   int
	Samples []int16
}

// StopResult is what a finished recording leaves.
type StopResult struct {
	Tail         Chunk   // the final, not yet emitted chunk (may be empty)
	AllSamples   []int16 // the whole recording, for history audio
	DurationSecs float64
	IsSilent     bool // no speech detected
}

// Sink receives what a recording produces; callbacks run on the recorder's worker goroutine.
type Sink interface {
	OnLevel(level float32)
	OnChunk(c Chunk)
	OnPCM(samples []int16) // live PCM before chunking (streaming recognizers)
	OnError(msg string)    // the input device failed mid-recording
}

// ClassifyDevice guesses a microphone's kind from its name.
func ClassifyDevice(name string) string {
	n := strings.ToLower(name)
	has := func(ks ...string) bool {
		for _, k := range ks {
			if strings.Contains(n, k) {
				return true
			}
		}
		return false
	}
	switch {
	case has("blackhole", "loopback", "virtual", "teams", "zoom", "soundflower", "aggregate", "multi-output", "obs"):
		return "virtual"
	case has("airpods", "bluetooth", "buds", "beats", "wh-1000", "wf-1000", "powerbeats"):
		return "bluetooth"
	case has("macbook", "built-in", "builtin", "内建", "内置", "imac", "mac mini", "mac studio", "studio display"):
		return "builtin"
	case has("usb"):
		return "usb"
	}
	return "unknown"
}

const (
	frameLen         = 512 // 32 ms @ 16 kHz
	frameMs          = 32
	levelDBMin       = -55.0
	levelDBMax       = -12.0
	minSpeechMs      = 250
	noiseWindowFrame = 96
	vadMarginDB      = 9.0
	vadMinDB         = -54.0
	vadMaxDB         = -38.0
)

// ChunkConfig sets where long recordings are cut: once a chunk is >= Min,
// at the next pause of PauseFrames; always at Max.
type ChunkConfig struct {
	MinChunkSamples int
	PauseFrames     int
	MaxChunkSamples int
}

func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{MinChunkSamples: 25 * TargetSampleRate, PauseFrames: (350 + frameMs - 1) / frameMs, MaxChunkSamples: 90 * TargetSampleRate}
}

// Processor turns device audio into 16 kHz PCM, levels, VAD and chunks.
type Processor struct {
	sink                         Sink
	rs                           *Resampler
	cfg                          ChunkConfig
	frameBuf, cur, all           []int16
	nextIndex                    int
	curSpeech, totalSpeech, sil  int
	recent                       []float32
	level                        float32
}

func frameDB(f []int16) float32 {
	if len(f) == 0 {
		return -100
	}
	var sum float64
	for _, s := range f {
		x := float64(s) / 32768
		sum += x * x
	}
	return float32(20 * math.Log10(math.Sqrt(sum/float64(len(f)))+1e-7))
}

func NewProcessor(inRate uint32, sink Sink, cfg ChunkConfig) *Processor {
	r := make([]float32, noiseWindowFrame)
	for i := range r {
		r[i] = -60
	}
	return &Processor{sink: sink, rs: NewResampler(inRate, TargetSampleRate), cfg: cfg, recent: r}
}

// Push feeds mono f32 samples at the device rate.
func (p *Processor) Push(in []float32) { p.push16k(p.rs.Process(in, nil)) }

func (p *Processor) push16k(s []float32) {
	pcm := make([]int16, len(s))
	for i, v := range s {
		pcm[i] = F32ToI16(v)
	}
	if len(pcm) > 0 {
		p.sink.OnPCM(pcm)
	}
	p.frameBuf = append(p.frameBuf, pcm...)
	for len(p.frameBuf) >= frameLen {
		f := append([]int16(nil), p.frameBuf[:frameLen]...)
		p.frameBuf = append(p.frameBuf[:0], p.frameBuf[frameLen:]...)
		p.analyze(f)
	}
}

func (p *Processor) analyze(f []int16) {
	db := frameDB(f)
	p.recent = append(p.recent, db)
	if len(p.recent) > noiseWindowFrame {
		p.recent = p.recent[1:]
	}
	floor := float32(math.MaxFloat32)
	for _, v := range p.recent {
		floor = min(floor, v)
	}
	th := min(max(floor+vadMarginDB, vadMinDB), vadMaxDB)
	if db > th {
		p.curSpeech++
		p.totalSpeech++
		p.sil = 0
	} else {
		p.sil++
	}
	norm := min(max((db-levelDBMin)/(levelDBMax-levelDBMin), 0), 1)
	coeff := float32(0.18)
	if norm > p.level {
		coeff = 0.65
	}
	p.level += (norm - p.level) * coeff
	p.sink.OnLevel(p.level)
	p.all = append(p.all, f...)
	p.cur = append(p.cur, f...)
	atPause := len(p.cur) >= p.cfg.MinChunkSamples && p.sil >= p.cfg.PauseFrames
	if atPause || len(p.cur) >= p.cfg.MaxChunkSamples {
		p.emit()
	}
}

func (p *Processor) emit() {
	s := p.cur
	p.cur = nil
	speechMs := p.curSpeech * frameMs
	p.curSpeech = 0
	if speechMs < 150 {
		log.Printf("dropping silent chunk of %d samples", len(s))
		return
	}
	c := Chunk{p.nextIndex, s}
	p.nextIndex++
	p.sink.OnChunk(c)
}

// Finish flushes the filter and returns the result.
func (p *Processor) Finish() StopResult {
	p.push16k(p.rs.Flush(nil))
	if len(p.frameBuf) > 0 {
		p.all = append(p.all, p.frameBuf...)
		p.cur = append(p.cur, p.frameBuf...)
		p.frameBuf = nil
	}
	tail := p.cur
	if p.nextIndex > 0 && p.curSpeech*frameMs < 150 {
		tail = nil
	}
	return StopResult{
		Tail:         Chunk{p.nextIndex, tail},
		AllSamples:   p.all,
		DurationSecs: float64(len(p.all)) / TargetSampleRate,
		IsSilent:     p.totalSpeech*frameMs < minSpeechMs,
	}
}
