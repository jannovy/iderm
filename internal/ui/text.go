package ui

import (
	"unicode"

	"github.com/mattn/go-runewidth"
)

const tabWidth = 4

// cellWidth returns how many screen cells rune r occupies at visual column v.
func cellWidth(r rune, v int) int {
	if r == '\t' {
		return tabWidth - v%tabWidth
	}
	if r < 0x20 || r == 0x7f {
		return 1
	}
	return max(runewidth.RuneWidth(r), 1)
}

// visualCol converts a rune index to a screen column.
func visualCol(line []rune, col int) int {
	v := 0
	for i := 0; i < col && i < len(line); i++ {
		v += cellWidth(line[i], v)
	}
	return v
}

// colForVisual converts a screen column to the rune index under it.
func colForVisual(line []rune, target int) int {
	v := 0
	for i, r := range line {
		w := cellWidth(r, v)
		if v+w > target {
			return i
		}
		v += w
	}
	return len(line)
}

// charClass groups runes for word-wise movement.
func charClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	}
	return 2
}

func leadingWhitespace(line []rune) []rune {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

func isBlank(rs []rune) bool {
	return len(leadingWhitespace(rs)) == len(rs)
}
