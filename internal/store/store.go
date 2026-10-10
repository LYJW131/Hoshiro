// Package store is Hoshiro's SQLite storage: history, dictionary, edits,
// the personal profile and provider usage. It opens databases written by
// the Rust app (same schema and user_version migrations). The driver is
// modernc.org/sqlite, pure Go, so the app keeps building without cgo.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/stats"
)

const SchemaVersion = 3

type DB struct {
	mu            sync.Mutex
	db            *sql.DB
	recordingsDir string
}

type HistoryRow struct {
	ID           string
	CreatedAt    string
	Mode         model.Mode
	Status       string
	RawText      *string
	RefinedText  *string
	SelectedText *string
	DurationSecs float64
	AppName      *string
	BundleID     *string
	WindowTitle  *string
	ErrorMessage *string
	AudioPath    *string // file name inside the recordings directory
	Source       string
	Words        uint32
	AsrMs        *uint32
	LlmMs        *uint32
	TotalMs      *uint32
	AsrProvider  *string
	LlmModel     *string
}

func NewRow(id string, mode model.Mode, status string) HistoryRow {
	return HistoryRow{ID: id, CreatedAt: Now(), Mode: mode, Status: status, Source: "hoshiro"}
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func Now() string { return ts(time.Now()) }

// NormalizeTS turns RFC 3339 or "YYYY-MM-DD HH:MM:SS" into UTC RFC 3339 with millis.
func NormalizeTS(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts(t), true
	}
	for _, f := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(f, s); err == nil {
			return ts(t), true
		}
	}
	return "", false
}

const historyCols = "id, created_at, mode, status, raw_text, refined_text, selected_text, duration_secs, " +
	"app_name, bundle_id, window_title, error_message, audio_path, source, words, " +
	"asr_ms, llm_ms, total_ms, asr_provider, llm_model"

type scanner interface{ Scan(...any) error }

func u32p(v sql.NullInt64) *uint32 {
	if !v.Valid {
		return nil
	}
	x := uint32(min(max(v.Int64, 0), 1<<32-1))
	return &x
}

func strp(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func scanHistory(s scanner) (HistoryRow, error) {
	var r HistoryRow
	var mode string
	var raw, ref, sel, app, bid, win, em, audio, src, ap, lm sql.NullString
	var dur sql.NullFloat64
	var words, a, l, t sql.NullInt64
	if err := s.Scan(&r.ID, &r.CreatedAt, &mode, &r.Status, &raw, &ref, &sel, &dur, &app, &bid, &win, &em, &audio, &src, &words, &a, &l, &t, &ap, &lm); err != nil {
		return r, err
	}
	r.Mode = model.ParseMode(mode)
	r.RawText, r.RefinedText, r.SelectedText = strp(raw), strp(ref), strp(sel)
	r.DurationSecs = dur.Float64
	r.AppName, r.BundleID, r.WindowTitle, r.ErrorMessage, r.AudioPath = strp(app), strp(bid), strp(win), strp(em), strp(audio)
	r.Source = "hoshiro"
	if src.Valid {
		r.Source = src.String
	}
	r.Words = uint32(max(words.Int64, 0))
	r.AsrMs, r.LlmMs, r.TotalMs = u32p(a), u32p(l), u32p(t)
	r.AsrProvider, r.LlmModel = strp(ap), strp(lm)
	return r, nil
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	steps := []string{
		`CREATE TABLE IF NOT EXISTS history (
			id TEXT PRIMARY KEY, created_at TEXT NOT NULL, mode TEXT NOT NULL, status TEXT NOT NULL,
			raw_text TEXT, refined_text TEXT, selected_text TEXT, duration_secs REAL NOT NULL DEFAULT 0,
			app_name TEXT, bundle_id TEXT, window_title TEXT, error_message TEXT, audio_path TEXT,
			source TEXT NOT NULL DEFAULT 'hoshiro', words INTEGER NOT NULL DEFAULT 0);
		 CREATE INDEX IF NOT EXISTS idx_history_created_at ON history(created_at);
		 CREATE INDEX IF NOT EXISTS idx_history_mode ON history(mode);
		 CREATE TABLE IF NOT EXISTS dictionary (
			id TEXT PRIMARY KEY, word TEXT NOT NULL UNIQUE COLLATE NOCASE, source TEXT NOT NULL, created_at TEXT NOT NULL);
		 PRAGMA user_version = 1;`,
		`CREATE TABLE edits (history_id TEXT PRIMARY KEY, inserted TEXT NOT NULL, final_text TEXT NOT NULL,
			app_name TEXT, created_at TEXT NOT NULL);
		 CREATE TABLE personal_profile (id INTEGER PRIMARY KEY CHECK(id = 1), data TEXT NOT NULL);
		 CREATE TABLE analyzed_sessions (history_id TEXT PRIMARY KEY);
		 CREATE TABLE usage (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, provider TEXT NOT NULL,
			model TEXT NOT NULL, input_tokens INTEGER, output_tokens INTEGER, audio_secs REAL NOT NULL DEFAULT 0);
		 CREATE INDEX idx_usage_created_at ON usage(created_at);
		 CREATE TRIGGER history_cleanup AFTER DELETE ON history BEGIN
			DELETE FROM edits WHERE history_id = OLD.id;
			DELETE FROM analyzed_sessions WHERE history_id = OLD.id;
		 END;
		 PRAGMA user_version = 2;`,
		`ALTER TABLE history ADD COLUMN asr_ms INTEGER;
		 ALTER TABLE history ADD COLUMN llm_ms INTEGER;
		 ALTER TABLE history ADD COLUMN total_ms INTEGER;
		 ALTER TABLE history ADD COLUMN asr_provider TEXT;
		 ALTER TABLE history ADD COLUMN llm_model TEXT;
		 ALTER TABLE usage ADD COLUMN cached_tokens INTEGER;
		 PRAGMA user_version = 3;`,
	}
	for i := v; i < len(steps); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(steps[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Open opens (and migrates) the database at path; ":memory:" for tests.
func Open(path, recordingsDir string) (*DB, error) {
	if err := os.MkdirAll(recordingsDir, 0o755); err != nil {
		return nil, err
	}
	dsn := ":memory:"
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // one connection, as the Rust mutex-guarded connection
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db, recordingsDir: recordingsDir}, nil
}

func (d *DB) Close() error             { return d.db.Close() }
func (d *DB) RecordingsDir() string    { return d.recordingsDir }

func (d *DB) AudioFile(r HistoryRow) (string, bool) {
	if r.AudioPath == nil {
		return "", false
	}
	p := filepath.Join(d.recordingsDir, *r.AudioPath)
	st, err := os.Stat(p)
	return p, err == nil && st.Mode().IsRegular()
}

func (d *DB) ToItem(r HistoryRow) model.HistoryItem {
	_, has := d.AudioFile(r)
	return model.HistoryItem{ID: r.ID, CreatedAt: r.CreatedAt, Mode: r.Mode, Status: r.Status, RawText: r.RawText,
		RefinedText: r.RefinedText, SelectedText: r.SelectedText, DurationSecs: r.DurationSecs, AppName: r.AppName,
		BundleID: r.BundleID, WindowTitle: r.WindowTitle, ErrorMessage: r.ErrorMessage, HasAudio: has, Source: r.Source}
}

type execer interface {
	Exec(string, ...any) (sql.Result, error)
}

func nullU32(p *uint32) any {
	if p == nil {
		return nil
	}
	return int64(*p)
}

func upsert(e execer, r HistoryRow) error {
	_, err := e.Exec("INSERT INTO history ("+historyCols+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) "+
		"ON CONFLICT(id) DO UPDATE SET status=excluded.status, raw_text=excluded.raw_text, "+
		"refined_text=excluded.refined_text, error_message=excluded.error_message, "+
		"audio_path=excluded.audio_path, words=excluded.words, duration_secs=excluded.duration_secs, "+
		"asr_ms=COALESCE(excluded.asr_ms, asr_ms), llm_ms=COALESCE(excluded.llm_ms, llm_ms), "+
		"total_ms=COALESCE(excluded.total_ms, total_ms), asr_provider=COALESCE(excluded.asr_provider, asr_provider), "+
		"llm_model=COALESCE(excluded.llm_model, llm_model)",
		r.ID, r.CreatedAt, string(r.Mode), r.Status, r.RawText, r.RefinedText, r.SelectedText, r.DurationSecs,
		r.AppName, r.BundleID, r.WindowTitle, r.ErrorMessage, r.AudioPath, r.Source, int64(r.Words),
		nullU32(r.AsrMs), nullU32(r.LlmMs), nullU32(r.TotalMs), r.AsrProvider, r.LlmModel)
	return err
}

func (d *DB) UpsertHistory(r HistoryRow) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return upsert(d.db, r)
}

func (d *DB) GetHistory(id string) (*HistoryRow, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, err := scanHistory(d.db.QueryRow("SELECT "+historyCols+" FROM history WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (d *DB) ListHistory(q model.HistoryQuery) ([]HistoryRow, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	query := "SELECT " + historyCols + " FROM history WHERE 1=1"
	var args []any
	if q.Mode != nil {
		query += " AND mode = ?"
		args = append(args, string(*q.Mode))
	}
	if s := strings.TrimSpace(q.Search); s != "" {
		query += ` AND (refined_text LIKE ? ESCAPE '\' OR raw_text LIKE ? ESCAPE '\')`
		esc := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
		args = append(args, "%"+esc+"%", "%"+esc+"%")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, min(max(q.Limit, 1), 500), q.Offset)
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		r, err := scanHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) LastCompletedText() (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var s string
	err := d.db.QueryRow("SELECT refined_text FROM history WHERE status = 'completed' AND refined_text IS NOT NULL " +
		"AND TRIM(refined_text) <> '' ORDER BY created_at DESC LIMIT 1").Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return s, err == nil, err
}

func (d *DB) CountHistory() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var n int
	err := d.db.QueryRow("SELECT COUNT(*) FROM history").Scan(&n)
	return n, err
}

func (d *DB) collectAudio(query string, args ...any) ([]string, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) removeAudio(name string) {
	// Only ever delete plain file names inside the recordings dir.
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return
	}
	p := filepath.Join(d.recordingsDir, name)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		log.Printf("cannot remove audio %s: %v", p, err)
	}
}

// DeleteHistory returns true if a row was removed.
func (d *DB) DeleteHistory(id string) (bool, error) {
	d.mu.Lock()
	var audio sql.NullString
	_ = d.db.QueryRow("SELECT audio_path FROM history WHERE id = ?", id).Scan(&audio)
	res, err := d.db.Exec("DELETE FROM history WHERE id = ?", id)
	d.mu.Unlock()
	if err != nil {
		return false, err
	}
	if audio.Valid {
		d.removeAudio(audio.String)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) DeleteAllHistory() (int, error) {
	d.mu.Lock()
	audio, err := d.collectAudio("SELECT audio_path FROM history WHERE audio_path IS NOT NULL")
	var n int64
	if err == nil {
		var res sql.Result
		if res, err = d.db.Exec("DELETE FROM history"); err == nil {
			n, _ = res.RowsAffected()
		}
	}
	d.mu.Unlock()
	if err != nil {
		return 0, err
	}
	for _, a := range audio {
		d.removeAudio(a)
	}
	return int(n), nil
}

// PurgeOlderThan deletes rows (and audio) older than cutoff; fresh "processing" rows stay.
func (d *DB) PurgeOlderThan(cutoff time.Time) (int, error) {
	c, grace := ts(cutoff), ts(time.Now().Add(-time.Hour))
	d.mu.Lock()
	audio, err := d.collectAudio("SELECT audio_path FROM history WHERE created_at < ? AND audio_path IS NOT NULL "+
		"AND (status <> 'processing' OR created_at < ?)", c, grace)
	var n int64
	if err == nil {
		var res sql.Result
		if res, err = d.db.Exec("DELETE FROM history WHERE created_at < ? AND (status <> 'processing' OR created_at < ?)", c, grace); err == nil {
			n, _ = res.RowsAffected()
		}
	}
	d.mu.Unlock()
	if err != nil {
		return 0, err
	}
	for _, a := range audio {
		d.removeAudio(a)
	}
	return int(n), nil
}

// FailStaleProcessing turns rows left "processing" by a crash into retryable errors.
func (d *DB) FailStaleProcessing() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.db.Exec("UPDATE history SET status = 'error', error_message = '处理被中断' WHERE status = 'processing'")
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (d *DB) StatRows() ([]stats.Row, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.db.Query("SELECT created_at, duration_secs, words FROM history WHERE status = 'completed'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stats.Row
	for rows.Next() {
		var r stats.Row
		var dur sql.NullFloat64
		var w sql.NullInt64
		if err := rows.Scan(&r.CreatedAt, &dur, &w); err != nil {
			return nil, err
		}
		r.DurationSecs, r.Words = dur.Float64, uint32(max(w.Int64, 0))
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImportRows inserts rows whose ids are new; returns (imported, skipped).
func (d *DB) ImportRows(rs []HistoryRow) (imported, skipped uint32, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, r := range rs {
		var exists bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM history WHERE id = ?)", r.ID).Scan(&exists); err != nil {
			return 0, 0, err
		}
		if exists {
			skipped++
			continue
		}
		if err := upsert(tx, r); err != nil {
			return 0, 0, err
		}
		imported++
	}
	return imported, skipped, tx.Commit()
}

func (d *DB) SaveEdit(id, inserted, final string, app *string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`INSERT INTO edits SELECT ?1, ?2, ?3, ?4, ?5 WHERE EXISTS(SELECT 1 FROM history WHERE id = ?1)
		ON CONFLICT(history_id) DO UPDATE SET final_text=excluded.final_text, created_at=excluded.created_at`,
		id, inserted, final, app, Now())
	return err
}

func (d *DB) Profile() (model.PersonalProfile, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var p model.PersonalProfile
	var data string
	err := d.db.QueryRow("SELECT data FROM personal_profile WHERE id=1").Scan(&data)
	if err == nil {
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return p, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	err = d.db.QueryRow(`SELECT COUNT(*) FROM history h WHERE status='completed' AND mode <> 'ask'
		AND raw_text IS NOT NULL AND NOT EXISTS(SELECT 1 FROM analyzed_sessions a WHERE a.history_id=h.id)`).Scan(&p.PendingSessions)
	return p, err
}

func (d *DB) SaveProfile(p model.PersonalProfile, ids []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT OR REPLACE INTO personal_profile VALUES(1, ?)", string(b)); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec("INSERT OR IGNORE INTO analyzed_sessions VALUES(?)", id); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		for _, w := range p.Vocabulary {
			if _, err := tx.Exec("INSERT OR IGNORE INTO dictionary (id, word, source, created_at) VALUES (?, ?, 'auto', ?)", uuid.NewString(), w, Now()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (d *DB) ResetProfile() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec("DELETE FROM personal_profile; DELETE FROM analyzed_sessions;")
	return err
}

// Sample is one session for the personalization job.
type Sample struct {
	ID   string
	Data map[string]any
}

// LearningSamples: newest unanalyzed completed dictations first, whole records within a character budget.
func (d *DB) LearningSamples() ([]Sample, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.db.Query(`SELECT h.id, h.app_name, h.raw_text, h.refined_text, e.inserted, e.final_text
		FROM history h LEFT JOIN edits e ON e.history_id=h.id
		WHERE h.status='completed' AND h.mode <> 'ask' AND h.raw_text IS NOT NULL
		ORDER BY EXISTS(SELECT 1 FROM analyzed_sessions a WHERE a.history_id=h.id), h.created_at DESC, h.id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sample
	size := 0
	opt := func(s sql.NullString) any {
		if s.Valid {
			return s.String
		}
		return nil
	}
	for rows.Next() {
		var id, raw string
		var app, fin, ins, uf sql.NullString
		if err := rows.Scan(&id, &app, &raw, &fin, &ins, &uf); err != nil {
			return nil, err
		}
		m := map[string]any{"app": opt(app), "raw": raw, "final": opt(fin), "inserted": opt(ins), "user_final": opt(uf)}
		b, _ := json.Marshal(m)
		n := len([]rune(string(b)))
		if size+n > 55_000 {
			continue
		}
		size += n
		out = append(out, Sample{id, m})
	}
	return out, rows.Err()
}

func (d *DB) RecordUsage(provider, mdl string, in, out, cached *uint64, audio float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := func(p *uint64) any {
		if p == nil {
			return nil
		}
		return int64(*p)
	}
	_, err := d.db.Exec("INSERT INTO usage(created_at,provider,model,input_tokens,output_tokens,audio_secs,cached_tokens) VALUES(?,?,?,?,?,?,?)",
		Now(), provider, mdl, n(in), n(out), audio, n(cached))
	return err
}

// UsageStats summarizes the last `days` local days (0 = all time).
func (d *DB) UsageStats(days uint32) (model.UsageStats, error) {
	cutoff := ""
	if days > 0 {
		cutoff = time.Now().AddDate(0, 0, -(int(min(days, 366)) - 1)).Format("2006-01-02")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	st := model.UsageStats{Hours: make([]uint64, 24), Weekdays: make([]uint64, 7)}
	daily, modes, apps, providers := map[string]*model.UsageBucket{}, map[string]*model.UsageBucket{}, map[string]*model.UsageBucket{}, map[string]*model.UsageBucket{}
	get := func(m map[string]*model.UsageBucket, k string) *model.UsageBucket {
		if m[k] == nil {
			m[k] = &model.UsageBucket{}
		}
		return m[k]
	}
	var tAll, aAll, lAll []uint64
	aBy, lBy := map[string][]uint64{}, map[string][]uint64{}
	var secsSum float64

	rows, err := d.db.Query("SELECT created_at, mode, status, duration_secs, words, app_name, source, asr_ms, llm_ms, total_ms, asr_provider, llm_model, bundle_id FROM history")
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var created, mode, status string
		var dur sql.NullFloat64
		var words, am, lm, tm sql.NullInt64
		var app, src, ap, lmod, bid sql.NullString
		if err := rows.Scan(&created, &mode, &status, &dur, &words, &app, &src, &am, &lm, &tm, &ap, &lmod, &bid); err != nil {
			rows.Close()
			return st, err
		}
		t, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			rows.Close()
			return st, err
		}
		local := t.Local()
		date := local.Format("2006-01-02")
		if date < cutoff {
			continue
		}
		st.Total.Sessions++
		switch status {
		case "completed":
			st.Outcomes.Completed++
		case "dismissed":
			st.Outcomes.Dismissed++
		case "error":
			st.Outcomes.Error++
			st.ErrorSessions++
		case "empty":
			st.Outcomes.Empty++
		}
		if status != "completed" {
			continue
		}
		st.CompletedSessions++
		w, audio := uint64(max(words.Int64, 0)), dur.Float64
		appName := strings.TrimSpace(app.String)
		if appName == "" {
			if src.String == "typeless" {
				appName = "Typeless 导入"
			} else {
				appName = "未知应用"
			}
		}
		var bundle *string
		if b := strings.TrimSpace(bid.String); b != "" {
			bundle = &bid.String
		} else if appName == "Typeless 导入" {
			s := "now.typeless.desktop"
			bundle = &s
		}
		for _, b := range []*model.UsageBucket{&st.Total, get(daily, date), get(modes, mode), get(apps, appName)} {
			b.Words += w
			b.AudioSecs += audio
		}
		daily[date].Sessions++
		modes[mode].Sessions++
		apps[appName].Sessions++
		if apps[appName].BundleID == nil {
			apps[appName].BundleID = bundle
		}
		st.Hours[local.Hour()]++
		st.Weekdays[(int(local.Weekday())+6)%7]++
		st.LongestSessionSecs = max(st.LongestSessionSecs, audio)
		secsSum += audio
		if tm.Valid {
			tAll = append(tAll, uint64(tm.Int64))
		}
		if am.Valid {
			aAll = append(aAll, uint64(am.Int64))
			if ap.Valid {
				aBy[ap.String] = append(aBy[ap.String], uint64(am.Int64))
			}
		}
		if lm.Valid {
			lAll = append(lAll, uint64(lm.Int64))
			if lmod.Valid {
				lBy[lmod.String] = append(lBy[lmod.String], uint64(lm.Int64))
			}
		}
	}
	rows.Close()
	n := float64(max(st.CompletedSessions, 1))
	st.AverageSessionSecs = secsSum / n
	st.AverageWordsPerSession = float64(st.Total.Words) / n
	st.Latency = model.LatencyReport{Total: stats.Latency(tAll), Asr: stats.Latency(aAll), Llm: stats.Latency(lAll)}

	rows, err = d.db.Query("SELECT created_at,provider,model,input_tokens,output_tokens,audio_secs,cached_tokens FROM usage")
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var created, prov, mdl string
		var in, out, cached sql.NullInt64
		var audio float64
		if err := rows.Scan(&created, &prov, &mdl, &in, &out, &audio, &cached); err != nil {
			rows.Close()
			return st, err
		}
		t, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			rows.Close()
			return st, err
		}
		date := t.Local().Format("2006-01-02")
		if date < cutoff {
			continue
		}
		kind := stats.Classify(prov)
		unrep := uint64(0)
		if kind == stats.KindLlm && (!in.Valid || !out.Valid) {
			unrep = 1
		}
		kt := &st.LlmTotal
		if kind == stats.KindAsr {
			kt = &st.AsrTotal
		}
		kt.Requests++
		kt.AudioSecs += audio
		kt.InputTokens += uint64(in.Int64)
		kt.OutputTokens += uint64(out.Int64)
		kt.CachedTokens += uint64(cached.Int64)
		kt.UnreportedRequests += unrep
		label := prov + " · " + mdl
		p := providers[label]
		if p == nil {
			p = &model.UsageBucket{Kind: string(kind)}
			providers[label] = p
		}
		p.AudioSecs += audio
		p.CachedTokens += uint64(cached.Int64)
		for _, b := range []*model.UsageBucket{&st.Total, get(daily, date), p} {
			b.Requests++
			b.InputTokens += uint64(in.Int64)
			b.OutputTokens += uint64(out.Int64)
			b.UnreportedRequests += unrep
		}
	}
	rows.Close()
	list := func(m map[string]*model.UsageBucket) []model.UsageBucket {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]model.UsageBucket, 0, len(m))
		for _, k := range keys {
			b := *m[k]
			b.Label = k
			out = append(out, b)
		}
		return out
	}
	st.Days, st.Modes, st.Apps, st.Providers = list(daily), list(modes), list(apps), list(providers)
	for i := range st.Providers {
		p := &st.Providers[i]
		by := lBy
		if p.Kind == "asr" {
			by = aBy
		}
		if v := by[p.Label]; len(v) > 0 {
			sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
			p.LatencySamples = uint64(len(v))
			m := stats.Percentile(v, 0.5)
			p.MedianMs = &m
		}
	}
	sort.SliceStable(st.Providers, func(i, j int) bool {
		a, b := st.Providers[i], st.Providers[j]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Label < b.Label
	})
	for _, p := range st.Providers {
		if p.Kind == "asr" {
			st.AsrModels = append(st.AsrModels, p)
		} else {
			st.LlmModels = append(st.LlmModels, p)
		}
	}
	sort.SliceStable(st.Apps, func(i, j int) bool {
		a, b := st.Apps[i], st.Apps[j]
		if a.Words != b.Words {
			return a.Words > b.Words
		}
		return a.Label < b.Label
	})
	if len(st.Apps) > 6 {
		st.Apps = st.Apps[:6]
	}
	err = d.db.QueryRow("SELECT COUNT(*), COALESCE(SUM(source='auto'),0) FROM dictionary").Scan(&st.DictionaryWords, &st.AutoWords)
	return st, err
}

// ── dictionary ──

func scanWords(rows *sql.Rows) ([]model.DictionaryWord, error) {
	defer rows.Close()
	var out []model.DictionaryWord
	for rows.Next() {
		var w model.DictionaryWord
		if err := rows.Scan(&w.ID, &w.Word, &w.Source, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (d *DB) ListWords() ([]model.DictionaryWord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.db.Query("SELECT id, word, source, created_at FROM dictionary ORDER BY created_at DESC, word ASC")
	if err != nil {
		return nil, err
	}
	return scanWords(rows)
}

// WordsForPrompt returns the most recently added words first.
func (d *DB) WordsForPrompt(limit int) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.db.Query("SELECT word FROM dictionary ORDER BY created_at DESC, word ASC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// AddWords adds words not present yet (case-insensitive) and returns the new entries.
func (d *DB) AddWords(words []string, source string) ([]model.DictionaryWord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	base := time.Now()
	var added []model.DictionaryWord
	for i, w := range words {
		e := model.DictionaryWord{ID: uuid.NewString(), Word: w, Source: source, CreatedAt: ts(base.Add(time.Duration(i) * time.Millisecond))}
		res, err := tx.Exec("INSERT OR IGNORE INTO dictionary (id, word, source, created_at) VALUES (?, ?, ?, ?)", e.ID, e.Word, e.Source, e.CreatedAt)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added = append(added, e)
		}
	}
	return added, tx.Commit()
}

func (d *DB) UpdateWord(id, word string) (*model.DictionaryWord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.db.Exec("UPDATE dictionary SET word = ? WHERE id = ?", word, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	var w model.DictionaryWord
	err = d.db.QueryRow("SELECT id, word, source, created_at FROM dictionary WHERE id = ?", id).Scan(&w.ID, &w.Word, &w.Source, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &w, err
}

func (d *DB) DeleteWords(ids []string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	total := 0
	for _, id := range ids {
		res, err := tx.Exec("DELETE FROM dictionary WHERE id = ?", id)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, tx.Commit()
}
