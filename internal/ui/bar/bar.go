// Package bar is the floating recording bar in MyGo native UI: the pill at
// the bottom of the screen with its waveform, and the stack of alerts, the
// ask-anything answer and the translation chip above it.
package bar

import (
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/core"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/egoist/mygo/ui"
)

const maxAlerts = 3

// Size of the bar window (as the Tauri version's).
const Width, Height = 560, 460

// Env is what the bar needs from the desktop.
type Env struct {
	// SetHitRects gives the window the regions that take the mouse; the rest passes through.
	SetHitRects func([]platform.HitRect)
	CopyText    func(string) error
}

// Bar is the bar window's state.
type Bar struct {
	a   *core.App
	env Env

	hover     bool
	recStart  time.Time
	lastPhase model.BarPhase

	level     float32
	levelAt   time.Time
	wave      wave
	closed    map[string]bool // alerts closed here before the core drops them
	copied    map[string]bool
	ansCopied time.Time
	expanded  string // id of the expanded answer

	rects []platform.HitRect
}

// New makes the bar.
func New(a *core.App, env Env) *Bar {
	return &Bar{a: a, env: env, wave: newWave(), closed: map[string]bool{}, copied: map[string]bool{}}
}

func formatElapsed(secs float64) string {
	s := int(max(0, secs))
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

var (
	cardBg    = ui.Hex("#1c1c1e")
	cardLine  = ui.RGBA(255, 255, 255, 0.08)
	titleCol  = ui.Hex("#f5f5f7")
	descCol   = ui.Hex("#a1a1a6")
	pillBg    = ui.RGBA(13, 13, 13, 1)
	pillIdle  = ui.RGBA(20, 20, 20, 0.55)
	toneColor = map[string]ui.Color{"warning": ui.Hex("#ffb340"), "error": ui.Hex("#ff6b61"), "success": ui.Hex("#4cd37b")}
)

// View builds the bar.
func (b *Bar) View(c *ui.Context) {
	snap := b.a.Bar()
	st := snap.State
	if snap.Level != b.level {
		b.level, b.levelAt = snap.Level, time.Now()
	}
	if st.Phase == model.BarRecording {
		start := time.Now().Add(-time.Duration(st.ElapsedSecs * float64(time.Second)))
		if b.recStart.IsZero() || (st.ElapsedSecs > 0 && absDur(b.recStart.Sub(start)) > 1500*time.Millisecond) {
			b.recStart = start
		}
	} else {
		b.recStart = time.Time{}
	}
	t := *c.Theme()
	t.Dark, t.Text = true, ui.Hex("#f2f2f2")
	c.SetTheme(&t)
	c.Root().Background(ui.RGBA(0, 0, 0, 0)).TextColor(ui.Hex("#f2f2f2")).FontSize(13)

	variant := "idle"
	switch {
	case st.Phase == model.BarProcessing:
		variant = "processing"
	case st.Phase == model.BarRecording && st.Locked:
		variant = "locked"
	case st.Phase == model.BarRecording:
		variant = "ptt"
	}
	visible := st.Phase == model.BarRecording || st.Phase == model.BarProcessing || (st.Phase == model.BarIdle && st.ShowIdleOnDesktop)
	idle := variant == "idle"
	if !idle {
		b.hover = false
	}
	showTime := variant == "locked" && !b.recStart.IsZero() && time.Since(b.recStart) >= time.Minute
	if variant == "locked" && !showTime && !b.recStart.IsZero() {
		c.After(time.Until(b.recStart.Add(time.Minute)))
	}
	askTint := st.Mode == model.ModeAsk && variant != "idle"
	chipVisible := st.Phase == model.BarRecording && (st.Mode == model.ModeTranslation || st.Mode == model.ModeAsk)

	var alerts []core.Alert
	for _, a := range snap.Alerts {
		if !b.closed[a.ID] {
			alerts = append(alerts, a)
		}
	}
	keep := maxAlerts
	if snap.Answer != nil {
		keep = 2
	}
	if len(alerts) > keep {
		alerts = alerts[len(alerts)-keep:]
	}

	var hits []ui.Element
	var pillHit ui.Element
	ui.Column(c).Fill().Justify(ui.End).AlignItems(ui.Center).Padding(6, 0, 10).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinHeight(0).FillWidth().Justify(ui.End).AlignItems(ui.Center).Gap(8).Padding(0, 24, 10).Children(func() {
			if a := snap.Answer; a != nil {
				hits = append(hits, b.answerCard(c.Key("answer-"+a.ID), *a))
			}
			for _, a := range alerts {
				hits = append(hits, b.alertCard(c.Key(a.ID), a))
			}
			if chipVisible {
				w := float32(176)
				if showTime {
					w = 204
				}
				ui.Row(c.Key("chip")).Width(w).Children(func() {
					if st.Mode == model.ModeTranslation {
						label := "翻译"
						if st.TranslationTargetLabel != nil {
							label = *st.TranslationTargetLabel
						}
						ch := chip(c, icons.Globe, label, ui.Hex("#8eb4ff"), true).Tooltip("切换翻译语言")
						hits = append(hits, ch)
						if ch.Clicked() {
							b.a.BarAction("cycle_translation_target")
						}
					} else {
						chip(c, icons.Sparkles, "问任何问题", ui.Hex("#b7a8ff"), false)
					}
				})
			}
		})
		ui.Row(c).Height(40).FillWidth().Justify(ui.Center).AlignItems(ui.End).Children(func() {
			if !visible {
				return
			}
			pillHit = b.pill(c.Key("pill"), st, variant, askTint, showTime)
		})
	})
	b.hitRects(c, hits, pillHit, idle)
	if visible && (variant != "idle" || b.hover) {
		c.AnimationFrame()
	}
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (b *Bar) hitRects(c *ui.Context, hits []ui.Element, pill ui.Element, idle bool) {
	if b.env.SetHitRects == nil {
		return
	}
	var rects []platform.HitRect
	add := func(r ui.Rect, px, py float32) {
		if r.W <= 0 || r.H <= 0 {
			c.AnimationFrame() // laid out next frame
			return
		}
		{
			rects = append(rects, platform.HitRect{X: float64(r.X - px), Y: float64(r.Y - py), Width: float64(r.W + 2*px), Height: float64(r.H + 2*py)})
		}
	}
	for _, e := range hits {
		add(e.Bounds(), 0, 0)
	}
	if pill != (ui.Element{}) {
		if idle {
			add(pill.Bounds(), 10, 8)
		} else {
			add(pill.Bounds(), 0, 0)
		}
	}
	b.rects = rects
	b.env.SetHitRects(rects)
}

// HitRects are the regions last given to Env.SetHitRects.
func (b *Bar) HitRects() []platform.HitRect { return b.rects }

func chip(c *ui.Context, icon *ui.SVG, label string, iconCol ui.Color, button bool) ui.Element {
	var e ui.Element
	if button {
		e = ui.ButtonBase(c).FocusRing(false)
		if e.Hovered() {
			e.Background(ui.Hex("#2a2a2d"))
		} else {
			e.Background(cardBg)
		}
	} else {
		e = ui.Row(c).Background(cardBg)
	}
	e.Gap(5).Height(24).Padding(0, 10, 0, 8).Radius(999).Border(1, ui.RGBA(255, 255, 255, 0.1)).
		Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.3)).FontSize(12).FontWeight(600).TextColor(ui.Hex("#e8e8ea"))
	return e.Children(func() {
		ui.Icon(c, icon).FontSize(12).TextColor(iconCol)
		ui.Text(c, label).SingleLine()
	})
}

var keycapRe = regexp.MustCompile(`(\[\[[^\]]+\]\]|\*[^*\s][^*]*\*)`)

// withKeycaps renders "[[fn]]" and "*Esc*" tokens as keycaps within the text.
func withKeycaps(c *ui.Context, text string, size float32, col ui.Color, weight int) {
	ui.RichText(c).FontSize(size).TextColor(col).FontWeight(weight).LineHeight(18 / 13.0).Children(func() {
		last, n := 0, 0
		for _, m := range keycapRe.FindAllStringIndex(text, -1) {
			if m[0] > last {
				ui.Text(c.Key(n), text[last:m[0]])
				n++
			}
			tok := strings.Trim(text[m[0]:m[1]], "[]*")
			ui.Text(c.Key(n), "\u2009"+tok+"\u2009").FontSize(size-2).FontWeight(600).TextColor(ui.Hex("#f2f2f2")).
				Background(ui.RGBA(255, 255, 255, 0.14))
			n++
			last = m[1]
		}
		if last < len(text) {
			ui.Text(c.Key(n), text[last:])
		}
	})
}

func toneIcon(tone string) *ui.SVG {
	switch tone {
	case "warning":
		return icons.AlertTriangle
	case "error":
		return icons.AlertCircle
	case "success":
		return icons.CheckCircle
	}
	return icons.Info
}

func (b *Bar) alertCard(c *ui.Context, a core.Alert) ui.Element {
	card := ui.Row(c).Gap(10).AlignItems(ui.Start).MinWidth(250).MaxWidth(340).Padding(12, 34, 12, 13).Radius(14).
		Background(cardBg).Border(1, cardLine).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.38))
	in := card.Animate("in", 1, 180*time.Millisecond)
	card.Opacity(in)
	hovered := card.Hovered()
	card.Children(func() {
		col, ok := toneColor[a.Tone]
		if !ok {
			col = ui.Hex("#7fb0ff")
		}
		ui.Icon(c, toneIcon(a.Tone)).FontSize(16).TextColor(col).Margin(1, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			withKeycaps(c, a.Title, 13, titleCol, 600)
			if a.Description != nil && *a.Description != "" {
				ui.Column(c).Margin(2, 0, 0).Children(func() { withKeycaps(c, *a.Description, 12, descCol, 400) })
			}
			if len(a.Actions) > 0 {
				ui.Row(c).Gap(6).Wrap().Margin(10, 0, 0).Children(func() {
					copied := false
					for _, act := range a.Actions {
						copied = copied || b.copied[a.ID+"/"+act.ID]
					}
					for _, act := range a.Actions {
						done := b.copied[a.ID+"/"+act.ID]
						btn := ui.ButtonBase(c.Key(act.ID)).Height(26).PaddingX(12).Radius(8).FontSize(12).FontWeight(600).FocusRing(false).Disabled(copied)
						h := btn.Hovered()
						switch {
						case done:
							btn.Background(ui.RGBA(76, 211, 123, 0.2)).TextColor(ui.Hex("#6ee79b"))
						case act.Style == "primary":
							btn.Background(map[bool]ui.Color{true: ui.Hex("#e8e8ea"), false: white}[h]).TextColor(ui.Hex("#111111"))
						case act.Style == "danger":
							btn.Background(ui.RGBA(255, 69, 58, map[bool]float32{true: 0.3, false: 0.2}[h])).TextColor(ui.Hex("#ff8178"))
						default:
							btn.Background(ui.RGBA(255, 255, 255, map[bool]float32{true: 0.18, false: 0.12}[h])).TextColor(ui.Hex("#f2f2f2"))
						}
						label := act.Label
						if done {
							label = "已复制"
						}
						btn.Children(func() { ui.Text(c, label) })
						if btn.Clicked() {
							b.a.AlertAction(a.ID, act.ID)
							if act.ID == "copy" {
								b.copied[a.ID+"/"+act.ID] = true
								id := a.ID
								time.AfterFunc(700*time.Millisecond, func() { b.close(id) })
							} else {
								b.close(a.ID)
							}
						}
					}
				})
			}
		})
		x := ui.ButtonBase(c).Absolute().Top(8).Right(8).Size(20, 20).Radius(10).Justify(ui.Center).TextColor(ui.Hex("#9a9a9f")).
			Label("关闭").FocusRing(false)
		if x.Hovered() {
			x.Background(ui.RGBA(255, 255, 255, 0.16)).TextColor(white)
		} else {
			x.Background(ui.RGBA(255, 255, 255, 0.08))
		}
		if !hovered && !x.Hovered() {
			x.Opacity(0)
		}
		x.Children(func() { ui.Icon(c, icons.X).FontSize(11) })
		if x.Clicked() {
			b.close(a.ID)
		}
	})
	return card
}

func (b *Bar) close(id string) {
	b.closed[id] = true
	b.a.DismissAlert(id)
}

const (
	answerLine      = 13 * 1.55
	collapsedLines  = 8
	answerCollapsed = float32(answerLine * collapsedLines)
	answerExpanded  = 190
)

func (b *Bar) answerCard(c *ui.Context, a model.AskAnswer) ui.Element {
	card := ui.Column(c).Width(420).MaxWidth(512).Padding(12, 16).Radius(16).Background(cardBg).Border(1, cardLine).
		Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, 0.45))
	card.Opacity(card.Animate("in", 1, 180*time.Millisecond))
	expanded := b.expanded == a.ID
	card.Children(func() {
		ui.Row(c).Gap(4).Height(24).Children(func() {
			ui.Text(c, "回答").FontSize(13).FontWeight(600).TextColor(titleCol).Grow(1)
			copied := time.Since(b.ansCopied) < 1500*time.Millisecond
			cb := ui.ButtonBase(c).Height(24).MinWidth(24).PaddingX(5).Radius(7).Gap(4).Justify(ui.Center).FocusRing(false).Label("复制")
			col := ui.Hex("#9a9a9f")
			if cb.Hovered() {
				cb.Background(ui.RGBA(255, 255, 255, 0.1))
				col = white
			}
			if copied {
				col = ui.Hex("#6ee79b")
				cb.Tooltip("已复制")
			} else {
				cb.Tooltip("复制")
			}
			cb.TextColor(col).Children(func() {
				if copied {
					ui.Icon(c, icons.Check).FontSize(14)
					ui.Text(c, "已复制").FontSize(11.5).FontWeight(600)
				} else {
					ui.Icon(c, icons.Copy).FontSize(14)
				}
			})
			if cb.Clicked() {
				if b.env.CopyText != nil {
					_ = b.env.CopyText(a.Answer)
				} else {
					_ = b.a.CopyText(a.Answer)
				}
				b.ansCopied = time.Now()
				c.After(1500 * time.Millisecond)
			}
			xb := ui.ButtonBase(c).Size(24, 24).Radius(7).Justify(ui.Center).FocusRing(false).Label("关闭").Tooltip("关闭").TextColor(ui.Hex("#9a9a9f"))
			if xb.Hovered() {
				xb.Background(ui.RGBA(255, 255, 255, 0.1)).TextColor(white)
			}
			xb.Children(func() { ui.Icon(c, icons.X).FontSize(15) })
			if xb.Clicked() {
				b.a.CloseAnswer()
			}
		})
		if a.Question != "" {
			ui.Text(c, a.Question).FontSize(12).LineHeight(17.0/12).TextColor(mdMuted).MaxLines(2).Margin(2, 0, 0)
		}
		body := ui.Column(c).Margin(8, 0, 0).Clip().MinHeight(72)
		inner := ui.Local(body, "h", func() float32 { return 0 })
		overflowing := *inner > answerCollapsed+4
		target := *inner
		if overflowing {
			target = answerCollapsed
			if expanded {
				target = min(*inner, answerExpanded)
			}
		}
		if target > 0 {
			body.Height(body.Animate("h", target, 220*time.Millisecond))
		}
		body.Children(func() {
			wrap := func() {
				content := ui.Column(c).FillWidth().Selectable()
				content.Children(func() { Markdown(c, a.Answer) })
				if h := content.Bounds().H; h > 0 && h != *inner {
					*inner = h
				}
			}
			if overflowing && expanded {
				ui.Scroll(c).Grow(1).Children(wrap)
			} else {
				wrap()
			}
			if overflowing && !expanded {
				ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(22).Gradient(cardBg.Alpha(0), cardBg, 180).PassThrough()
			}
		})
		if overflowing {
			label := "...更多"
			if expanded {
				label = "显示更少"
			}
			mb := ui.ButtonBase(c).Height(20).Margin(6, 0, 0).FontSize(12).FontWeight(600).FocusRing(false).Justify(ui.Start)
			if mb.Hovered() {
				mb.TextColor(ui.Hex("#b4cdff"))
			} else {
				mb.TextColor(mdLink)
			}
			mb.Children(func() { ui.Text(c, label) })
			if mb.Clicked() {
				if expanded {
					b.expanded = ""
				} else {
					b.expanded = a.ID
				}
			}
		}
	})
	return card
}

func (b *Bar) pill(c *ui.Context, st model.BarState, variant string, askTint, showTime bool) ui.Element {
	idle := variant == "idle"
	hovered := idle && b.hover
	w, h := float32(36), float32(8)
	switch variant {
	case "idle":
		if hovered {
			w, h = 120, 28
		}
	case "ptt", "processing":
		w, h = 104, 34
	default:
		w, h = 176, 40
		if showTime {
			w = 204
		}
	}
	var p ui.Element
	if idle {
		p = ui.ButtonBase(c).FocusRing(false).Label("开始口述")
	} else {
		p = ui.Box(c)
	}
	aw := p.Animate("w", w, 260*time.Millisecond)
	ah := p.Animate("h", h, 260*time.Millisecond)
	p.Size(aw, ah).Radius(999).Shadow(0, 4, 14, 0, ui.RGBA(0, 0, 0, 0.35))
	scale := p.Animate("in", 1, 160*time.Millisecond)
	p.Opacity(scale)
	if idle {
		p.Background(pillIdle).Border(1, ui.RGBA(255, 255, 255, 0.18))
	} else {
		p.Background(pillBg).Border(1, ui.RGBA(255, 255, 255, 0.14))
	}
	if askTint {
		p.Shadow(0, 0, 16, 0, ui.RGBA(130, 110, 255, 0.34))
	}
	if idle {
		b.hover = p.Hovered()
		if p.Clicked() {
			b.hover = false
			b.a.BarAction("start")
		}
	}
	shimmer := float32(0)
	if variant == "processing" {
		shimmer = p.Loop("shimmer", 1200*time.Millisecond, ui.EaseInOut)
	}
	glow := float32(0)
	if askTint {
		glow = 0.7 + 0.2*float32(math.Cos(float64(p.Loop("glow", 2400*time.Millisecond, ui.Linear))*2*math.Pi))
	}
	mode := "live"
	switch {
	case variant == "processing":
		mode = "processing"
	case idle:
		mode = "idle"
	}
	color := white
	if askTint {
		color = ui.RGB(232, 226, 255)
	}
	showWave := !(idle && !hovered)
	level, levelAt := b.level, b.levelAt
	p.Draw(func(pt *ui.Painter, r ui.Rect) {
		if variant == "processing" {
			sw := r.W * 0.55
			x := r.X - sw + shimmer*(r.W*0.55*2.9)
			pt.Clip(r, r.H/2, func() {
				pt.FillGradient(ui.Rect{X: x, Y: r.Y, W: sw / 2, H: r.H}, ui.LinearGradient{From: white.Alpha(0), To: white.Alpha(0.26), Angle: 100}, 0)
				pt.FillGradient(ui.Rect{X: x + sw/2, Y: r.Y, W: sw / 2, H: r.H}, ui.LinearGradient{From: white.Alpha(0.26), To: white.Alpha(0), Angle: 100}, 0)
			})
		}
		if glow > 0 {
			pt.StrokePathGradient(roundRect(r, r.H/2), 1.2, ui.LinearGradient{From: ui.Hex("#9b7bff").Alpha(glow), To: ui.Hex("#5aa0ff").Alpha(glow), Angle: 120})
		}
		if showWave {
			b.wave.draw(pt, r.X+r.W/2-waveW/2, r.Y+r.H/2-waveH/2, mode, level, levelAt, color, pt.Now())
			pt.AnimationFrame()
		}
	})
	p.Children(func() {
		if showTime && variant == "locked" {
			ui.Text(c, formatElapsed(time.Since(b.recStart).Seconds())).Absolute().Left(aw/2+waveW/2+8).Top(ah/2-7).
				FontSize(10.5).FontWeight(600).FontFeatures("tnum").TextColor(white.Alpha(0.55))
		}
		if variant == "locked" {
			cancel := ui.ButtonBase(c.Key("cancel")).Absolute().Left(7).Top(ah/2-12).Size(24, 24).Radius(12).Justify(ui.Center).
				Label("取消").FocusRing(false)
			if cancel.Hovered() {
				cancel.Background(ui.Hex("#3a3a3c")).TextColor(white)
			} else {
				cancel.Background(ui.Hex("#2b2b2b")).TextColor(ui.Hex("#a8a8ad"))
			}
			cancel.Opacity(cancel.Animate("in", 1, 150*time.Millisecond))
			cancel.Children(func() { ui.Icon(c, icons.X).FontSize(13) })
			if cancel.Clicked() {
				b.a.BarAction("cancel")
			}
			finish := ui.ButtonBase(c.Key("finish")).Absolute().Right(7).Top(ah/2-12).Size(24, 24).Radius(12).Justify(ui.Center).
				Label("完成").FocusRing(false)
			if finish.Hovered() {
				finish.Background(ui.Hex("#e4e4e6"))
			} else {
				finish.Background(white)
			}
			finish.Opacity(finish.Animate("in", 1, 150*time.Millisecond))
			finish.Children(func() { ui.Box(c).Size(9, 9).Radius(2.5).Background(pillBg) })
			if finish.Clicked() {
				b.a.BarAction("finish")
			}
		}
	})
	if hovered {
		tip := ui.Row(c.Key("tip")).Absolute().Bottom(ah+10).Gap(2).Padding(6, 10).Radius(9).Background(cardBg).
			Border(1, ui.RGBA(255, 255, 255, 0.1)).Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.4)).FontSize(12).FontWeight(500).
			TextColor(ui.Hex("#e8e8ea")).PassThrough()
		tip.Opacity(tip.Animate("in", 1, 140*time.Millisecond))
		tip.Children(func() {
			ui.Text(c, "点击或按住")
			ui.Row(c).Height(18).MinWidth(18).PaddingX(6).Margin(0, 2).Radius(5).Justify(ui.Center).Background(ui.RGBA(255, 255, 255, 0.12)).Children(func() {
				ui.Text(c, st.DictationKeyLabel).FontSize(11).FontWeight(600)
			})
			ui.Text(c, "开始口述")
		})
	}
	return p
}

func roundRect(r ui.Rect, rad float32) *ui.Path {
	var p ui.Path
	const steps = 12
	arc := func(cx, cy, from float32) {
		for i := 0; i <= steps; i++ {
			a := float64(from+90*float32(i)/steps) * math.Pi / 180
			x, y := cx+rad*float32(math.Cos(a)), cy+rad*float32(math.Sin(a))
			if i == 0 && from == 180 {
				p.MoveTo(x, y)
			} else {
				p.LineTo(x, y)
			}
		}
	}
	arc(r.X+rad, r.Y+rad, 180)
	arc(r.X+r.W-rad, r.Y+rad, 270)
	arc(r.X+r.W-rad, r.Y+r.H-rad, 0)
	arc(r.X+rad, r.Y+r.H-rad, 90)
	p.Close()
	return &p
}

// ── waveform (Waveform.tsx) ──

const (
	waveN    = 11
	barW     = 3
	barGap   = 3
	waveW    = waveN*barW + (waveN-1)*barGap
	waveH    = 24
	minH     = 3
	maxH     = 20
	waveLerp = 0.3
)

type wave struct {
	heights        [waveN]float64
	f1, f2, p1, p2 [waveN]float64
	bell           [waveN]float64
	env            float64
	prev           time.Time
}

func newWave() wave {
	var w wave
	for i := range waveN {
		w.heights[i] = minH
		w.f1[i] = 4 + rand.Float64()*4
		w.f2[i] = 9 + rand.Float64()*6
		w.p1[i] = rand.Float64() * 6.283
		w.p2[i] = rand.Float64() * 6.283
		d := (float64(i) - (waveN-1)/2.0) / 3.4
		w.bell[i] = math.Exp(-d * d)
	}
	return w
}

// step advances the bars to now and returns their heights and opacities.
func (w *wave) step(mode string, level float32, levelAt, now time.Time) (h, alpha [waveN]float64) {
	dt := 16.667
	if !w.prev.IsZero() {
		dt = min(64, float64(now.Sub(w.prev).Microseconds())/1000)
	}
	w.prev = now
	k60 := dt / 16.667
	t := float64(now.UnixNano()) / 1e9
	input := 0.0
	if mode == "live" && now.Sub(levelAt) < 300*time.Millisecond {
		input = float64(level)
		if input < 0.01 {
			input = 0
		} else {
			input = math.Pow(math.Min(1, input), 0.6)
		}
	}
	a := 0.16
	if input > w.env {
		a = 0.6
	}
	w.env += (input - w.env) * (1 - math.Pow(1-a, k60))
	kLerp := 1 - math.Pow(1-waveLerp, k60)
	quiet := 1 - math.Min(1, w.env*8)
	for i := range waveN {
		wt := 0.3 + 0.7*w.bell[i]
		target := float64(minH)
		alpha[i] = 1
		switch mode {
		case "live":
			noise := 0.5 + 0.25*math.Sin(t*w.f1[i]+w.p1[i]) + 0.25*math.Sin(t*w.f2[i]+w.p2[i])
			speech := math.Min(1, w.env*wt*(0.58+0.42*noise)*1.3)
			breath := quiet * (0.5 + 0.5*math.Sin(t*2.4-float64(i)*0.55)) * (0.35 + 0.65*w.bell[i]) * 2.4
			target = minH + (maxH-minH)*speech + breath
		case "processing":
			alpha[i] = 0.4 + 0.6*(0.5+0.5*math.Sin(t*5.5-float64(i)*0.65))
		default:
			alpha[i] = 0.6
		}
		w.heights[i] += (target - w.heights[i]) * kLerp
		h[i] = w.heights[i]
	}
	return h, alpha
}

func (w *wave) draw(pt *ui.Painter, x, y float32, mode string, level float32, levelAt time.Time, col ui.Color, now time.Time) {
	hs, alpha := w.step(mode, level, levelAt, now)
	for i := range waveN {
		bh := float32(hs[i])
		pt.Fill(ui.Rect{X: x + float32(i*(barW+barGap)), Y: y + (waveH-bh)/2, W: barW, H: bh}, col.Alpha(float32(alpha[i])), barW/2)
	}
}
