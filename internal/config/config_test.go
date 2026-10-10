package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/secrets"
)

func TestPartialFileGetsDefaults(t *testing.T) {
	s, err := Parse([]byte(`{"launchAtLogin": false, "shortcuts": {"dictation": ["Help"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.LaunchAtLogin || s.Shortcuts.Dictation[0] != "Help" || s.Shortcuts.AskAnything[0] != "Fn+Space" || s.Llm.TimeoutSecs != 12 {
		t.Fatalf("%+v", s)
	}
}

func TestWrongTypedFieldIsSkipped(t *testing.T) {
	s, err := Parse([]byte(`{"launchAtLogin": "yes", "showInDock": false, "asr": {"mimo": {"apiKey": "k"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !s.LaunchAtLogin || s.ShowInDock || s.Asr.Mimo.APIKey != "k" || s.Asr.Mimo.Model != "mimo-v2.5-asr" {
		t.Fatalf("%+v", s)
	}
}

func TestInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte(`[1]`)); err == nil {
		t.Fatal("array root accepted")
	}
	if _, err := Parse([]byte(`{`)); err == nil {
		t.Fatal("broken json accepted")
	}
}

func TestSanitize(t *testing.T) {
	s := model.DefaultSettings()
	s.Shortcuts.Dictation = nil
	s.HistoryRetention = "bogus"
	s.TranslationTargets = []string{"a", "b", "c", "d"}
	s.Asr.Mimo.APIKey = "  key\n"
	def := "default"
	s.MicrophoneDeviceID = &def
	s.Llm.TimeoutSecs = 999
	Sanitize(&s)
	if s.Shortcuts.Dictation[0] != "Fn" || s.HistoryRetention != "1m" || len(s.TranslationTargets) != 3 ||
		s.Asr.Mimo.APIKey != "key" || s.MicrophoneDeviceID != nil || s.Llm.TimeoutSecs != 120 {
		t.Fatalf("%+v", s)
	}
}

func TestSaveLoadRoundtripKeepsSecretsOutOfFile(t *testing.T) {
	dir := t.TempDir()
	v := secrets.NewVault(&secrets.MemStore{}, nil)
	s := model.DefaultSettings()
	s.Asr.Mimo.APIKey = "secret"
	if err := SaveWith(dir, s, v); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(b) == "" || contains(string(b), "secret") {
		t.Fatalf("secret leaked to file: %s", b)
	}
	st, _ := os.Stat(filepath.Join(dir, "settings.json"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if got := LoadWith(dir, v); got.Asr.Mimo.APIKey != "secret" {
		t.Fatalf("key lost: %q", got.Asr.Mimo.APIKey)
	}
}

func TestUnavailableStoreKeepsSecretsInFile(t *testing.T) {
	dir := t.TempDir()
	v := secrets.NewVault(&secrets.MemStore{Fail: true}, nil)
	s := model.DefaultSettings()
	s.Llm.OpenAI.APIKey = "sk-1"
	if err := SaveWith(dir, s, v); err != nil {
		t.Fatal(err)
	}
	if got := LoadWith(dir, v); got.Llm.OpenAI.APIKey != "sk-1" {
		t.Fatal("key lost")
	}
}

func TestPlaintextMigration(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"llm":{"gemini":{"apiKey":"g"}}}`), 0o600)
	store := &secrets.MemStore{}
	got := LoadWith(dir, secrets.NewVault(store, nil))
	if got.Llm.Gemini.APIKey != "g" || store.Blob == nil {
		t.Fatal("not migrated")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if contains(string(b), `"g"`) {
		t.Fatal("plaintext remains")
	}
}

func TestCorruptFileBackedUp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{nope`), 0o600)
	s := LoadWith(dir, secrets.NewVault(&secrets.MemStore{}, nil))
	if s.HistoryRetention != "1m" {
		t.Fatal("no defaults")
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.bak")); err != nil {
		t.Fatal("no backup")
	}
}

func contains(s, sub string) bool { return len(sub) > 0 && len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
