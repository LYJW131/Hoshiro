// Command hoshiro is Hoshiro, a dictation app for macOS: hold Fn, speak, and
// the polished text appears at the cursor. It runs on MyGo with native UI:
// the main window (internal/ui/hub), the floating bar (internal/ui/bar) and a
// menu-bar tray.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/core"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/ui/bar"
	"github.com/IcyBlue17/hoshiro/internal/ui/hub"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

//go:embed tray.png
var trayIcon []byte

// version is set by the build (mygo build links the configured version).
var version = "0.1.1"

type desktop struct {
	app *core.App
	hub *hub.Hub
	bar *bar.Bar

	mu     sync.Mutex
	main   *mygo.Window
	barWin *mygo.Window
	tray   *mygo.Tray

	trayRefreshed time.Time
	mics          []model.MicDevice
}

func main() {
	autostart := flag.Bool("autostart", false, "started at login: stay in the background")
	demo := flag.Bool("demo", false, "show the bar in its states, for a look at it")
	flag.Parse()

	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.SetName("Hoshiro")
	if mygo.App.Version() == "" {
		mygo.App.SetVersion(version)
	} else {
		version = mygo.App.Version()
	}

	paths := core.DefaultPaths()
	setupLogging(paths.DataDir)
	app, err := core.Open(paths, version)
	if err != nil {
		log.Fatalf("cannot open data: %v", err)
	}
	d := &desktop{app: app}
	d.hub = hub.New(app, hub.Env{
		Update:   d.updateHub,
		OpenFile: d.openFile,
		SaveFile: d.saveFile,
		Relaunch: mygo.App.Relaunch,
		Reveal:   revealData,
	})
	d.bar = bar.New(app, bar.Env{SetHitRects: platform.SetBarHitRects, CopyText: platform.CopyToClipboard})
	app.Hooks = core.Hooks{
		BarChanged: d.updateBar,
		HubChanged: d.hub.Changed,
		Navigate:   func(page string) { mygo.RunOnMain(func() { d.navigate(page) }) },
		ShowBar:    platform.ShowBar,
		HideBar:    platform.HideBar,
		SetDockVisible: func(show bool) {
			mygo.RunOnMain(func() { setDock(show) })
		},
		SetOpenAtLogin: func(on bool) {
			if mygo.IsDev() {
				log.Printf("launchAtLogin=%v: skipped in development builds", on)
				return
			}
			if err := mygo.App.SetOpenAtLogin(on); err != nil {
				log.Printf("cannot change open at login: %v", err)
			}
		},
		OpenURL:        openURL,
		PlaySound:      func(b []byte) { go func() { _ = platform.PlayWAV(b) }() },
		RefreshTray:    func() { mygo.RunOnMain(func() { d.refreshTray(true) }) },
		CheckForUpdate: checkForUpdate,
		InstallUpdate:  installUpdate,
	}

	mygo.App.SetMenu(d.appMenu())
	mygo.App.OnSecondInstance(func([]string, string) { d.showMain() })
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			d.showMain()
		}
	})
	// A tray app: closing the main window keeps it running.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnQuit(app.Close)

	mygo.App.WhenReady(func() {
		s := app.Settings()
		setDock(s.ShowInDock)
		d.setupTray()
		d.setupBar()
		app.Start()
		if *demo {
			go d.runDemo()
		}
		if !*autostart && !mygo.App.WasOpenedAtLogin() || !s.OnboardingCompleted {
			d.showMain()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func setupLogging(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "hoshiro.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	if st, err := f.Stat(); err == nil && st.Size() > 4<<20 {
		_ = f.Truncate(0)
	}
	log.SetOutput(f)
	log.Printf("Hoshiro %s starting (data dir %s)", version, dir)
}

func setDock(show bool) {
	if runtime.GOOS != "darwin" {
		return
	}
	if show {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	} else {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
}

// revealData shows settings.json in Finder, or the folder when it is missing.
func revealData(dir string) {
	if f := filepath.Join(dir, "settings.json"); fileExists(f) {
		mygo.Shell.ShowItemInFolder(f)
	} else if err := mygo.Shell.OpenPath(dir); err != nil {
		log.Printf("cannot open data folder: %v", err)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func openURL(url string) error { return mygo.Shell.OpenExternal(url) }

// ── main window ──

func (d *desktop) updateHub(f func()) {
	d.mu.Lock()
	w := d.main
	d.mu.Unlock()
	if w != nil {
		w.Update(f)
	} else {
		mygo.RunOnMain(f)
	}
}

func (d *desktop) showMain() {
	d.mu.Lock()
	w := d.main
	d.mu.Unlock()
	if w == nil {
		w = mygo.NewWindow(mygo.WindowOptions{
			Title:                "Hoshiro",
			Width:                1080,
			Height:               750,
			MinWidth:             880,
			MinHeight:            600,
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 22, Y: 26},
			StateKey:             "main",
			Content:              ui.View(d.hub.View),
		})
		w.OnClosed(func() {
			d.mu.Lock()
			if d.main == w {
				d.main = nil
			}
			d.mu.Unlock()
		})
		d.mu.Lock()
		d.main = w
		d.mu.Unlock()
	}
	w.Show()
	w.Restore()
	w.Focus()
}

func (d *desktop) navigate(page string) {
	d.showMain()
	d.updateHub(func() { d.hub.Navigate(page) })
}

func (d *desktop) openFile(title string, exts []string) string {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: d.main, Title: title,
		Filters: []mygo.FileFilter{{Name: "CSV", Extensions: exts}}})
	if err != nil || len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func (d *desktop) saveFile(title, name string) string {
	p, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: d.main, Title: title, DefaultPath: name,
		Filters: []mygo.FileFilter{{Name: "WAV 音频", Extensions: []string{"wav"}}}})
	if err != nil {
		return ""
	}
	return p
}

// ── floating bar ──

func (d *desktop) setupBar() {
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:         "Hoshiro Bar",
		Width:         bar.Width,
		Height:        bar.Height,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		DisableShadow: true,
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content:       ui.View(d.bar.View),
	})
	w.SetVisibleOnAllWorkspaces(true)
	d.mu.Lock()
	d.barWin = w
	d.mu.Unlock()
	if err := platform.SetupBar(w.NativeHandle()); err != nil {
		log.Printf("bar setup failed: %v", err)
	}
	platform.ShowBar()
}

func (d *desktop) updateBar() {
	d.mu.Lock()
	w := d.barWin
	d.mu.Unlock()
	if w != nil {
		w.Update(func() {})
	}
}

// ── menus ──

func (d *desktop) appMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Hoshiro", Submenu: []*mygo.MenuItem{
			{Label: "关于 Hoshiro", Click: func(*mygo.MenuItem, *mygo.Window) { d.navigate("settings:about") }},
			{Label: "检查更新…", Click: func(*mygo.MenuItem, *mygo.Window) { d.app.UpdateTrayAction(mygo.App.Relaunch) }},
			mygo.Separator(),
			{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { d.navigate("settings") }},
			mygo.Separator(),
			{Role: mygo.RoleHide, Label: "隐藏 Hoshiro"},
			{Role: mygo.RoleHideOthers, Label: "隐藏其他"},
			{Role: mygo.RoleUnhide, Label: "全部显示"},
			mygo.Separator(),
			{Label: "退出 Hoshiro", Accelerator: "CmdOrCtrl+Q", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		}},
		{Label: "编辑", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo, Label: "撤销"}, {Role: mygo.RoleRedo, Label: "重做"}, mygo.Separator(),
			{Role: mygo.RoleCut, Label: "剪切"}, {Role: mygo.RoleCopy, Label: "拷贝"}, {Role: mygo.RolePaste, Label: "粘贴"},
			{Role: mygo.RoleSelectAll, Label: "全选"},
		}},
		{Label: "窗口", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize, Label: "最小化"}, {Role: mygo.RoleZoom, Label: "缩放"}, mygo.Separator(),
			{Role: mygo.RoleClose, Label: "关闭窗口"},
		}},
	})
}

func (d *desktop) setupTray() {
	t, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon, IconIsTemplate: true, ToolTip: "Hoshiro", Menu: d.trayMenu()})
	if err != nil {
		log.Printf("tray setup failed: %v", err)
		return
	}
	d.tray = t
	// The menu shows on click; re-enumerate microphones for it now and then.
	t.OnClick(func() { d.refreshTray(false) })
	d.refreshTray(true)
}

// refreshTray re-lists microphones (when permitted) and rebuilds the menu; at most every 1.5 s unless forced.
func (d *desktop) refreshTray(force bool) {
	if d.tray == nil || (!force && time.Since(d.trayRefreshed) < 1500*time.Millisecond) {
		return
	}
	d.trayRefreshed = time.Now()
	go func() {
		mics := d.app.ListMics()
		mygo.RunOnMain(func() {
			d.mics = mics
			d.tray.SetMenu(d.trayMenu())
		})
	}()
}

func (d *desktop) trayMenu() *mygo.Menu {
	cur := d.app.Settings().MicrophoneDeviceID
	setMic := func(id *string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			_, _ = d.app.UpdateSettings(func(s *model.Settings) { s.MicrophoneDeviceID = id })
			d.refreshTray(true)
		}
	}
	mics := []*mygo.MenuItem{{Label: "自动检测", Type: mygo.MenuItemCheckbox, Checked: cur == nil, Click: setMic(nil)}}
	if len(d.mics) > 0 {
		mics = append(mics, mygo.Separator())
	}
	for _, m := range d.mics {
		id := m.ID
		mics = append(mics, &mygo.MenuItem{Label: m.Name, Type: mygo.MenuItemCheckbox, Checked: cur != nil && *cur == id, Click: setMic(&id)})
	}
	upd := "检查更新…"
	if v := d.app.KnownUpdateVersion(); v != "" {
		upd = "安装新版本 " + v
	}
	nav := func(page string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { d.navigate(page) }
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开 Hoshiro 主页", Click: nav("home")},
		{Label: "显示历史记录", Click: nav("history")},
		{Label: "将词汇添加到词典", Click: nav("dictionary")},
		{Label: "选择麦克风", Submenu: mics},
		{Label: "粘贴上一次转录", Click: func(*mygo.MenuItem, *mygo.Window) { d.app.PasteLast() }},
		{Label: "设置…", Click: nav("settings")},
		mygo.Separator(),
		{Label: "版本 " + version, Disabled: true},
		{Label: upd, Click: func(*mygo.MenuItem, *mygo.Window) { d.app.UpdateTrayAction(mygo.App.Relaunch) }},
		{Label: "退出 Hoshiro", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
}

// ── updates ──

var (
	pendingMu sync.Mutex
	pending   *mygo.Update
)

func checkForUpdate(ctx context.Context) (*model.UpdateInfo, error) {
	info := &model.UpdateInfo{CurrentVersion: version}
	u, err := mygo.Updater.Check(ctx)
	if err != nil {
		return nil, err
	}
	pendingMu.Lock()
	pending = u
	pendingMu.Unlock()
	if u != nil {
		info.Available, info.Version, info.Notes = true, u.Version, u.Notes
		if !u.Date.IsZero() {
			info.Date = u.Date.Format(time.RFC3339)
		}
	}
	return info, nil
}

func installUpdate(ctx context.Context, progress func(done, total int64)) error {
	pendingMu.Lock()
	u := pending
	pendingMu.Unlock()
	if u == nil {
		return fmt.Errorf("没有可用的更新")
	}
	return u.Install(ctx, progress)
}

// ── demo ──

// runDemo cycles the bar through its states (the Tauri version's Demo.tsx).
func (d *desktop) runDemo() {
	str := func(s string) *string { return &s }
	steps := []func(){
		func() { d.app.PreviewBar(model.BarState{Phase: model.BarIdle, ShowIdleOnDesktop: true, DictationKeyLabel: "fn"}, 0, nil, nil) },
		func() { d.app.PreviewBar(model.BarState{Phase: model.BarRecording, Mode: model.ModeDictation}, 0.5, nil, nil) },
		func() {
			d.app.PreviewBar(model.BarState{Phase: model.BarRecording, Mode: model.ModeTranslation, Locked: true, TranslationTargetLabel: str("英语")}, 0.4, nil, nil)
		},
		func() { d.app.PreviewBar(model.BarState{Phase: model.BarProcessing, Mode: model.ModeAsk}, 0, nil, nil) },
		func() {
			d.app.PreviewBar(model.BarState{Phase: model.BarIdle, ShowIdleOnDesktop: true, DictationKeyLabel: "fn"}, 0,
				[]model.BarAlert{{ID: "demo", Kind: "demo", Title: "按住 [[fn]] 说话，松开结束", Tone: "info", Actions: []model.AlertAction{}}},
				&model.AskAnswer{ID: "demo", Question: "这是什么？", Answer: "**Hoshiro** 的悬浮条演示。\n\n- 口述\n- 翻译\n- 随便问"})
		},
	}
	for i := 0; ; i++ {
		steps[i%len(steps)]()
		time.Sleep(2500 * time.Millisecond)
	}
}
