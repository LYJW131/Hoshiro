package providers

import (
	"strings"
	"testing"

	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func TestRelayGeminiShapes(t *testing.T) {
	base, s := serve(t,
		200, `{"candidates":[{"content":{"parts":[{"text":"思考中","thought":true},{"text":"{\"action\":\"answer\",\"text\":\"42\"}"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
		200, `{"promptFeedback":{"blockReason":"OTHER"},"candidates":[]}`,
		200, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`,
		200, `{"candidates":[{"content":{"parts":[]},"finishReason":"MAX_TOKENS"}]}`)
	ep := model.LlmEndpointConfig{APIKey: "gk", BaseURL: base, Model: "models/gemini-3-pro-preview"}
	var usage []string
	old := Usage
	Usage = func(p, m string, b []byte, _ float64) { usage = append(usage, p+"/"+m) }
	defer func() { Usage = old }()
	o, err := rewriteWith(bg, model.LlmGemini, ep, req(model.ModeAsk, "六乘七"), 10, EffortHigh)
	if err != nil || o != (Outcome{Answer, "42"}) {
		t.Fatal(o, err)
	}
	b := body(s.reqs[0])
	gen := b["generationConfig"].(map[string]any)
	if gen["thinkingConfig"].(map[string]any)["thinkingLevel"] != "high" || gen["temperature"] != 0.2 ||
		len(b["safetySettings"].([]any)) != 4 || b["systemInstruction"] == nil || !strings.Contains(s.reqs[0], "/models/gemini-3-pro-preview:generateContent") {
		t.Fatal(s.reqs[0])
	}
	if len(usage) != 1 || usage[0] != "Gemini/gemini-3-pro-preview" {
		t.Fatal(usage)
	}
	for _, want := range []string{"拒绝处理该内容（OTHER）", "安全策略", "截断"} {
		if _, err := rewriteWith(bg, model.LlmGemini, ep, req(model.ModeDictation, "好"), 10, EffortOff); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
}

func TestRelaySkipsReasoning(t *testing.T) {
	base, _ := serve(t, 200, `{"content":[{"type":"thinking","thinking":"hmm","signature":"x"},{"type":"text","text":"你好世界"}],"stop_reason":"end_turn"}`)
	o, err := rewriteWith(bg, model.LlmAnthropic, model.LlmEndpointConfig{APIKey: "k", BaseURL: base, Model: "claude-relay"}, req(model.ModeDictation, "你好 世界"), 10, EffortOff)
	if err != nil || o.Text != "你好世界" {
		t.Fatal(o, err)
	}
	base, _ = serve(t, 200, `{"choices":[{"message":{"role":"assistant","content":"Hi","reasoning_content":"thinking"}}]}`)
	o, err = rewriteWith(bg, model.LlmOpenAI, model.LlmEndpointConfig{APIKey: "k", BaseURL: base, Model: "o-relay"}, req(model.ModeTranslation, "你好"), 10, EffortOff)
	if err != nil || o.Text != "Hi" {
		t.Fatal(o, err)
	}
}

func TestRelayConversion(t *testing.T) {
	for _, f := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		if _, err := toProvider(f, chatRequest{Model: "m", System: "s", User: "u", JSON: true}); err != nil {
			t.Fatal(f, err)
		}
	}
	if _, err := fromProvider(types.RelayFormatClaude, []byte("not json")); err == nil {
		t.Fatal("bad json accepted")
	}
}
