//go:build darwin

package platform

import (
	"log"
	"os/exec"
	"sync/atomic"

	"github.com/IcyBlue17/hoshiro/internal/macos"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/ebitengine/purego/objc"
)

// Accessibility and Input Monitoring have no "not determined" API: remember whether we asked.
var askedAX, askedIM atomic.Bool

func microphone() model.PermissionState {
	cls := macos.Class("AVCaptureDevice")
	if cls == 0 {
		return model.PermNotDetermined
	}
	switch macos.SendInt(cls, "authorizationStatusForMediaType:", macos.NSString("soun")) {
	case 3:
		return model.PermGranted
	case 1, 2:
		return model.PermDenied
	}
	return model.PermNotDetermined
}

func CheckPermissions() model.Permissions {
	p := model.Permissions{Microphone: microphone(), Accessibility: model.PermNotDetermined, InputMonitoring: model.PermNotDetermined}
	switch {
	case axIsProcessTrusted():
		p.Accessibility = model.PermGranted
	case askedAX.Load():
		p.Accessibility = model.PermDenied
	}
	switch {
	case cgPreflightListen():
		p.InputMonitoring = model.PermGranted
	case askedIM.Load():
		p.InputMonitoring = model.PermDenied
	}
	return p
}

// RequestPermission triggers the system prompt. The microphone prompt is
// asynchronous: poll CheckPermissions afterwards.
func RequestPermission(kind model.PermissionKind) model.Permissions {
	switch kind {
	case model.PermMicrophone:
		if cls := macos.Class("AVCaptureDevice"); cls != 0 {
			block := objc.NewBlock(func(_ objc.Block, granted bool) {
				log.Printf("microphone access granted: %v", granted)
			})
			macos.Send(cls, "requestAccessForMediaType:completionHandler:", macos.NSString("soun"), block)
		}
	case model.PermAccessibility:
		askedAX.Store(true)
		opts := macos.Dict1(kAXTrustedCheckOptionPrompt, macos.KCFBooleanTrue)
		axIsProcessTrustedWithOptions(opts)
		macos.CFRelease(opts)
	case model.PermInputMonitoring:
		askedIM.Store(true)
		cgRequestListen()
	}
	return CheckPermissions()
}

func openURL(u string) {
	if err := exec.Command("/usr/bin/open", u).Start(); err != nil {
		log.Printf("open %s: %v", u, err)
	}
}

func OpenPermissionSettings(kind model.PermissionKind) { openURL(SettingsURL(kind)) }
func OpenKeyboardSettings()                            { openURL(KeyboardSettingsURL) }

func runningApps(bundle string) []macos.ID {
	arr := macos.Send(macos.Class("NSRunningApplication"), "runningApplicationsWithBundleIdentifier:", macos.NSString(bundle))
	n := macos.SendInt(arr, "count")
	out := make([]macos.ID, n)
	for i := range n {
		out[i] = macos.Send(arr, "objectAtIndex:", uint(i))
	}
	return out
}

// RunningPIDs lists the pids of apps with this bundle id (e.g. Typeless).
func RunningPIDs(bundle string) []int {
	var pids []int
	macos.Pool(func() {
		for _, a := range runningApps(bundle) {
			pids = append(pids, int(macos.SendInt32(a, "processIdentifier")))
		}
	})
	return pids
}

// TerminateApp politely asks apps with this bundle id to quit.
func TerminateApp(bundle string) bool {
	any := false
	macos.Pool(func() {
		for _, a := range runningApps(bundle) {
			any = macos.SendBool(a, "terminate") || any
		}
	})
	return any
}
