//go:build !darwin

package platform

import (
	"errors"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/hotkey"
	"github.com/IcyBlue17/hoshiro/internal/model"
)

// Hoshiro targets macOS. These stubs keep the app building (and its UI and
// logic testable) elsewhere.

var errUnsupported = errors.New("not supported on this system")

func CheckPermissions() model.Permissions {
	return model.Permissions{Microphone: model.PermGranted, Accessibility: model.PermGranted, InputMonitoring: model.PermGranted}
}
func RequestPermission(model.PermissionKind) model.Permissions { return CheckPermissions() }
func OpenPermissionSettings(model.PermissionKind)              {}
func OpenKeyboardSettings()                                    {}
func RunningPIDs(string) []int                                 { return nil }
func TerminateApp(string) bool                                 { return false }
func InsertText(string) error                                  { return errUnsupported }
func ReplaceSelection(string) error                            { return errUnsupported }
func CopyToClipboard(string) error                             { return errUnsupported }
func CaptureContext(bool, bool) model.AppContext               { return model.AppContext{} }
func FocusedTextValue() (string, bool)                         { return "", false }
func MuteSystemOutput() func()                                 { return nil }
func AppIconPNG(string, int) []byte                            { return nil }
func ListMics() []model.MicDevice                              { return nil }
func PlayWAV([]byte) error                                     { return errUnsupported }
func SetupBar(uintptr) error                                   { return nil }
func ShowBar()                                                 {}
func HideBar()                                                 {}
func SetBarHitRects([]HitRect)                                 {}

func OpenMic(string, func([]float32), func(string)) (uint32, audio.Stream, error) {
	return 0, nil, errUnsupported
}

type HotkeyTap struct{}

func StartHotkeyTap(*hotkey.Engine) (*HotkeyTap, error) { return nil, errUnsupported }
func (*HotkeyTap) Active() bool                         { return false }
func (*HotkeyTap) Close()                               {}
