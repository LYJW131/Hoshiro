package hotkey

import (
	"reflect"
	"testing"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

func bind(d, a, tr, p []string) model.ShortcutBindings {
	return model.ShortcutBindings{Dictation: d, AskAnything: a, Translation: tr, PasteLastTranscript: p}
}

type sim struct {
	t     *testing.T
	m     *Machine
	phase Phase
	now   uint64
}

func newSim(t *testing.T, b model.ShortcutBindings) *sim { return &sim{t, NewMachine(b), Idle, 1000} }
func defSim(t *testing.T) *sim                           { return newSim(t, model.DefaultShortcuts()) }

func (s *sim) apply(acts []Action) {
	for _, a := range acts {
		switch a.Kind {
		case Start:
			if a.Locked {
				s.phase = RecordingLocked
			} else {
				s.phase = RecordingPtt
			}
		case SwitchMode, Lock:
			s.phase = RecordingLocked
		case Finish:
			s.phase = Processing
		case CancelSilently, TapTooShort, Cancel:
			s.phase = Idle
		}
	}
}
func (s *sim) advance(ms uint64) []Action {
	s.now += ms
	a := s.m.OnTimer(s.now, s.phase)
	s.apply(a)
	return a
}
func (s *sim) ev(k string, down bool, dt uint64) Outcome {
	s.now += dt
	o := s.m.Handle(KeyEvent{k, down, s.now}, s.phase)
	s.apply(o.Actions)
	return o
}
func (s *sim) down(k string, dt uint64) Outcome { return s.ev(k, true, dt) }
func (s *sim) up(k string, dt uint64) Outcome   { return s.ev(k, false, dt) }

func acts(a ...Action) []Action { return a }
func start(m model.Mode, locked bool) []Action {
	return []Action{{Kind: Start, Mode: m, Locked: locked}}
}

var (
	fin    = []Action{{Kind: Finish}}
	lock   = []Action{{Kind: Lock}}
	cancel = []Action{{Kind: Cancel}}
	silent = []Action{{Kind: CancelSilently}}
)

func eq(t *testing.T, got, want []Action) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPttHoldAndRelease(t *testing.T) {
	s := defSim(t)
	eq(t, s.down("Fn", 0).Actions, start(model.ModeDictation, false))
	o := s.up("Fn", 600)
	eq(t, o.Actions, fin)
	if o.Swallow {
		t.Fatal()
	}
	if _, ok := s.m.NextDeadline(); ok {
		t.Fatal()
	}
	s = defSim(t)
	s.down("Fn", 0)
	eq(t, s.up("Fn", 350).Actions, fin)
}

func TestShortTap(t *testing.T) {
	s := defSim(t)
	s.down("Fn", 0)
	eq(t, s.up("Fn", 100).Actions, nil)
	if d, _ := s.m.NextDeadline(); d != s.now+DoublePressWindowMs {
		t.Fatal(d)
	}
	eq(t, s.advance(299), nil)
	eq(t, s.advance(1), acts(Action{Kind: TapTooShort}))
	if s.phase != Idle {
		t.Fatal()
	}
	eq(t, s.advance(1000), nil)
}

func TestDoublePressLocks(t *testing.T) {
	s := defSim(t)
	s.down("Fn", 0)
	s.up("Fn", 80)
	eq(t, s.down("Fn", 120).Actions, lock)
	eq(t, s.up("Fn", 70).Actions, nil)
	eq(t, s.advance(1000), nil)
	if s.phase != RecordingLocked {
		t.Fatal()
	}
	s.down("Fn", 2000)
	eq(t, s.up("Fn", 60).Actions, nil)
	if s.phase != Processing {
		t.Fatal(s.phase)
	}
}

func TestLockedSwallowedKey(t *testing.T) {
	s := newSim(t, bind([]string{"Help"}, nil, nil, nil))
	if !s.down("Help", 0).Swallow || !s.up("Help", 80).Swallow || !s.down("Help", 100).Swallow || s.phase != RecordingLocked {
		t.Fatal()
	}
	s.up("Help", 40)
	o := s.down("Help", 1000)
	eq(t, o.Actions, fin)
	if !o.Swallow || !s.up("Help", 50).Swallow {
		t.Fatal()
	}
}

func TestFnSpaceAsk(t *testing.T) {
	s := defSim(t)
	s.down("Fn", 0)
	o := s.down("Space", 200)
	eq(t, o.Actions, acts(Action{Kind: SwitchMode, Mode: model.ModeAsk}))
	if !o.Swallow || !s.down("Space", 30).Swallow || !s.up("Space", 50).Swallow {
		t.Fatal()
	}
	eq(t, s.up("Fn", 50).Actions, nil)
	eq(t, s.down("Fn", 800).Actions, fin)
	s = defSim(t)
	s.down("Fn", 0)
	o = s.down("LeftShift", 300)
	eq(t, o.Actions, acts(Action{Kind: SwitchMode, Mode: model.ModeTranslation}))
	if o.Swallow {
		t.Fatal()
	}
}

func TestFinishThenSpace(t *testing.T) {
	s := defSim(t)
	s.down("Fn", 0)
	s.down("Space", 100)
	s.up("Space", 20)
	s.up("Fn", 20)
	eq(t, s.down("Fn", 1000).Actions, fin)
	o := s.down("Space", 10)
	if len(o.Actions) != 0 || !o.Swallow || !s.up("Space", 10).Swallow {
		t.Fatal(o)
	}
}

func TestOtherKey(t *testing.T) {
	s := defSim(t)
	s.down("Fn", 0)
	o := s.down("Left", 200)
	eq(t, o.Actions, silent)
	if o.Swallow || s.up("Left", 50).Swallow {
		t.Fatal()
	}
	eq(t, s.up("Fn", 50).Actions, nil)
	eq(t, s.advance(2000), nil)

	s = defSim(t)
	s.down("Fn", 0)
	eq(t, s.down("Left", 2000).Actions, nil)
	s.up("Left", 10)
	eq(t, s.up("Fn", 100).Actions, fin)
}

func TestEsc(t *testing.T) {
	s := defSim(t)
	if o := s.down("Esc", 0); len(o.Actions) > 0 || o.Swallow || s.up("Esc", 10).Swallow {
		t.Fatal()
	}
	s.down("Fn", 100)
	o := s.down("Esc", 100)
	eq(t, o.Actions, cancel)
	if !o.Swallow || !s.up("Esc", 10).Swallow {
		t.Fatal()
	}
	eq(t, s.up("Fn", 10).Actions, nil)
	s = defSim(t)
	s.phase = Processing
	eq(t, s.down("Esc", 0).Actions, cancel)
	s = defSim(t)
	s.down("Fn", 0)
	s.up("Fn", 100)
	eq(t, s.down("Esc", 50).Actions, cancel)
	eq(t, s.advance(1000), nil)
}

func TestPasteLast(t *testing.T) {
	s := defSim(t)
	s.down("LeftCtrl", 0)
	s.down("RightCmd", 10)
	o := s.down("V", 10)
	eq(t, o.Actions, acts(Action{Kind: PasteLast}))
	if !o.Swallow || !s.up("V", 10).Swallow {
		t.Fatal()
	}
	s = defSim(t)
	s.down("LeftShift", 0)
	s.down("LeftCtrl", 10)
	s.down("LeftCmd", 10)
	if o := s.down("V", 10); len(o.Actions) > 0 || o.Swallow {
		t.Fatal()
	}
}

func TestMultipleBindings(t *testing.T) {
	s := newSim(t, bind([]string{"Fn", "Help", "RightOption"}, []string{"Fn+Space"}, []string{"Fn+LeftShift"}, nil))
	eq(t, s.down("Fn", 0).Actions, start(model.ModeDictation, false))
	eq(t, s.up("Fn", 700).Actions, fin)
	s.advance(5)
	s.phase = Idle
	o := s.down("Help", 500)
	eq(t, o.Actions, start(model.ModeDictation, false))
	if !o.Swallow {
		t.Fatal()
	}
	eq(t, s.up("Help", 600).Actions, fin)
	s.phase = Idle
	eq(t, s.down("RightOption", 500).Actions, start(model.ModeDictation, false))
	eq(t, s.up("RightOption", 900).Actions, fin)
	s.phase = Idle
	s.down("Help", 500)
	eq(t, s.down("Space", 100).Actions, silent)

	s = newSim(t, bind([]string{"Fn", "RightOption"}, nil, nil, nil))
	s.down("Fn", 0)
	eq(t, s.down("RightOption", 200).Actions, nil)
	s.up("RightOption", 200)
	eq(t, s.up("Fn", 100).Actions, fin)
}

func TestRightOption(t *testing.T) {
	b := bind([]string{"RightOption"}, nil, nil, nil)
	s := newSim(t, b)
	s.down("RightOption", 0)
	o := s.down("E", 150)
	eq(t, o.Actions, silent)
	s.up("E", 30)
	eq(t, s.up("RightOption", 30).Actions, nil)
	eq(t, newSim(t, b).down("LeftOption", 0).Actions, nil)
}

func TestStartRules(t *testing.T) {
	s := defSim(t)
	s.phase = Processing
	eq(t, s.down("Fn", 0).Actions, start(model.ModeDictation, false))
	s = defSim(t)
	s.down("LeftCmd", 0)
	eq(t, s.down("Fn", 10).Actions, nil)
	s = defSim(t)
	s.down("A", 0)
	eq(t, s.down("Fn", 10).Actions, start(model.ModeDictation, false))

	s = newSim(t, bind([]string{"Fn"}, []string{"RightCmd+A"}, nil, nil))
	s.down("RightCmd", 0)
	o := s.down("A", 20)
	eq(t, o.Actions, start(model.ModeAsk, true))
	if !o.Swallow || !s.up("A", 10).Swallow {
		t.Fatal()
	}
	s.up("RightCmd", 10)
	s.down("RightCmd", 1000)
	eq(t, s.down("A", 10).Actions, fin)

	s = newSim(t, bind([]string{"RightOption"}, nil, []string{"Fn+LeftShift"}, nil))
	eq(t, s.down("Fn", 0).Actions, nil)
	eq(t, s.down("LeftShift", 10).Actions, start(model.ModeTranslation, true))
}

func TestLaggingController(t *testing.T) {
	m := NewMachine(model.DefaultShortcuts())
	eq(t, m.Handle(KeyEvent{"Fn", true, 0}, Idle).Actions, start(model.ModeDictation, false))
	eq(t, m.Handle(KeyEvent{"Space", true, 100}, Idle).Actions, acts(Action{Kind: SwitchMode, Mode: model.ModeAsk}))
	eq(t, m.Handle(KeyEvent{"Fn", false, 200}, Idle).Actions, nil)
	m = NewMachine(model.DefaultShortcuts())
	m.Handle(KeyEvent{"Fn", true, 0}, Idle)
	eq(t, m.Handle(KeyEvent{"Fn", false, 600}, RecordingPtt).Actions, fin)
	m = NewMachine(model.DefaultShortcuts())
	m.Handle(KeyEvent{"Fn", true, 0}, Idle)
	eq(t, m.Handle(KeyEvent{"Fn", false, 2000}, Idle).Actions, fin)
	m = NewMachine(model.DefaultShortcuts())
	m.Handle(KeyEvent{"Fn", true, 0}, Idle)
	eq(t, m.Handle(KeyEvent{"Fn", false, 5_000_000}, Processing).Actions, nil)
}

func TestBindingsChange(t *testing.T) {
	s := defSim(t)
	s.m.SetBindings(bind([]string{"RightOption"}, nil, nil, nil))
	eq(t, s.down("Fn", 0).Actions, nil)
	s.up("Fn", 10)
	eq(t, s.down("RightOption", 10).Actions, start(model.ModeDictation, false))
	s = newSim(t, bind([]string{"Bogus", "Fn"}, []string{"Fn+"}, nil, nil))
	eq(t, s.down("Fn", 0).Actions, start(model.ModeDictation, false))
}

func TestParse(t *testing.T) {
	k := func(n string) KeySpec { return KeySpec{Key: n} }
	a := func(f Family) KeySpec { return KeySpec{Any: f} }
	cases := map[string]Binding{
		"Fn": {k("Fn")}, "Fn+LeftShift": {k("Fn"), k("LeftShift")}, "Ctrl+Cmd+V": {a(FamCtrl), a(FamCmd), k("V")},
		"Left Shift": {k("LeftShift")}, "right option": {k("RightOption")}, "Help": {k("Help")}, "fn+space": {k("Fn"), k("Space")},
		"Cmd+ArrowUp": {a(FamCmd), k("Up")}, "Mouse4": {k("Mouse4")}, "Cmd++": {a(FamCmd), k("=")}, "F13": {k("F13")},
	}
	for s, want := range cases {
		if got, ok := ParseBinding(s); !ok || !reflect.DeepEqual(got, want) {
			t.Error(s, got)
		}
	}
	for _, s := range []string{"", "Fn+", "Nope", "Fn+Fn"} {
		if _, ok := ParseBinding(s); ok {
			t.Error(s)
		}
	}
	b, _ := ParseBinding("Ctrl+Cmd+V")
	for held, ok := range map[string]bool{"LeftCtrl LeftCmd V": true, "RightCtrl RightCmd V": true, "LeftCtrl V": false, "LeftCtrl LeftCmd LeftShift V": false} {
		if _, got := MatchBinding(b, splitSp(held)); got != ok {
			t.Error(held)
		}
	}
	b, _ = ParseBinding("Cmd+A")
	if _, ok := MatchBinding(b, []string{"LeftCmd", "RightCmd", "A"}); ok {
		t.Error("both sides")
	}
	if a(FamShift).Accepts("Fn") {
		t.Error()
	}
}

func splitSp(s string) []string {
	var out []string
	cur := ""
	for _, c := range s + " " {
		if c == ' ' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	return out
}

func TestKeycodes(t *testing.T) {
	for c, n := range map[uint16]string{63: "Fn", 114: "Help", 53: "Esc", 49: "Space", 56: "LeftShift", 60: "RightShift", 59: "LeftCtrl",
		62: "RightCtrl", 58: "LeftOption", 61: "RightOption", 55: "LeftCmd", 54: "RightCmd", 9: "V", 96: "F5", 126: "Up"} {
		if got, _ := KeyForCode(c); got != n {
			t.Error(c)
		}
	}
	if c, _ := CodeForKey("V"); c != 9 {
		t.Error()
	}
	if n, _ := MouseButtonName(3); n != "Mouse4" {
		t.Error()
	}
	if _, ok := MouseButtonName(0); ok {
		t.Error()
	}
	for i, a := range keys {
		for _, b := range keys[i+1:] {
			if a.code == b.code || a.name == b.name {
				t.Error(a, b)
			}
		}
	}
}

func TestFlags(t *testing.T) {
	type r struct {
		n  string
		d  bool
		ok bool
	}
	chk := func(code uint16, f uint64, want r) {
		n, d, ok := ModifierTransition(code, f)
		if (r{n, d, ok}) != want {
			t.Error(code, f, n, d, ok)
		}
	}
	chk(63, FlagFn, r{"Fn", true, true})
	chk(63, 0, r{"Fn", false, true})
	chk(56, FlagShift|devLShift, r{"LeftShift", true, true})
	chk(56, FlagShift|devRShift, r{"LeftShift", false, true})
	chk(60, FlagShift|devRShift, r{"RightShift", true, true})
	chk(62, FlagCtrl|devRCtrl, r{"RightCtrl", true, true})
	chk(54, FlagCmd|devRCmd|FlagFn, r{"RightCmd", true, true})
	chk(58, FlagOption, r{"LeftOption", true, true})
	chk(57, 0x10000, r{"", false, false})
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{"V LeftCmd LeftCtrl": "LeftCtrl+LeftCmd+V", "Space Fn": "Fn+Space",
		"RightCmd LeftShift RightOption LeftCtrl Fn": "Fn+LeftCtrl+RightOption+LeftShift+RightCmd", "LeftCmd RightCmd": "LeftCmd+RightCmd", "B A Cmd": "Cmd+B+A"} {
		if got := CanonicalCombo(splitSp(in)); got != want {
			t.Error(got)
		}
	}
	if n, _ := NormalizeBinding("cmd+ctrl+v"); n != "Ctrl+Cmd+V" {
		t.Error(n)
	}
	if n, _ := NormalizeBinding("space+fn"); n != "Fn+Space" {
		t.Error(n)
	}
	if _, ok := NormalizeBinding("nonsense"); ok {
		t.Error()
	}
}

func TestCapture(t *testing.T) {
	var c CaptureMachine
	chk := func(k string, d bool, combo string, done, changed bool) {
		t.Helper()
		a, b, ch := c.OnKey(k, d)
		if a != combo || b != done || ch != changed {
			t.Fatal(k, a, b, ch)
		}
	}
	chk("Space", true, "Space", false, true)
	c.Reset()
	chk("Fn", true, "Fn", false, true)
	chk("Space", true, "Fn+Space", false, true)
	chk("Space", true, "", false, false)
	chk("Space", false, "Fn", false, true)
	chk("Fn", false, "Fn+Space", true, true)
	chk("RightOption", true, "RightOption", false, true)
	chk("RightOption", false, "RightOption", true, true)
	chk("V", true, "V", false, true)
	chk("LeftCmd", true, "LeftCmd+V", false, true)
	chk("LeftCtrl", true, "LeftCtrl+LeftCmd+V", false, true)
	c.OnKey("LeftCtrl", false)
	c.OnKey("LeftCmd", false)
	chk("V", false, "LeftCtrl+LeftCmd+V", true, true)
}
