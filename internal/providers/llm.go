package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

// OutcomeKind says what to do with a rewrite.
type OutcomeKind int

const (
	Insert  OutcomeKind = iota // insert at the caret
	Replace                    // replace the selection (Ask with selection)
	Answer                     // show in the answer card
)

type Outcome struct {
	Kind OutcomeKind
	Text string
}

const (
	geminiDefaultBase    = "https://generativelanguage.googleapis.com"
	anthropicDefaultBase = "https://api.anthropic.com"
	openaiDefaultBase    = "https://api.openai.com/v1"
)

func providerLabel(p model.LlmProvider) string {
	switch p {
	case model.LlmAnthropic:
		return "Anthropic"
	case model.LlmOpenAI:
		return "OpenAI 兼容服务"
	}
	return "Gemini"
}

// Effort is the unified reasoning effort, mapped per provider.
type Effort int

const (
	EffortOff Effort = iota
	EffortLow
	EffortMedium
	EffortHigh
)

func ParseEffort(s string) Effort {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return EffortLow
	case "medium":
		return EffortMedium
	case "high":
		return EffortHigh
	}
	return EffortOff
}

func (e Effort) level() string {
	switch e {
	case EffortMedium:
		return "medium"
	case EffortHigh:
		return "high"
	}
	return "low"
}

func (e Effort) anthropicBudget() int {
	switch e {
	case EffortMedium:
		return 4096
	case EffortHigh:
		return 16000
	}
	return 1024
}

func (e Effort) geminiBudget() int {
	return [...]int{0, 1024, 8192, 24576}[e]
}

func (e Effort) geminiLevel() string {
	if e == EffortOff {
		return "minimal"
	}
	return e.level()
}

// rejected remembers optional parameters a provider+model refused.
var rejected sync.Map

func paramKey(p model.LlmProvider, mdl, param string) string {
	return string(p) + "|" + mdl + "|" + param
}
func isRejected(k string) bool { _, ok := rejected.Load(k); return ok }
func reject(k string)          { rejected.Store(k, true) }

// Rewrite runs the active provider, honoring cfg.TimeoutSecs (plus 6 s per minute of audio).
func Rewrite(ctx context.Context, cfg model.LlmConfig, req *RewriteRequest) (Outcome, error) {
	secs := min(max(int(cfg.TimeoutSecs), 1), 120) + int(math.Ceil(req.DurationSecs/60*6))
	ctx, cancel := context.WithTimeout(ctx, time.Duration(secs)*time.Second)
	defer cancel()
	out, err := rewriteWith(ctx, cfg.Provider, *cfg.ActiveEndpoint(), req, secs, ParseEffort(cfg.ReasoningEffort))
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("改写超时（%d 秒）", secs)
	}
	return out, err
}

func rewriteWith(ctx context.Context, p model.LlmProvider, ep model.LlmEndpointConfig, req *RewriteRequest, timeoutSecs int, effort Effort) (Outcome, error) {
	system := SystemPromptFor(req.Mode, req.SmartFormatting, req.LongLists)
	raw, err := Complete(ctx, p, ep, system, BuildUserMessage(req), req.Mode == model.ModeAsk, timeoutSecs, effort)
	if err != nil {
		return Outcome{}, err
	}
	return Finalize(req, raw)
}

// Finalize turns the model's raw reply into an outcome.
func Finalize(req *RewriteRequest, raw string) (Outcome, error) {
	switch req.Mode {
	case model.ModeDictation:
		out := CleanOutput(raw)
		t := strings.TrimSpace(req.Transcript)
		if out == "" || LooksLikeAssistantReply(t, out) {
			log.Print("dictation rewrite rejected (empty or assistant-style); using transcript")
			return Outcome{Insert, t}, nil
		}
		return Outcome{Insert, out}, nil
	case model.ModeTranslation:
		out := CleanOutput(raw)
		if out == "" {
			return Outcome{}, errors.New("翻译结果为空")
		}
		return Outcome{Insert, out}, nil
	}
	_, has := nonEmpty(req.SelectedText)
	return ParseAsk(raw, has)
}

func isFenceLang(s string) bool {
	for _, c := range s {
		if !(c < 128 && (unicode.IsLetter(c) || unicode.IsDigit(c)) || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(t, "```"); ok {
		if i := strings.IndexByte(rest, '\n'); i >= 0 && isFenceLang(rest[:i]) {
			rest = rest[i+1:]
		}
		rest = strings.TrimRightFunc(rest, unicode.IsSpace)
		rest = strings.TrimSuffix(rest, "```")
		return strings.TrimSpace(rest)
	}
	return t
}

var prefacePrefixes = []string{"以下是", "下面是", "这是", "好的", "当然", "整理后", "润色后", "翻译如下", "译文", "here is", "here's", "here are", "sure", "certainly", "of course", "the following", "cleaned", "translation"}

func isPrefaceLine(line string) bool {
	l := strings.TrimSpace(line)
	if utf8.RuneCountInString(l) > 60 || !(strings.HasSuffix(l, "：") || strings.HasSuffix(l, ":")) {
		return false
	}
	lower := strings.ToLower(l)
	for _, p := range prefacePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// CleanOutput trims and drops wrapping quotes, code fences, tags and "以下是…：" prefaces.
func CleanOutput(raw string) string {
	s := stripCodeFence(raw)
	if strings.HasPrefix(s, "<analysis>") {
		if _, after, ok := strings.Cut(s, "</analysis>"); ok {
			s = strings.TrimSpace(after)
		} else {
			s = ""
		}
	}
	for _, tag := range []string{"transcript", "output", "result", "text"} {
		if rest, ok := strings.CutPrefix(s, "<"+tag+">"); ok {
			s = strings.TrimSpace(strings.TrimSuffix(rest, "</"+tag+">"))
		}
	}
	if first, rest, ok := strings.Cut(s, "\n"); ok && isPrefaceLine(first) {
		s = strings.TrimSpace(rest)
	}
	s = StripWrappingQuotes(s)
	for _, label := range []string{"Output:", "输出：", "输出:", "Result:", "结果："} {
		if rest, ok := strings.CutPrefix(s, label); ok {
			s = strings.TrimSpace(rest)
			break
		}
	}
	return strings.TrimSpace(s)
}

var assistantOpeners = []string{
	"好的", "好的，", "当然", "没问题", "抱歉", "对不起", "很抱歉", "我无法", "我不能", "我没办法", "作为一个", "作为ai", "作为 ai", "以下是", "下面是", "为您", "为你",
	"sure", "certainly", "of course", "absolutely", "i'm sorry", "i am sorry", "sorry,", "i cannot", "i can't", "i'm unable", "as an ai", "here is", "here's", "here are",
	"great question", "happy to help",
}

// LooksLikeAssistantReply guards against the model chatting instead of cleaning up dictation.
func LooksLikeAssistantReply(transcript, output string) bool {
	t := strings.ToLower(strings.TrimSpace(transcript))
	o := strings.ToLower(strings.TrimSpace(output))
	if t == "" {
		return false
	}
	if float64(utf8.RuneCountInString(o)) > float64(utf8.RuneCountInString(t))*2.5+40 {
		return true
	}
	head := []rune(t)
	head = head[:min(len(head), 40)]
	for _, op := range assistantOpeners {
		if strings.HasPrefix(o, op) && !strings.Contains(string(head), strings.TrimSuffix(op, "，")) {
			return true
		}
	}
	return false
}

func jsonCandidates(raw string) []string {
	s := stripCodeFence(raw)
	out := []string{s}
	if a, b := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}'); a >= 0 && b > a {
		out = append(out, s[a:b+1])
	}
	return out
}

func lenientTextField(s string) (string, bool) {
	k := strings.Index(s, `"text"`)
	if k < 0 {
		return "", false
	}
	after := s[k+6:]
	c := strings.IndexByte(after, ':')
	if c < 0 {
		return "", false
	}
	after = strings.TrimLeftFunc(after[c+1:], unicode.IsSpace)
	body, ok := strings.CutPrefix(after, `"`)
	if !ok {
		return "", false
	}
	end := strings.LastIndexByte(body, '"')
	if end < 0 {
		return "", false
	}
	raw := []rune(body[:end])
	var sb strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			sb.WriteRune(raw[i])
			continue
		}
		i++
		if i >= len(raw) {
			sb.WriteByte('\\')
			break
		}
		switch raw[i] {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case '"', '\\', '/':
			sb.WriteRune(raw[i])
		default:
			sb.WriteByte('\\')
			sb.WriteRune(raw[i])
		}
	}
	return sb.String(), true
}

// ParseAsk parses the Ask reply: strict JSON {"action","text"}, tolerant of fences and prose.
func ParseAsk(raw string, hasSelection bool) (Outcome, error) {
	var action string
	var text *string
	for _, c := range jsonCandidates(raw) {
		var m map[string]any
		if json.Unmarshal([]byte(c), &m) != nil || m == nil {
			continue
		}
		action = ""
		if a, ok := m["action"].(string); ok {
			action = strings.ToLower(strings.TrimSpace(a))
		}
		text = nil
		if t, ok := m["text"].(string); ok {
			text = &t
		} else if t, ok := m["answer"].(string); ok {
			text = &t
		}
		if text != nil {
			break
		}
	}
	if text == nil {
		s := stripCodeFence(raw)
		if t, ok := lenientTextField(s); ok {
			action = ""
			for _, a := range []string{"replace", "insert", "answer"} {
				if strings.Contains(s, `"`+a+`"`) {
					action = a
					break
				}
			}
			text = &t
		}
	}
	if text == nil {
		a := strings.TrimSpace(raw)
		if a == "" {
			return Outcome{}, errors.New("模型没有返回内容")
		}
		return Outcome{Answer, a}, nil
	}
	t := strings.TrimSpace(*text)
	if t == "" {
		return Outcome{}, errors.New("模型没有返回内容")
	}
	switch {
	case action == "replace" && hasSelection:
		return Outcome{Replace, t}, nil
	case action == "replace" || action == "insert":
		return Outcome{Insert, t}, nil
	}
	return Outcome{Answer, t}, nil
}

// ── provider calls ──

// Complete sends one system+user prompt to provider p and returns the text.
func Complete(ctx context.Context, p model.LlmProvider, ep model.LlmEndpointConfig, system, user string, jsonMode bool, timeoutSecs int, effort Effort) (string, error) {
	if strings.TrimSpace(ep.APIKey) == "" {
		return "", fmt.Errorf("未配置%s的 API Key", providerLabel(p))
	}
	if strings.TrimSpace(ep.Model) == "" {
		return "", fmt.Errorf("未配置%s的模型名称", providerLabel(p))
	}
	timeout := time.Duration(max(timeoutSecs, 1)) * time.Second
	var s string
	var f *Failure
	switch p {
	case model.LlmAnthropic:
		s, f = anthropicComplete(ctx, ep, system, user, timeout, effort)
	case model.LlmOpenAI:
		s, f = openaiComplete(ctx, ep, system, user, timeout, effort)
	default:
		s, f = geminiComplete(ctx, ep, system, user, jsonMode, timeout, effort)
	}
	if f != nil {
		return "", f
	}
	return s, nil
}

func postJSON(ctx context.Context, label, u string, hs []header, body map[string]any, timeout time.Duration) (any, []byte, *Failure) {
	b, _ := json.Marshal(body)
	hs = append(hs, header{"Content-Type", "application/json"})
	resp, f := send(ctx, label, "POST", u, hs, bytes.NewReader(b), timeout)
	if f != nil {
		return nil, nil, f
	}
	var v any
	if json.Unmarshal(resp, &v) != nil {
		return nil, nil, &Failure{Message: label + " 返回了无法解析的响应"}
	}
	mdl, _ := body["model"].(string)
	if mdl == "" {
		if _, after, ok := strings.Cut(u, "/models/"); ok {
			mdl, _, _ = strings.Cut(after, ":")
		}
	}
	Usage(label, mdl, resp, 0)
	return v, resp, nil
}

func mentions(f *Failure, hint string) bool {
	return f.Status == 400 && strings.Contains(strings.ToLower(f.Message), hint)
}

// -- Gemini --

func geminiBase(ep model.LlmEndpointConfig) string {
	b := TrimBase(ep.BaseURL)
	if b == "" {
		b = geminiDefaultBase
	}
	if strings.HasSuffix(b, "/v1beta") || strings.HasSuffix(b, "/v1") {
		return b
	}
	return b + "/v1beta"
}

// geminiThinking: gemini-3 takes thinkingLevel; 2.5 and earlier take thinkingBudget.
func geminiThinking(mdl string, e Effort) map[string]any {
	if strings.Contains(strings.ToLower(mdl), "gemini-3") {
		return map[string]any{"thinkingLevel": e.geminiLevel()}
	}
	return map[string]any{"thinkingBudget": e.geminiBudget()}
}

func geminiModel(ep model.LlmEndpointConfig) string {
	return strings.TrimPrefix(strings.TrimSpace(ep.Model), "models/")
}

func geminiComplete(ctx context.Context, ep model.LlmEndpointConfig, system, user string, jsonMode bool, timeout time.Duration, e Effort) (string, *Failure) {
	mdl := geminiModel(ep)
	u := geminiBase(ep) + "/models/" + mdl + ":generateContent"
	hs := []header{{"x-goog-api-key", strings.TrimSpace(ep.APIKey)}}
	tk := paramKey(model.LlmGemini, mdl, "thinkingConfig")
	withThinking, withMime := !isRejected(tk), jsonMode
	for range 3 {
		gen := map[string]any{"temperature": 0.2}
		if withThinking {
			gen["thinkingConfig"] = geminiThinking(mdl, e)
		}
		if withMime {
			gen["responseMimeType"] = "application/json"
		}
		var safety []any
		for _, c := range []string{"HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH", "HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT"} {
			safety = append(safety, map[string]any{"category": c, "threshold": "BLOCK_NONE"})
		}
		body := map[string]any{
			"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": system}}},
			"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": user}}}},
			"generationConfig":  gen,
			"safetySettings":    safety,
		}
		v, _, f := postJSON(ctx, "Gemini", u, hs, body, timeout)
		switch {
		case f == nil:
			return extractGemini(v)
		case withThinking && mentions(f, "thinking"):
			log.Printf("Gemini model %s rejects thinkingConfig; retrying without it", mdl)
			reject(tk)
			withThinking = false
		case withMime && mentions(f, "mime"):
			withMime = false
		default:
			return "", f
		}
	}
	return "", &Failure{Message: "Gemini 请求失败"}
}

func extractGemini(v any) (string, *Failure) {
	var sb strings.Builder
	if parts, ok := dig(v, "candidates", 0, "content", "parts").([]any); ok {
		for _, p := range parts {
			if th, _ := dig(p, "thought").(bool); th {
				continue
			}
			if t, ok := dig(p, "text").(string); ok {
				sb.WriteString(t)
			}
		}
	}
	if strings.TrimSpace(sb.String()) != "" {
		return sb.String(), nil
	}
	if r, ok := dig(v, "promptFeedback", "blockReason").(string); ok {
		return "", &Failure{Message: "Gemini 拒绝处理该内容（" + r + "）"}
	}
	switch dig(v, "candidates", 0, "finishReason") {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST":
		return "", &Failure{Message: "Gemini 因安全策略拦截了该内容"}
	case "MAX_TOKENS":
		return "", &Failure{Message: "Gemini 输出被截断"}
	}
	return "", &Failure{Message: "Gemini 没有返回内容"}
}

// -- Anthropic --

func anthropicBase(ep model.LlmEndpointConfig) string {
	b := TrimBase(ep.BaseURL)
	if b == "" {
		b = anthropicDefaultBase
	}
	return strings.TrimSuffix(b, "/v1")
}

type anthropicMode struct {
	kind   int // 0 disabled, 1 effort, 2 budget, 3 plain
	effort Effort
	budget int
}

func anthropicComplete(ctx context.Context, ep model.LlmEndpointConfig, system, user string, timeout time.Duration, e Effort) (string, *Failure) {
	mdl := strings.TrimSpace(ep.Model)
	u := anthropicBase(ep) + "/v1/messages"
	hs := []header{{"x-api-key", strings.TrimSpace(ep.APIKey)}, {"anthropic-version", "2023-06-01"}}
	key := func(p string) string { return paramKey(model.LlmAnthropic, mdl, p) }
	// Off: thinking disabled -> effort low -> plain. Others: effort -> legacy budget -> plain.
	for range 5 {
		var m anthropicMode
		switch {
		case e == EffortOff && !isRejected(key("thinking")):
			m = anthropicMode{kind: 0}
		case e == EffortOff && !isRejected(key("effort")):
			m = anthropicMode{kind: 1, effort: EffortLow}
		case e == EffortOff:
			m = anthropicMode{kind: 3}
		case !isRejected(key("effort")):
			m = anthropicMode{kind: 1, effort: e}
		case !isRejected(key("budget")):
			m = anthropicMode{kind: 2, budget: e.anthropicBudget()}
		default:
			m = anthropicMode{kind: 3}
		}
		cache := !isRejected(key("cache_control"))
		body := map[string]any{"model": mdl, "max_tokens": 4096, "messages": []any{map[string]any{"role": "user", "content": user}}}
		if cache {
			body["system"] = []any{map[string]any{"type": "text", "text": system, "cache_control": map[string]any{"type": "ephemeral"}}}
		} else {
			body["system"] = system
		}
		switch m.kind {
		case 0:
			body["thinking"] = map[string]any{"type": "disabled"}
		case 1:
			body["output_config"] = map[string]any{"effort": m.effort.level()}
		case 2:
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": m.budget}
			body["max_tokens"] = m.budget + 4096
		}
		v, _, f := postJSON(ctx, "Anthropic", u, hs, body, timeout)
		switch {
		case f == nil:
			return extractAnthropic(v)
		case cache && mentions(f, "cache_control"):
			reject(key("cache_control"))
		case m.kind == 0 && mentions(f, "thinking"):
			reject(key("thinking"))
		case m.kind == 1 && mentions(f, "effort"):
			reject(key("effort"))
		case m.kind == 2 && (mentions(f, "thinking") || mentions(f, "budget")):
			reject(key("budget"))
		default:
			return "", f
		}
	}
	return "", &Failure{Message: "Anthropic 请求失败"}
}

func extractAnthropic(v any) (string, *Failure) {
	var sb strings.Builder
	if blocks, ok := dig(v, "content").([]any); ok {
		for _, b := range blocks {
			if dig(b, "type") == "text" {
				if t, ok := dig(b, "text").(string); ok {
					sb.WriteString(t)
				}
			}
		}
	}
	if strings.TrimSpace(sb.String()) == "" {
		return "", &Failure{Message: "Anthropic 没有返回内容"}
	}
	return sb.String(), nil
}

// -- OpenAI-compatible --

func openaiBase(ep model.LlmEndpointConfig) string {
	if b := TrimBase(ep.BaseURL); b != "" {
		return b
	}
	return openaiDefaultBase
}

func openaiComplete(ctx context.Context, ep model.LlmEndpointConfig, system, user string, timeout time.Duration, e Effort) (string, *Failure) {
	mdl := strings.TrimSpace(ep.Model)
	u := openaiBase(ep) + "/chat/completions"
	hs := []header{{"Authorization", "Bearer " + strings.TrimSpace(ep.APIKey)}}
	tk, rk := paramKey(model.LlmOpenAI, mdl, "temperature"), paramKey(model.LlmOpenAI, mdl, "reasoning_effort")
	withTemp, withReasoning := !isRejected(tk), e != EffortOff && !isRejected(rk)
	for range 3 {
		body := map[string]any{"model": mdl, "messages": []any{
			map[string]any{"role": "system", "content": system}, map[string]any{"role": "user", "content": user}}}
		if withTemp {
			body["temperature"] = 0.2
		}
		if withReasoning {
			body["reasoning_effort"] = e.level()
		}
		v, _, f := postJSON(ctx, "OpenAI 兼容服务", u, hs, body, timeout)
		switch {
		case f == nil:
			if t := extractContent(v); strings.TrimSpace(t) != "" {
				return t, nil
			}
			return "", &Failure{Message: "OpenAI 兼容服务没有返回内容"}
		case withTemp && mentions(f, "temperature"):
			reject(tk)
			withTemp = false
		case withReasoning && (mentions(f, "reasoning") || mentions(f, "effort")):
			reject(rk)
			withReasoning = false
		default:
			return "", f
		}
	}
	return "", &Failure{Message: "OpenAI 兼容服务请求失败"}
}

// ── models + test ──

func getJSON(ctx context.Context, label, u string, hs []header) (any, *Failure) {
	b, f := send(ctx, label, "GET", u, hs, nil, 15*time.Second)
	if f != nil {
		return nil, f
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil, &Failure{Message: label + " 返回了无法解析的响应"}
	}
	return v, nil
}

func collectIDs(v any, out *[]string) {
	if arr, ok := dig(v, "data").([]any); ok {
		for _, m := range arr {
			if id, ok := dig(m, "id").(string); ok {
				*out = append(*out, id)
			}
		}
	}
}

// ListModels lists the models of a provider, sorted and deduplicated.
func ListModels(ctx context.Context, p model.LlmProvider, cfg model.LlmEndpointConfig) ([]string, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return nil, errors.New("请先填写 API Key")
	}
	var models []string
	switch p {
	case model.LlmGemini:
		hs := []header{{"x-goog-api-key", key}}
		token := ""
		for range 6 {
			u := geminiBase(cfg) + "/models?pageSize=200"
			if token != "" {
				u += "&pageToken=" + url.QueryEscape(token)
			}
			v, f := getJSON(ctx, "Gemini", u, hs)
			if f != nil {
				return nil, f
			}
			if arr, ok := dig(v, "models").([]any); ok {
				for _, m := range arr {
					supports := false
					if ms, ok := dig(m, "supportedGenerationMethods").([]any); ok {
						for _, x := range ms {
							supports = supports || x == "generateContent"
						}
					}
					if name, ok := dig(m, "name").(string); ok && supports {
						models = append(models, strings.TrimPrefix(name, "models/"))
					}
				}
			}
			token, _ = dig(v, "nextPageToken").(string)
			if token == "" {
				break
			}
		}
	case model.LlmAnthropic:
		v, f := getJSON(ctx, "Anthropic", anthropicBase(cfg)+"/v1/models?limit=1000", []header{{"x-api-key", key}, {"anthropic-version", "2023-06-01"}})
		if f != nil {
			return nil, f
		}
		collectIDs(v, &models)
	case model.LlmOpenAI:
		v, f := getJSON(ctx, "OpenAI 兼容服务", openaiBase(cfg)+"/models", []header{{"Authorization", "Bearer " + key}})
		if f != nil {
			return nil, f
		}
		collectIDs(v, &models)
	}
	sort.Strings(models)
	out := models[:0]
	for i, m := range models {
		if i == 0 || m != models[i-1] {
			out = append(out, m)
		}
	}
	return out, nil
}

// TestLlm runs a tiny dictation rewrite with the given effort.
func TestLlm(ctx context.Context, p model.LlmProvider, cfg model.LlmEndpointConfig, e Effort) model.TestResult {
	req := &RewriteRequest{Mode: model.ModeDictation, Transcript: "嗯 你好 世界", Languages: []string{"zh-CN"}, SmartFormatting: true}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := rewriteWith(ctx, p, cfg, req, 20, e)
	ms := uint64(time.Since(start).Milliseconds())
	switch {
	case err == nil:
		return model.TestResult{OK: true, Message: fmt.Sprintf("连接成功（%d ms）：%s", ms, out.Text), LatencyMs: ms}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return model.TestResult{Message: "请求超时", LatencyMs: ms}
	}
	return model.TestResult{Message: err.Error(), LatencyMs: ms}
}
