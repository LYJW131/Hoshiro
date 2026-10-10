//go:build darwin

package platform

import (
	"errors"
	"log"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/IcyBlue17/hoshiro/internal/hotkey"
	"github.com/IcyBlue17/hoshiro/internal/macos"
	"github.com/ebitengine/purego"
)

// CGEventType values.
const (
	evKeyDown         = 10
	evKeyUp           = 11
	evFlagsChanged    = 12
	evOtherMouseDown  = 25
	evOtherMouseUp    = 26
	evTapDisabledTime = 0xFFFF_FFFE
	evTapDisabledUser = 0xFFFF_FFFF
)

// HotkeyTap is the global CGEventTap feeding a hotkey.Engine.
type HotkeyTap struct {
	engine  *hotkey.Engine
	port    atomic.Uintptr
	runLoop atomic.Uintptr
	active  atomic.Bool
	done    chan struct{}
}

var (
	tapMu      sync.Mutex
	currentTap *HotkeyTap
	tapCB      uintptr
	tapCBOnce  sync.Once
)

// tapCallback runs on the tap's run loop thread for every event. It must never block.
func tapCallback(_ uintptr, etype uintptr, event uintptr, _ uintptr) uintptr {
	tapMu.Lock()
	t := currentTap
	tapMu.Unlock()
	if t == nil {
		return event
	}
	swallow := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in hotkey tap callback: %v", r)
				swallow = false
			}
		}()
		swallow = t.handle(uint32(etype), event)
	}()
	if swallow {
		return 0
	}
	return event
}

func (t *HotkeyTap) handle(etype uint32, event uintptr) bool {
	switch etype {
	case evTapDisabledTime, evTapDisabledUser:
		log.Printf("event tap disabled by the system (%#x); re-enabling", etype)
		if p := t.port.Load(); p != 0 {
			cgTapEnable(p, true)
		}
		go t.engine.Resync(physicallyDown)
		return false
	case evKeyDown, evKeyUp, evFlagsChanged, evOtherMouseDown, evOtherMouseUp:
	default:
		return false
	}
	if event == 0 || cgGetIntField(event, fieldSourceUserData) == synthMagic {
		return false // our own paste / copy
	}
	switch etype {
	case evKeyDown, evKeyUp:
		code := uint16(cgGetIntField(event, fieldKeycode))
		key, ok := hotkey.KeyForCode(code)
		if !ok {
			key = hotkey.UnknownKeyName("Key", int64(code))
		}
		return t.engine.Key(key, etype == evKeyDown)
	case evFlagsChanged:
		code := uint16(cgGetIntField(event, fieldKeycode))
		flags := cgGetFlags(event)
		if key, down, ok := hotkey.ModifierTransition(code, flags); ok {
			return t.engine.Flags(key, down, flags)
		}
		return false
	default:
		b := cgGetIntField(event, fieldMouseButton)
		key, ok := hotkey.MouseButtonName(b)
		if !ok {
			key = hotkey.UnknownKeyName("Mouse", b+1)
		}
		return t.engine.Key(key, etype == evOtherMouseDown)
	}
}

func physicallyDown(key string) bool {
	if fam := hotkey.ModifierFamily(key); fam != hotkey.FamNone {
		return cgSourceFlagsState(1)&hotkey.FamilyFlag(fam) != 0 // kCGEventSourceStateHIDSystemState
	}
	if code, ok := hotkey.CodeForKey(key); ok {
		return cgSourceKeyState(1, code)
	}
	if b, ok := hotkey.MouseButtonNumber(key); ok {
		return cgSourceButtonState(1, uint32(b))
	}
	return false
}

// StartHotkeyTap installs the global event tap. It fails without the
// Accessibility / Input Monitoring permission; the caller retries later.
func StartHotkeyTap(engine *hotkey.Engine) (*HotkeyTap, error) {
	tapCBOnce.Do(func() { tapCB = purego.NewCallback(tapCallback) })
	t := &HotkeyTap{engine: engine, done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // the run loop belongs to this thread for good
		mask := uint64(1<<evKeyDown | 1<<evKeyUp | 1<<evFlagsChanged | 1<<evOtherMouseDown | 1<<evOtherMouseUp)
		tapMu.Lock()
		currentTap = t
		tapMu.Unlock()
		// kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault (active)
		port := cgTapCreate(1, 0, 0, mask, tapCB, 0)
		if port == 0 {
			tapMu.Lock()
			if currentTap == t {
				currentTap = nil
			}
			tapMu.Unlock()
			ready <- errors.New("could not create the keyboard event tap (grant Accessibility / Input Monitoring permission)")
			return
		}
		src := macos.CFMachPortCreateRunLoopSource(0, port, 0)
		if src == 0 {
			macos.CFRelease(port)
			ready <- errors.New("could not create the event tap run loop source")
			return
		}
		rl := macos.CFRunLoopGetCurrent()
		macos.CFRunLoopAddSource(rl, src, macos.KCFRunLoopCommonModes)
		cgTapEnable(port, true)
		t.port.Store(port)
		t.runLoop.Store(rl)
		t.active.Store(true)
		ready <- nil
		macos.CFRunLoopRun()
		t.active.Store(false)
		t.port.Store(0)
		cgTapEnable(port, false)
		macos.CFRelease(src)
		macos.CFRelease(port)
		close(t.done)
		log.Print("hotkey tap thread exited")
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	log.Print("hotkey tap started")
	return t, nil
}

// Active reports whether the tap is installed and enabled.
func (t *HotkeyTap) Active() bool {
	p := t.port.Load()
	return t.active.Load() && p != 0 && cgTapIsEnabled(p)
}

// Close removes the tap.
func (t *HotkeyTap) Close() {
	tapMu.Lock()
	if currentTap == t {
		currentTap = nil
	}
	tapMu.Unlock()
	if rl := t.runLoop.Load(); rl != 0 {
		macos.CFRunLoopStop(rl)
		<-t.done
	}
}
