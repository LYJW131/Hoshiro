package core

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/hotkey"
	"github.com/IcyBlue17/hoshiro/internal/importer"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/providers"
	"github.com/IcyBlue17/hoshiro/internal/store"
	"github.com/google/uuid"
)

// The controller: every hotkey, bar and alert action funnels into one channel
// that a single goroutine consumes, so transitions are strictly serialized.
// Slow work (ASR, LLM, paste) runs in a processing goroutine that Cancel aborts.

const (
	maxSessionSecs  = 20 * 60
	warnSessionSecs = 19 * 60
	minChunkSamples = 1600 // 100 ms
)

type cmdKind int

const (
	cmdHotkey cmdKind = iota
	cmdBar
	cmdAlert
	cmdTick
	cmdProcessingFinished
	cmdSettingsChanged
	cmdRecorderError
)

type cmd struct {
	kind    cmdKind
	hotkey  hotkey.Action
	action  string
	alertID string
	id      string // session id
	message string
}

func (a *App) send(c cmd) {
	select {
	case a.cmds <- c:
	case <-a.ctx.Done():
	}
}

type chunkList struct {
	mu    sync.Mutex
	tasks []*task
}

func (c *chunkList) add(t *task) { c.mu.Lock(); c.tasks = append(c.tasks, t); c.mu.Unlock() }
func (c *chunkList) drain() []*task {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.tasks
	c.tasks = nil
	return t
}

type session struct {
	id        string
	mode      model.Mode
	locked    bool
	started   time.Time
	createdAt string
	asr       model.AsrConfig
	words     []string
	stream    *providers.Stream
	chunks    *chunkList
	ctxCh     chan model.AppContext
	muteMu    sync.Mutex
	muteEnded bool
	unmute    func()
	stopTick  chan struct{}
	warned    bool
}

type stored struct {
	id        string
	samples   []int16
	ctx       model.AppContext
	mode      model.Mode
	duration  float64
	createdAt string
}

type processing struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
}

type controller struct {
	sess         *session
	proc         *processing
	stash        []stored
	lastKeyAlert time.Time
}

// sink feeds a recording's output to the bar and the chunk recognizers.
type sink struct {
	a         *App
	sessionID string
	tag       string
	chunks    *chunkList
	asr       model.AsrConfig
	words     []string
	stream    *providers.Stream
	language  string
	lastLevel time.Time
}

func (s *sink) OnLevel(l float32) {
	if time.Since(s.lastLevel) < 28*time.Millisecond {
		return
	}
	s.lastLevel = time.Now()
	s.a.setLevel(min(max(l, 0), 1))
}

func (s *sink) OnError(msg string) {
	go s.a.send(cmd{kind: cmdRecorderError, id: s.sessionID, message: msg})
}

func (s *sink) OnPCM(samples []int16) {
	if s.stream != nil {
		s.stream.Send(slices.Clone(samples))
	}
}

func (s *sink) OnChunk(c audio.Chunk) {
	if s.asr.Streaming() || len(c.Samples) < minChunkSamples {
		return
	}
	log.Printf("[%s] chunk %d ready (%.1fs)", s.tag, c.Index, float64(len(c.Samples))/16000)
	t := spawnASR(s.asr, s.language, s.words, c.Samples, fmt.Sprintf("%s#%d", s.tag, c.Index))
	t.index = c.Index
	s.chunks.add(t)
}

func makeRow(id string, mode model.Mode, ctx model.AppContext, dur float64, createdAt, status string) store.HistoryRow {
	r := store.NewRow(id, mode, status)
	r.CreatedAt, r.DurationSecs = createdAt, dur
	r.AppName, r.BundleID, r.WindowTitle = ctx.AppName, ctx.BundleID, ctx.WindowTitle
	if mode == model.ModeAsk {
		r.SelectedText = ctx.SelectedText
	}
	return r
}

func ctxFromRow(r store.HistoryRow) model.AppContext {
	return model.AppContext{AppName: r.AppName, BundleID: r.BundleID, WindowTitle: r.WindowTitle, SelectedText: r.SelectedText}
}

func (a *App) controllerLoop() {
	c := &controller{}
	for {
		select {
		case <-a.ctx.Done():
			return
		case m := <-a.cmds:
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("controller panic: %v", r)
					}
				}()
				a.handle(c, m)
			}()
		}
	}
}

func (a *App) showIdleBar() {
	s := a.Settings()
	a.updateBar(func(b *model.BarState) {
		b.Phase, b.Mode, b.Locked, b.ElapsedSecs = idlePhase(s), model.ModeDictation, false, 0
	})
	a.setLevel(0)
	if s.ShowBarOnDesktop {
		a.showBarWindow()
	} else {
		a.hideBarWindow()
	}
}

func (a *App) showBarWindow() {
	if a.Hooks.ShowBar != nil {
		a.Hooks.ShowBar()
	}
}

// hideBarWindow hides the bar unless alerts or an answer are still showing (the bar hides itself later).
func (a *App) hideBarWindow() {
	if a.Hooks.HideBar != nil {
		a.Hooks.HideBar()
	}
}

func (a *App) storeEnabled() bool { return a.Settings().HistoryRetention != "never" }

func (a *App) handle(c *controller, m cmd) {
	switch m.kind {
	case cmdHotkey:
		a.onHotkey(c, m.hotkey)
	case cmdBar:
		a.onBarAction(c, m.action)
	case cmdAlert:
		a.onAlertAction(c, m.alertID, m.action)
	case cmdTick:
		a.onTick(c, m.id)
	case cmdProcessingFinished:
		if c.proc != nil && c.proc.id == m.id {
			c.proc = nil
			if c.sess == nil {
				a.setPhase(hotkey.Idle)
				a.showIdleBar()
			}
		}
	case cmdRecorderError:
		if c.sess != nil && c.sess.id == m.id {
			log.Printf("[%s] microphone failed mid-recording: %s", shortID(m.id), m.message)
			a.play(SoundError)
			a.alert("mic_unavailable", "麦克风不可用", "error").desc(m.message).duration(6000).show()
			a.finish(c)
		}
	case cmdSettingsChanged:
		if c.sess == nil && c.proc == nil {
			a.showIdleBar()
		} else {
			a.updateBar(func(*model.BarState) {})
		}
	}
}

func (a *App) onHotkey(c *controller, act hotkey.Action) {
	switch act.Kind {
	case hotkey.Start:
		a.start(c, act.Mode, act.Locked)
	case hotkey.SwitchMode:
		a.switchMode(c, act.Mode)
	case hotkey.Lock:
		a.lock(c)
	case hotkey.Finish:
		a.finish(c)
	case hotkey.CancelSilently:
		a.discard(c, false)
	case hotkey.TapTooShort:
		a.discard(c, true)
	case hotkey.Cancel:
		a.cancel(c)
	case hotkey.PasteLast:
		a.pasteLast(c)
	}
}

func (a *App) onBarAction(c *controller, action string) {
	switch action {
	case "cancel":
		a.cancel(c)
	case "finish":
		a.finish(c)
	case "start":
		if c.sess == nil {
			a.start(c, model.ModeDictation, true)
		}
	case "cycle_translation_target":
		if _, err := a.UpdateSettings(func(s *model.Settings) {
			if len(s.TranslationTargets) > 1 {
				s.TranslationTargets = append(s.TranslationTargets[1:], s.TranslationTargets[0])
			}
		}); err != nil {
			log.Printf("cannot cycle translation target: %v", err)
		}
		a.updateBar(func(*model.BarState) {})
	default:
		log.Printf("unknown bar action %q", action)
	}
}

func (a *App) busyAlert() {
	a.alert("busy", "Hoshiro 仍在处理您的上一个转录", "warning").action("cancel_processing", "取消", "secondary").duration(4000).show()
}

func (a *App) refuseStart(c *controller, key string, build func() *AlertBuilder) {
	log.Printf("start refused: %s", key)
	a.setPhase(hotkey.Idle)
	if time.Since(c.lastKeyAlert) < 4*time.Second {
		return
	}
	c.lastKeyAlert = time.Now()
	build().show()
}

var warmUp = providers.WarmUp

func (a *App) start(c *controller, mode model.Mode, locked bool) {
	if c.proc != nil {
		a.busyAlert()
		return
	}
	if c.sess != nil {
		return
	}
	s := a.Settings()
	go warmUp(s) // opens pooled connections while the mic starts

	if platform.CheckPermissions().Microphone == model.PermDenied {
		a.refuseStart(c, "microphone", func() *AlertBuilder {
			return a.alert("permission", "需要麦克风权限", "warning").desc("请在系统设置中允许 Hoshiro 使用麦克风").
				action("open_mic_settings", "打开设置", "primary").duration(8000)
		})
		return
	}
	if !s.Asr.Ready() {
		a.refuseStart(c, "asr_key", func() *AlertBuilder {
			return a.alert("missing_key", "尚未配置语音识别服务", "warning").desc("请先在设置中配置所选语音识别服务").
				action("open_settings", "打开设置", "primary").duration(8000)
		})
		return
	}
	id := uuid.NewString()
	tag := shortID(id)
	t0 := time.Now()

	// State first: the engine must see the new phase before any slow work.
	if locked {
		a.setPhase(hotkey.RecordingLocked)
	} else {
		a.setPhase(hotkey.RecordingPtt)
	}
	a.showBarWindow()
	a.updateBar(func(b *model.BarState) {
		b.Phase, b.Mode, b.Locked, b.ElapsedSecs = model.BarRecording, mode, locked, 0
	})

	words, _ := a.DB.WordsForPrompt(100)
	sess := &session{id: id, mode: mode, locked: locked, createdAt: store.Now(), asr: s.Asr, words: words,
		chunks: &chunkList{}, stopTick: make(chan struct{})}
	if s.Asr.Streaming() {
		sess.stream = providers.StartStream(context.Background(), s.Asr, s.SelectedLanguages, words)
		sess.chunks.add(streamTask(sess.stream))
	}
	sk := &sink{a: a, sessionID: id, tag: tag, chunks: sess.chunks, asr: s.Asr, words: words, stream: sess.stream, language: asrLanguage(s)}
	// Audible feedback must be instant, so don't wait for the mic.
	a.play(SoundStart)
	if err := a.recorder.Start(ptrStr(s.MicrophoneDeviceID), sk); err != nil {
		msg := errText(err)
		abortTasks(sess.chunks.drain())
		log.Printf("[%s] recorder failed to start: %s", tag, msg)
		a.setPhase(hotkey.Idle)
		a.showIdleBar()
		a.play(SoundError)
		a.alert("record_failed", "无法开始录音", "error").desc(msg).duration(6000).show()
		return
	}
	sess.ctxCh = captureAsync(mode == model.ModeAsk)

	if s.MuteWhileDictating {
		delay := time.Duration(0)
		if s.InteractionSounds {
			delay = 120 * time.Millisecond
		}
		go func() {
			time.Sleep(delay)
			sess.muteMu.Lock()
			defer sess.muteMu.Unlock()
			if !sess.muteEnded {
				sess.unmute = platform.MuteSystemOutput()
			}
		}()
	}
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-sess.stopTick:
				return
			case <-t.C:
				a.send(cmd{kind: cmdTick, id: id})
			}
		}
	}()
	log.Printf("[%s] recording started mode=%s locked=%v (+%d ms)", tag, mode, locked, time.Since(t0).Milliseconds())
	sess.started = time.Now()
	c.sess = sess
}

func captureAsync(withSelection bool) chan model.AppContext {
	ch := make(chan model.AppContext, 1)
	go func() { ch <- platform.CaptureContext(withSelection, withSelection) }()
	return ch
}

func resolveCtx(ch chan model.AppContext) model.AppContext {
	if ch == nil {
		return model.AppContext{}
	}
	select {
	case c := <-ch:
		return c
	case <-time.After(2 * time.Second):
		return model.AppContext{}
	}
}

func (a *App) lock(c *controller) {
	if c.sess == nil || c.sess.locked {
		return
	}
	c.sess.locked = true
	a.setPhase(hotkey.RecordingLocked)
	a.play(SoundLock)
	a.updateBar(func(b *model.BarState) { b.Locked = true })
}

func (a *App) switchMode(c *controller, mode model.Mode) {
	if c.sess == nil {
		return
	}
	c.sess.mode, c.sess.locked = mode, true
	if mode == model.ModeAsk {
		c.sess.ctxCh = captureAsync(true)
	}
	a.setPhase(hotkey.RecordingLocked)
	a.play(SoundLock)
	a.updateBar(func(b *model.BarState) { b.Mode, b.Locked = mode, true })
}

func (a *App) onTick(c *controller, id string) {
	s := c.sess
	if s == nil || s.id != id {
		return
	}
	elapsed := int(time.Since(s.started).Seconds())
	a.updateBar(func(b *model.BarState) { b.ElapsedSecs = float64(elapsed) })
	if elapsed >= warnSessionSecs && !s.warned {
		s.warned = true
		a.alert("time_limit", "录音将在 1 分钟内结束", "warning").duration(8000).show()
	}
	if elapsed >= maxSessionSecs {
		log.Printf("[%s] max duration reached, finishing", shortID(s.id))
		a.finish(c)
	}
}

// releaseSession ends the stream, ticker and mute of a finished session.
func releaseSession(s *session) {
	if s.stream != nil {
		s.stream.Close()
	}
	close(s.stopTick)
	s.muteMu.Lock()
	s.muteEnded = true
	un := s.unmute
	s.unmute = nil
	s.muteMu.Unlock()
	if un != nil {
		un()
	}
}

func (a *App) finish(c *controller) {
	sess := c.sess
	if sess == nil {
		return
	}
	c.sess = nil
	tag := shortID(sess.id)
	s := a.Settings()
	a.setPhase(hotkey.Processing)
	a.updateBar(func(b *model.BarState) { b.Phase, b.Locked, b.ElapsedSecs = model.BarProcessing, false, 0 })

	stop, err := a.recorder.Stop()
	releaseSession(sess)
	a.play(SoundStop)
	if err != nil {
		log.Printf("[%s] recorder stop failed: %v", tag, err)
		abortTasks(sess.chunks.drain())
		a.setPhase(hotkey.Idle)
		a.showIdleBar()
		a.play(SoundError)
		a.alert("record_failed", "录音出现问题", "error").desc(errText(err)).duration(6000).show()
		return
	}
	log.Printf("[%s] recording finished: %.1fs silent=%v samples=%d", tag, stop.DurationSecs, stop.IsSilent, len(stop.AllSamples))
	actx := resolveCtx(sess.ctxCh)
	if stop.IsSilent {
		if sess.stream != nil {
			sess.stream.Abort()
		}
		abortTasks(sess.chunks.drain())
		a.writeRow(makeRow(sess.id, sess.mode, actx, stop.DurationSecs, sess.createdAt, "empty"))
		a.setPhase(hotkey.Idle)
		a.showIdleBar()
		return
	}
	tasks := sess.chunks.drain()
	if !sess.asr.Streaming() && len(stop.Tail.Samples) >= minChunkSamples {
		t := spawnASR(sess.asr, asrLanguage(s), sess.words, stop.Tail.Samples, fmt.Sprintf("%s#%d", tag, stop.Tail.Index))
		t.index = stop.Tail.Index
		tasks = append(tasks, t)
	}
	if tasks == nil {
		tasks = []*task{}
	}
	row := makeRow(sess.id, sess.mode, actx, stop.DurationSecs, sess.createdAt, "processing")
	keep := a.storeEnabled()
	if keep {
		row.AudioPath = a.saveAudioAndRow(row, stop.AllSamples)
	}
	c.pushStash(stored{sess.id, stop.AllSamples, actx, sess.mode, stop.DurationSecs, sess.createdAt})
	a.spawnProcessing(c, processInput{row: row, ctx: actx, settings: s, live: tasks, deliver: true, store: keep})
}

// saveAudioAndRow writes the WAV and a "processing" row; returns the audio file name when saved.
func (a *App) saveAudioAndRow(row store.HistoryRow, samples []int16) *string {
	var name *string
	if n, err := SaveAudio(a.DB.RecordingsDir(), row.ID, samples); err != nil {
		log.Printf("cannot save audio: %v", err)
	} else {
		name = &n
	}
	row.AudioPath = name
	if err := a.DB.UpsertHistory(row); err != nil {
		log.Printf("cannot write history row: %v", err)
	}
	a.historyChanged()
	return name
}

func (a *App) writeRow(r store.HistoryRow) {
	if !a.storeEnabled() {
		return
	}
	if err := a.DB.UpsertHistory(r); err != nil {
		log.Printf("cannot write history row: %v", err)
	}
	a.historyChanged()
}

func (c *controller) pushStash(s stored) {
	c.stash = slices.DeleteFunc(c.stash, func(x stored) bool { return x.id == s.id })
	c.stash = append(c.stash, s)
	if len(c.stash) > 3 {
		c.stash = c.stash[len(c.stash)-3:]
	}
}

func (c *controller) findStash(id string) (stored, bool) {
	for _, s := range c.stash {
		if s.id == id {
			return s, true
		}
	}
	return stored{}, false
}

func (a *App) spawnProcessing(c *controller, in processInput) {
	ctx, cancel := context.WithCancel(a.ctx)
	p := &processing{id: in.row.ID, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		weak := a.startWeakNet()
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[%s] processing panicked: %v", shortID(p.id), r)
					a.markError(p.id, "处理时发生内部错误")
				}
			}()
			a.process(ctx, in)
		}()
		weak.stop()
		a.send(cmd{kind: cmdProcessingFinished, id: p.id})
	}()
	a.setPhase(hotkey.Processing)
	a.updateBar(func(b *model.BarState) { b.Phase, b.Locked = model.BarProcessing, false })
	c.proc = p
}

func (a *App) cancel(c *controller) {
	if sess := c.sess; sess != nil {
		c.sess = nil
		tag := shortID(sess.id)
		a.setPhase(hotkey.Idle)
		a.showIdleBar()
		stop, err := a.recorder.Stop()
		if sess.stream != nil {
			sess.stream.Abort()
		}
		releaseSession(sess)
		abortTasks(sess.chunks.drain())
		a.play(SoundCancel)
		if err != nil {
			return
		}
		log.Printf("[%s] cancelled after %.1fs", tag, stop.DurationSecs)
		if stop.IsSilent || len(stop.AllSamples) < minChunkSamples {
			return
		}
		actx := resolveCtx(sess.ctxCh)
		row := makeRow(sess.id, sess.mode, actx, stop.DurationSecs, sess.createdAt, "dismissed")
		if a.storeEnabled() {
			a.saveAudioAndRow(row, stop.AllSamples)
		}
		c.pushStash(stored{sess.id, stop.AllSamples, actx, sess.mode, stop.DurationSecs, sess.createdAt})
		a.cancelledAlert(sess.id)
	} else if p := c.proc; p != nil {
		c.proc = nil
		log.Printf("[%s] processing cancelled", shortID(p.id))
		p.cancel()
		<-p.done
		a.setPhase(hotkey.Idle)
		a.showIdleBar()
		a.play(SoundCancel)
		a.markDismissed(c, p.id)
		a.cancelledAlert(p.id)
	}
}

func (a *App) markDismissed(c *controller, id string) {
	if a.storeEnabled() {
		var row *store.HistoryRow
		if r, err := a.DB.GetHistory(id); err == nil && r != nil {
			row = r
		} else if s, ok := c.findStash(id); ok {
			r := makeRow(s.id, s.mode, s.ctx, s.duration, s.createdAt, "dismissed")
			row = &r
		}
		if row != nil {
			row.Status, row.ErrorMessage = "dismissed", nil
			_ = a.DB.UpsertHistory(*row)
		}
	}
	a.historyChanged()
}

func (a *App) cancelledAlert(id string) {
	a.alert("transcript_cancelled", "转录已取消", "info").action("undo", "撤销", "primary").duration(5000).session(id).show()
}

// discard handles CancelSilently / TapTooShort.
func (a *App) discard(c *controller, hint bool) {
	sess := c.sess
	if sess == nil {
		return
	}
	c.sess = nil
	a.setPhase(hotkey.Idle)
	a.showIdleBar()
	a.recorder.Cancel()
	if sess.stream != nil {
		sess.stream.Abort()
	}
	releaseSession(sess)
	abortTasks(sess.chunks.drain())
	log.Printf("[%s] discarded (hint=%v)", shortID(sess.id), hint)
	if hint {
		l := FirstBindingLabel(a.Settings())
		a.alert("hint", fmt.Sprintf("按住 [[%s]] 说话，双击 [[%s]] 进入免提模式", l, l), "info").duration(3500).show()
	}
}

func (a *App) lastTranscript() (string, bool) {
	if p := a.lastText.Load(); p != nil {
		return *p, true
	}
	t, ok, err := a.DB.LastCompletedText()
	return t, ok && err == nil
}

func (a *App) pasteLast(c *controller) {
	if c.sess != nil {
		return
	}
	text, ok := a.lastTranscript()
	if !ok {
		a.alert("no_last", "没有可粘贴的转录", "info").duration(2500).show()
		return
	}
	switch res, msg := deliverText(model.AppContext{}, text, false); res {
	case notEditable:
		a.alert("copy_last", "复制最后的转录", "info").desc("当前没有可输入的文本框").action("copy", "复制", "primary").duration(10_000).text(text).show()
	case insertFailed:
		a.alert("insert_failed", "无法插入文本", "error").desc(msg).action("copy", "复制", "primary").duration(10_000).text(text).show()
	}
}

func (a *App) onAlertAction(c *controller, alertID, actionID string) {
	a.barMu.Lock()
	ac := a.alertCtx[alertID]
	a.barMu.Unlock()
	a.DismissAlert(alertID)
	switch actionID {
	case "retry", "undo":
		if ac.sessionID != "" {
			a.reprocess(c, ac.sessionID)
		}
	case "copy":
		if ac.text != "" {
			if err := platform.CopyToClipboard(ac.text); err == nil {
				a.alert("copied", "已复制", "success").duration(1500).show()
			} else {
				a.alert("copied", "复制失败", "error").duration(3000).show()
			}
		}
	case "open_settings":
		a.Navigate("settings")
	case "open_history":
		a.Navigate("history")
	case "open_mic_settings":
		platform.OpenPermissionSettings(model.PermMicrophone)
	case "quit_typeless":
		platform.TerminateApp(importer.BundleID)
	case "cancel_processing":
		a.cancel(c)
	case "dismiss":
	default:
		log.Printf("unknown alert action %q", actionID)
	}
}

// reprocess handles saved audio like a fresh Finish (undo / retry) and inserts at the current focus.
func (a *App) reprocess(c *controller, id string) {
	if c.proc != nil {
		a.busyAlert()
		return
	}
	if c.sess != nil {
		return
	}
	st, ok := c.findStash(id)
	if !ok {
		st, ok = a.loadStored(id)
	}
	if !ok {
		a.alert("no_audio", "找不到录音文件，无法重试", "error").duration(5000).show()
		return
	}
	log.Printf("[%s] reprocessing saved audio (%.1fs)", shortID(id), st.duration)
	actx := platform.CaptureContext(false, false)
	actx.SelectedText = st.ctx.SelectedText
	keep := a.storeEnabled()
	var row store.HistoryRow
	if r, err := a.DB.GetHistory(st.id); err == nil && r != nil {
		row = *r
	} else {
		row = makeRow(st.id, st.mode, st.ctx, st.duration, st.createdAt, "processing")
	}
	row.Status, row.ErrorMessage = "processing", nil
	if keep {
		a.writeRow(row)
	}
	c.pushStash(st)
	a.showBarWindow()
	a.spawnProcessing(c, processInput{row: row, ctx: actx, settings: a.Settings(), samples: st.samples, deliver: true, store: keep})
}

func (a *App) loadStored(id string) (stored, bool) {
	r, err := a.DB.GetHistory(id)
	if err != nil || r == nil {
		return stored{}, false
	}
	path, ok := a.DB.AudioFile(*r)
	if !ok {
		return stored{}, false
	}
	samples, err := LoadAudio(path)
	if err != nil {
		return stored{}, false
	}
	return stored{r.ID, samples, ctxFromRow(*r), r.Mode, r.DurationSecs, r.CreatedAt}, true
}

func (a *App) markError(id, msg string) {
	if r, err := a.DB.GetHistory(id); err == nil && r != nil {
		r.Status, r.ErrorMessage = "error", &msg
		_ = a.DB.UpsertHistory(*r)
	}
	a.historyChanged()
}
