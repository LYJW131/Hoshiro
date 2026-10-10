package bar

import (
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui"
)

// The answer card's markdown subset: paragraphs, headings (as bold lines),
// bullet/numbered lists, block quotes, fenced code, `code`, **bold**, *italic*
// and [links](https://…).

var (
	inlineRe = regexp.MustCompile("(`[^`\\n]+`)|(\\*\\*[^*\\n]+\\*\\*)|(__[^_\\n]+__)|(\\*[^*\\s][^*\\n]*\\*)|(\\[[^\\]\\n]+\\]\\(https?://[^)\\s]+\\))")
	linkRe   = regexp.MustCompile(`^\[([^\]]+)\]\(([^)]+)\)$`)
	bulletRe = regexp.MustCompile(`^\s*[-*•+]\s+(.*)$`)
	numRe    = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	headRe   = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*)$`)
	fenceRe  = regexp.MustCompile("^\\s*```")
	quoteRe  = regexp.MustCompile(`^\s*>\s?(.*)$`)
)

var (
	white   = ui.RGB(255, 255, 255)
	mdText  = ui.Hex("#e8e8ea")
	mdMuted = ui.Hex("#8e8e93")
	mdLink  = ui.Hex("#8eb4ff")
)

// Seg is one styled run of inline markdown.
type Seg struct {
	Text, Kind, URL string // Kind: text | code | bold | italic | link
}

// Inline splits text into styled runs.
func Inline(text string) []Seg {
	var out []Seg
	last := 0
	for _, m := range inlineRe.FindAllStringSubmatchIndex(text, -1) {
		if m[0] > last {
			out = append(out, Seg{Text: text[last:m[0]], Kind: "text"})
		}
		s := text[m[0]:m[1]]
		switch {
		case m[2] >= 0:
			out = append(out, Seg{Text: s[1 : len(s)-1], Kind: "code"})
		case m[4] >= 0 || m[6] >= 0:
			out = append(out, Seg{Text: s[2 : len(s)-2], Kind: "bold"})
		case m[8] >= 0:
			out = append(out, Seg{Text: s[1 : len(s)-1], Kind: "italic"})
		case m[10] >= 0:
			mm := linkRe.FindStringSubmatch(s)
			out = append(out, Seg{Text: mm[1], Kind: "link", URL: mm[2]})
		}
		last = m[1]
	}
	if last < len(text) {
		out = append(out, Seg{Text: text[last:], Kind: "text"})
	}
	return out
}

func inline(c *ui.Context, text string, base ui.Color) ui.Element {
	return ui.RichText(c).TextColor(base).Children(func() {
		for i, sg := range Inline(text) {
			k := c.Key(i)
			switch sg.Kind {
			case "code":
				ui.Text(k, sg.Text).Font("monospace").FontSize(12).Background(ui.RGBA(255, 255, 255, 0.1))
			case "bold":
				ui.Text(k, sg.Text).FontWeight(650).TextColor(white)
			case "italic":
				ui.Text(k, sg.Text).Italic()
			case "link":
				ui.Link(k, sg.Text, sg.URL).TextColor(mdLink)
			default:
				ui.Text(k, sg.Text)
			}
		}
	})
}

// Block is one rendered block of a markdown answer.
type Block struct {
	Kind  string // p | h | ul | ol | quote | code
	Text  string
	Items []string
	Start int
}

func special(l string) bool {
	return bulletRe.MatchString(l) || numRe.MatchString(l) || headRe.MatchString(l) || fenceRe.MatchString(l) || quoteRe.MatchString(l)
}

// ParseMarkdown splits text into blocks.
func ParseMarkdown(text string) []Block {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	var out []Block
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case fenceRe.MatchString(line):
			var code []string
			i++
			for i < len(lines) && !fenceRe.MatchString(lines[i]) {
				code = append(code, lines[i])
				i++
			}
			i++
			out = append(out, Block{Kind: "code", Text: strings.Join(code, "\n")})
		case headRe.MatchString(line):
			out = append(out, Block{Kind: "h", Text: headRe.FindStringSubmatch(line)[1]})
			i++
		case bulletRe.MatchString(line) || numRe.MatchString(line):
			ordered := numRe.MatchString(line) && !bulletRe.MatchString(line)
			b := Block{Kind: "ul", Start: 1}
			re := bulletRe
			if ordered {
				b.Kind, re = "ol", numRe
				b.Start = atoi(numRe.FindStringSubmatch(line)[1])
			}
			for i < len(lines) {
				m := re.FindStringSubmatch(lines[i])
				if m == nil {
					break
				}
				b.Items = append(b.Items, m[len(m)-1])
				i++
			}
			out = append(out, b)
		case quoteRe.MatchString(line):
			var q []string
			for i < len(lines) && quoteRe.MatchString(lines[i]) {
				q = append(q, quoteRe.FindStringSubmatch(lines[i])[1])
				i++
			}
			out = append(out, Block{Kind: "quote", Text: strings.Join(q, "\n")})
		default:
			var p []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !special(lines[i]) {
				p = append(p, lines[i])
				i++
			}
			out = append(out, Block{Kind: "p", Text: strings.Join(p, "\n")})
		}
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// Markdown renders text.
func Markdown(c *ui.Context, text string) {
	ui.Column(c).Gap(8).FontSize(13).LineHeight(1.55).TextColor(mdText).Children(func() {
		for i, b := range ParseMarkdown(text) {
			k := c.Key(i)
			switch b.Kind {
			case "h":
				inline(k, b.Text, white).FontWeight(700)
			case "p":
				inline(k, b.Text, mdText)
			case "code":
				ui.Text(k, b.Text).Font("monospace").FontSize(12).LineHeight(1.5).Padding(9, 11).Radius(9).
					Background(ui.RGBA(0, 0, 0, 0.4)).Border(1, ui.RGBA(255, 255, 255, 0.06)).FillWidth()
			case "quote":
				inline(k, b.Text, ui.Hex("#b4b4ba")).Padding(0, 0, 0, 10).BorderWidth(0, 0, 0, 2).BorderColor(ui.RGBA(255, 255, 255, 0.2))
			case "ul", "ol":
				ui.Column(k).Gap(2).Padding(0, 0, 0, 4).Children(func() {
					for j, it := range b.Items {
						marker := "•"
						if b.Kind == "ol" {
							marker = itoa(b.Start+j) + "."
						}
						ui.Row(c.Key(j)).Gap(6).AlignItems(ui.Start).Children(func() {
							ui.Text(c, marker).TextColor(mdMuted).MinWidth(14)
							inline(c, it, mdText).Grow(1).Shrink(1)
						})
					}
				})
			}
		}
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
