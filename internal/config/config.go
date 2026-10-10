// Package config persists settings as JSON at <data dir>/settings.json
// (0600, atomic writes, defaults for missing fields), with API keys in the
// credential store.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/secrets"
)

var (
	vaultOnce sync.Once
	vault     *secrets.Vault
)

func systemVault() *secrets.Vault {
	vaultOnce.Do(func() { vault = secrets.NewVault(secrets.System(), secrets.Legacy()) })
	return vault
}

var RetentionValues = []string{"never", "24h", "1w", "1m", "1y", "forever"}

func Path(dataDir string) string { return filepath.Join(dataDir, "settings.json") }

// Load never fails: a corrupt file is moved aside and defaults are used.
func Load(dataDir string) model.Settings { return LoadWith(dataDir, systemVault()) }

// LoadWith overlays keychain secrets and migrates plaintext secrets still in
// settings.json into the keychain.
func LoadWith(dataDir string, v *secrets.Vault) model.Settings {
	s := loadFile(dataDir)
	fromFile := secrets.Extract(s)
	secrets.Overlay(&s, v.Stored())
	if len(fromFile) > 0 {
		if v.Available() {
			log.Printf("migrating %d plaintext secret(s) from settings.json to the credential store", len(fromFile))
			if err := SaveWith(dataDir, s, v); err != nil {
				log.Printf("secret migration failed: %v", err)
			}
		} else {
			log.Printf("credential store unavailable; %d secret(s) stay in settings.json", len(fromFile))
		}
	}
	return s
}

func loadFile(dataDir string) model.Settings {
	p := Path(dataDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("cannot read %s: %v", p, err)
		}
		return model.DefaultSettings()
	}
	s, err := Parse(b)
	if err != nil {
		log.Printf("settings.json is unreadable (%v); backing it up and using defaults", err)
		_ = os.Rename(p, filepath.Join(dataDir, "settings.json.bak"))
		return model.DefaultSettings()
	}
	Sanitize(&s)
	return s
}

// Parse reads a possibly partial settings document: missing fields take
// their defaults and fields of a wrong type are skipped instead of
// invalidating the whole file.
func Parse(b []byte) (model.Settings, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return model.Settings{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if root == nil {
		return model.Settings{}, errors.New("settings root is not an object")
	}
	s := model.DefaultSettings()
	if err := json.Unmarshal(b, &s); err == nil {
		return s, nil
	}
	// Field-by-field recovery: apply each top-level key on its own.
	s = model.DefaultSettings()
	for k, raw := range root {
		cand := s
		one, _ := json.Marshal(map[string]json.RawMessage{k: raw})
		if err := json.Unmarshal(one, &cand); err != nil {
			log.Printf("ignoring invalid settings field '%s'", k)
			continue
		}
		s = cand
	}
	return s, nil
}

func trimAll(v []string) []string {
	out := v[:0:0]
	seen := map[string]bool{}
	for _, x := range v {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// Sanitize repairs out-of-range values.
func Sanitize(s *model.Settings) {
	d := model.DefaultSettings()
	s.Shortcuts.Dictation = trimAll(s.Shortcuts.Dictation)
	s.Shortcuts.AskAnything = trimAll(s.Shortcuts.AskAnything)
	s.Shortcuts.Translation = trimAll(s.Shortcuts.Translation)
	s.Shortcuts.PasteLastTranscript = trimAll(s.Shortcuts.PasteLastTranscript)
	if len(s.Shortcuts.Dictation) == 0 {
		s.Shortcuts.Dictation = d.Shortcuts.Dictation
	}
	if s.SelectedLanguages = trimAll(s.SelectedLanguages); len(s.SelectedLanguages) == 0 {
		s.SelectedLanguages = d.SelectedLanguages
	}
	s.TranslationTargets = trimAll(s.TranslationTargets)
	if len(s.TranslationTargets) > 3 {
		s.TranslationTargets = s.TranslationTargets[:3]
	}
	if len(s.TranslationTargets) == 0 {
		s.TranslationTargets = d.TranslationTargets
	}
	if !slices.Contains(RetentionValues, s.HistoryRetention) {
		s.HistoryRetention = d.HistoryRetention
	}
	if !slices.Contains([]string{"system", "light", "dark"}, s.Appearance) {
		s.Appearance = "system"
	}
	if !slices.Contains([]string{"zh-CN", "en-US"}, s.InterfaceLanguage) {
		s.InterfaceLanguage = "zh-CN"
	}
	if id := s.MicrophoneDeviceID; id != nil && (strings.TrimSpace(*id) == "" || *id == "default" || *id == "auto") {
		s.MicrophoneDeviceID = nil
	}
	if !slices.Contains([]string{"mimo", "cloudflare", "azure", "google"}, s.Asr.Provider) {
		s.Asr.Provider = "mimo"
	}
	for _, p := range []*string{&s.Asr.Cloudflare.AccountID, &s.Asr.Cloudflare.APIKey, &s.Asr.Cloudflare.Model,
		&s.Asr.Azure.APIKey, &s.Asr.Azure.Region, &s.Asr.Google.APIKey, &s.Asr.Google.ServiceAccountJSON,
		&s.Asr.Google.ProjectID, &s.Asr.Google.Location, &s.Asr.Google.Model, &s.Asr.Mimo.APIKey, &s.Asr.Mimo.Model} {
		*p = strings.TrimSpace(*p)
	}
	s.Asr.Mimo.BaseURL = strings.TrimRight(strings.TrimSpace(s.Asr.Mimo.BaseURL), "/")
	if s.Asr.Mimo.BaseURL == "" {
		s.Asr.Mimo.BaseURL = d.Asr.Mimo.BaseURL
	}
	if s.Asr.Mimo.Model == "" {
		s.Asr.Mimo.Model = d.Asr.Mimo.Model
	}
	for _, p := range []model.LlmProvider{model.LlmGemini, model.LlmAnthropic, model.LlmOpenAI} {
		ep, def := s.Llm.Endpoint(p), d.Llm.Endpoint(p)
		ep.APIKey = strings.TrimSpace(ep.APIKey)
		ep.BaseURL = strings.TrimRight(strings.TrimSpace(ep.BaseURL), "/")
		ep.Model = strings.TrimSpace(ep.Model)
		if ep.Model == "" {
			ep.Model = def.Model
		}
	}
	if !slices.Contains([]model.LlmProvider{model.LlmGemini, model.LlmAnthropic, model.LlmOpenAI}, s.Llm.Provider) {
		s.Llm.Provider = model.LlmGemini
	}
	if s.Llm.TimeoutSecs == 0 {
		s.Llm.TimeoutSecs = d.Llm.TimeoutSecs
	}
	s.Llm.TimeoutSecs = min(max(s.Llm.TimeoutSecs, 3), 120)
}

func Save(dataDir string, s model.Settings) error { return SaveWith(dataDir, s, systemVault()) }

// SaveWith writes secrets to the credential store and the rest to the file.
// If the store cannot be verified to hold the secrets they stay in the file.
func SaveWith(dataDir string, s model.Settings, v *secrets.Vault) error {
	if v.Sync(secrets.Extract(s)) {
		return writeFile(dataDir, secrets.PublicCopy(s))
	}
	return writeFile(dataDir, s)
}

func writeFile(dataDir string, s model.Settings) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dataDir, "settings.json.tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open temp settings file: %w", err)
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, Path(dataDir)); err != nil {
		return fmt.Errorf("replace settings file: %w", err)
	}
	return nil
}

var languageNames = map[string]string{
	"zh-CN": "简体中文", "zh-TW": "繁體中文", "zh-HK": "粵語",
	"en-US": "English", "en-GB": "English", "en-AU": "English", "en-CA": "English", "en": "English",
	"ja-JP": "日本語", "ja": "日本語", "ko-KR": "한국어", "ko": "한국어",
	"fr-FR": "Français", "fr": "Français", "de-DE": "Deutsch", "de": "Deutsch",
	"es-ES": "Español", "es-MX": "Español", "es": "Español",
	"pt-BR": "Português", "pt-PT": "Português", "pt": "Português",
	"ru-RU": "Русский", "ru": "Русский", "it-IT": "Italiano", "it": "Italiano",
	"vi-VN": "Tiếng Việt", "vi": "Tiếng Việt", "th-TH": "ไทย", "th": "ไทย",
	"id-ID": "Bahasa Indonesia", "id": "Bahasa Indonesia", "ar-SA": "العربية", "ar": "العربية",
	"hi-IN": "हिन्दी", "hi": "हिन्दी", "tr-TR": "Türkçe", "tr": "Türkçe",
	"nl-NL": "Nederlands", "nl": "Nederlands", "pl-PL": "Polski", "pl": "Polski",
	"uk-UA": "Українська", "uk": "Українська",
}

func LanguageLabel(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return code
}

// RetentionSecs returns how long history is kept; ok=false means forever, 0 means never store.
func RetentionSecs(r string) (secs int64, ok bool) {
	switch r {
	case "never":
		return 0, true
	case "24h":
		return 86_400, true
	case "1w":
		return 7 * 86_400, true
	case "1m":
		return 30 * 86_400, true
	case "1y":
		return 365 * 86_400, true
	}
	return 0, false
}
