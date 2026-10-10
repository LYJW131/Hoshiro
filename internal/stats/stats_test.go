package stats

import (
	"testing"
	"time"
)

func TestCountWords(t *testing.T) {
	cases := map[string]uint32{"": 0, "hello world": 2, "你好世界": 4, "don't stop": 2, "GPT-4 是 AI": 4, "  a,b  ": 2, "こんにちは": 5}
	for in, want := range cases {
		if got := CountWords(in); got != want {
			t.Errorf("%q: got %d want %d", in, got, want)
		}
	}
}

func d(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

func TestStreaksAndHeatmap(t *testing.T) {
	today := d("2026-10-10")
	rows := []DayRow{{d("2026-10-10"), 60, 100}, {d("2026-10-09"), 60, 50}, {d("2026-10-01"), 0, 10},
		{d("2026-10-02"), 30, 10}, {d("2026-10-03"), 30, 10}}
	s := ComputeDays(rows, today)
	if s.CurrentStreak != 2 || s.LongestStreak != 3 || s.ActiveDays != 5 || s.TotalWords != 180 {
		t.Fatalf("%+v", s)
	}
	if s.Heatmap[len(s.Heatmap)-1].Date != "2026-10-10" || s.Heatmap[len(s.Heatmap)-1].Words != 100 {
		t.Fatal("heatmap end")
	}
	if first := d(s.Heatmap[0].Date); first.Weekday() != time.Sunday {
		t.Fatal("heatmap must start on Sunday")
	}
	if s.AverageWPM != 180.0/3 {
		t.Fatalf("wpm %v", s.AverageWPM)
	}
}

func TestCurrentStreakFromYesterday(t *testing.T) {
	s := ComputeDays([]DayRow{{d("2026-10-09"), 1, 1}}, d("2026-10-10"))
	if s.CurrentStreak != 1 {
		t.Fatal(s.CurrentStreak)
	}
}

func TestPercentile(t *testing.T) {
	v := []uint64{100, 200, 300, 400}
	if Percentile(v, 0.5) != 250 || Percentile(v, 0.9) != 370 || Percentile(nil, 0.5) != 0 {
		t.Fatal("percentile")
	}
	l := Latency([]uint64{3, 1, 2})
	if l.MedianMs != 2 || l.MeanMs != 2 || l.Samples != 3 {
		t.Fatalf("%+v", l)
	}
}

func TestTokens(t *testing.T) {
	in, out := Tokens([]byte(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3}}`))
	if *in != 13 || *out != 5 {
		t.Fatal("anthropic")
	}
	in, out = Tokens([]byte(`{"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2,"thoughtsTokenCount":4}}`))
	if *in != 7 || *out != 6 {
		t.Fatal("gemini")
	}
	in, out = Tokens([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}`))
	if *in != 1 || *out != 2 || *CachedTokens([]byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":1}}}`)) != 1 {
		t.Fatal("openai")
	}
	if in, out = Tokens([]byte(`{}`)); in != nil || out != nil {
		t.Fatal("empty")
	}
	if Classify("Azure") != KindAsr || Classify("OpenAI") != KindLlm {
		t.Fatal("classify")
	}
}
