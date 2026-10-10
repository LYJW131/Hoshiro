package providers

import (
	"context"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

const warmDedupe = 20 * time.Second

var (
	warmMu   sync.Mutex
	warmLast = map[string]time.Time{}
)

func shouldWarm(m map[string]time.Time, key string, now time.Time, window time.Duration) bool {
	if t, ok := m[key]; ok && now.Sub(t) < window {
		return false
	}
	m[key] = now
	return true
}

// origin returns scheme://host[:port] of a URL.
func origin(raw string) (string, bool) {
	u, err := url.Parse(TrimBase(raw))
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// WarmTargets are the origins of the active speech and rewrite providers.
func WarmTargets(s model.Settings) []string {
	var out []string
	add := func(u string, ok bool) {
		if ok && (len(out) == 0 || out[len(out)-1] != u) {
			out = append(out, u)
		}
	}
	switch s.Asr.Provider {
	case "mimo":
		add(origin(s.Asr.Mimo.BaseURL))
	case "cloudflare":
		add("https://api.cloudflare.com", true)
	}
	if s.Llm.Enabled {
		ep := s.Llm.ActiveEndpoint()
		def := map[model.LlmProvider]string{model.LlmGemini: geminiDefaultBase, model.LlmAnthropic: anthropicDefaultBase, model.LlmOpenAI: openaiDefaultBase}[s.Llm.Provider]
		base := ep.BaseURL
		if TrimBase(base) == "" {
			base = def
		}
		add(origin(base))
	}
	return out
}

// WarmUp opens pooled HTTPS connections to the hosts a dictation will use. Non-blocking.
func WarmUp(s model.Settings) {
	now := time.Now()
	for _, u := range WarmTargets(s) {
		warmMu.Lock()
		ok := shouldWarm(warmLast, u, now, warmDedupe)
		warmMu.Unlock()
		if !ok {
			continue
		}
		go func(u string) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			t := time.Now()
			_, f := send(ctx, "warm", "HEAD", u, nil, nil, 3*time.Second)
			if f != nil && f.Status == 0 {
				log.Printf("warm %s: %s", u, f.Message)
				return
			}
			log.Printf("warm %s in %d ms", u, time.Since(t).Milliseconds())
		}(u)
	}
}
