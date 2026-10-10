package providers

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/model"
)

// segmentSamples rotates a stream before the services' five-minute limits.
const segmentSamples = 240 * 16_000

func isCJKRune(c rune) bool {
	return unicode.Is(unicode.Han, c) || unicode.Is(unicode.Hiragana, c) || unicode.Is(unicode.Katakana, c) || unicode.Is(unicode.Hangul, c) ||
		strings.ContainsRune("，。！？、；：（）《》「」『』“”‘’…", c)
}

// JoinTranscripts joins parts with a space between Latin words, none around CJK text.
func JoinTranscripts(parts []string) string {
	var sb strings.Builder
	var last rune
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		first := []rune(p)[0]
		if sb.Len() > 0 && !(isCJKRune(last) || isCJKRune(first)) && !unicode.IsSpace(last) {
			sb.WriteByte(' ')
		}
		sb.WriteString(p)
		r := []rune(p)
		last = r[len(r)-1]
	}
	return sb.String()
}

// Stream is a live recognition: send 16 kHz PCM frames, then Close and Wait.
type Stream struct {
	frames chan []int16
	done   chan struct{}
	text   string
	err    error
	cancel context.CancelFunc
}

func (s *Stream) Send(frame []int16) bool {
	select {
	case s.frames <- frame:
		return true
	case <-s.done:
		return false
	}
}

// Close ends the audio; Wait returns the transcript.
func (s *Stream) Close()                { defer func() { recover() }(); close(s.frames) }
func (s *Stream) Abort()                { s.cancel() }
func (s *Stream) Wait() (string, error) { <-s.done; return s.text, s.err }

// StartStream starts a streaming recognizer (Azure or Google).
func StartStream(ctx context.Context, cfg model.AsrConfig, languages, words []string) *Stream {
	ctx, cancel := context.WithCancel(ctx)
	s := &Stream{frames: make(chan []int16, 4096), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(s.done)
		defer cancel()
		var parts []string
		for {
			var text string
			var ended bool
			var err error
			switch cfg.Provider {
			case "google":
				text, ended, err = googleStream(ctx, cfg, languages, words, s.frames)
			case "azure":
				text, ended, err = azure(ctx, cfg, languages, words, s.frames)
			default:
				err = errors.New("该服务不支持流式识别")
			}
			if err != nil {
				s.err = err
				return
			}
			parts = append(parts, text)
			if ended {
				s.text = JoinTranscripts(parts)
				return
			}
		}
	}()
	return s
}

// TranscribeSamples streams recorded samples at about real time (as the services require).
func TranscribeSamples(ctx context.Context, cfg model.AsrConfig, languages, words []string, samples []int16) (string, error) {
	s := StartStream(ctx, cfg, languages, words)
	defer s.Abort()
	for i := 0; i < len(samples); i += 3200 {
		if !s.Send(samples[i:min(i+3200, len(samples))]) {
			break
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	s.Close()
	return s.Wait()
}

func pcmBytes(s []int16) []byte {
	b := make([]byte, len(s)*2)
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

// ServiceError maps a streaming error to a Chinese hint.
func ServiceError(provider string, err error) error {
	t := strings.ToLower(fmt.Sprint(err))
	hint := "连接失败或超时，请检查网络和服务配置后重试"
	switch {
	case strings.Contains(t, "401") || strings.Contains(t, "403") || strings.Contains(t, "unauthenticated") || strings.Contains(t, "permission"):
		hint = "鉴权失败，请检查密钥、服务账户权限与所属项目/区域"
	case strings.Contains(t, "429") || strings.Contains(t, "exhausted") || strings.Contains(t, "quota"):
		hint = "额度不足或请求过多，请检查配额后重试"
	case strings.Contains(t, "invalid") || strings.Contains(t, "400") || strings.Contains(t, "404"):
		hint = "配置不正确，请检查区域、语言、模型及项目 ID"
	}
	return fmt.Errorf("%s：%s", provider, hint)
}

// ── Azure (Speech SDK WebSocket framing) ──

func azureText(path, id string, body any) []byte {
	b, _ := json.Marshal(body)
	return []byte(fmt.Sprintf("Path:%s\r\nX-RequestId:%s\r\nX-Timestamp:%s\r\nContent-Type:application/json\r\n\r\n%s",
		path, id, time.Now().UTC().Format(time.RFC3339Nano), b))
}

func azureAudio(id string, data []byte) []byte {
	h := fmt.Sprintf("Path:audio\r\nX-RequestId:%s\r\nX-Timestamp:%s\r\nContent-Type:audio/x-wav\r\n", id, time.Now().UTC().Format(time.RFC3339Nano))
	p := make([]byte, 2, 2+len(h)+len(data))
	binary.BigEndian.PutUint16(p, uint16(len(h)))
	return append(append(p, h...), data...)
}

func azurePath(msg string) (path, body string) {
	headers, body, _ := strings.Cut(msg, "\r\n\r\n")
	for _, l := range strings.Split(headers, "\r\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "path") {
			return strings.TrimSpace(v), body
		}
	}
	return "", body
}

// AzurePhrase extracts the text of a speech.phrase message.
func AzurePhrase(msg string) (string, bool, error) {
	path, body := azurePath(msg)
	if !strings.EqualFold(path, "speech.phrase") || !strings.Contains(msg, "\r\n\r\n") {
		return "", false, nil
	}
	var v map[string]any
	if json.Unmarshal([]byte(body), &v) != nil {
		return "", false, errors.New("Azure 返回了无法解析的识别结果")
	}
	switch v["RecognitionStatus"] {
	case "Success":
		if s, ok := v["DisplayText"].(string); ok {
			return s, true, nil
		}
		if s, ok := dig(v, "NBest", 0, "Display").(string); ok {
			return s, true, nil
		}
		return "", false, nil
	case "NoMatch", "InitialSilenceTimeout", "BabbleTimeout", "EndOfDictation":
		return "", false, nil
	}
	return "", false, errors.New("Azure 语音识别失败，请检查语言和服务配置")
}

func validRegion(s string) bool {
	for _, c := range s {
		if !(c < 128 && (unicode.IsLetter(c) || unicode.IsDigit(c)) || c == '-') {
			return false
		}
	}
	return true
}

func azure(ctx context.Context, cfg model.AsrConfig, languages, words []string, frames <-chan []int16) (string, bool, error) {
	if !validRegion(cfg.Azure.Region) {
		return "", false, errors.New("Azure 区域格式不正确")
	}
	lang := "zh-CN"
	if len(languages) > 0 {
		lang = languages[0]
	}
	path := "/speech/recognition/conversation/cognitiveservices/v1"
	q := url.Values{}
	if len(languages) > 1 {
		path = "/stt/speech/universal/v2"
	} else {
		q.Set("language", lang)
	}
	q.Set("format", "simple")
	u := "wss://" + cfg.Azure.Region + ".stt.speech.microsoft.com" + path + "?" + q.Encode()
	return azureAt(ctx, cfg, languages, words, frames, u)
}

func azureAt(ctx context.Context, cfg model.AsrConfig, languages, words []string, frames <-chan []int16, u string) (string, bool, error) {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPHeader: http.Header{
		"Ocp-Apim-Subscription-Key": {strings.TrimSpace(cfg.Azure.APIKey)}, "X-ConnectionId": {id}}})
	cancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", false, errors.New("Azure 连接超时")
		}
		return "", false, ServiceError("Azure", err)
	}
	conn.SetReadLimit(1 << 22)
	defer conn.CloseNow()
	ctx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	write := func(t websocket.MessageType, b []byte) error {
		if err := conn.Write(ctx, t, b); err != nil {
			return ServiceError("Azure", err)
		}
		return nil
	}
	items := []any{}
	for _, w := range words[:min(len(words), 100)] {
		items = append(items, map[string]any{"text": w})
	}
	sctx := map[string]any{"phraseDetection": map[string]any{"mode": "Conversation"},
		"dgi": map[string]any{"groups": []any{map[string]any{"type": "Generic", "items": items}}}}
	if len(languages) > 1 {
		sctx["languageId"] = map[string]any{"languages": languages[:min(len(languages), 4)], "mode": "DetectAtAudioStart",
			"onSuccess": map[string]any{"action": "Recognize"}, "onUnknown": map[string]any{"action": "None"}, "priority": "PrioritizeLatency"}
	}
	header := audio.EncodeWAV(nil, 16000)
	if err := write(websocket.MessageText, azureText("speech.config", id, map[string]any{"context": map[string]any{
		"system": map[string]any{"name": "Hoshiro", "version": Version}, "os": map[string]any{"platform": runtime.GOOS}}})); err != nil {
		return "", false, err
	}
	if err := write(websocket.MessageText, azureText("speech.context", id, sctx)); err != nil {
		return "", false, err
	}
	if err := write(websocket.MessageBinary, azureAudio(id, header)); err != nil {
		return "", false, err
	}

	type msg struct {
		t   websocket.MessageType
		b   []byte
		err error
	}
	incoming := make(chan msg, 16)
	go func() {
		for {
			t, b, err := conn.Read(ctx)
			incoming <- msg{t, b, err}
			if err != nil {
				return
			}
		}
	}()
	var pcm []int16
	var parts []string
	ended, sending := false, true
	turnBase, confirmed := 0, 0
	deadline := time.NewTimer(275 * time.Second)
	defer deadline.Stop()
	in := frames
	for {
		select {
		case <-deadline.C:
			return "", false, errors.New("Azure 流式识别超时")
		case <-ctx.Done():
			return "", false, ctx.Err()
		case f, ok := <-in:
			if ok {
				if err := write(websocket.MessageBinary, azureAudio(id, pcmBytes(f))); err != nil {
					return "", false, err
				}
				pcm = append(pcm, f...)
			} else {
				ended = true
			}
			if ended || len(pcm) >= segmentSamples {
				sending, in = false, nil
				if err := write(websocket.MessageBinary, azureAudio(id, nil)); err != nil {
					return "", false, err
				}
				deadline.Reset(30 * time.Second)
			}
		case m := <-incoming:
			if m.err != nil {
				if websocket.CloseStatus(m.err) != -1 {
					return "", false, errors.New("Azure 连接在返回完整结果前关闭")
				}
				return "", false, ServiceError("Azure", m.err)
			}
			if m.t != websocket.MessageText {
				continue
			}
			text := string(m.b)
			p, ok, err := AzurePhrase(text)
			if err != nil {
				return "", false, err
			}
			if ok {
				parts = append(parts, p)
			}
			path, body := azurePath(text)
			if strings.EqualFold(path, "speech.phrase") {
				var v struct{ Offset, Duration *uint64 }
				if json.Unmarshal([]byte(body), &v) == nil && v.Offset != nil && v.Duration != nil {
					// Offsets are 100 ns ticks relative to this request's audio.
					confirmed = max(confirmed, min(turnBase+int((*v.Offset+*v.Duration)*16000/10_000_000), len(pcm)))
				}
			}
			if strings.EqualFold(path, "turn.end") {
				if !sending {
					Usage("Azure Speech", "streaming", []byte("{}"), float64(len(pcm))/16000)
					return JoinTranscripts(parts), ended, nil
				}
				// The service ended a turn while recording continues: replay unacknowledged audio.
				if confirmed == turnBase {
					return "", false, errors.New("Azure 未确认当前音频，请重试")
				}
				turnBase = confirmed
				id = strings.ReplaceAll(uuid.NewString(), "-", "")
				if err := write(websocket.MessageText, azureText("speech.context", id, sctx)); err != nil {
					return "", false, err
				}
				if err := write(websocket.MessageBinary, azureAudio(id, header)); err != nil {
					return "", false, err
				}
				for i := confirmed; i < len(pcm); i += 3200 {
					if err := write(websocket.MessageBinary, azureAudio(id, pcmBytes(pcm[i:min(i+3200, len(pcm))]))); err != nil {
						return "", false, err
					}
				}
			}
		}
	}
}
