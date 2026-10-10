package hotkey

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

// Engine wires the gesture machine to a raw event source. The OS layer calls
// Key/Flags from its tap callback (they never block on the controller) and
// actions are delivered in order on a worker goroutine.
type Engine struct {
	mu        sync.Mutex
	machine   *Machine
	capture   CaptureMachine
	captureCb func(keys string, done bool)
	capturing atomic.Bool
	phase     func() Phase
	onAction  func(Action)
	epoch     time.Time
	msgs      chan msg
	stop      chan struct{}
	stopOnce  sync.Once
}

type msg struct {
	actions []Action
	capture *struct {
		keys string
		done bool
	}
}

func NewEngine(b model.ShortcutBindings, phase func() Phase, onAction func(Action)) *Engine {
	e := &Engine{machine: NewMachine(b), phase: phase, onAction: onAction, epoch: time.Now(),
		msgs: make(chan msg, 256), stop: make(chan struct{})}
	go e.worker()
	return e
}

func (e *Engine) nowMs() uint64 { return uint64(time.Since(e.epoch).Milliseconds()) }

func (e *Engine) send(m msg) {
	select {
	case e.msgs <- m:
	default:
		log.Print("hotkey: action queue full, dropping")
	}
}

func (e *Engine) wake() { e.send(msg{}) }

func (e *Engine) Close() { e.stopOnce.Do(func() { close(e.stop) }) }

func (e *Engine) UpdateBindings(b model.ShortcutBindings) {
	e.mu.Lock()
	e.machine.SetBindings(b)
	e.mu.Unlock()
	e.wake()
}

// StartCapture suspends hotkeys; every combination goes to cb (and is swallowed) until StopCapture.
func (e *Engine) StartCapture(cb func(string, bool)) {
	e.mu.Lock()
	e.machine.Reset()
	e.capture.Reset()
	e.captureCb = cb
	e.mu.Unlock()
	e.capturing.Store(true)
	e.wake()
}

func (e *Engine) StopCapture() {
	e.capturing.Store(false)
	e.mu.Lock()
	e.captureCb = nil
	e.capture.Reset()
	e.machine.Reset()
	e.mu.Unlock()
}

// Key feeds a key or mouse event (not a modifier flagsChanged). Returns true to swallow it.
func (e *Engine) Key(key string, down bool) bool { return e.process(key, down, 0, false) }

// Flags feeds a flagsChanged event (one modifier moved). Returns true to swallow it (never).
func (e *Engine) Flags(key string, down bool, flags uint64) bool {
	return e.process(key, down, flags, true)
}

func (e *Engine) process(key string, down bool, flags uint64, isFlags bool) bool {
	regular := !isFlags && !IsModifier(key)
	if e.capturing.Load() {
		e.mu.Lock()
		combo, done, changed := e.capture.OnKey(key, down)
		e.mu.Unlock()
		if changed {
			e.send(msg{capture: &struct {
				keys string
				done bool
			}{combo, done}})
		}
		return regular
	}
	phase := e.phase()
	now := e.nowMs()
	var actions []Action
	e.mu.Lock()
	before, hadBefore := e.machine.NextDeadline()
	if isFlags {
		// Modifier state in the flags is authoritative: repair anything we missed.
		for _, held := range e.machine.HeldModifiers() {
			fam := ModifierFamily(held)
			stale := held != key && fam != FamFn && flags&FamilyFlag(fam) == 0
			if stale || (down && held == key) {
				actions = append(actions, e.machine.Handle(KeyEvent{held, false, now}, phase).Actions...)
			}
		}
	}
	o := e.machine.Handle(KeyEvent{key, down, now}, phase)
	actions = append(actions, o.Actions...)
	after, hadAfter := e.machine.NextDeadline()
	e.mu.Unlock()
	if len(actions) > 0 {
		e.send(msg{actions: actions})
	} else if before != after || hadBefore != hadAfter {
		e.wake()
	}
	return o.Swallow && regular
}

// Resync is called after the OS disabled and re-enabled the tap: keys that
// are no longer physically down get synthetic releases.
func (e *Engine) Resync(physicallyDown func(key string) bool) {
	if e.capturing.Load() {
		e.mu.Lock()
		e.capture.Reset()
		e.mu.Unlock()
		return
	}
	phase := e.phase()
	now := e.nowMs()
	var actions []Action
	e.mu.Lock()
	for _, k := range append([]string(nil), e.machine.HeldKeys()...) {
		if !physicallyDown(k) {
			actions = append(actions, e.machine.Handle(KeyEvent{k, false, now}, phase).Actions...)
		}
	}
	e.mu.Unlock()
	e.send(msg{actions: actions})
}

func (e *Engine) worker() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		e.mu.Lock()
		d, ok := e.machine.NextDeadline()
		e.mu.Unlock()
		wait := time.Hour
		if ok {
			wait = time.Duration(d-min(d, e.nowMs())) * time.Millisecond
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
		select {
		case <-e.stop:
			return
		case m := <-e.msgs:
			if m.capture != nil {
				e.mu.Lock()
				cb := e.captureCb
				e.mu.Unlock()
				if cb != nil {
					safe(func() { cb(m.capture.keys, m.capture.done) })
				}
			}
			e.dispatch(m.actions)
		case <-timer.C:
			phase := e.phase()
			e.mu.Lock()
			acts := e.machine.OnTimer(e.nowMs(), phase)
			e.mu.Unlock()
			e.dispatch(acts)
		}
	}
}

func (e *Engine) dispatch(acts []Action) {
	for _, a := range acts {
		safe(func() { e.onAction(a) })
	}
}

func safe(f func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in hotkey handler: %v", r)
		}
	}()
	f()
}
