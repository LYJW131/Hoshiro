package importer

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/store"
)

func fixture() map[string]any {
	var v map[string]any
	json.Unmarshal([]byte(`{"featureShortcutBindings":{"dictationMode":["Fn","Help","RightOption"],"askAnythingMode":["Fn+Space"],
	"translationMode":["Fn+LeftShift"],"pasteLastTranscript":["Ctrl+Cmd+V"]},"selectedLanguages":["zh-CN"],"autoSelectLanguages":false,
	"launchAtSystemStartup":true,"showFlowBarOnDesktop":false,"enableInteractionSoundEffects":true,"enableShowAppInDock":true,
	"historyDurationSeconds":86400,"enabledMuteBackgroundAudio":true,"translationModeTargetLanguageCodes":["en-US"],"appearancePreference":"system"}`), &v)
	return v
}

func TestMapSettings(t *testing.T) {
	base := model.DefaultSettings()
	base.Asr.Mimo.APIKey = "keep-me"
	base.ShowInDock = false
	s, msgs := MapSettings(fixture(), base)
	if len(msgs) != 0 || !slices.Equal(s.Shortcuts.Dictation, []string{"Fn", "Help", "RightOption"}) || s.Shortcuts.AskAnything[0] != "Fn+Space" ||
		s.Shortcuts.Translation[0] != "Fn+LeftShift" || s.Shortcuts.PasteLastTranscript[0] != "Ctrl+Cmd+V" || s.AutoSelectLanguages ||
		!s.LaunchAtLogin || s.ShowBarOnDesktop || !s.ShowInDock || s.HistoryRetention != "24h" || s.Asr.Mimo.APIKey != "keep-me" {
		t.Fatalf("%+v", s)
	}
	base.ShowInDock = false
	s, _ = MapSettings(map[string]any{"appearancePreference": "dark"}, base)
	if s.Appearance != "dark" || s.ShowInDock || s.HistoryRetention != base.HistoryRetention {
		t.Fatal()
	}
	var v map[string]any
	json.Unmarshal([]byte(`{"featureShortcutBindings":{"dictationMode":["Fn","Wat"],"askAnythingMode":[]}}`), &v)
	s, msgs = MapSettings(v, model.DefaultSettings())
	if !slices.Equal(s.Shortcuts.Dictation, []string{"Fn"}) || len(s.Shortcuts.AskAnything) != 0 || len(msgs) != 1 {
		t.Fatal(s.Shortcuts, msgs)
	}
}

func TestMappings(t *testing.T) {
	for in, want := range map[float64]string{86400: "24h", 604800: "1w", 2592000: "1m", 31536000: "1y", -1: "forever", 0: "never"} {
		if RetentionFromSeconds(in, true) != want {
			t.Error(in)
		}
	}
	if RetentionFromSeconds(nil, true) != "forever" || RetentionFromSeconds(nil, false) != "forever" {
		t.Error("null")
	}
	for in, want := range map[string]string{"fn+space": "Fn+Space", "Control+Command+v": "Ctrl+Cmd+V", "RightAlt": "RightOption", "KeyA": "A", "f13": "F13"} {
		if got, _ := NormalizeShortcut(in); got != want {
			t.Error(in, got)
		}
	}
	if _, ok := NormalizeShortcut("Fn+Bogus"); ok {
		t.Error()
	}
	if MapMode("voice_translation") != model.ModeTranslation || MapMode("voice_command") != model.ModeAsk || MapMode("") != model.ModeDictation {
		t.Error()
	}
	if MapStatus("completed", true) != "completed" || MapStatus("completed", false) != "empty" || MapStatus("dismissed", false) != "dismissed" || MapStatus("failed", false) != "error" {
		t.Error()
	}
}

func TestImportFixtureDB(t *testing.T) {
	src := t.TempDir()
	c, err := sql.Open("sqlite", filepath.Join(src, "typeless.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE history_v2 (id text PRIMARY KEY, status text, mode text, refined_text text, duration real, created_at text, mode_meta blob)`,
		`CREATE TABLE history (id text PRIMARY KEY, status text, mode text, refined_text text, duration real, created_at text, mode_meta blob, focused_app_name text, focused_app_bundle_id text, focused_app_window_title text)`,
		`INSERT INTO history_v2 VALUES ('a','completed','voice_transcript','你好 world',2.5,'2026-10-09T18:57:31.104Z',NULL)`,
		`INSERT INTO history_v2 VALUES ('b','completed','voice_command',NULL,1.0,'2026-10-09T19:00:00.000Z', CAST('{"ai_result":{"user_prompt":"翻译","refined_text":"Hello"}}' AS BLOB))`,
		`INSERT INTO history_v2 VALUES ('c','dismissed','voice_transcript',NULL,0.5,'2026-10-09T16:58:51.611Z',NULL)`,
		`INSERT INTO history VALUES ('a','completed','voice_transcript','x',1.0,'2026-10-09T18:57:31.104Z',NULL,'Notes','com.apple.Notes','Doc')`,
		`INSERT INTO history VALUES ('old','completed','voice_translation','Hi',1.0,'2026-01-01T00:00:00.000Z',NULL,'Mail','com.apple.mail',NULL)`,
	} {
		if _, err := c.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	if n, err := CountHistory(src); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	rows, err := ReadHistoryRows(src)
	if err != nil || len(rows) != 4 {
		t.Fatal(len(rows), err)
	}
	by := map[string]store.HistoryRow{}
	for _, r := range rows {
		by[r.ID] = r
	}
	a := by["a"]
	if *a.RefinedText != "你好 world" || *a.AppName != "Notes" || a.Words != 3 || a.Source != "typeless" || a.AudioPath != nil {
		t.Fatalf("%+v", a)
	}
	if b := by["b"]; b.Mode != model.ModeAsk || *b.RefinedText != "Hello" || *b.RawText != "翻译" {
		t.Fatal("b")
	}
	if by["c"].Status != "dismissed" || by["old"].Mode != model.ModeTranslation {
		t.Fatal("c/old")
	}
	dst := t.TempDir()
	db, err := store.Open(filepath.Join(dst, "h.db"), filepath.Join(dst, "rec"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if i, s, err := ImportHistory(src, db); i != 4 || s != 0 || err != nil {
		t.Fatal(i, s, err)
	}
	if i, s, _ := ImportHistory(src, db); i != 0 || s != 4 {
		t.Fatal(i, s)
	}
}
