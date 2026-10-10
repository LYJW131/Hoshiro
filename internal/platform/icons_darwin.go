//go:build darwin

package platform

import (
	"github.com/IcyBlue17/hoshiro/internal/macos"
)

// AppIconPNG renders an installed app's icon as a px-square PNG; nil if not installed.
func AppIconPNG(bundle string, px int) []byte {
	var out []byte
	macos.Pool(func() {
		ws := macos.Send(macos.Class("NSWorkspace"), "sharedWorkspace")
		url := macos.Send(ws, "URLForApplicationWithBundleIdentifier:", macos.NSString(bundle))
		if url == 0 {
			return
		}
		path := macos.Send(url, "path")
		if path == 0 {
			return
		}
		img := macos.Send(ws, "iconForFile:", path)
		rect := macos.Rect{Size: macos.Size{W: float64(px), H: float64(px)}}
		cg := macos.Send(img, "CGImageForProposedRect:context:hints:", &rect, uintptr(0), uintptr(0))
		if cg == 0 {
			return
		}
		rep := macos.Send(macos.Send(macos.Class("NSBitmapImageRep"), "alloc"), "initWithCGImage:", cg)
		defer macos.Send(rep, "release")
		// NSBitmapImageFileTypePNG = 4
		data := macos.Send(rep, "representationUsingType:properties:", uint(4), macos.Send(macos.Class("NSDictionary"), "dictionary"))
		out = macos.DataBytes(uintptr(data))
	})
	return out
}
