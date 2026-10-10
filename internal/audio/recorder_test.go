package audio

import (
	"errors"
	"testing"
)

type fakeStream struct{ stopped bool }

func (f *fakeStream) Stop() { f.stopped = true }

func TestRecorderFallbackAndStop(t *testing.T) {
	var opened []string
	var feed func([]float32)
	r := NewRecorder(func(dev string, on func([]float32), _ func(string)) (uint32, Stream, error) {
		opened = append(opened, dev)
		if dev == "Gone Mic" {
			return 0, nil, errors.New("missing")
		}
		feed = on
		return 16000, &fakeStream{}, nil
	})
	if err := r.Start("Gone Mic", &testSink{}); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[1] != "" || !r.IsRecording() {
		t.Fatal(opened)
	}
	feed(speech(1))
	res, err := r.Stop()
	if err != nil || res.IsSilent || len(res.AllSamples) < 15000 {
		t.Fatal(err, res.IsSilent, len(res.AllSamples))
	}
	if _, err := r.Stop(); err == nil {
		t.Fatal("second stop")
	}
	r.Start("", &testSink{})
	r.Cancel()
	if r.IsRecording() {
		t.Fatal()
	}
}
