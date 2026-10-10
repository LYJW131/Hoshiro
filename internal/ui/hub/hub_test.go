package hub

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/IcyBlue17/hoshiro/internal/core"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/store"
	"github.com/egoist/mygo/ui"
)

func newHub(t *testing.T, mutate func(*model.Settings)) (*Hub, *core.App) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "h.db"), filepath.Join(dir, "rec"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := model.DefaultSettings()
	s.OnboardingCompleted = true
	s.Asr.Mimo.APIKey = "k"
	if mutate != nil {
		mutate(&s)
	}
	a := core.New(core.Paths{DataDir: dir, DBPath: filepath.Join(dir, "h.db"), RecordingsDir: filepath.Join(dir, "rec")}, s, db, "0.2.0")
	return New(a, Env{}), a
}

// shot saves the tester's last frame when HOSHIRO_SHOTS names a directory.
func shot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("HOSHIRO_SHOTS")
	if dir == "" {
		return
	}
	tt.Frame()
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestHomeRenders(t *testing.T) {
	h, _ := newHub(t, nil)
	tt := ui.NewTester(h.View, 1080, 750)
	tt.SetDark(true)
	if !tt.HasText("说话，不要打字") || !tt.HasText("洞察") || !tt.HasText("口述字数") {
		t.Fatal(tt.Texts())
	}
	shot(t, tt, "home-dark")
	tt.SetDark(false)
	shot(t, tt, "home-light")
}

func seedHistory(t *testing.T, a *core.App) {
	t.Helper()
	str := func(s string) *string { return &s }
	rows := []store.HistoryRow{}
	r := store.NewRow("a1", model.ModeDictation, "completed")
	r.RawText, r.RefinedText, r.AppName, r.DurationSecs = str("嗯 今天 下午 三点 开会"), str("今天下午三点开会。"), str("Notes"), 3.2
	rows = append(rows, r)
	r = store.NewRow("a2", model.ModeAsk, "completed")
	r.RawText, r.RefinedText, r.SelectedText = str("翻译成英文"), str("Meeting at 3 pm today."), str("今天下午三点开会")
	rows = append(rows, r)
	r = store.NewRow("a3", model.ModeTranslation, "error")
	r.ErrorMessage = str("MiMo 请求失败（401）：API Key 无效")
	rows = append(rows, r)
	r = store.NewRow("a4", model.ModeDictation, "empty")
	rows = append(rows, r)
	for _, r := range rows {
		if err := a.DB.UpsertHistory(r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHistoryPage(t *testing.T) {
	h, a := newHub(t, nil)
	seedHistory(t, a)
	h.page = "history"
	tt := ui.NewTester(h.View, 1080, 1100)
	tt.SetDark(true)
	for _, s := range []string{"历史记录", "今天下午三点开会。", "翻译成英文", "查看答案", "音频无声。", "保存历史"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if err := tt.Click("查看答案"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Meeting at 3 pm today.") {
		t.Fatal("answer not shown")
	}
	if err := tt.Click("今天下午三点开会。"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("原始转录") {
		t.Fatal("raw not shown")
	}
	shot(t, tt, "history-dark")
	if err := tt.Click("翻译"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("今天下午三点开会。") || !tt.HasText("MiMo 请求失败（401）：API Key 无效") {
		t.Fatal("tab filter", tt.Texts())
	}
}

func TestDictionaryPage(t *testing.T) {
	h, a := newHub(t, nil)
	a.AddWords([]string{"Tauri", "MiMo"}, "manual")
	a.AddWords([]string{"Hoshiro"}, "auto")
	h.page = "dictionary"
	tt := ui.NewTester(h.View, 1080, 750)
	tt.SetDark(true)
	if !tt.HasText("Tauri") || !tt.HasText("Hoshiro") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("新词"); err != nil {
		t.Fatal(err)
	}
	tt.Type("星辰")
	if err := tt.Click("添加词汇"); err != nil {
		t.Fatal(err)
	}
	h.Changed("dictionary")
	tt.Frame()
	if !tt.HasText("星辰") {
		t.Fatal("word not added", tt.Texts())
	}
	if err := tt.Click("自动添加"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Tauri") || !tt.HasText("Hoshiro") {
		t.Fatal("filter")
	}
	if err := tt.Click("所有"); err != nil {
		t.Fatal(err)
	}
	shot(t, tt, "dictionary-dark")
}

func TestStatisticsPage(t *testing.T) {
	h, a := newHub(t, nil)
	str := func(s string) *string { return &s }
	for i, w := range []string{"今天下午三点开会。", "把这个发给小王", "Hello world again"} {
		r := store.NewRow(fmt.Sprint("s", i), model.ModeDictation, "completed")
		r.RawText, r.RefinedText, r.AppName, r.DurationSecs = str(w), str(w), str([]string{"Notes", "Slack", "Notes"}[i]), 4
		r.AsrProvider, r.AsrMs, r.LlmModel, r.LlmMs, r.TotalMs = str("mimo"), u32(820), str("gemini-flash"), u32(640), u32(1700)
		r.Words = uint32(len([]rune(w)))
		if err := a.DB.UpsertHistory(r); err != nil {
			t.Fatal(err)
		}
		in, out := uint64(1200), uint64(80)
		a.DB.RecordUsage("gemini", "gemini-flash", &in, &out, nil, 0)
		a.DB.RecordUsage("mimo", "mimo-v2.5", nil, nil, nil, 4)
	}
	h.page = "statistics"
	tt := ui.NewTester(h.View, 1080, 1800)
	tt.SetDark(true)
	for _, s := range []string{"使用统计", "每日趋势", "速度", "结果", "常用应用", "Notes", "使用时段", "语音识别", "改写模型"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q: %q", s, tt.Texts())
		}
	}
	shot(t, tt, "statistics-dark")
}

func u32(v uint32) *uint32 { return &v }

func TestSettingsModal(t *testing.T) {
	h, a := newHub(t, nil)
	tt := ui.NewTester(h.View, 1080, 1500)
	tt.SetDark(true)
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	for _, cat := range categories {
		if err := tt.Click(cat.label); err != nil {
			t.Fatalf("%s: %v", cat.label, err)
		}
		tt.Frame()
		shot(t, tt, "settings-"+cat.key)
	}
	// Providers: switch the rewrite provider and type a key.
	tt.Click("模型服务")
	tt.Frame()
	if !tt.HasText("语音识别 · 小米 MiMo") || !tt.HasText("改写（AI 润色）") {
		t.Fatal(tt.Texts())
	}
	r, _ := tt.Find("改写（AI 润色）")
	tt.Scroll(r.X, r.Y, 0, 400)
	tt.Frame()
	ar, ok := tt.Find("Anthropic")
	t.Log(r, ar, ok)
	if err := tt.Click("Anthropic"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	shot(t, tt, "settings-providers-2")
	if a.Settings().Llm.Provider != model.LlmAnthropic {
		t.Fatal("provider not saved")
	}
	// General: appearance.
	tt.Click("常规")
	tt.Frame()
	if err := tt.Click("明亮"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Settings().Appearance != "light" {
		t.Fatal("appearance not saved")
	}
	shot(t, tt, "settings-general-light")
}

func TestShortcutValidation(t *testing.T) {
	others := []string{"Fn", "Fn+Space", "Fn+LeftShift", "Ctrl+Cmd+V"}
	for combo, want := range map[string]string{
		"A+B":                   "alphanumericOnly",
		"Ctrl+Option+Cmd+Space": "tooManyKeys",
		"RightCmd+Q":            "systemReserved",
		"Space+Fn":              "alreadyInUse",
		"Fn+RightShift":         "alreadyInUse",
		"Fn+RightCmd":           "",
		"RightOption":           "",
	} {
		if got := ValidateShortcut(combo, others); got != want {
			t.Errorf("%s: got %q want %q", combo, got, want)
		}
	}
	if NormalizedCombo("RightShift+Fn") != "Fn+Shift" {
		t.Fatal(NormalizedCombo("RightShift+Fn"))
	}
}

func TestOnboarding(t *testing.T) {
	h, a := newHub(t, func(s *model.Settings) { s.OnboardingCompleted = false })
	tt := ui.NewTester(h.View, 1080, 760)
	tt.SetDark(true)
	if !tt.HasText("欢迎使用 Hoshiro") {
		t.Fatal(tt.Texts())
	}
	shot(t, tt, "onboarding-welcome")
	for _, next := range []string{"开始设置", "下一步", "继续", "下一步", "下一步"} {
		if err := tt.Click(next); err != nil {
			t.Fatalf("%s: %v (%q)", next, err, tt.Texts())
		}
		tt.Frame()
	}
	if !tt.HasText("一切就绪") {
		t.Fatal(tt.Texts())
	}
	shot(t, tt, "onboarding-done")
	if err := tt.Click("开始使用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.Settings().OnboardingCompleted || !tt.HasText("说话，不要打字") {
		t.Fatal("onboarding not completed")
	}
}
