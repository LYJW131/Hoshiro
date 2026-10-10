package bar

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/core"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/store"
	"github.com/egoist/mygo/ui"
)

func newBar(t *testing.T) (*Bar, *core.App, *[]platform.HitRect) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "h.db"), filepath.Join(dir, "rec"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := core.New(core.Paths{DataDir: dir}, model.DefaultSettings(), db, "0.2.0")
	var rects []platform.HitRect
	return New(a, Env{SetHitRects: func(r []platform.HitRect) { rects = r }}), a, &rects
}

func shot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("HOSHIRO_SHOTS")
	if dir == "" {
		return
	}
	tt.Frame()
	img := tt.Image()
	// Composite over a checkerboard, as the bar's transparent window shows the desktop.
	f, _ := os.Create(filepath.Join(dir, "bar-"+name+".png"))
	defer f.Close()
	png.Encode(f, img)
}

func str(s string) *string { return &s }

func TestBarStates(t *testing.T) {
	b, a, rects := newBar(t)
	tt := ui.NewTester(b.View, Width, Height)

	a.PreviewBar(model.BarState{Phase: model.BarHidden}, 0, nil, nil)
	tt.Frame()
	if len(*rects) != 0 {
		t.Fatalf("hidden bar takes clicks: %v", *rects)
	}

	a.PreviewBar(model.BarState{Phase: model.BarIdle, ShowIdleOnDesktop: true, DictationKeyLabel: "fn"}, 0, nil, nil)
	tt.Frame()
	if len(*rects) != 1 {
		t.Fatalf("idle pill rects: %v", *rects)
	}
	shot(t, tt, "idle")

	a.PreviewBar(model.BarState{Phase: model.BarRecording, Mode: model.ModeTranslation, Locked: true, TranslationTargetLabel: str("英语")}, 0.6, nil, nil)
	tt.Frame()
	if !tt.HasText("英语") {
		t.Fatal(tt.Texts())
	}
	shot(t, tt, "locked-translation")

	alerts := []model.BarAlert{{ID: "a1", Kind: "k", Title: "按住 [[fn]] 说话，松开结束", Tone: "info", Actions: []model.AlertAction{}},
		{ID: "a2", Kind: "e", Title: "转录失败", Description: str("MiMo 请求失败（401）。按 *Esc* 关闭"), Tone: "error",
			Actions: []model.AlertAction{{ID: "retry", Label: "重试", Style: "primary"}, {ID: "copy", Label: "复制原文", Style: "secondary"}}}}
	ans := &model.AskAnswer{ID: "q", Question: "把这段翻译成英文", Answer: "**Meeting** at 3 pm.\n\n- bring the `report`\n- invite [Wang](https://example.com)\n\n1. one\n2. two"}
	a.PreviewBar(model.BarState{Phase: model.BarProcessing, Mode: model.ModeAsk}, 0, alerts, ans)
	tt.Frame()
	for _, s := range []string{"转录失败", "重试", "回答", "Meeting", "report"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q: %q", s, tt.Texts())
		}
	}
	if len(*rects) < 3 {
		t.Fatalf("rects %v", *rects)
	}
	shot(t, tt, "processing-alerts-answer")
	if err := tt.Click("复制原文"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("已复制") {
		t.Fatal("copy feedback")
	}
}

func TestMarkdown(t *testing.T) {
	bs := ParseMarkdown("# Title\npara one\nline two\n\n- a\n- b\n\n3. x\n4. y\n\n> quote\n\n```\ncode\n```")
	kinds := ""
	for _, b := range bs {
		kinds += b.Kind + " "
	}
	if kinds != "h p ul ol quote code " || bs[3].Start != 3 || bs[1].Text != "para one\nline two" {
		t.Fatalf("%q %+v", kinds, bs)
	}
	segs := Inline("a `c` **b** *i* [l](https://x.y)")
	want := []string{"text", "code", "text", "bold", "text", "italic", "text", "link"}
	if len(segs) != len(want) {
		t.Fatalf("%+v", segs)
	}
	for i, s := range segs {
		if s.Kind != want[i] {
			t.Fatalf("%d: %+v", i, s)
		}
	}
}

func TestWaveform(t *testing.T) {
	w := newWave()
	var h [waveN]float64
	now := time.Now()
	for i := range 60 {
		at := now.Add(time.Duration(i) * 16 * time.Millisecond)
		h, _ = w.step("live", 0.8, at, at)
	}
	if h[waveN/2] < 10 {
		t.Fatalf("loud speech should raise the middle bar: %v", h)
	}
	for i := range 120 {
		h, _ = w.step("processing", 0, now, now.Add(time.Second+time.Duration(i)*16*time.Millisecond))
	}
	if h[waveN/2] > minH+0.5 {
		t.Fatalf("processing bars collapse to dots: %v", h)
	}
}
