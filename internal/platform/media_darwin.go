//go:build darwin

package platform

import (
	"unsafe"

	"github.com/IcyBlue17/hoshiro/internal/macos"
	"github.com/ebitengine/purego"
)

type audioAddress struct{ selector, scope, element uint32 }

var (
	aoGetData     func(obj uint32, addr *audioAddress, qs uint32, q unsafe.Pointer, size *uint32, data unsafe.Pointer) int32
	aoSetData     func(obj uint32, addr *audioAddress, qs uint32, q unsafe.Pointer, size uint32, data unsafe.Pointer) int32
	aoIsSettable  func(obj uint32, addr *audioAddress, out *uint8) int32
	aoHasProperty func(obj uint32, addr *audioAddress) bool
	aoGetDataSize func(obj uint32, addr *audioAddress, qs uint32, q unsafe.Pointer, size *uint32) int32
)

func init() {
	ca := macos.CoreAudio
	purego.RegisterLibFunc(&aoGetData, ca, "AudioObjectGetPropertyData")
	purego.RegisterLibFunc(&aoSetData, ca, "AudioObjectSetPropertyData")
	purego.RegisterLibFunc(&aoIsSettable, ca, "AudioObjectIsPropertySettable")
	purego.RegisterLibFunc(&aoHasProperty, ca, "AudioObjectHasProperty")
	purego.RegisterLibFunc(&aoGetDataSize, ca, "AudioObjectGetPropertyDataSize")
}

func fourcc(s string) uint32 {
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
}

var (
	scopeGlobal       = fourcc("glob")
	scopeOutput       = fourcc("outp")
	selDefaultOutput  = fourcc("dOut")
	selMute           = fourcc("mute")
	selVirtualMainVol = fourcc("vmvc")
)

func aoGet[T any](obj, sel, scope, el uint32) (T, bool) {
	var v T
	a := audioAddress{sel, scope, el}
	size := uint32(unsafe.Sizeof(v))
	return v, aoGetData(obj, &a, 0, nil, &size, unsafe.Pointer(&v)) == 0
}

func aoSet[T any](obj, sel, scope, el uint32, v T) bool {
	a := audioAddress{sel, scope, el}
	return aoSetData(obj, &a, 0, nil, uint32(unsafe.Sizeof(v)), unsafe.Pointer(&v)) == 0
}

func aoSettable(obj, sel, scope, el uint32) bool {
	a := audioAddress{sel, scope, el}
	if !aoHasProperty(obj, &a) {
		return false
	}
	var s uint8
	return aoIsSettable(obj, &a, &s) == 0 && s != 0
}

// MuteSystemOutput mutes the default output device unless it is already
// muted, using its mute switch or else its main volume. The returned function
// restores exactly what was changed (if nobody changed it since); nil when nothing changed.
func MuteSystemOutput() func() {
	dev, ok := aoGet[uint32](1, selDefaultOutput, scopeGlobal, 0)
	if !ok || dev == 0 {
		return nil
	}
	var elements []uint32
	if aoSettable(dev, selMute, scopeOutput, 0) {
		if m, ok := aoGet[uint32](dev, selMute, scopeOutput, 0); ok {
			if m != 0 {
				return nil // muted by the user
			}
			elements = append(elements, 0)
		}
	} else {
		for ch := uint32(1); ch <= 2; ch++ {
			if m, ok := aoGet[uint32](dev, selMute, scopeOutput, ch); ok && m == 0 && aoSettable(dev, selMute, scopeOutput, ch) {
				elements = append(elements, ch)
			}
		}
	}
	var muted []uint32
	for _, el := range elements {
		if aoSet(dev, selMute, scopeOutput, el, uint32(1)) {
			muted = append(muted, el)
		}
	}
	if len(muted) > 0 {
		return func() {
			for _, el := range muted {
				if m, ok := aoGet[uint32](dev, selMute, scopeOutput, el); ok && m == 1 {
					aoSet(dev, selMute, scopeOutput, el, uint32(0))
				}
			}
		}
	}
	if aoSettable(dev, selVirtualMainVol, scopeOutput, 0) {
		prev, ok := aoGet[float32](dev, selVirtualMainVol, scopeOutput, 0)
		if !ok || prev <= 0.001 {
			return nil
		}
		if aoSet(dev, selVirtualMainVol, scopeOutput, 0, float32(0)) {
			return func() {
				if v, ok := aoGet[float32](dev, selVirtualMainVol, scopeOutput, 0); ok && v <= 0.001 {
					aoSet(dev, selVirtualMainVol, scopeOutput, 0, prev)
				}
			}
		}
	}
	return nil
}
