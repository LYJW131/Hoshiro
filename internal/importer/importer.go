// Package importer migrates from Typeless: settings from app-settings.json,
// history from typeless.db.
package importer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/stats"
	"github.com/IcyBlue17/hoshiro/internal/store"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const BundleID = "now.typeless.desktop"

// Dir is Typeless' data directory (~/Library/Application Support/Typeless).
func Dir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "Typeless")
}

func Installed() bool {
	if _, err := os.Stat("/Applications/Typeless.app"); err == nil {
		return true
	}
	h, _ := os.UserHomeDir()
	_, err := os.Stat(filepath.Join(h, "Applications/Typeless.app"))
	return h != "" && err == nil
}

// ── settings ──

var keyNames = []string{"Fn", "Help", "Esc", "Space", "Tab", "Enter", "Backspace", "LeftShift", "RightShift", "Shift", "LeftCtrl",
	"RightCtrl", "Ctrl", "LeftOption", "RightOption", "Option", "LeftCmd", "RightCmd", "Cmd", "Up", "Down", "Left",
	"Right", "Mouse3", "Mouse4", "Mouse5"}

var keyAliases = map[string]string{
	"control": "Ctrl", "leftcontrol": "LeftCtrl", "controlleft": "LeftCtrl", "lctrl": "LeftCtrl",
	"rightcontrol": "RightCtrl", "controlright": "RightCtrl", "rctrl": "RightCtrl",
	"alt": "Option", "opt": "Option",
	"leftalt": "LeftOption", "altleft": "LeftOption", "leftopt": "LeftOption", "lalt": "LeftOption", "loption": "LeftOption",
	"rightalt": "RightOption", "altright": "RightOption", "rightopt": "RightOption", "ralt": "RightOption", "roption": "RightOption",
	"command": "Cmd", "meta": "Cmd", "super": "Cmd", "win": "Cmd", "windows": "Cmd",
	"leftcommand": "LeftCmd", "leftmeta": "LeftCmd", "metaleft": "LeftCmd", "lcmd": "LeftCmd", "leftsuper": "LeftCmd",
	"rightcommand": "RightCmd", "rightmeta": "RightCmd", "metaright": "RightCmd", "rcmd": "RightCmd", "rightsuper": "RightCmd",
	"lshift": "LeftShift", "shiftleft": "LeftShift", "rshift": "RightShift", "shiftright": "RightShift",
	"return": "Enter", "escape": "Esc", "arrowup": "Up", "arrowdown": "Down", "arrowleft": "Left", "arrowright": "Right",
	"globe": "Fn", "function": "Fn",
}

// NormalizeKeyName maps a Typeless key name to ours (best effort); false = unknown.
func NormalizeKeyName(name string) (string, bool) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", false
	}
	for _, k := range keyNames {
		if strings.EqualFold(k, n) {
			return k, true
		}
	}
	lower := strings.ToLower(n)
	if m, ok := keyAliases[lower]; ok {
		return m, true
	}
	stripped := lower
	if s, ok := strings.CutPrefix(lower, "key"); ok {
		stripped = s
	} else if s, ok := strings.CutPrefix(lower, "digit"); ok {
		stripped = s
	}
	if r := []rune(stripped); len(r) == 1 && r[0] < 128 && (r[0] >= 'a' && r[0] <= 'z' || r[0] >= '0' && r[0] <= '9') {
		return strings.ToUpper(stripped), true
	}
	if rest, ok := strings.CutPrefix(lower, "f"); ok {
		if num, err := strconv.ParseUint(rest, 10, 8); err == nil && num >= 1 && num <= 20 {
			return fmt.Sprintf("F%d", num), true
		}
	}
	return "", false
}

func NormalizeShortcut(s string) (string, bool) {
	var parts []string
	for _, p := range strings.Split(s, "+") {
		k, ok := NormalizeKeyName(p)
		if !ok {
			return "", false
		}
		parts = append(parts, k)
	}
	return strings.Join(parts, "+"), len(parts) > 0
}

func mapBindings(v any, label string, msgs *[]string) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := []string{}
	for _, it := range arr {
		s, ok := it.(string)
		if !ok {
			continue
		}
		if n, ok := NormalizeShortcut(s); ok {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		} else {
			*msgs = append(*msgs, fmt.Sprintf("%s快捷键 “%s” 无法识别，已跳过", label, s))
		}
	}
	return out, true
}

// RetentionFromSeconds maps Typeless' historyDurationSeconds; present=false means the key is missing.
func RetentionFromSeconds(v any, present bool) string {
	if !present || v == nil {
		return "forever"
	}
	secs, ok := v.(float64)
	if !ok {
		secs = -1
	}
	switch {
	case secs < 0:
		return "forever"
	case secs == 0:
		return "never"
	case secs <= 86_400:
		return "24h"
	case secs <= 604_800:
		return "1w"
	case secs <= 2_592_000:
		return "1m"
	case secs <= 31_536_000:
		return "1y"
	}
	return "forever"
}

// ShortcutsFromJSON parses only the bindings (detection).
func ShortcutsFromJSON(v map[string]any) *model.ShortcutBindings {
	b, ok := v["featureShortcutBindings"].(map[string]any)
	if !ok {
		return nil
	}
	var msgs []string
	def := model.DefaultShortcuts()
	out := def
	if d, ok := mapBindings(b["dictationMode"], "口述", &msgs); ok && len(d) > 0 {
		out.Dictation = d
	}
	if x, ok := mapBindings(b["askAnythingMode"], "随便问", &msgs); ok {
		out.AskAnything = x
	}
	if x, ok := mapBindings(b["translationMode"], "翻译", &msgs); ok {
		out.Translation = x
	}
	if x, ok := mapBindings(b["pasteLastTranscript"], "粘贴上一次转录", &msgs); ok {
		out.PasteLastTranscript = x
	}
	return &out
}

// MapSettings overlays Typeless' settings onto base. Only fields present in
// the file are touched; API keys and providers never are.
func MapSettings(v map[string]any, base model.Settings) (model.Settings, []string) {
	s := base
	s.Shortcuts.Dictation = slices.Clone(base.Shortcuts.Dictation)
	msgs := []string{}
	if b, ok := v["featureShortcutBindings"].(map[string]any); ok {
		if d, ok := mapBindings(b["dictationMode"], "口述", &msgs); ok && len(d) > 0 {
			s.Shortcuts.Dictation = d
		}
		if x, ok := mapBindings(b["askAnythingMode"], "随便问", &msgs); ok {
			s.Shortcuts.AskAnything = x
		}
		if x, ok := mapBindings(b["translationMode"], "翻译", &msgs); ok {
			s.Shortcuts.Translation = x
		}
		if x, ok := mapBindings(b["pasteLastTranscript"], "粘贴上一次转录", &msgs); ok {
			s.Shortcuts.PasteLastTranscript = x
		}
	}
	strList := func(key string) []string {
		arr, _ := v[key].([]any)
		var l []string
		for _, x := range arr {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				l = append(l, strings.TrimSpace(s))
			}
		}
		return l
	}
	if l := strList("selectedLanguages"); len(l) > 0 {
		s.SelectedLanguages = l
	}
	if l := strList("translationModeTargetLanguageCodes"); len(l) > 0 {
		s.TranslationTargets = l[:min(3, len(l))]
	}
	flag := func(key string, dst *bool) {
		if b, ok := v[key].(bool); ok {
			*dst = b
		}
	}
	flag("autoSelectLanguages", &s.AutoSelectLanguages)
	flag("launchAtSystemStartup", &s.LaunchAtLogin)
	flag("showFlowBarOnDesktop", &s.ShowBarOnDesktop)
	flag("enableInteractionSoundEffects", &s.InteractionSounds)
	flag("enableShowAppInDock", &s.ShowInDock)
	flag("enabledMuteBackgroundAudio", &s.MuteWhileDictating)
	if a, ok := v["appearancePreference"].(string); ok && slices.Contains([]string{"system", "light", "dark"}, a) {
		s.Appearance = a
	}
	if x, ok := v["historyDurationSeconds"]; ok {
		s.HistoryRetention = RetentionFromSeconds(x, true)
	}
	return s, msgs
}

// ── history ──

func MapMode(m string) model.Mode {
	switch m {
	case "voice_translation":
		return model.ModeTranslation
	case "voice_command", "ask", "voice_ask":
		return model.ModeAsk
	}
	return model.ModeDictation
}

func MapStatus(status string, hasText bool) string {
	switch strings.ToLower(status) {
	case "dismissed", "cancelled", "canceled", "aborted":
		return "dismissed"
	case "failed", "error":
		return "error"
	case "empty", "no_speech", "silent":
		return "empty"
	}
	if hasText {
		return "completed"
	}
	return "empty"
}

type legacyInfo struct{ app, bundle, title sql.NullString }

func ptr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func buildRow(id string, status, mode, refinedCol sql.NullString, dur sql.NullFloat64, created, meta sql.NullString, legacy *legacyInfo) store.HistoryRow {
	var ai map[string]any
	if meta.Valid {
		var m map[string]any
		if json.Unmarshal([]byte(meta.String), &m) == nil {
			ai, _ = m["ai_result"].(map[string]any)
		}
	}
	str := func(k string) string { s, _ := ai[k].(string); return s }
	var refined *string
	if refinedCol.Valid && strings.TrimSpace(refinedCol.String) != "" {
		refined = &refinedCol.String
	} else if t := str("refined_text"); strings.TrimSpace(t) != "" {
		refined = &t
	}
	md := MapMode(mode.String)
	r := store.NewRow(id, md, MapStatus(status.String, refined != nil))
	if created.Valid {
		if ts, ok := store.NormalizeTS(created.String); ok {
			r.CreatedAt = ts
		}
	}
	if dur.Valid && dur.Float64 >= 0 && dur.Float64 < 1e12 {
		r.DurationSecs = dur.Float64
	}
	if refined != nil {
		r.Words = stats.CountWords(*refined)
	}
	r.RefinedText = refined
	if p := str("user_prompt"); md == model.ModeAsk && strings.TrimSpace(p) != "" {
		r.RawText = &p
	}
	if legacy != nil {
		r.AppName, r.BundleID, r.WindowTitle = ptr(legacy.app), ptr(legacy.bundle), ptr(legacy.title)
	}
	r.Source = "typeless"
	return r
}

// openCopy copies typeless.db (+ WAL/SHM) to a temp dir and opens the copy read-only.
func openCopy(dir string) (*sql.DB, string, error) {
	if fi, err := os.Stat(filepath.Join(dir, "typeless.db")); err != nil || fi.IsDir() {
		return nil, "", errors.New("未找到 Typeless 的历史数据库")
	}
	tmp := filepath.Join(os.TempDir(), "hoshiro-typeless-"+uuid.NewString())
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, "", err
	}
	cp := func(name string) error {
		src, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.Create(filepath.Join(tmp, name))
		if err != nil {
			return err
		}
		defer dst.Close()
		_, err = io.Copy(dst, src)
		return err
	}
	if err := cp("typeless.db"); err != nil {
		os.RemoveAll(tmp)
		return nil, "", fmt.Errorf("复制 typeless.db 失败: %w", err)
	}
	_ = cp("typeless.db-wal")
	_ = cp("typeless.db-shm")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(tmp, "typeless.db")+"?mode=ro")
	if err != nil {
		os.RemoveAll(tmp)
		return nil, "", err
	}
	return db, tmp, nil
}

func tableExists(db *sql.DB, name string) bool {
	var one int
	return db.QueryRow("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&one) == nil
}

func CountHistory(dir string) (uint32, error) {
	db, tmp, err := openCopy(dir)
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	defer db.Close()
	if !tableExists(db, "history_v2") {
		return 0, nil
	}
	var n int64
	if err := db.QueryRow("SELECT COUNT(*) FROM history_v2").Scan(&n); err != nil {
		return 0, err
	}
	return uint32(max(n, 0)), nil
}

func ReadHistoryRows(dir string) ([]store.HistoryRow, error) {
	db, tmp, err := openCopy(dir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	defer db.Close()
	type v2 struct {
		id                                   string
		status, mode, refined, created, meta sql.NullString
		dur                                  sql.NullFloat64
	}
	var rows []v2
	ids := map[string]bool{}
	if tableExists(db, "history_v2") {
		q, err := db.Query("SELECT id, status, mode, refined_text, duration, created_at, CAST(mode_meta AS TEXT) FROM history_v2")
		if err != nil {
			return nil, err
		}
		for q.Next() {
			var r v2
			if err := q.Scan(&r.id, &r.status, &r.mode, &r.refined, &r.dur, &r.created, &r.meta); err != nil {
				q.Close()
				return nil, err
			}
			ids[r.id] = true
			rows = append(rows, r)
		}
		q.Close()
	}
	legacy := map[string]*legacyInfo{}
	var legacyOnly []store.HistoryRow
	if tableExists(db, "history") {
		q, err := db.Query(`SELECT id, status, mode, refined_text, duration, created_at, CAST(mode_meta AS TEXT),
			focused_app_name, focused_app_bundle_id, focused_app_window_title FROM history`)
		if err == nil {
			for q.Next() {
				var r v2
				var li legacyInfo
				if q.Scan(&r.id, &r.status, &r.mode, &r.refined, &r.dur, &r.created, &r.meta, &li.app, &li.bundle, &li.title) != nil {
					continue
				}
				if ids[r.id] {
					legacy[r.id] = &li
				} else {
					legacyOnly = append(legacyOnly, buildRow(r.id, r.status, r.mode, r.refined, r.dur, r.created, r.meta, &li))
				}
			}
			q.Close()
		}
	}
	out := make([]store.HistoryRow, 0, len(rows)+len(legacyOnly))
	for _, r := range rows {
		out = append(out, buildRow(r.id, r.status, r.mode, r.refined, r.dur, r.created, r.meta, legacy[r.id]))
	}
	return append(out, legacyOnly...), nil
}

// Detect reports what a migration would find. running tells whether Typeless runs (platform check).
func Detect(running bool) model.TypelessDetection {
	d := model.TypelessDetection{Installed: Installed(), Running: running}
	dir := Dir()
	if dir == "" {
		return d
	}
	settingsFile := filepath.Join(dir, "app-settings.json")
	_, e1 := os.Stat(settingsFile)
	fi, e2 := os.Stat(filepath.Join(dir, "typeless.db"))
	hasDB := e2 == nil && !fi.IsDir()
	d.DataFound = e1 == nil || hasDB
	if v, err := ReadSettingsJSON(dir); err == nil {
		d.Shortcuts = ShortcutsFromJSON(v)
	}
	if hasDB {
		if n, err := CountHistory(dir); err == nil {
			d.HistoryCount = n
		}
	}
	return d
}

func ReadSettingsJSON(dir string) (map[string]any, error) {
	b, err := os.ReadFile(filepath.Join(dir, "app-settings.json"))
	if err != nil {
		return nil, errors.New("无法读取 Typeless 设置文件")
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return nil, errors.New("Typeless 设置文件格式无法识别")
	}
	return v, nil
}

func ImportHistory(dir string, db *store.DB) (uint32, uint32, error) {
	rows, err := ReadHistoryRows(dir)
	if err != nil {
		return 0, 0, err
	}
	return db.ImportRows(rows)
}

func FillMessages(r *model.TypelessImportResult, o model.TypelessImportOptions) {
	if o.History {
		r.Messages = append(r.Messages, "录音文件不会被迁移，导入的历史记录仅包含文字。")
	}
	r.Messages = append(r.Messages, "Typeless 的词典保存在云端，无法直接导入；可在 Typeless 中导出 CSV 后在词典页导入。")
}
