package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/autodict"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/personalize"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/providers"
	"github.com/IcyBlue17/hoshiro/internal/stats"
	"github.com/IcyBlue17/hoshiro/internal/store"
)

// asrSem limits concurrent recognition requests.
var asrSem = make(chan struct{}, 3)

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// errText is an error message capped at 300 characters.
func errText(err error) string {
	s := err.Error()
	if r := []rune(s); len(r) > 300 {
		return string(r[:300]) + "…"
	}
	return s
}

// task is one running transcription (a chunk, or a whole streaming session).
type task struct {
	index  int
	done   chan struct{}
	text   string
	err    error
	cancel context.CancelFunc
}

func (t *task) wait(ctx context.Context) (string, error) {
	select {
	case <-t.done:
		return t.text, t.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func abortTasks(ts []*task) {
	for _, t := range ts {
		t.cancel()
	}
}

// recognizeFn is replaceable in tests.
var recognizeFn = providers.Recognize

func spawnASR(cfg model.AsrConfig, language string, words []string, samples []int16, tag string) *task {
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(t.done)
		select {
		case asrSem <- struct{}{}:
		case <-ctx.Done():
			t.err = ctx.Err()
			return
		}
		defer func() { <-asrSem }()
		secs := float64(len(samples)) / 16000
		start := time.Now()
		t.text, t.err = recognizeFn(ctx, cfg, audio.EncodeWAV(samples, 16000), language, words)
		if t.err != nil {
			log.Printf("[%s] asr chunk failed after %d ms: %v", tag, time.Since(start).Milliseconds(), t.err)
		} else {
			log.Printf("[%s] asr chunk %.1fs -> %d chars in %d ms", tag, secs, len([]rune(t.text)), time.Since(start).Milliseconds())
		}
	}()
	return t
}

func streamTask(s *providers.Stream) *task {
	t := &task{done: make(chan struct{}), cancel: s.Abort}
	go func() {
		defer close(t.done)
		t.text, t.err = s.Wait()
	}()
	return t
}

// SplitSamples splits long audio into <= ~90 s pieces, cutting at the quietest
// 100 ms frame of the last 15 s.
func SplitSamples(samples []int16) [][]int16 {
	const sr = 16000
	const maxSingle, target, window, frame = 120 * sr, 90 * sr, 15 * sr, sr / 10
	if len(samples) <= maxSingle {
		return [][]int16{samples}
	}
	var out [][]int16
	pos := 0
	for len(samples)-pos > maxSingle {
		lo, hi := pos+target-window, pos+target
		best, cut := uint64(math.MaxUint64), hi
		for i := lo; i+frame <= hi; i += frame / 2 {
			var e uint64
			for _, s := range samples[i : i+frame] {
				if s < 0 {
					e += uint64(-int64(s))
				} else {
					e += uint64(s)
				}
			}
			if e < best {
				best, cut = e, i+frame/2
			}
		}
		out = append(out, samples[pos:cut])
		pos = cut
	}
	return append(out, samples[pos:])
}

func asrLanguage(s model.Settings) string {
	return providers.LanguageParam(s.SelectedLanguages, s.AutoSelectLanguages)
}

func (a *App) transcribeSamples(ctx context.Context, s model.Settings, samples []int16, tag string) (string, error) {
	words, err := a.DB.WordsForPrompt(100)
	if err != nil {
		return "", err
	}
	if s.Asr.Streaming() {
		return providers.TranscribeSamples(ctx, s.Asr, s.SelectedLanguages, words, samples)
	}
	var ts []*task
	for i, part := range SplitSamples(samples) {
		ts = append(ts, spawnASR(s.Asr, asrLanguage(s), words, part, fmt.Sprintf("%s#%d", tag, i)))
	}
	defer abortTasks(ts)
	var parts []string
	for _, t := range ts {
		text, err := t.wait(ctx)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	return providers.JoinTranscripts(parts), nil
}

// ── audio files ──

// SaveAudio writes <dir>/<id>.wav atomically (mode 0600) and returns the file name.
func SaveAudio(dir, id string, samples []int16) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := id + ".wav"
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, audio.EncodeWAV(samples, 16000), 0o600); err != nil {
		return "", err
	}
	return name, os.Rename(tmp, filepath.Join(dir, name))
}

func LoadAudio(path string) ([]int16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return audio.Decode16kMono(b)
}

// ── delivery ──

type delivered int

const (
	inserted delivered = iota
	notEditable
	insertFailed
)

func deliverText(start model.AppContext, text string, replace bool) (delivered, string) {
	fresh := platform.CaptureContext(false, false)
	if start.PID != nil && fresh.PID != nil && *start.PID != *fresh.PID {
		log.Printf("frontmost app changed during the session (pid %d -> %d); pasting into the new target", *start.PID, *fresh.PID)
	}
	isTrue := func(b *bool) bool { return b != nil && *b }
	isFalse := func(b *bool) bool { return b != nil && !*b }
	if !isTrue(start.FocusedEditable) && !isTrue(fresh.FocusedEditable) && (isFalse(start.FocusedEditable) || isFalse(fresh.FocusedEditable)) {
		return notEditable, ""
	}
	var err error
	if replace {
		err = platform.ReplaceSelection(text)
	} else {
		err = platform.InsertText(text)
	}
	if err != nil {
		return insertFailed, errText(err)
	}
	return inserted, ""
}

// ── the pipeline ──

type processInput struct {
	row      store.HistoryRow // id, mode, created_at, app info, selection, duration, audio_path
	ctx      model.AppContext
	settings model.Settings
	live     []*task // running chunk transcriptions (tail included), or
	samples  []int16 // saved audio to transcribe
	deliver  bool    // insert/answer, alerts and sounds (false for the history "retry")
	store    bool    // write the history row
}

type stage struct {
	text    string
	kind    providers.OutcomeKind
	warning string
}

func (a *App) persist(row store.HistoryRow, store bool) {
	if store {
		if err := a.DB.UpsertHistory(row); err != nil {
			log.Printf("cannot write history row %s: %v", shortID(row.ID), err)
		}
	}
	a.historyChanged()
}

var rewriteFn = providers.Rewrite

func (a *App) rewriteStage(ctx context.Context, s model.Settings, mode model.Mode, raw string, actx model.AppContext, selected *string, dur float64) (stage, error) {
	ep := s.Llm.ActiveEndpoint()
	if !s.Llm.Enabled || strings.TrimSpace(ep.APIKey) == "" {
		if mode == model.ModeAsk {
			return stage{}, errors.New("问任何问题需要先在设置中启用并配置改写模型")
		}
		st := stage{text: raw, kind: providers.Insert}
		if mode == model.ModeTranslation {
			st.warning = "未配置改写模型，无法翻译"
		}
		return st, nil
	}
	dict, _ := a.DB.WordsForPrompt(300)
	req := &providers.RewriteRequest{Mode: mode, Transcript: raw, Context: actx, Dictionary: dict,
		CustomInstructions: s.Style.CustomInstructions, SmartFormatting: s.Style.SmartFormatting,
		LongLists: s.Style.LongFormStyle == "list", DurationSecs: dur}
	if mode == model.ModeAsk {
		req.SelectedText = selected
	}
	if !s.AutoSelectLanguages {
		req.Languages = s.SelectedLanguages
	}
	if mode == model.ModeTranslation && len(s.TranslationTargets) > 0 {
		req.TranslationTarget = &s.TranslationTargets[0]
	}
	if s.Style.AIPersonalization {
		if p, err := a.DB.Profile(); err == nil {
			req.Profile = &p
		}
	}
	limit := time.Duration(int(s.Llm.TimeoutSecs)+int(math.Ceil(dur/60*6))+8) * time.Second
	cctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := rewriteFn(cctx, s.Llm, req)
	if err != nil && ctx.Err() == nil && cctx.Err() != nil {
		err = errors.New("改写超时")
	}
	if err != nil {
		if ctx.Err() != nil {
			return stage{}, ctx.Err()
		}
		if mode == model.ModeAsk {
			return stage{}, errors.New(errText(err))
		}
		return stage{text: raw, kind: providers.Insert, warning: errText(err)}, nil
	}
	if strings.TrimSpace(out.Text) == "" {
		if mode == model.ModeAsk {
			return stage{}, errors.New("改写模型返回了空内容")
		}
		return stage{text: raw, kind: providers.Insert, warning: "改写模型返回了空内容"}, nil
	}
	return stage{text: out.Text, kind: out.Kind}, nil
}

// AsrLabel is "Provider · model", as the usage table labels the same request.
func AsrLabel(s model.Settings) string {
	switch s.Asr.Provider {
	case "cloudflare":
		return "Cloudflare · " + s.Asr.Cloudflare.Model
	case "azure":
		return "Azure Speech · streaming"
	case "google":
		return "Google Speech · " + s.Asr.Google.Model
	}
	return "MiMo · " + s.Asr.Mimo.Model
}

func LlmLabel(s model.Settings) string {
	return personalize.ProviderName(s.Llm.Provider) + " · " + s.Llm.ActiveEndpoint().Model
}

func (a *App) fail(row store.HistoryRow, msg string, in processInput) store.HistoryRow {
	log.Printf("[%s] session failed: %s", shortID(row.ID), msg)
	row.Status = "error"
	row.ErrorMessage = &msg
	a.persist(row, in.store)
	if in.deliver {
		a.play(SoundError)
		a.alert("transcription_error", "出现问题", "error").desc(msg).action("retry", "重试", "primary").
			duration(12_000).session(row.ID).show()
	}
	return row
}

func u32ms(d time.Duration) *uint32 {
	v := uint32(min(d.Milliseconds(), math.MaxUint32))
	return &v
}

// process runs ASR → rewrite → delivery → history. A cancelled ctx stops it
// without writing anything further (the controller marks the row dismissed).
func (a *App) process(ctx context.Context, in processInput) store.HistoryRow {
	row, s, mode := in.row, in.settings, in.row.Mode
	tag := shortID(row.ID)
	t0 := time.Now()

	// 1. transcript
	var raw string
	if in.live != nil {
		defer abortTasks(in.live)
		ts := slices.Clone(in.live)
		slices.SortFunc(ts, func(x, y *task) int { return x.index - y.index })
		var parts []string
		for _, t := range ts {
			text, err := t.wait(ctx)
			if ctx.Err() != nil {
				return row
			}
			if err != nil {
				return a.fail(row, errText(err), in)
			}
			parts = append(parts, text)
		}
		raw = providers.JoinTranscripts(parts)
	} else {
		text, err := a.transcribeSamples(ctx, s, in.samples, tag)
		if ctx.Err() != nil {
			return row
		}
		if err != nil {
			return a.fail(row, errText(err), in)
		}
		raw = text
	}
	asrDur := time.Since(t0)
	if strings.TrimSpace(raw) == "" {
		log.Printf("[%s] empty transcript (asr wait %d ms)", tag, asrDur.Milliseconds())
		row.Status, row.RawText, row.ErrorMessage = "empty", nil, nil
		a.persist(row, in.store)
		return row
	}
	row.RawText = &raw

	// 2. rewrite
	t1 := time.Now()
	st, err := a.rewriteStage(ctx, s, mode, raw, in.ctx, row.SelectedText, row.DurationSecs)
	if ctx.Err() != nil {
		return row
	}
	if err != nil {
		return a.fail(row, err.Error(), in)
	}
	llmDur := time.Since(t1)
	if st.warning != "" {
		log.Printf("[%s] rewrite fallback to raw text: %s", tag, st.warning)
		if in.deliver {
			a.alert("rewrite_failed", "改写失败，已插入原始转录", "warning").desc(st.warning).show()
		}
	}

	// 3. deliver
	didInsert := false
	if in.deliver {
		switch st.kind {
		case providers.Insert, providers.Replace:
			res, msg := deliverText(in.ctx, st.text, st.kind == providers.Replace)
			switch res {
			case inserted:
				didInsert = true
			case notEditable:
				a.alert("copy_last", "复制最后的转录", "info").desc("当前没有可输入的文本框").
					action("copy", "复制", "primary").duration(10_000).text(st.text).show()
			case insertFailed:
				log.Printf("[%s] insert failed: %s", tag, msg)
				a.play(SoundError)
				a.alert("insert_failed", "无法插入文本", "error").desc(msg).
					action("copy", "复制", "primary").duration(10_000).text(st.text).show()
			}
		case providers.Answer:
			a.showAnswer(model.AskAnswer{ID: row.ID, Question: raw, Answer: st.text})
		}
	}

	// 4. history
	if in.deliver {
		// Live sessions only: a history retry would otherwise overwrite the original timings.
		used := s.Llm.Enabled && strings.TrimSpace(s.Llm.ActiveEndpoint().APIKey) != ""
		row.AsrMs, row.TotalMs, row.LlmMs, row.LlmModel = u32ms(asrDur), u32ms(time.Since(t0)), nil, nil
		label := AsrLabel(s)
		row.AsrProvider = &label
		if used {
			l := LlmLabel(s)
			row.LlmMs, row.LlmModel = u32ms(llmDur), &l
		}
	}
	row.Status, row.ErrorMessage = "completed", nil
	row.Words = stats.CountWords(st.text)
	text := st.text
	row.RefinedText = &text
	a.persist(row, in.store)
	if in.deliver {
		a.lastText.Store(&text)
	}
	log.Printf("[%s] done mode=%s audio=%.1fs asr_wait=%dms llm=%dms total=%dms chars=%d", tag, mode, row.DurationSecs,
		asrDur.Milliseconds(), llmDur.Milliseconds(), time.Since(t0).Milliseconds(), len([]rune(text)))

	// 5. auto-dictionary
	if didInsert && mode == model.ModeDictation && (s.Style.AutoAddDictionary || s.Style.AIPersonalization) {
		go a.autoDictionary(text, row.ID, row.AppName, in.ctx.PID, in.store)
	}
	return row
}

func (a *App) autoDictionary(insertedText, id string, appName *string, pid *int32, store bool) {
	var latest string
	var have bool
	for _, wait := range []time.Duration{3, 7, 20} {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(wait * time.Second):
		}
		cur := platform.CaptureContext(false, false)
		if pid != nil && (cur.PID == nil || *cur.PID != *pid) {
			continue
		}
		field, ok := platform.FocusedTextValue()
		if !ok {
			continue
		}
		region, ok := autodict.EditedRegion(insertedText, field)
		if !ok {
			continue
		}
		latest, have = region, true
		if a.Settings().Style.AutoAddDictionary {
			if word, ok := autodict.DetectCorrection(insertedText, region); ok {
				if added, err := a.DB.AddWords([]string{word}, "auto"); err == nil && len(added) > 0 {
					a.hub("dictionary")
					a.historyChanged()
					a.alert("dictionary_added", "“"+word+"” 已添加到您的词典", "success").duration(3500).show()
				}
			}
		}
	}
	s := a.Settings()
	if store && s.HistoryRetention != "never" && s.Style.AIPersonalization && have && latest != insertedText {
		if err := a.DB.SaveEdit(id, insertedText, latest, appName); err != nil {
			log.Printf("cannot save edit: %v", err)
		}
	}
}

// weakNetGuard shows "connection slower than usual" after 7 s of processing.
type weakNetGuard struct {
	mu      sync.Mutex
	stopped bool
	id      string
	timer   *time.Timer
	a       *App
}

func (a *App) startWeakNet() *weakNetGuard {
	g := &weakNetGuard{a: a}
	g.timer = time.AfterFunc(7*time.Second, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.stopped {
			g.id = a.alert("weak_network", "连接比平常慢…", "warning").duration(60_000).show()
		}
	})
	return g
}

func (g *weakNetGuard) stop() {
	g.timer.Stop()
	g.mu.Lock()
	g.stopped = true
	id := g.id
	g.mu.Unlock()
	if id != "" {
		g.a.DismissAlert(id)
	}
}
