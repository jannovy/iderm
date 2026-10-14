package index

import (
	"bytes"
	"context"
	"os"
	"strings"
	"unicode/utf8"
)

// TextMatch is one line found by SearchText.
type TextMatch struct {
	File      string
	Line, Col int // rune column of the match
	Text      string
}

// SearchText finds lines containing q in project files outside vendor.
// Matching ignores case unless q contains an uppercase letter.
func (ix *Index) SearchText(ctx context.Context, q string, limit int) []TextMatch {
	if q == "" {
		return nil
	}
	fold := q == strings.ToLower(q)
	needle := []byte(q)
	var out []TextMatch
	for _, p := range ix.Paths() {
		if ctx.Err() != nil || len(out) >= limit {
			break
		}
		if ix.InVendor(p) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil || len(data) > 2<<20 || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			continue
		}
		hay := data
		if fold {
			hay = bytes.ToLower(data)
			if len(hay) != len(data) {
				continue // case folding changed byte offsets; skip exotic encodings
			}
		}
		if !bytes.Contains(hay, needle) {
			continue
		}
		line, start := 0, 0
		for start <= len(data) && len(out) < limit {
			end := bytes.IndexByte(data[start:], '\n')
			if end < 0 {
				end = len(data)
			} else {
				end += start
			}
			if i := bytes.Index(hay[start:end], needle); i >= 0 {
				text := strings.TrimRight(string(data[start:end]), "\r")
				out = append(out, TextMatch{File: p, Line: line, Col: utf8.RuneCount(data[start : start+i]), Text: text})
			}
			line++
			start = end + 1
		}
	}
	return out
}
