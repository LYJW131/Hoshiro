//go:build darwin

package platform

import (
	"errors"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/macos"
	"github.com/ebitengine/purego/objc"
)

const (
	panelLevel = 101 // NSPopUpMenuWindowLevel: above the menu bar, Dock and full-screen chrome
	hitSlop    = 1.0
	// NSWindowCollectionBehavior: CanJoinAllSpaces | Stationary | IgnoresCycle | FullScreenAuxiliary
	panelBehavior           = 1<<0 | 1<<4 | 1<<6 | 1<<8
	styleNonactivatingPanel = 1 << 7
)

var (
	barMu          sync.Mutex
	barWin         macos.ID
	barRects       []HitRect
	barFrame       macos.Rect
	barVisible     bool
	barInteractive bool
	barPolling     bool

	panelClassOnce sync.Once
	panelClass     objc.Class
)

// HoshiroBarPanel is an NSPanel that never becomes key or main, so clicking
// the bar never activates the app or steals focus from where the user types.
func barPanelClass() objc.Class {
	panelClassOnce.Do(func() {
		no := func(_ objc.ID, _ objc.SEL) bool { return false }
		c, err := objc.RegisterClass("HoshiroBarPanel", objc.GetClass("NSPanel"), nil, nil, []objc.MethodDef{
			{Cmd: objc.RegisterName("canBecomeKeyWindow"), Fn: no},
			{Cmd: objc.RegisterName("canBecomeMainWindow"), Fn: no},
		})
		if err != nil {
			log.Printf("registering the bar panel class: %v", err)
			return
		}
		panelClass = c
	})
	return panelClass
}

var objectSetClass func(obj objc.ID, cls objc.Class) objc.Class

// SetupBar turns the window (an NSWindow* from mygo.Window.NativeHandle) into
// a non-activating, all-spaces panel above everything that ignores the mouse
// outside the hit rects. Runs on the main thread.
func SetupBar(handle uintptr) error {
	if handle == 0 {
		return errors.New("bar window has no native handle")
	}
	cls := barPanelClass()
	if cls == 0 {
		return errors.New("could not register the panel class")
	}
	var err error
	RunOnMain(func() {
		w := macos.ID(handle)
		// MyGoWindow adds no instance variables and NSPanel none over NSWindow, so the isa swap is sound.
		if objc.ID(w).Class() != cls {
			if objectSetClass == nil {
				objectSetClass = objcSetClass()
			}
			objectSetClass(objc.ID(w), cls)
		}
		mask := macos.SendInt(w, "styleMask") | styleNonactivatingPanel
		macos.Send(w, "setStyleMask:", uint(mask))
		macos.Send(w, "setFloatingPanel:", true)
		macos.Send(w, "setBecomesKeyOnlyIfNeeded:", true)
		macos.Send(w, "setWorksWhenModal:", true)
		macos.Send(w, "setLevel:", panelLevel)
		macos.Send(w, "setCollectionBehavior:", uint(panelBehavior))
		macos.Send(w, "setHasShadow:", false)
		macos.Send(w, "setOpaque:", false)
		macos.Send(w, "setBackgroundColor:", macos.Send(macos.Class("NSColor"), "clearColor"))
		macos.Send(w, "setHidesOnDeactivate:", false)
		macos.Send(w, "setMovable:", false)
		macos.Send(w, "setAcceptsMouseMovedEvents:", true)
		macos.Send(w, "setIgnoresMouseEvents:", true)
		barMu.Lock()
		barWin = w
		barFrame = objc.Send[macos.Rect](w, objc.RegisterName("frame"))
		barInteractive = false
		barMu.Unlock()
	})
	return err
}

func positionBar(w macos.ID) {
	mouse := objc.Send[macos.Point](macos.Class("NSEvent"), objc.RegisterName("mouseLocation"))
	screens := macos.Send(macos.Class("NSScreen"), "screens")
	var screen macos.ID
	for i := range macos.SendInt(screens, "count") {
		s := macos.Send(screens, "objectAtIndex:", uint(i))
		f := objc.Send[macos.Rect](s, objc.RegisterName("frame"))
		if mouse.X >= f.Origin.X && mouse.X < f.Origin.X+f.Size.W && mouse.Y >= f.Origin.Y && mouse.Y < f.Origin.Y+f.Size.H {
			screen = s
			break
		}
	}
	if screen == 0 {
		screen = macos.Send(macos.Class("NSScreen"), "mainScreen")
	}
	if screen == 0 {
		return
	}
	vf := objc.Send[macos.Rect](screen, objc.RegisterName("visibleFrame"))
	size := objc.Send[macos.Rect](w, objc.RegisterName("frame")).Size
	x, y := BottomCenter(vf.Origin.X, vf.Origin.Y, vf.Size.W, size.W, 0)
	macos.Send(w, "setFrameOrigin:", macos.Point{X: x, Y: y})
	barMu.Lock()
	barFrame = macos.Rect{Origin: macos.Point{X: x, Y: y}, Size: size}
	barMu.Unlock()
}

// ShowBar orders the bar front without activating, on the screen under the mouse.
func ShowBar() {
	RunOnMain(func() {
		barMu.Lock()
		w := barWin
		barMu.Unlock()
		if w == 0 {
			return
		}
		positionBar(w)
		macos.Send(w, "orderFrontRegardless")
		barMu.Lock()
		barVisible = true
		barMu.Unlock()
		ensurePoller()
	})
}

// HideBar orders the bar out.
func HideBar() {
	RunOnMain(func() {
		barMu.Lock()
		w := barWin
		barVisible, barInteractive = false, false
		barMu.Unlock()
		if w == 0 {
			return
		}
		macos.Send(w, "setIgnoresMouseEvents:", true)
		macos.Send(w, "orderOut:", uintptr(0))
	})
}

// SetBarHitRects updates the interactive regions (points from the window's top-left).
func SetBarHitRects(rects []HitRect) {
	barMu.Lock()
	same := slices.Equal(barRects, rects)
	barRects = slices.Clone(rects)
	barMu.Unlock()
	if same {
		return
	}
	if len(rects) == 0 {
		setInteractive(false)
	} else {
		ensurePoller()
	}
}

func setInteractive(v bool) {
	barMu.Lock()
	if barInteractive == v {
		barMu.Unlock()
		return
	}
	barInteractive = v
	w := barWin
	barMu.Unlock()
	if w != 0 {
		RunOnMain(func() { macos.Send(w, "setIgnoresMouseEvents:", !v) })
	}
}

func ensurePoller() {
	barMu.Lock()
	if barPolling || len(barRects) == 0 {
		barMu.Unlock()
		return
	}
	barPolling = true
	barMu.Unlock()
	go pollBar()
}

// pollBar toggles click-through by comparing the mouse with the hit rects.
func pollBar() {
	for {
		barMu.Lock()
		if len(barRects) == 0 {
			barPolling = false
			barMu.Unlock()
			return
		}
		rects, f, visible, interactive := slices.Clone(barRects), barFrame, barVisible, barInteractive
		barMu.Unlock()
		sleep := 100 * time.Millisecond
		if visible {
			m := objc.Send[macos.Point](macos.Class("NSEvent"), objc.RegisterName("mouseLocation"))
			fx, fy, fw, fh := f.Origin.X, f.Origin.Y, f.Size.W, f.Size.H
			near := m.X >= fx-60 && m.X <= fx+fw+60 && m.Y >= fy-60 && m.Y <= fy+fh+60
			sleep = 66 * time.Millisecond
			if near {
				sleep = 16 * time.Millisecond
			}
			inside := PointInRects(m.X-fx, fy+fh-m.Y, rects, hitSlop)
			if !inside && interactive && macos.SendInt(macos.Class("NSEvent"), "pressedMouseButtons") != 0 {
				inside = true // a drag that started on the UI keeps the window
			}
			if inside != interactive {
				setInteractive(inside)
			}
		}
		time.Sleep(sleep)
	}
}
