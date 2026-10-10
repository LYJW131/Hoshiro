package personalize

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/providers"
	"github.com/IcyBlue17/hoshiro/internal/store"
)

func TestSchedule(t *testing.T) {
	now := time.Now().UTC()
	s := func(t time.Time) *string { v := t.Format(time.RFC3339); return &v }
	p := model.PersonalProfile{PendingSessions: 9}
	if Eligible(p, false, now) || !Eligible(p, true, now) {
		t.Fatal(1)
	}
	p.PendingSessions = 10
	if !Eligible(p, false, now) {
		t.Fatal(2)
	}
	p.UpdatedAt = s(now.Add(-21 * time.Hour))
	if Eligible(p, false, now) {
		t.Fatal(3)
	}
	p.PendingSessions = 15
	if !Eligible(p, false, now) {
		t.Fatal(4)
	}
	p.NextRunAt = s(now.Add(time.Hour))
	if Eligible(p, false, now) || !Eligible(p, true, now) {
		t.Fatal(5)
	}
	p.NextRunAt = nil
	p.UpdatedAt = s(now.Add(-19 * time.Hour))
	if Eligible(p, false, now) {
		t.Fatal(6)
	}
	next, _ := time.Parse(time.RFC3339, NextRun(now))
	if h := next.Sub(now).Hours(); h < 20 || h > 26.01 {
		t.Fatal(h)
	}
}

func TestResolve(t *testing.T) {
	cfg := model.DefaultSettings().Llm
	cfg.Provider = model.LlmAnthropic
	cfg.Anthropic.APIKey, cfg.Gemini.APIKey = "ak", "gk"
	cfg.Personalization.ReasoningEffort = "high"
	cfg.Personalization.Provider = model.LlmGemini
	cfg.Personalization.Model = "gemini-3-pro"
	p, ep, e, err := ResolveLLM(cfg)
	if err != nil || p != model.LlmAnthropic || ep.APIKey != "ak" || ep.Model != "claude-haiku-5-5" || e != providers.ParseEffort("high") {
		t.Fatal(p, ep, err)
	}
	cfg.Personalization.FollowRewrite = false
	if p, ep, _, _ := ResolveLLM(cfg); p != model.LlmGemini || ep.APIKey != "gk" || ep.Model != "gemini-3-pro" {
		t.Fatal(p, ep)
	}
	cfg.Personalization.Model = "  "
	if _, ep, _, _ := ResolveLLM(cfg); ep.Model != "gemini-flash-latest" {
		t.Fatal(ep.Model)
	}
	cfg.Personalization.Provider = model.LlmOpenAI
	if _, _, _, err := ResolveLLM(cfg); err == nil || !strings.Contains(err.Error(), "API Key") || !strings.Contains(err.Error(), "OpenAI") {
		t.Fatal(err)
	}
}

func TestSanitize(t *testing.T) {
	p := model.PersonalProfile{Vocabulary: []string{" Tauri ", "tauri", strings.Repeat("字", 33)}, PromptAddendum: strings.Repeat("字", 1300)}
	for i := range 40 {
		p.VerbalTics = append(p.VerbalTics, "词"+string(rune('a'+i)))
	}
	Sanitize(&p)
	if len(p.Vocabulary) != 1 || p.Vocabulary[0] != "Tauri" || len(p.VerbalTics) != 30 || runeLen(p.PromptAddendum) != 1200 {
		t.Fatal(p.Vocabulary, len(p.VerbalTics))
	}
}

func TestRunWithFakeModel(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "h.db"), filepath.Join(dir, "rec"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 4 {
		r := store.NewRow(string(rune('a'+i))+"-id", model.ModeDictation, "completed")
		raw, fin := "嗯 用 tauri 写", "用 Tauri 写"
		r.RawText, r.RefinedText = &raw, &fin
		db.UpsertHistory(r)
	}
	s := model.DefaultSettings()
	s.Llm.Gemini.APIKey = "k"
	l := &Learner{DB: db, Settings: func() model.Settings { return s }}
	l.complete = func(_ context.Context, _ model.LlmProvider, _ model.LlmEndpointConfig, sys, user string, _ bool, _ int, _ providers.Effort) (string, error) {
		if !strings.Contains(user, "tauri") {
			t.Error("records missing")
		}
		return "```json\n{\"verbalTics\":[\"嗯\"],\"corrections\":[{\"wrong\":\"tauri\",\"right\":\"Tauri\"}],\"vocabulary\":[\"Tauri\"],\"styleNotes\":[],\"promptAddendum\":\"简洁\"}\n```", nil
	}
	p, err := l.Run(context.Background(), true)
	if err != nil || len(p.Vocabulary) != 1 || p.PromptAddendum != "简洁" || p.UpdatedAt == nil || p.AnalyzedSessions != 4 {
		t.Fatal(err, p)
	}
	if _, err := l.Run(context.Background(), true); err == nil {
		t.Fatal("nothing new to analyze")
	}
}
