package hub

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

var usageRanges = []uint32{7, 30, 0}

type usageState struct {
	rng, loadedRng int
	data           *model.UsageStats
	err            string
	icons          map[string]*ui.Bitmap
}

// Num is a rounded, grouped number ("12,345").
func Num[T ~uint64 | ~float64 | ~int | ~uint32](n T) string {
	v := int64(math.Round(float64(n)))
	s := fmt.Sprint(v)
	if v < 0 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func msParts(ms float64) part {
	if ms < 1000 {
		return part{fmt.Sprint(int(math.Round(ms))), "毫秒"}
	}
	if ms < 10_000 {
		return part{fmt.Sprintf("%.1f", ms/1000), "秒"}
	}
	return part{fmt.Sprintf("%.0f", ms/1000), "秒"}
}

func msText(ms float64) string { p := msParts(ms); return p.value + " " + p.unit }

func minutesText(secs float64) string {
	m := secs / 60
	if m >= 100 {
		return fmt.Sprint(int(math.Round(m)))
	}
	return fmt.Sprintf("%.1f", m)
}

// TokenText is 12345 -> "12.3K".
func TokenText(n uint64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return Num(n)
}

// NiceMax rounds a y-axis maximum up to 1, 2, 2.5, 5 × 10^k.
func NiceMax(v float64) float64 {
	if v <= 4 {
		return 4
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}

// FillDays gives one bucket per local day from the window start to today.
func FillDays(days []model.UsageBucket, rng int, now time.Time) []model.UsageBucket {
	by := map[string]model.UsageBucket{}
	for _, d := range days {
		by[d.Label] = d
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	span := rng
	if rng == 0 {
		first := today
		if len(days) > 0 {
			if t, err := time.ParseInLocation("2006-01-02", days[0].Label, now.Location()); err == nil {
				first = t.Add(12 * time.Hour)
			}
		}
		span = min(90, max(7, int(math.Round(today.Sub(first).Hours()/24))+1))
	}
	out := make([]model.UsageBucket, 0, span)
	for i := span - 1; i >= 0; i-- {
		key := today.AddDate(0, 0, -i).Format("2006-01-02")
		b, ok := by[key]
		if !ok {
			b = model.UsageBucket{Label: key}
		}
		out = append(out, b)
	}
	return out
}

func splitLabel(l string) (string, string) {
	if i := strings.Index(l, " · "); i >= 0 {
		return l[:i], l[i+len(" · "):]
	}
	return l, ""
}

func (h *Hub) statisticsPage(c *ui.Context) {
	p := kit.P(c)
	u := &h.usage
	changed := h.take("usage")
	if u.data == nil || changed || u.loadedRng != u.rng {
		d, err := h.a.Usage(usageRanges[u.rng])
		u.loadedRng = u.rng
		if err != nil {
			u.err = err.Error()
		} else {
			u.data, u.err = &d, ""
		}
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(44, 36, 56).Children(func() {
			ui.Row(c).Gap(16).Wrap().Children(func() {
				ui.Text(c, "使用统计").FontSize(48).FontWeight(800).LetterSpacing(-0.96).Grow(1)
				kit.Segmented(c.Key("usage-range"), &u.rng, false, "近 7 天", "近 30 天", "全部")
			})
			ui.Text(c, "看看您说了多少，写了多少，以及 AI 为您做了多少。").FontSize(14).TextColor(p.Text2).Margin(8, 0, 22)
			if u.err != "" {
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, "加载失败："+u.err)
					if kit.LinkBtn(c, "重试", true).Clicked() {
						h.Changed("usage")
					}
				})
			}
			if u.data == nil {
				if u.err == "" {
					kit.Spinner(c, 18)
				}
				return
			}
			h.usageBody(c, u.data, int(usageRanges[u.rng]))
		})
	})
}

func usCard(c *ui.Context, title, sub string, fn func()) ui.Element {
	p := kit.P(c)
	return kit.Card(c).Padding(20, 22).MinWidth(0).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.End).Margin(0, 0, 16).Children(func() {
			ui.Text(c, title).FontSize(17).FontWeight(700).LetterSpacing(-0.17).Grow(1)
			if sub != "" {
				ui.Text(c, sub).FontSize(12.5).TextColor(p.Text2)
			}
		})
		fn()
	})
}

func usEmpty(c *ui.Context, text string) {
	p := kit.P(c)
	ui.Column(c).Grow(1).MinHeight(96).Justify(ui.Center).AlignItems(ui.Center).Padding(12).Children(func() {
		ui.Text(c, text).FontSize(13).TextColor(p.Text2).LineHeight(1.7).TextAlign(ui.Center)
	})
}

func usBig(c *ui.Context, value, unit, label string) {
	p := kit.P(c)
	ui.Row(c).Gap(6).AlignItems(ui.End).Children(func() {
		ui.Text(c, value).FontSize(34).FontWeight(650).LetterSpacing(-0.68).FontFeatures("tnum")
		ui.Text(c, unit).FontSize(13.5).FontWeight(650).Margin(0, 0, 6)
		if label != "" {
			ui.Text(c, label).FontSize(13).TextColor(p.Text2).Margin(0, 0, 6, 8)
		}
	})
}

type seg struct {
	label string
	v     float64
	color ui.Color
	val   string
	dim   bool
}

func usSplit(c *ui.Context, segs []seg, minFrac float64) {
	p := kit.P(c)
	var sum float64
	for _, s := range segs {
		sum += s.v
	}
	ui.Row(c).Height(12).Radius(8).Clip().Background(p.Hover).Gap(2).AlignItems(ui.Stretch).Children(func() {
		for i, s := range segs {
			if s.v <= 0 && minFrac == 0 {
				continue
			}
			g := float32(max(s.v, sum*minFrac))
			ui.Box(c.Key(i)).Grow(g).Basis(0).MinWidth(3).Radius(3).Background(s.color)
		}
	})
}

func usLegend(c *ui.Context, segs []seg) {
	p := kit.P(c)
	ui.Column(c).Gap(9).Margin(14, 0, 0).Children(func() {
		for i, s := range segs {
			r := ui.Row(c.Key(i)).Gap(10).FontSize(13)
			if s.dim {
				r.Opacity(0.55)
			}
			r.Children(func() {
				ui.Box(c).Size(10, 10).Radius(3).Background(s.color)
				ui.Text(c, s.label).Grow(1)
				ui.Text(c, s.val).TextColor(p.Text2).FontFeatures("tnum")
			})
		}
	})
}

func (h *Hub) usageBody(c *ui.Context, d *model.UsageStats, rng int) {
	p := kit.P(c)
	total, llm := d.Total, d.LlmTotal
	saved := max(0, float64(total.Words)/40*60-total.AudioSecs)
	w, _ := c.Size()
	two := w >= 900
	cols := []ui.Track{ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)}
	if !two {
		cols = cols[:2]
	}
	rise := func(e ui.Element, i int) {
		e.Opacity(e.Animate("rise", 1, time.Duration(280+40*i)*time.Millisecond))
	}
	ui.Column(c).Gap(16).Children(func() {
		ui.Grid(c).ColumnTracks(cols...).Gap(16).Children(func() {
			kpi := func(i int, parts []part, label, detail string) {
				card := kit.Card(c.Key(i)).Padding(16, 18, 15)
				rise(card, i)
				card.Children(func() {
					bigNumber(c, parts, 31)
					ui.Text(c, label).FontSize(14.5).TextColor(p.Text2).Margin(4, 0, 0)
					ui.Text(c, detail).FontSize(12.5).TextColor(p.Text3).Margin(6, 0, 0)
				})
			}
			kpi(0, []part{{FormatWords(total.Words), "字"}}, "生成文字", Num(d.CompletedSessions)+" 次已完成会话")
			kpi(1, FormatDuration(total.AudioSecs), "已完成转录时长", "语音识别 "+Num(d.AsrTotal.Requests)+" 次请求")
			kpi(2, FormatDuration(saved), "预计节省打字时间", "按每分钟 40 字估算")
			det := "输入 " + TokenText(llm.InputTokens) + " · 输出 " + TokenText(llm.OutputTokens)
			if llm.CachedTokens > 0 {
				det += " · 缓存 " + TokenText(llm.CachedTokens)
			}
			kpi(3, []part{{TokenText(llm.InputTokens + llm.OutputTokens), "tokens"}}, "改写模型用量", det)
		})
		h.trendCard(c, d, rng)
		pair := func(a, b func()) {
			if two {
				ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(16).Children(func() { a(); b() })
			} else {
				a()
				b()
			}
		}
		pair(func() { speedCard(c, d) }, func() { outcomeCard(c, d) })
		pair(func() { modeCard(c, d) }, func() { h.appsCard(c, d) })
		timeCard(c, d)
		asrTable(c, d.AsrModels)
		llmTable(c, d.LlmModels)
		ui.Row(c).Gap(24).Wrap().Justify(ui.SpaceBetween).Padding(4, 4, 0).FontSize(13).TextColor(p.Text2).Children(func() {
			ui.Text(c, fmt.Sprintf("%s 个词典词条（自动学习 %s 个）", Num(d.DictionaryWords), Num(d.AutoWords)))
			ui.Text(c, "所选期间失败的会话 "+Num(d.ErrorSessions))
		})
	})
}

func (h *Hub) trendCard(c *ui.Context, d *model.UsageStats, rng int) {
	p := kit.P(c)
	days := FillDays(d.Days, rng, time.Now())
	has := false
	var top uint64
	for _, x := range d.Days {
		has = has || x.Words > 0
	}
	for _, x := range days {
		top = max(top, x.Words)
	}
	n := len(days)
	sub := ""
	if has {
		sub = fmt.Sprintf("生成字数 · 共 %d 天", n)
	}
	usCard(c, "每日趋势", sub, func() {
		if !has {
			usEmpty(c, "开始一次口述，您每天的使用趋势就会出现在这里。")
			return
		}
		mx := NiceMax(float64(top))
		ui.Row(c).Gap(10).Height(190).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(36).Justify(ui.SpaceBetween).Padding(0, 0, 22).Children(func() {
				for _, f := range []float64{1, 0.5, 0} {
					ui.Text(c.Key(int(f*2)), FormatWords(uint64(mx*f))).FontSize(11).TextColor(p.Text3).TextAlign(ui.End).FontFeatures("tnum")
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				plot := ui.Row(c).Grow(1).AlignItems(ui.End).Gap(2)
				plot.Draw(func(pt *ui.Painter, r ui.Rect) {
					for _, f := range []float32{0, 0.5} {
						pt.Line(r.X, r.Y+r.H*f, r.X+r.W, r.Y+r.H*f, 1, p.Divider)
					}
					pt.Line(r.X, r.Y+r.H, r.X+r.W, r.Y+r.H, 1, p.BorderStrong)
				})
				plot.Children(func() {
					for i, day := range days {
						col := ui.Column(c.Key(day.Label)).Grow(1).Basis(0).FillHeight().Justify(ui.End).AlignItems(ui.Center).Radius(4)
						if col.Hovered() {
							col.Background(p.Hover.Alpha(0.6))
						}
						col.Tooltip(fmt.Sprintf("%s 月 %s 日\n生成字数　%s 字\n会话　%d 次\n音频　%s 分钟",
							day.Label[5:7], day.Label[8:], Num(day.Words), day.Sessions, minutesText(day.AudioSecs)))
						frac := float32(float64(day.Words) / mx)
						up := col.Animate("up", frac, time.Duration(550+min(i*12, 420))*time.Millisecond)
						col.Children(func() {
							bar := ui.Box(c).FillWidth().MaxWidth(22).MinHeight(2).HeightPercent(up*100).Radius(5, 5, 2, 2)
							if day.Words == 0 {
								bar.Background(p.BorderStrong)
							} else {
								bar.Gradient(p.Accent.Alpha(0.7), p.Accent, 0)
							}
						})
					}
				})
				ui.Row(c).Height(22).Padding(6, 0, 0).Children(func() {
					k := min(6, n)
					seen := map[int]bool{}
					for j := 0; j < k; j++ {
						i := int(math.Round(float64(j*(n-1)) / float64(max(1, k-1))))
						if seen[i] {
							continue
						}
						seen[i] = true
						x := (float32(i) + 0.5) / float32(n) * 100
						t := ui.Text(c.Key(i), strings.Replace(days[i].Label[5:], "-", "/", 1)).FontSize(11).TextColor(p.Text3).Absolute()
						switch j {
						case 0:
							t.Left(0)
						case k - 1:
							t.Right(0)
						default:
							t.LeftPercent(x).Margin(0, 0, 0, -14)
						}
					}
				})
			})
		})
	})
}

func speedCard(c *ui.Context, d *model.UsageStats) {
	p := kit.P(c)
	l := d.Latency
	if l.Total.Samples == 0 {
		usCard(c, "速度", "", func() { usEmpty(c, "暂无数据") })
		return
	}
	asr, llm := float64(l.Asr.MeanMs), float64(l.Llm.MeanMs)
	other := max(0, float64(l.Total.MeanMs)-asr-llm)
	sum := max(1, asr+llm+other)
	segs := []seg{{label: "语音识别", v: asr, color: p.UsAsr}, {label: "改写", v: llm, color: p.UsLlm}}
	if other > 0 {
		segs = append(segs, seg{label: "上屏等其他", v: other, color: p.Text3})
	}
	for i := range segs {
		segs[i].val = fmt.Sprintf("%s · %d%%", msText(segs[i].v), int(math.Round(100*segs[i].v/sum)))
	}
	usCard(c, "速度", "基于 "+Num(l.Total.Samples)+" 次会话", func() {
		big := msParts(float64(l.Total.MeanMs))
		usBig(c, big.value, big.unit, "")
		ui.Text(c, fmt.Sprintf("平均松手到上屏耗时 · 中位 %s · P90 %s", msText(float64(l.Total.MedianMs)), msText(float64(l.Total.P90Ms)))).
			FontSize(12.5).TextColor(p.Text2).Margin(4, 0, 14)
		usSplit(c, segs, 0.01)
		usLegend(c, segs)
	})
}

func outcomeCard(c *ui.Context, d *model.UsageStats) {
	p := kit.P(c)
	o := d.Outcomes
	all := o.Completed + o.Dismissed + o.Error + o.Empty
	if all == 0 {
		usCard(c, "结果", "", func() { usEmpty(c, "暂无数据") })
		return
	}
	segs := []seg{
		{label: "完成", v: float64(o.Completed), color: p.Success},
		{label: "取消", v: float64(o.Dismissed), color: p.Text3},
		{label: "失败", v: float64(o.Error), color: p.Danger},
		{label: "无声", v: float64(o.Empty), color: p.Warning},
	}
	for i := range segs {
		segs[i].val, segs[i].dim = Num(segs[i].v)+" 次", segs[i].v == 0
	}
	usCard(c, "结果", "共 "+Num(all)+" 次会话", func() {
		usBig(c, fmt.Sprint(int(math.Round(100*float64(o.Completed)/float64(all)))), "%", "成功率")
		ui.Box(c).Height(14)
		usSplit(c, segs, 0)
		usLegend(c, segs)
	})
}

func modeCard(c *ui.Context, d *model.UsageStats) {
	p := kit.P(c)
	modes := []struct {
		key, label string
		color      ui.Color
	}{{"dictation", "口述", p.UsAsr}, {"translation", "翻译", p.UsLlm}, {"ask", "问任何问题", p.Warning}}
	var segs []seg
	var total float64
	for _, m := range modes {
		var n uint64
		for _, b := range d.Modes {
			if b.Label == m.key {
				n = b.Sessions
			}
		}
		s := seg{label: m.label, v: float64(n), color: m.color, val: "—", dim: n == 0}
		if n > 0 {
			s.val = Num(n) + " 次"
		}
		total += float64(n)
		segs = append(segs, s)
	}
	usCard(c, "使用方式", "", func() {
		if total == 0 {
			usEmpty(c, "暂无数据")
			return
		}
		usSplit(c, segs, 0)
		usLegend(c, segs)
		ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(10).Margin(18, 0, 0).Children(func() {
			for i, f := range []struct {
				v     float64
				label string
			}{{d.AverageWordsPerSession, "字 / 次"}, {d.AverageSessionSecs, "平均秒数"}, {d.LongestSessionSecs, "最长秒数"}} {
				ui.Column(c.Key(i)).Padding(10, 12).Radius(12).Background(p.Card2).Border(1, p.Border).Children(func() {
					ui.Text(c, fmt.Sprint(int(math.Round(f.v)))).FontSize(18).FontWeight(650).FontFeatures("tnum")
					ui.Text(c, f.label).FontSize(12).TextColor(p.Text2).Margin(2, 0, 0)
				})
			}
		})
	})
}

func (h *Hub) appsCard(c *ui.Context, d *model.UsageStats) {
	p := kit.P(c)
	apps := d.Apps[:min(6, len(d.Apps))]
	all := float64(max(1, d.Total.Words))
	var top uint64 = 1
	for _, a := range apps {
		top = max(top, a.Words)
	}
	if h.usage.icons == nil {
		h.usage.icons = map[string]*ui.Bitmap{}
	}
	usCard(c, "常用应用", "", func() {
		if len(apps) == 0 {
			usEmpty(c, "暂无数据")
			return
		}
		ui.Column(c).Gap(14).Children(func() {
			for i, a := range apps {
				ui.Row(c.Key(a.Label)).Gap(12).Children(func() {
					var icon *ui.Bitmap
					if a.BundleID != nil {
						id := *a.BundleID
						var ok bool
						if icon, ok = h.usage.icons[id]; !ok {
							if png := platform.AppIconPNG(id, 64); png != nil {
								icon, _ = ui.DecodeBitmap(png)
							}
							h.usage.icons[id] = icon
						}
					}
					if icon != nil {
						ui.Image(c, icon).Size(28, 28)
					} else {
						ui.Box(c).Size(28, 28).Radius(7).Background(p.Hover).Border(1, p.Border)
					}
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Row(c).Gap(10).Children(func() {
							ui.Text(c, a.Label).FontSize(13.5).FontWeight(600).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
							ui.Text(c, Num(a.Words)+" 字").FontSize(12).TextColor(p.Text2).FontFeatures("tnum")
						})
						track := ui.Box(c).Height(4).Radius(4).Background(p.Hover).Margin(7, 0, 0).Clip()
						frac := track.Animate("w", float32(float64(a.Words)/float64(top)), time.Duration(600+i*30)*time.Millisecond)
						track.Children(func() {
							ui.Box(c).WidthPercent(frac * 100).FillHeight().Radius(4).Background(p.Accent).Opacity(0.85)
						})
					})
					ui.Text(c, fmt.Sprintf("%d%%", int(math.Round(100*float64(a.Words)/all)))).Width(44).TextAlign(ui.End).
						FontSize(13).TextColor(p.Text2).FontFeatures("tnum")
				})
			}
		})
	})
}

var weekNames = []string{"一", "二", "三", "四", "五", "六", "日"}

func timeCard(c *ui.Context, d *model.UsageStats) {
	p := kit.P(c)
	hours, week := d.Hours, d.Weekdays
	if len(hours) != 24 {
		hours = make([]uint64, 24)
	}
	if len(week) != 7 {
		week = make([]uint64, 7)
	}
	peak := func(v []uint64) (int, uint64) {
		bi, bm := -1, uint64(0)
		for i, x := range v {
			if x > bm {
				bi, bm = i, x
			}
		}
		return bi, bm
	}
	ph, hMax := peak(hours)
	pw, wMax := peak(week)
	sub := ""
	if ph >= 0 {
		sub = fmt.Sprintf("高峰 %02d:00–%02d:00 · 周%s最常用", ph, (ph+1)%24, weekNames[pw])
	}
	bar := func(c *ui.Context, v, mx uint64, isPeak bool, delay int, tip string) {
		pct := float32(4)
		if v > 0 {
			pct = max(8, float32(100*float64(v)/float64(mx)))
		}
		col := ui.Column(c).Grow(1).Basis(0).FillHeight().Justify(ui.End).Tooltip(tip)
		up := col.Animate("up", pct, time.Duration(500+delay)*time.Millisecond)
		col.Children(func() {
			b := ui.Box(c).FillWidth().HeightPercent(up).Radius(3)
			switch {
			case v == 0:
				b.Background(p.BorderStrong)
			case isPeak:
				b.Background(p.Accent)
			default:
				b.Background(p.Accent.Alpha(0.45))
			}
		})
	}
	usCard(c, "使用时段", sub, func() {
		if hMax == 0 {
			usEmpty(c, "有了使用记录后，这里会显示您常在几点、周几使用。")
			return
		}
		ui.Grid(c).ColumnTracks(ui.Fr(2), ui.Fr(1)).Gap(28).Children(func() {
			ui.Column(c).Children(func() {
				ui.Row(c).Height(110).Gap(3).AlignItems(ui.Stretch).Children(func() {
					for hh, v := range hours {
						bar(c.Key(hh), v, hMax, hh == ph, hh*14, fmt.Sprintf("%02d:00　%d 次", hh, v))
					}
				})
				ui.Row(c).Height(20).Padding(6, 0, 0).Children(func() {
					for _, hh := range []int{0, 6, 12, 18} {
						t := ui.Text(c.Key(hh), fmt.Sprintf("%02d:00", hh)).FontSize(11).TextColor(p.Text3).Absolute()
						if hh == 0 {
							t.Left(0)
						} else {
							t.LeftPercent((float32(hh)+0.5)/24*100).Margin(0, 0, 0, -15)
						}
					}
					ui.Text(c, "24:00").FontSize(11).TextColor(p.Text3).Absolute().Right(0)
				})
			})
			ui.Row(c).Height(130).Gap(8).AlignItems(ui.Stretch).Children(func() {
				for dd, v := range week {
					ui.Column(c.Key(dd)).Grow(1).Basis(0).Gap(6).AlignItems(ui.Stretch).Children(func() {
						ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
							bar(c, v, wMax, dd == pw, dd*40, fmt.Sprintf("周%s　%d 次", weekNames[dd], v))
						})
						ui.Text(c, weekNames[dd]).FontSize(11.5).TextColor(p.Text2).TextAlign(ui.Center)
					})
				}
			})
		})
	})
}

func svcCell(c *ui.Context, label string, extra string) {
	p := kit.P(c)
	name, mdl := splitLabel(label)
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, name).FontWeight(650)
		if mdl != "" {
			ui.Text(c, mdl).FontSize(12).TextColor(p.Text2)
		}
		if extra != "" {
			ui.Text(c, extra).FontSize(12).TextColor(p.Text3)
		}
	})
}

func usTable(c *ui.Context, head []string, rows [][]string, first func(i int)) {
	p := kit.P(c)
	tracks := []ui.Track{ui.Fr(2)}
	for range head[1:] {
		tracks = append(tracks, ui.Fr(1))
	}
	ui.Column(c).Dividers(1, p.Divider).FontSize(13).Children(func() {
		ui.Grid(c).ColumnTracks(tracks...).Gap(10).Padding(4, 4, 12).Children(func() {
			for i, hd := range head {
				t := ui.Text(c.Key(i), hd).FontSize(12).TextColor(p.Text2)
				if i > 0 {
					t.TextAlign(ui.End)
				}
			}
		})
		for ri, r := range rows {
			ui.Grid(c.Key(ri)).ColumnTracks(tracks...).Gap(10).Padding(12, 4).AlignItems(ui.Center).Children(func() {
				first(ri)
				for i, v := range r {
					ui.Text(c.Key(i), v).TextAlign(ui.End).FontFeatures("tnum")
				}
			})
		}
	})
}

func median(m *uint64) string {
	if m == nil {
		return "—"
	}
	return msText(float64(*m))
}

func asrTable(c *ui.Context, rows []model.UsageBucket) {
	usCard(c, "语音识别", "按音频时长计费", func() {
		if len(rows) == 0 {
			usEmpty(c, "还没有语音识别请求。")
			return
		}
		var cells [][]string
		for _, r := range rows {
			cells = append(cells, []string{Num(r.Requests), minutesText(r.AudioSecs), median(r.MedianMs)})
		}
		usTable(c, []string{"服务 · 模型", "请求次数", "音频时长（分钟）", "中位识别耗时"}, cells, func(i int) { svcCell(c, rows[i].Label, "") })
	})
}

func llmTable(c *ui.Context, rows []model.UsageBucket) {
	p := kit.P(c)
	usCard(c, "改写模型", "按 token 计费", func() {
		if len(rows) == 0 {
			usEmpty(c, "还没有改写请求。启用改写模型后，用量会显示在这里。")
			return
		}
		hasCache := false
		var unreported uint64
		for _, r := range rows {
			hasCache = hasCache || r.CachedTokens > 0
			unreported += r.UnreportedRequests
		}
		head := []string{"服务 · 模型", "请求", "输入 token", "输出 token"}
		if hasCache {
			head = append(head, "缓存命中")
		}
		head = append(head, "中位耗时")
		var cells [][]string
		for _, r := range rows {
			row := []string{Num(r.Requests), TokenText(r.InputTokens), TokenText(r.OutputTokens)}
			if hasCache {
				cached := "—"
				if r.CachedTokens > 0 {
					cached = TokenText(r.CachedTokens)
				}
				row = append(row, cached)
			}
			cells = append(cells, append(row, median(r.MedianMs)))
		}
		usTable(c, head, cells, func(i int) {
			extra := ""
			if rows[i].UnreportedRequests > 0 {
				extra = fmt.Sprintf("%d 次未返回 token 数", rows[i].UnreportedRequests)
			}
			svcCell(c, rows[i].Label, extra)
		})
		if unreported > 0 {
			ui.Text(c, fmt.Sprintf("有 %d 次改写请求，服务商没有返回 token 用量，未计入上方数字。", unreported)).FontSize(12.5).TextColor(p.Text3).Margin(10, 0, 0)
		}
	})
}
