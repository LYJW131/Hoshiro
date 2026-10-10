package hub

import (
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
