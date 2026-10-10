// Package core is Hoshiro's application logic without any UI: settings, the
// dictation controller, the processing pipeline, the floating bar's state and
// alerts, and every operation the windows offer. The UI packages read its
// snapshots and are told to redraw through Hooks.
package core

import (
	"context"
	"embed"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/config"
	"github.com/IcyBlue17/hoshiro/internal/hotkey"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/personalize"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/providers"
	"github.com/IcyBlue17/hoshiro/internal/stats"
	"github.com/IcyBlue17/hoshiro/internal/store"
	"github.com/google/uuid"
)

const (
	Identifier       = "cn.icybit.hoshiro"
	LegacyIdentifier = "dev.hoshiro.app"
)

//go:embed sounds/*.wav
var soundFS embed.FS

// Sound is an interaction sound.
type Sound string

const (
	SoundStart  Sound = "start"
	SoundStop   Sound = "stop"
	SoundCancel Sound = "cancel"
	SoundLock   Sound = "lock"
	SoundError  Sound = "error"
)

// Hooks connect the core to the windows and the OS. Every hook may be nil.
type Hooks struct {
	BarChanged     func()            // bar state, level, alerts or answer changed
	HubChanged     func(what string) // "settings", "history", "stats", "dictionary", "profile", "permissions", "update", "mic", "capture"
	Navigate       func(page string) // show the hub on a page ("home", "history", "dictionary", "settings", "settings:about"...)
	ShowBar        func()
	HideBar        func()
	SetDockVisible func(bool)
	SetOpenAtLogin func(bool)
	OpenURL        func(string) error
	PlaySound      func([]byte)
	RefreshTray    func()
	CheckForUpdate func(ctx context.Context) (*model.UpdateInfo, error)
	InstallUpdate  func(ctx context.Context, progress func(done, total int64)) error
}

// Paths of the app's data.
type Paths struct{ DataDir, DBPath, RecordingsDir string }

// DefaultPaths are under ~/Library/Application Support/cn.icybit.hoshiro,
// where the Tauri version kept them, so existing data is picked up.
func DefaultPaths() Paths {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	old, dir := filepath.Join(base, LegacyIdentifier), filepath.Join(base, Identifier)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err := os.Stat(old); err == nil {
			if err := os.Rename(old, dir); err != nil {
				log.Printf("cannot move data dir %s -> %s: %v", old, dir, err)
			}
		}
	}
	_ = os.MkdirAll(dir, 0o700)
	return Paths{dir, filepath.Join(dir, "hoshiro.db"), filepath.Join(dir, "recordings")}
}

// Alert is a bar alert with its shown time (the bar expires it after DurationMs).
type Alert struct {
	model.BarAlert
	Shown time.Time
}

type alertCtx struct{ sessionID, text string }

// App is the running application.
type App struct {
	Paths   Paths
	DB      *store.DB
	Hooks   Hooks
	Version string

	settingsMu    sync.RWMutex
	settingsWrite sync.Mutex
	settings      model.Settings

	phase atomic.Int32 // hotkey.Phase

	barMu     sync.Mutex
	bar       model.BarState
	level     float32
	alerts    []Alert
	alertCtx  map[string]alertCtx
	alertKind map[string]string
	answer    *model.AskAnswer

	lastText atomic.Pointer[string]

	cmds     chan cmd
	recorder *audio.Recorder

	engine    *hotkey.Engine
	tapMu     sync.Mutex
	tap       *platform.HotkeyTap
	captureMu sync.Mutex
	capture   struct {
		keys string
		done bool
		on   bool
	}

	micMu    sync.Mutex
	micTest  *audio.Recorder
	micLevel float32
	micGen   int

	Learner *personalize.Learner

	updMu      sync.Mutex
	update     *model.UpdateInfo
	updErr     string
	installing atomic.Bool
	progress   [2]int64

	permMu sync.Mutex
	perms  model.Permissions

	sounds map[Sound][]byte
	ctx    context.Context
	stop   context.CancelFunc
}

// Open loads settings and the database (moving a corrupt one aside).
func Open(paths Paths, version string) (*App, error) {
	s := config.Load(paths.DataDir)
	db, err := store.Open(paths.DBPath, paths.RecordingsDir)
	if err != nil {
		log.Printf("cannot open database (%v); moving it aside and starting fresh", err)
		stamp := time.Now().UTC().Format("20060102150405")
		_ = os.Rename(paths.DBPath, paths.DBPath+".corrupt-"+stamp)
		_ = os.Remove(paths.DBPath + "-wal")
		_ = os.Remove(paths.DBPath + "-shm")
		if db, err = store.Open(paths.DBPath, paths.RecordingsDir); err != nil {
			return nil, err
		}
	}
	return New(paths, s, db, version), nil
}

// New assembles an App around loaded settings and an open database.
func New(paths Paths, s model.Settings, db *store.DB, version string) *App {
	ctx, stop := context.WithCancel(context.Background())
	a := &App{Paths: paths, DB: db, Version: version, settings: s, alertCtx: map[string]alertCtx{}, alertKind: map[string]string{},
		cmds: make(chan cmd, 256), recorder: audio.NewRecorder(platform.OpenMic), ctx: ctx, stop: stop, sounds: map[Sound][]byte{}}
	for _, n := range []Sound{SoundStart, SoundStop, SoundCancel, SoundLock, SoundError} {
		if b, err := soundFS.ReadFile("sounds/" + string(n) + ".wav"); err == nil {
			a.sounds[n] = b
		}
	}
	a.bar = model.BarState{Phase: idlePhase(s), Mode: model.ModeDictation}
	a.decorateBar(s)
	a.Learner = &personalize.Learner{DB: db, Settings: a.Settings, Changed: func() { a.hub("profile"); a.hub("history") }}
	providers.Usage = func(provider, mdl string, resp []byte, audioSecs float64) {
		in, out := stats.Tokens(resp)
		if err := db.RecordUsage(provider, mdl, in, out, stats.CachedTokens(resp), audioSecs); err != nil {
			log.Printf("cannot record usage: %v", err)
		}
	}
	return a
}

// Start runs the controller and the background jobs. Call once the UI is ready.
func (a *App) Start() {
	if n, err := a.DB.FailStaleProcessing(); err != nil {
		log.Printf("cannot reset stale rows: %v", err)
	} else if n > 0 {
		log.Printf("marked %d interrupted sessions as failed", n)
	}
	go a.controllerLoop()
	a.startHotkeys()
	go a.permissionWatcher()
	go a.purgeLoop()
	go a.Learner.Loop(a.ctx)
	go a.updateLoop()
	go a.typelessNotice()
	if h := a.Hooks.SetOpenAtLogin; h != nil {
		h(a.Settings().LaunchAtLogin)
	}
}

// Close stops background work.
func (a *App) Close() {
	a.stop()
	a.tapMu.Lock()
	if a.tap != nil {
		a.tap.Close()
	}
	a.tapMu.Unlock()
	if a.engine != nil {
		a.engine.Close()
	}
	a.DB.Close()
}

func (a *App) hub(what string) {
	if h := a.Hooks.HubChanged; h != nil {
		h(what)
	}
}

func (a *App) barChanged() {
	if h := a.Hooks.BarChanged; h != nil {
		h()
	}
}

func (a *App) historyChanged() { a.hub("history"); a.hub("stats") }

// Navigate shows the hub on a page.
func (a *App) Navigate(page string) {
	if h := a.Hooks.Navigate; h != nil {
		h(page)
	}
}

func (a *App) play(s Sound) {
	if a.Settings().InteractionSounds && a.Hooks.PlaySound != nil {
		a.Hooks.PlaySound(a.sounds[s])
	}
}

// ── settings ──

func (a *App) Settings() model.Settings {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return cloneSettings(a.settings)
}

func cloneSettings(s model.Settings) model.Settings {
	s.Shortcuts.Dictation = slices.Clone(s.Shortcuts.Dictation)
	s.Shortcuts.AskAnything = slices.Clone(s.Shortcuts.AskAnything)
	s.Shortcuts.Translation = slices.Clone(s.Shortcuts.Translation)
	s.Shortcuts.PasteLastTranscript = slices.Clone(s.Shortcuts.PasteLastTranscript)
	s.SelectedLanguages = slices.Clone(s.SelectedLanguages)
	s.TranslationTargets = slices.Clone(s.TranslationTargets)
	if s.MicrophoneDeviceID != nil {
		v := *s.MicrophoneDeviceID
		s.MicrophoneDeviceID = &v
	}
	return s
}

// UpdateSettings mutates, sanitizes, saves and applies settings; returns the saved ones.
func (a *App) UpdateSettings(f func(*model.Settings)) (model.Settings, error) {
	a.settingsWrite.Lock()
	old := a.Settings()
	n := cloneSettings(old)
	f(&n)
	config.Sanitize(&n)
	n.OnboardingCompleted = n.OnboardingCompleted || old.OnboardingCompleted
	if err := config.Save(a.Paths.DataDir, n); err != nil {
		a.settingsWrite.Unlock()
		return old, errors.New("保存设置失败：" + err.Error())
	}
	a.settingsMu.Lock()
	a.settings = n
	a.settingsMu.Unlock()
	a.settingsWrite.Unlock()
	a.applySettings(old, n)
	return cloneSettings(n), nil
}

func (a *App) applySettings(old, n model.Settings) {
	if !shortcutsEqual(old.Shortcuts, n.Shortcuts) && a.engine != nil {
		a.engine.UpdateBindings(n.Shortcuts)
	}
	if old.ShowInDock != n.ShowInDock && a.Hooks.SetDockVisible != nil {
		a.Hooks.SetDockVisible(n.ShowInDock)
	}
	if old.LaunchAtLogin != n.LaunchAtLogin && a.Hooks.SetOpenAtLogin != nil {
		a.Hooks.SetOpenAtLogin(n.LaunchAtLogin)
	}
	if old.HistoryRetention != n.HistoryRetention {
		go a.purge()
	}
	if ptrStr(old.MicrophoneDeviceID) != ptrStr(n.MicrophoneDeviceID) || !slices.Equal(old.TranslationTargets, n.TranslationTargets) ||
		old.ShowBarOnDesktop != n.ShowBarOnDesktop || !slices.Equal(old.Shortcuts.Dictation, n.Shortcuts.Dictation) {
		a.send(cmd{kind: cmdSettingsChanged})
		if a.Hooks.RefreshTray != nil {
			a.Hooks.RefreshTray()
		}
	}
	a.hub("settings")
}

func shortcutsEqual(x, y model.ShortcutBindings) bool {
	return slices.Equal(x.Dictation, y.Dictation) && slices.Equal(x.AskAnything, y.AskAnything) &&
		slices.Equal(x.Translation, y.Translation) && slices.Equal(x.PasteLastTranscript, y.PasteLastTranscript)
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (a *App) purge() {
	secs, ok := config.RetentionSecs(a.Settings().HistoryRetention)
	if !ok {
		return
	}
	n, err := a.DB.PurgeOlderThan(time.Now().Add(-time.Duration(secs) * time.Second))
	if err != nil {
		log.Printf("history purge failed: %v", err)
	} else if n > 0 {
		log.Printf("purged %d history rows", n)
		a.historyChanged()
	}
}

func (a *App) purgeLoop() {
	for {
		a.purge()
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// ── phase, bar state and alerts ──

func (a *App) Phase() hotkey.Phase     { return hotkey.Phase(a.phase.Load()) }
func (a *App) setPhase(p hotkey.Phase) { a.phase.Store(int32(p)) }

func idlePhase(s model.Settings) model.BarPhase {
	if s.ShowBarOnDesktop {
		return model.BarIdle
	}
	return model.BarHidden
}

func FirstBindingLabel(s model.Settings) string {
	if len(s.Shortcuts.Dictation) > 0 {
		return hotkey.KeyLabel(s.Shortcuts.Dictation[0])
	}
	return "fn"
}

func (a *App) decorateBar(s model.Settings) {
	a.bar.ShowIdleOnDesktop = s.ShowBarOnDesktop
	a.bar.DictationKeyLabel = FirstBindingLabel(s)
	a.bar.TranslationTargetLabel = nil
	if a.bar.Mode == model.ModeTranslation && len(s.TranslationTargets) > 0 {
		l := config.LanguageLabel(s.TranslationTargets[0])
		a.bar.TranslationTargetLabel = &l
	}
}

func (a *App) updateBar(f func(*model.BarState)) {
	s := a.Settings()
	a.barMu.Lock()
	f(&a.bar)
	a.decorateBar(s)
	a.barMu.Unlock()
	a.barChanged()
}

// BarSnapshot is everything the bar draws.
type BarSnapshot struct {
	State  model.BarState
	Level  float32
	Alerts []Alert
	Answer *model.AskAnswer
}

func (a *App) Bar() BarSnapshot {
	a.barMu.Lock()
	defer a.barMu.Unlock()
	now := time.Now()
	a.alerts = slices.DeleteFunc(a.alerts, func(x Alert) bool {
		return x.DurationMs != nil && now.Sub(x.Shown) > time.Duration(*x.DurationMs)*time.Millisecond
	})
	snap := BarSnapshot{State: a.bar, Level: a.level, Alerts: slices.Clone(a.alerts)}
	if a.answer != nil {
		v := *a.answer
		snap.Answer = &v
	}
	return snap
}

func (a *App) setLevel(l float32) {
	a.barMu.Lock()
	a.level = l
	a.barMu.Unlock()
	a.barChanged()
}

// AlertBuilder builds a bar alert, as notify::alert did.
type AlertBuilder struct {
	a     *App
	alert model.BarAlert
	ctx   alertCtx
}

func (a *App) alert(kind, title, tone string) *AlertBuilder {
	d := uint64(5000)
	return &AlertBuilder{a: a, alert: model.BarAlert{Kind: kind, Title: title, Tone: tone, DurationMs: &d, Actions: []model.AlertAction{}}}
}

func (b *AlertBuilder) desc(d string) *AlertBuilder { b.alert.Description = &d; return b }
func (b *AlertBuilder) action(id, label, style string) *AlertBuilder {
	b.alert.Actions = append(b.alert.Actions, model.AlertAction{ID: id, Label: label, Style: style})
	return b
}
func (b *AlertBuilder) duration(ms uint64) *AlertBuilder { b.alert.DurationMs = &ms; return b }
func (b *AlertBuilder) session(id string) *AlertBuilder  { b.ctx.sessionID = id; return b }
func (b *AlertBuilder) text(t string) *AlertBuilder      { b.ctx.text = t; return b }

// show replaces any alert of the same kind and returns the new alert's id.
func (b *AlertBuilder) show() string {
	a := b.a
	b.alert.ID = uuid.NewString()
	a.barMu.Lock()
	if old, ok := a.alertKind[b.alert.Kind]; ok {
		a.alerts = slices.DeleteFunc(a.alerts, func(x Alert) bool { return x.ID == old })
	}
	a.alertKind[b.alert.Kind] = b.alert.ID
	a.alertCtx[b.alert.ID] = b.ctx
	if len(a.alertCtx) > 64 {
		for id := range a.alertCtx {
			if id != b.alert.ID && !slices.ContainsFunc(a.alerts, func(x Alert) bool { return x.ID == id }) {
				delete(a.alertCtx, id)
			}
		}
	}
	a.alerts = append(a.alerts, Alert{b.alert, time.Now()})
	a.barMu.Unlock()
	a.barChanged()
	return b.alert.ID
}

// DismissAlert removes an alert from the bar.
func (a *App) DismissAlert(id string) {
	a.barMu.Lock()
	a.alerts = slices.DeleteFunc(a.alerts, func(x Alert) bool { return x.ID == id })
	a.barMu.Unlock()
	a.barChanged()
}

// CloseAnswer hides the Ask answer card.
func (a *App) CloseAnswer() {
	a.barMu.Lock()
	a.answer = nil
	a.barMu.Unlock()
	a.barChanged()
}

func (a *App) showAnswer(ans model.AskAnswer) {
	a.barMu.Lock()
	a.answer = &ans
	a.barMu.Unlock()
	a.barChanged()
}

// BarAction is a click on the bar ("cancel", "finish", "start", "cycle_translation_target").
func (a *App) BarAction(action string) { a.send(cmd{kind: cmdBar, action: action}) }

// AlertAction is a click on an alert button.
func (a *App) AlertAction(alertID, actionID string) {
	a.send(cmd{kind: cmdAlert, alertID: alertID, action: actionID})
}

// PasteLast is the tray's "paste last transcript".
func (a *App) PasteLast() {
	a.send(cmd{kind: cmdHotkey, hotkey: hotkey.Action{Kind: hotkey.PasteLast}})
}

// ── hotkeys and permissions ──

func (a *App) startHotkeys() {
	if a.engine == nil {
		a.engine = hotkey.NewEngine(a.Settings().Shortcuts, a.Phase, func(act hotkey.Action) {
			a.send(cmd{kind: cmdHotkey, hotkey: act})
		})
	}
	a.tapMu.Lock()
	defer a.tapMu.Unlock()
	if a.tap != nil {
		a.tap.Close()
		a.tap = nil
	}
	t, err := platform.StartHotkeyTap(a.engine)
	if err != nil {
		log.Printf("hotkey engine not started yet: %v", err)
		return
	}
	a.tap = t
}

func (a *App) hotkeysActive() bool {
	a.tapMu.Lock()
	defer a.tapMu.Unlock()
	return a.tap != nil && a.tap.Active()
}

// Permissions returns the last checked permissions.
func (a *App) Permissions() model.Permissions {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	if a.perms.Microphone == "" {
		a.perms = platform.CheckPermissions()
	}
	return a.perms
}

func (a *App) RequestPermission(k model.PermissionKind) model.Permissions {
	p := platform.RequestPermission(k)
	a.permMu.Lock()
	a.perms = p
	a.permMu.Unlock()
	a.hub("permissions")
	return p
}

func (a *App) permissionWatcher() {
	inactive := 0
	var last model.Permissions
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		p := platform.CheckPermissions()
		changed := p != last
		if a.hotkeysActive() {
			inactive = 0
		} else {
			inactive++
			if inactive >= 2 {
				a.startHotkeys()
				if a.hotkeysActive() {
					inactive, changed = 0, true
				}
			}
		}
		if changed {
			last = p
			a.permMu.Lock()
			a.perms = p
			a.permMu.Unlock()
			a.hub("permissions")
		}
	}
}

func (a *App) typelessNotice() {
	select {
	case <-a.ctx.Done():
		return
	case <-time.After(4 * time.Second):
	}
	if len(platform.RunningPIDs("now.typeless.desktop")) > 0 {
		a.alert("typeless_running", "检测到 Typeless 正在运行", "warning").
			desc("Typeless 与 Hoshiro 会同时响应同一个按键，建议先退出 Typeless。").
			action("quit_typeless", "退出 Typeless", "primary").duration(15_000).show()
	}
}
