package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// The language-model calls go through RelayKit (github.com/QuantumNous/new-api/relaykit):
// every prompt is one OpenAI Chat Completions request, which RelayKit converts
// to the provider's protocol (Claude Messages, Gemini generateContent, or
// OpenAI Chat as is); the provider's reply is converted back to an OpenAI
// Chat response, from which the text is read. Provider quirks RelayKit does
// not model (Anthropic's output_config effort and prompt caching, Gemini 3's
// thinkingLevel, safety settings) and the parameter fallbacks are applied to
// the converted request DTO.

// chatRequest is the canonical request.
type chatRequest struct {
	Model, System, User string
	Temperature         *float64
	ReasoningEffort     string // OpenAI's: low | medium | high; empty for none
	JSON                bool
}

func temp(v float64) *float64 { return &v }

// toProvider converts a chat request to the DTO of format f.
func toProvider(f types.RelayFormat, r chatRequest) (any, error) {
	req := &dto.GeneralOpenAIRequest{
		Model:           r.Model,
		Messages:        []dto.Message{{Role: "system", Content: r.System}, {Role: "user", Content: r.User}},
		Temperature:     r.Temperature,
		ReasoningEffort: r.ReasoningEffort,
	}
	if r.JSON {
		req.ResponseFormat = &dto.ResponseFormat{Type: "json_object"}
	}
	meta := &convmeta.Values{OriginModelName: r.Model, UpstreamModelName: r.Model, ChannelMetaAttached: true,
		Options: &convmeta.Options{Claude: convmeta.ClaudeOptions{DefaultMaxTokens: func(string) int { return 4096 }}}}
	res, err := relayconvert.ConvertRequest(context.Background(), meta, f, req)
	if err != nil {
		return nil, fmt.Errorf("relaykit: %w", err)
	}
	return res.Value, nil
}

// fromProvider decodes a reply of format f and converts it to OpenAI Chat;
// it returns the answer text (without reasoning) and the response.
func fromProvider(f types.RelayFormat, raw []byte) (string, error) {
	var v any
	switch f {
	case types.RelayFormatClaude:
		v = &dto.ClaudeResponse{}
	case types.RelayFormatGemini:
		v = &dto.GeminiChatResponse{}
	default:
		v = &dto.OpenAITextResponse{}
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return "", err
	}
	if f != types.RelayFormatOpenAI {
		res, err := relayconvert.ConvertResponse(context.Background(), &convmeta.Values{ChannelMetaAttached: true}, types.RelayFormatOpenAI, v)
		if err != nil {
			return "", fmt.Errorf("relaykit: %w", err)
		}
		v = res.Value
	}
	resp, ok := v.(*dto.OpenAITextResponse)
	if !ok {
		return "", fmt.Errorf("relaykit: unexpected %T", v)
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	return resp.Choices[0].Message.StringContent(), nil
}

// postRelay sends a converted request and returns the raw reply.
func postRelay(ctx context.Context, label, u string, hs []header, body any, mdl string, timeout time.Duration) ([]byte, *Failure) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, &Failure{Message: label + " 请求无法编码"}
	}
	hs = append(hs, header{"Content-Type", "application/json"})
	resp, f := send(ctx, label, "POST", u, hs, bytes.NewReader(b), timeout)
	if f != nil {
		return nil, f
	}
	if !json.Valid(resp) {
		return nil, &Failure{Message: label + " 返回了无法解析的响应"}
	}
	Usage(label, mdl, resp, 0)
	return resp, nil
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }
