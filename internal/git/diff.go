package git

// LineMark describes how a line of the current text differs from the base.
type LineMark uint8

const (
	Added        LineMark = 1 << iota
	Modified              // the line replaces one or more base lines
	DeletedAbove          // base lines were removed just above this line
	DeletedBelow          // base lines were removed after the last line
)

type op uint8

const (
	opEq op = iota
	opDel
	opIns
)

// maxEdits bounds the diff work; larger differences mark the whole middle as modified.
const maxEdits = 2000

// Hunk is a block of changed lines: cur[Start:End] replaces base[BaseStart:BaseEnd].
// Start == End is a pure deletion before line Start, BaseStart == BaseEnd an addition.
type Hunk struct {
	Start, End         int
	BaseStart, BaseEnd int
}

// Hunks returns the changed blocks between base and cur in order.
func Hunks(base, cur []string) []Hunk {
	p := 0
	for p < len(base) && p < len(cur) && base[p] == cur[p] {
		p++
	}
	s := 0
	for s < len(base)-p && s < len(cur)-p && base[len(base)-1-s] == cur[len(cur)-1-s] {
		s++
	}
	a, b := base[p:len(base)-s], cur[p:len(cur)-s]
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	ops := myers(a, b)
	if ops == nil {
		return []Hunk{{p, len(cur) - s, p, len(base) - s}}
	}
	var hunks []Hunk
	ai, bi := p, p
	for k := 0; k < len(ops); {
		if ops[k] == opEq {
			ai++
			bi++
			k++
			continue
		}
		h := Hunk{Start: bi, BaseStart: ai}
		for k < len(ops) && ops[k] != opEq {
			if ops[k] == opDel {
				ai++
			} else {
				bi++
			}
			k++
		}
		h.End, h.BaseEnd = bi, ai
		hunks = append(hunks, h)
	}
	return hunks
}

// Diff compares base with cur and returns a mark for every line of cur.
func Diff(base, cur []string) []LineMark {
	return Marks(Hunks(base, cur), len(cur))
}

// Marks converts hunks to a mark for each of the n lines of cur.
func Marks(hunks []Hunk, n int) []LineMark {
	marks := make([]LineMark, n)
	for _, h := range hunks {
		switch {
		case h.Start == h.End: // pure deletion
			if h.Start < n {
				marks[h.Start] |= DeletedAbove
			} else if n > 0 {
				marks[n-1] |= DeletedBelow
			}
		case h.BaseStart == h.BaseEnd:
			for j := h.Start; j < h.End; j++ {
				marks[j] |= Added
			}
		default:
			for j := h.Start; j < h.End; j++ {
				marks[j] |= Modified
			}
		}
	}
	return marks
}

// HunkAt returns the index of the hunk shown at line of cur, or -1.
func HunkAt(hunks []Hunk, line, n int) int {
	for i, h := range hunks {
		if line >= h.Start && line < h.End || h.Start == h.End && (line == h.Start || h.Start >= n && line == n-1) {
			return i
		}
	}
	return -1
}

// myers returns the shortest edit script from a to b, or nil when it would
// need more than maxEdits operations.
func myers(a, b []string) []op {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return []op{}
	}
	off := n + m + 1
	v := make([]int, 2*off+1)
	// trace[d] holds v[-d-1 .. d+1] as it was before round d.
	var trace [][]int
	for d := 0; d <= min(n+m, maxEdits); d++ {
		trace = append(trace, append([]int(nil), v[off-d-1:off+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m)
			}
		}
	}
	return nil
}

func backtrack(trace [][]int, n, m int) []op {
	var ops []op
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		at := func(k int) int { return v[k+d+1] }
		k := x - y
		var prevK int
		if k == -d || k != d && at(k-1) < at(k+1) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append(ops, opEq)
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				ops = append(ops, opIns)
			} else {
				ops = append(ops, opDel)
			}
		}
		x, y = prevX, prevY
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}
