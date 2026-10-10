package audio

import (
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Stream is an open input stream; Stop closes it and returns once no more samples will arrive.
type Stream interface{ Stop() }

// Opener opens an input device ("" = the default one) delivering mono f32
// samples at the returned rate. The platform layer provides it.
type Opener func(device string, onSamples func([]float32), onError func(string)) (rate uint32, s Stream, err error)

type recMsg struct {
	audio  []float32
	err    string
	finish chan StopResult
	cancel bool
}

type session struct {
	stream Stream
	msgs   chan recMsg
	done   chan struct{}
	failed atomic.Bool
}

// Recorder records one session at a time: device -> Processor on a worker goroutine.
type Recorder struct {
	Open  Opener
	Chunk ChunkConfig
	mu    sync.Mutex
	cur   *session
}

func NewRecorder(open Opener) *Recorder { return &Recorder{Open: open, Chunk: DefaultChunkConfig()} }

// Start opens the device (falling back to the default input) and starts processing.
func (r *Recorder) Start(device string, sink Sink) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur != nil {
		log.Print("recorder.Start while already recording; discarding the previous session")
		r.shutdown(r.cur)
		r.cur = nil
	}
	s := &session{msgs: make(chan recMsg, 1024), done: make(chan struct{})}
	onSamples := func(b []float32) {
		select {
		case s.msgs <- recMsg{audio: b}:
		default:
			log.Print("audio worker behind; dropping a buffer")
		}
	}
	onError := func(e string) {
		select {
		case s.msgs <- recMsg{err: e}:
		default:
		}
	}
	if r.Open == nil {
		return errors.New("录音不支持此系统")
	}
	rate, stream, err := r.Open(device, onSamples, onError)
	if err != nil && device != "" {
		log.Printf("microphone %q failed (%v), falling back to the default input", device, err)
		rate, stream, err = r.Open("", onSamples, onError)
	}
	if err != nil {
		return err
	}
	s.stream = stream
	go r.worker(rate, sink, s)
	r.cur = s
	return nil
}

func (r *Recorder) worker(rate uint32, sink Sink, s *session) {
	defer close(s.done)
	p := NewProcessor(rate, sink, r.Chunk)
	for m := range s.msgs {
		switch {
		case m.cancel:
			return
		case m.finish != nil:
			m.finish <- p.Finish()
			return
		case m.err != "":
			log.Printf("audio input error: %s", m.err)
			if !s.failed.Swap(true) {
				sink.OnError(m.err)
			}
		default:
			p.Push(m.audio)
		}
	}
}

func (r *Recorder) shutdown(s *session) {
	s.stream.Stop()
	s.msgs <- recMsg{cancel: true}
	<-s.done
}

// Stop ends the recording and returns everything captured.
func (r *Recorder) Stop() (StopResult, error) {
	r.mu.Lock()
	s := r.cur
	r.cur = nil
	r.mu.Unlock()
	if s == nil {
		return StopResult{}, errors.New("not recording")
	}
	s.stream.Stop()
	reply := make(chan StopResult, 1)
	s.msgs <- recMsg{finish: reply}
	select {
	case res := <-reply:
		<-s.done
		return res, nil
	case <-time.After(10 * time.Second):
		return StopResult{}, errors.New("audio worker did not finish")
	}
}

// Cancel stops and discards.
func (r *Recorder) Cancel() {
	r.mu.Lock()
	s := r.cur
	r.cur = nil
	r.mu.Unlock()
	if s != nil {
		r.shutdown(s)
	}
}

func (r *Recorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cur != nil && !r.cur.failed.Load()
}
