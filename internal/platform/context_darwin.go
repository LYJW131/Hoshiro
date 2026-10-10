//go:build darwin

package platform

import (
	"log"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/IcyBlue17/hoshiro/internal/macos"
	"github.com/IcyBlue17/hoshiro/internal/model"
)

const (
	axTimeoutSecs       = 0.12
	axBudget            = 50 * time.Millisecond
	axErrCannotComplete = -25204
	axValueCFRange      = 4
	surroundingMax      = 1500
	focusedValueMax     = 100_000
	bundleWeChat        = "com.tencent.xinWeChat"
	bundleSublime       = "com.sublimetext.4"
)

var textRoles = []string{"AXTextField", "AXTextArea", "AXComboBox", "AXSearchField"}

// nonTextRoles are clearly not text input; anything else stays "unknown".
var nonTextRoles = []string{
	"AXButton", "AXCheckBox", "AXRadioButton", "AXPopUpButton", "AXMenuButton", "AXMenuItem", "AXMenuBarItem",
	"AXMenu", "AXMenuBar", "AXImage", "AXStaticText", "AXSlider", "AXTabGroup", "AXToolbar", "AXList", "AXTable",
	"AXOutline", "AXRow", "AXColumn", "AXCell", "AXBrowser", "AXDisclosureTriangle", "AXScrollBar", "AXSplitter",
	"AXLink", "AXProgressIndicator", "AXIncrementor", "AXColorWell", "AXDockItem",
}

// manualAX remembers per pid whether the app accepted AXManualAccessibility (Chromium/Electron).
var (
	manualMu sync.Mutex
	manualAX = map[int32]bool{}
)

type ax struct {
	start time.Time
	hung  bool
}

func (a *ax) overBudget() bool { return a.hung || time.Since(a.start) > axBudget }

// copy returns an owned CF value (caller releases) or 0.
func (a *ax) copy(el uintptr, attr string) uintptr {
	if a.hung || el == 0 {
		return 0
	}
	key := macos.CFStr(attr)
	defer macos.CFRelease(key)
	var out uintptr
	t := time.Now()
	err := axCopyAttr(el, key, &out)
	if err == axErrCannotComplete && time.Since(t) > 80*time.Millisecond {
		log.Printf("AX call %s timed out; skipping the remaining AX reads", attr)
		a.hung = true
	}
	if err != 0 {
		return 0
	}
	return out
}

func (a *ax) str(el uintptr, attr string) (string, bool) {
	v := a.copy(el, attr)
	if v == 0 {
		return "", false
	}
	defer macos.CFRelease(v)
	return macos.GoString(v)
}

func (a *ax) settable(el uintptr, attr string) bool {
	if a.hung {
		return false
	}
	key := macos.CFStr(attr)
	defer macos.CFRelease(key)
	var s uint8
	return axIsSettable(el, key, &s) == 0 && s != 0
}

func cfRange(v uintptr) (macos.Range, bool) {
	var r macos.Range
	if v == 0 || axValueGetType(v) != axValueCFRange {
		return r, false
	}
	return r, axValueGetValue(v, axValueCFRange, unsafe.Pointer(&r))
}

type front struct {
	name, bundle string
	pid          int32
	ok           bool
}

func frontmost() front {
	var f front
	macos.Pool(func() {
		app := macos.Send(macos.Send(macos.Class("NSWorkspace"), "sharedWorkspace"), "frontmostApplication")
		if app == 0 {
			return
		}
		f = front{macos.GoNS(macos.Send(app, "localizedName")), macos.GoNS(macos.Send(app, "bundleIdentifier")),
			macos.SendInt32(app, "processIdentifier"), true}
	})
	return f
}

func ensureManualAccessibility(appEl uintptr, pid int32) bool {
	manualMu.Lock()
	defer manualMu.Unlock()
	if v, ok := manualAX[pid]; ok {
		return v
	}
	key := macos.CFStr("AXManualAccessibility")
	defer macos.CFRelease(key)
	ok := axSetAttr(appEl, key, macos.KCFBooleanTrue) == 0
	if len(manualAX) > 512 {
		clear(manualAX)
	}
	manualAX[pid] = ok
	return ok
}

type focus struct {
	appEl, element uintptr
	chromium       bool
}

func (f focus) release() {
	if f.element != 0 {
		macos.CFRelease(f.element)
	}
	if f.appEl != 0 {
		macos.CFRelease(f.appEl)
	}
}

func resolveFocus(a *ax, pid int32) (focus, bool) {
	appEl := axCreateApplication(pid)
	if appEl == 0 {
		return focus{}, false
	}
	axSetTimeout(appEl, axTimeoutSecs)
	f := focus{appEl: appEl, chromium: ensureManualAccessibility(appEl, pid)}
	f.element = a.copy(appEl, "AXFocusedUIElement")
	if f.element != 0 {
		axSetTimeout(f.element, axTimeoutSecs)
	}
	return f, true
}

func strp(s string) *string { return &s }

// CaptureContext takes a fast (< ~50 ms) snapshot of the frontmost app and
// focused element. With wantSelection it also reads the selected text (AX,
// then a clipboard-restoring Cmd+C round trip if allowCopyFallback).
func CaptureContext(wantSelection, allowCopyFallback bool) model.AppContext {
	var ctx model.AppContext
	fr := frontmost()
	if !fr.ok {
		return ctx
	}
	ctx.AppName, ctx.BundleID, ctx.PID = nonEmpty(fr.name), nonEmpty(fr.bundle), &fr.pid
	if !axIsProcessTrusted() {
		return ctx
	}
	badAX := fr.bundle == bundleWeChat || fr.bundle == bundleSublime
	a := &ax{start: time.Now()}
	if sys := axCreateSystemWide(); sys != 0 {
		axSetTimeout(sys, axTimeoutSecs)
		macos.CFRelease(sys)
	}
	f, ok := resolveFocus(a, fr.pid)
	if !ok {
		return ctx
	}
	defer f.release()

	if !a.overBudget() {
		w := a.copy(f.appEl, "AXFocusedWindow")
		if w == 0 {
			w = a.copy(f.appEl, "AXMainWindow")
		}
		if w != 0 {
			if t, _ := a.str(w, "AXTitle"); t != "" {
				ctx.WindowTitle = strp(t)
			}
			if !a.overBudget() {
				if u, _ := a.str(w, "AXDocument"); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
					ctx.URL = strp(u)
				}
			}
			macos.CFRelease(w)
		}
	}

	knownEmpty := false
	if p := f.element; p != 0 {
		role, _ := a.str(p, "AXRole")
		subrole, _ := a.str(p, "AXSubrole")
		secure := subrole == "AXSecureTextField"
		editable := slices.Contains(textRoles, role) || a.settable(p, "AXValue")
		if !editable && role != "AXStaticText" && role != "AXWebArea" && role != "AXScrollArea" {
			if r := a.copy(p, "AXSelectedTextRange"); r != 0 {
				editable = true
				macos.CFRelease(r)
			}
		}
		switch {
		case badAX:
		case editable:
			ctx.FocusedEditable = boolp(true)
		case slices.Contains(nonTextRoles, role):
			ctx.FocusedEditable = boolp(false)
		}
		if wantSelection && !secure && !a.overBudget() {
			if s, ok := a.str(p, "AXSelectedText"); ok {
				if s != "" {
					ctx.SelectedText = strp(s)
				} else {
					// A definitive "nothing selected" from a native control is trustworthy; Chromium often lies.
					knownEmpty = !f.chromium && slices.Contains(textRoles, role)
				}
			}
		}
		if !secure && !a.overBudget() {
			if s := surroundingText(a, p); s != "" {
				ctx.SurroundingText = strp(s)
			}
		}
	}
	if wantSelection && ctx.SelectedText == nil && allowCopyFallback && !knownEmpty {
		if s := copySelectionViaCmdC(); s != "" {
			ctx.SelectedText = strp(s)
		}
	}
	return ctx
}

func boolp(b bool) *bool { return &b }

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func surroundingText(a *ax, el uintptr) string {
	var rng macos.Range
	haveRange := false
	if v := a.copy(el, "AXSelectedTextRange"); v != 0 {
		rng, haveRange = cfRange(v)
		macos.CFRelease(v)
	}
	total, haveTotal := int64(0), false
	if v := a.copy(el, "AXNumberOfCharacters"); v != 0 {
		total, haveTotal = macos.Int64(v)
		macos.CFRelease(v)
	}
	if haveRange && haveTotal && total > surroundingMax && !a.overBudget() {
		// Ask only for the window we need: AXValue of a huge document can take very long.
		start := max(rng.Location-surroundingMax*2/3, 0)
		end := max(min(start+surroundingMax, int(total)), start)
		want := macos.Range{Location: start, Length: end - start}
		if param := axValueCreate(axValueCFRange, unsafe.Pointer(&want)); param != 0 {
			key := macos.CFStr("AXStringForRange")
			var out uintptr
			err := axCopyParamAttr(el, key, param, &out)
			macos.CFRelease(key)
			macos.CFRelease(param)
			if err == 0 && out != 0 {
				s, _ := macos.GoString(out)
				macos.CFRelease(out)
				if s != "" {
					return s
				}
			}
		}
	}
	value, _ := a.str(el, "AXValue")
	if value == "" {
		return ""
	}
	caret := int(^uint(0) >> 1)
	if haveRange {
		caret = max(rng.Location+rng.Length, 0)
	}
	return WindowAround(value, caret, surroundingMax)
}

// FocusedTextValue reads the focused text field's value (auto-dictionary learning).
func FocusedTextValue() (string, bool) {
	if !axIsProcessTrusted() {
		return "", false
	}
	fr := frontmost()
	if !fr.ok {
		return "", false
	}
	a := &ax{start: time.Now()}
	f, ok := resolveFocus(a, fr.pid)
	if !ok {
		return "", false
	}
	defer f.release()
	el := f.element
	if el == 0 {
		return "", false
	}
	if s, _ := a.str(el, "AXSubrole"); s == "AXSecureTextField" {
		return "", false
	}
	role, _ := a.str(el, "AXRole")
	if !slices.Contains(textRoles, role) && !a.settable(el, "AXValue") {
		return "", false
	}
	v, ok := a.str(el, "AXValue")
	if !ok {
		return "", false
	}
	if len(v) > focusedValueMax {
		cut := focusedValueMax
		for cut > 0 && (v[cut]&0xC0) == 0x80 {
			cut--
		}
		v = v[:cut]
	}
	return v, true
}

// copySelectionViaCmdC: snapshot the pasteboard, Cmd+C, read, restore. "" if the app copied nothing.
func copySelectionViaCmdC() string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var snap snapshot
	var before int
	macos.Pool(func() {
		pb := pasteboard()
		snap = snapshotPasteboard(pb)
		before = macos.SendInt(pb, "changeCount")
	})
	if err := postKeyPress(keycodeForChar('c'), flagCommand); err != nil {
		log.Printf("Cmd+C fallback failed: %v", err)
		return ""
	}
	deadline := time.Now().Add(180 * time.Millisecond)
	changed := false
	for time.Now().Before(deadline) {
		time.Sleep(8 * time.Millisecond)
		n := 0
		macos.Pool(func() { n = macos.SendInt(pasteboard(), "changeCount") })
		if n != before {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}
	time.Sleep(6 * time.Millisecond)
	var text string
	macos.Pool(func() {
		pb := pasteboard()
		text = macos.GoNS(macos.Send(pb, "stringForType:", macos.NSString(typeString)))
		restoreSnapshot(pb, snap)
	})
	return text
}
