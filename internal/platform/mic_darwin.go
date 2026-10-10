//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/macos"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/ebitengine/purego"
)

type asbd struct {
	SampleRate                                                float64
	FormatID, FormatFlags, BytesPerPacket, FramesPerPacket    uint32
	BytesPerFrame, ChannelsPerFrame, BitsPerChannel, Reserved uint32
}

// audioQueueBuffer mirrors the head of AudioQueueBuffer.
type audioQueueBuffer struct {
	capacity uint32
	_        uint32
	data     unsafe.Pointer
	size     uint32
}

var (
	aqNewInput      func(f *asbd, cb, user, runLoop, mode uintptr, flags uint32, out *uintptr) int32
	aqAllocBuffer   func(aq uintptr, size uint32, out *uintptr) int32
	aqEnqueueBuffer func(aq, buf uintptr, n uint32, descs uintptr) int32
	aqStart         func(aq, at uintptr) int32
	aqStop          func(aq uintptr, immediate bool) int32
	aqDispose       func(aq uintptr, immediate bool) int32
	aqSetProperty   func(aq uintptr, id uint32, data unsafe.Pointer, size uint32) int32

	aqCB     uintptr
	aqCBOnce sync.Once
	aqMu     sync.Mutex
	aqLive   = map[uintptr]*macStream{}
)

func init() {
	at := macos.AudioToolbox
	purego.RegisterLibFunc(&aqNewInput, at, "AudioQueueNewInput")
	purego.RegisterLibFunc(&aqAllocBuffer, at, "AudioQueueAllocateBuffer")
	purego.RegisterLibFunc(&aqEnqueueBuffer, at, "AudioQueueEnqueueBuffer")
	purego.RegisterLibFunc(&aqStart, at, "AudioQueueStart")
	purego.RegisterLibFunc(&aqStop, at, "AudioQueueStop")
	purego.RegisterLibFunc(&aqDispose, at, "AudioQueueDispose")
	purego.RegisterLibFunc(&aqSetProperty, at, "AudioQueueSetProperty")
}

const captureRate = 48000

type macStream struct {
	aq        uintptr
	runLoop   uintptr
	onSamples func([]float32)
	stopped   chan struct{}
	once      sync.Once
}

// aqInput is the AudioQueue input callback; it runs on the stream's run loop thread.
func aqInput(user, aq uintptr, buf unsafe.Pointer, _ uintptr, _ uintptr, _ uintptr) uintptr {
	aqMu.Lock()
	s := aqLive[aq]
	aqMu.Unlock()
	if s == nil || buf == nil {
		return 0
	}
	b := (*audioQueueBuffer)(buf)
	if n := int(b.size / 4); n > 0 {
		s.onSamples(append([]float32(nil), unsafe.Slice((*float32)(b.data), n)...))
	}
	aqEnqueueBuffer(aq, uintptr(buf), 0, 0)
	return 0
}

func (s *macStream) Stop() {
	s.once.Do(func() {
		aqStop(s.aq, true)
		aqMu.Lock()
		delete(aqLive, s.aq)
		aqMu.Unlock()
		aqDispose(s.aq, true)
		macos.CFRunLoopStop(s.runLoop)
		<-s.stopped
	})
}

// OpenMic opens an input device by name ("" = default) as mono f32 at 48 kHz.
func OpenMic(device string, onSamples func([]float32), onError func(string)) (uint32, audio.Stream, error) {
	aqCBOnce.Do(func() { aqCB = purego.NewCallback(aqInput) })
	uid := ""
	if device != "" {
		var ok bool
		if uid, ok = deviceUID(device); !ok {
			return 0, nil, fmt.Errorf("microphone %q not found", device)
		}
	}
	type opened struct {
		s   *macStream
		err error
	}
	ready := make(chan opened, 1)
	go func() {
		runtime.LockOSThread()
		s := &macStream{runLoop: macos.CFRunLoopGetCurrent(), onSamples: onSamples, stopped: make(chan struct{})}
		fail := func(e error) { ready <- opened{nil, e} }
		f := asbd{SampleRate: captureRate, FormatID: fourcc("lpcm"), FormatFlags: 1 | 8, // float | packed
			BytesPerPacket: 4, FramesPerPacket: 1, BytesPerFrame: 4, ChannelsPerFrame: 1, BitsPerChannel: 32}
		if st := aqNewInput(&f, aqCB, 0, s.runLoop, macos.KCFRunLoopCommonModes, 0, &s.aq); st != 0 {
			fail(fmt.Errorf("无法打开麦克风 (AudioQueueNewInput %d)", st))
			return
		}
		if uid != "" {
			cf := macos.CFStr(uid)
			st := aqSetProperty(s.aq, fourcc("aqcd"), unsafe.Pointer(&cf), uint32(unsafe.Sizeof(cf)))
			macos.CFRelease(cf)
			if st != 0 {
				aqDispose(s.aq, true)
				fail(fmt.Errorf("无法打开麦克风 %q (%d)", device, st))
				return
			}
		}
		const bufBytes = captureRate / 50 * 4 // 20 ms
		for range 4 {
			var b uintptr
			if aqAllocBuffer(s.aq, bufBytes, &b) == 0 {
				aqEnqueueBuffer(s.aq, b, 0, 0)
			}
		}
		aqMu.Lock()
		aqLive[s.aq] = s
		aqMu.Unlock()
		if st := aqStart(s.aq, 0); st != 0 {
			aqMu.Lock()
			delete(aqLive, s.aq)
			aqMu.Unlock()
			aqDispose(s.aq, true)
			fail(fmt.Errorf("无法启动麦克风 (%d)", st))
			return
		}
		ready <- opened{s, nil}
		macos.CFRunLoopRun()
		close(s.stopped)
	}()
	o := <-ready
	if o.err != nil {
		return 0, nil, o.err
	}
	log.Printf("recording from %q at %d Hz", device, captureRate)
	return captureRate, o.s, nil
}

// ── devices ──

var (
	selDevices      = fourcc("dev#")
	selDefaultInput = fourcc("dIn ")
	selStreams      = fourcc("stm#")
	selName         = fourcc("lnam")
	selUID          = fourcc("uid ")
	scopeInput      = fourcc("inpt")
)

type coreDevice struct {
	id        uint32
	name, uid string
}

func cfProp(dev, sel uint32) string {
	var s uintptr
	a := audioAddress{sel, scopeGlobal, 0}
	size := uint32(unsafe.Sizeof(s))
	if aoGetData(dev, &a, 0, nil, &size, unsafe.Pointer(&s)) != 0 || s == 0 {
		return ""
	}
	defer macos.CFRelease(s)
	str, _ := macos.GoString(s)
	return str
}

func inputDevices() ([]coreDevice, uint32) {
	a := audioAddress{selDevices, scopeGlobal, 0}
	var size uint32
	if aoGetDataSize(1, &a, 0, nil, &size) != 0 || size == 0 {
		return nil, 0
	}
	ids := make([]uint32, size/4)
	if aoGetData(1, &a, 0, nil, &size, unsafe.Pointer(&ids[0])) != 0 {
		return nil, 0
	}
	var out []coreDevice
	for _, id := range ids[:size/4] {
		sa := audioAddress{selStreams, scopeInput, 0}
		var n uint32
		if aoGetDataSize(id, &sa, 0, nil, &n) != 0 || n == 0 {
			continue // no input streams
		}
		out = append(out, coreDevice{id, cfProp(id, selName), cfProp(id, selUID)})
	}
	def, _ := aoGet[uint32](1, selDefaultInput, scopeGlobal, 0)
	return out, def
}

func deviceUID(name string) (string, bool) {
	devs, _ := inputDevices()
	for _, d := range devs {
		if d.name == name {
			return d.uid, true
		}
	}
	return "", false
}

// ListMics lists input devices; the id is the device name, as in earlier versions' settings.
func ListMics() []model.MicDevice {
	devs, def := inputDevices()
	var out []model.MicDevice
	seen := map[string]bool{}
	for _, d := range devs {
		if d.name == "" || seen[d.name] {
			continue
		}
		seen[d.name] = true
		out = append(out, model.MicDevice{ID: d.name, Name: d.name, IsDefault: d.id == def, Kind: audio.ClassifyDevice(d.name)})
	}
	return out
}

var errNoSound = errors.New("sound not loaded")

// PlayWAV plays a WAV clip without blocking (NSSound).
func PlayWAV(data []byte) error {
	if len(data) == 0 {
		return errNoSound
	}
	RunOnMain(func() {
		macos.Pool(func() {
			snd := macos.Send(macos.Send(macos.Class("NSSound"), "alloc"), "initWithData:", macos.NSData(data))
			if snd != 0 {
				macos.Send(snd, "play")
				go func() { // keep it alive until it finished playing
					time.Sleep(10 * time.Second)
					RunOnMain(func() { macos.Send(snd, "release") })
				}()
			}
		})
	})
	return nil
}
