package hub

import (
	"fmt"
	"math"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

func (h *Hub) homePage(c *ui.Context) {
	p := kit.P(c)
	if h.typeless == nil || h.take("typeless") {
		d := h.a.DetectTypeless()
		h.typeless = &d
		go func() { // usePoll(detect, 5000)
			time.Sleep(5 * time.Second)
			h.Changed("typeless")
		}()
	}
	w, _ := c.Size()
	narrow := w < 1000
	scroll := ui.Scroll(c).Grow(1)
	scroll.Children(func() {
		ui.Column(c).Padding(0, 24, 40, 24).Draw(func(pt *ui.Painter, r ui.Rect) {
			// .hero-glow: radial-gradient(70% 90% at 28% 0%, vignette, transparent 78%)
			pt.FillGradient(ui.Rect{X: r.X - 24, Y: r.Y, W: r.W + 48, H: 280}, ui.LinearGradient{From: p.Vignette, To: p.Vignette.Alpha(0), Angle: 180, End: 0.78}, 0)
		}).Children(func() {
			var body ui.Element
			if narrow {
				body = ui.Column(c).MaxWidth(1040)
			} else {
				body = ui.Row(c).Gap(28).AlignItems(ui.Start).MaxWidth(1040)
			}
			body.Children(func() {
				if narrow {
					ui.Text(c, "说话，不要打字").FontSize(48).FontWeight(800).LetterSpacing(-0.96).Padding(30, 0, 0).Margin(0, 0, 22)
					h.shortcutCard(c, true)
				}
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					if !narrow {
						ui.Text(c, "说话，不要打字").FontSize(48).FontWeight(800).LetterSpacing(-0.96).Padding(30, 0, 0).Margin(0, 0, 22)
					}
					if h.typeless != nil && h.typeless.Running {
						kit.Banner(c, "Typeless 正在运行，会与 Hoshiro 抢占 Fn 键", func() {
							label := "退出 Typeless"
							if h.quitting {
								label = "正在退出…"
							}
							if kit.Btn(c, label, kit.Secondary, kit.Sm, nil).Disabled(h.quitting).Clicked() {
								h.quitting = true
								h.async(c, func() {
									_ = h.a.QuitTypeless()
									time.Sleep(600 * time.Millisecond)
								}, func() { h.quitting = false; h.Changed("typeless") })
							}
						}).Margin(0, 0, 18)
					}
					h.insights(c, narrow)
				})
				if !narrow {
					ui.Column(c).Width(196).Padding(94, 0, 0).MinHeight(max(0, func() float32 { _, hh := c.Size(); return hh - 16 }())).Children(func() {
						h.shortcutCard(c, false)
						ui.Spacer(c)
						h.versionBox(c, false)
					})
				} else {
					h.versionBox(c, true)
				}
			})
		})
	})
}

func (h *Hub) insights(c *ui.Context, narrow bool) {
	p := kit.P(c)
	st := h.stats
	ui.Column(c).Padding(4, 0, 0).Children(func() {
		ui.Box(c).Absolute().Top(-14).Left(-14).Right(-14).Bottom(-10).Draw(func(pt *ui.Painter, r ui.Rect) {
			for y := r.Y; y < r.Y+r.H; y += 8 {
				fade := float32(1)
				if d := y - r.Y; d < 56 {
					fade = d / 56
				} else if d := r.Y + r.H - y; d < 56 {
					fade = d / 56
				}
				col := p.DotGrid.Alpha(float32(p.DotGrid.A) / 255 * fade)
				for x := r.X; x < r.X+r.W; x += 8 {
					pt.Fill(ui.Rect{X: x, Y: y, W: 1.6, H: 1.6}, col, 0.8)
				}
			}
		})
		ui.Row(c).Height(44).Margin(0, 0, 12).Children(func() {
			ui.Text(c, "洞察").FontSize(25).FontWeight(700).TextColor(p.Text2)
		})
		size := float32(31)
		if narrow {
			size = 28
		}
		ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(16).Margin(0, 0, 16).Children(func() {
			stat := func(i int, parts []part, label string) {
				card := kit.Card(c.Key(i)).Padding(16, 18, 15).MinHeight(84)
				card.Opacity(card.Animate("in", 1, time.Duration(280+40*i)*time.Millisecond))
				card.Children(func() {
					bigNumber(c, parts, size)
					ui.Text(c, label).FontSize(14.5).TextColor(p.Text2).Margin(4, 0, 0)
				})
			}
			stat(1, []part{{FormatWords(st.TotalWords), "字"}}, "口述字数")
			stat(2, FormatDuration(st.TimeSavedSecs), "节省时间")
			stat(3, []part{{fmt.Sprint(int(math.Round(st.AverageWPM))), "每分钟字数"}}, "平均口述速度")
			stat(4, FormatDuration(st.TotalDictationSecs), "总口述时间")
		})
		kit.Card(c).Padding(16, 18, 14).Margin(0, 0, 20).Children(func() {
			ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(12).Margin(0, 0, 22).Children(func() {
				for i, x := range []struct {
					v     uint32
					label string
				}{{st.ActiveDays, "活跃天数"}, {st.CurrentStreak, "当前连续天数"}, {st.LongestStreak, "最长连续天数"}} {
					ui.Column(c.Key(i)).Children(func() {
						bigNumber(c, []part{{fmt.Sprint(x.v), "天"}}, size)
						ui.Text(c, x.label).FontSize(14.5).TextColor(p.Text2).Margin(4, 0, 0)
					})
				}
			})
			h.heatmap(c)
		})
	})
}

func (h *Hub) shortcutCard(c *ui.Context, row bool) {
	p := kit.P(c)
	sc := h.settings.Shortcuts
	var card ui.Element
	if !row {
		card = kit.Card(c).Padding(16, 16, 14).Gap(16)
	} else {
		card = ui.Row(c).Background(p.Card).Radius(kit.RadiusCard).Border(1, p.Border).Padding(16).Wrap().GapX(32).GapY(18).Margin(0, 0, 18)
	}
	card.Children(func() {
		for _, r := range []struct {
			label string
			list  []string
		}{{"口述", sc.Dictation}, {"翻译", sc.Translation}, {"问任何问题", sc.AskAnything}} {
			ui.Column(c.Key(r.label)).Children(func() {
				ui.Text(c, r.label).FontSize(13.5).TextColor(p.Text2).Margin(0, 0, 8)
				var keys ui.Element
				if row {
					keys = ui.Row(c).Gap(6).Wrap()
				} else {
					keys = ui.Column(c).Gap(6).AlignItems(ui.Start)
				}
				keys.Children(func() {
					if len(r.list) == 0 {
						ui.Text(c, "未设置").TextColor(p.Text2)
					}
					for i, s := range r.list {
						var alt ui.Element
						if row {
							alt = ui.Row(c.Key(s)).Gap(5)
						} else {
							alt = ui.Column(c.Key(s)).Gap(5).AlignItems(ui.Start)
						}
						alt.Children(func() {
							if i > 0 {
								ui.Text(c, "或").FontSize(11.5).TextColor(p.Text3)
							}
							kit.ShortcutKeys(c, s, false)
						})
					}
				})
			})
		}
	})
}

func (h *Hub) versionBox(c *ui.Context, row bool) {
	p := kit.P(c)
	var box ui.Element
	if row {
		box = ui.Row(c).Justify(ui.End).Gap(10).Padding(6, 0, 0)
	} else {
		box = ui.Column(c).AlignItems(ui.End).Gap(4).Padding(0, 0, 8)
	}
	box.FontSize(13.5).TextColor(p.Text2).Children(func() {
		v := h.a.Version
		if v == "" {
			v = "0.1.0"
		}
		ui.Text(c, "版本 v"+v)
		phase, info, progress := h.updatePhase()
		if phase == "available" || phase == "installing" {
			label := updateLabel(phase, info, progress)
			if phase == "available" {
				label = "新版本 " + info.Version
			}
			if kit.LinkBtn(c, label, true).Clicked() {
				h.openSettings("about")
			}
		} else {
			label := "检查更新"
			switch phase {
			case "checking":
				label = "检查中…"
			case "latest":
				label = "已是最新版本"
			}
			if kit.LinkBtn(c, label, false).Disabled(phase == "checking").Clicked() {
				h.checkUpdate(c)
			}
		}
	})
}

// ── heatmap ──

const heatCols, heatPage = 24, 4

var months = []string{"一月", "二月", "三月", "四月", "五月", "六月", "七月", "八月", "九月", "十月", "十一月", "十二月"}
var usageNames = []string{"", "轻度使用", "中度使用", "高使用频率", "强力使用"}

type heatCell struct {
	date  time.Time
	key   string
	words uint64
	kind  string // future | nodata | none | level
	level int
}

// HeatColumns lays out 24 weeks ending offset weeks before this one.
func HeatColumns(days []model.HeatmapDay, offset int, now time.Time) ([][]heatCell, string) {
	by := map[string]model.HeatmapDay{}
	var maxW uint64
	first := ""
	for _, d := range days {
		by[d.Date] = d
		if d.Words > 0 || d.Sessions > 0 {
			maxW = max(maxW, d.Words)
			if first == "" || d.Date < first {
				first = d.Date
			}
		}
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayKey := today.Format("2006-01-02")
	weekStart := today.AddDate(0, 0, -int(today.Weekday())-offset*7)
	cols := make([][]heatCell, heatCols)
	for ci := range cols {
		start := weekStart.AddDate(0, 0, -(heatCols-1-ci)*7)
		for r := 0; r < 7; r++ {
			d := start.AddDate(0, 0, r)
			key := d.Format("2006-01-02")
			cell := heatCell{date: d, key: key, words: by[key].Words, kind: "none"}
			switch {
			case key > todayKey:
				cell.kind = "future"
			case first == "" || key < first:
				cell.kind = "nodata"
			case cell.words > 0:
				cell.kind = "level"
				cell.level = min(4, max(1, int(math.Ceil(float64(cell.words)/float64(max(maxW, 1))*4))))
			}
			cols[ci] = append(cols[ci], cell)
		}
	}
	return cols, first
}

func (h *Hub) heatmap(c *ui.Context) {
	p := kit.P(c)
	cols, first := HeatColumns(h.stats.Heatmap, h.heatOffset, time.Now())
	tracks := []ui.Track{ui.Fixed(14)}
	for range heatCols {
		tracks = append(tracks, ui.Fr(1))
	}
	ui.Column(c).Children(func() {
		ui.Grid(c).ColumnTracks(tracks...).Gap(6).AlignItems(ui.Center).Children(func() {
			for r, lab := range []string{"S", "M", "T", "W", "T", "F", "S"} {
				ui.Text(c.Key(fmt.Sprint("d", r)), lab).FontSize(10.5).TextColor(p.Text2).TextAlign(ui.Center).FontWeight(550)
				for ci := range cols {
					cell := cols[ci][r]
					box := ui.Box(c.Key(cell.key)).AspectRatio(1).Radius(4.5)
					switch cell.kind {
					case "future":
						box.Border(1, p.Divider)
					case "nodata":
						box.Stripes(p.HeatHatch, 1.4, 2.8, 135)
					case "level":
						box.Background(p.Heat[cell.level])
					default:
						box.Background(p.Heat[0])
					}
					tip := "暂无数据"
					switch cell.kind {
					case "none":
						tip = "没有活动"
					case "level":
						tip = fmt.Sprintf("%d 字 · %s", cell.words, usageNames[cell.level])
					}
					box.Tooltip(fmt.Sprintf("%d年%d月%d日\n%s", cell.date.Year(), cell.date.Month(), cell.date.Day(), tip))
					if box.Hovered() {
						box.Border(1.5, p.Text2)
					}
				}
			}
		})
		canPrev := first != "" && cols[0][0].key > first
		canNext := h.heatOffset > 0
		ui.Row(c).Gap(4).Margin(9, 0, 0).Height(20).Children(func() {
			if arrow(c, icons.ChevronLeft, "上一页", canPrev).Clicked() {
				h.heatOffset += heatPage
			}
			ui.Box(c).Grow(1).Height(16).Children(func() {
				for i := 1; i < len(cols); i++ {
					for _, cell := range cols[i] {
						if cell.date.Day() == 1 {
							ui.Text(c.Key(i), months[cell.date.Month()-1]).Absolute().LeftPercent(float32(i) / heatCols * 100).
								FontSize(11.5).TextColor(p.Text2).SingleLine()
						}
					}
				}
			})
			if arrow(c, icons.ChevronRight, "下一页", canNext).Clicked() {
				h.heatOffset = max(0, h.heatOffset-heatPage)
			}
		})
		ui.Row(c).Gap(5).Margin(12, 0, 0).FontSize(12.5).TextColor(p.Text2).Children(func() {
			ui.Text(c, "更少").Margin(0, 3, 0, 0)
			for l := 1; l <= 4; l++ {
				ui.Box(c.Key(l)).Size(13, 13).Radius(4).Background(p.Heat[l])
			}
			ui.Text(c, "更多").Margin(0, 0, 0, 3)
		})
	})
}

func arrow(c *ui.Context, icon *ui.SVG, label string, enabled bool) ui.Element {
	p := kit.P(c)
	b := ui.ButtonBase(c).Size(18, 18).Radius(6).Justify(ui.Center).TextColor(p.Text2).Label(label).FocusRing(false).Disabled(!enabled)
	if !enabled {
		b.Opacity(0.3)
	} else if b.Hovered() {
		b.Background(p.Hover).TextColor(p.Text)
	}
	return b.Children(func() { ui.Icon(c, icon).FontSize(15) })
}

var _ = platform.KeyboardSettingsURL
