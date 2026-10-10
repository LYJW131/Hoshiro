package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/model"
)

// Recognize runs a batch recognizer (MiMo or Cloudflare) on one WAV.
func Recognize(ctx context.Context, cfg model.AsrConfig, wav []byte, language string, words []string) (string, error) {
	switch cfg.Provider {
	case "mimo":
		return TranscribeMiMo(ctx, cfg.Mimo, wav, language)
	case "cloudflare":
		return cloudflare(ctx, cfg, wav, language, words)
	}
	return "", errors.New("该服务需要流式音频")
}

type CfFormat int

const (
	CfTurbo CfFormat = iota
	CfWhisper
	CfNova3
)

func CfFormatOf(m string) CfFormat {
	switch {
	case strings.HasPrefix(m, "@cf/deepgram/"):
		return CfNova3
	case m == "@cf/openai/whisper" || strings.HasPrefix(m, "@cf/openai/whisper-tiny"):
		return CfWhisper
	}
	return CfTurbo
}

type CfRequest struct {
	ContentType string
	Query       url.Values
	Body        []byte
}

func CloudflareBody(wav []byte, language string, words []string) map[string]any {
	b := map[string]any{"audio": base64.StdEncoding.EncodeToString(wav), "initial_prompt": strings.Join(words[:min(len(words), 100)], ", ")}
	if language != "auto" {
		b["language"] = language
	}
	return b
}

func CloudflareRequest(m string, wav []byte, language string, words []string) CfRequest {
	switch CfFormatOf(m) {
	case CfWhisper:
		return CfRequest{"application/octet-stream", url.Values{}, wav}
	case CfNova3:
		q := url.Values{"punctuate": {"true"}, "smart_format": {"true"}}
		if language == "auto" {
			q.Set("detect_language", "true")
		} else {
			q.Set("language", language)
		}
		n := 0
		for _, w := range words {
			if w = strings.TrimSpace(w); w != "" && n < 50 {
				q.Add("keyterm", w)
				n++
			}
		}
		return CfRequest{"audio/wav", q, wav}
	}
	b, _ := json.Marshal(CloudflareBody(wav, language, words))
	return CfRequest{"application/json", url.Values{}, b}
}

// CloudflareText extracts the transcript from the result object of any supported model.
func CloudflareText(result any) (string, bool) {
	for _, p := range [][]any{{"text"}, {"transcription_info", "text"}, {"results", "channels", 0, "alternatives", 0, "transcript"}} {
		if s, ok := dig(result, p...).(string); ok {
			return strings.TrimSpace(s), true
		}
	}
	return "", false
}

func cloudflare(ctx context.Context, cfg model.AsrConfig, wav []byte, language string, words []string) (string, error) {
	if !cfg.Ready() {
		return "", errors.New("请填写 Cloudflare 账户 ID 和 API Token")
	}
	c := cfg.Cloudflare
	for _, r := range c.AccountID {
		if r > 127 || !(unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return "", errors.New("Cloudflare 账户 ID 格式不正确")
		}
	}
	if !strings.HasPrefix(c.Model, "@cf/") || strings.Contains(c.Model, "..") || strings.ContainsAny(c.Model, "?#") {
		return "", errors.New("Cloudflare 模型名称不正确")
	}
	if CfFormatOf(c.Model) == CfNova3 && strings.HasPrefix(strings.ToLower(language), "zh") {
		return "", errors.New("Nova-3 不支持中文，请改用 Whisper 模型")
	}
	return cloudflareAt(ctx, "https://api.cloudflare.com/client/v4/accounts/"+c.AccountID+"/ai/run/"+c.Model, cfg, wav, language, words)
}

func cloudflareAt(ctx context.Context, endpoint string, cfg model.AsrConfig, wav []byte, language string, words []string) (string, error) {
	c := cfg.Cloudflare
	req := CloudflareRequest(c.Model, wav, language, words)
	if len(req.Query) > 0 {
		endpoint += "?" + req.Query.Encode()
	}
	b, f := send(ctx, "Cloudflare", "POST", endpoint, []header{{"Authorization", "Bearer " + strings.TrimSpace(c.APIKey)}, {"Content-Type", req.ContentType}}, bytes.NewReader(req.Body), 60*time.Second)
	if f != nil {
		if f.Status != 0 {
			return "", StatusFailure("Cloudflare", f.Status, "请检查账户、模型与权限")
		}
		return "", f
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return "", errors.New("Cloudflare 响应格式不正确")
	}
	if s, ok := v["success"].(bool); ok && !s {
		return "", errors.New("Cloudflare 识别失败，请检查账户权限与额度")
	}
	if e, ok := v["errors"].([]any); ok && len(e) > 0 {
		return "", errors.New("Cloudflare 识别失败，请检查账户权限与额度")
	}
	var result any = v
	if r, ok := v["result"]; ok {
		result = r
	}
	out, ok := CloudflareText(result)
	if !ok {
		return "", errors.New("Cloudflare 响应格式不正确")
	}
	Usage("Cloudflare", c.Model, []byte("{}"), float64(max(len(wav)-44, 0))/32000)
	return out, nil
}

// TestAsr verifies the selected speech provider with half a second of silence.
func TestAsr(ctx context.Context, cfg model.AsrConfig, languages, words []string) model.TestResult {
	start := time.Now()
	var err error
	if cfg.Streaming() {
		_, err = TranscribeSamples(ctx, cfg, languages, words, make([]int16, 8000))
	} else {
		_, err = Recognize(ctx, cfg, audio.EncodeWAV(make([]int16, 8000), audio.TargetSampleRate), LanguageParam(languages, false), words)
	}
	r := model.TestResult{OK: err == nil, Message: "连接成功", LatencyMs: uint64(time.Since(start).Milliseconds())}
	if err != nil {
		r.Message = err.Error()
	}
	return r
}
