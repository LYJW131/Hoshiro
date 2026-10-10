package kit

import (
	"strings"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/egoist/mygo/ui"
)

// Variant of a button, as .btn-primary/.btn-secondary/.btn-ghost/.btn-danger.
type Variant int

const (
	Primary Variant = iota
	Secondary
	Ghost
	Danger
)

// Size of a button: .btn-sm, .btn, .btn-lg.
type Size int

const (
	Sm Size = iota
	Md
	Lg
)

// Card is .card.
func Card(c *ui.Context) ui.Element {
	p := P(c)
	return ui.Column(c).Background(p.Card).Radius(RadiusCard).Border(1, p.Border)
}

// Btn is .btn: a label with an optional leading icon.
func Btn(c *ui.Context, label string, v Variant, s Size, icon *ui.SVG) ui.Element {
	p := P(c)
	b := ui.ButtonBase(c).Gap(7).Justify(ui.Center).FocusRing(false).Shrink(0)
	h, px, r, fs := float32(36), float32(15), float32(RadiusBtn), float32(14)
	switch s {
	case Sm:
		h, px, r, fs = 30, 12, 10, 13
	case Lg:
		h, px, r, fs = 44, 22, 14, 15
	}
	b.Height(h).PaddingX(px).Radius(r).FontSize(fs).FontWeight(600)
	hover := b.Hovered() && !b.IsDisabled()
	switch v {
	case Primary:
		bg := p.Accent
		if hover {
			bg = p.AccentHover
		}
		b.Background(bg).TextColor(p.AccentText)
	case Secondary:
		bg := p.Hover
		if hover {
			bg = p.Hover.Mix(p.Text, 0.08)
		}
		b.Background(bg).Border(1, p.BorderStrong).TextColor(p.Text)
	case Ghost:
		b.TextColor(p.Text2)
		if hover {
			b.Background(p.Hover).TextColor(p.Text)
		}
	case Danger:
		bg := p.Danger
		if hover {
			bg = p.Danger.Mix(ui.RGB(255, 255, 255), 0.08)
		}
		b.Background(bg).TextColor(ui.RGB(255, 255, 255))
	}
	if b.IsDisabled() {
		b.Opacity(0.45)
	}
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(fs + 2)
		}
		if label != "" {
			ui.Text(c, label).SingleLine()
		}
	})
	return b
}

// IconBtn is .icon-btn (size 34, 30 "sm" or 22 "xs").
func IconBtn(c *ui.Context, icon *ui.SVG, label string, size float32) ui.Element {
	p := P(c)
	b := ui.ButtonBase(c).Size(size, size).Justify(ui.Center).Radius(size*0.3).TextColor(p.Text2).FocusRing(false).Label(label)
	if label != "" {
		b.Tooltip(label)
	}
	if b.Hovered() {
		b.Background(p.Hover).TextColor(p.Text)
	}
	is := float32(18)
	if size < 30 {
		is = 14
	} else if size < 34 {
		is = 16
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(is) })
	return b
}

// LinkBtn is .link-btn.
func LinkBtn(c *ui.Context, label string, accent bool) ui.Element {
	p := P(c)
	b := ui.ButtonBase(c).FocusRing(false).FontSize(13)
	col := p.Text
	if accent || b.Hovered() {
		col = p.Accent
	}
	if b.IsDisabled() {
		col = p.Text3
	}
	b.Children(func() {
		t := ui.Text(c, label).TextColor(col)
		if !b.IsDisabled() && !accent {
			t.Underline()
		}
	})
	return b
}

var keyLabels = map[string]string{
	"Backspace": "Delete", "LeftShift": "Left Shift", "RightShift": "Right Shift",
	"LeftCtrl": "Left ⌃", "RightCtrl": "Right ⌃", "Ctrl": "⌃",
	"LeftOption": "Left ⌥", "RightOption": "Right ⌥", "Option": "⌥",
	"LeftCmd": "Left ⌘", "RightCmd": "Right ⌘", "Cmd": "⌘",
	"Up": "↑", "Down": "↓", "Left": "←", "Right": "→",
	"Mouse3": "Mouse 3", "Mouse4": "Mouse 4", "Mouse5": "Mouse 5",
}

// PrettyKey is format.ts's prettyKey.
func PrettyKey(k string) string {
	if l, ok := keyLabels[k]; ok {
		return l
	}
	return k
}

// SplitShortcut splits "Fn+Space".
func SplitShortcut(s string) []string {
	var out []string
	for _, k := range strings.Split(s, "+") {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// ShortcutLabel is "Fn + Space".
func ShortcutLabel(s string) string {
	ks := SplitShortcut(s)
	for i, k := range ks {
		ks[i] = PrettyKey(k)
	}
	return strings.Join(ks, " + ")
}

// Keycap is .keycap (sm = .keycap-sm).
func Keycap(c *ui.Context, label string, sm bool) ui.Element {
	p := P(c)
	k := ui.Row(c).Justify(ui.Center).Background(p.KeycapBg).Border(1, p.KeycapBorder).TextColor(p.KeycapText).
		Shadow(0, 1, 0, 0, ui.RGBA(0, 0, 0, 0.25)).FontWeight(650)
	if sm {
		k.MinWidth(24).Height(24).PaddingX(7).Radius(7).FontSize(12)
	} else {
		k.MinWidth(30).Height(30).PaddingX(10).Radius(9).FontSize(14.5)
	}
	return k.Children(func() { ui.Text(c, label).SingleLine() })
}

// ShortcutKeys is a row of keycaps.
func ShortcutKeys(c *ui.Context, shortcut string, sm bool) ui.Element {
	return ui.Row(c).Gap(6).Wrap().Children(func() {
		for i, k := range SplitShortcut(shortcut) {
			Keycap(c.Key(i), PrettyKey(k), sm)
		}
	})
}

// Switch is .switch bound to v.
func Switch(c *ui.Context, v *bool) ui.Element {
	p := P(c)
	s := ui.SwitchBase(c, v).Size(42, 24).Radius(12).FocusRing(false)
	if *v {
		s.Background(p.Accent).Border(1, p.Accent)
	} else {
		s.Background(p.Hover).Border(1, p.BorderStrong)
	}
	if s.IsDisabled() {
		s.Opacity(0.45)
	}
	x := float32(0)
	if *v {
		x = 18
	}
	x = s.Animate("knob", x, 180*time.Millisecond)
	s.Children(func() {
		ui.Box(c).Absolute().Top(2).Left(2+x).Size(18, 18).Radius(9).Background(ui.RGB(255, 255, 255)).
			Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.35))
	})
	return s
}

// Checkbox is .checkbox bound to v.
func Checkbox(c *ui.Context, v *bool) ui.Element {
	p := P(c)
	b := ui.CheckboxBase(c, v).Size(18, 18).Radius(6).Justify(ui.Center).FocusRing(false)
	if *v {
		b.Background(p.Accent).Border(1.5, p.Accent).TextColor(ui.RGB(255, 255, 255))
	} else {
		col := p.BorderStrong
		if b.Hovered() {
			col = p.Text2
		}
		b.Background(p.Input).Border(1.5, col)
	}
	b.Children(func() {
		if *v {
			ui.Icon(c, icons.Check).FontSize(12)
		}
	})
	return b
}

// Segmented is .segmented (sm = .segmented-sm) choosing one of labels.
func Segmented(c *ui.Context, sel *int, sm bool, labels ...string) ui.Element {
	p := P(c)
	s := ui.SegmentedBase(c, sel, len(labels))
	track := s.Track.Radius(999).Gap(2)
	if sm {
		track.Padding(3).Background(p.Input).Border(1, p.Border)
	} else {
		track.Padding(4).Background(p.Card).Border(1, p.Border)
	}
	track.Children(func() {
		for i, l := range labels {
			seg := s.Segment(i).Radius(999).FocusRing(false).FontWeight(550)
			if sm {
				seg.Height(28).PaddingX(12).FontSize(13)
			} else {
				seg.Height(34).PaddingX(16).FontSize(14.5)
			}
			col := p.Text2
			if i == *sel {
				col = p.Text
				switch {
				case sm && p.Dark:
					seg.Background(p.Hover).Border(1, p.Border)
				case sm:
					seg.Background(ui.RGB(255, 255, 255)).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.12))
				case p.Dark:
					seg.Background(p.Input).Border(1, p.BorderStrong)
				default:
					seg.Background(p.Selected)
				}
			} else if seg.Hovered() {
				col = p.Text
			}
			seg.TextColor(col).Children(func() { ui.Text(c, l).SingleLine() })
		}
	})
	return track
}

// Option is one choice of a Select.
type Option struct{ Value, Label, Hint string }

// Select is .select with a popover .menu-list.
func Select(c *ui.Context, value *string, opts []Option) ui.Element {
	p := P(c)
	s := ui.SelectBase(c, value)
	label := *value
	for _, o := range opts {
		if o.Value == *value {
			label = o.Label
		}
	}
	trig := s.Trigger.Height(40).Padding(0, 12, 0, 14).Radius(12).Gap(8).Background(p.Input).FontSize(15).FontWeight(600).FocusRing(false)
	border := p.BorderStrong
	if trig.Hovered() || s.Open() {
		border = p.Text3
	}
	trig.Border(1, border).Children(func() {
		ui.Text(c, label).SingleLine().Grow(1)
		ui.Icon(c, icons.ChevronDown).FontSize(16).TextColor(p.Text2)
	})
	s.Popup(func(panel ui.Element) {
		panel.Margin(6, 0, 0, 0).Padding(6).Radius(14).Background(p.Popover).MinWidth(trig.Bounds().W).MaxHeight(300).
			Shadow(0, 12, 32, 0, p.PopoverShadow).Border(1, p.Border).Gap(2)
		for _, o := range opts {
			it := s.Item(o.Value).MinHeight(34).Padding(6, 10).Radius(9).Gap(9).FontSize(14)
			if it.Highlighted() {
				it.Background(p.Hover)
			}
			if o.Value == *value {
				it.FontWeight(650)
			}
			it.Children(func() {
				ui.Text(c, o.Label).SingleLine().Grow(1)
				if o.Hint != "" {
					ui.Text(c, o.Hint).FontSize(12).TextColor(p.Text3)
				}
				if o.Value == *value {
					ui.Icon(c, icons.Check).FontSize(15).TextColor(p.Accent)
				}
			})
		}
	})
	return trig
}

// Input is .input bound to v.
func Input(c *ui.Context, v *string, placeholder string, mono bool) ui.Element {
	p := P(c)
	in := ui.TextInput(c, v).Placeholder(placeholder).Height(36).Radius(10).Background(p.Input).FontSize(14)
	if mono {
		in.Font("monospace").FontSize(13)
	}
	if in.Focused() {
		in.Border(1, p.Accent).Shadow(0, 0, 0, 3, p.AccentSoft)
	} else {
		in.Border(1, p.BorderStrong)
	}
	return in
}

// Spinner is .spinner.
func Spinner(c *ui.Context, size float32) ui.Element {
	p := P(c)
	s := ui.Box(c).Size(size, size)
	angle := s.Loop("spin", 700*time.Millisecond, ui.Linear) * 360
	return s.Draw(func(pt *ui.Painter, r ui.Rect) {
		cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, r.W/2-1
		var ring, arc ui.Path
		circle(&ring, cx, cy, rad, 0, 360)
		pt.StrokePath(&ring, 2, p.BorderStrong)
		circle(&arc, cx, cy, rad, angle-90, angle)
		pt.StrokePath(&arc, 2, p.Accent)
	})
}

// Dot is .dot (kind: ok, bad, warn, off).
func Dot(c *ui.Context, kind string) ui.Element {
	p := P(c)
	d := ui.Box(c).Size(8, 8).Radius(4).Background(p.Text3)
	switch kind {
	case "ok":
		d.Background(p.Success).Shadow(0, 0, 0, 3, p.SuccessSoft)
	case "bad":
		d.Background(p.Danger).Shadow(0, 0, 0, 3, p.DangerSoft)
	case "warn":
		d.Background(p.Warning).Shadow(0, 0, 0, 3, p.WarningSoft)
	}
	return d
}

// Modal is .modal over .modal-backdrop.
func Modal(c *ui.Context, open *bool, width, height float32, fn func()) ui.Element {
	p := P(c)
	return ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.Overlay)
		w, h := c.Size()
		panel.Width(min(width, w-32)).Radius(22).Background(p.Modal).Clip().
			Shadow(0, 24, 64, 0, p.ModalShadow).Border(1, p.Border).MaxHeight(h - 40)
		if height > 0 {
			panel.Height(min(height, h-40))
		}
		fn()
	})
}

// ModalHeader is .modal-header.
func ModalHeader(c *ui.Context, title, subtitle string, onClose func()) {
	p := P(c)
	ui.Row(c).Padding(22, 22, 8, 26).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(19).FontWeight(700)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(13.5).TextColor(p.Text2).MaxWidth(360).LineHeight(1.5).Margin(6, 0, 0, 0)
			}
		})
		if onClose != nil && IconBtn(c, icons.X, "关闭", 30).Clicked() {
			onClose()
		}
	})
}

// Confirm is feedback.tsx's confirm dialog. Returns true when confirmed.
type Confirm struct {
	Open         bool
	Title, Desc  string
	OK           string
	Danger       bool
	OnConfirm    func()
}

func (cf *Confirm) Ask(title, desc, ok string, danger bool, f func()) {
	*cf = Confirm{Open: true, Title: title, Desc: desc, OK: ok, Danger: danger, OnConfirm: f}
}

func (cf *Confirm) View(c *ui.Context) {
	if !cf.Open {
		return
	}
	p := P(c)
	Modal(c.Key("confirm"), &cf.Open, 400, 0, func() {
		ui.Column(c).Padding(26, 26, 22).Children(func() {
			ui.Text(c, cf.Title).FontSize(18).FontWeight(700)
			if cf.Desc != "" {
				ui.Text(c, cf.Desc).FontSize(14).TextColor(p.Text2).LineHeight(1.55).Margin(10, 0, 0, 0)
			}
			ui.Row(c).Gap(10).Justify(ui.End).Margin(22, 0, 0, 0).Children(func() {
				if Btn(c, "取消", Secondary, Md, nil).Clicked() {
					cf.Open = false
				}
				v := Primary
				if cf.Danger {
					v = Danger
				}
				if Btn(c, cf.OK, v, Md, nil).Clicked() {
					cf.Open = false
					if cf.OnConfirm != nil {
						cf.OnConfirm()
					}
				}
			})
		})
	})
}

// PageTitle is .page-title-row.
func PageTitle(c *ui.Context, title string, right func()) {
	ui.Row(c).Gap(16).Children(func() {
		ui.Text(c, title).FontSize(48).FontWeight(800).LetterSpacing(-0.96).Grow(1)
		if right != nil {
			right()
		}
	})
}

// Banner is .banner.warn.
func Banner(c *ui.Context, text string, action func()) ui.Element {
	p := P(c)
	return ui.Row(c).Gap(10).Padding(11, 14).Radius(16).Background(p.WarningSoft).Border(1, p.Warning.Alpha(0.35)).
		FontSize(14).FontWeight(550).Children(func() {
		ui.Icon(c, icons.AlertTriangle).FontSize(18).TextColor(p.Warning)
		ui.Text(c, text).Grow(1)
		if action != nil {
			action()
		}
	})
}

// EmptyState is .empty-state.
func EmptyState(c *ui.Context, icon *ui.SVG, title, desc string, action func()) {
	p := P(c)
	ui.Column(c).AlignItems(ui.Center).Gap(10).Padding(70, 20).Children(func() {
		ui.Row(c).Size(60, 60).Radius(20).Justify(ui.Center).Background(p.Card).Border(1, p.Border).TextColor(p.Text2).
			Margin(0, 0, 6, 0).Children(func() { ui.Icon(c, icon).FontSize(26) })
		ui.Text(c, title).FontSize(18).FontWeight(700)
		if desc != "" {
			ui.Text(c, desc).FontSize(14).TextColor(p.Text2).MaxWidth(360).LineHeight(1.55).TextAlign(ui.Center)
		}
		if action != nil {
			action()
		}
	})
}

// Chip is .chip.
func Chip(c *ui.Context, label string, primary bool) ui.Element {
	p := P(c)
	ch := ui.Row(c).Gap(6).Height(30).PaddingX(12).Radius(999).FontSize(13.5).FontWeight(550).Background(p.Input).Border(1, p.BorderStrong)
	if primary {
		ch.Border(1, p.Accent).Background(p.AccentSoft)
	}
	return ch.Children(func() { ui.Text(c, label).SingleLine() })
}

// Group is a settings .sgroup with its .sgroup-card.
func Group(c *ui.Context, title string, fn func()) {
	p := P(c)
	ui.Column(c).Margin(0, 0, 22, 0).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(13).FontWeight(650).TextColor(p.Text2).Margin(0, 4, 9).LetterSpacing(0.26)
		}
		ui.Column(c).Radius(16).Background(p.Card).Border(1, p.Border).Dividers(1, p.Divider).Children(fn)
	})
}

// SRow is .srow: a title, description and control. stacked puts the control below.
func SRow(c *ui.Context, title, desc string, stacked, disabled bool, control func()) {
	p := P(c)
	row := ui.Row(c).Gap(24).Padding(15, 18)
	if stacked {
		row = ui.Column(c).Gap(12).Padding(15, 18)
	}
	if disabled {
		row.Opacity(0.5)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Children(func() {
			ui.Text(c, title).FontSize(14.5).FontWeight(650)
			if desc != "" {
				ui.Text(c, desc).FontSize(13).TextColor(p.Text2).LineHeight(1.5).Margin(4, 0, 0, 0)
			}
		})
		if control != nil {
			ui.Row(c).Shrink(0).Children(control)
		}
	})
}

func circle(path *ui.Path, cx, cy, r, from, to float32) {
	const steps = 48
	for i := 0; i <= steps; i++ {
		a := (from + (to-from)*float32(i)/steps) * 3.14159265 / 180
		x, y := cx+r*cos(a), cy+r*sin(a)
		if i == 0 {
			path.MoveTo(x, y)
		} else {
			path.LineTo(x, y)
		}
	}
}
