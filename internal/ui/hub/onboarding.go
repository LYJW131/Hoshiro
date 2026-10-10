package hub

import (
	"fmt"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

type onboardingState struct {
	det       *model.TypelessDetection
	idx, dir  int
	finishing bool
	busy      bool
	result    *model.TypelessImportResult
	quitting  bool
	tryText   string
}

func (o *onboardingState) steps() []string {
	s := []string{"welcome"}
	if o.det != nil && o.det.Installed && o.det.DataFound {
		s = append(s, "import")
	}
	return append(s, "permissions", "providers", "fn", "try", "done")
}

func (h *Hub) onboardingView(c *ui.Context) {
	p := kit.P(c)
	o := &h.onboarding
	if o.det == nil {
		d := h.a.DetectTypeless()
		o.det = &d
		o.dir = 1
	}
	steps := o.steps()
	o.idx = min(o.idx, len(steps)-1)
	step := steps[o.idx]
	last := o.idx >= len(steps)-1
	goTo := func(n int) {
		if n > o.idx {
			o.dir = 1
		} else {
			o.dir = -1
		}
		o.idx = max(0, min(len(steps)-1, n))
	}
	ui.Column(c).Fill().Background(p.Page).Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H * 0.6}, ui.LinearGradient{From: p.AccentSoft, To: p.AccentSoft.Alpha(0), Angle: 180}, 0)
	}).Children(func() {
		ui.Box(c).Height(36).FillWidth().DragWindow()
		ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Center).Padding(0, 32).Children(func() {
			stage := ui.Column(c.Key(step)).Grow(1).MinHeight(0).Width(min(640, func() float32 { w, _ := c.Size(); return w - 64 }())).
				Justify(ui.Center)
			if step == "providers" {
				stage.Justify(ui.Start)
			}
			stage.Opacity(stage.Animate("in", 1, 200*time.Millisecond))
			stage.Children(func() {
				switch step {
				case "welcome":
					h.obWelcome(c)
				case "import":
					h.obImport(c)
				case "permissions":
					obHeader(c, "在您的电脑上设置 Hoshiro", "需要以下权限，才能听到您说话、识别快捷键并把文字输入到光标处。")
					kit.Group(c, "", func() { h.permissionRows(c) })
				case "providers":
					obHeader(c, "连接模型服务", "Hoshiro 使用您自己的 API Key，音频和文字只会发送给这些服务商。可以稍后在设置中修改。")
					ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
						ui.Column(c).Padding(0, 4, 12).Children(func() {
							h.asrForm(c)
							h.llmForm(c)
						})
					})
				case "fn":
					h.obFn(c)
				case "try":
					h.obTry(c)
				case "done":
					h.obDone(c)
				}
			})
		})
		ui.Row(c).Padding(16, 32, 26).Gap(16).Children(func() {
			back := kit.Btn(c, "上一步", kit.Ghost, kit.Md, icons.ArrowLeft)
			if o.idx == 0 {
				back.Invisible()
			}
			if back.Clicked() {
				goTo(o.idx - 1)
			}
			ui.Row(c).Grow(1).Justify(ui.Center).Gap(7).Children(func() {
				for i, s := range steps {
					d := ui.Box(c.Key(s)).Height(7).Radius(4)
					w := d.Animate("w", map[bool]float32{true: 22, false: 7}[i == o.idx], 200*time.Millisecond)
					d.Width(w)
					switch {
					case i == o.idx:
						d.Background(p.Accent)
					case i < o.idx:
						d.Background(p.Accent.Alpha(0.45))
					default:
						d.Background(p.BorderStrong)
					}
				}
			})
			if last {
				b := kit.Btn(c, "开始使用", kit.Primary, kit.Md, icons.Check).Disabled(o.finishing)
				if b.Clicked() {
					o.finishing = true
					if err := h.a.CompleteOnboarding(); err != nil {
						c.Toast("完成引导失败：" + err.Error())
						o.finishing = false
					} else {
						h.settings.OnboardingCompleted = true
						h.forceOnboarding = false
						o.finishing = false
					}
				}
			} else {
				label := "下一步"
				switch step {
				case "welcome":
					label = "开始设置"
				case "import", "providers":
					label = "继续"
				}
				b := kit.Btn(c, label, kit.Primary, kit.Md, nil)
				b.Children(func() { ui.Icon(c, icons.ArrowRight).FontSize(16) })
				if b.Clicked() {
					goTo(o.idx + 1)
				}
			}
		})
	})
}

func obHeader(c *ui.Context, title, sub string) {
	p := kit.P(c)
	ui.Text(c, title).FontSize(32).FontWeight(800).LetterSpacing(-0.5).TextAlign(ui.Center).FillWidth()
	ui.Text(c, sub).FontSize(15).TextColor(p.Text2).LineHeight(1.6).TextAlign(ui.Center).FillWidth().Margin(12, 0, 26).Padding(0, 30)
}

func (h *Hub) obWelcome(c *ui.Context) {
	p := kit.P(c)
	ui.Column(c).AlignItems(ui.Center).Children(func() {
		img := ui.Image(c, logo).Size(96, 96).Radius(26).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.25)).Margin(0, 0, 24)
		img.Opacity(img.Animate("pop", 1, 300*time.Millisecond))
		obHeader(c, "欢迎使用 Hoshiro", "说话，不要打字。按住 Fn 说话，松开后，整理好的文字会直接出现在任何应用的光标处。")
		ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Gap(12).FillWidth().Children(func() {
			for _, f := range [][2]string{{"口述", "杂乱的想法 → 清晰的文字"}, {"翻译", "自然的语言 → 地道的写作"}, {"问任何问题", "编辑或询问所选文本"}} {
				kit.Card(c.Key(f[0])).Padding(16).Gap(6).Children(func() {
					ui.Text(c, f[0]).FontSize(15).FontWeight(700)
					ui.Text(c, f[1]).FontSize(13).TextColor(p.Text2)
				})
			}
		})
	})
}

func (h *Hub) obImport(c *ui.Context) {
	p := kit.P(c)
	o := &h.onboarding
	det := o.det
	obHeader(c, "从 Typeless 迁移", "一键导入快捷键、语言、偏好设置和历史记录，使用习惯完全不变。")
	kit.Card(c).Padding(20).Gap(16).Children(func() {
		ui.Row(c).Gap(16).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				ui.Text(c, "已检测到 Typeless").FontSize(14.5).FontWeight(650)
				ui.Text(c, fmt.Sprintf("找到 %d 条历史记录", det.HistoryCount)).FontSize(13).TextColor(p.Text2).Margin(4, 0, 0)
			})
			if det.Shortcuts != nil {
				for _, s := range det.Shortcuts.Dictation {
					kit.ShortcutKeys(c.Key(s), s, true)
				}
			}
		})
		if det.Running {
			kit.Banner(c, "Typeless 正在运行，会与 Hoshiro 抢占 Fn 键", func() {
				if kit.Btn(c, "退出 Typeless", kit.Secondary, kit.Sm, nil).Disabled(o.quitting).Clicked() {
					o.quitting = true
					go func() {
						_ = h.a.QuitTypeless()
						time.Sleep(600 * time.Millisecond)
						d := h.a.DetectTypeless()
						h.env.Update(func() { o.quitting, o.det = false, &d })
					}()
				}
			})
		}
		if r := o.result; r != nil {
			ui.Column(c).Gap(6).Padding(12, 14).Radius(12).Background(p.SuccessSoft).FontSize(13.5).Children(func() {
				ui.Row(c).Gap(6).Children(func() {
					ui.Icon(c, icons.Check).FontSize(14).TextColor(p.Success)
					ui.Text(c, "导入完成").FontWeight(650)
				})
				lines := []string{map[bool]string{true: "已导入设置", false: "未导入设置"}[r.SettingsImported],
					fmt.Sprintf("历史记录：导入 %d 条，跳过 %d 条", r.HistoryImported, r.HistorySkipped)}
				for i, l := range append(lines, r.Messages...) {
					ui.Text(c.Key(i), "• "+l).TextColor(p.Text2)
				}
			})
		} else {
			b := kit.Btn(c, "从 Typeless 导入", kit.Primary, kit.Lg, nil).Disabled(o.busy)
			if o.busy {
				b.Children(func() { kit.Spinner(c, 15) })
			}
			if b.Clicked() {
				o.busy = true
				go func() {
					r, err := h.a.ImportTypeless(model.TypelessImportOptions{Settings: true, History: true})
					h.env.Update(func() {
						o.busy = false
						if err != nil {
							c.Toast("导入失败：" + err.Error())
							return
						}
						o.result = &r
						h.settings = h.a.Settings()
					})
				}()
			}
		}
		ui.Text(c, "Typeless 的词典保存在其云端，无法自动导入，可稍后在「词典」中通过 CSV 导入。").FontSize(13).TextColor(p.Text2)
	})
}

func (h *Hub) obFn(c *ui.Context) {
	p := kit.P(c)
	ui.Column(c).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(10).Margin(0, 0, 24).Children(func() {
			kit.Keycap(c, "🌐", false)
			kit.Keycap(c, "Fn", false)
		})
		ui.Text(c, "设置 Fn 键").FontSize(32).FontWeight(800).TextAlign(ui.Center)
		b := func(t string) ui.Span { return ui.Span{Text: t, Weight: 700, Color: p.Text} }
		ui.RichText(c, ui.Span{Text: "将 "}, b("系统设置"), ui.Span{Text: " › "}, b("键盘"), ui.Span{Text: " › “按下 🌐 键时” 设为 "},
			b("不执行任何操作"), ui.Span{Text: "，这样按住 Fn 说话时不会触发表情符号或听写。"}).FontSize(15).TextColor(p.Text2).LineHeight(1.6).TextAlign(ui.Center).Margin(12, 0, 26).Padding(0, 30)
		if kit.Btn(c, "打开键盘设置", kit.Secondary, kit.Lg, nil).Clicked() {
			platform.OpenKeyboardSettings()
		}
	})
}

func (h *Hub) obTry(c *ui.Context) {
	p := kit.P(c)
	o := &h.onboarding
	first := "Fn"
	if l := h.settings.Shortcuts.Dictation; len(l) > 0 {
		first = l[0]
	}
	ui.Column(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "试一试").FontSize(32).FontWeight(800)
		ui.Row(c).Gap(6).Wrap().Justify(ui.Center).Margin(12, 0, 22).FontSize(15).TextColor(p.Text2).Children(func() {
			ui.Text(c, "点击下面的输入框，然后按住")
			kit.ShortcutKeys(c, first, true)
			ui.Text(c, "说话，松开即可输入。")
		})
		ta := ui.TextArea(c, &o.tryText).Placeholder("按住 fn 说话，松开即可输入").FillWidth().Height(150).Radius(14).
			Background(p.Card).Padding(14, 16).FontSize(15).LineHeight(1.6).AutoFocus()
		if ta.Focused() {
			ta.Border(1, p.Accent).Shadow(0, 0, 0, 3, p.AccentSoft)
		} else {
			ta.Border(1, p.BorderStrong)
		}
		hint := "快捷键"
		if first == "Fn" {
			hint = "Fn"
		}
		ui.Text(c, "轻点 "+hint+" 后再按一次进入免提模式；按 Esc 取消。").FontSize(13).TextColor(p.Text2).Margin(12, 0, 0)
	})
}

func (h *Hub) obDone(c *ui.Context) {
	p := kit.P(c)
	sc := h.settings.Shortcuts
	ui.Column(c).AlignItems(ui.Center).Children(func() {
		chk := ui.Row(c).Size(72, 72).Radius(36).Justify(ui.Center).Background(p.Success).TextColor(ui.RGB(255, 255, 255)).Margin(0, 0, 24)
		chk.Opacity(chk.Animate("pop", 1, 260*time.Millisecond))
		chk.Children(func() { ui.Icon(c, icons.Check).FontSize(34) })
		obHeader(c, "一切就绪", "只需自然地说话，Hoshiro 会把您的想法变成精炼的文字。")
		kit.Card(c).FillWidth().Dividers(1, p.Divider).Children(func() {
			for _, r := range []struct {
				label string
				list  []string
			}{{"口述", sc.Dictation}, {"翻译", sc.Translation}, {"问任何问题", sc.AskAnything}} {
				ui.Row(c.Key(r.label)).Padding(14, 18).Gap(12).Children(func() {
					ui.Text(c, r.label).FontSize(14.5).FontWeight(600).Grow(1)
					ui.Row(c).Gap(8).Children(func() {
						for _, s := range r.list {
							kit.ShortcutKeys(c.Key(s), s, true)
						}
					})
				})
			}
		})
	})
}
