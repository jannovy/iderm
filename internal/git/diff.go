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

// Diff compares base with cur and returns a mark for every line of cur.
func Diff(base, cur []string) []LineMark {
	marks := make([]LineMark, len(cur))
	p := 0
	for p < len(base) && p < len(cur) && base[p] == cur[p] {
		p++
	}
	s := 0
	for s < len(base)-p && s < len(cur)-p && base[len(base)-1-s] == cur[len(cur)-1-s] {
		s++
	}
	a, b := base[p:len(base)-s], cur[p:len(cur)-s]
	ops := myers(a, b)
	if ops == nil {
		for j := p; j < len(cur)-s; j++ {
			marks[j] = Modified
		}
		return marks
	}
	bi := p
	for k := 0; k < len(ops); {
		if ops[k] == opEq {
			bi++
			k++
			continue
		}
		dels, start := 0, bi
		for k < len(ops) && ops[k] != opEq {
			if ops[k] == opDel {
				dels++
			} else {
				bi++
			}
			k++
		}
		switch {
		case bi == start: // pure deletion
			if start < len(cur) {
				marks[start] |= DeletedAbove
			} else if len(cur) > 0 {
				marks[len(cur)-1] |= DeletedBelow
			}
		case dels == 0:
			for j := start; j < bi; j++ {
				marks[j] |= Added
			}
		default:
			for j := start; j < bi; j++ {
				marks[j] |= Modified
			}
		}
	}
	return marks
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
