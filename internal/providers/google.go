package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	speech "cloud.google.com/go/speech/apiv2"
	"cloud.google.com/go/speech/apiv2/speechpb"
	"google.golang.org/api/option"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

// GoogleFinalText keeps the best alternative of final results.
func GoogleFinalText(r *speechpb.StreamingRecognizeResponse) []string {
	var out []string
	for _, res := range r.GetResults() {
		if res.GetIsFinal() && len(res.GetAlternatives()) > 0 {
			out = append(out, res.GetAlternatives()[0].GetTranscript())
		}
	}
	return out
}

func googleStream(ctx context.Context, cfg model.AsrConfig, languages, words []string, frames <-chan []int16) (string, bool, error) {
	c := cfg.Google
	var opts []option.ClientOption
	if strings.TrimSpace(c.ServiceAccountJSON) != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(c.ServiceAccountJSON)))
	} else {
		opts = append(opts, option.WithAPIKey(strings.TrimSpace(c.APIKey)))
	}
	if !validRegion(c.Location) {
		return "", false, errors.New("Google 区域格式不正确")
	}
	ep := "speech.googleapis.com:443"
	if c.Location != "global" {
		ep = c.Location + "-speech.googleapis.com:443"
	}
	opts = append(opts, option.WithEndpoint(ep))
	ctx, cancel := context.WithTimeout(ctx, 275*time.Second)
	defer cancel()
	client, err := speech.NewClient(ctx, opts...)
	if err != nil {
		return "", false, ServiceError("Google", err)
	}
	defer client.Close()
	conf := &speechpb.RecognitionConfig{
		Model:         c.Model,
		LanguageCodes: languages[:min(len(languages), 3)],
		DecodingConfig: &speechpb.RecognitionConfig_ExplicitDecodingConfig{ExplicitDecodingConfig: &speechpb.ExplicitDecodingConfig{
			Encoding: speechpb.ExplicitDecodingConfig_LINEAR16, SampleRateHertz: 16000, AudioChannelCount: 1}},
	}
	if len(words) > 0 {
		var phrases []*speechpb.PhraseSet_Phrase
		for _, w := range words[:min(len(words), 100)] {
			phrases = append(phrases, &speechpb.PhraseSet_Phrase{Value: w, Boost: 10})
		}
		conf.Adaptation = &speechpb.SpeechAdaptation{PhraseSets: []*speechpb.SpeechAdaptation_AdaptationPhraseSet{{
			Value: &speechpb.SpeechAdaptation_AdaptationPhraseSet_InlinePhraseSet{InlinePhraseSet: &speechpb.PhraseSet{Phrases: phrases}}}}}
	}
	stream, err := client.StreamingRecognize(ctx)
	if err != nil {
		return "", false, ServiceError("Google", err)
	}
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		Recognizer:       fmt.Sprintf("projects/%s/locations/%s/recognizers/_", c.ProjectID, c.Location),
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{StreamingConfig: &speechpb.StreamingRecognitionConfig{Config: conf}},
	}); err != nil {
		return "", false, ServiceError("Google", err)
	}
	type result struct {
		text string
		err  error
	}
	recv := make(chan result, 1)
	go func() {
		var text []string
		for {
			r, err := stream.Recv()
			if err == io.EOF {
				recv <- result{JoinTranscripts(text), nil}
				return
			}
			if err != nil {
				recv <- result{"", ServiceError("Google", err)}
				return
			}
			text = append(text, GoogleFinalText(r)...)
		}
	}()
	count, ended := 0, false
send:
	for count < segmentSamples {
		select {
		case f, ok := <-frames:
			if !ok {
				ended = true
				break send
			}
			count += len(f)
			// Keep each gRPC audio message below the 25 KB limit.
			for i := 0; i < len(f); i += 8000 {
				if err := stream.Send(&speechpb.StreamingRecognizeRequest{StreamingRequest: &speechpb.StreamingRecognizeRequest_Audio{Audio: pcmBytes(f[i:min(i+8000, len(f))])}}); err != nil {
					return "", false, ServiceError("Google", err)
				}
			}
		case r := <-recv:
			if r.err != nil {
				return "", false, r.err
			}
			return "", false, errors.New("流式识别在音频发送结束前中断，请重试")
		case <-ctx.Done():
			return "", false, errors.New("Google 流式识别超时")
		}
	}
	_ = stream.CloseSend()
	select {
	case r := <-recv:
		if r.err != nil {
			return "", false, r.err
		}
		Usage("Google Speech", c.Model, []byte("{}"), float64(count)/16000)
		return r.text, ended, nil
	case <-time.After(30 * time.Second):
		return "", false, errors.New("等待最终识别结果超时")
	}
}
