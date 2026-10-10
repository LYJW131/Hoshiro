// Package hotkey holds the pure hotkey logic: key naming, binding parsing and
// the Typeless-style gesture state machine. No OS calls: everything is driven
// by explicit events and timestamps, so it is unit-testable. The macOS event
// tap that feeds it lives in package platform.
package hotkey

import (
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

const (
	// HoldThresholdMs: held at least this long, a push-to-talk release finishes the recording.
	HoldThresholdMs = 350
	// DoublePressWindowMs: after a short tap, wait this long for a second press (hands-free lock).
	DoublePressWindowMs = 300
	// OtherKeyWindowMs: another key within this time after the dictation key cancels silently.
	OtherKeyWindowMs = 1500
	// assumedPhaseGraceMs: our own view of the phase wins this long after we emitted an action.
	assumedPhaseGraceMs = 400
)

type Phase int

const (
	Idle Phase = iota
	RecordingPtt
	RecordingLocked
	Processing
)

type ActionKind int

const (
	Start ActionKind = iota
	SwitchMode
	Lock
	Finish
	CancelSilently
	TapTooShort
	Cancel
	PasteLast
)

// Action is what the controller is asked to do.
type Action struct {
	Kind   ActionKind
	Mode   model.Mode // Start, SwitchMode
	Locked bool       // Start
}

func (a Action) String() string {
	return fmt.Sprintf("%s(%s,%v)", [...]string{"Start", "SwitchMode", "Lock", "Finish", "CancelSilently", "TapTooShort", "Cancel", "PasteLast"}[a.Kind], a.Mode, a.Locked)
}

// ── key tables ──

type kc struct {
	code uint16
	name string
}

// keys maps macOS virtual keycodes to canonical key names.
var keys = []kc{
	{0, "A"}, {11, "B"}, {8, "C"}, {2, "D"}, {14, "E"}, {3, "F"}, {5, "G"}, {4, "H"}, {34, "I"},
	{38, "J"}, {40, "K"}, {37, "L"}, {46, "M"}, {45, "N"}, {31, "O"}, {35, "P"}, {12, "Q"},
	{15, "R"}, {1, "S"}, {17, "T"}, {32, "U"}, {9, "V"}, {13, "W"}, {7, "X"}, {16, "Y"}, {6, "Z"},
	{29, "0"}, {18, "1"}, {19, "2"}, {20, "3"}, {21, "4"}, {23, "5"}, {22, "6"}, {26, "7"},
	{28, "8"}, {25, "9"},
	{122, "F1"}, {120, "F2"}, {99, "F3"}, {118, "F4"}, {96, "F5"}, {97, "F6"}, {98, "F7"},
	{100, "F8"}, {101, "F9"}, {109, "F10"}, {103, "F11"}, {111, "F12"}, {105, "F13"}, {107, "F14"},
	{113, "F15"}, {106, "F16"}, {64, "F17"}, {79, "F18"}, {80, "F19"}, {90, "F20"},
	{123, "Left"}, {124, "Right"}, {125, "Down"}, {126, "Up"},
	{49, "Space"}, {53, "Esc"}, {36, "Enter"}, {48, "Tab"}, {51, "Backspace"}, {117, "Delete"},
	{115, "Home"}, {119, "End"}, {116, "PageUp"}, {121, "PageDown"}, {114, "Help"},
	{27, "-"}, {24, "="}, {33, "["}, {30, "]"}, {42, "\\"}, {41, ";"}, {39, "'"}, {43, ","},
	{47, "."}, {44, "/"}, {50, "`"},
	{82, "Num0"}, {83, "Num1"}, {84, "Num2"}, {85, "Num3"}, {86, "Num4"}, {87, "Num5"},
	{88, "Num6"}, {89, "Num7"}, {91, "Num8"}, {92, "Num9"}, {76, "NumEnter"},
	{63, "Fn"}, {56, "LeftShift"}, {60, "RightShift"}, {59, "LeftCtrl"}, {62, "RightCtrl"},
	{58, "LeftOption"}, {61, "RightOption"}, {55, "LeftCmd"}, {54, "RightCmd"},
}

// mouse maps CGEvent button numbers to names; left/right are never tapped.
var mouse = map[int64]string{2: "Mouse3", 3: "Mouse4", 4: "Mouse5"}

func KeyForCode(code uint16) (string, bool) {
	for _, k := range keys {
		if k.code == code {
			return k.name, true
		}
	}
	return "", false
}

func CodeForKey(name string) (uint16, bool) {
	for _, k := range keys {
		if k.name == name {
			return k.code, true
		}
	}
	return 0, false
}

func MouseButtonName(b int64) (string, bool) { n, ok := mouse[b]; return n, ok }

func MouseButtonNumber(name string) (int64, bool) {
	for b, n := range mouse {
		if n == name {
			return b, true
		}
	}
	return 0, false
}

// UnknownKeyName names keycodes/buttons missing from the tables.
func UnknownKeyName(kind string, id int64) string { return fmt.Sprintf("%s%d", kind, id) }

type Family int

const (
	FamNone Family = iota
	FamFn
	FamCtrl
	FamOption
	FamShift
	FamCmd
)

func ModifierFamily(name string) Family {
	switch name {
	case "Fn":
		return FamFn
	case "LeftCtrl", "RightCtrl":
		return FamCtrl
	case "LeftOption", "RightOption":
		return FamOption
	case "LeftShift", "RightShift":
		return FamShift
	case "LeftCmd", "RightCmd":
		return FamCmd
	}
	return FamNone
}

func IsModifier(name string) bool { return ModifierFamily(name) != FamNone }

// CGEventFlags device-independent masks.
const (
	FlagShift  uint64 = 0x0002_0000
	FlagCtrl   uint64 = 0x0004_0000
	FlagOption uint64 = 0x0008_0000
	FlagCmd    uint64 = 0x0010_0000
	FlagFn     uint64 = 0x0080_0000

	devLCtrl  uint64 = 0x0001
	devLShift uint64 = 0x0002
	devRShift uint64 = 0x0004
	devLCmd   uint64 = 0x0008
	devRCmd   uint64 = 0x0010
	devLOpt   uint64 = 0x0020
	devROpt   uint64 = 0x0040
	devRCtrl  uint64 = 0x2000
	devAll           = devLCtrl | devLShift | devRShift | devLCmd | devRCmd | devLOpt | devROpt | devRCtrl
)

func FamilyFlag(f Family) uint64 {
	return map[Family]uint64{FamFn: FlagFn, FamCtrl: FlagCtrl, FamOption: FlagOption, FamShift: FlagShift, FamCmd: FlagCmd}[f]
}

// ModifierTransition interprets a flagsChanged event: which modifier moved, and whether it went down.
func ModifierTransition(code uint16, flags uint64) (string, bool, bool) {
	var name string
	var dev, gen uint64
	switch code {
	case 63:
		name, dev, gen = "Fn", 0, FlagFn
	case 56:
		name, dev, gen = "LeftShift", devLShift, FlagShift
	case 60:
		name, dev, gen = "RightShift", devRShift, FlagShift
	case 59:
		name, dev, gen = "LeftCtrl", devLCtrl, FlagCtrl
	case 62:
		name, dev, gen = "RightCtrl", devRCtrl, FlagCtrl
	case 58:
		name, dev, gen = "LeftOption", devLOpt, FlagOption
	case 61:
		name, dev, gen = "RightOption", devROpt, FlagOption
	case 55:
		name, dev, gen = "LeftCmd", devLCmd, FlagCmd
	case 54:
		name, dev, gen = "RightCmd", devRCmd, FlagCmd
	default:
		return "", false, false
	}
	if dev == 0 || flags&devAll == 0 {
		return name, flags&gen != 0, true
	}
	return name, flags&dev != 0, true
}

// ── bindings ──

// KeySpec is a concrete key, or a generic modifier family matching either side.
type KeySpec struct {
	Key string
	Any Family
}

func (s KeySpec) Accepts(key string) bool {
	if s.Any == FamNone {
		return s.Key == key
	}
	return ModifierFamily(key) == s.Any && s.Any != FamFn
}

type Binding []KeySpec

func lookupToken(token string) (KeySpec, bool) {
	var sb strings.Builder
	for _, c := range token {
		if !unicode.IsSpace(c) && c != '_' {
			sb.WriteRune(unicode.ToLower(c))
		}
	}
	n := sb.String()
	switch n {
	case "fn", "globe":
		return KeySpec{Key: "Fn"}, true
	case "ctrl", "control":
		return KeySpec{Any: FamCtrl}, true
	case "option", "opt", "alt":
		return KeySpec{Any: FamOption}, true
	case "shift":
		return KeySpec{Any: FamShift}, true
	case "cmd", "command", "meta", "super":
		return KeySpec{Any: FamCmd}, true
	}
	alias := map[string]string{
		"leftctrl": "LeftCtrl", "leftcontrol": "LeftCtrl", "lctrl": "LeftCtrl",
		"rightctrl": "RightCtrl", "rightcontrol": "RightCtrl", "rctrl": "RightCtrl",
		"leftoption": "LeftOption", "leftalt": "LeftOption", "loption": "LeftOption", "lalt": "LeftOption",
		"rightoption": "RightOption", "rightalt": "RightOption", "roption": "RightOption", "ralt": "RightOption",
		"leftshift": "LeftShift", "lshift": "LeftShift", "rightshift": "RightShift", "rshift": "RightShift",
		"leftcmd": "LeftCmd", "leftcommand": "LeftCmd", "lcmd": "LeftCmd",
		"rightcmd": "RightCmd", "rightcommand": "RightCmd", "rcmd": "RightCmd",
		"escape": "Esc", "esc": "Esc", "return": "Enter", "enter": "Enter", "backspace": "Backspace",
		"delete": "Delete", "forwarddelete": "Delete", "del": "Delete", "help": "Help", "insert": "Help", "ins": "Help",
		"space": "Space", "spacebar": "Space", "tab": "Tab", "home": "Home", "end": "End", "pageup": "PageUp", "pagedown": "PageDown",
		"up": "Up", "arrowup": "Up", "uparrow": "Up", "down": "Down", "arrowdown": "Down", "downarrow": "Down",
		"left": "Left", "arrowleft": "Left", "leftarrow": "Left", "right": "Right", "arrowright": "Right", "rightarrow": "Right",
		"plus": "=", "minus": "-", "mouse3": "Mouse3", "middlemouse": "Mouse3", "mouse4": "Mouse4", "mouse5": "Mouse5",
	}[n]
	if alias != "" {
		return KeySpec{Key: alias}, true
	}
	for _, k := range keys {
		if strings.EqualFold(k.name, n) {
			return KeySpec{Key: k.name}, true
		}
	}
	return KeySpec{}, false
}

// ParseBinding parses "Fn+LeftShift", "Ctrl+Cmd+V", "RightOption"...; unknown tokens invalidate it.
func ParseBinding(s string) (Binding, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	tokens := strings.Split(s, "+")
	for i := range tokens {
		tokens[i] = strings.TrimSpace(tokens[i])
	}
	if n := len(tokens); n >= 2 && tokens[n-1] == "" && tokens[n-2] == "" {
		tokens = append(tokens[:n-2], "plus")
	}
	var b Binding
	for _, t := range tokens {
		if t == "" {
			return nil, false
		}
		spec, ok := lookupToken(t)
		if !ok || slices.Contains(b, spec) {
			return nil, false
		}
		b = append(b, spec)
	}
	return b, true
}

func specName(s KeySpec) string {
	switch s.Any {
	case FamCtrl:
		return "Ctrl"
	case FamOption:
		return "Option"
	case FamShift:
		return "Shift"
	case FamCmd:
		return "Cmd"
	case FamFn:
		return "Fn"
	}
	return s.Key
}

func modifierRank(n string) int {
	r, ok := map[string]int{"Fn": 0, "Ctrl": 10, "LeftCtrl": 11, "RightCtrl": 12, "Option": 20, "LeftOption": 21, "RightOption": 22,
		"Shift": 30, "LeftShift": 31, "RightShift": 32, "Cmd": 40, "LeftCmd": 41, "RightCmd": 42}[n]
	if !ok {
		return 100
	}
	return r
}

// CanonicalCombo orders Fn, Ctrl, Option, Shift, Cmd (left before right), then other keys in press order.
func CanonicalCombo(ks []string) string {
	s := slices.Clone(ks)
	sort.SliceStable(s, func(i, j int) bool { return modifierRank(s[i]) < modifierRank(s[j]) })
	return strings.Join(s, "+")
}

// NormalizeBinding returns the canonical form of a binding, false if invalid.
func NormalizeBinding(s string) (string, bool) {
	b, ok := ParseBinding(s)
	if !ok {
		return "", false
	}
	names := make([]string, len(b))
	for i, x := range b {
		names[i] = specName(x)
	}
	return CanonicalCombo(names), true
}

// MatchBinding reports whether held satisfies specs exactly, returning the indices it used.
// Every spec needs a distinct held key, every held modifier must be used, other regular keys are ignored.
func MatchBinding(specs Binding, held []string) ([]int, bool) {
	used := make([]bool, len(held))
	take := func(ok func(string) bool) bool {
		for i, h := range held {
			if !used[i] && ok(h) {
				used[i] = true
				return true
			}
		}
		return false
	}
	for _, s := range specs {
		if s.Any == FamNone && !take(func(h string) bool { return h == s.Key }) {
			return nil, false
		}
	}
	for _, s := range specs {
		if s.Any != FamNone && !take(s.Accepts) {
			return nil, false
		}
	}
	var idx []int
	for i, h := range held {
		if !used[i] && IsModifier(h) {
			return nil, false
		}
		if used[i] {
			idx = append(idx, i)
		}
	}
	return idx, true
}

// ── gesture machine ──

type KeyEvent struct {
	Key  string
	Down bool
	TMs  uint64 // monotonic milliseconds
}

type Outcome struct {
	Actions []Action
	Swallow bool // remove the event from the system stream
}

type ptt struct {
	keys   []string
	downAt uint64
}

type found int

const (
	notFound found = iota
	blocked
	hit
)

// Machine is the Typeless gesture state machine.
type Machine struct {
	dictation, ask, translation, pasteLast []Binding
	held, swallowed, consumed              []string
	ptt                                    *ptt
	pendingTap                             *uint64
	assumed                                *struct {
		p Phase
		t uint64
	}
	mode model.Mode
}

func compile(list []string, what string) []Binding {
	var out []Binding
	for _, s := range list {
		if b, ok := ParseBinding(s); ok {
			out = append(out, b)
		} else {
			log.Printf("ignoring invalid %s shortcut %q", what, s)
		}
	}
	return out
}

func NewMachine(b model.ShortcutBindings) *Machine {
	m := &Machine{mode: model.ModeDictation}
	m.SetBindings(b)
	return m
}

func (m *Machine) SetBindings(b model.ShortcutBindings) {
	m.dictation = compile(b.Dictation, "dictation")
	m.ask = compile(b.AskAnything, "askAnything")
	m.translation = compile(b.Translation, "translation")
	m.pasteLast = compile(b.PasteLastTranscript, "pasteLastTranscript")
	m.ptt, m.pendingTap = nil, nil
}

// Reset forgets all input state; bindings are kept.
func (m *Machine) Reset() {
	m.held, m.swallowed, m.consumed = nil, nil, nil
	m.ptt, m.pendingTap, m.assumed = nil, nil, nil
}

func (m *Machine) HeldKeys() []string { return m.held }

func (m *Machine) HeldModifiers() []string {
	var out []string
	for _, k := range m.held {
		if IsModifier(k) {
			out = append(out, k)
		}
	}
	return out
}

func (m *Machine) IsHeld(k string) bool { return slices.Contains(m.held, k) }

// NextDeadline is the absolute time at which OnTimer has something to do.
func (m *Machine) NextDeadline() (uint64, bool) {
	if m.pendingTap == nil {
		return 0, false
	}
	return *m.pendingTap, true
}

func (m *Machine) OnTimer(now uint64, _ Phase) []Action {
	if m.pendingTap != nil && now >= *m.pendingTap {
		m.pendingTap = nil
		m.assume(Idle, now)
		return []Action{{Kind: TapTooShort}}
	}
	return nil
}

func (m *Machine) Handle(ev KeyEvent, reported Phase) Outcome {
	p := m.effectivePhase(reported, ev.TMs)
	if ev.Down {
		return m.onDown(ev, p)
	}
	return m.onUp(ev, p)
}

func (m *Machine) effectivePhase(reported Phase, now uint64) Phase {
	if a := m.assumed; a != nil && now-min(now, a.t) < assumedPhaseGraceMs {
		if a.p == reported {
			m.assumed = nil
		}
		return a.p
	}
	m.assumed = nil
	return reported
}

func (m *Machine) assume(p Phase, now uint64) {
	m.assumed = &struct {
		p Phase
		t uint64
	}{p, now}
}

func (m *Machine) find(list []Binding, key string) (found, []string) {
	keyIdx := -1
	for i := len(m.held) - 1; i >= 0; i-- {
		if m.held[i] == key {
			keyIdx = i
			break
		}
	}
	if keyIdx < 0 {
		return notFound, nil
	}
	for _, b := range list {
		if used, ok := MatchBinding(b, m.held); ok && slices.Contains(used, keyIdx) {
			names := make([]string, len(used))
			for i, u := range used {
				names[i] = m.held[u]
			}
			for _, n := range names {
				if slices.Contains(m.consumed, n) {
					return blocked, nil
				}
			}
			return hit, names
		}
	}
	return notFound, nil
}

func (m *Machine) isDictationKey(key string) bool {
	for _, b := range m.dictation {
		for _, s := range b {
			if s.Accepts(key) {
				return true
			}
		}
	}
	return false
}

func (m *Machine) swallowIfRegular(key string, out *Outcome) {
	if !IsModifier(key) {
		out.Swallow = true
		if !slices.Contains(m.swallowed, key) {
			m.swallowed = append(m.swallowed, key)
		}
	}
}

func (m *Machine) modeCombo(key string) (model.Mode, []string, bool) {
	if f, used := m.find(m.translation, key); f == hit {
		return model.ModeTranslation, used, true
	}
	if f, used := m.find(m.ask, key); f == hit {
		return model.ModeAsk, used, true
	}
	return "", nil, false
}

func (m *Machine) modeComboBlocked(key string) bool {
	for _, l := range [][]Binding{m.translation, m.ask, m.dictation} {
		if f, _ := m.find(l, key); f == blocked {
			return true
		}
	}
	return false
}

func (m *Machine) onDown(ev KeyEvent, p Phase) Outcome {
	key, t := ev.Key, ev.TMs
	var out Outcome
	if slices.Contains(m.held, key) {
		out.Swallow = slices.Contains(m.swallowed, key) // autorepeat
		return out
	}
	m.held = append(m.held, key)
	if key == "Esc" && p != Idle {
		m.ptt, m.pendingTap = nil, nil
		m.consumed = slices.Clone(m.held)
		m.assume(Idle, t)
		out.Actions = append(out.Actions, Action{Kind: Cancel})
		m.swallowIfRegular(key, &out)
		return out
	}
	switch f, _ := m.find(m.pasteLast, key); f {
	case hit:
		out.Actions = append(out.Actions, Action{Kind: PasteLast})
		m.swallowIfRegular(key, &out)
		return out
	case blocked:
		m.swallowIfRegular(key, &out)
		return out
	}
	switch p {
	case Idle, Processing:
		m.downStartable(key, t, &out)
	case RecordingPtt:
		m.downPtt(key, t, &out)
	case RecordingLocked:
		m.downLocked(key, t, &out)
	}
	return out
}

func (m *Machine) downStartable(key string, t uint64, out *Outcome) {
	switch f, used := m.find(m.dictation, key); f {
	case hit:
		m.ptt = &ptt{used, t}
		m.pendingTap = nil
		m.mode = model.ModeDictation
		m.assume(RecordingPtt, t)
		out.Actions = append(out.Actions, Action{Kind: Start, Mode: model.ModeDictation})
		m.swallowIfRegular(key, out)
		return
	case blocked:
		m.swallowIfRegular(key, out)
		return
	}
	if mode, _, ok := m.modeCombo(key); ok {
		m.ptt = nil
		m.mode = mode
		m.assume(RecordingLocked, t)
		out.Actions = append(out.Actions, Action{Kind: Start, Mode: mode, Locked: true})
		m.swallowIfRegular(key, out)
	} else if m.modeComboBlocked(key) {
		m.swallowIfRegular(key, out)
	}
}

func (m *Machine) downPtt(key string, t uint64, out *Outcome) {
	if m.pendingTap != nil {
		switch f, _ := m.find(m.dictation, key); f {
		case hit:
			m.pendingTap, m.ptt = nil, nil
			m.assume(RecordingLocked, t)
			out.Actions = append(out.Actions, Action{Kind: Lock})
			m.swallowIfRegular(key, out)
		case blocked:
			m.swallowIfRegular(key, out)
		}
		return
	}
	if m.ptt == nil || slices.Contains(m.ptt.keys, key) || m.isDictationKey(key) {
		return
	}
	downAt := m.ptt.downAt
	if mode, _, ok := m.modeCombo(key); ok {
		m.ptt = nil
		m.mode = mode
		m.assume(RecordingLocked, t)
		out.Actions = append(out.Actions, Action{Kind: SwitchMode, Mode: mode})
		m.swallowIfRegular(key, out)
	} else if t-min(t, downAt) < OtherKeyWindowMs {
		// The dictation key is used as a modifier (Fn+Arrow, RightOption+E...).
		m.ptt = nil
		m.assume(Idle, t)
		out.Actions = append(out.Actions, Action{Kind: CancelSilently})
	}
}

func (m *Machine) downLocked(key string, t uint64, out *Outcome) {
	switch f, used := m.find(m.dictation, key); f {
	case hit:
		m.finishLocked(used, t, out, key)
		return
	case blocked:
		m.swallowIfRegular(key, out)
		return
	}
	if mode, used, ok := m.modeCombo(key); ok {
		if mode == m.mode {
			m.finishLocked(used, t, out, key)
		} else {
			m.mode = mode
			out.Actions = append(out.Actions, Action{Kind: SwitchMode, Mode: mode})
			m.swallowIfRegular(key, out)
		}
	} else if m.modeComboBlocked(key) {
		m.swallowIfRegular(key, out)
	}
}

func (m *Machine) finishLocked(used []string, t uint64, out *Outcome, key string) {
	m.consumed = append(m.consumed, used...)
	m.ptt = nil
	m.assume(Processing, t)
	out.Actions = append(out.Actions, Action{Kind: Finish})
	m.swallowIfRegular(key, out)
}

func remove(s []string, k string) []string {
	return slices.DeleteFunc(s, func(x string) bool { return x == k })
}

func (m *Machine) onUp(ev KeyEvent, p Phase) Outcome {
	key := ev.Key
	var out Outcome
	if i := slices.Index(m.swallowed, key); i >= 0 {
		m.swallowed = slices.Delete(m.swallowed, i, i+1)
		out.Swallow = true
	}
	m.consumed = remove(m.consumed, key)
	wasHeld := slices.Contains(m.held, key)
	m.held = remove(m.held, key)
	if !wasHeld || m.ptt == nil || !slices.Contains(m.ptt.keys, key) {
		return out
	}
	pt := m.ptt
	m.ptt = nil
	if p == RecordingLocked || p == Processing {
		return out
	}
	if ev.TMs-min(ev.TMs, pt.downAt) >= HoldThresholdMs {
		m.assume(Processing, ev.TMs)
		out.Actions = append(out.Actions, Action{Kind: Finish})
	} else {
		d := ev.TMs + DoublePressWindowMs // recording keeps running meanwhile
		m.pendingTap = &d
	}
	return out
}

// ── shortcut capture ──

// CaptureMachine tracks the keys held while the settings UI records a shortcut.
type CaptureMachine struct{ held, peak []string }

func (c *CaptureMachine) Reset()         { c.held, c.peak = nil, nil }
func (c *CaptureMachine) Held() []string { return c.held }

// OnKey returns (combo, done, changed).
func (c *CaptureMachine) OnKey(key string, down bool) (string, bool, bool) {
	if down {
		if slices.Contains(c.held, key) {
			return "", false, false
		}
		if len(c.held) == 0 {
			c.peak = nil
		}
		c.held = append(c.held, key)
		c.peak = slices.Clone(c.held)
		return CanonicalCombo(c.held), false, true
	}
	i := slices.Index(c.held, key)
	if i < 0 {
		return "", false, false
	}
	c.held = slices.Delete(c.held, i, i+1)
	if len(c.held) == 0 {
		combo := CanonicalCombo(c.peak)
		c.peak = nil
		return combo, true, true
	}
	return CanonicalCombo(c.held), false, true
}

// KeyLabel is the human label of a binding: "Fn" -> "fn", "RightOption" -> "右 ⌥".
func KeyLabel(binding string) string {
	labels := map[string]string{"Fn": "fn", "Help": "Help", "Esc": "esc", "Space": "space", "LeftOption": "左 ⌥", "RightOption": "右 ⌥",
		"Option": "⌥", "LeftCmd": "左 ⌘", "RightCmd": "右 ⌘", "Cmd": "⌘", "LeftShift": "左 ⇧", "RightShift": "右 ⇧", "Shift": "⇧",
		"LeftCtrl": "左 ⌃", "RightCtrl": "右 ⌃", "Ctrl": "⌃"}
	parts := strings.Split(binding, "+")
	for i, p := range parts {
		if l, ok := labels[p]; ok {
			parts[i] = l
		}
	}
	return strings.Join(parts, "")
}
