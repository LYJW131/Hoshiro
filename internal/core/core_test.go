package core

import (
	"context"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/hotkey"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/providers"
	"github.com/IcyBlue17/hoshiro/internal/store"
)

type fakeStream struct{}

func (fakeStream) Stop() {}

func speech(secs float64) []float32 {
	n := int(secs * 16000)
	out := make([]float32, n)
	for i := range out {
		out[i] = 0.4 * float32(math.Sin(2*math.Pi*220*float64(i)/16000)) * float32(0.6+0.4*math.Sin(2*math.Pi*3*float64(i)/16000))
	}
	return out
}

type feeder struct {
	mu sync.Mutex
	f  func([]float32)
}

func (f *feeder) set(fn func([]float32)) { f.mu.Lock(); f.f = fn; f.mu.Unlock() }
func (f *feeder) ready() bool           { f.mu.Lock(); defer f.mu.Unlock(); return f.f != nil }
func (f *feeder) push(b []float32)      { f.mu.Lock(); fn := f.f; f.mu.Unlock(); fn(b) }

func newTestApp(t *testing.T) (*App, *feeder) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "h.db"), filepath.Join(dir, "rec"))
	if err != nil {
		t.Fatal(err)
	}
	s := model.DefaultSettings()
	s.Asr.Mimo.APIKey = "k"
	s.Llm.Gemini.APIKey = "g"
	s.InteractionSounds = false
	s.MuteWhileDictating = false
	s.Style.AutoAddDictionary, s.Style.AIPersonalization = false, false
	a := New(Paths{dir, filepath.Join(dir, "h.db"), filepath.Join(dir, "rec")}, s, db, "test")
	feed := &feeder{}
	a.recorder = audio.NewRecorder(func(string, func([]float32), func(string)) (uint32, audio.Stream, error) {
		return 16000, fakeStream{}, nil
	})
	a.recorder.Open = func(_ string, on func([]float32), _ func(string)) (uint32, audio.Stream, error) {
		feed.set(on)
		return 16000, fakeStream{}, nil
	}
	warmUp = func(model.Settings) {}
	go a.controllerLoop()
	t.Cleanup(func() { a.stop(); db.Close() })
	return a, feed
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for " + what)
}

func TestDictationFlow(t *testing.T) {
	a, feed := newTestApp(t)
	var mu sync.Mutex
	var asrCalls int
	recognizeFn = func(_ context.Context, _ model.AsrConfig, wav []byte, _ string, _ []string) (string, error) {
		mu.Lock()
		asrCalls++
		mu.Unlock()
		return "嗯 你好 世界", nil
	}
	rewriteFn = func(_ context.Context, _ model.LlmConfig, r *providers.RewriteRequest) (providers.Outcome, error) {
		return providers.Outcome{Kind: providers.Insert, Text: "你好，世界。"}, nil
	}
	defer func() { recognizeFn, rewriteFn = providers.Recognize, providers.Rewrite }()

	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.Start, Mode: model.ModeDictation}})
	waitFor(t, "recording", func() bool { return a.Phase() == hotkey.RecordingPtt && feed.ready() })
	if a.Bar().State.Phase != model.BarRecording {
		t.Fatal(a.Bar().State)
	}
	feed.push(speech(2))
	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.Finish}})
	waitFor(t, "idle", func() bool { return a.Phase() == hotkey.Idle })
	items, err := a.ListHistory(model.HistoryQuery{Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	it := items[0]
	if it.Status != "completed" || it.RefinedText == nil || *it.RefinedText != "你好，世界。" {
		t.Fatalf("%+v", it)
	}
	if r, _ := a.DB.GetHistory(it.ID); r.AsrProvider == nil || !strings.HasPrefix(*r.AsrProvider, "MiMo") || r.LlmModel == nil || r.TotalMs == nil {
		t.Fatalf("%+v", r)
	}
	// Linux has no text insertion: the copy fallback alert must show.
	if !slices.ContainsFunc(a.Bar().Alerts, func(x Alert) bool { return x.Kind == "insert_failed" }) {
		t.Fatal(a.Bar().Alerts)
	}
	if text, ok := a.lastTranscript(); !ok || text != "你好，世界。" {
		t.Fatal(text)
	}
	if asrCalls == 0 {
		t.Fatal("no asr")
	}
	if a.Bar().State.Phase != model.BarHidden {
		t.Fatal(a.Bar().State.Phase)
	}
}

func TestCancelThenUndo(t *testing.T) {
	a, feed := newTestApp(t)
	block := make(chan struct{})
	recognizeFn = func(ctx context.Context, _ model.AsrConfig, _ []byte, _ string, _ []string) (string, error) {
		select {
		case <-block:
			return "hello there", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	rewriteFn = func(_ context.Context, _ model.LlmConfig, r *providers.RewriteRequest) (providers.Outcome, error) {
		return providers.Outcome{Kind: providers.Insert, Text: "Hello there."}, nil
	}
	defer func() { recognizeFn, rewriteFn = providers.Recognize, providers.Rewrite }()

	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.Start, Mode: model.ModeDictation, Locked: true}})
	waitFor(t, "recording", func() bool { return a.Phase() == hotkey.RecordingLocked && feed.ready() })
	feed.push(speech(1.5))
	a.BarAction("finish")
	waitFor(t, "processing", func() bool { return a.Phase() == hotkey.Processing })
	// a second start while busy is refused with an alert
	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.Start, Mode: model.ModeDictation}})
	waitFor(t, "busy", func() bool {
		return slices.ContainsFunc(a.Bar().Alerts, func(x Alert) bool { return x.Kind == "busy" })
	})
	a.BarAction("cancel")
	waitFor(t, "cancelled", func() bool {
		return slices.ContainsFunc(a.Bar().Alerts, func(x Alert) bool { return x.Kind == "transcript_cancelled" })
	})
	items, _ := a.ListHistory(model.HistoryQuery{Limit: 10})
	if len(items) != 1 || items[0].Status != "dismissed" || !items[0].HasAudio {
		t.Fatalf("%+v", items)
	}
	var undo Alert
	for _, x := range a.Bar().Alerts {
		if x.Kind == "transcript_cancelled" {
			undo = x
		}
	}
	close(block)
	a.AlertAction(undo.ID, "undo")
	waitFor(t, "reprocessed", func() bool {
		items, _ := a.ListHistory(model.HistoryQuery{Limit: 10})
		return len(items) == 1 && items[0].Status == "completed"
	})
	waitFor(t, "idle", func() bool { return a.Phase() == hotkey.Idle })
}

func TestTapTooShortHintAndMissingKey(t *testing.T) {
	a, feed := newTestApp(t)
	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.Start, Mode: model.ModeDictation}})
	waitFor(t, "recording", func() bool { return feed.ready() && a.Phase() == hotkey.RecordingPtt })
	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.TapTooShort}})
	waitFor(t, "hint", func() bool {
		return slices.ContainsFunc(a.Bar().Alerts, func(x Alert) bool { return x.Kind == "hint" && strings.Contains(x.Title, "[[fn]]") })
	})
	a.UpdateSettings(func(s *model.Settings) { s.Asr.Mimo.APIKey = "" })
	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.Start, Mode: model.ModeDictation}})
	waitFor(t, "missing key", func() bool {
		return slices.ContainsFunc(a.Bar().Alerts, func(x Alert) bool { return x.Kind == "missing_key" })
	})
	if a.Phase() != hotkey.Idle {
		t.Fatal(a.Phase())
	}
}

func TestAlertsReplaceByKindAndExpire(t *testing.T) {
	a, _ := newTestApp(t)
	a.alert("x", "one", "info").show()
	a.alert("x", "two", "info").duration(1).show()
	al := a.Bar().Alerts
	if len(al) != 1 || al[0].Title != "two" {
		t.Fatal(al)
	}
	time.Sleep(5 * time.Millisecond)
	if len(a.Bar().Alerts) != 0 {
		t.Fatal("not expired")
	}
}

func TestSplitSamples(t *testing.T) {
	if len(SplitSamples(make([]int16, 60*16000))) != 1 {
		t.Fatal()
	}
	sr := 16000
	s := make([]int16, 300*sr)
	for i := range s {
		s[i] = 5000
	}
	for i := 80 * sr; i < 80*sr+3200; i++ {
		s[i] = 0
	}
	parts := SplitSamples(s)
	total := 0
	for _, p := range parts {
		total += len(p)
		if len(p) > 120*sr {
			t.Fatal("too long")
		}
	}
	if len(parts) < 3 || total != len(s) || math.Abs(float64(len(parts[0])-(80*sr+800))) >= 2000 {
		t.Fatal(len(parts), len(parts[0]))
	}
}

func TestWordsAndCSV(t *testing.T) {
	if w, ok := NormalizeWord("  Typeless \n"); !ok || w != "Typeless" {
		t.Fatal(w)
	}
	if _, ok := NormalizeWord(""); ok {
		t.Fatal()
	}
	if _, ok := NormalizeWord(strings.Repeat("a", 65)); ok {
		t.Fatal()
	}
	if _, ok := NormalizeWord(strings.Repeat("字", 64)); !ok {
		t.Fatal()
	}
	if _, ok := NormalizeWord("a\nb"); ok {
		t.Fatal()
	}
	cases := map[string][]string{
		"word\nTauri\nMiMo\ntauri\n":                      {"Tauri", "MiMo"},
		"\ufeffHoshiro,ignored\r\n\"Hello, World\",x\r\n": {"Hoshiro", "Hello, World"},
		"词汇\n星辰\n\n  \n":                                  {"星辰"},
		"\"say \"\"hi\"\"\"\nplain":                       {"say \"hi\"", "plain"},
		strings.Repeat("x", 70) + "\nok":                  {"ok"},
	}
	for in, want := range cases {
		if got := ParseCSVWords(in); !slices.Equal(got, want) {
			t.Errorf("%q: %q", in, got)
		}
	}
	if len(ParseCSVWords("")) != 0 {
		t.Fatal()
	}
}

func TestSettingsCycleTranslationTarget(t *testing.T) {
	a, _ := newTestApp(t)
	a.UpdateSettings(func(s *model.Settings) { s.TranslationTargets = []string{"en-US", "ja-JP"} })
	a.BarAction("cycle_translation_target")
	waitFor(t, "cycled", func() bool { return a.Settings().TranslationTargets[0] == "ja-JP" })
}
