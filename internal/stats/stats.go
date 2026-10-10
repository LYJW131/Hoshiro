// Package stats counts words and computes usage statistics.
package stats

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

func isCJK(c rune) bool {
	switch {
	case c >= 0x3400 && c <= 0x4DBF, c >= 0x4E00 && c <= 0x9FFF, c >= 0xF900 && c <= 0xFAFF, c >= 0x20000 && c <= 0x2FA1F:
		return true
	case c >= 0x3040 && c <= 0x30FF, c >= 0x31F0 && c <= 0x31FF:
		return true
	case c >= 0xAC00 && c <= 0xD7AF, c >= 0x1100 && c <= 0x11FF:
		return true
	}
	return false
}

// CountWords counts each CJK character as one word and each run of letters
// or digits as one word; contractions stay one word.
func CountWords(text string) uint32 {
	var count uint32
	inWord, prevAlnum := false, false
	for _, c := range text {
		switch {
		case isCJK(c):
			count++
			inWord, prevAlnum = false, false
		case unicode.IsLetter(c) || unicode.IsNumber(c):
			if !inWord {
				count++
				inWord = true
			}
			prevAlnum = true
		case (c == '\'' || c == '’') && prevAlnum && inWord:
			prevAlnum = false
		default:
			inWord, prevAlnum = false, false
		}
	}
	return count
}

type Row struct {
	CreatedAt    string // RFC 3339
	DurationSecs float64
	Words        uint32
}

type DayRow struct {
	Date  time.Time // local midnight
	Secs  float64
	Words uint32
}

func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Compute builds the home-page stats from completed sessions.
func Compute(rows []Row, today time.Time) model.Stats {
	var drs []DayRow
	for _, r := range rows {
		t, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if err != nil {
			continue
		}
		drs = append(drs, DayRow{day(t.Local()), r.DurationSecs, r.Words})
	}
	return ComputeDays(drs, day(today))
}

func ComputeDays(rows []DayRow, today time.Time) model.Stats {
	today = day(today)
	var totalWords uint64
	var totalSecs float64
	type agg struct{ words, sessions uint64 }
	perDay := map[time.Time]agg{}
	for _, r := range rows {
		totalWords += uint64(r.Words)
		if !math.IsInf(r.Secs, 0) && !math.IsNaN(r.Secs) && r.Secs > 0 {
			totalSecs += r.Secs
		}
		a := perDay[day(r.Date)]
		a.words += uint64(r.Words)
		a.sessions++
		perDay[day(r.Date)] = a
	}
	var wpm float64
	if totalSecs > 0 {
		wpm = float64(totalWords) / (totalSecs / 60)
	}
	saved := math.Max(float64(totalWords)/40*60-totalSecs, 0)

	dates := make([]time.Time, 0, len(perDay))
	for d := range perDay {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	var longest, run uint32
	for i, d := range dates {
		if i > 0 && d.Sub(dates[i-1]) == 24*time.Hour {
			run++
		} else {
			run = 1
		}
		longest = max(longest, run)
	}
	var current uint32
	cur := today
	if _, ok := perDay[today]; !ok {
		cur = today.AddDate(0, 0, -1)
	}
	for {
		if _, ok := perDay[cur]; !ok {
			break
		}
		current++
		cur = cur.AddDate(0, 0, -1)
	}

	// 53 weeks, Sunday-based columns, oldest first, up to today.
	weekStart := today.AddDate(0, 0, -int(today.Weekday()))
	start := weekStart.AddDate(0, 0, -52*7)
	heat := make([]model.HeatmapDay, 0, 53*7)
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		a := perDay[d]
		heat = append(heat, model.HeatmapDay{Date: d.Format("2006-01-02"), Words: a.words, Sessions: a.sessions})
	}
	return model.Stats{
		TotalWords: totalWords, TotalDictationSecs: totalSecs, AverageWPM: wpm, TimeSavedSecs: saved,
		ActiveDays: uint32(len(perDay)), CurrentStreak: current, LongestStreak: longest, Heatmap: heat,
	}
}

// ── provider usage ──

type Kind string

const (
	KindAsr Kind = "asr"
	KindLlm Kind = "llm"
)

// Classify: speech services bill by audio time, everything else is an LLM.
func Classify(provider string) Kind {
	p := strings.ToLower(strings.TrimSpace(provider))
	for _, n := range []string{"mimo", "cloudflare", "azure", "google"} {
		if strings.HasPrefix(p, n) {
			return KindAsr
		}
	}
	return KindLlm
}

// Percentile is the linear-interpolated percentile (p in 0..1) of an ascending slice.
func Percentile(sorted []uint64, p float64) uint64 {
	switch len(sorted) {
	case 0:
		return 0
	case 1:
		return sorted[0]
	}
	pos := math.Min(math.Max(p, 0), 1) * float64(len(sorted)-1)
	lo, hi := int(math.Floor(pos)), int(math.Ceil(pos))
	frac := pos - float64(lo)
	return uint64(math.Round(float64(sorted[lo]) + (float64(sorted[hi])-float64(sorted[lo]))*frac))
}

func Latency(v []uint64) model.LatencyStat {
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	n := uint64(len(v))
	var sum uint64
	for _, x := range v {
		sum += x
	}
	st := model.LatencyStat{Samples: n, MedianMs: Percentile(v, 0.5), P90Ms: Percentile(v, 0.9)}
	if n > 0 {
		st.MeanMs = sum / n
	}
	return st
}

func pointer(v any, path ...string) (uint64, bool) {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return 0, false
		}
		v = m[k]
	}
	f, ok := v.(float64)
	if !ok || f < 0 {
		return 0, false
	}
	return uint64(f), true
}

func first(v any, paths ...[]string) *uint64 {
	for _, p := range paths {
		if n, ok := pointer(v, p...); ok {
			return &n
		}
	}
	return nil
}

func decode(resp []byte) any {
	var v any
	_ = json.Unmarshal(resp, &v)
	return v
}

// Tokens extracts input/output token counts from an OpenAI, Anthropic or Gemini response.
func Tokens(resp []byte) (in, out *uint64) {
	v := decode(resp)
	in = first(v, []string{"usage", "prompt_tokens"}, []string{"usage", "input_tokens"}, []string{"usageMetadata", "promptTokenCount"})
	if _, ok := pointer(v, "usage", "input_tokens"); ok && in != nil {
		n := *in
		if x, ok := pointer(v, "usage", "cache_read_input_tokens"); ok {
			n += x
		}
		if x, ok := pointer(v, "usage", "cache_creation_input_tokens"); ok {
			n += x
		}
		in = &n
	}
	out = first(v, []string{"usage", "completion_tokens"}, []string{"usage", "output_tokens"}, []string{"usageMetadata", "candidatesTokenCount"})
	if th, ok := pointer(v, "usageMetadata", "thoughtsTokenCount"); ok {
		n := th
		if out != nil {
			n += *out
		}
		out = &n
	}
	return
}

// CachedTokens returns cache-hit input tokens, when reported.
func CachedTokens(resp []byte) *uint64 {
	return first(decode(resp), []string{"usage", "cache_read_input_tokens"},
		[]string{"usage", "prompt_tokens_details", "cached_tokens"}, []string{"usageMetadata", "cachedContentTokenCount"})
}
