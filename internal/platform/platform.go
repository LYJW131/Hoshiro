// Package platform is the OS integration layer: permissions, text insertion,
// focused-app context, the global hotkey tap, output muting, app icons and
// the floating bar panel. macOS is implemented (with purego, no cgo); other
// systems get stubs so the rest of the app builds and tests everywhere.
package platform

import (
	"time"
	"unicode/utf16"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

// RunOnMain runs f on the main (UI) thread and waits. The app sets it to mygo.RunOnMain.
var RunOnMain = func(f func()) { f() }

// onMainTimeout runs f on the main thread but gives up waiting after d.
func onMainTimeout[T any](d time.Duration, f func() T) (T, bool) {
	ch := make(chan T, 1)
	go RunOnMain(func() { ch <- f() })
	select {
	case v := <-ch:
		return v, true
	case <-time.After(d):
		var z T
		return z, false
	}
}

// SettingsURL is the System Settings pane for a permission.
func SettingsURL(kind model.PermissionKind) string {
	switch kind {
	case model.PermMicrophone:
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone"
	case model.PermAccessibility:
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	default:
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent"
	}
}

const KeyboardSettingsURL = "x-apple.systempreferences:com.apple.Keyboard-Settings.extension"

// WindowAround returns up to max UTF-16 units of text around caret (a UTF-16
// offset), biased towards the text before it, never splitting surrogate pairs.
func WindowAround(text string, caret, max int) string {
	u := utf16.Encode([]rune(text))
	if len(u) <= max {
		return text
	}
	caret = min(caret, len(u))
	start := max0(caret - max*2/3)
	end := start + max
	if end > len(u) {
		end = len(u)
		start = end - max
	}
	if start < len(u) && u[start] >= 0xDC00 && u[start] < 0xE000 {
		start++
	}
	if end > start && u[end-1] >= 0xD800 && u[end-1] < 0xDC00 {
		end--
	}
	return string(utf16.Decode(u[start:end]))
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// HitRect is an interactive region of the bar, in window points from the top-left.
type HitRect struct{ X, Y, Width, Height float64 }

// PointInRects is the bar's hit test.
func PointInRects(x, y float64, rects []HitRect, slop float64) bool {
	for _, r := range rects {
		if x >= r.X-slop && x <= r.X+r.Width+slop && y >= r.Y-slop && y <= r.Y+r.Height+slop {
			return true
		}
	}
	return false
}

// BottomCenter places a window of width w at the bottom centre of the visible frame (AppKit coordinates).
func BottomCenter(vx, vy, vw, w, gap float64) (float64, float64) { return vx + (vw-w)/2, vy + gap }
