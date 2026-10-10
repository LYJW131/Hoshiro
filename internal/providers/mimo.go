package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/model"
)

const maxBase64Bytes = 10 * 1024 * 1024

// LanguageParam is MiMo's asr_options.language for the user's dictation languages.
func LanguageParam(selected []string, auto bool) string {
	if auto || len(selected) == 0 {
		return "auto"
	}
	all := func(prefix string) bool {
		for _, l := range selected {
			if !strings.HasPrefix(strings.ToLower(l), prefix) {
				return false
			}
		}
		return true
	}
	switch {
	case all("zh"):
		return "zh"
	case all("en"):
		return "en"
	}
	return "auto"
}

// TranscribeMiMo transcribes one 16 kHz mono WAV; language is zh | en | auto.
func TranscribeMiMo(ctx context.Context, cfg model.MimoAsrConfig, wav []byte, language string) (string, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return "", errors.New("未配置 MiMo API Key")
	}
	if len(wav) <= 44 {
		return "", nil
	}
	url := TrimBase(cfg.BaseURL) + "/chat/completions"
	mdl := strings.TrimSpace(cfg.Model)
	if mdl == "" {
		mdl = "mimo-v2.5-asr"
	}
	audioSecs := float64(len(wav)-44) / 32000
	enc := base64.StdEncoding.EncodeToString(wav)
	if len(enc) >= maxBase64Bytes {
		return "", errors.New("音频过长（超过 10 MB），请缩短单次录音")
	}
	body, _ := json.Marshal(map[string]any{
		"model": mdl,
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "input_audio", "input_audio": map[string]any{"data": "data:audio/wav;base64," + enc},
		}}}},
		"asr_options": map[string]any{"language": language},
	})
	hs := []header{{"Authorization", "Bearer " + cfg.APIKey}, {"api-key", cfg.APIKey}, {"Content-Type", "application/json"}}
	for attempt := 0; ; attempt++ {
		b, f := send(ctx, "MiMo", "POST", url, hs, bytes.NewReader(body), 30*time.Second)
		if f == nil {
			var v any
			if json.Unmarshal(b, &v) != nil {
				return "", &Failure{Message: "MiMo 返回了无法解析的响应"}
			}
			Usage("MiMo", mdl, b, audioSecs)
			return CleanTranscript(extractContent(v)), nil
		}
		if f.Retryable && attempt == 0 {
			log.Printf("MiMo retrying after: %s", f.Message)
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return "", ctx.Err()
			}
			continue
		}
		return "", f
	}
}

func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func extractContent(v any) string {
	switch c := dig(v, "choices", 0, "message", "content").(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, p := range c {
			if t, ok := dig(p, "text").(string); ok {
				sb.WriteString(t)
			}
		}
		return sb.String()
	}
	return ""
}

func tagChars(tag string) bool {
	if tag == "" {
		return false
	}
	for _, c := range tag {
		if !(c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_/|- ", c))) {
			return false
		}
	}
	return true
}

// CleanTranscript trims and strips wrapping quotes, code fences or tags around the transcript.
func CleanTranscript(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimLeft(s, "`")
		s = strings.TrimLeftFunc(s, func(r rune) bool { return r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') })
		s = strings.TrimSpace(strings.TrimRight(strings.TrimRight(s, " \t\r\n"), "`"))
	}
	for {
		before := len(s)
		if strings.HasPrefix(s, "<") {
			if end := strings.IndexByte(s, '>'); end >= 0 && end <= 24 && tagChars(s[1:end]) {
				s = strings.TrimLeft(s[end+1:], " \t\r\n")
			}
		}
		if strings.HasSuffix(s, ">") {
			if start := strings.LastIndexByte(s, '<'); start >= 0 && len(s)-start <= 24 && tagChars(s[start+1:len(s)-1]) {
				s = strings.TrimRight(s[:start], " \t\r\n")
			}
		}
		if len(s) == before {
			break
		}
	}
	return strings.TrimSpace(StripWrappingQuotes(s))
}

var quotePairs = [][2]rune{{'"', '"'}, {'“', '”'}, {'「', '」'}, {'『', '』'}, {'\'', '\''}, {'‘', '’'}}

// StripWrappingQuotes removes a quote pair wrapping the whole text, when not used inside.
func StripWrappingQuotes(s string) string {
	t := strings.TrimSpace(s)
	if utf8.RuneCountInString(t) < 2 {
		return t
	}
	first, fl := utf8.DecodeRuneInString(t)
	last, ll := utf8.DecodeLastRuneInString(t)
	for _, p := range quotePairs {
		if first == p[0] && last == p[1] {
			inner := t[fl : len(t)-ll]
			if !strings.ContainsRune(inner, p[0]) && !strings.ContainsRune(inner, p[1]) {
				return strings.TrimSpace(inner)
			}
		}
	}
	return t
}

// TestMiMo sends ~1 s of generated audio to verify the key and endpoint.
func TestMiMo(ctx context.Context, cfg model.MimoAsrConfig) model.TestResult {
	samples := make([]int16, 16000)
	for n := range samples {
		t := float64(n) / 16000
		env := math.Pow(math.Sin(math.Pi*t), 2)
		samples[n] = int16(env * 0.08 * math.Sin(2*math.Pi*220*t) * 32767)
	}
	start := time.Now()
	_, err := TranscribeMiMo(ctx, cfg, audio.EncodeWAV(samples, 16000), "auto")
	ms := uint64(time.Since(start).Milliseconds())
	if err != nil {
		return model.TestResult{Message: err.Error(), LatencyMs: ms}
	}
	return model.TestResult{OK: true, Message: fmt.Sprintf("连接成功（%d ms）", ms), LatencyMs: ms}
}
