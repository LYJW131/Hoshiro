package hotkey

import (
	"testing"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

func TestEngineTapTimer(t *testing.T) {
	got := make(chan Action, 8)
	e := NewEngine(model.DefaultShortcuts(), func() Phase { return Idle }, func(a Action) { got <- a })
	defer e.Close()
	e.Key("Fn", true)
	if a := <-got; a.Kind != Start {
		t.Fatal(a)
	}
	e.Flags("Fn", false, 0)
	select {
	case a := <-got:
		if a.Kind != TapTooShort {
			t.Fatal(a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no timer action")
	}
}

func TestEngineCaptureAndRepair(t *testing.T) {
	caps := make(chan string, 8)
	e := NewEngine(model.DefaultShortcuts(), func() Phase { return Idle }, func(Action) {})
	defer e.Close()
	e.StartCapture(func(k string, done bool) {
		if done {
			caps <- k
		}
	})
	if !e.Key("Space", true) {
		t.Fatal("capture swallows regular keys")
	}
	e.Flags("Fn", true, FlagFn)
	e.Flags("Fn", false, 0)
	e.Key("Space", false)
	if k := <-caps; k != "Fn+Space" {
		t.Fatal(k)
	}
	e.StopCapture()
	// stale LeftShift (missed up) is repaired by a later flags event without shift
	e.Flags("LeftShift", true, FlagShift|devLShift)
	e.Flags("LeftCmd", true, FlagCmd|devLCmd)
	e.mu.Lock()
	h := e.machine.HeldKeys()
	e.mu.Unlock()
	if len(h) != 1 || h[0] != "LeftCmd" {
		t.Fatal(h)
	}
}
