// Package providers holds the speech-recognition and LLM clients. User-facing
// messages are Chinese, as in the original app.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

var Version = "0.1.1"

// Client is the shared pooled HTTP client.
var Client = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 60 * time.Second}).DialContext,
		IdleConnTimeout:     300 * time.Second,
		MaxIdleConnsPerHost: 8,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

// Usage records provider-reported usage; set by the app.
var Usage = func(provider, model string, resp []byte, audioSecs float64) {}

// Failure is a provider error with a user-facing message.
type Failure struct {
	Message   string
	Retryable bool
	Status    int // 0 when not an HTTP status
}

func (f *Failure) Error() string { return f.Message }

func networkFailure(provider string, err error) *Failure {
	msg := "网络连接失败"
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		msg = provider + " 请求超时"
	case errors.Is(err, io.ErrUnexpectedEOF):
		msg = provider + " 响应读取失败"
	}
	log.Printf("%s network error: %v", provider, err)
	return &Failure{Message: msg, Retryable: true}
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// APIMessage pulls error.message (OpenAI/Anthropic/Gemini style) or message out of an error body.
func APIMessage(body string) (string, bool) {
	var v map[string]any
	if json.Unmarshal([]byte(body), &v) != nil {
		return "", false
	}
	var msg string
	if e, ok := v["error"].(map[string]any); ok {
		msg, _ = e["message"].(string)
	}
	if msg == "" {
		msg, _ = v["message"].(string)
	}
	if msg == "" {
		msg, _ = v["error"].(string)
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", false
	}
	return truncRunes(msg, 200), true
}

func withDetail(summary, detail string, ok bool) string {
	if !ok {
		return summary
	}
	return summary + "：" + truncRunes(detail, 90)
}

func StatusFailure(provider string, status int, body string) *Failure {
	d, ok := APIMessage(body)
	lower := strings.ToLower(body)
	f := &Failure{Status: status}
	switch {
	case status == 401:
		f.Message = withDetail(provider+" API Key 无效", d, ok)
	case status == 403:
		f.Message = withDetail(provider+" 拒绝访问", d, ok)
	case status == 404:
		f.Message = withDetail(provider+" 接口或模型不存在", d, ok)
	case status == 402:
		f.Message = withDetail(provider+" 账户余额不足", d, ok)
	case status == 408:
		f.Message, f.Retryable = provider+" 请求超时", true
	case status == 413:
		f.Message = "音频过长，请缩短录音"
	case status == 429:
		if strings.Contains(lower, "quota") || strings.Contains(lower, "balance") || strings.Contains(lower, "insufficient") {
			f.Message = provider + " 额度已用尽"
		} else {
			f.Message, f.Retryable = provider+" 请求过于频繁，请稍后再试", true
		}
	case status >= 500 && status <= 599:
		f.Message, f.Retryable = fmt.Sprintf("%s 服务暂时不可用（HTTP %d）", provider, status), true
	case ok:
		f.Message = provider + " 请求被拒绝：" + d
	default:
		f.Message = fmt.Sprintf("%s 请求失败（HTTP %d）", provider, status)
	}
	log.Printf("%s HTTP %d: %s", provider, status, truncRunes(body, 300))
	return f
}

func TrimBase(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

type header struct{ k, v string }

// send performs one request and returns the body of a 2xx response.
func send(ctx context.Context, label, method, url string, headers []header, body io.Reader, timeout time.Duration) ([]byte, *Failure) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, &Failure{Message: label + " 请求地址不正确"}
	}
	req.Header.Set("User-Agent", "Hoshiro/"+Version)
	for _, h := range headers {
		req.Header.Set(h.k, h.v)
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, networkFailure(label, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, networkFailure(label, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, StatusFailure(label, resp.StatusCode, string(b))
	}
	return b, nil
}
