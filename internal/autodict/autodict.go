// Package autodict detects a single corrected token between the text Hoshiro
// inserted and what the field contains a few seconds later.
package autodict

import (
	"slices"
	"strings"
	"unicode"
)

func isCJK(c rune) bool {
	return (c >= 0x3400 && c <= 0x4DBF) || (c >= 0x4E00 && c <= 0x9FFF) || (c >= 0xF900 && c <= 0xFAFF) ||
		(c >= 0x3040 && c <= 0x30FF) || (c >= 0xAC00 && c <= 0xD7AF)
}

func isLatinWord(c rune) bool { return (unicode.IsLetter(c) || unicode.IsNumber(c)) && !isCJK(c) }

var common = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the be to of and a in that have i it for not on with he as you
do at this but his by from they we say her she or an will my one all would there their what so up out if about who get which go me
when make can like time no just him know take people into year your good some could them see other than then now look only come its over
think also back after use two how our work first well way even new want because any these give day most us is are was were been has had
did does am very too here yes ok okay hi hello thanks please should need let put set run cat dog red big end off own may why where`) {
		common[w] = true
	}
}

// EditDistance is the Levenshtein distance between two rune slices.
func EditDistance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ca := range a {
		cur := []int{i + 1}
		for j, cb := range b {
			cost := 1
			if ca == cb {
				cost = 0
			}
			cur = append(cur, min(prev[j]+cost, prev[j+1]+1, cur[j]+1))
		}
		prev = cur
	}
	return prev[len(b)]
}

func findSub(hay, needle []rune, from int) int {
	if len(needle) == 0 || len(hay) < len(needle) {
		return -1
	}
	for i := from; i <= len(hay)-len(needle); i++ {
		if slices.Equal(hay[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// EditedRegion locates only an unambiguous anchored region; it never learns surrounding field content.
func EditedRegion(inserted, field string) (string, bool) {
	ins := []rune(strings.TrimSpace(inserted))
	fld := []rune(field)
	if len(ins) < 6 {
		return "", false
	}
	k := min(max(len(ins)/3, 2), 8)
	start := findSub(fld, ins[:k], 0)
	if start < 0 || findSub(fld, ins[:k], start+1) >= 0 {
		return "", false
	}
	tail := ins[len(ins)-k:]
	end := findSub(fld, tail, start+k)
	if end < 0 || findSub(fld, tail, end+1) >= 0 {
		return "", false
	}
	region := fld[start : end+k]
	if len(region) > len(ins)*2+100 {
		return "", false
	}
	return string(region), true
}

// DetectCorrection returns the new token when field contains inserted with
// exactly one token replaced by a similar, different, uncommon token.
func DetectCorrection(inserted, field string) (string, bool) {
	ins := []rune(strings.TrimSpace(inserted))
	fld := []rune(field)
	if len(ins) < 6 || len(fld) < 6 {
		return "", false
	}
	k := min(max(len(ins)/3, 2), 5)
	start := findSub(fld, ins[:k], 0)
	if start < 0 {
		return "", false
	}
	e := findSub(fld, ins[len(ins)-k:], start)
	if e < 0 {
		return "", false
	}
	end := e + k
	if end <= start {
		return "", false
	}
	region := fld[start:end]
	d := len(region) - len(ins)
	if d > 24 || d < -24 || slices.Equal(region, ins) {
		return "", false
	}
	p := 0
	for p < len(ins) && p < len(region) && ins[p] == region[p] {
		p++
	}
	insRest, regRest := ins[p:], region[p:]
	s := 0
	for s < len(insRest) && s < len(regRest) && insRest[len(insRest)-1-s] == regRest[len(regRest)-1-s] {
		s++
	}
	oldMid, newMid := insRest[:len(insRest)-s], regRest[:len(regRest)-s]
	if len(oldMid) == 0 && len(newMid) == 0 {
		return "", false
	}
	latin := true
	for _, c := range append(slices.Clone(newMid), oldMid...) {
		if !isLatinWord(c) {
			latin = false
			break
		}
	}
	a, bOld, bNew := p, len(ins)-s, len(region)-s
	if latin {
		for a > 0 && isLatinWord(ins[a-1]) {
			a--
		}
		for bOld < len(ins) && isLatinWord(ins[bOld]) {
			bOld++
		}
		for bNew < len(region) && isLatinWord(region[bNew]) {
			bNew++
		}
	} else {
		if len(oldMid) == 0 || len(newMid) == 0 {
			return "", false
		}
		allCJK := true
		for _, c := range newMid {
			allCJK = allCJK && isCJK(c)
		}
		if allCJK && len(newMid) < 2 && a > 0 && isCJK(ins[a-1]) {
			a--
		}
	}
	oldTok, newTok := ins[a:bOld], region[a:bNew]
	if slices.Equal(oldTok, newTok) || len(newTok) < 2 || len(newTok) > 32 || len(oldTok) == 0 {
		return "", false
	}
	alpha := false
	for _, c := range append(slices.Clone(newTok), oldTok...) {
		if unicode.IsSpace(c) {
			return "", false
		}
	}
	for _, c := range newTok {
		alpha = alpha || unicode.IsLetter(c)
	}
	if !alpha || EditDistance(oldTok, newTok) > 3 {
		return "", false
	}
	tok := string(newTok)
	if common[strings.ToLower(tok)] {
		return "", false
	}
	return tok, true
}
