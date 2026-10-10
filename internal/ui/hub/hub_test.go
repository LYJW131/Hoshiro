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
