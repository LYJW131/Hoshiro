package hub

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

var sideMod = regexp.MustCompile(`^(Left|Right)(Shift|Ctrl|Option|Cmd)$`)

// NormalizedCombo drops key sides and sorts: "RightCmd+Q" -> "Cmd+Q".
func NormalizedCombo(s string) string {
	ks := kit.SplitShortcut(s)
	for i, k := range ks {
		ks[i] = sideMod.ReplaceAllString(k, "$2")
	}
	sort.Strings(ks)
	return strings.Join(ks, "+")
}

var reserved = func() []string {
	var out []string
	for _, s := range []string{"Cmd+Q", "Cmd+Tab", "Cmd+Space", "Cmd+W", "Cmd+H", "Cmd+M"} {
		out = append(out, NormalizedCombo(s))
	}
	return out
}()

var alnum = regexp.MustCompile(`^[A-Z0-9]$`)

// ShortcutErrorText maps ValidateShortcut's errors to the UI text.
var ShortcutErrorText = map[string]string{
	"tooManyKeys":      "快捷键必须包含 3 个或更少的键",
	"alphanumericOnly": "快捷键不能只包含字母和数字",
	"systemReserved":   "此快捷键已保留供系统使用",
	"alreadyInUse":     "此快捷键已在使用中",
}

// ValidateShortcut checks combo against the other configured bindings; "" is fine.
func ValidateShortcut(combo string, others []string) string {
	ks := kit.SplitShortcut(combo)
	if len(ks) > 3 {
		return "tooManyKeys"
	}
	if len(ks) > 0 && !slices.ContainsFunc(ks, func(k string) bool { return !alnum.MatchString(k) }) {
		return "alphanumericOnly"
	}
	norm := NormalizedCombo(combo)
	if slices.Contains(reserved, norm) {
		return "systemReserved"
	}
	for _, o := range others {
		if NormalizedCombo(o) == norm || o == combo {
			return "alreadyInUse"
		}
	}
	return ""
}

func allBindings(b model.ShortcutBindings) []string {
	return slices.Concat(b.Dictation, b.AskAnything, b.Translation, b.PasteLastTranscript)
}

func bindingList(b *model.ShortcutBindings, feature string) *[]string {
	switch feature {
	case "askAnything":
		return &b.AskAnything
	case "translation":
		return &b.Translation
	case "pasteLastTranscript":
		return &b.PasteLastTranscript
	}
	return &b.Dictation
}

var features = []struct{ key, title, desc string }{
	{"dictation", "语音输入", "按住说话，松开结束；双击进入免提模式。"},
	{"askAnything", "随便问", "按下开始和停止随便提问。"},
	{"translation", "翻译", "按下开始和停止翻译。"},
	{"pasteLastTranscript", "粘贴上一次转录", "再次粘贴最近一次的转录结果。"},
}

type shortcutRec struct {
	feature string
	index   int // chip being replaced, -1 for a new one
	errFeat string
	errText string
	active  bool
}

func (h *Hub) stopRec() {
	if h.set.rec.active {
		h.a.StopShortcutCapture()
	}
	h.set.rec.active = false
}

func (h *Hub) startRec(feature string, index int) {
	r := &h.set.rec
	r.errFeat, r.errText = "", ""
	if !r.active {
		_ = h.a.StartShortcutCapture()
	}
	r.active, r.feature, r.index = true, feature, index
}

// pollCapture finishes a recording once the capture reports a combo.
func (h *Hub) pollCapture() string {
	r := &h.set.rec
	if !r.active {
		return ""
	}
	keys, done, _ := h.a.Capture()
	if !done || keys == "" {
		return keys
	}
	if keys == "Esc" {
		h.stopRec()
		return ""
	}
	list := *bindingList(&h.settings.Shortcuts, r.feature)
	others := allBindings(h.settings.Shortcuts)
	if r.index >= 0 && r.index < len(list) {
		if i := slices.Index(others, list[r.index]); i >= 0 {
			others = slices.Delete(others, i, i+1)
		}
	}
	if err := ValidateShortcut(keys, others); err != "" {
		r.errFeat, r.errText = r.feature, ShortcutErrorText[err]
		_ = h.a.StartShortcutCapture() // re-arm
		return ""
	}
	feature, index := r.feature, r.index
	h.update(func(s *model.Settings) {
		l := bindingList(&s.Shortcuts, feature)
		if index >= 0 && index < len(*l) {
			(*l)[index] = keys
		} else {
			*l = append(*l, keys)
		}
	})
	h.stopRec()
	return ""
}

func (h *Hub) shortcutsSection(c *ui.Context) {
	p := kit.P(c)
	live := h.pollCapture()
	r := &h.set.rec
	if r.active {
		c.AnimationFrame() // follow the capture while it runs
	}
	defs := model.DefaultShortcuts()
	kit.Group(c, "", func() {
		for _, f := range features {
			list := *bindingList(&h.settings.Shortcuts, f.key)
			isDefault := slices.Equal(list, *bindingList(&defs, f.key))
			ui.Column(c.Key(f.key)).Gap(12).Padding(15, 18).Children(func() {
				ui.Column(c).Children(func() {
					ui.Row(c).Gap(6).Children(func() {
						ui.Text(c, f.title).FontSize(14.5).FontWeight(650)
						if !isDefault && kit.IconBtn(c, icons.RotateCcw, "重置快捷键", 22).Clicked() {
							h.stopRec()
							key := f.key
							h.update(func(s *model.Settings) { *bindingList(&s.Shortcuts, key) = slices.Clone(*bindingList(&defs, key)) })
						}
					})
					ui.Text(c, f.desc).FontSize(13).TextColor(p.Text2).Margin(4, 0, 0)
				})
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for i, s := range list {
						isRec := r.active && r.feature == f.key && r.index == i
						var remove func()
						if !(f.key == "dictation" && len(list) <= 1) {
							key, idx := f.key, i
							remove = func() {
								h.stopRec()
								h.update(func(st *model.Settings) {
									l := bindingList(&st.Shortcuts, key)
									*l = slices.Delete(*l, idx, idx+1)
								})
							}
						}
						if h.shortcutChip(c.Key(i), s, isRec, live, remove) {
							if isRec {
								h.stopRec()
							} else {
								h.startRec(f.key, i)
							}
						}
					}
					if r.active && r.feature == f.key && r.index == -1 {
						if h.shortcutChip(c.Key("new"), "", true, live, nil) {
							h.stopRec()
						}
					} else {
						label := "添加另一个"
						if len(list) == 0 {
							label = "添加快捷方式"
						}
						if kit.Btn(c, label, kit.Ghost, kit.Sm, icons.Plus).Clicked() {
							h.startRec(f.key, -1)
						}
					}
				})
				if r.errFeat == f.key && r.errText != "" {
					ui.Text(c, r.errText).FontSize(13).TextColor(p.Danger)
				}
			})
		}
	})
}

// shortcutChip is .sc-chip; it reports a click on its main part.
func (h *Hub) shortcutChip(c *ui.Context, shortcut string, recording bool, live string, remove func()) bool {
	p := kit.P(c)
	chip := ui.Row(c).Radius(12).Background(p.Input)
	clicked := false
	chip.Children(func() {
		main := ui.ButtonBase(c).MinHeight(40).Padding(4, 12).Radius(12).FocusRing(false)
		if recording {
			main.Tooltip("取消录制")
		} else {
			main.Tooltip("更改快捷键")
		}
		main.Children(func() {
			switch {
			case recording && live != "":
				kit.ShortcutKeys(c, live, false)
			case recording:
				ui.Text(c, "按下键盘按键或鼠标按钮").FontSize(13.5).TextColor(p.Text2)
			default:
				kit.ShortcutKeys(c, shortcut, false)
			}
		})
		clicked = main.Clicked()
		if !recording && remove != nil {
			x := ui.ButtonBase(c).Size(22, 22).Radius(11).Justify(ui.Center).Margin(0, 6, 0, -4).TextColor(p.Text3).
				Tooltip("禁用快捷键").Label("禁用快捷键").FocusRing(false)
			if x.Hovered() {
				x.Background(p.Hover).TextColor(p.Text)
			}
			x.Children(func() { ui.Icon(c, icons.X).FontSize(12) })
			if x.Clicked() {
				remove()
			}
		}
	})
	switch {
	case recording:
		a := chip.Loop("pulse", 1400*time.Millisecond, ui.EaseInOut)
		chip.Border(1, p.Accent).Shadow(0, 0, 0, 3+2*float32(math.Sin(float64(a)*math.Pi)), p.AccentSoft)
	case chip.Hovered():
		chip.Border(1, p.Text3)
	default:
		chip.Border(1, p.BorderStrong)
	}
	return clicked
}
