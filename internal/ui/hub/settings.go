package hub

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

var categories = []struct {
	key, label string
	icon       *ui.SVG
}{
	{"general", "常规", icons.Sliders},
	{"shortcuts", "快捷键", icons.Keyboard},
	{"audio", "音频", icons.Mic},
	{"language", "语言", icons.Languages},
	{"providers", "模型服务", icons.Cloud},
	{"style", "个性化", icons.Sparkles},
	{"data", "数据与迁移", icons.ArrowsLR},
	{"permissions", "权限", icons.Shield},
	{"about", "关于", icons.Info},
}

type tester struct {
	loading bool
	result  *model.TestResult
}

type settingsState struct {
	rec shortcutRec

	micOpen bool
	micSel  *string
	mics    []model.MicDevice
	levels  [28]float32
	last    float32

	langOpen, targetOpen bool
	langQuery            string

	asrTest, llmTest tester
	mimoCustom       bool
	cfCustom         bool
	models           []string
	modelsErr        string
	modelsLoading    bool
	modelsFor        string

	typeless                *model.TypelessDetection
	detectedAt              time.Time
	optSettings, optHistory bool
	importing               bool
	importRes               *model.TypelessImportResult
	quitting                bool

	perms   model.Permissions
	permsAt time.Time

	profile    *model.PersonalProfile
	draft      string
	draftDirty bool
	pzBusy     bool
	pzShowAll  bool
	pzAdvanced bool

	lic, deps bool
	depsQuery string
	notesOpen bool
}

func (h *Hub) settingsModal(c *ui.Context) {
	if !h.settingsOpen {
		if h.set.rec.active {
			h.stopRec()
		}
		return
	}
	p := kit.P(c)
	kit.Modal(c.Key("settings"), &h.settingsOpen, 860, 640, func() {
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(210).Padding(22, 12).Gap(3).Background(p.Card2).BorderWidth(0, 1, 0, 0).BorderColor(p.Divider).Children(func() {
				ui.Text(c, "设置").FontSize(18).FontWeight(750).Padding(0, 12, 14)
				for _, cat := range categories {
					b := ui.ButtonBase(c.Key(cat.key)).Justify(ui.Start).Gap(10).Height(38).PaddingX(12).Radius(11).
						FontSize(14.5).FontWeight(550).FocusRing(false)
					switch {
					case h.category == cat.key:
						b.Background(p.Selected).TextColor(p.Text)
					case b.Hovered():
						b.TextColor(p.Text)
					default:
						b.TextColor(p.Text2)
					}
					b.Children(func() {
						ui.Icon(c, cat.icon).FontSize(17)
						ui.Text(c, cat.label)
					})
					if b.Clicked() {
						if h.category == "shortcuts" {
							h.stopRec()
						}
						h.category = cat.key
					}
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				label := ""
				for _, cat := range categories {
					if cat.key == h.category {
						label = cat.label
					}
				}
				ui.Row(c).Padding(18, 20, 6, 30).Children(func() {
					ui.Text(c, label).FontSize(20).FontWeight(750).Grow(1)
					if kit.IconBtn(c, icons.X, "关闭", 34).Clicked() {
						h.settingsOpen = false
					}
				})
				ui.Scroll(c.Key("scroll-" + h.category)).Grow(1).MinHeight(0).Children(func() {
					body := ui.Column(c.Key(h.category)).Padding(8, 30, 30)
					body.Opacity(body.Animate("in", 1, 120*time.Millisecond))
					body.Children(func() {
						switch h.category {
						case "general":
							h.generalSection(c)
						case "shortcuts":
							h.shortcutsSection(c)
						case "audio":
							h.audioSection(c)
						case "language":
							h.languageSection(c)
						case "providers":
							h.asrForm(c)
							h.llmForm(c)
						case "style":
							h.styleSection(c)
						case "data":
							h.dataSection(c)
						case "permissions":
							h.permissionsSection(c)
						case "about":
							h.aboutSection(c)
						}
					})
				})
			})
		})
	})
	h.micPicker(c)
	h.aboutModals(c)
}

// switchRow is a settings row with a switch bound to a settings field.
func (h *Hub) switchRow(c *ui.Context, title, desc string, get func(*model.Settings) *bool) {
	kit.SRow(c.Key(title), title, desc, false, false, func() {
		v := *get(&h.settings)
		if kit.Switch(c, &v).Label(title).Changed() {
			h.update(func(s *model.Settings) { *get(s) = v })
		}
	})
}

// segmented binds a kit.Segmented to a string setting.
func (h *Hub) segmented(c *ui.Context, cur string, values, labels []string, set func(*model.Settings, string), disabled bool) ui.Element {
	i := max(0, slices.Index(values, cur))
	e := kit.Segmented(c, &i, true, labels...)
	if disabled {
		e.Disabled(true).Opacity(0.6)
	}
	if e.Changed() && values[i] != cur && !disabled {
		v := values[i]
		h.update(func(s *model.Settings) { set(s, v) })
	}
	return e
}

// field is a text input showing value that commits edits (trimmed when trim is set).
func (h *Hub) field(c *ui.Context, value string, placeholder string, mono, secret, disabled bool, width float32, commit func(string)) ui.Element {
	row := ui.Row(c).Width(width)
	buf := ui.Local(row, "buf", func() string { return value })
	var in ui.Element
	row.Children(func() {
		show := ui.Local(row, "show", func() bool { return false })
		in = kit.Input(c, buf, placeholder, mono).Grow(1).Disabled(disabled)
		if secret && !*show {
			in.Password()
		}
		if secret {
			in.Padding(0, 34, 0, 12)
			icon := icons.Eye
			if *show {
				icon = icons.EyeOff
			}
			eye := kit.IconBtn(c, icon, map[bool]string{true: "隐藏", false: "显示"}[*show], 26).Absolute().Right(5).Top(5).FocusRing(false)
			if eye.Clicked() {
				*show = !*show
			}
		}
		if !in.Focused() && *buf != value && !in.Changed() {
			*buf = value
		}
		if in.Changed() {
			commit(*buf)
		}
		if disabled {
			in.Opacity(0.5)
		}
	})
	return in
}

// ── general ──

func (h *Hub) generalSection(c *ui.Context) {
	kit.Group(c, "", func() {
		kit.SRow(c, "外观", "选择明亮模式或黑暗模式。", false, false, func() {
			h.segmented(c, h.settings.Appearance, []string{"system", "light", "dark"}, []string{"系统", "明亮", "黑暗"},
				func(s *model.Settings, v string) { s.Appearance = v }, false)
		})
		h.switchRow(c, "在 Dock 中显示应用", "", func(s *model.Settings) *bool { return &s.ShowInDock })
		h.switchRow(c, "开机启动", "登录 Mac 时自动启动 Hoshiro。", func(s *model.Settings) *bool { return &s.LaunchAtLogin })
		h.switchRow(c, "在桌面上显示悬浮条", "空闲时在屏幕底部显示待机条。", func(s *model.Settings) *bool { return &s.ShowBarOnDesktop })
	})
}

// ── audio ──

func (h *Hub) audioSection(c *ui.Context) {
	if h.set.mics == nil {
		h.set.mics = h.a.ListMics()
	}
	cur := h.settings.MicrophoneDeviceID
	name := "自动检测"
	if cur != nil {
		name = "未知设备"
		for _, d := range h.set.mics {
			if d.ID == *cur {
				name = d.Name
			}
		}
	}
	kit.Group(c, "", func() {
		kit.SRow(c, "麦克风", "", false, false, func() {
			if kit.Btn(c, name, kit.Secondary, kit.Md, icons.Mic).MaxWidth(260).Clicked() {
				h.openMicPicker()
			}
		})
		h.switchRow(c, "交互声音", "为开始/停止等关键操作播放声音。", func(s *model.Settings) *bool { return &s.InteractionSounds })
		h.switchRow(c, "语音输入时静音", "在语音输入时自动静音其他活动音频。", func(s *model.Settings) *bool { return &s.MuteWhileDictating })
	})
}

func (h *Hub) openMicPicker() {
	s := &h.set
	s.micOpen = true
	s.micSel = h.settings.MicrophoneDeviceID
	s.mics = h.a.ListMics()
	s.levels = [28]float32{}
	h.startMicTest()
}

func (h *Hub) startMicTest() {
	dev := ""
	if h.set.micSel != nil {
		dev = *h.set.micSel
	}
	_ = h.a.StartMicTest(dev)
}

func micKind(kind string) (*ui.SVG, string) {
	switch kind {
	case "builtin":
		return icons.Laptop, "推荐"
	case "bluetooth":
		return icons.Bluetooth, "蓝牙麦克风"
	case "usb":
		return icons.Usb, "外部麦克风"
	case "virtual":
		return icons.Mic, "外部麦克风"
	}
	return icons.Mic, ""
}

func (h *Hub) micPicker(c *ui.Context) {
	s := &h.set
	if !s.micOpen {
		return
	}
	p := kit.P(c)
	if lv, on := h.a.MicLevel(); on {
		sm := s.last*0.35 + lv*0.65
		s.last = sm
		copy(s.levels[:], s.levels[1:])
		s.levels[len(s.levels)-1] = sm
		c.AnimationFrame()
	}
	closeIt := func() {
		s.micOpen = false
		h.a.StopMicTest()
	}
	kit.Modal(c.Key("mic"), &s.micOpen, 460, 0, func() {
		kit.ModalHeader(c, "麦克风", "选择能捕捉到您声音的麦克风。如果指示条没有移动，请尝试其他麦克风。", closeIt)
		ui.Column(c).Padding(8, 26, 6).Children(func() {
			ui.Row(c).Height(52).Gap(3).Justify(ui.Center).Padding(9, 14).Radius(14).Background(p.Input).Border(1, p.Border).Margin(0, 0, 14).Children(func() {
				for i, l := range s.levels {
					hgt := max(3, float32(math.Min(1, math.Pow(float64(l), 0.7)))*34)
					ui.Box(c.Key(i)).Width(4).Height(hgt).Radius(2).Background(p.Accent)
				}
			})
			ui.Column(c).Gap(4).Children(func() {
				var def *model.MicDevice
				for i := range s.mics {
					if s.mics[i].IsDefault {
						def = &s.mics[i]
					}
				}
				sub := "使用系统默认麦克风"
				if def != nil {
					sub = "使用系统默认麦克风（" + def.Name + "）"
				}
				if micRow(c.Key("auto"), icons.Wand, "自动检测", sub, "", s.micSel == nil) {
					s.micSel = nil
					h.startMicTest()
				}
				for _, d := range s.mics {
					icon, tag := micKind(d.Kind)
					if micRow(c.Key(d.ID), icon, d.Name, "", tag, s.micSel != nil && *s.micSel == d.ID) {
						id := d.ID
						s.micSel = &id
						h.startMicTest()
					}
				}
				if len(s.mics) == 0 {
					ui.Column(c).Padding(16).AlignItems(ui.Center).Gap(4).Children(func() {
						ui.Text(c, "未检测到麦克风").FontWeight(650)
						ui.Text(c, "检查您的设备设置或连接麦克风。").FontSize(13).TextColor(p.Text2)
					})
				}
			})
		})
		ui.Row(c).Justify(ui.End).Gap(10).Padding(16, 26, 22).Children(func() {
			if kit.Btn(c, "取消", kit.Secondary, kit.Md, nil).Clicked() {
				closeIt()
			}
			if kit.Btn(c, "保存", kit.Primary, kit.Md, nil).Clicked() {
				sel := s.micSel
				h.update(func(st *model.Settings) { st.MicrophoneDeviceID = sel })
				closeIt()
			}
		})
	})
	if !s.micOpen {
		h.a.StopMicTest()
	}
}

func micRow(c *ui.Context, icon *ui.SVG, name, sub, tag string, selected bool) bool {
	p := kit.P(c)
	b := ui.ButtonBase(c).Justify(ui.Start).Gap(12).Padding(10, 12).Radius(12).FocusRing(false)
	switch {
	case selected:
		b.Background(p.AccentSoft)
	case b.Hovered():
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Row(c).Size(32, 32).Radius(9).Justify(ui.Center).Background(p.Card).Border(1, p.Border).Children(func() {
			ui.Icon(c, icon).FontSize(17)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(14.5).FontWeight(600).SingleLine().Ellipsis("…")
			if sub != "" {
				ui.Text(c, sub).FontSize(12.5).TextColor(p.Text2).SingleLine().Ellipsis("…")
			}
		})
		if tag != "" {
			ui.Text(c, tag).FontSize(11.5).FontWeight(600).Padding(2, 8).Radius(999).Background(p.Hover).TextColor(p.Text2)
		}
		if selected {
			ui.Icon(c, icons.Check).FontSize(16).TextColor(p.Accent)
		}
	})
	return b.Clicked()
}

// ── language ──

func (h *Hub) langPicker(c *ui.Context, open *bool, label string, selected []string, maxN int, tip string, toggle func(string)) {
	p := kit.P(c)
	full := maxN > 0 && len(selected) >= maxN
	b := kit.Btn(c, label, kit.Ghost, kit.Sm, icons.Plus)
	if full {
		b.Opacity(0.5)
		if tip != "" {
			b.Tooltip(tip)
		}
	}
	if b.Clicked() && !full {
		*open = !*open
		h.set.langQuery = ""
	}
	ui.PopoverBase(c, b, open, func(panel ui.Element) {
		panel.Margin(6, 0, 0, 0).Padding(8).Radius(14).Background(p.Popover).Shadow(0, 12, 32, 0, p.PopoverShadow).Border(1, p.Border).
			Width(300).Gap(6)
		in := kit.Input(c, &h.set.langQuery, "搜索语言", false).AutoFocus()
		_ = in
		q := strings.ToLower(strings.TrimSpace(h.set.langQuery))
		ui.Scroll(c).MaxHeight(280).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				n := 0
				for _, l := range languages {
					if q != "" && !strings.Contains(strings.ToLower(l.label), q) && !strings.Contains(strings.ToLower(l.code), q) {
						continue
					}
					n++
					on := slices.Contains(selected, l.code)
					it := ui.ButtonBase(c.Key(l.code)).Justify(ui.Start).Gap(9).MinHeight(34).Padding(6, 10).Radius(9).FontSize(14).
						FocusRing(false).Disabled(!on && full)
					if it.Hovered() {
						it.Background(p.Hover)
					}
					if !on && full {
						it.Opacity(0.45)
					}
					it.Children(func() {
						ui.Text(c, l.label).Grow(1)
						ui.Text(c, l.code).FontSize(12).TextColor(p.Text3)
						if on {
							ui.Icon(c, icons.Check).FontSize(15).TextColor(p.Accent)
						}
					})
					if it.Clicked() {
						toggle(l.code)
					}
				}
				if n == 0 {
					ui.Text(c, "没有选项").FontSize(13).TextColor(p.Text2).Padding(8, 10)
				}
			})
		})
	})
}

func removableChip(c *ui.Context, label string, primary bool, canRemove bool, onLabel func(), remove func()) {
	p := kit.P(c)
	ch := kit.Chip(c, "", primary).Padding(0, 6, 0, 12).Gap(6)
	ch.Children(func() {
		if onLabel != nil {
			b := ui.ButtonBase(c).FocusRing(false).FontSize(13.5).FontWeight(550)
			if primary {
				b.Disabled(true).Tooltip("当前目标语言")
			} else {
				b.Tooltip("设为当前目标语言")
			}
			b.Children(func() { ui.Text(c, label) })
			if b.Clicked() {
				onLabel()
			}
		} else {
			ui.Text(c, label)
		}
		if primary {
			ui.Text(c, "当前").FontSize(11).FontWeight(650).TextColor(p.Accent)
		}
		if canRemove {
			x := ui.ButtonBase(c).Size(18, 18).Radius(9).Justify(ui.Center).TextColor(p.Text3).Label("移除").FocusRing(false)
			if x.Hovered() {
				x.Background(p.Hover).TextColor(p.Text)
			}
			x.Children(func() { ui.Icon(c, icons.X).FontSize(12) })
			if x.Clicked() {
				remove()
			}
		}
	})
}

func (h *Hub) languageSection(c *ui.Context) {
	sel, targets := h.settings.SelectedLanguages, h.settings.TranslationTargets
	kit.Group(c, "", func() {
		kit.SRow(c, "听写语言", "选择您在口述中使用的语言及其首选变体，以获得最佳转录效果。", true, false, func() {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, code := range sel {
					code := code
					removableChip(c.Key(code), languageLabel(code), false, len(sel) > 1, nil, func() {
						h.update(func(s *model.Settings) {
							s.SelectedLanguages = slices.DeleteFunc(slices.Clone(s.SelectedLanguages), func(x string) bool { return x == code })
						})
					})
				}
				h.langPicker(c, &h.set.langOpen, "添加语言", sel, 0, "", func(code string) {
					h.update(func(s *model.Settings) {
						if slices.Contains(s.SelectedLanguages, code) {
							if len(s.SelectedLanguages) > 1 {
								s.SelectedLanguages = slices.DeleteFunc(slices.Clone(s.SelectedLanguages), func(x string) bool { return x == code })
							}
						} else {
							s.SelectedLanguages = append(slices.Clone(s.SelectedLanguages), code)
						}
					})
				})
			})
		})
		h.switchRow(c, "自动检测语言", "由语音识别自动判断所说的语言，可混合使用多种语言。", func(s *model.Settings) *bool { return &s.AutoSelectLanguages })
	})
	kit.Group(c, "", func() {
		kit.SRow(c, "翻译目标", "在翻译模式中最多选择 3 种目标语言；第一个为当前使用的目标。", true, false, func() {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for i, code := range targets {
					code := code
					removableChip(c.Key(code), languageLabel(code), i == 0, len(targets) > 1, func() {
						h.update(func(s *model.Settings) {
							rest := slices.DeleteFunc(slices.Clone(s.TranslationTargets), func(x string) bool { return x == code })
							s.TranslationTargets = append([]string{code}, rest...)
						})
					}, func() {
						h.update(func(s *model.Settings) {
							s.TranslationTargets = slices.DeleteFunc(slices.Clone(s.TranslationTargets), func(x string) bool { return x == code })
						})
					})
				}
				h.langPicker(c, &h.set.targetOpen, "添加另一种语言", targets, 3, "最多支持 3 种目标语言。删除一个以添加另一个。", func(code string) {
					h.update(func(s *model.Settings) {
						if slices.Contains(s.TranslationTargets, code) {
							if len(s.TranslationTargets) > 1 {
								s.TranslationTargets = slices.DeleteFunc(slices.Clone(s.TranslationTargets), func(x string) bool { return x == code })
							}
						} else if len(s.TranslationTargets) < 3 {
							s.TranslationTargets = append(slices.Clone(s.TranslationTargets), code)
						}
					})
				})
			})
		})
	})
}

// ── providers ──

const (
	mimoGlobal = "https://api.xiaomimimo.com/v1"
	mimoCN     = "https://token-plan-cn.xiaomimimo.com/v1"
)

var asrShort = map[string]string{"mimo": "MiMo", "cloudflare": "Cloudflare", "azure": "Azure", "google": "Google"}

var cloudflareModels = []kit.Option{
	{Value: "@cf/openai/whisper-large-v3-turbo", Label: "Whisper Large v3 Turbo（推荐）"},
	{Value: "@cf/openai/whisper", Label: "Whisper 旧版（较快，偶尔繁体）"},
	{Value: "@cf/deepgram/nova-3", Label: "Deepgram Nova-3（仅英文）"},
}

var llmProviders = []struct {
	value                   model.LlmProvider
	label, keyHint, urlHint string
}{
	{model.LlmGemini, "Gemini", "Google AI Studio API Key", "留空使用官方地址 generativelanguage.googleapis.com"},
	{model.LlmAnthropic, "Anthropic", "Anthropic API Key", "留空使用官方地址 api.anthropic.com"},
	{model.LlmOpenAI, "OpenAI 兼容", "API Key", "必填，例如 https://api.openai.com/v1"},
}

var efforts, effortLabels = []string{"off", "low", "medium", "high"}, []string{"关闭", "低", "中", "高"}

func (h *Hub) runTest(t *tester, run func() model.TestResult) {
	t.loading, t.result = true, nil
	go func() {
		r := run()
		h.env.Update(func() { t.loading, t.result = false, &r })
	}()
}

func testButton(c *ui.Context, t *tester, disabled bool) bool {
	p := kit.P(c)
	clicked := false
	ui.Row(c).Gap(10).Wrap().Children(func() {
		b := kit.Btn(c, "测试连接", kit.Secondary, kit.Sm, nil).Disabled(disabled || t.loading)
		if t.loading {
			b.Children(func() { kit.Spinner(c, 13) })
		}
		clicked = b.Clicked()
		if r := t.result; r != nil {
			col, icon, msg := p.Success, icons.Check, r.Message
			if r.OK {
				if msg == "" {
					msg = "连接成功"
				}
				if r.LatencyMs > 0 {
					msg += fmt.Sprintf(" · %d ms", r.LatencyMs)
				}
			} else {
				col, icon = p.Danger, icons.X
				if msg == "" {
					msg = "连接失败"
				}
			}
			ui.Row(c).Gap(6).FontSize(13).TextColor(col).MaxWidth(320).Children(func() {
				ui.Icon(c, icon).FontSize(14)
				ui.Text(c, msg).Shrink(1)
			})
		}
	})
	return clicked
}

func (h *Hub) asrForm(c *ui.Context) {
	a := h.settings.Asr
	kit.Group(c, "语音识别", func() {
		kit.SRow(c, "服务商", "", true, false, func() {
			vals := []string{"mimo", "cloudflare", "azure", "google"}
			i := max(0, slices.Index(vals, a.Provider))
			seg := kit.Segmented(c.Key("asr-provider"), &i, false, "MiMo", "Cloudflare", "Azure", "Google")
			if seg.Changed() && vals[i] != a.Provider {
				v := vals[i]
				h.set.asrTest = tester{}
				h.update(func(s *model.Settings) { s.Asr.Provider = v })
			}
		})
	})
	if a.Provider == "mimo" {
		h.mimoForm(c)
	} else {
		h.otherAsrForm(c)
	}
}

func (h *Hub) setAsr(f func(*model.AsrConfig, string)) func(string) {
	return func(v string) { h.update(func(s *model.Settings) { f(&s.Asr, strings.TrimSpace(v)) }) }
}

func (h *Hub) mimoForm(c *ui.Context) {
	m := h.settings.Asr.Mimo
	region := "custom"
	if !h.set.mimoCustom {
		switch m.BaseURL {
		case mimoGlobal:
			region = "global"
		case mimoCN:
			region = "cn"
		}
	}
	desc := map[string]string{"custom": "自定义接口地址", "global": mimoGlobal, "cn": mimoCN}[region]
	kit.Group(c, "语音识别 · 小米 MiMo", func() {
		kit.SRow(c, "API Key", "用于 MiMo ASR；仅保存在本机。", false, false, func() {
			h.field(c, m.APIKey, "粘贴 API Key", true, true, false, 300, h.setAsr(func(a *model.AsrConfig, v string) { a.Mimo.APIKey = v }))
		})
		kit.SRow(c, "区域", desc, false, false, func() {
			vals := []string{"global", "cn", "custom"}
			i := slices.Index(vals, region)
			seg := kit.Segmented(c.Key("mimo-region"), &i, true, "Global", "中国", "自定义")
			if seg.Changed() && vals[i] != region {
				switch vals[i] {
				case "custom":
					h.set.mimoCustom = true
				default:
					h.set.mimoCustom = false
					url := map[string]string{"global": mimoGlobal, "cn": mimoCN}[vals[i]]
					h.update(func(s *model.Settings) { s.Asr.Mimo.BaseURL = url })
				}
			}
		})
		if region == "custom" {
			kit.SRow(c, "接口地址", "", false, false, func() {
				h.field(c, m.BaseURL, mimoGlobal, true, false, false, 300, h.setAsr(func(a *model.AsrConfig, v string) { a.Mimo.BaseURL = v }))
			})
		}
		kit.SRow(c, "模型", "", false, false, func() {
			h.field(c, m.Model, "mimo-v2.5-asr", true, false, false, 300, h.setAsr(func(a *model.AsrConfig, v string) { a.Mimo.Model = v }))
		})
		kit.SRow(c, "连接测试", "发送一小段静音音频验证 Key 与网络。", false, false, func() {
			if testButton(c, &h.set.asrTest, m.APIKey == "") {
				cfg := h.settings.Asr
				h.runTest(&h.set.asrTest, func() model.TestResult { return h.a.TestAsr(h.ctx(), cfg) })
			}
		})
	})
}

func (h *Hub) otherAsrForm(c *ui.Context) {
	a := h.settings.Asr
	title := "语音识别 · " + asrShort[a.Provider]
	if a.Streaming() {
		title += " · 流式"
	}
	text := func(title, desc, value, ph string, secret bool, width float32, set func(*model.AsrConfig, string)) {
		kit.SRow(c.Key(title), title, desc, false, false, func() {
			h.field(c, value, ph, false, secret, false, width, h.setAsr(set))
		})
	}
	kit.Group(c, title, func() {
		switch a.Provider {
		case "cloudflare":
			text("账户 ID", "", a.Cloudflare.AccountID, "", false, 300, func(x *model.AsrConfig, v string) { x.Cloudflare.AccountID = v })
			text("API Token", "需要 Workers AI 读取/运行权限。", a.Cloudflare.APIKey, "粘贴 API Key", true, 300, func(x *model.AsrConfig, v string) { x.Cloudflare.APIKey = v })
			kit.SRow(c, "识别模型", "", false, false, func() {
				ui.Column(c).Gap(8).AlignItems(ui.End).Children(func() {
					preset := slices.ContainsFunc(cloudflareModels, func(o kit.Option) bool { return o.Value == a.Cloudflare.Model })
					sel := a.Cloudflare.Model
					if h.set.cfCustom || !preset {
						sel = "__custom__"
					}
					opts := append(slices.Clone(cloudflareModels), kit.Option{Value: "__custom__", Label: "自定义…"})
					if kit.Select(c, &sel, opts).Width(300).Changed() {
						if sel == "__custom__" {
							h.set.cfCustom = true
						} else {
							h.set.cfCustom = false
							v := sel
							h.update(func(s *model.Settings) { s.Asr.Cloudflare.Model = v })
						}
					}
					if sel == "__custom__" {
						h.field(c, a.Cloudflare.Model, "@cf/…", true, false, false, 300, h.setAsr(func(x *model.AsrConfig, v string) { x.Cloudflare.Model = v }))
					}
				})
			})
		case "azure":
			text("Speech Key", "", a.Azure.APIKey, "粘贴 API Key", true, 300, func(x *model.AsrConfig, v string) { x.Azure.APIKey = v })
			text("区域", "须与 Speech 资源区域一致，例如 eastasia。", a.Azure.Region, "", false, 300, func(x *model.AsrConfig, v string) { x.Azure.Region = v })
		case "google":
			text("项目 ID", "", a.Google.ProjectID, "", false, 300, func(x *model.AsrConfig, v string) { x.Google.ProjectID = v })
			text("区域", "区域须支持所选模型，例如 global、us 或 eu。", a.Google.Location, "", false, 300, func(x *model.AsrConfig, v string) { x.Google.Location = v })
			text("模型", "", a.Google.Model, "", false, 300, func(x *model.AsrConfig, v string) { x.Google.Model = v })
			text("API Key", "使用服务账户时可留空；项目需已启用 Speech-to-Text。", a.Google.APIKey, "粘贴 API Key", true, 300, func(x *model.AsrConfig, v string) { x.Google.APIKey = v })
			kit.SRow(c, "服务账户 JSON", "与 API Key 二选一，优先使用服务账户。", true, false, func() {
				h.field(c, a.Google.ServiceAccountJSON, "粘贴服务账户 JSON", true, true, false, 400, h.setAsr(func(x *model.AsrConfig, v string) { x.Google.ServiceAccountJSON = v }))
			})
		}
		kit.SRow(c, "连接测试", "会产生极少量用量。", false, false, func() {
			if testButton(c, &h.set.asrTest, !a.Ready()) {
				cfg := a
				h.runTest(&h.set.asrTest, func() model.TestResult { return h.a.TestAsr(h.ctx(), cfg) })
			}
		})
	})
}

func (h *Hub) llmForm(c *ui.Context) {
	l := h.settings.Llm
	off := !l.Enabled
	info := llmProviders[0]
	for _, x := range llmProviders {
		if x.value == l.Provider {
			info = x
		}
	}
	cfg := *l.ActiveEndpoint()
	setEp := func(f func(*model.LlmEndpointConfig, string)) func(string) {
		return func(v string) {
			h.update(func(s *model.Settings) { f(s.Llm.ActiveEndpoint(), strings.TrimSpace(v)) })
		}
	}
	kit.Group(c, "改写（AI 润色）", func() {
		h.switchRow(c, "启用改写", "关闭后直接输入原始转录。", func(s *model.Settings) *bool { return &s.Llm.Enabled })
		kit.SRow(c, "服务商", "", false, off, func() {
			vals := []string{"gemini", "anthropic", "openai"}
			h.segmented(c.Key("llm-provider"), string(l.Provider), vals, []string{"Gemini", "Anthropic", "OpenAI 兼容"},
				func(s *model.Settings, v string) {
					s.Llm.Provider = model.LlmProvider(v)
					h.set.llmTest = tester{}
					h.set.models = nil
				}, off)
		})
		kit.SRow(c.Key("key-"+string(l.Provider)), "API Key", info.keyHint, false, off, func() {
			h.field(c, cfg.APIKey, "粘贴 API Key", true, true, off, 300, setEp(func(e *model.LlmEndpointConfig, v string) { e.APIKey = v }))
		})
		kit.SRow(c.Key("url-"+string(l.Provider)), "接口地址", info.urlHint, false, off, func() {
			ph := "默认"
			if l.Provider == model.LlmOpenAI {
				ph = "https://api.openai.com/v1"
			}
			h.field(c, cfg.BaseURL, ph, true, false, off, 300, setEp(func(e *model.LlmEndpointConfig, v string) { e.BaseURL = v }))
		})
		kit.SRow(c.Key("model-"+string(l.Provider)), "模型", "", false, off, func() {
			h.modelInput(c, l.Provider, cfg, cfg.Model, "", off, setEp(func(e *model.LlmEndpointConfig, v string) { e.Model = v }))
		})
		kit.SRow(c, "连接测试", "", false, off, func() {
			if testButton(c, &h.set.llmTest, off || cfg.APIKey == "") {
				p := l.Provider
				h.runTest(&h.set.llmTest, func() model.TestResult { return h.a.TestLlm(h.ctx(), p, cfg) })
			}
		})
		desc := "推荐关闭。开启后会明显变慢，效果提升有限。"
		kit.SRow(c, "思考强度", desc, false, off, func() {
			h.segmented(c.Key("llm-effort"), l.ReasoningEffort, efforts, effortLabels, func(s *model.Settings, v string) { s.Llm.ReasoningEffort = v }, off)
		})
		if l.ReasoningEffort != "off" {
			ui.Text(c, "不推荐：会明显变慢").FontSize(12.5).TextColor(kit.P(c).Warning).Padding(0, 18, 12)
		}
		kit.SRow(c, "超时", "超过该时间仍未返回时，改为输入原始转录。", false, off, func() {
			ui.Row(c).Gap(8).Children(func() {
				h.field(c, strconv.Itoa(int(l.TimeoutSecs)), "", false, false, off, 84, func(v string) {
					if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && n > 0 {
						secs := uint32(min(120, max(3, math.Round(n))))
						h.update(func(s *model.Settings) { s.Llm.TimeoutSecs = secs })
					}
				})
				ui.Text(c, "秒").TextColor(kit.P(c).Text2)
			})
		})
	})
}

// modelInput is the model field with its 「获取模型列表」 popover.
func (h *Hub) modelInput(c *ui.Context, provider model.LlmProvider, cfg model.LlmEndpointConfig, value, placeholder string, disabled bool, pick func(string)) {
	p := kit.P(c)
	s := &h.set
	ui.Row(c).Gap(8).Children(func() {
		h.field(c.Key(value), value, placeholder, true, false, disabled, 300, pick)
		btn := kit.Btn(c, "获取模型列表", kit.Secondary, kit.Sm, nil).Disabled(disabled || cfg.APIKey == "")
		btn.Children(func() { ui.Icon(c, icons.ChevronDown).FontSize(14) })
		open := ui.Local(btn, "open", func() bool { return false })
		if btn.Clicked() {
			*open = true
			s.modelsLoading, s.modelsErr, s.models = true, "", nil
			go func() {
				list, err := h.a.ListModels(h.ctx(), provider, cfg)
				h.env.Update(func() {
					s.modelsLoading, s.models = false, list
					if err != nil {
						s.modelsErr = err.Error()
					}
				})
			}()
		}
		ui.PopoverBase(c, btn, open, func(panel ui.Element) {
			panel.Margin(6, 0, 0, 0).Padding(6).Radius(14).Background(p.Popover).Shadow(0, 12, 32, 0, p.PopoverShadow).Border(1, p.Border).
				MinWidth(260).MaxHeight(320).AttachTo(btn, ui.AnchorBottomRight, ui.AnchorTopRight)
			ui.Scroll(c).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					note := func(t string, col ui.Color) {
						ui.Row(c).Gap(8).Padding(8, 10).FontSize(13).TextColor(col).Children(func() {
							if s.modelsLoading {
								kit.Spinner(c, 14)
							}
							ui.Text(c, t).Shrink(1)
						})
					}
					switch {
					case s.modelsLoading:
						note("正在获取…", p.Text2)
					case s.modelsErr != "":
						note(s.modelsErr, p.Danger)
					case len(s.models) == 0:
						note("没有可用的模型", p.Text2)
					}
					for _, m := range s.models {
						it := ui.ButtonBase(c.Key(m)).Justify(ui.Start).MinHeight(32).Padding(6, 10).Radius(9).FocusRing(false).Font("monospace").FontSize(13)
						if it.Hovered() {
							it.Background(p.Hover)
						}
						if m == value {
							it.FontWeight(650)
						}
						it.Children(func() { ui.Text(c, m) })
						if it.Clicked() {
							pick(m)
							*open = false
						}
					}
				})
			})
		})
	})
}

// ── style / personalization ──

func (h *Hub) styleSection(c *ui.Context) {
	h.personalization(c)
	st := h.settings.Style
	kit.Group(c, "", func() {
		h.switchRow(c, "智能格式化", "列举转列表，邮箱、网址写成书面形式。", func(s *model.Settings) *bool { return &s.Style.SmartFormatting })
		if st.SmartFormatting {
			desc := "按话题分段，尽量保留原话。"
			if st.LongFormStyle == "list" {
				desc = "几件事连着说时整理成 1. 2. 3."
			}
			kit.SRow(c, "长段整理", desc, false, false, func() {
				cur := st.LongFormStyle
				if cur == "" {
					cur = "paragraphs"
				}
				h.segmented(c.Key("long-form"), cur, []string{"paragraphs", "list"}, []string{"分段", "列点"},
					func(s *model.Settings, v string) { s.Style.LongFormStyle = v }, false)
			})
		}
		h.switchRow(c, "自动添加到词典", "修改插入的文字后，自动学习更正后的专有名词。", func(s *model.Settings) *bool { return &s.Style.AutoAddDictionary })
	})
	kit.Group(c, "", func() {
		kit.SRow(c, "自定义指令", "附加给改写模型的要求。", true, false, func() {
			p := kit.P(c)
			wrap := ui.Column(c).FillWidth()
			buf := ui.Local(wrap, "instr", func() string { return st.CustomInstructions })
			var ta ui.Element
			wrap.Children(func() { ta = ui.TextArea(c, buf) })
			ta = ta.Placeholder("例如：保持简洁；称呼对方为“您”；不要在句末加句号。").FillWidth().Height(120).
				Radius(12).Background(p.Input).Padding(10, 12).FontSize(14).LineHeight(1.55)
			if ta.Focused() {
				ta.Border(1, p.Accent).Shadow(0, 0, 0, 3, p.AccentSoft)
			} else {
				ta.Border(1, p.BorderStrong)
				if *buf != st.CustomInstructions && !ta.Changed() {
					*buf = st.CustomInstructions
				}
			}
			if ta.Changed() {
				v := *buf
				h.update(func(s *model.Settings) { s.Style.CustomInstructions = v })
			}
		})
	})
}

func shortDate(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return iso
	}
	t = t.Local()
	return fmt.Sprintf("%d月%d日", t.Month(), t.Day())
}

func (h *Hub) personalization(c *ui.Context) {
	p := kit.P(c)
	s := &h.set
	if s.profile == nil || h.take("profile") {
		if pr, err := h.a.Profile(); err == nil {
			s.profile = &pr
			if !s.draftDirty {
				s.draft = pr.PromptAddendum
			}
		}
	}
	pr := s.profile
	enabled := h.settings.Style.AIPersonalization
	running := s.pzBusy || (pr != nil && pr.Running)
	type learnedItem struct {
		key  string
		i    int
		text string
	}
	var learned []learnedItem
	if pr != nil {
		for i, t := range pr.Vocabulary {
			learned = append(learned, learnedItem{"vocabulary", i, t})
		}
		for i, cr := range pr.Corrections {
			learned = append(learned, learnedItem{"corrections", i, cr.Wrong + " → " + cr.Right})
		}
		for i, t := range pr.VerbalTics {
			learned = append(learned, learnedItem{"verbalTics", i, t})
		}
	}
	status := "积累一些记录后自动开始"
	switch {
	case !enabled:
		status = "已关闭"
	case pr != nil && pr.Running:
		status = "正在学习…"
	case pr != nil && pr.UpdatedAt != nil:
		status = fmt.Sprintf("上次学习 %s · 已学会 %d 项", shortDate(*pr.UpdatedAt), len(learned))
	}
	if pr != nil && pr.LastError != nil && *pr.LastError != "" {
		status = "上次学习未完成"
	}
	save := func(next model.PersonalProfile) {
		s.pzBusy = true
		go func() {
			saved, err := h.a.UpdateProfile(next)
			h.env.Update(func() {
				s.pzBusy = false
				if err != nil {
					c.Toast(err.Error())
					return
				}
				s.profile = &saved
				if !s.draftDirty || next.PromptAddendum != pr.PromptAddendum {
					s.draft, s.draftDirty = saved.PromptAddendum, false
				}
			})
		}()
	}
	kit.Group(c, "", func() {
		kit.SRow(c, "AI 个性化学习", status, false, false, func() {
			ui.Row(c).Gap(12).Children(func() {
				if enabled && pr != nil {
					label, icon := "立即学习", icons.Sparkles
					if running {
						label, icon = "学习中", nil
					}
					b := kit.Btn(c, label, kit.Secondary, kit.Sm, icon).
						Disabled(running || pr.PendingSessions < 3 || strings.TrimSpace(h.settings.Llm.ActiveEndpoint().APIKey) == "")
					if b.Clicked() {
						s.pzBusy = true
						go func() {
							saved, err := h.a.RunPersonalization(h.ctx())
							h.env.Update(func() {
								s.pzBusy = false
								if err != nil {
									c.Toast(err.Error())
									return
								}
								s.profile = &saved
								if !s.draftDirty {
									s.draft = saved.PromptAddendum
								}
							})
						}()
					}
				}
				v := enabled
				if kit.Switch(c, &v).Label("AI 个性化学习").Changed() {
					h.update(func(st *model.Settings) { st.Style.AIPersonalization = v })
				}
			})
		})
		if enabled && len(learned) > 0 {
			kit.SRow(c, "已学会", "", true, false, func() {
				shown := learned
				if !s.pzShowAll && len(shown) > 18 {
					shown = shown[:18]
				}
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for _, it := range shown {
						ui.Row(c.Key(it.key+it.text)).Gap(4).Height(28).Padding(0, 4, 0, 10).Radius(999).Background(p.Input).
							Border(1, p.Border).FontSize(13).Children(func() {
							ui.Text(c, it.text)
							x := kit.IconBtn(c, icons.X, "删除"+it.text, 20).Disabled(running)
							if x.Clicked() {
								next := *pr
								switch it.key {
								case "vocabulary":
									next.Vocabulary = slices.Delete(slices.Clone(pr.Vocabulary), it.i, it.i+1)
								case "corrections":
									next.Corrections = slices.Delete(slices.Clone(pr.Corrections), it.i, it.i+1)
								default:
									next.VerbalTics = slices.Delete(slices.Clone(pr.VerbalTics), it.i, it.i+1)
								}
								save(next)
							}
						})
					}
					if len(learned) > len(shown) && kit.LinkBtn(c, fmt.Sprintf("全部 %d 项", len(learned)), true).Clicked() {
						s.pzShowAll = true
					}
				})
			})
		}
	})
	if !enabled || pr == nil {
		return
	}
	adv := ui.ButtonBase(c).Justify(ui.Start).Gap(6).Margin(0, 4, 12).FontSize(13).FontWeight(650).TextColor(p.Text2).FocusRing(false)
	adv.Children(func() {
		icon := icons.ChevronRight
		if s.pzAdvanced {
			icon = icons.ChevronDown
		}
		ui.Icon(c, icon).FontSize(14)
		ui.Text(c, "高级")
	})
	if adv.Clicked() {
		s.pzAdvanced = !s.pzAdvanced
	}
	if !s.pzAdvanced {
		return
	}
	h.analysisModel(c)
	kit.Group(c, "", func() {
		kit.SRow(c, "个人提示词", "", true, false, func() {
			ui.Column(c).Gap(10).Children(func() {
				ta := ui.TextArea(c, &s.draft).FillWidth().Height(100).Radius(12).Background(p.Input).Padding(10, 12).FontSize(14).
					Border(1, p.BorderStrong).Disabled(running).Label("个人提示词")
				if ta.Changed() {
					s.draftDirty = true
					if r := []rune(s.draft); len(r) > 1200 {
						s.draft = string(r[:1200])
					}
				}
				ui.Row(c).Gap(14).Children(func() {
					if kit.Btn(c, "保存", kit.Secondary, kit.Sm, nil).Disabled(running || s.draft == pr.PromptAddendum).Clicked() {
						next := *pr
						next.PromptAddendum = s.draft
						s.draftDirty = false
						save(next)
					}
					if pr.AddendumEditedByUser && kit.LinkBtn(c, "恢复自动更新", false).Disabled(running).Clicked() {
						next := *pr
						next.AddendumEditedByUser = false
						save(next)
					}
					ui.Spacer(c)
					reset := kit.LinkBtn(c, "清空学习结果", false).Disabled(running).TextColor(p.Danger)
					if reset.Clicked() {
						s.pzBusy = true
						go func() {
							saved, err := h.a.ResetProfile()
							h.env.Update(func() {
								s.pzBusy = false
								if err != nil {
									c.Toast(err.Error())
									return
								}
								s.profile, s.draft, s.draftDirty = &saved, saved.PromptAddendum, false
							})
						}()
					}
				})
			})
		})
	})
}

func (h *Hub) analysisModel(c *ui.Context) {
	l := h.settings.Llm
	pz := l.Personalization
	follow := pz.FollowRewrite
	provider := pz.Provider
	if follow {
		provider = l.Provider
	}
	cfg := *l.Endpoint(provider)
	kit.Group(c, "", func() {
		desc := ""
		if follow {
			desc = "与改写相同"
		}
		kit.SRow(c, "学习模型", desc, false, false, func() {
			cur := "custom"
			if follow {
				cur = "follow"
			}
			h.segmented(c.Key("pz-follow"), cur, []string{"follow", "custom"}, []string{"跟随改写", "单独指定"},
				func(s *model.Settings, v string) { s.Llm.Personalization.FollowRewrite = v == "follow" }, false)
		})
		if !follow {
			kit.SRow(c, "服务商", "", false, false, func() {
				h.segmented(c.Key("pz-provider"), string(pz.Provider), []string{"gemini", "anthropic", "openai"}, []string{"Gemini", "Anthropic", "OpenAI 兼容"},
					func(s *model.Settings, v string) { s.Llm.Personalization.Provider = model.LlmProvider(v) }, false)
			})
			desc := ""
			if strings.TrimSpace(cfg.APIKey) == "" {
				desc = "该服务商还没有 API Key"
			}
			kit.SRow(c.Key("pz-model-"+string(provider)), "模型", desc, false, false, func() {
				ph := cfg.Model
				if ph == "" {
					ph = "默认"
				}
				h.modelInput(c, provider, cfg, pz.Model, ph, false, func(m string) {
					m = strings.TrimSpace(m)
					h.update(func(s *model.Settings) { s.Llm.Personalization.Model = m })
				})
			})
		}
		kit.SRow(c, "思考强度", "", false, false, func() {
			h.segmented(c.Key("pz-effort"), pz.ReasoningEffort, efforts, effortLabels, func(s *model.Settings, v string) { s.Llm.Personalization.ReasoningEffort = v }, false)
		})
	})
}

// ── data ──

func (h *Hub) detectTypeless(force bool) {
	s := &h.set
	if !force && s.typeless != nil && time.Since(s.detectedAt) < 4*time.Second {
		return
	}
	d := h.a.DetectTypeless()
	s.typeless, s.detectedAt = &d, time.Now()
}

func (h *Hub) dataSection(c *ui.Context) {
	p := kit.P(c)
	s := &h.set
	if s.typeless == nil {
		s.optSettings, s.optHistory = true, true
	}
	h.detectTypeless(false)
	c.After(4 * time.Second) // usePoll(detect, 4000)
	det := s.typeless
	kit.Group(c, "从 Typeless 导入", func() {
		ui.Column(c).Padding(18).Gap(14).Children(func() {
			found := det.Installed && det.DataFound
			if !found {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, "未检测到 Typeless 数据").FontWeight(650)
					ui.Text(c, "未在 ~/Library/Application Support/Typeless 中找到设置或历史记录。").FontSize(13).TextColor(p.Text2)
				})
			} else {
				ui.Row(c).Gap(16).Wrap().Children(func() {
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, "已检测到 Typeless").FontSize(14.5).FontWeight(650)
						run := ""
						if det.Running {
							run = "正在运行 · "
						}
						ui.Text(c, fmt.Sprintf("%s找到 %d 条历史记录", run, det.HistoryCount)).FontSize(13).TextColor(p.Text2).Margin(4, 0, 0)
					})
					if det.Shortcuts != nil {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, "口述快捷键").FontSize(13).TextColor(p.Text2)
							for _, sc := range det.Shortcuts.Dictation {
								kit.ShortcutKeys(c.Key(sc), sc, true)
							}
						})
					}
				})
				if det.Running {
					kit.Banner(c, "Typeless 正在运行，会与 Hoshiro 抢占 Fn 键", func() {
						if kit.Btn(c, "退出 Typeless", kit.Secondary, kit.Sm, nil).Disabled(s.quitting).Clicked() {
							s.quitting = true
							go func() {
								_ = h.a.QuitTypeless()
								time.Sleep(600 * time.Millisecond)
								h.env.Update(func() { s.quitting = false; h.detectTypeless(true) })
							}()
						}
					})
				}
				check := func(v *bool, label string) {
					ui.Row(c.Key(label)).Gap(10).Children(func() {
						kit.Checkbox(c, v)
						ui.Text(c, label).FontSize(14)
					})
				}
				check(&s.optSettings, "设置（快捷键、语言、外观等偏好）")
				check(&s.optHistory, "历史记录（不含音频）")
				ui.Row(c).Children(func() {
					b := kit.Btn(c, "从 Typeless 导入", kit.Primary, kit.Md, nil).Disabled(s.importing || (!s.optSettings && !s.optHistory))
					if s.importing {
						b.Children(func() { kit.Spinner(c, 14) })
					}
					if b.Clicked() {
						s.importing = true
						opts := model.TypelessImportOptions{Settings: s.optSettings, History: s.optHistory}
						go func() {
							r, err := h.a.ImportTypeless(opts)
							h.env.Update(func() {
								s.importing = false
								if err != nil {
									c.Toast("导入失败：" + err.Error())
									return
								}
								s.importRes = &r
							})
						}()
					}
				})
				if r := s.importRes; r != nil {
					ui.Column(c).Gap(6).Padding(12, 14).Radius(12).Background(p.SuccessSoft).FontSize(13.5).Children(func() {
						ui.Text(c, "导入完成").FontWeight(650)
						lines := []string{map[bool]string{true: "已导入设置", false: "未导入设置"}[r.SettingsImported],
							fmt.Sprintf("历史记录：导入 %d 条，跳过 %d 条", r.HistoryImported, r.HistorySkipped)}
						for i, l := range append(lines, r.Messages...) {
							ui.Text(c.Key(i), "• "+l).TextColor(p.Text2)
						}
					})
				}
			}
			ui.Text(c, "Typeless 的词典保存在其云端，无法自动导入；可整理成单列 CSV 后在「词典」页面使用「导入 CSV」。").FontSize(13).TextColor(p.Text2).LineHeight(1.5)
		})
	})
	kit.Group(c, "本地数据", func() {
		kit.SRow(c, "数据文件夹", "", false, false, func() {
			if kit.Btn(c, "打开数据文件夹", kit.Secondary, kit.Md, icons.FolderOpen).Clicked() {
				if h.env.Reveal != nil {
					h.env.Reveal(h.a.Paths.DataDir)
				} else {
					c.Toast("仅在桌面应用中可用")
				}
			}
		})
	})
}

// ── permissions ──

var permissionInfo = []struct {
	kind        model.PermissionKind
	title, desc string
}{
	{model.PermMicrophone, "麦克风", "仅在您按下快捷键口述时才会使用麦克风录音。"},
	{model.PermAccessibility, "辅助功能", "用于读取当前应用的上下文，并将文字粘贴到任何文本框中。"},
	{model.PermInputMonitoring, "输入监控", "用于识别 Fn 等全局快捷键。"},
}

func permState(p model.Permissions, k model.PermissionKind) model.PermissionState {
	switch k {
	case model.PermMicrophone:
		return p.Microphone
	case model.PermAccessibility:
		return p.Accessibility
	}
	return p.InputMonitoring
}

func (h *Hub) pollPermissions(c *ui.Context) {
	s := &h.set
	if h.take("permissions") || time.Since(s.permsAt) > 2*time.Second {
		s.perms, s.permsAt = h.a.Permissions(), time.Now()
	}
	c.After(2 * time.Second)
}

func (h *Hub) permissionAction(c *ui.Context, kind model.PermissionKind) {
	p := kit.P(c)
	switch permState(h.set.perms, kind) {
	case model.PermGranted:
		ui.Row(c).Gap(6).FontSize(13.5).FontWeight(600).TextColor(p.Success).Children(func() {
			ui.Icon(c, icons.Check).FontSize(15)
			ui.Text(c, "已授权")
		})
	case model.PermDenied:
		if kit.Btn(c, "打开系统设置", kit.Secondary, kit.Sm, nil).Clicked() {
			platform.OpenPermissionSettings(kind)
		}
	case "":
	default:
		if kit.Btn(c, "授权", kit.Primary, kit.Sm, nil).Clicked() {
			go func() {
				ps := h.a.RequestPermission(kind)
				h.env.Update(func() {
					h.set.perms, h.set.permsAt = ps, time.Now()
					if permState(ps, kind) != model.PermGranted && kind != model.PermMicrophone {
						platform.OpenPermissionSettings(kind)
					}
				})
			}()
		}
	}
}

func (h *Hub) permissionRows(c *ui.Context) {
	p := kit.P(c)
	h.pollPermissions(c)
	for _, info := range permissionInfo {
		st := permState(h.set.perms, info.kind)
		ui.Row(c.Key(string(info.kind))).Gap(24).Padding(15, 18).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					dot := "off"
					switch st {
					case model.PermGranted:
						dot = "ok"
					case model.PermDenied:
						dot = "bad"
					case "":
					default:
						dot = "warn"
					}
					kit.Dot(c, dot)
					ui.Text(c, info.title).FontSize(14.5).FontWeight(650)
				})
				ui.Text(c, info.desc).FontSize(13).TextColor(p.Text2).LineHeight(1.5).Margin(4, 0, 0)
			})
			h.permissionAction(c, info.kind)
		})
	}
}

func (h *Hub) permissionsSection(c *ui.Context) {
	kit.Group(c, "", func() { h.permissionRows(c) })
	kit.Group(c, "", func() {
		kit.SRow(c, "Fn 键", "系统设置 › 键盘 › 按下 🌐 键时：不执行任何操作", false, false, func() {
			if kit.Btn(c, "打开键盘设置", kit.Secondary, kit.Md, nil).Clicked() {
				platform.OpenKeyboardSettings()
			}
		})
	})
}
