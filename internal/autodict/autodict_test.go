package autodict

import "testing"

func TestRegion(t *testing.T) {
	if r, ok := EditedRegion("请把今天安排的三项任务做完之后发给我", "前文。请把今天安排的两项任务做完之后发给我。后文"); !ok || r != "请把今天安排的两项任务做完之后发给我" {
		t.Fatal(r)
	}
	if _, ok := EditedRegion("please open the dashboard now", "unrelated field"); ok {
		t.Fatal()
	}
	if _, ok := EditedRegion("please open the dashboard now", "please open the dashboard now; please open the dashboard now"); ok {
		t.Fatal()
	}
}

func TestCorrections(t *testing.T) {
	for _, c := range []struct{ ins, fld, want string }{
		{"I use Hoshira to dictate every day", "I use Hoshiro to dictate every day", "Hoshiro"},
		{"please open the Kubernets dashboard now", "Dear team,\nplease open the Kubernetes dashboard now\nthanks", "Kubernetes"},
		{"I bought a new iphone yesterday", "I bought a new iPhone yesterday", "iPhone"},
		{"我在用星城输入法写东西", "我在用星辰输入法写东西", "星辰"},
	} {
		if got, ok := DetectCorrection(c.ins, c.fld); !ok || got != c.want {
			t.Error(c.ins, got)
		}
	}
	for _, c := range [][2]string{
		{"hello there my friend", "hello there my friend"},
		{"hello there my friend", "hello where my fiend"},
		{"hello there my friend", "something totally different"},
		{"I saw a dog yesterday there", "I saw a cat yesterday there"},
		{"send it to Alexander today", "send it to Bob today"},
		{"hello there my dear friend", "hello there my friend"},
	} {
		if got, ok := DetectCorrection(c[0], c[1]); ok {
			t.Error(c, got)
		}
	}
}
