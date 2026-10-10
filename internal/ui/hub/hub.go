// Package hub is Hoshiro's main window in MyGo native UI: the sidebar, the
// Home, History, Dictionary and Statistics pages, the settings modal and
// onboarding.
package hub

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/core"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

//go:embed logo.png
var logoPNG []byte

var logo, _ = ui.DecodeBitmap(logoPNG)

// Env is what the hub needs from the desktop besides the core.
type Env struct {
	// Update runs f on the main thread and redraws (mygo's Window.Update).
	Update func(f func())
	// OpenFile and SaveFile show the system's file dialogs ("" = cancelled).
	OpenFile func(title string, exts []string) string
	SaveFile func(title, name string) string
	Relaunch func()
}

// Hub is the main window's state.
type Hub struct {
	a   *core.App
	env Env

	settings        model.Settings
	page            string // home | history | dictionary | statistics
	settingsOpen    bool
	category        string
	help            bool
	forceOnboarding bool
	onboarding      onboardingState

	mu    sync.Mutex
	dirty map[string]bool

	stats      model.Stats
	typeless   *model.TypelessDetection
	quitting   bool
	heatOffset int
	heatHover  int

	hist  histState
	dict  dictState
	usage usageState
	set   settingsState

	updChecking bool
	confirm     kit.Confirm
}

// New makes the hub; call Changed from core.Hooks.HubChanged.
func New(a *core.App, env Env) *Hub {
	if env.Update == nil {
		env.Update = func(f func()) { f() }
	}
	h := &Hub{a: a, env: env, page: "home", category: "general", heatHover: -1,
		dirty: map[string]bool{"settings": true, "stats": true, "history": true, "dictionary": true, "profile": true, "usage": true}}
	h.settings = a.Settings()
	kit.Appearance = h.settings.Appearance
	h.hist.limit = 50
	return h
}

// Changed marks data to reload ("settings", "history", ...); safe from any goroutine.
func (h *Hub) Changed(what string) {
	h.env.Update(func() {
		h.mu.Lock()
		h.dirty[what] = true
		if what == "history" {
			h.dirty["stats"] = true
			h.dirty["usage"] = true
		}
		h.mu.Unlock()
	})
}

func (h *Hub) take(what string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.dirty[what]
	delete(h.dirty, what)
	return d
}

// Navigate handles a route from the tray, the bar or the app menu.
func (h *Hub) Navigate(route string) {
	switch route {
	case "settings":
		h.openSettings("general")
	case "settings:about":
		h.openSettings("about")
	case "home", "history", "dictionary", "statistics":
		h.page = route
	}
}

func (h *Hub) openSettings(cat string) {
	h.settingsOpen, h.category = true, cat
}

// update applies a settings mutation optimistically and saves it.
func (h *Hub) update(f func(*model.Settings)) {
	f(&h.settings)
	s, err := h.a.UpdateSettings(f)
	if err == nil {
		h.settings = s
	}
	kit.Appearance = h.settings.Appearance
}

func (h *Hub) async(ctx *ui.Context, work func(), done func()) {
	go func() {
		work()
		h.env.Update(done)
	}()
}

func (h *Hub) ctx() context.Context { return context.Background() }

func (h *Hub) reload() {
	if h.take("settings") {
		h.settings = h.a.Settings()
		kit.Appearance = h.settings.Appearance
	}
	if h.take("stats") {
		if s, err := h.a.Stats(); err == nil {
			h.stats = s
		}
	}
}

// View builds the window.
func (h *Hub) View(c *ui.Context) {
	h.reload()
	kit.ApplyTheme(c)
	p := kit.P(c)
	if !h.settings.OnboardingCompleted || h.forceOnboarding {
		h.onboardingView(c)
		h.confirm.View(c)
		return
	}
	ui.Row(c).Fill().Padding(8, 0, 8, 8).AlignItems(ui.Stretch).Background(p.Page).Children(func() {
		h.sidebar(c)
		main := ui.Column(c).Grow(1).MinWidth(0)
		main.Children(func() {
			pg := ui.Column(c.Key(h.page)).Grow(1).MinHeight(0)
			pg.Opacity(pg.Animate("fade", 1, 160*time.Millisecond))
			pg.Children(func() {
				switch h.page {
				case "history":
					h.historyPage(c)
				case "dictionary":
					h.dictionaryPage(c)
				case "statistics":
					h.statisticsPage(c)
				default:
					h.homePage(c)
				}
			})
		})
	})
	// The window's title bar region drags the window (hidden title bar).
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(30).DragWindow()
	h.settingsModal(c)
	h.helpModal(c)
	h.confirm.View(c)
}

var nav = []struct {
	page, label string
	icon        *ui.SVG
}{
	{"home", "首页", icons.House},
	{"history", "历史记录", icons.History},
	{"dictionary", "词典", icons.BookOpen},
	{"statistics", "使用统计", icons.BarChart},
}

var asrNames = map[string]string{"mimo": "Xiaomi MiMo", "cloudflare": "Cloudflare", "azure": "Azure Speech", "google": "Google Speech"}
var llmNames = map[model.LlmProvider]string{model.LlmGemini: "Gemini", model.LlmAnthropic: "Claude", model.LlmOpenAI: "OpenAI 兼容"}

func (h *Hub) sidebar(c *ui.Context) {
	p := kit.P(c)
	s := h.settings
	ui.Column(c).Width(200).Background(p.Sidebar).Radius(kit.RadiusCard).Border(1, p.Border).Padding(0, 10, 10).Children(func() {
		ui.Box(c).Height(52).DragWindow()
		ui.Row(c).Gap(9).Padding(6, 10, 18).Children(func() {
			ui.Image(c, logo).Size(28, 28).Radius(8)
			ui.Text(c, "Hoshiro").FontSize(20).FontWeight(750)
		})
		ui.Column(c).Gap(4).Children(func() {
			for _, n := range nav {
				b := ui.ButtonBase(c.Key(n.page)).Justify(ui.Start).Height(38).PaddingX(12).Radius(12).Gap(11).FontSize(15).FontWeight(550).FocusRing(false)
				switch {
				case h.page == n.page:
					b.Background(p.Selected).TextColor(p.Text)
				case b.Hovered():
					b.Background(p.Hover.Alpha(0.55)).TextColor(p.Text)
				default:
					b.TextColor(p.Text2)
				}
				b.Children(func() {
					ui.Icon(c, n.icon).FontSize(19)
					ui.Text(c, n.label)
				})
				if b.Clicked() {
					h.page = n.page
				}
			}
		})
		ui.Spacer(c)
		asrReady := s.Asr.Ready()
		llmReady := !s.Llm.Enabled || strings.TrimSpace(s.Llm.ActiveEndpoint().APIKey) != ""
		var missing []string
		if !asrReady {
			missing = append(missing, asrNames[s.Asr.Provider])
		}
		if !llmReady {
			missing = append(missing, llmNames[s.Llm.Provider])
		}
		pc := ui.Column(c).Gap(7).Padding(12).Radius(14).Border(1, p.Border).Cursor(ui.CursorPointer).Tooltip("模型服务设置")
		if pc.Hovered() {
			pc.Background(p.Hover)
		} else {
			pc.Background(p.Card2)
		}
		if len(missing) > 0 {
			pc.Border(1, p.Warning.Alpha(0.4))
		}
		if pc.Clicked() {
			h.openSettings("providers")
		}
		pc.Children(func() {
			line := func(k string, dot string, v string) {
				ui.Row(c).Gap(8).FontSize(12.5).Children(func() {
					ui.Text(c, k).TextColor(p.Text2).SingleLine().Shrink(0).Margin(0, 6, 0, 0)
					ui.Spacer(c)
					ui.Row(c).Gap(6).FontWeight(600).Shrink(0).Children(func() {
						kit.Dot(c, dot)
						ui.Text(c, v).SingleLine()
					})
				})
			}
			line("语音识别", map[bool]string{true: "ok", false: "bad"}[asrReady], asrNames[s.Asr.Provider])
			if s.Llm.Enabled {
				line("改写", map[bool]string{true: "ok", false: "bad"}[llmReady], llmNames[s.Llm.Provider])
			} else {
				line("改写", "off", "已关闭")
			}
			if len(missing) > 0 {
				ui.Row(c).Gap(6).AlignItems(ui.Start).Margin(3, 0, 0).Padding(7, 8).Radius(9).FontSize(12).LineHeight(1.35).
					TextColor(p.Warning).Background(p.WarningSoft).Children(func() {
					ui.Icon(c, icons.AlertTriangle).FontSize(13)
					ui.Text(c, "缺少 "+strings.Join(missing, "、")+" 的 API Key，点击配置").Grow(1)
				})
			}
		})
		info, _, installing, _, _ := h.a.UpdateState()
		if installing || (info != nil && info.Available) {
			label := "正在更新…"
			if !installing {
				label = "新版本 " + info.Version
			}
			pill := ui.ButtonBase(c).FocusRing(false).Gap(8).Height(32).PaddingX(12).Margin(10, 0, 0).Radius(10).
				Background(p.AccentSoft).TextColor(p.Accent).FontSize(13).FontWeight(600)
			pill.Children(func() {
				ui.Box(c).Size(7, 7).Radius(4).Background(p.Accent)
				ui.Text(c, label)
			})
			if pill.Clicked() {
				h.openSettings("about")
			}
		}
		ui.Row(c).Justify(ui.End).Gap(4).Margin(10, 0, 0).Padding(8, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(p.Divider).Children(func() {
			if kit.IconBtn(c, icons.Settings, "设置", 34).Clicked() {
				h.openSettings("general")
			}
			if kit.IconBtn(c, icons.Help, "帮助", 34).Clicked() {
				h.help = true
			}
		})
	})
}

func first(l []string, def string) string {
	if len(l) > 0 {
		return l[0]
	}
	return def
}

func (h *Hub) helpModal(c *ui.Context) {
	if !h.help {
		return
	}
	p := kit.P(c)
	sc := h.settings.Shortcuts
	kit.Modal(c.Key("help"), &h.help, 440, 0, func() {
		kit.ModalHeader(c, "使用帮助", "", func() { h.help = false })
		ui.Column(c).Padding(4, 24, 22).Dividers(1, p.Divider).Children(func() {
			for _, r := range [][3]string{
				{"口述", first(sc.Dictation, "Fn"), "按住说话，松开后自动输入；双击进入免提模式。"},
				{"随便问", first(sc.AskAnything, "Fn+Space"), "对选中的文字下指令，或直接提问。"},
				{"翻译", first(sc.Translation, "Fn+LeftShift"), "把你说的话翻译成目标语言后输入。"},
				{"粘贴上一次转录", first(sc.PasteLastTranscript, "Ctrl+Cmd+V"), "再次粘贴最近一次的结果。"},
				{"取消", "Esc", "录音或处理过程中按下即可取消。"},
			} {
				ui.Row(c.Key(r[0])).Gap(16).Padding(11, 0).Children(func() {
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, r[0]).FontSize(14).FontWeight(650)
						ui.Text(c, r[2]).FontSize(12.5).TextColor(p.Text2).Margin(2, 0, 0)
					})
					kit.ShortcutKeys(c, r[1], true)
				})
			}
		})
	})
}

// ── formatting (format.ts) ──

func trim1(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}

// FormatWords is 11234 -> "11.2K".
func FormatWords(n uint64) string {
	switch {
	case n >= 1_000_000:
		return trim1(float64(n)/1_000_000) + "M"
	case n >= 1000:
		return trim1(float64(n)/1000) + "K"
	}
	return fmt.Sprint(n)
}

type part struct {
	value string
	unit  string
}

// FormatDuration is 20700 -> [5 小时, 45 分钟].
func FormatDuration(secs float64) []part {
	t := int64(max(0, secs) + 0.5)
	hh, mm := t/3600, (t%3600)/60
	switch {
	case hh > 0:
		return []part{{fmt.Sprint(hh), "小时"}, {fmt.Sprint(mm), "分钟"}}
	case mm > 0:
		return []part{{fmt.Sprint(mm), "分钟"}}
	}
	return []part{{fmt.Sprint(t), "秒"}}
}

func bigNumber(c *ui.Context, parts []part, size float32) {
	ui.Row(c).Gap(8).Wrap().AlignItems(ui.End).Children(func() {
		for i, pt := range parts {
			ui.Row(c.Key(i)).Gap(5).AlignItems(ui.End).Children(func() {
				ui.Text(c, pt.value).FontSize(size).FontWeight(650).LetterSpacing(-0.6).FontFeatures("tnum")
				ui.Text(c, pt.unit).FontSize(13.5).FontWeight(650).Margin(0, 0, 4)
			})
		}
	})
}

func (h *Hub) checkUpdate(c *ui.Context) {
	if h.updChecking {
		return
	}
	h.updChecking = true
	var err error
	h.async(c, func() { _, err = h.a.CheckUpdate(context.Background()) }, func() {
		h.updChecking = false
		if err != nil {
			c.Toast(err.Error())
		}
	})
}

// updateLabel is update.tsx's updateLabel.
func (h *Hub) updatePhase() (phase string, info *model.UpdateInfo, progress float64) {
	info, _, installing, done, total := h.a.UpdateState()
	progress = -1
	if total > 0 {
		progress = min(1, float64(done)/float64(total))
	}
	switch {
	case installing:
		return "installing", info, progress
	case h.updChecking:
		return "checking", info, progress
	case info != nil && info.Available:
		return "available", info, progress
	case info != nil:
		return "latest", info, progress
	}
	return "idle", info, progress
}

func updateLabel(phase string, info *model.UpdateInfo, progress float64) string {
	switch phase {
	case "checking":
		return "检查中…"
	case "latest":
		return "已是最新版本"
	case "available":
		if info != nil {
			return "发现新版本 " + info.Version
		}
		return "发现新版本"
	case "installing":
		if progress >= 1 {
			return "正在重启…"
		}
		return "正在下载…"
	}
	return ""
}
