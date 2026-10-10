//go:build darwin

package platform

import (
	"errors"
	"time"
	"unsafe"

	"github.com/IcyBlue17/hoshiro/internal/macos"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// synthMagic is written into kCGEventSourceUserData of every event we post, so our own tap ignores them.
const (
	synthMagic                 = 0x4853_4852 // "HSHR"
	fieldSourceUserData        = 42
	fieldKeycode               = 9
	fieldMouseButton           = 3
	flagCommand         uint64 = 0x0010_0000
)

var (
	axIsProcessTrusted            func() bool
	axIsProcessTrustedWithOptions func(uintptr) bool
	axCreateSystemWide            func() uintptr
	axCreateApplication           func(pid int32) uintptr
	axCopyAttr                    func(el, attr uintptr, out *uintptr) int32
	axCopyParamAttr               func(el, attr, param uintptr, out *uintptr) int32
	axSetAttr                     func(el, attr, value uintptr) int32
	axIsSettable                  func(el, attr uintptr, out *uint8) int32
	axSetTimeout                  func(el uintptr, secs float32) int32
	axValueGetValue               func(v uintptr, typ uint32, out unsafe.Pointer) bool
	axValueCreate                 func(typ uint32, p unsafe.Pointer) uintptr
	axValueGetType                func(v uintptr) uint32
	kAXTrustedCheckOptionPrompt   uintptr

	cgPreflightListen   func() bool
	cgRequestListen     func() bool
	cgSourceFlagsState  func(state int32) uint64
	cgSourceKeyState    func(state int32, key uint16) bool
	cgSourceButtonState func(state int32, button uint32) bool
	cgSourceCreate      func(state int32) uintptr
	cgKeyboardEvent     func(src uintptr, code uint16, down bool) uintptr
	cgSetFlags          func(ev uintptr, flags uint64)
	cgSetIntField       func(ev uintptr, field uint32, v int64)
	cgGetIntField       func(ev uintptr, field uint32) int64
	cgGetFlags          func(ev uintptr) uint64
	cgPost              func(tap uint32, ev uintptr)
	cgTapCreate         func(tap, place, options uint32, mask uint64, cb, info uintptr) uintptr
	cgTapEnable         func(port uintptr, enable bool)
	cgTapIsEnabled      func(port uintptr) bool

	tisCopyASCIILayout func() uintptr
	tisGetProperty     func(src, key uintptr) uintptr
	kTISUnicodeLayout  uintptr
	lmGetKbdType       func() uint8
	ucKeyTranslate     func(layout unsafe.Pointer, code, action uint16, mods, kbd, opts uint32, dead *uint32, max int, n *int, buf *uint16) int32
)

func init() {
	as, cg, carbon := macos.AppServices, macos.CoreGraphics, macos.Carbon
	purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
	purego.RegisterLibFunc(&axIsProcessTrustedWithOptions, as, "AXIsProcessTrustedWithOptions")
	purego.RegisterLibFunc(&axCreateSystemWide, as, "AXUIElementCreateSystemWide")
	purego.RegisterLibFunc(&axCreateApplication, as, "AXUIElementCreateApplication")
	purego.RegisterLibFunc(&axCopyAttr, as, "AXUIElementCopyAttributeValue")
	purego.RegisterLibFunc(&axCopyParamAttr, as, "AXUIElementCopyParameterizedAttributeValue")
	purego.RegisterLibFunc(&axSetAttr, as, "AXUIElementSetAttributeValue")
	purego.RegisterLibFunc(&axIsSettable, as, "AXUIElementIsAttributeSettable")
	purego.RegisterLibFunc(&axSetTimeout, as, "AXUIElementSetMessagingTimeout")
	purego.RegisterLibFunc(&axValueGetValue, as, "AXValueGetValue")
	purego.RegisterLibFunc(&axValueCreate, as, "AXValueCreate")
	purego.RegisterLibFunc(&axValueGetType, as, "AXValueGetType")
	kAXTrustedCheckOptionPrompt = macos.Symbol(as, "kAXTrustedCheckOptionPrompt")

	purego.RegisterLibFunc(&cgPreflightListen, cg, "CGPreflightListenEventAccess")
	purego.RegisterLibFunc(&cgRequestListen, cg, "CGRequestListenEventAccess")
	purego.RegisterLibFunc(&cgSourceFlagsState, cg, "CGEventSourceFlagsState")
	purego.RegisterLibFunc(&cgSourceKeyState, cg, "CGEventSourceKeyState")
	purego.RegisterLibFunc(&cgSourceButtonState, cg, "CGEventSourceButtonState")
	purego.RegisterLibFunc(&cgSourceCreate, cg, "CGEventSourceCreate")
	purego.RegisterLibFunc(&cgKeyboardEvent, cg, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&cgSetFlags, cg, "CGEventSetFlags")
	purego.RegisterLibFunc(&cgSetIntField, cg, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&cgGetIntField, cg, "CGEventGetIntegerValueField")
	purego.RegisterLibFunc(&cgGetFlags, cg, "CGEventGetFlags")
	purego.RegisterLibFunc(&cgPost, cg, "CGEventPost")
	purego.RegisterLibFunc(&cgTapCreate, cg, "CGEventTapCreate")
	purego.RegisterLibFunc(&cgTapEnable, cg, "CGEventTapEnable")
	purego.RegisterLibFunc(&cgTapIsEnabled, cg, "CGEventTapIsEnabled")

	purego.RegisterLibFunc(&tisCopyASCIILayout, carbon, "TISCopyCurrentASCIICapableKeyboardLayoutInputSource")
	purego.RegisterLibFunc(&tisGetProperty, carbon, "TISGetInputSourceProperty")
	purego.RegisterLibFunc(&lmGetKbdType, carbon, "LMGetKbdType")
	purego.RegisterLibFunc(&ucKeyTranslate, carbon, "UCKeyTranslate")
	kTISUnicodeLayout = macos.Symbol(carbon, "kTISPropertyUnicodeKeyLayoutData")
}

// postKeyPress posts key down+up with exactly flags, from a private source so
// physically held keys cannot leak into the shortcut. Never call it from the tap callback.
func postKeyPress(code uint16, flags uint64) error {
	src := cgSourceCreate(-1) // kCGEventSourceStatePrivate
	if src == 0 {
		return errors.New("CGEventSourceCreate failed")
	}
	defer macos.CFRelease(src)
	for _, down := range []bool{true, false} {
		ev := cgKeyboardEvent(src, code, down)
		if ev == 0 {
			return errors.New("CGEventCreateKeyboardEvent failed")
		}
		cgSetFlags(ev, flags)
		cgSetIntField(ev, fieldSourceUserData, synthMagic)
		cgPost(0, ev) // kCGHIDEventTap
		macos.CFRelease(ev)
		if down {
			time.Sleep(8 * time.Millisecond)
		}
	}
	return nil
}

func objcSetClass() func(objc.ID, objc.Class) objc.Class {
	var f func(objc.ID, objc.Class) objc.Class
	h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		panic(err)
	}
	purego.RegisterLibFunc(&f, h, "object_setClass")
	return f
}
