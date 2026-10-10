package hub

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/core"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

var dictTabs = []string{"所有", "自动添加", "手动添加"}
var dictTabSource = []string{"", "auto", "manual"}
var dictEmpty = []string{
	"Hoshiro 会记住您独特的名称和词汇，这些都是通过您的编辑自动学习或您手动添加的。",
	"Hoshiro 会自动学习您独特的名称和词汇，并将其添加到您的个人词典中。",
	"您可以手动将独特的名称和词汇添加到您的个人词典中。",
}

const maxWordLen = 60

type wordModal struct {
	open   bool
	title  string
	submit string
	value  string
	err    string
	edit   *model.DictionaryWord
}

type dictState struct {
	words     []model.DictionaryWord
	loaded    bool
	tab       int
	query     string
	selected  map[string]bool
	modal     wordModal
	preview   []string
	previewOf string
	showPrev  bool
	importing bool
}

func (h *Hub) loadWords(c *ui.Context) {
	d := &h.dict
	if d.selected == nil {
		d.selected = map[string]bool{}
	}
	if !d.loaded || h.take("dictionary") {
		if w, err := h.a.ListWords(); err == nil {
			d.words, d.loaded = w, true
		}
		for id := range d.selected {
			if !slices.ContainsFunc(d.words, func(w model.DictionaryWord) bool { return w.ID == id }) {
				delete(d.selected, id)
			}
		}
	}
}

func (h *Hub) dictionaryPage(c *ui.Context) {
	p := kit.P(c)
	d := &h.dict
	h.loadWords(c)
	q := strings.ToLower(strings.TrimSpace(d.query))
	var visible []model.DictionaryWord
	for _, w := range d.words {
		if (d.tab == 0 || w.Source == dictTabSource[d.tab]) && (q == "" || strings.Contains(strings.ToLower(w.Word), q)) {
			visible = append(visible, w)
		}
	}
	slices.SortFunc(visible, func(a, b model.DictionaryWord) int { return strings.Compare(b.CreatedAt, a.CreatedAt) })

	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(30, 24, 40).Children(func() {
			kit.PageTitle(c, "词典", func() {
				ui.Row(c).Gap(10).Children(func() {
					imp := kit.Btn(c, "导入 CSV", kit.Secondary, kit.Md, icons.FileUp).Disabled(d.importing).Tooltip("使用一列的 CSV 文件，每行一个单词。")
					if imp.Clicked() {
						h.importCSV(c)
					}
					if kit.Btn(c, "新词", kit.Primary, kit.Md, icons.Plus).Clicked() {
						d.modal = wordModal{open: true, title: "添加到词典", submit: "添加词汇"}
					}
				})
			})
			ui.Row(c).Gap(7).AlignItems(ui.Start).Margin(12, 0, 20).FontSize(13).TextColor(p.Text2).LineHeight(1.5).Children(func() {
				ui.Icon(c, icons.Info).FontSize(14).Margin(2, 0, 0)
				ui.Text(c, "Typeless 的词典保存在其云端，无法自动导入；可将词汇整理成单列 CSV（每行一个单词）后点击「导入 CSV」。").Grow(1)
			})
			ui.Row(c).Gap(16).Wrap().Margin(0, 0, 16).Children(func() {
				kit.Segmented(c.Key("dict-tabs"), &d.tab, false, dictTabs...)
				ui.Spacer(c)
				ui.Row(c).Width(250).Children(func() {
					in := ui.TextInput(c.Key("search"), &d.query).Placeholder("搜索").Height(40).Radius(999).Background(p.Card).
						Padding(0, 30, 0, 34).FontSize(14).Grow(1)
					if in.Focused() {
						in.Border(1, p.Accent)
					} else {
						in.Border(1, p.BorderStrong)
					}
					ui.Icon(c, icons.Search).FontSize(15).TextColor(p.Text3).Absolute().Left(12).Top(12.5).PassThrough()
					if d.query != "" && kit.IconBtn(c, icons.X, "清除", 22).Absolute().Right(8).Top(9).Clicked() {
						d.query = ""
					}
				})
			})
			if n := len(d.selected); n > 0 {
				all := len(visible) > 0
				for _, w := range visible {
					all = all && d.selected[w.ID]
				}
				ui.Row(c).Gap(16).Height(48).Margin(0, 0, 12).Padding(0, 14, 0, 18).Radius(14).Background(p.AccentSoft).FontSize(14).Children(func() {
					ui.Text(c, fmt.Sprintf("已选择 %d 个词", n)).FontWeight(650)
					if kit.LinkBtn(c, "全选", false).Disabled(all).Clicked() {
						for _, w := range visible {
							d.selected[w.ID] = true
						}
					}
					if kit.LinkBtn(c, "清除选择", false).Clicked() {
						clear(d.selected)
					}
					ui.Spacer(c)
					if kit.Btn(c, fmt.Sprintf("删除 %d 个词", n), kit.Danger, kit.Sm, icons.Trash).Clicked() {
						h.confirm.Ask(fmt.Sprintf("删除 %d 个词？", n), "所选的词将被永久删除，无法恢复。", "删除", true, func() {
							var ids []string
							for id := range d.selected {
								ids = append(ids, id)
							}
							if err := h.a.DeleteWords(ids); err == nil {
								clear(d.selected)
								c.Toast(fmt.Sprintf("已删除 %d 个词", n))
							}
						})
					}
				})
			}
			if d.loaded && len(visible) == 0 {
				title, desc := "还没有词汇", dictEmpty[d.tab]
				if q != "" {
					title, desc = "没有结果", "未找到与您的搜索匹配的单词。"
				}
				kit.EmptyState(c, icons.BookOpen, title, desc, func() {
					if q == "" && kit.Btn(c, "添加词汇", kit.Primary, kit.Md, icons.Plus).Clicked() {
						d.modal = wordModal{open: true, title: "添加到词典", submit: "添加词汇"}
					}
				})
			} else {
				ui.Column(c).Radius(18).Border(1, p.Border).Clip().Background(p.Card2).Dividers(1, p.Divider).Children(func() {
					for _, w := range visible {
						h.wordRow(c.Key(w.ID), w)
					}
				})
			}
		})
	})
	h.wordModal(c)
	h.importModal(c)
}

func (h *Hub) wordRow(c *ui.Context, w model.DictionaryWord) {
	p := kit.P(c)
	d := &h.dict
	sel := d.selected[w.ID]
	row := ui.Row(c).Gap(12).Height(52).PaddingX(14)
	if row.Clicked() && len(d.selected) > 0 {
		d.selected[w.ID] = !sel
		if !d.selected[w.ID] {
			delete(d.selected, w.ID)
		}
	}
	hover := row.Hovered()
	row.Children(func() {
		check := ui.Box(c).Width(18)
		if !hover && !sel && len(d.selected) == 0 {
			check.Opacity(0)
		}
		check.Children(func() {
			v := sel
			if kit.Checkbox(c, &v).Changed() {
				if v {
					d.selected[w.ID] = true
				} else {
					delete(d.selected, w.ID)
				}
			}
		})
		ui.Text(c, w.Word).FontSize(15.5).FontWeight(550).SingleLine().Grow(1).MinWidth(0)
		icon, tip := icons.PenLine, "手动添加"
		if w.Source == "auto" {
			icon, tip = icons.Sparkles, "由 Hoshiro 自动添加"
		}
		ui.Row(c).Size(26, 26).Justify(ui.Center).Radius(8).TextColor(p.Text3).Tooltip(tip).Children(func() { ui.Icon(c, icon).FontSize(14) })
		acts := ui.Row(c).Gap(2)
		if !hover {
			acts.Opacity(0)
		}
		acts.Children(func() {
			if kit.IconBtn(c, icons.Pencil, "编辑", 30).Clicked() {
				wc := w
				d.modal = wordModal{open: true, title: "编辑词汇", submit: "保存", value: w.Word, edit: &wc}
			}
			if kit.IconBtn(c, icons.Trash, "删除", 30).Clicked() {
				h.confirm.Ask("删除词汇？", fmt.Sprintf("'%s' 将被永久删除，无法恢复。", w.Word), "删除", true, func() {
					if h.a.DeleteWords([]string{w.ID}) == nil {
						c.Toast(fmt.Sprintf("'%s' 已删除", w.Word))
					}
				})
			}
		})
	})
	switch {
	case sel:
		row.Background(p.AccentSoft)
	case hover:
		row.Background(p.Card)
	}
}

func (h *Hub) wordModal(c *ui.Context) {
	m := &h.dict.modal
	if !m.open {
		return
	}
	p := kit.P(c)
	submit := func() {
		v := strings.TrimSpace(m.value)
		switch {
		case v == "":
			m.err = "请输入一个词"
			return
		case len([]rune(v)) > maxWordLen:
			m.err = "名称超过了长度限制。"
			return
		}
		var err error
		if m.edit == nil {
			_, err = h.a.AddWords([]string{v}, "manual")
			if err == nil {
				c.Toast(fmt.Sprintf("'%s' 添加成功", v))
			}
		} else {
			if v != m.edit.Word {
				_, err = h.a.UpdateWord(m.edit.ID, v)
			}
			if err == nil {
				c.Toast(fmt.Sprintf("已更新 '%s'", v))
			}
		}
		if err != nil {
			m.err = err.Error()
			return
		}
		m.open = false
	}
	kit.Modal(c.Key("word"), &m.open, 440, 0, func() {
		kit.ModalHeader(c, m.title, "", func() { m.open = false })
		ui.Column(c).Padding(8, 26, 6).Children(func() {
			in := kit.Input(c, &m.value, m.title, false).AutoFocus()
			if m.err != "" {
				in.Border(1, p.Danger).Shadow(0, 0, 0, 3, p.DangerSoft)
			}
			if in.Changed() {
				m.err = ""
			}
			if in.Submitted() {
				submit()
			}
			if m.err != "" {
				ui.Text(c, m.err).FontSize(13).TextColor(p.Danger).Margin(8, 0, 0)
			}
		})
		ui.Row(c).Justify(ui.End).Gap(10).Padding(16, 26, 22).Children(func() {
			if kit.Btn(c, "取消", kit.Secondary, kit.Md, nil).Clicked() {
				m.open = false
			}
			if kit.Btn(c, m.submit, kit.Primary, kit.Md, nil).Disabled(strings.TrimSpace(m.value) == "").Clicked() {
				submit()
			}
		})
	})
}

func (h *Hub) importCSV(c *ui.Context) {
	d := &h.dict
	if h.env.OpenFile == nil {
		return
	}
	path := h.env.OpenFile("导入 CSV", []string{"csv"})
	if path == "" {
		return
	}
	name := filepath.Base(path)
	words, err := core.PreviewCSV(path)
	if err != nil {
		if strings.Contains(err.Error(), "没有找到") {
			c.Toast("在" + name + "中未找到有效的单词")
		} else {
			c.Toast("文件无法导入。请使用一列的 CSV 文件，每行一个单词。")
		}
		return
	}
	d.preview, d.previewOf, d.showPrev = words, name, true
}

func (h *Hub) importModal(c *ui.Context) {
	d := &h.dict
	if !d.showPrev {
		return
	}
	p := kit.P(c)
	kit.Modal(c.Key("csv"), &d.showPrev, 440, 0, func() {
		kit.ModalHeader(c, "导入 CSV", "", func() { d.showPrev = false })
		ui.Column(c).Padding(8, 26, 6).Children(func() {
			ui.Text(c, fmt.Sprintf("%d 个有效单词在 %s 中找到", len(d.preview), d.previewOf)).FontSize(14.5).Margin(0, 0, 12)
			ui.Scroll(c).MaxHeight(220).Children(func() {
				ui.Row(c).Wrap().Gap(6).Padding(4, 0).Children(func() {
					for i, w := range d.preview[:min(60, len(d.preview))] {
						kit.Chip(c.Key(i), w, false)
					}
					if len(d.preview) > 60 {
						kit.Chip(c, fmt.Sprintf("… 另有 %d 个", len(d.preview)-60), false).TextColor(p.Text2)
					}
				})
			})
		})
		ui.Row(c).Justify(ui.End).Gap(10).Padding(16, 26, 22).Children(func() {
			if kit.Btn(c, "取消", kit.Secondary, kit.Md, nil).Clicked() {
				d.showPrev = false
			}
			if kit.Btn(c, "添加单词", kit.Primary, kit.Md, nil).Clicked() {
				list := d.preview
				d.showPrev = false
				if _, err := h.a.AddWords(list, "manual"); err != nil {
					c.Toast(fmt.Sprintf("添加 %d 个单词失败", len(list)))
				} else {
					c.Toast(fmt.Sprintf("%d 个单词已添加", len(list)))
				}
			}
		})
	})
}

var _ = time.Second
