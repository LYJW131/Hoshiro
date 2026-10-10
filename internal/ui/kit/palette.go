// Package kit is Hoshiro's look in MyGo native UI: the design tokens of the
// Tauri version's CSS and the controls built on MyGo's bases.
package kit

import "github.com/egoist/mygo/ui"

// Palette is tokens.css as Go values.
type Palette struct {
	Dark                                        bool
	Page, Vignette, Sidebar, Card, Card2, Hover ui.Color
	Selected, Input, Overlay, Modal, Popover    ui.Color
	Text, Text2, Text3                          ui.Color
	Border, BorderStrong, Divider               ui.Color
	Accent, AccentHover, AccentSoft, AccentText ui.Color
	Danger, DangerSoft, Success, SuccessSoft    ui.Color
	Warning, WarningSoft                        ui.Color
	KeycapBg, KeycapBorder, KeycapText          ui.Color
	Heat                                        [5]ui.Color
	HeatHatch, DotGrid                          ui.Color
	ModalShadow, PopoverShadow                  ui.Color
	UsAsr, UsLlm                                ui.Color
}

const (
	RadiusCard = 20
	RadiusBtn  = 12
)

var Dark = Palette{
	Dark: true,
	Page: ui.Hex("#1e1e1e"), Vignette: ui.Hex("#121212"), Sidebar: ui.Hex("#2a2a2a"), Card: ui.Hex("#323232"),
	Card2: ui.Hex("#2b2b2b"), Hover: ui.Hex("#3a3a3a"), Selected: ui.Hex("#3a3a3a"), Input: ui.Hex("#262626"),
	Overlay: ui.RGBA(0, 0, 0, 0.55), Modal: ui.Hex("#2a2a2a"), Popover: ui.Hex("#303030"),
	Text: ui.Hex("#f2f2f2"), Text2: ui.Hex("#9a9a9a"), Text3: ui.Hex("#6f6f6f"),
	Border: ui.RGBA(255, 255, 255, 0.06), BorderStrong: ui.RGBA(255, 255, 255, 0.12), Divider: ui.RGBA(255, 255, 255, 0.07),
	Accent: ui.Hex("#5b7cfa"), AccentHover: ui.Hex("#6f8dff"), AccentSoft: ui.RGBA(91, 124, 250, 0.18), AccentText: ui.Hex("#ffffff"),
	Danger: ui.Hex("#ff5d5d"), DangerSoft: ui.RGBA(255, 93, 93, 0.14), Success: ui.Hex("#3ccf8e"), SuccessSoft: ui.RGBA(60, 207, 142, 0.14),
	Warning: ui.Hex("#f5b84a"), WarningSoft: ui.RGBA(245, 184, 74, 0.14),
	KeycapBg: ui.Hex("#3b3b3b"), KeycapBorder: ui.RGBA(255, 255, 255, 0.14), KeycapText: ui.Hex("#f2f2f2"),
	Heat:      [5]ui.Color{ui.Hex("#3d3d3d"), ui.Hex("#2c3a6b"), ui.Hex("#3b4fa8"), ui.Hex("#4f6ae0"), ui.Hex("#7b96ff")},
	HeatHatch: ui.RGBA(255, 255, 255, 0.1), DotGrid: ui.RGBA(255, 255, 255, 0.075),
	ModalShadow: ui.RGBA(0, 0, 0, 0.55), PopoverShadow: ui.RGBA(0, 0, 0, 0.45),
	UsAsr: ui.Hex("#5b7cfa"), UsLlm: ui.Hex("#a66cf0"),
}

var Light = Palette{
	Page: ui.Hex("#f3f3f4"), Vignette: ui.Hex("#ebebee"), Sidebar: ui.Hex("#ffffff"), Card: ui.Hex("#ffffff"),
	Card2: ui.Hex("#f7f7f8"), Hover: ui.Hex("#f0f0f2"), Selected: ui.Hex("#ececef"), Input: ui.Hex("#f4f4f6"),
	Overlay: ui.RGBA(20, 20, 24, 0.32), Modal: ui.Hex("#ffffff"), Popover: ui.Hex("#ffffff"),
	Text: ui.Hex("#151515"), Text2: ui.Hex("#6b6b6b"), Text3: ui.Hex("#9b9b9b"),
	Border: ui.RGBA(0, 0, 0, 0.06), BorderStrong: ui.RGBA(0, 0, 0, 0.12), Divider: ui.RGBA(0, 0, 0, 0.07),
	Accent: ui.Hex("#4a68f0"), AccentHover: ui.Hex("#3e5ae0"), AccentSoft: ui.RGBA(74, 104, 240, 0.12), AccentText: ui.Hex("#ffffff"),
	Danger: ui.Hex("#e5484d"), DangerSoft: ui.RGBA(229, 72, 77, 0.1), Success: ui.Hex("#1f9d6b"), SuccessSoft: ui.RGBA(31, 157, 107, 0.1),
	Warning: ui.Hex("#c98a12"), WarningSoft: ui.RGBA(201, 138, 18, 0.12),
	KeycapBg: ui.Hex("#f4f4f6"), KeycapBorder: ui.RGBA(0, 0, 0, 0.14), KeycapText: ui.Hex("#151515"),
	Heat:      [5]ui.Color{ui.Hex("#ececef"), ui.Hex("#cdd7fb"), ui.Hex("#9fb2f8"), ui.Hex("#6f8af0"), ui.Hex("#4a66e0")},
	HeatHatch: ui.RGBA(0, 0, 0, 0.09), DotGrid: ui.RGBA(0, 0, 0, 0.07),
	ModalShadow: ui.RGBA(20, 20, 40, 0.22), PopoverShadow: ui.RGBA(20, 20, 40, 0.16),
	UsAsr: ui.Hex("#4a68f0"), UsLlm: ui.Hex("#8b4fe0"),
}

// Appearance is the user's choice: "system", "light" or "dark".
var Appearance = "system"

// P is the palette in effect.
func P(c *ui.Context) *Palette {
	switch Appearance {
	case "light":
		return &Light
	case "dark":
		return &Dark
	}
	if c.Theme().Dark {
		return &Dark
	}
	return &Light
}

// ApplyTheme makes MyGo's own widgets (text inputs, scroll bars, focus) follow the palette.
func ApplyTheme(c *ui.Context) {
	p := P(c)
	t := *c.Theme()
	t.Dark = p.Dark
	t.Background, t.Surface, t.SurfaceHover = p.Page, p.Input, p.Hover
	t.Border, t.Text, t.TextMuted = p.BorderStrong, p.Text, p.Text2
	t.Accent, t.AccentHover, t.AccentText = p.Accent, p.AccentHover, p.AccentText
	t.Danger, t.Warning, t.Success = p.Danger, p.Warning, p.Success
	t.Selection, t.Focus = p.AccentSoft, p.Accent.Alpha(0.6)
	t.Radius = 10
	c.SetTheme(&t)
	c.Root().Background(p.Page).TextColor(p.Text)
}
