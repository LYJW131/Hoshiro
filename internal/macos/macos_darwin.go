//go:build darwin

// Package macos loads the system frameworks Hoshiro talks to directly
// (CoreFoundation, CoreGraphics, Accessibility, AppKit, Carbon, CoreAudio,
// AudioToolbox) through purego, so the app builds without cgo, and wraps the
// handful of CoreFoundation and Objective-C helpers the platform code shares.
package macos

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

func lib(path string) uintptr {
	h, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		panic("hoshiro: cannot load " + path + ": " + err.Error())
	}
	return h
}

const fw = "/System/Library/Frameworks/"

var (
	CoreFoundation = lib(fw + "CoreFoundation.framework/CoreFoundation")
	CoreGraphics   = lib(fw + "CoreGraphics.framework/CoreGraphics")
	AppServices    = lib(fw + "ApplicationServices.framework/ApplicationServices")
	Foundation     = lib(fw + "Foundation.framework/Foundation")
	AppKit         = lib(fw + "AppKit.framework/AppKit")
	Carbon         = lib(fw + "Carbon.framework/Carbon")
	CoreAudio      = lib(fw + "CoreAudio.framework/CoreAudio")
	AudioToolbox   = lib(fw + "AudioToolbox.framework/AudioToolbox")
	AVFoundation   = lib(fw + "AVFoundation.framework/AVFoundation")
)

// Symbol returns the value of a global pointer variable (kCFBooleanTrue and friends).
func Symbol(h uintptr, name string) uintptr {
	p, err := purego.Dlsym(h, name)
	if err != nil || p == 0 {
		return 0
	}
	return **(**uintptr)(unsafe.Pointer(&p))
}

// CoreFoundation.
var (
	CFRelease                     func(uintptr)
	CFRetain                      func(uintptr) uintptr
	CFGetTypeID                   func(uintptr) uintptr
	CFStringGetTypeID             func() uintptr
	CFNumberGetTypeID             func() uintptr
	CFBooleanGetTypeID            func() uintptr
	CFStringCreateWithBytes       func(alloc uintptr, b *byte, n int, enc uint32, bom bool) uintptr
	CFStringGetLength             func(uintptr) int
	CFStringGetBytes              func(s uintptr, loc, length int, enc uint32, lossByte uint8, external bool, buf *byte, max int, used *int) int
	CFNumberGetValue              func(n uintptr, typ int, out unsafe.Pointer) bool
	CFBooleanGetValue             func(uintptr) bool
	CFDictionaryCreate            func(alloc uintptr, keys, values *uintptr, n int, kcb, vcb uintptr) uintptr
	CFDataGetBytePtr              func(uintptr) unsafe.Pointer
	CFDataGetLength               func(uintptr) int
	CFRunLoopGetCurrent           func() uintptr
	CFRunLoopRun                  func()
	CFRunLoopStop                 func(uintptr)
	CFRunLoopAddSource            func(rl, src, mode uintptr)
	CFMachPortCreateRunLoopSource func(alloc, port uintptr, order int) uintptr

	KCFBooleanTrue                                                 uintptr
	KCFRunLoopCommonModes                                          uintptr
	kCFTypeDictionaryKeyCallBacks, kCFTypeDictionaryValueCallBacks uintptr
)

const utf8 = 0x08000100 // kCFStringEncodingUTF8

func init() {
	cf := CoreFoundation
	purego.RegisterLibFunc(&CFRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&CFRetain, cf, "CFRetain")
	purego.RegisterLibFunc(&CFGetTypeID, cf, "CFGetTypeID")
	purego.RegisterLibFunc(&CFStringGetTypeID, cf, "CFStringGetTypeID")
	purego.RegisterLibFunc(&CFNumberGetTypeID, cf, "CFNumberGetTypeID")
	purego.RegisterLibFunc(&CFBooleanGetTypeID, cf, "CFBooleanGetTypeID")
	purego.RegisterLibFunc(&CFStringCreateWithBytes, cf, "CFStringCreateWithBytes")
	purego.RegisterLibFunc(&CFStringGetLength, cf, "CFStringGetLength")
	purego.RegisterLibFunc(&CFStringGetBytes, cf, "CFStringGetBytes")
	purego.RegisterLibFunc(&CFNumberGetValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&CFBooleanGetValue, cf, "CFBooleanGetValue")
	purego.RegisterLibFunc(&CFDictionaryCreate, cf, "CFDictionaryCreate")
	purego.RegisterLibFunc(&CFDataGetBytePtr, cf, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&CFDataGetLength, cf, "CFDataGetLength")
	purego.RegisterLibFunc(&CFRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&CFRunLoopRun, cf, "CFRunLoopRun")
	purego.RegisterLibFunc(&CFRunLoopStop, cf, "CFRunLoopStop")
	purego.RegisterLibFunc(&CFRunLoopAddSource, cf, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&CFMachPortCreateRunLoopSource, cf, "CFMachPortCreateRunLoopSource")
	KCFBooleanTrue = Symbol(cf, "kCFBooleanTrue")
	KCFRunLoopCommonModes = Symbol(cf, "kCFRunLoopCommonModes")
	if p, err := purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks"); err == nil {
		kCFTypeDictionaryKeyCallBacks = p
	}
	if p, err := purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks"); err == nil {
		kCFTypeDictionaryValueCallBacks = p
	}
}

// CFStr creates a CFString the caller must CFRelease.
func CFStr(s string) uintptr {
	b := []byte(s)
	var p *byte
	if len(b) > 0 {
		p = &b[0]
	}
	r := CFStringCreateWithBytes(0, p, len(b), utf8, false)
	runtime.KeepAlive(b)
	return r
}

// GoString converts a CFString (or NSString, toll-free bridged) to Go; "" and false for other types.
func GoString(s uintptr) (string, bool) {
	if s == 0 || CFGetTypeID(s) != CFStringGetTypeID() {
		return "", false
	}
	n := CFStringGetLength(s)
	if n == 0 {
		return "", true
	}
	var used int
	CFStringGetBytes(s, 0, n, utf8, '?', false, nil, 0, &used)
	buf := make([]byte, used)
	CFStringGetBytes(s, 0, n, utf8, '?', false, &buf[0], used, &used)
	return string(buf[:used]), true
}

// Int64 reads a CFNumber.
func Int64(n uintptr) (int64, bool) {
	if n == 0 || CFGetTypeID(n) != CFNumberGetTypeID() {
		return 0, false
	}
	var v int64
	ok := CFNumberGetValue(n, 4 /* kCFNumberSInt64Type */, unsafe.Pointer(&v))
	return v, ok
}

// Dict1 builds a one-entry CFDictionary (caller releases).
func Dict1(key, value uintptr) uintptr {
	return CFDictionaryCreate(0, &key, &value, 1, kCFTypeDictionaryKeyCallBacks, kCFTypeDictionaryValueCallBacks)
}

// DataBytes copies the bytes of a CFData / NSData.
func DataBytes(d uintptr) []byte {
	if d == 0 {
		return nil
	}
	n := CFDataGetLength(d)
	if n == 0 {
		return []byte{}
	}
	return append([]byte(nil), unsafe.Slice((*byte)(CFDataGetBytePtr(d)), n)...)
}

// ── Objective-C ──

type ID = objc.ID

func Class(name string) ID { return ID(objc.GetClass(name)) }

func Sel(name string) objc.SEL { return objc.RegisterName(name) }

// Send sends a message returning an object (or nothing interesting).
func Send(o ID, sel string, args ...any) ID { return o.Send(objc.RegisterName(sel), args...) }

func SendBool(o ID, sel string, args ...any) bool {
	return objc.Send[bool](o, objc.RegisterName(sel), args...)
}

func SendInt(o ID, sel string, args ...any) int {
	return objc.Send[int](o, objc.RegisterName(sel), args...)
}

// NSString makes an autoreleased NSString.
func NSString(s string) ID {
	b := append([]byte(s), 0)
	r := Send(Class("NSString"), "stringWithUTF8String:", &b[0])
	runtime.KeepAlive(b)
	return r
}

// GoNS converts an NSString to Go.
func GoNS(s ID) string {
	str, _ := GoString(uintptr(s))
	return str
}

// NSData makes an autoreleased NSData copy of b.
func NSData(b []byte) ID {
	if len(b) == 0 {
		return Send(Class("NSData"), "data")
	}
	r := Send(Class("NSData"), "dataWithBytes:length:", &b[0], uint(len(b)))
	runtime.KeepAlive(b)
	return r
}

// Pool runs f inside an autorelease pool.
func Pool(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p := Send(Send(Class("NSAutoreleasePool"), "alloc"), "init")
	defer Send(p, "drain")
	f()
}

// IsMainThread reports whether the calling OS thread is the main thread.
func IsMainThread() bool { return SendBool(Class("NSThread"), "isMainThread") }

// Point, Size and Rect mirror CGPoint/CGSize/CGRect.
type Point struct{ X, Y float64 }
type Size struct{ W, H float64 }
type Rect struct {
	Origin Point
	Size   Size
}

// Range mirrors CFRange.
type Range struct{ Location, Length int }

func SendInt32(o ID, sel string, args ...any) int32 {
	return objc.Send[int32](o, objc.RegisterName(sel), args...)
}
