package hub

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/IcyBlue17/hoshiro/internal/ui/icons"
	"github.com/IcyBlue17/hoshiro/internal/ui/kit"
	"github.com/egoist/mygo/ui"
)

const (
	RepoURL = "https://github.com/IcyBlue17/hoshiro"
	Tagline = "按住 Fn 说话，松开就是写好的字"
)

//go:embed license.txt
var licenseText string

//go:embed licenses.json
var licensesJSON []byte

type component struct{ Name, Version, License, URL string }

var goComponents = func() []component {
	var d struct {
		Go []component `json:"go"`
	}
	_ = json.Unmarshal(licensesJSON, &d)
	return d.Go
}()

func (h *Hub) openURL(c *ui.Context, url string) {
	if err := h.a.OpenURL(url); err != nil {
		c.Toast("无法打开链接：" + err.Error())
	}
}

func (h *Hub) aboutSection(c *ui.Context) {
	p := kit.P(c)
	v := h.a.Version
	if v == "" {
		v = "0.1.0"
	}
	ui.Column(c).AlignItems(ui.Center).Padding(18, 0, 26).Children(func() {
		ui.Image(c, logo).Size(84, 84).Radius(22).Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, 0.22)).Border(1, p.Border)
		ui.Text(c, "Hoshiro").FontSize(26).FontWeight(750).LetterSpacing(0.26).Margin(16, 0, 0)
		ui.Text(c, "版本 "+v).FontSize(13).TextColor(p.Text2).Margin(4, 0, 0).FontFeatures("tnum")
		ui.Text(c, Tagline).FontSize(14.5).TextColor(p.Text2).Margin(12, 0, 0)
	})
	h.updateGroup(c, v)
	kit.Group(c, "", func() {
		kit.SRow(c, "GitHub", "IcyBlue17/hoshiro · 源码、反馈与发布", false, false, func() {
			b := kit.Btn(c, "打开仓库", kit.Secondary, kit.Md, nil)
			b.Children(func() { ui.Icon(c, icons.ExternalLink).FontSize(13) })
			if b.Clicked() {
				h.openURL(c, RepoURL)
			}
		})
		kit.SRow(c, "反馈问题", "遇到 bug 或有想法，欢迎开个 Issue。", false, false, func() {
			b := kit.Btn(c, "Issues", kit.Secondary, kit.Md, nil)
			b.Children(func() { ui.Icon(c, icons.ExternalLink).FontSize(13) })
			if b.Clicked() {
				h.openURL(c, RepoURL+"/issues")
			}
		})
	})
	kit.Group(c, "", func() {
		if aboutLink(c, icons.FileText, "开源许可", "Hoshiro 以此协议开放源码", "MIT License") {
			h.set.lic = true
		}
		if aboutLink(c, icons.Package, "第三方开源组件", fmt.Sprintf("Go %d", len(goComponents)), fmt.Sprintf("%d 个", len(goComponents))) {
			h.set.deps, h.set.depsQuery = true, ""
		}
	})
	kit.Group(c, "", func() {
		kit.SRow(c, "隐私", "历史记录、词典和 API Key 仅保存在此设备上；音频与文字只会发送给您自行配置的语音识别与改写服务商；检查更新时只会访问 GitHub。", false, false, nil)
	})
	ui.Text(c, "Hoshiro 的诞生离不开这些开源项目 ❤︎").FontSize(13).TextColor(p.Text3).TextAlign(ui.Center).FillWidth().Margin(26, 0, 4)
}

func aboutLink(c *ui.Context, icon *ui.SVG, title, desc, value string) bool {
	p := kit.P(c)
	b := ui.ButtonBase(c.Key(title)).Justify(ui.Start).Gap(14).Padding(15, 18).FillWidth().TextColor(p.Text2).FocusRing(false)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(18).TextColor(p.Text)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, title).FontSize(14.5).FontWeight(650).TextColor(p.Text)
			ui.Text(c, desc).FontSize(13).TextColor(p.Text2).Margin(4, 0, 0)
		})
		ui.Text(c, value).FontSize(13.5)
		ui.Icon(c, icons.ChevronRight).FontSize(16)
	})
	return b.Clicked()
}

func (h *Hub) updateGroup(c *ui.Context, version string) {
	p := kit.P(c)
	phase, info, progress := h.updatePhase()
	status := updateLabel(phase, info, progress)
	desc := "v" + version
	if status != "" {
		desc += " · " + status
	}
	kit.Group(c, "", func() {
		kit.SRow(c, "版本", desc, false, false, func() {
			switch phase {
			case "available":
				if kit.Btn(c, "更新并重启", kit.Primary, kit.Md, nil).Clicked() {
					go func() {
						if err := h.a.InstallUpdate(h.ctx()); err != nil {
							h.env.Update(func() { c.Toast("更新失败：" + err.Error()) })
						}
					}()
				}
			case "installing":
			default:
				label := "检查更新"
				if phase == "checking" {
					label = "检查中…"
				}
				if kit.Btn(c, label, kit.Secondary, kit.Md, nil).Disabled(phase == "checking").Clicked() {
					h.checkUpdate(c)
				}
			}
		})
		if phase == "installing" {
			ui.Column(c).Padding(0, 18, 16).Children(func() {
				track := ui.Box(c).Height(6).Radius(3).Background(p.Hover).Clip()
				track.Children(func() {
					if progress < 0 {
						x := track.Loop("indet", 1200*1e6, ui.EaseInOut)
						ui.Box(c).WidthPercent(35).FillHeight().Radius(3).Background(p.Accent).Absolute().LeftPercent(x*100 - 35)
					} else {
						ui.Box(c).WidthPercent(float32(progress) * 100).FillHeight().Radius(3).Background(p.Accent)
					}
				})
			})
		}
		if phase == "available" && info != nil && strings.TrimSpace(info.Notes) != "" {
			notes := strings.TrimSpace(info.Notes)
			long := strings.Count(notes, "\n") >= 4 || len([]rune(notes)) > 200
			ui.Column(c).Padding(0, 18, 16).Gap(6).AlignItems(ui.Start).Children(func() {
				t := ui.Text(c, notes).FontSize(13).TextColor(p.Text2).LineHeight(1.6).Padding(10, 12).Radius(10).Background(p.Input).FillWidth()
				if long && !h.set.notesOpen {
					t.MaxLines(4)
				}
				if long {
					label := "展开"
					if h.set.notesOpen {
						label = "收起"
					}
					if kit.LinkBtn(c, label, true).Clicked() {
						h.set.notesOpen = !h.set.notesOpen
					}
				}
			})
		}
	})
}

var paraSplit = regexp.MustCompile(`\n\s*\n`)
var wrapSpace = regexp.MustCompile(`\s*\n\s*`)

func (h *Hub) aboutModals(c *ui.Context) {
	p := kit.P(c)
	if h.set.lic {
		kit.Modal(c.Key("lic"), &h.set.lic, 560, 0, func() {
			kit.ModalHeader(c, "开源许可", "MIT License", func() { h.set.lic = false })
			ui.Scroll(c).MaxHeight(420).Children(func() {
				ui.Column(c).Gap(10).Padding(8, 26, 26).Children(func() {
					for i, para := range paraSplit.Split(strings.TrimSpace(licenseText), -1) {
						ui.Text(c.Key(i), wrapSpace.ReplaceAllString(para, " ")).FontSize(13).TextColor(p.Text2).LineHeight(1.6).Selectable()
					}
				})
			})
		})
	}
	if h.set.deps {
		kit.Modal(c.Key("deps"), &h.set.deps, 640, 0, func() {
			kit.ModalHeader(c, "第三方开源组件", fmt.Sprintf("共 %d 个链接进应用的 Go 模块。其中 MPL-2.0 为文件级弱 copyleft 许可，可正常随应用分发，仅供知悉。", len(goComponents)), func() { h.set.deps = false })
			q := strings.ToLower(strings.TrimSpace(h.set.depsQuery))
			var hit []component
			for _, comp := range goComponents {
				if q == "" || strings.Contains(strings.ToLower(comp.Name), q) || strings.Contains(strings.ToLower(comp.License), q) {
					hit = append(hit, comp)
				}
			}
			ui.Column(c).Padding(8, 26, 22).Gap(10).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					kit.Input(c, &h.set.depsQuery, "搜索名称或许可证", false).Grow(1)
					ui.Text(c, fmt.Sprint(len(hit))).FontSize(13).TextColor(p.Text2)
				})
				ui.Scroll(c).Height(380).Children(func() {
					ui.Column(c).Children(func() {
						if len(hit) > 0 {
							ui.Row(c).Height(36).Gap(8).PaddingX(8).FontSize(12.5).FontWeight(650).TextColor(p.Text2).Children(func() {
								ui.Text(c, "Go")
								ui.Text(c, fmt.Sprint(len(hit))).TextColor(p.Text3)
							})
						}
						for _, comp := range hit {
							b := ui.ButtonBase(c.Key(comp.Name)).Justify(ui.Start).Height(46).Gap(10).PaddingX(8).Radius(10).FocusRing(false).Tooltip(comp.URL)
							if b.Hovered() {
								b.Background(p.Hover)
							}
							b.Children(func() {
								ui.Text(c, comp.Name).FontSize(13.5).FontWeight(600).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
								ui.Text(c, comp.Version).FontSize(12).TextColor(p.Text3).SingleLine()
								ui.Spacer(c)
								badge := ui.Text(c, comp.License).FontSize(11.5).FontWeight(600).Padding(2, 8).Radius(999).Background(p.Hover).TextColor(p.Text2).SingleLine()
								if strings.Contains(strings.ToUpper(comp.License), "MPL") {
									badge.Background(p.WarningSoft).TextColor(p.Warning)
								}
								ui.Icon(c, icons.ExternalLink).FontSize(13).TextColor(p.Text3)
							})
							if b.Clicked() {
								h.openURL(c, comp.URL)
							}
						}
						if len(hit) == 0 {
							ui.Text(c, "没有匹配的组件").TextColor(p.Text2).Padding(30, 0).TextAlign(ui.Center).FillWidth()
						}
					})
				})
			})
		})
	}
}
