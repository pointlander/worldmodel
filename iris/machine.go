// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Search finds one Turing machine that writes an iris file.
//
// The numbering is a byte program for a single-tape machine. emit, lit, rep,
// and copy are ordinary tape operations; rec and rows print one decimal
// record, and rows packs a whole run as one mixed-radix integer of the
// offsets from the run's own column minima. Shortest-program search picks
// the minimum of that tape program and the row-run program.
//
// The file is split into lines. Each line gets its shortest program. Adjacent
// programs are paired, the pair is searched again (the two programs written
// end to end are one candidate), and pairing repeats until a single program
// remains. Run is the universal machine applied to that program.

package iris

import "bytes"

const (
	opEmit = 1 // 01 bb
	opLit  = 2 // 02 nn bb...          nn >= 2
	opRep  = 3 // 03 bb nn             nn >= 2
	opCopy = 4 // 04 dist u16 nn       nn >= 3, dist >= 1
	opRec  = 5 // 05 nn name v0 v1 v2 v3
	opRows = 6 // 06 nn name cols count u16 (min span)*cols packed
)

// Level is one round of pairing. Bytes is the sum of the program lengths.
type Level struct {
	Machines int
	Bytes    int
}

// Machine is the program Search found, plus the size of each pairing round.
type Machine struct {
	Prog   []byte
	Levels []Level
}

// Run applies the universal machine to m.Prog.
func (m Machine) Run() ([]byte, error) {
	return run(m.Prog)
}

// Search splits data into lines and pairs shortest programs until one remains.
func Search(data []byte) (Machine, error) {
	blocks := splitBlocks(data)
	progs := make([][]byte, len(blocks))
	for i, block := range blocks {
		progs[i] = smallest(block)
		if err := matches(progs[i], block); err != nil {
			return Machine{}, err
		}
	}
	levels := []Level{levelOf(progs)}
	for len(progs) > 1 {
		n := (len(progs) + 1) / 2
		next := make([][]byte, 0, n)
		nextData := make([][]byte, 0, n)
		for i := 0; i < len(progs); i += 2 {
			if i+1 == len(progs) {
				next = append(next, progs[i])
				nextData = append(nextData, blocks[i])
				continue
			}
			cat := append(append([]byte{}, blocks[i]...), blocks[i+1]...)
			prog := combine(progs[i], progs[i+1], cat)
			if err := matches(prog, cat); err != nil {
				return Machine{}, err
			}
			next = append(next, prog)
			nextData = append(nextData, cat)
		}
		progs = next
		blocks = nextData
		levels = append(levels, levelOf(progs))
	}
	if len(progs) == 0 {
		return Machine{Levels: levels}, nil
	}
	return Machine{Prog: progs[0], Levels: levels}, nil
}

func levelOf(progs [][]byte) Level {
	n := 0
	for _, p := range progs {
		n += len(p)
	}
	return Level{Machines: len(progs), Bytes: n}
}

func combine(left, right, data []byte) []byte {
	best := make([]byte, 0, len(left)+len(right))
	best = append(best, left...)
	best = append(best, right...)
	if s := smallest(data); len(s) < len(best) || (len(s) == len(best) && bytes.Compare(s, best) < 0) {
		return s
	}
	return best
}

func matches(prog, data []byte) error {
	got, err := run(prog)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return ErrFormat
	}
	return nil
}

func smallest(data []byte) []byte {
	tape := encodeTape(data)
	runs := encodeRuns(data)
	if len(runs) < len(tape) || (len(runs) == len(tape) && bytes.Compare(runs, tape) < 0) {
		return runs
	}
	return tape
}

func splitBlocks(data []byte) [][]byte {
	var blocks [][]byte
	for i := 0; i < len(data); {
		if _, _, n, ok := parseRow(data[i:]); ok {
			blocks = append(blocks, data[i:i+n])
			i += n
			continue
		}
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		if j < len(data) {
			j++
		}
		if j == i {
			j++
		}
		blocks = append(blocks, data[i:j])
		i = j
	}
	return blocks
}

// parseRow accepts "d.d,d.d,d.d,d.d,<name>\n". n is the byte length.
func parseRow(data []byte) (name string, vals [4]int, n int, ok bool) {
	p := 0
	for c := range vals {
		if p+4 > len(data) {
			return "", vals, 0, false
		}
		a, dot, b, comma := data[p], data[p+1], data[p+2], data[p+3]
		if a < '0' || a > '9' || dot != '.' || b < '0' || b > '9' || comma != ',' {
			return "", vals, 0, false
		}
		vals[c] = int(a-'0')*10 + int(b-'0')
		p += 4
	}
	start := p
	for p < len(data) && data[p] != '\n' {
		if data[p] == ',' || p-start > 255 {
			return "", vals, 0, false
		}
		p++
	}
	if p >= len(data) || p == start {
		return "", vals, 0, false
	}
	return string(data[start:p]), vals, p + 1, true
}

func encodeRuns(data []byte) []byte {
	var prog []byte
	for i := 0; i < len(data); {
		name, vals, n, ok := parseRow(data[i:])
		if !ok {
			j := i + 1
			for j < len(data) {
				if _, _, _, row := parseRow(data[j:]); row {
					break
				}
				j++
			}
			prog = append(prog, encodeTape(data[i:j])...)
			i = j
			continue
		}
		rows := [][4]int{vals}
		i += n
		for i < len(data) {
			next, v, w, ok := parseRow(data[i:])
			if !ok || next != name {
				break
			}
			rows = append(rows, v)
			i += w
		}
		prog = append(prog, encodeRun(name, rows)...)
	}
	return prog
}

func encodeRun(name string, rows [][4]int) []byte {
	recs := encodeRecs(name, rows)
	packed := encodeRows(name, rows)
	if len(packed) == 0 || len(recs) < len(packed) {
		return recs
	}
	return packed
}

func encodeRecs(name string, rows [][4]int) []byte {
	var prog []byte
	nb := []byte(name)
	for _, row := range rows {
		prog = append(prog, opRec, byte(len(nb)))
		prog = append(prog, nb...)
		for _, v := range row {
			prog = append(prog, byte(v))
		}
	}
	return prog
}

func encodeRows(name string, rows [][4]int) []byte {
	if len(rows) == 0 || len(rows) > 65535 || len(name) == 0 || len(name) > 255 {
		return nil
	}
	const cols = 4
	mins := [cols]int{}
	spans := [cols]int{}
	for c := range cols {
		lo, hi := rows[0][c], rows[0][c]
		for _, row := range rows[1:] {
			if row[c] < lo {
				lo = row[c]
			}
			if row[c] > hi {
				hi = row[c]
			}
		}
		mins[c] = lo
		spans[c] = hi - lo + 1
	}
	base, ok := spansBase(spans[:])
	if !ok {
		return nil
	}
	width := (bitsNeeded(base, len(rows)) + 7) / 8
	packed := make([]byte, width)
	for _, row := range rows {
		d := 0
		for c := range cols {
			d = d*spans[c] + (row[c] - mins[c])
		}
		if err := mulAddWide(packed, base, d); err != nil {
			return nil
		}
	}
	prog := []byte{opRows, byte(len(name))}
	prog = append(prog, name...)
	prog = append(prog, cols, byte(len(rows)), byte(len(rows)>>8))
	for c := range cols {
		prog = append(prog, byte(mins[c]), byte(spans[c]))
	}
	return append(prog, packed...)
}

func spansBase(spans []int) (int, bool) {
	base := 1
	for _, sp := range spans {
		if sp < 1 || base > maxInt/sp {
			return 0, false
		}
		base *= sp
	}
	return base, true
}

const maxInt = int(^uint(0) >> 1)

type link struct {
	cost int
	prev int
	op   byte
	a, b int
}

func encodeTape(data []byte) []byte {
	n := len(data)
	if n == 0 {
		return nil
	}
	const inf = int(^uint(0) >> 2)
	links := make([]link, n+1)
	for i := 1; i <= n; i++ {
		links[i].cost = inf
	}
	occ := make([][]int, 256)
	for i := 0; i < n; i++ {
		relax(links, i+1, links[i].cost+2, i, opEmit, int(data[i]), 0)
		maxL := n - i
		if maxL > 255 {
			maxL = 255
		}
		for l := 2; l <= maxL; l++ {
			relax(links, i+l, links[i].cost+2+l, i, opLit, l, 0)
		}
		if i+1 < n && data[i] == data[i+1] {
			l := 2
			for i+l < n && l < 255 && data[i+l] == data[i] {
				l++
			}
			for c := 2; c <= l; c++ {
				relax(links, i+c, links[i].cost+3, i, opRep, c, int(data[i]))
			}
		}
		bestL, bestD := 0, 0
		for _, j := range occ[data[i]] {
			maxM := i - j
			if remain := n - i; remain < maxM {
				maxM = remain
			}
			if maxM > 255 {
				maxM = 255
			}
			if maxM <= bestL {
				continue
			}
			l := 1
			for l < maxM && data[j+l] == data[i+l] {
				l++
			}
			if l > bestL {
				bestL, bestD = l, i-j
			}
		}
		for l := 3; l <= bestL; l++ {
			relax(links, i+l, links[i].cost+4, i, opCopy, bestD, l)
		}
		occ[data[i]] = append(occ[data[i]], i)
	}
	chunks := make([][]byte, 0, n)
	for pos := n; pos > 0; {
		l := links[pos]
		if l.prev < 0 || l.prev >= pos || l.cost == inf {
			return encodeLit(data)
		}
		switch l.op {
		case opEmit:
			chunks = append(chunks, []byte{opEmit, byte(l.a)})
		case opLit:
			seg := make([]byte, 0, 2+l.a)
			seg = append(seg, opLit, byte(l.a))
			seg = append(seg, data[l.prev:pos]...)
			chunks = append(chunks, seg)
		case opRep:
			chunks = append(chunks, []byte{opRep, byte(l.b), byte(l.a)})
		case opCopy:
			chunks = append(chunks, []byte{opCopy, byte(l.a), byte(l.a >> 8), byte(l.b)})
		default:
			return encodeLit(data)
		}
		pos = l.prev
	}
	prog := make([]byte, 0, links[n].cost)
	for i := len(chunks) - 1; i >= 0; i-- {
		prog = append(prog, chunks[i]...)
	}
	return prog
}

func relax(links []link, pos, cost, prev int, op byte, a, b int) {
	if cost < links[pos].cost {
		links[pos] = link{cost: cost, prev: prev, op: op, a: a, b: b}
	}
}

func encodeLit(data []byte) []byte {
	var prog []byte
	for len(data) > 0 {
		n := len(data)
		if n > 255 {
			n = 255
		}
		if n == 1 {
			prog = append(prog, opEmit, data[0])
			data = data[1:]
			continue
		}
		prog = append(prog, opLit, byte(n))
		prog = append(prog, data[:n]...)
		data = data[n:]
	}
	return prog
}

func Run(prog []byte) ([]byte, error) {
	return run(prog)
}

func run(prog []byte) ([]byte, error) {
	var out []byte
	i := 0
	for i < len(prog) {
		switch prog[i] {
		case opEmit:
			if i+1 >= len(prog) {
				return nil, ErrFormat
			}
			out = append(out, prog[i+1])
			i += 2
		case opLit:
			if i+1 >= len(prog) {
				return nil, ErrFormat
			}
			n := int(prog[i+1])
			i += 2
			if n < 2 || i+n > len(prog) {
				return nil, ErrFormat
			}
			out = append(out, prog[i:i+n]...)
			i += n
		case opRep:
			if i+2 >= len(prog) {
				return nil, ErrFormat
			}
			b := prog[i+1]
			n := int(prog[i+2])
			if n < 2 {
				return nil, ErrFormat
			}
			for range n {
				out = append(out, b)
			}
			i += 3
		case opCopy:
			if i+3 >= len(prog) {
				return nil, ErrFormat
			}
			dist := int(prog[i+1]) | int(prog[i+2])<<8
			n := int(prog[i+3])
			if dist < 1 || n < 3 || dist > len(out) {
				return nil, ErrFormat
			}
			for range n {
				out = append(out, out[len(out)-dist])
			}
			i += 4
		case opRec:
			name, vals, ni, err := readRec(prog, i)
			if err != nil {
				return nil, err
			}
			if err := renderRow(name, vals[:], &out); err != nil {
				return nil, err
			}
			i = ni
		case opRows:
			ni, err := execRows(prog, i, &out)
			if err != nil {
				return nil, err
			}
			i = ni
		default:
			return nil, ErrFormat
		}
	}
	return out, nil
}

func readRec(prog []byte, i int) (string, [4]int, int, error) {
	var vals [4]int
	if i+1 >= len(prog) {
		return "", vals, 0, ErrFormat
	}
	n := int(prog[i+1])
	i += 2
	if n < 1 || i+n+4 > len(prog) {
		return "", vals, 0, ErrFormat
	}
	name := string(prog[i : i+n])
	i += n
	for c := range vals {
		vals[c] = int(prog[i+c])
		if vals[c] > 99 {
			return "", vals, 0, ErrFormat
		}
	}
	return name, vals, i + 4, nil
}

func execRows(prog []byte, i int, out *[]byte) (int, error) {
	if i+1 >= len(prog) {
		return 0, ErrFormat
	}
	nameLen := int(prog[i+1])
	i += 2
	if nameLen < 1 || i+nameLen+1+2 > len(prog) {
		return 0, ErrFormat
	}
	name := string(prog[i : i+nameLen])
	i += nameLen
	cols := int(prog[i])
	count := int(prog[i+1]) | int(prog[i+2])<<8
	i += 3
	if cols < 1 || cols > 8 || count < 1 || i+2*cols > len(prog) {
		return 0, ErrFormat
	}
	mins := make([]int, cols)
	spans := make([]int, cols)
	for c := range cols {
		mins[c] = int(prog[i])
		spans[c] = int(prog[i+1])
		i += 2
	}
	base, ok := spansBase(spans)
	if !ok {
		return 0, ErrFormat
	}
	width := (bitsNeeded(base, count) + 7) / 8
	if i+width > len(prog) {
		return 0, ErrFormat
	}
	packed := make([]byte, width)
	copy(packed, prog[i:i+width])
	i += width
	rows := make([][]int, count)
	for r := count - 1; r >= 0; r-- {
		d := divModWide(packed, base)
		row := make([]int, cols)
		for c := cols - 1; c >= 0; c-- {
			row[c] = mins[c] + d%spans[c]
			d /= spans[c]
		}
		if d != 0 {
			return 0, ErrFormat
		}
		for _, v := range row {
			if v > 99 {
				return 0, ErrFormat
			}
		}
		rows[r] = row
	}
	if !zero(packed) {
		return 0, ErrFormat
	}
	for _, row := range rows {
		if err := renderRow(name, row, out); err != nil {
			return 0, err
		}
	}
	return i, nil
}

func renderRow(name string, vals []int, dst *[]byte) error {
	for c, v := range vals {
		if v < 0 || v > 99 {
			return ErrFormat
		}
		if c > 0 {
			*dst = append(*dst, ',')
		}
		*dst = append(*dst, byte('0'+v/10), '.', byte('0'+v%10))
	}
	*dst = append(*dst, ',')
	*dst = append(*dst, name...)
	*dst = append(*dst, '\n')
	return nil
}

func mulAddWide(n []byte, base, add int) error {
	carry := int64(add)
	b := int64(base)
	for i := len(n) - 1; i >= 0; i-- {
		v := int64(n[i])*b + carry
		n[i] = byte(v)
		carry = v >> 8
	}
	if carry != 0 {
		return ErrFormat
	}
	return nil
}

func divModWide(n []byte, base int) int {
	rem := int64(0)
	b := int64(base)
	for i := range n {
		v := rem<<8 | int64(n[i])
		n[i] = byte(v / b)
		rem = v % b
	}
	return int(rem)
}

func bitsNeeded(base, exp int) int {
	n := []byte{1}
	for range exp {
		n = mulGrow(n, base)
	}
	bits := bitLen(n)
	if isPow2(n) {
		bits--
	}
	return bits
}

func mulGrow(n []byte, m int) []byte {
	carry := int64(0)
	mm := int64(m)
	for i := len(n) - 1; i >= 0; i-- {
		v := int64(n[i])*mm + carry
		n[i] = byte(v)
		carry = v >> 8
	}
	for carry > 0 {
		n = append([]byte{byte(carry)}, n...)
		carry >>= 8
	}
	return n
}

func bitLen(n []byte) int {
	i := 0
	for i < len(n) && n[i] == 0 {
		i++
	}
	if i == len(n) {
		return 0
	}
	bits := (len(n) - i - 1) * 8
	for v := n[i]; v > 0; v >>= 1 {
		bits++
	}
	return bits
}

func isPow2(n []byte) bool {
	seen := false
	for _, b := range n {
		if b == 0 {
			continue
		}
		if seen || b&(b-1) != 0 {
			return false
		}
		seen = true
	}
	return seen
}

// progText describes a program for tests and logs.
func progText(prog []byte) (string, error) {
	var b []byte
	i := 0
	for i < len(prog) {
		if len(b) > 0 {
			b = append(b, '\n')
		}
		switch prog[i] {
		case opEmit:
			if i+1 >= len(prog) {
				return "", ErrFormat
			}
			b = append(b, "emit "...)
			b = append(b, hexByte(prog[i+1])...)
			i += 2
		case opLit:
			if i+1 >= len(prog) {
				return "", ErrFormat
			}
			n := int(prog[i+1])
			i += 2
			if n < 2 || i+n > len(prog) {
				return "", ErrFormat
			}
			b = append(b, "lit "...)
			b = append(b, itoa(n)...)
			i += n
		case opRep:
			if i+2 >= len(prog) {
				return "", ErrFormat
			}
			b = append(b, "rep "...)
			b = append(b, itoa(int(prog[i+2]))...)
			i += 3
		case opCopy:
			if i+3 >= len(prog) {
				return "", ErrFormat
			}
			dist := int(prog[i+1]) | int(prog[i+2])<<8
			n := int(prog[i+3])
			b = append(b, "copy "...)
			b = append(b, itoa(dist)...)
			b = append(b, ' ')
			b = append(b, itoa(n)...)
			i += 4
		case opRec:
			_, _, ni, err := readRec(prog, i)
			if err != nil {
				return "", err
			}
			b = append(b, "rec "...)
			b = append(b, itoa(ni-i)...)
			i = ni
		case opRows:
			if i+1 >= len(prog) {
				return "", ErrFormat
			}
			nameLen := int(prog[i+1])
			if nameLen < 1 || i+2+nameLen+3 > len(prog) {
				return "", ErrFormat
			}
			name := string(prog[i+2 : i+2+nameLen])
			j := i + 2 + nameLen
			cols := int(prog[j])
			count := int(prog[j+1]) | int(prog[j+2])<<8
			j += 3
			if cols < 1 || j+2*cols > len(prog) {
				return "", ErrFormat
			}
			spans := make([]int, cols)
			for c := range cols {
				spans[c] = int(prog[j+2*c+1])
			}
			j += 2 * cols
			base, ok := spansBase(spans)
			if !ok {
				return "", ErrFormat
			}
			width := (bitsNeeded(base, count) + 7) / 8
			if j+width > len(prog) {
				return "", ErrFormat
			}
			b = append(b, "rows "...)
			b = append(b, name...)
			b = append(b, " x"...)
			b = append(b, itoa(count)...)
			b = append(b, " packed "...)
			b = append(b, itoa(width)...)
			i = j + width
		default:
			return "", ErrFormat
		}
	}
	return string(b), nil
}

func hexByte(v byte) string {
	const d = "0123456789abcdef"
	return string([]byte{d[v>>4], d[v&0x0f]})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
