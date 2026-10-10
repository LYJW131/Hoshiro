package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/stats"
)

func mem(t *testing.T) (*DB, string) {
	dir := t.TempDir()
	d, err := Open(":memory:", dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, dir
}

func row(id string, m model.Mode, status, text, at string) HistoryRow {
	r := NewRow(id, m, status)
	r.CreatedAt = at
	raw := "raw " + text
	r.RefinedText, r.RawText = &text, &raw
	r.Words = stats.CountWords(text)
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func q(m *model.Mode, s string) model.HistoryQuery {
	return model.HistoryQuery{Mode: m, Limit: 50, Search: s}
}

func TestHistoryRoundtripAndFilters(t *testing.T) {
	d, _ := mem(t)
	must(t, d.UpsertHistory(row("a", model.ModeDictation, "completed", "hello world", "2026-10-01T10:00:00.000Z")))
	must(t, d.UpsertHistory(row("b", model.ModeTranslation, "completed", "你好 100%", "2026-10-02T10:00:00.000Z")))
	must(t, d.UpsertHistory(row("c", model.ModeAsk, "error", "x", "2026-10-03T10:00:00.000Z")))
	all, _ := d.ListHistory(q(nil, ""))
	var ids []string
	for _, r := range all {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "c,b,a" {
		t.Fatal(ids)
	}
	tr := model.ModeTranslation
	for s, want := range map[string]int{"hello": 1, "100%": 1, "%": 1} {
		if got, _ := d.ListHistory(q(nil, s)); len(got) != want {
			t.Fatalf("search %q: %d", s, len(got))
		}
	}
	if got, _ := d.ListHistory(q(&tr, "")); len(got) != 1 {
		t.Fatal("mode filter")
	}
	if s, ok, _ := d.LastCompletedText(); !ok || s != "你好 100%" {
		t.Fatal(s)
	}
	g, _ := d.GetHistory("a")
	if g.Mode != model.ModeDictation || g.Words != 2 {
		t.Fatal(g)
	}
	if ok, _ := d.DeleteHistory("a"); !ok {
		t.Fatal("delete")
	}
	if ok, _ := d.DeleteHistory("a"); ok {
		t.Fatal("double delete")
	}
	if n, _ := d.CountHistory(); n != 2 {
		t.Fatal(n)
	}
	d.DeleteAllHistory()
	if n, _ := d.CountHistory(); n != 0 {
		t.Fatal(n)
	}
}

func TestPurgeRemovesOldRowsAndAudio(t *testing.T) {
	d, dir := mem(t)
	old := row("old", model.ModeDictation, "completed", "old", "2020-01-01T00:00:00.000Z")
	a := "old.wav"
	old.AudioPath = &a
	os.WriteFile(filepath.Join(dir, a), []byte("x"), 0o644)
	must(t, d.UpsertHistory(old))
	must(t, d.UpsertHistory(row("fresh", model.ModeDictation, "completed", "fresh", Now())))
	if !d.ToItem(old).HasAudio {
		t.Fatal("audio")
	}
	n, err := d.PurgeOlderThan(time.Now().AddDate(0, 0, -30))
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, a)); !os.IsNotExist(err) {
		t.Fatal("audio kept")
	}
}

func TestStaleProcessing(t *testing.T) {
	d, _ := mem(t)
	must(t, d.UpsertHistory(row("p", model.ModeDictation, "processing", "", Now())))
	if n, _ := d.FailStaleProcessing(); n != 1 {
		t.Fatal(n)
	}
	if g, _ := d.GetHistory("p"); g.Status != "error" {
		t.Fatal(g.Status)
	}
}

func TestDictionaryCaseInsensitive(t *testing.T) {
	d, _ := mem(t)
	added, err := d.AddWords([]string{"Typeless", "typeless", "MiMo"}, "manual")
	if err != nil || len(added) != 2 {
		t.Fatal(added, err)
	}
	if again, _ := d.AddWords([]string{"TYPELESS"}, "auto"); len(again) != 0 {
		t.Fatal("dup")
	}
	id := added[0].ID
	if u, _ := d.UpdateWord(id, "Hoshiro"); u.Word != "Hoshiro" {
		t.Fatal(u)
	}
	if _, err := d.UpdateWord(id, "mimo"); err == nil {
		t.Fatal("unique violated")
	}
	if u, err := d.UpdateWord("nope", "x"); u != nil || err != nil {
		t.Fatal("missing")
	}
	if n, _ := d.DeleteWords([]string{id}); n != 1 {
		t.Fatal(n)
	}
	if w, _ := d.WordsForPrompt(10); len(w) != 1 || w[0] != "MiMo" {
		t.Fatal(w)
	}
}

func TestImportSkipsExisting(t *testing.T) {
	d, _ := mem(t)
	must(t, d.UpsertHistory(row("a", model.ModeDictation, "completed", "one", "2026-10-01T10:00:00.000Z")))
	b := row("b", model.ModeDictation, "completed", "two", "2026-10-02T10:00:00.000Z")
	b.Source = "typeless"
	i, s, err := d.ImportRows([]HistoryRow{row("a", model.ModeDictation, "completed", "dup", "2026-10-01T10:00:00.000Z"), b})
	if err != nil || i != 1 || s != 1 {
		t.Fatal(i, s, err)
	}
	if g, _ := d.GetHistory("a"); *g.RefinedText != "one" {
		t.Fatal("overwritten")
	}
	if r, _ := d.StatRows(); len(r) != 2 {
		t.Fatal(len(r))
	}
}

func TestProfileRollbackAndLearning(t *testing.T) {
	d, _ := mem(t)
	must(t, d.UpsertHistory(row("learn", model.ModeDictation, "completed", "hello", Now())))
	d.db.Exec("CREATE TRIGGER reject_word BEFORE INSERT ON dictionary BEGIN SELECT RAISE(ABORT, 'test failure'); END;")
	p := model.PersonalProfile{Vocabulary: []string{"Hoshiro"}}
	if d.SaveProfile(p, []string{"learn"}) == nil {
		t.Fatal("expected failure")
	}
	if pr, _ := d.Profile(); len(pr.Vocabulary) != 0 || pr.PendingSessions != 1 {
		t.Fatal(pr)
	}
	d.db.Exec("DROP TRIGGER reject_word")
	must(t, d.SaveProfile(p, []string{"learn"}))
	if pr, _ := d.Profile(); pr.PendingSessions != 0 {
		t.Fatal(pr)
	}
	if w, _ := d.WordsForPrompt(10); len(w) != 1 || w[0] != "Hoshiro" {
		t.Fatal(w)
	}
}

func TestEditsFollowHistory(t *testing.T) {
	d, _ := mem(t)
	h := row("learn", model.ModeDictation, "completed", "请使用 Hoshiro 输入文字", Now())
	must(t, d.UpsertHistory(h))
	app := "Notes"
	must(t, d.SaveEdit(h.ID, "请使用 Hoshira 输入文字", "请使用 Hoshiro 输入文字", &app))
	must(t, d.SaveProfile(model.PersonalProfile{AnalyzedSessions: 1}, []string{h.ID}))
	s, _ := d.LearningSamples()
	if s[0].Data["user_final"] != "请使用 Hoshiro 输入文字" {
		t.Fatal(s)
	}
	d.DeleteHistory(h.ID)
	if s, _ := d.LearningSamples(); len(s) != 0 {
		t.Fatal("samples remain")
	}
	must(t, d.SaveEdit(h.ID, "old", "late", nil))
	var n int
	d.db.QueryRow("SELECT COUNT(*) FROM edits").Scan(&n)
	if n != 0 {
		t.Fatal("orphan edit")
	}
}

func TestLearningBudget(t *testing.T) {
	d, _ := mem(t)
	must(t, d.UpsertHistory(row("ask", model.ModeAsk, "completed", "question", Now())))
	must(t, d.UpsertHistory(row("large", model.ModeDictation, "completed", strings.Repeat("字", 40_000), Now())))
	must(t, d.UpsertHistory(row("small", model.ModeDictation, "completed", "你好", Now())))
	s, _ := d.LearningSamples()
	if len(s) != 1 || s[0].ID != "small" {
		t.Fatal(s)
	}
	if p, _ := d.Profile(); p.PendingSessions != 2 {
		t.Fatal(p.PendingSessions)
	}
}

func u(v uint64) *uint64 { return &v }

func TestUsageCounts(t *testing.T) {
	d, _ := mem(t)
	h := row("one", model.ModeDictation, "completed", "你好世界", Now())
	h.DurationSecs = 12
	must(t, d.UpsertHistory(h))
	must(t, d.UpsertHistory(h))
	must(t, d.RecordUsage("MiMo", "asr", nil, nil, nil, 12))
	must(t, d.RecordUsage("Gemini", "flash", u(10), u(20), nil, 0))
	must(t, d.RecordUsage("Gemini", "flash", u(30), u(5), nil, 0))
	s, err := d.UsageStats(7)
	must(t, err)
	if s.CompletedSessions != 1 || s.Total.Requests != 3 || s.Total.InputTokens != 40 || s.Total.OutputTokens != 25 ||
		s.Total.UnreportedRequests != 0 || s.AsrTotal.Requests != 1 || s.LlmTotal.Requests != 2 ||
		len(s.AsrModels) != 1 || len(s.LlmModels) != 1 || s.LlmModels[0].InputTokens != 40 || s.Total.AudioSecs != 12 {
		t.Fatalf("%+v", s)
	}
	d.DeleteAllHistory()
	s, _ = d.UsageStats(0)
	if s.Total.Sessions != 0 || s.Total.Requests != 3 {
		t.Fatal(s.Total)
	}
	must(t, d.RecordUsage("Gemini", "flash", nil, nil, nil, 0))
	must(t, d.RecordUsage("Anthropic", "haiku", u(100), u(10), u(80), 0))
	s, _ = d.UsageStats(0)
	if s.LlmTotal.UnreportedRequests != 1 || s.LlmTotal.CachedTokens != 80 || s.AsrTotal.UnreportedRequests != 0 {
		t.Fatalf("%+v", s.LlmTotal)
	}
}

func TestSessionsLatencyAppsOutcomes(t *testing.T) {
	d, _ := mem(t)
	old := ts(time.Now().AddDate(0, 0, -20))
	mk := func(id, status, at string, total *uint32) HistoryRow {
		r := row(id, model.ModeDictation, status, "你好世界", at)
		r.DurationSecs, r.TotalMs = 10, total
		if total != nil {
			a := *total / 2
			p := "MiMo · asr"
			r.AsrMs, r.AsrProvider = &a, &p
		}
		return r
	}
	p := func(v uint32) *uint32 { return &v }
	for i, v := range []uint32{1000, 2000, 3000, 4000, 10000} {
		must(t, d.UpsertHistory(mk(fmt.Sprintf("c%d", i), "completed", Now(), p(v))))
	}
	must(t, d.UpsertHistory(mk("old", "completed", old, p(500))))
	must(t, d.UpsertHistory(mk("legacy", "completed", Now(), nil)))
	must(t, d.UpsertHistory(mk("x", "error", Now(), nil)))
	must(t, d.UpsertHistory(mk("d", "dismissed", Now(), nil)))
	must(t, d.UpsertHistory(mk("e", "empty", Now(), nil)))
	tl := mk("t", "completed", Now(), nil)
	tl.Source = "typeless"
	must(t, d.UpsertHistory(tl))
	s, err := d.UsageStats(7)
	must(t, err)
	if s.Outcomes.Completed != 7 || s.Outcomes.Error != 1 || s.Outcomes.Dismissed != 1 || s.Outcomes.Empty != 1 {
		t.Fatalf("%+v", s.Outcomes)
	}
	if s.Latency.Total.Samples != 5 || s.Latency.Total.MedianMs != 3000 || s.Latency.Total.P90Ms != 7600 || s.Latency.Llm.Samples != 0 {
		t.Fatalf("%+v", s.Latency)
	}
	labels := map[string]bool{}
	for _, a := range s.Apps {
		labels[a.Label] = true
	}
	if !labels["Typeless 导入"] || !labels["未知应用"] {
		t.Fatal(labels)
	}
	var h, w uint64
	for _, x := range s.Hours {
		h += x
	}
	for _, x := range s.Weekdays {
		w += x
	}
	if h != 7 || w != 7 || s.LongestSessionSecs != 10 {
		t.Fatal(h, w)
	}
	if all, _ := d.UsageStats(0); all.Latency.Total.Samples != 6 {
		t.Fatal(all.Latency.Total.Samples)
	}
}

func rawDB(t *testing.T, setup string) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	must(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec(setup)
	must(t, err)
	return db
}

func TestUpgradesV1AndV2(t *testing.T) {
	v1 := rawDB(t, "CREATE TABLE history(id TEXT PRIMARY KEY); INSERT INTO history VALUES('old'); PRAGMA user_version=1;")
	must(t, migrate(v1))
	must(t, migrate(v1))
	var n, v int
	v1.QueryRow("SELECT COUNT(*) FROM history").Scan(&n)
	v1.QueryRow("PRAGMA user_version").Scan(&v)
	if n != 1 || v != 3 {
		t.Fatal(n, v)
	}
	v2 := rawDB(t, `CREATE TABLE history(id TEXT PRIMARY KEY, created_at TEXT NOT NULL, mode TEXT NOT NULL, status TEXT NOT NULL, raw_text TEXT, refined_text TEXT, selected_text TEXT, duration_secs REAL NOT NULL DEFAULT 0, app_name TEXT, bundle_id TEXT, window_title TEXT, error_message TEXT, audio_path TEXT, source TEXT NOT NULL DEFAULT 'hoshiro', words INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE edits (history_id TEXT PRIMARY KEY, inserted TEXT NOT NULL, final_text TEXT NOT NULL, app_name TEXT, created_at TEXT NOT NULL);
		CREATE TABLE usage (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, input_tokens INTEGER, output_tokens INTEGER, audio_secs REAL NOT NULL DEFAULT 0);
		INSERT INTO history(id,created_at,mode,status,words) VALUES('old','2026-01-01T00:00:00.000Z','dictation','completed',7);
		INSERT INTO usage(created_at,provider,model,input_tokens,output_tokens) VALUES('2026-01-01T00:00:00.000Z','Gemini','m',1,2);
		PRAGMA user_version=2;`)
	must(t, migrate(v2))
	must(t, migrate(v2))
	var w int
	var tm sql.NullInt64
	v2.QueryRow("SELECT words,total_ms FROM history WHERE id='old'").Scan(&w, &tm)
	if w != 7 || tm.Valid {
		t.Fatal(w, tm)
	}
	_, err := v2.Exec("UPDATE usage SET cached_tokens=3")
	must(t, err)
}

func TestOnDiskReopen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.db")
	d, err := Open(p, filepath.Join(dir, "rec"))
	must(t, err)
	must(t, d.UpsertHistory(row("a", model.ModeDictation, "completed", "x", Now())))
	d.Close()
	d, err = Open(p, filepath.Join(dir, "rec"))
	must(t, err)
	defer d.Close()
	if n, _ := d.CountHistory(); n != 1 {
		t.Fatal(n)
	}
}

func TestNormalizeTS(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-09T18:57:31.104Z":  "2026-10-09T18:57:31.104Z",
		"2026-10-09T20:57:31+02:00": "2026-10-09T18:57:31.000Z",
		"2026-10-09 18:57:31":       "2026-10-09T18:57:31.000Z",
	} {
		if got, ok := NormalizeTS(in); !ok || got != want {
			t.Errorf("%s -> %s", in, got)
		}
	}
	if _, ok := NormalizeTS("garbage"); ok {
		t.Fatal("garbage")
	}
}
