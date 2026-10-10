// Package icons holds the Lucide icons (ISC license) the windows use.
package icons

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

//go:embed svg/*.svg
var fs embed.FS

func load(name string) *ui.SVG {
	b, err := fs.ReadFile("svg/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(b)
}

var (
	AlertCircle   = load("circle-alert")
	AlertTriangle = load("triangle-alert")
	ArrowLeft     = load("arrow-left")
	ArrowRight    = load("arrow-right")
	BarChart      = load("chart-column")
	Bluetooth     = load("bluetooth")
	BookOpen      = load("book-open")
	Check         = load("check")
	CheckCircle   = load("circle-check")
	ChevronDown   = load("chevron-down")
	ChevronLeft   = load("chevron-left")
	ChevronRight  = load("chevron-right")
	Help          = load("circle-question-mark")
	Copy          = load("copy")
	ExternalLink  = load("external-link")
	Eye           = load("eye")
	EyeOff        = load("eye-off")
	FileText      = load("file-text")
	FileUp        = load("file-up")
	Globe         = load("globe")
	History       = load("history")
	House         = load("house")
	Info          = load("info")
	Laptop        = load("laptop")
	Loader        = load("loader-circle")
	Mic           = load("mic")
	Package       = load("package")
	PenLine       = load("pen-line")
	Pencil        = load("pencil")
	Plus          = load("plus")
	RotateCcw     = load("rotate-ccw")
	Search        = load("search")
	Settings      = load("settings")
	Sparkles      = load("sparkles")
	Trash         = load("trash-2")
	Usb           = load("usb")
	Wand          = load("wand-sparkles")
	X             = load("x")
	Download      = load("download")
	HardDrive     = load("hard-drive")
	Lock          = load("lock")
	More          = load("ellipsis")
)
