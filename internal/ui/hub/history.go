package hub

import (
	"fmt"
	"strings"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

const histPage = 50

var histTabs = []struct {
	mode  model.Mode
	label string
}{{"", "全部"}, {model.ModeDictation, "听写"}, {model.ModeTranslation, "翻译"}, {model.ModeAsk, "问任何问题"}}

var modeChip = map[model.Mode]string{model.ModeTranslation: "翻译", model.ModeAsk: "问任何问题"}

var retentionOptions = []kit.Option{
	{Value: "never", Label: "从不"}, {Value: "24h", Label: "24 小时"}, {Value: "1w", Label: "1 周"},
	{Value: "1m", Label: "1 个月"}, {Value: "1y", Label: "1 年"}, {Value: "forever", Label: "永远"},
}

func retentionRank(r string) int {
	for i, o := range retentionOptions {
		if o.Value == r {
			return i
		}
	}
	return -1
}

func retentionLabel(r string) string {
	for _, o := range retentionOptions {
		if o.Value == r {
			return o.Label
		}
	}
	return r
}

type histState struct {
	tab, loadedTab int
	limit          int
	items          []model.HistoryItem
	hasMore        bool
	loaded         bool
	busy           map[string]bool
	open           map[string]bool
	answer         map[string]bool
	copied         string
	retention      string
}

func (h *Hub) loadHistory(c *ui.Context) {
	hs := &h.hist
	changed := h.take("history")
	if hs.loaded && !changed && hs.loadedTab == hs.tab {
		return
	}
	if hs.loadedTab != hs.tab {
		hs.limit = histPage
	}
	var mode *model.Mode
	if m := histTabs[hs.tab].mode; m != "" {
		mode = &m
	}
	items, err := h.a.ListHistory(model.HistoryQuery{Mode: mode, Limit: uint32(max(hs.limit, histPage))})
	if err != nil {
		c.Toast(err.Error())
		return
	}
	hs.items, hs.loaded, hs.loadedTab = items, true, hs.tab
	hs.hasMore = len(items) >= max(hs.limit, histPage)
}

// FormatTime is "03:04 PM".
func FormatTime(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return ""
	}
	return t.Local().Format("03:04 PM")
}

var weekdays = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// GroupLabel is "今天", "昨天" or "10月8日 周四".
func GroupLabel(day time.Time, now time.Time) string {
	key := day.Format("2006-01-02")
	if key == now.Format("2006-01-02") {
		return "今天"
	}
	if key == now.AddDate(0, 0, -1).Format("2006-01-02") {
		return "昨天"
	}
	base := fmt.Sprintf("%d月%d日 %s", day.Month(), day.Day(), weekdays[day.Weekday()])
	if day.Year() == now.Year() {
		return base
	}
	return fmt.Sprintf("%d年%s", day.Year(), base)
}

func (h *Hub) historyPage(c *ui.Context) {
	p := kit.P(c)
	hs := &h.hist
	if hs.busy == nil {
		hs.busy, hs.open, hs.answer = map[string]bool{}, map[string]bool{}, map[string]bool{}
	}
	h.loadHistory(c)
	ui.Column(c).Grow(1).MinHeight(0).Padding(0, 20, 0, 24).Children(func() {
		ui.Column(c).Padding(30, 0, 0).Children(func() {
			kit.PageTitle(c, "历史记录", func() {
				more := kit.IconBtn(c, icons.More, "更多", 34)
				kit.Dropdown(c, more, []kit.MenuItem{{Label: "删除所有", Icon: icons.Trash, Danger: true, Click: func() {
					h.confirm.Ask("删除所有历史记录？", "您所有的本地历史记录将被永久删除，无法恢复。", "删除", true, func() {
						if err := h.a.DeleteAllHistory(); err != nil {
							c.Toast("删除所有历史记录失败")
						} else {
							hs.items, hs.hasMore = nil, false
							c.Toast("所有记录已删除")
						}
					})
				}}})
			})
			kit.Card(c).Padding(4, 20).Margin(16, 0, 0).Children(func() {
				ui.Row(c).Gap(20).Padding(14, 0).Children(func() {
					ui.Column(c).Grow(1).Children(func() {
						ui.Row(c).Gap(8).FontSize(15).FontWeight(700).Children(func() {
							ui.Icon(c, icons.HardDrive).FontSize(15).TextColor(p.Text2)
							ui.Text(c, "保存历史")
						})
						ui.Text(c, "选择在此设备上保留历史记录的时间。").FontSize(14).TextColor(p.Text2).Margin(6, 0, 0)
					})
					hs.retention = h.settings.HistoryRetention
					if kit.Select(c, &hs.retention, retentionOptions).Width(140).Changed() {
						h.changeRetention(hs.retention)
					}
				})
				ui.Box(c).Height(1).Background(p.Divider)
				ui.Column(c).Padding(14, 0).Children(func() {
					ui.Row(c).Gap(8).FontSize(15).FontWeight(700).Children(func() {
						ui.Icon(c, icons.Lock).FontSize(15).TextColor(p.Text2)
						ui.Text(c, "您的数据保持私密")
					})
					ui.Text(c, "录音和转录仅存储在此设备上，音频只会发送给您配置的语音识别与改写服务商。").FontSize(14).TextColor(p.Text2).LineHeight(1.5).Margin(6, 0, 0)
				})
			})
			ui.Row(c).Margin(20, 0, 8).Children(func() {
				labels := make([]string, len(histTabs))
				for i, t := range histTabs {
					labels[i] = t.label
				}
				kit.Segmented(c.Key("hist-tabs"), &hs.tab, false, labels...)
			})
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Margin(0, -10, 0, 0).Children(func() {
			ui.Column(c).Padding(8, 10, 40, 0).Children(func() {
				switch {
				case h.settings.HistoryRetention == "never" && len(hs.items) == 0:
					kit.EmptyState(c, icons.History, "从不保存历史", "您可以在顶部更改持续时间以开始保存口述历史。", nil)
				case len(hs.items) == 0:
					kit.EmptyState(c, icons.History, "尚无口述", "一旦您在不同应用中进行口述，您的所有口述历史将出现在这里。", nil)
				default:
					h.historyGroups(c)
				}
				sentinel := ui.Box(c).Height(40)
				if hs.hasMore {
					_, wh := c.Size()
					if b := sentinel.Bounds(); b.H > 0 && b.Y < wh+200 {
						hs.limit = len(hs.items) + histPage
						h.Changed("history")
					}
				}
			})
		})
	})
}

func (h *Hub) changeRetention(next string) {
	cur := h.settings.HistoryRetention
	if next == cur {
		return
	}
	apply := func() { h.update(func(s *model.Settings) { s.HistoryRetention = next }) }
	if retentionRank(next) < retentionRank(cur) {
		never := next == "never"
		title, desc := "删除旧历史记录？", "您所有的本地历史记录早于 "+retentionLabel(next)+" 将被永久删除，无法恢复。"
		if never {
			title, desc = "从不保存历史？", "您所有的本地历史记录将被永久删除，无法恢复。"
		}
		h.confirm.Ask(title, desc, "删除", true, apply)
		return
	}
	apply()
}

func (h *Hub) historyGroups(c *ui.Context) {
	p := kit.P(c)
	now := time.Now()
	type group struct {
		key, label string
		rows       []model.HistoryItem
	}
	var groups []group
	for _, it := range h.hist.items {
		t, _ := time.Parse(time.RFC3339Nano, it.CreatedAt)
		t = t.Local()
		key := t.Format("2006-01-02")
		if n := len(groups); n > 0 && groups[n-1].key == key {
			groups[n-1].rows = append(groups[n-1].rows, it)
		} else {
			groups = append(groups, group{key, GroupLabel(t, now), []model.HistoryItem{it}})
		}
	}
	for _, g := range groups {
		ui.Column(c.Key(g.key)).Margin(0, 0, 18).Children(func() {
			ui.Text(c, g.label).FontSize(14).FontWeight(550).TextColor(p.Text2).Padding(6, 0, 12)
			ui.Column(c).Radius(16).Border(1, p.Border).Clip().Background(p.Page).Dividers(1, p.Divider).Children(func() {
				for _, it := range g.rows {
					h.historyRow(c.Key(it.ID), it)
				}
			})
		})
	}
}

func (h *Hub) retryHistory(c *ui.Context, id string) {
	hs := &h.hist
	hs.busy[id] = true
	var item model.HistoryItem
	var err error
	h.async(c, func() { item, err = h.a.RetryHistory(h.ctx(), id) }, func() {
		delete(hs.busy, id)
		if err != nil {
			c.Toast("重试失败：" + err.Error())
			return
		}
		for i := range hs.items {
			if hs.items[i].ID == id {
				hs.items[i] = item
			}
		}
	})
}

func (h *Hub) historyRow(c *ui.Context, it model.HistoryItem) {
	p := kit.P(c)
	hs := &h.hist
	final := ""
	if it.RefinedText != nil {
		final = *it.RefinedText
	} else if it.RawText != nil {
		final = *it.RawText
	}
	row := ui.Row(c).Gap(18).Padding(16, 20).AlignItems(ui.Start)
	menuOpen := false
	if row.Clicked() {
		hs.open[it.ID] = !hs.open[it.ID]
	}
	row.Children(func() {
		ui.Column(c).Width(88).Gap(6).AlignItems(ui.Start).FontSize(14).TextColor(p.Text2).Padding(1, 0, 0).Children(func() {
			ui.Text(c, FormatTime(it.CreatedAt)).FontFeatures("tnum")
			if chip := modeChip[it.Mode]; chip != "" {
				ui.Text(c, chip).Padding(1, 7).Radius(6).FontSize(11).FontWeight(650).Background(p.AccentSoft).TextColor(p.Accent)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Padding(0, 76, 0, 0).Children(func() {
			h.historyBody(c, it, final)
			if hs.open[it.ID] {
				raw := "（无）"
				if it.RawText != nil && *it.RawText != "" {
					raw = *it.RawText
				}
				var meta []string
				if it.AppName != nil {
					meta = append(meta, *it.AppName)
				}
				if it.DurationSecs > 0 {
					meta = append(meta, fmt.Sprintf("%.1f 秒", it.DurationSecs))
				}
				if it.Source == "typeless" {
					meta = append(meta, "从 Typeless 导入")
				}
				box := ui.Column(c).Margin(12, 0, 0).Padding(12, 14).Radius(12).Background(p.Input).Border(1, p.Border)
				box.Opacity(box.Animate("in", 1, 180*time.Millisecond))
				box.Children(func() {
					ui.Text(c, "原始转录").FontSize(12).FontWeight(650).TextColor(p.Text2).Margin(0, 0, 6).LetterSpacing(0.24)
					ui.Text(c, raw).FontSize(14).LineHeight(1.6).TextColor(p.Text2).Selectable()
					ui.Text(c, strings.Join(meta, " · ")).FontSize(12).TextColor(p.Text3).Margin(8, 0, 0)
				})
			}
		})
		actions := ui.Row(c).Absolute().Top(10).Right(14).Gap(2)
		actions.Children(func() {
			if final != "" && it.Status == "completed" {
				icon := icons.Copy
				if hs.copied == it.ID {
					icon = icons.Check
				}
				b := kit.IconBtn(c, icon, "复制转录", 30)
				if hs.copied == it.ID {
					b.Tooltip("已复制")
				}
				if b.Clicked() {
					if err := h.a.CopyText(final); err == nil {
						hs.copied = it.ID
						id := it.ID
						go func() {
							time.Sleep(1400 * time.Millisecond)
							h.env.Update(func() {
								if hs.copied == id {
									hs.copied = ""
								}
							})
						}()
					} else {
						c.Toast(err.Error())
					}
				}
			}
			var items []kit.MenuItem
			if it.HasAudio {
				items = append(items,
					kit.MenuItem{Label: "重试", Icon: icons.RotateCcw, Click: func() { h.retryHistory(c, it.ID) }},
					kit.MenuItem{Label: "下载音频", Icon: icons.Download, Click: func() { h.downloadAudio(c, it) }})
			}
			items = append(items, kit.MenuItem{Label: "删除转录", Icon: icons.Trash, Danger: true, Click: func() {
				h.confirm.Ask("删除此转录？", "此转录将被永久删除，无法恢复。", "删除", true, func() {
					if err := h.a.DeleteHistory(it.ID); err != nil {
						c.Toast("删除转录失败。")
						return
					}
					c.Toast("转录已删除")
				})
			}})
			menuOpen = kit.Dropdown(c, kit.IconBtn(c, icons.More, "更多", 30), items)
		})
		if !row.Hovered() && !menuOpen {
			actions.Opacity(0)
		}
	})
	if row.Hovered() || menuOpen {
		row.Background(p.Hover)
	}
}

func (h *Hub) historyBody(c *ui.Context, it model.HistoryItem, final string) {
	p := kit.P(c)
	hs := &h.hist
	status := func(fn func()) {
		ui.Row(c).Gap(8).Wrap().FontSize(15).LineHeight(1.65).TextColor(p.Text2).Children(fn)
	}
	retry := func() {
		if it.HasAudio {
			b := ui.ButtonBase(c).FocusRing(false).FontSize(14).FontWeight(600).TextColor(p.Accent)
			b.Children(func() {
				t := ui.Text(c, "重试")
				if b.Hovered() {
					t.Underline()
				}
			})
			if b.Clicked() {
				h.retryHistory(c, it.ID)
			}
		}
	}
	switch {
	case hs.busy[it.ID] || it.Status == "processing":
		status(func() { kit.Spinner(c, 13); ui.Text(c, "正在处理…") })
	case it.Status == "dismissed":
		status(func() { ui.Text(c, "转录已被取消。"); retry() })
	case it.Status == "error":
		msg := "您的转录被中断。但 Hoshiro 仍然可以为您重试。"
		if it.ErrorMessage != nil && *it.ErrorMessage != "" {
			msg = *it.ErrorMessage
		}
		status(func() {
			ui.Icon(c, icons.AlertCircle).FontSize(14).TextColor(p.Danger)
			ui.Text(c, msg).TextColor(p.Text).Shrink(1)
			retry()
		})
	case it.Status == "empty":
		status(func() { ui.Text(c, "音频无声。").Tooltip("Hoshiro 没有检测到口述中的任何单词，因此此转录为空。") })
	case it.Mode == model.ModeAsk:
		ui.Column(c).Gap(8).AlignItems(ui.Start).Children(func() {
			if it.SelectedText != nil && *it.SelectedText != "" {
				ui.Text(c, *it.SelectedText).Padding(4, 0, 4, 12).BorderWidth(0, 0, 0, 3).BorderColor(p.BorderStrong).
					TextColor(p.Text2).FontSize(14).LineHeight(1.55)
			}
			q := final
			if it.RawText != nil && *it.RawText != "" {
				q = *it.RawText
			}
			ui.Text(c, q).FontSize(15).LineHeight(1.65)
			if it.RefinedText != nil {
				label := "查看答案"
				if hs.answer[it.ID] {
					label = "收起答案"
				}
				if kit.LinkBtn(c, label, true).Clicked() {
					hs.answer[it.ID] = !hs.answer[it.ID]
				}
				if hs.answer[it.ID] {
					ui.Text(c, *it.RefinedText).FillWidth().Padding(12, 14).Radius(12).Background(p.Input).Border(1, p.Border).
						FontSize(14.5).LineHeight(1.65).Selectable()
				}
			}
		})
	default:
		ui.Text(c, final).FontSize(15).LineHeight(1.65)
	}
}

func (h *Hub) downloadAudio(c *ui.Context, it model.HistoryItem) {
	if h.env.SaveFile == nil {
		c.Toast("下载音频仅在桌面应用中可用")
		return
	}
	t, _ := time.Parse(time.RFC3339Nano, it.CreatedAt)
	t = t.Local()
	path := h.env.SaveFile("保存音频", fmt.Sprintf("hoshiro-%s-%s.wav", t.Format("2006-01-02"), t.Format("1504")))
	if path == "" {
		return
	}
	if err := h.a.ExportHistoryAudio(it.ID, path); err != nil {
		c.Toast("保存音频失败：" + err.Error())
		return
	}
	c.Toast("音频已保存")
}
