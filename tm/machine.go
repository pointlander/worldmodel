// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tm compresses arbitrary bytes as one Turing-machine program.
//
// Search applies a bijective Burrows-Wheeler transform and move-to-front,
// then solves one dynamic program for the fewest bits under the fixed field
// tables. Each tape position is reached by an emit, lit, rep, copy, or far.
//
// Instructions:
//
//	emit  01 bb
//	lit   02 nn bb...       nn >= 2, nn <= 255
//	rep   03 bb nn          nn >= 2, nn <= 255
//	copy  04 dist u16 nn    nn >= 3, dist >= 1
//	far   05 dist u32 nn    nn >= 3, dist >= 1
//
// copy and far append nn bytes from dist back on the tape already written.
// The search prices each of the 256 most recent earlier copies of the first
// three bytes and keeps a copy when no other is both longer and cheaper.
// rep writes one byte nn times. lit and emit write raw bytes.
//
// Compress writes that program with the fixed field tables in table.go.
// The tables stay in the decoder, and the blob is the coded program.
package tm

import (
	"bytes"
	"errors"
	"sort"
)

type Lyndon struct {
	Words [][]uint8
}

func (l *Lyndon) Factor(s []uint8) {
	k, m, n, words, max := 0, 1, len(s), l.Words[:0], len(s)
	if max == 0 {
		panic("len(s) should be > 0")
	} else if max == 1 {
		l.Words = append(words, s)
		return
	} else if max > 256 {
		max = 256 + (max-256)/2
	}
	if cap(words) < max {
		words = make([][]uint8, 0, max)
	}

	for {
		switch sk, sm := s[k], s[m]; true {
		case sk < sm:
			k, m = 0, m+1
			if m < n {
				continue
			}
		case sk == sm:
			k, m = k+1, m+1
			if m < n {
				continue
			}
			fallthrough
		case sk > sm:
			split := m - k
			k, m, s, words = 0, 1, s[split:], append(words, s[:split])
			n = len(s)
			if n > 1 {
				continue
			}
		}
		break
	}
	l.Words = append(words, s)
}

type rotation struct {
	int
	s []uint8
}

type Rotations []rotation

func (r Rotations) Len() int {
	return len(r)
}

func less(a, b rotation) bool {
	la, lb, ia, ib := len(a.s), len(b.s), a.int, b.int
	for {
		if x, y := a.s[ia], b.s[ib]; x != y {
			return x < y
		}
		ia, ib = ia+1, ib+1
		if ia == la {
			ia = 0
		}
		if ib == lb {
			ib = 0
		}
		if ia == a.int && ib == b.int {
			break
		}
	}
	return false
}

func (r Rotations) Less(i, j int) bool {
	return less(r[i], r[j])
}

func (r Rotations) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

func merge(left, right, out Rotations) {
	for len(left) > 0 && len(right) > 0 {
		if less(left[0], right[0]) {
			out[0], left = left[0], left[1:]
		} else {
			out[0], right = right[0], right[1:]
		}
		out = out[1:]
	}
	copy(out, left)
	copy(out, right)
}

func psort(in Rotations, s chan<- bool) {
	if len(in) < 1024 {
		sort.Sort(in)
		s <- true
		return
	}

	l, r, split := make(chan bool), make(chan bool), len(in)/2
	left, right := in[:split], in[split:]
	go psort(left, l)
	go psort(right, r)
	_, _ = <-l, <-r
	out := make(Rotations, len(in))
	merge(left, right, out)
	copy(in, out)
	s <- true
}

func BijectiveBurrowsWheelerCoder(block []byte) []byte {
	var lyndon Lyndon
	var rotations Rotations
	wait := make(chan bool)
	var buffer []uint8

	if cap(buffer) < len(block) {
		buffer = make([]uint8, len(block))
	} else {
		buffer = buffer[:len(block)]
	}
	copy(buffer, block)
	lyndon.Factor(buffer)

	/* rotate */
	if length := len(block); cap(rotations) < length {
		rotations = make(Rotations, length)
	} else {
		rotations = rotations[:length]
	}
	r := 0
	for _, word := range lyndon.Words {
		for i, _ := range word {
			rotations[r], r = rotation{i, word}, r+1
		}
	}

	go psort(rotations, wait)
	<-wait

	/* output the last character of each rotation */
	for i, j := range rotations {
		if j.int == 0 {
			j.int = len(j.s)
		}
		block[i] = j.s[j.int-1]
	}

	return block
}

func BijectiveBurrowsWheelerDecoder(buffer []byte) {
	inverse := func(buffer []byte) {
		length := len(buffer)
		input, major, minor := make([]byte, length), [256]int{}, make([]int, length)
		for k, v := range buffer {
			input[k], minor[k], major[v] = v, major[v], major[v]+1
		}

		sum := 0
		for k, v := range major {
			major[k], sum = sum, sum+v
		}

		j := length - 1
		for k, _ := range input {
			for minor[k] != -1 {
				buffer[j], j, k, minor[k] = input[k], j-1, major[input[k]]+minor[k], -1
			}
		}
	}

	inverse(buffer)
}

func MoveToFrontCoder(block []byte) []byte {
	nodes, buffer := [256]byte{}, make([]byte, 0, len(block))
	var first byte

	for node, _ := range nodes {
		nodes[node] = uint8(node) + 1
	}

	for _, v := range block {
		var node, next byte
		var symbol uint8
		for next = first; next != v; node, next = next, nodes[next] {
			symbol++
		}

		buffer = append(buffer, symbol)
		if symbol != 0 {
			first, nodes[node], nodes[next] = next, nodes[next], first
		}
	}
	return buffer
}

func MoveToFrontDecoder(block []byte) []byte {
	nodes, buffer := [256]byte{}, make([]byte, 0, len(block))
	var first byte

	for node, _ := range nodes {
		nodes[node] = uint8(node) + 1
	}

	for _, symbol := range block {
		var node, next byte
		moveToFront := symbol != 0
		for next = first; symbol > 0; node, next = next, nodes[next] {
			symbol--
		}

		if moveToFront {
			first, nodes[node], nodes[next] = next, nodes[next], first
		}

		buffer = append(buffer, next)
	}

	return buffer
}

const (
	opEmit = 1
	opLit  = 2
	opRep  = 3
	opCopy = 4
	opFar  = 5

	maxProbe  = 256
	maxCopy   = 255
	maxDist16 = 65535
)

// cheapNear and cheapFar are the fewest fixed-table bits a near or far
// distance can cost. A match that is already that cheap and maxCopy long
// dominates every later match in the probe window.
var cheapNear, cheapFar int

func init() {
	cheapNear = distMin(1, maxDist16, false)
	low := distMin(0, maxDist16, true)
	high := distMin(1, maxDist16, true)
	cheapFar = int(opLen[opFar]) + low + high
}

// distMin is the minimum distance-byte cost over distances in [lo, hi].
// pair is the low 16 bits only; the far opcode is added by the caller.
func distMin(lo, hi int, pair bool) int {
	best := int(^uint(0) >> 1)
	for d := lo; d <= hi; d++ {
		b := int(distLen[byte(d)]) + int(distLen[byte(d>>8)])
		if !pair {
			b = distBits(d, false)
		}
		if b < best {
			best = b
		}
	}
	return best
}

// ErrFormat reports a program this machine cannot run.
var ErrFormat = errors.New("tm: invalid program")

var errMismatch = errors.New("tm: encode mismatch")

// Level is the program Search found. Bytes is its length and Bits is that
// length under the fixed field tables.
type Level struct {
	Machines int
	Bytes    int
	Bits     int
}

// Machine is the program Search found, plus the size of each pairing round.
type Machine struct {
	Prog   []byte
	Levels []Level
}

// Compress returns the table-coded program that reprints src.
func Compress(src []byte) ([]byte, error) {
	m, err := Search(src, true)
	if err != nil {
		panic(err)
	}
	return pack(m.Prog)
}

// Decompress runs a program produced by Compress.
func Decompress(prog []byte) ([]byte, error) {
	raw, err := unpack(prog)
	if err != nil {
		return nil, err
	}
	return run(raw, true)
}

// Search returns the program with the fewest fixed-table bits that reprints src.
func Search(src []byte, enc bool) (Machine, error) {
	if len(src) == 0 {
		return Machine{}, nil
	}
	if enc {
		cp := make([]byte, len(src))
		copy(cp, src)
		src = BijectiveBurrowsWheelerCoder(cp)
		src = MoveToFrontCoder(src)
	}
	prog := encodeDP(src)
	if err := matches(prog, src); err != nil {
		panic(err)
	}
	return Machine{Prog: prog, Levels: []Level{{
		Machines: 1,
		Bytes:    len(prog),
		Bits:     mustBits(prog),
	}}}, nil
}

func mustBits(prog []byte) int {
	n, err := codedBits(prog)
	if err != nil {
		panic(err)
	}
	return n
}

func matches(prog, data []byte) error {
	got, err := run(prog, false)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return errMismatch
	}
	return nil
}

func emitBits(b byte) int {
	return int(opLen[opEmit]) + int(payLen[b])
}

func repBits(b byte, n int) int {
	return int(opLen[opRep]) + int(payLen[b]) + int(lenLen[n])
}

func distBits(dist int, far bool) int {
	if far {
		return int(opLen[opFar]) +
			int(distLen[byte(dist)]) + int(distLen[byte(dist>>8)]) +
			int(distLen[byte(dist>>16)]) + int(distLen[byte(dist>>24)])
	}
	return int(opLen[opCopy]) + int(distLen[byte(dist)]) + int(distLen[byte(dist>>8)])
}

type link struct {
	cost int
	prev int
	op   byte
	a, b int
}

func encodeDP(data []byte) []byte {
	n := len(data)
	const inf = int(^uint(0) >> 2)
	links := make([]link, n+1)
	for i := 1; i <= n; i++ {
		links[i].cost = inf
	}
	c := newCoder(data)
	for i := 0; i < n; i++ {
		maxL := n - i
		if maxL > maxCopy {
			maxL = maxCopy
		}
		acc := 0
		for l := 1; l <= maxL; l++ {
			acc += int(payLen[data[i+l-1]])
			if l == 1 {
				relax(links, i+1, links[i].cost+emitBits(data[i]), i, opEmit, int(data[i]), 0)
				continue
			}
			relax(links, i+l, links[i].cost+int(opLen[opLit])+int(lenLen[l])+acc, i, opLit, l, 0)
		}
		if r := c.run[i]; r >= 2 {
			if r > maxCopy {
				r = maxCopy
			}
			for k := 2; k <= r; k++ {
				relax(links, i+k, links[i].cost+repBits(data[i], k), i, opRep, k, int(data[i]))
			}
		}
		for _, m := range c.candsAt(i) {
			op := byte(opCopy)
			if m.far {
				op = opFar
			}
			base := links[i].cost + m.base
			for l := 3; l <= m.l; l++ {
				relax(links, i+l, base+int(lenLen[l]), i, op, m.dist, l)
			}
		}
		c.add(i)
	}
	chunks := make([][]byte, 0, 8)
	for pos := n; pos > 0; {
		l := links[pos]
		if l.prev < 0 || l.prev >= pos || l.cost == inf {
			panic("tm: broken path")
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
			chunks = append(chunks, emitCopy(l.a, l.b, false))
		case opFar:
			chunks = append(chunks, emitCopy(l.a, l.b, true))
		default:
			panic("tm: broken path")
		}
		pos = l.prev
	}
	prog := make([]byte, 0, links[n].cost)
	for i := len(chunks) - 1; i >= 0; i-- {
		prog = append(prog, chunks[i]...)
	}
	if mustBits(prog) != links[n].cost {
		panic("tm: bit cost")
	}
	return prog
}

func relax(links []link, pos, cost, prev int, op byte, a, b int) {
	if cost < links[pos].cost {
		links[pos] = link{cost: cost, prev: prev, op: op, a: a, b: b}
	}
}

func emitCopy(dist, n int, far bool) []byte {
	if !far {
		return []byte{opCopy, byte(dist), byte(dist >> 8), byte(n)}
	}
	return []byte{opFar, byte(dist), byte(dist >> 8), byte(dist >> 16), byte(dist >> 24), byte(n)}
}

type cand struct {
	l    int
	dist int
	base int
	far  bool
}

type coder struct {
	data  []byte
	run   []int
	mask  int
	head  []int
	next  []int
	cands []cand
}

func newCoder(data []byte) *coder {
	n := len(data)
	run := make([]int, n)
	for i := n - 1; i >= 0; i-- {
		run[i] = 1
		if i+1 < n && data[i] == data[i+1] {
			run[i] = run[i+1] + 1
		}
	}
	size := 16
	for size < n*2 {
		size <<= 1
	}
	head := make([]int, size)
	for i := range head {
		head[i] = -1
	}
	return &coder{
		data:  data,
		run:   run,
		mask:  size - 1,
		head:  head,
		next:  make([]int, n),
		cands: make([]cand, 0, 16),
	}
}

func (c *coder) add(j int) {
	if j < 0 || j+2 >= len(c.data) {
		return
	}
	k := int(c.data[j])<<16 | int(c.data[j+1])<<8 | int(c.data[j+2])
	slot := k & c.mask
	c.next[j] = c.head[slot]
	c.head[slot] = j
}

// candsAt returns matches that are not both shorter and at least as expensive
// as another match in the probe window. Costs are fixed-table bits, so a
// recent short copy can beat a longer one whose distance bytes are rare.
func (c *coder) candsAt(p int) []cand {
	c.cands = c.cands[:0]
	data := c.data
	if p+2 >= len(data) {
		return nil
	}
	k := int(data[p])<<16 | int(data[p+1])<<8 | int(data[p+2])
	steps := 0
	for j := c.head[k&c.mask]; j >= 0; j = c.next[j] {
		steps++
		if steps > maxProbe {
			break
		}
		if j >= p || p-j < 3 {
			continue
		}
		if data[j] != data[p] || data[j+1] != data[p+1] || data[j+2] != data[p+2] {
			continue
		}
		L := c.matchLen(j, p)
		if L < 3 {
			continue
		}
		d := p - j
		far := d > maxDist16
		if c.pushCand(L, d, distBits(d, far), far) {
			break
		}
	}
	return c.cands
}

// pushCand keeps m when it is not dominated. It reports whether m is long
// enough and cheap enough that later matches cannot improve the front.
func (c *coder) pushCand(L, dist, base int, far bool) bool {
	for _, m := range c.cands {
		if m.l >= L && m.base <= base {
			return false
		}
	}
	n := 0
	for _, m := range c.cands {
		if L >= m.l && base <= m.base {
			continue
		}
		c.cands[n] = m
		n++
	}
	c.cands = append(c.cands[:n], cand{l: L, dist: dist, base: base, far: far})
	return L == maxCopy && base <= cheapNear && base <= cheapFar
}

func (c *coder) matchLen(j, p int) int {
	data := c.data
	maxM := p - j
	if remain := len(data) - p; remain < maxM {
		maxM = remain
	}
	if maxM > maxCopy {
		maxM = maxCopy
	}
	L := c.run[j]
	if c.run[p] < L {
		L = c.run[p]
	}
	if L > maxM {
		L = maxM
	}
	for L < maxM && data[j+L] == data[p+L] {
		L++
	}
	return L
}

func run(prog []byte, decode bool) ([]byte, error) {
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
			if n < 2 || n > maxCopy || i+n > len(prog) {
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
			if err := copyInto(&out, dist, n); err != nil {
				return nil, err
			}
			i += 4
		case opFar:
			if i+5 >= len(prog) {
				return nil, ErrFormat
			}
			dist := int(prog[i+1]) | int(prog[i+2])<<8 | int(prog[i+3])<<16 | int(prog[i+4])<<24
			n := int(prog[i+5])
			if err := copyInto(&out, dist, n); err != nil {
				return nil, err
			}
			i += 6
		default:
			return nil, ErrFormat
		}
	}
	if decode {
		out = MoveToFrontDecoder(out)
		BijectiveBurrowsWheelerDecoder(out)
	}
	return out, nil
}

func copyInto(out *[]byte, dist, n int) error {
	if dist < 1 || n < 3 || dist > len(*out) {
		return ErrFormat
	}
	for range n {
		*out = append(*out, (*out)[len(*out)-dist])
	}
	return nil
}
