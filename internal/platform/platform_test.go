package platform

import (
	"strings"
	"testing"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

func TestWindowAround(t *testing.T) {
	if WindowAround("hello", 2, 1500) != "hello" {
		t.Fatal()
	}
	var b strings.Builder
	for i := range 5000 {
		b.WriteByte(byte('a' + i%26))
	}
	text := b.String()
	w := WindowAround(text, 2500, 1500)
	if len(w) != 1500 || !strings.HasPrefix(w, text[1500:1510]) {
		t.Fatal(len(w))
	}
	x := strings.Repeat("x", 4000)
	for _, c := range []int{0, 4000, int(^uint(0) >> 1)} {
		if len(WindowAround(x, c, 1500)) != 1500 {
			t.Fatal(c)
		}
	}
	e := strings.Repeat("😀", 2000)
	for _, c := range []int{0, 1, 999, 1000, 1001, 3999} {
		w := WindowAround(e, c, 1501)
		if strings.ContainsRune(w, '\uFFFD') || strings.Trim(w, "😀") != "" {
			t.Fatal(c)
		}
	}
}

func TestHitAndPlacement(t *testing.T) {
	rects := []HitRect{{100, 400, 120, 30}, {10, 10, 50, 50}}
	if !PointInRects(150, 410, rects, 0) || !PointInRects(30, 30, rects, 0) || PointInRects(150, 300, rects, 0) ||
		PointInRects(99.5, 410, rects, 0) || !PointInRects(99.5, 410, rects, 1) || PointInRects(1, 1, nil, 1) {
		t.Fatal()
	}
	if x, y := BottomCenter(0, 70, 1440, 560, 0); x != 440 || y != 70 {
		t.Fatal(x, y)
	}
	if x, y := BottomCenter(-1920, 0, 1920, 560, 2); x != -1920+680 || y != 2 {
		t.Fatal(x, y)
	}
	if !strings.HasSuffix(SettingsURL(model.PermMicrophone), "Privacy_Microphone") ||
		!strings.HasSuffix(SettingsURL(model.PermAccessibility), "Privacy_Accessibility") ||
		!strings.HasSuffix(SettingsURL(model.PermInputMonitoring), "Privacy_ListenEvent") {
		t.Fatal()
	}
}
