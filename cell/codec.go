// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	pixels  = frame
	frames  = 66
	half    = frames / 2
	baseN   = half * pixels
	motionN = (half - 1) * pixels
	block   = 8
	grid    = width / block
	nBlock  = grid * (height / block)
	mvRange = 2
	kBright = 2
	kXor    = 5
	kOnes   = 4
	kValue  = 5
	kNew    = 3
	kStay   = 4
	kMove   = 0
	kCorr   = 5
)

// CompressedSize is the length of the blob for the canonical cell.csv.
// 45207 bits is the fixed-parameter motion coding of that file.
const CompressedSize = 5651

// ErrFormat reports an input that is not the canonical cell clip or a blob
// this model can decode.
var ErrFormat = errors.New("cell: invalid data")

// The clip is 66 frames at four per second, so the header and the time
// column are fixed. The second half repeats the first, except at pixels
// that differ by one.
//
// Inside the first half, each 8x8 block is a translation of the previous
// frame by at most two pixels. A zero vector is one bit; any other vector
// is a zigzag Rice pair. Pixels whose brightness the translation gets wrong
// are a gap-Rice correction of the bright mask. A translated bright pixel
// stores a Rice residual from the pixel it came from, and a newly bright
// pixel stores its level above 1. Ones stay an absolute gap-Rice mask.
// Decompress applies those same translations and residuals.

var head string

func init() {
	var b strings.Builder
	b.WriteString("time")
	for y := range height {
		for x := range width {
			fmt.Fprintf(&b, ",r%dc%d", y, x)
		}
	}
	head = b.String()
}

// Compress packs a canonical cell.csv into its fixed-parameter motion coding.
func Compress(src []byte) ([]byte, error) {
	pix, err := parse(src)
	if err != nil {
		return nil, err
	}
	var bright0, ones, corr, signs []int
	var values0 []int
	for f := range half {
		for p := range pixels {
			v := int(pix[f][p])
			if f == 0 && v > 1 {
				bright0 = append(bright0, p)
				values0 = append(values0, v-2)
			}
			if v == 1 {
				ones = append(ones, f*pixels+p)
			}
			d := int(pix[f+half][p]) - v
			switch d {
			case 0:
			case -1:
				corr = append(corr, f*pixels+p)
				signs = append(signs, 0)
			case 1:
				corr = append(corr, f*pixels+p)
				signs = append(signs, 1)
			default:
				return nil, ErrFormat
			}
		}
	}
	vecs, xors, levels := motionOf(&pix)
	var w bits
	w.bits(len(bright0), 16)
	w.bits(len(xors), 16)
	w.bits(len(ones), 16)
	w.bits(len(corr), 16)
	writeGaps(&w, bright0, kBright)
	for _, v := range values0 {
		w.rice(v, kValue)
	}
	for f := range vecs {
		for _, mv := range vecs[f] {
			w.vector(mv[0], mv[1])
		}
	}
	writeGaps(&w, xors, kXor)
	for _, lv := range levels {
		w.rice(lv[1], lv[0])
	}
	writeGaps(&w, ones, kOnes)
	writeGaps(&w, corr, kCorr)
	for _, s := range signs {
		w.bit(s)
	}
	return w.finish(), nil
}

// Decompress restores the exact file bytes written by Compress.
func Decompress(src []byte) ([]byte, error) {
	r := reader{b: src}
	nBright, err := r.bits(16)
	if err != nil {
		return nil, err
	}
	nXor, err := r.bits(16)
	if err != nil {
		return nil, err
	}
	nOnes, err := r.bits(16)
	if err != nil {
		return nil, err
	}
	nCorr, err := r.bits(16)
	if err != nil {
		return nil, err
	}
	bright0, err := readGaps(&r, nBright, kBright, pixels)
	if err != nil {
		return nil, err
	}
	var pix [frames][pixels]byte
	for _, p := range bright0 {
		v, err := r.rice(kValue)
		if err != nil {
			return nil, err
		}
		if v > 253 {
			return nil, ErrFormat
		}
		pix[0][p] = byte(v + 2)
	}
	var vecs [half - 1][nBlock][2]int
	for f := range vecs {
		for b := range vecs[f] {
			dx, dy, err := r.vector()
			if err != nil {
				return nil, err
			}
			vecs[f][b] = [2]int{dx, dy}
		}
	}
	xors, err := readGaps(&r, nXor, kXor, motionN)
	if err != nil {
		return nil, err
	}
	xi := 0
	for dest := 1; dest < half; dest++ {
		var pred [pixels]byte
		var bright [pixels]bool
		for p := range pixels {
			pred[p] = predAt(&pix[dest-1], vecs[dest-1], p)
			bright[p] = pred[p] > 1
		}
		for xi < len(xors) && xors[xi]/pixels == dest-1 {
			p := xors[xi] % pixels
			bright[p] = !bright[p]
			xi++
		}
		for p := range pixels {
			if !bright[p] {
				continue
			}
			v, err := r.level(pred[p])
			if err != nil {
				return nil, err
			}
			pix[dest][p] = byte(v)
		}
	}
	if xi != len(xors) {
		return nil, ErrFormat
	}
	ones, err := readGaps(&r, nOnes, kOnes, baseN)
	if err != nil {
		return nil, err
	}
	for _, i := range ones {
		f, p := i/pixels, i%pixels
		if pix[f][p] != 0 {
			return nil, ErrFormat
		}
		pix[f][p] = 1
	}
	corr, err := readGaps(&r, nCorr, kCorr, baseN)
	if err != nil {
		return nil, err
	}
	for f := range half {
		pix[f+half] = pix[f]
	}
	for _, i := range corr {
		s, err := r.bit()
		if err != nil {
			return nil, err
		}
		f, p := i/pixels, i%pixels
		v := int(pix[f][p])
		if s == 0 {
			v--
		} else {
			v++
		}
		if v < 0 || v > 255 {
			return nil, ErrFormat
		}
		pix[f+half][p] = byte(v)
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return format(pix), nil
}

func motionOf(pix *[frames][pixels]byte) ([half - 1][nBlock][2]int, []int, [][2]int) {
	var vecs [half - 1][nBlock][2]int
	var xors []int
	var levels [][2]int
	for f := 1; f < half; f++ {
		vecs[f-1] = searchMV(&pix[f-1], &pix[f])
		for p := range pixels {
			pred := predAt(&pix[f-1], vecs[f-1], p)
			cur := pix[f][p]
			if (pred > 1) != (cur > 1) {
				xors = append(xors, (f-1)*pixels+p)
			}
			if k, v, ok := residual(pred, cur); ok {
				levels = append(levels, [2]int{k, v})
			}
		}
	}
	return vecs, xors, levels
}

// (dx, dy) is how far the block moved. The sample under (x, y) is the
// previous pixel at (x-dx, y-dy); off-frame samples are black.
func predAt(src *[pixels]byte, vecs [nBlock][2]int, p int) byte {
	y, x := p/width, p%width
	b := (y/block)*grid + x/block
	dx, dy := vecs[b][0], vecs[b][1]
	return sample(src, x-dx, y-dy)
}

func sample(src *[pixels]byte, x, y int) byte {
	if uint(x) >= width || uint(y) >= height {
		return 0
	}
	return src[y*width+x]
}

func residual(pred, cur byte) (k, v int, ok bool) {
	if cur <= 1 {
		return 0, 0, false
	}
	if pred > 1 {
		return kStay, zig(int(cur) - int(pred)), true
	}
	return kNew, int(cur) - 2, true
}

func searchMV(prev, cur *[pixels]byte) [nBlock][2]int {
	var out [nBlock][2]int
	b := 0
	for by := 0; by < height; by += block {
		for bx := 0; bx < width; bx += block {
			bestMis := block*block + 1
			var bestDX, bestDY int
			for oy := -mvRange; oy <= mvRange; oy++ {
				for ox := -mvRange; ox <= mvRange; ox++ {
					mis := blockMiss(prev, cur, bx, by, ox, oy)
					if lessMV(mis, ox, oy, bestMis, bestDX, bestDY) {
						bestMis = mis
						bestDX, bestDY = ox, oy
					}
				}
			}
			out[b] = [2]int{bestDX, bestDY}
			b++
		}
	}
	return out
}

func blockMiss(prev, cur *[pixels]byte, bx, by, dx, dy int) int {
	mis := 0
	for y := by; y < by+block; y++ {
		for x := bx; x < bx+block; x++ {
			p := sample(prev, x-dx, y-dy)
			c := cur[y*width+x]
			if (p > 1) != (c > 1) {
				mis++
			}
		}
	}
	return mis
}

func lessMV(mis, dx, dy, bestMis, bestDX, bestDY int) bool {
	if mis != bestMis {
		return mis < bestMis
	}
	if iabs(dx)+iabs(dy) != iabs(bestDX)+iabs(bestDY) {
		return iabs(dx)+iabs(dy) < iabs(bestDX)+iabs(bestDY)
	}
	if iabs(dx) != iabs(bestDX) {
		return iabs(dx) < iabs(bestDX)
	}
	if dx != bestDX {
		return dx < bestDX
	}
	return dy < bestDY
}

func iabs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func zig(n int) int {
	if n >= 0 {
		return n * 2
	}
	return -n*2 - 1
}

func unzig(n int) int {
	if n&1 == 0 {
		return n / 2
	}
	return -(n/2 + 1)
}

func (b *bits) vector(dx, dy int) {
	if dx == 0 && dy == 0 {
		b.bit(0)
		return
	}
	b.bit(1)
	b.rice(zig(dx), kMove)
	b.rice(zig(dy), kMove)
}

func (r *reader) vector() (int, int, error) {
	flag, err := r.bit()
	if err != nil {
		return 0, 0, err
	}
	if flag == 0 {
		return 0, 0, nil
	}
	dx, err := r.motion()
	if err != nil {
		return 0, 0, err
	}
	dy, err := r.motion()
	if err != nil {
		return 0, 0, err
	}
	return dx, dy, nil
}

func (r *reader) motion() (int, error) {
	n, err := r.rice(kMove)
	if err != nil {
		return 0, err
	}
	v := unzig(n)
	if v < -mvRange || v > mvRange {
		return 0, ErrFormat
	}
	return v, nil
}

func (r *reader) level(pred byte) (int, error) {
	if pred > 1 {
		d, err := r.rice(kStay)
		if err != nil {
			return 0, err
		}
		v := int(pred) + unzig(d)
		if v < 2 || v > 255 {
			return 0, ErrFormat
		}
		return v, nil
	}
	d, err := r.rice(kNew)
	if err != nil {
		return 0, err
	}
	v := d + 2
	if v > 255 {
		return 0, ErrFormat
	}
	return v, nil
}

func writeGaps(w *bits, pos []int, k int) {
	prev := -1
	for _, p := range pos {
		w.rice(p-prev-1, k)
		prev = p
	}
}

func readGaps(r *reader, count, k, limit int) ([]int, error) {
	if count > limit {
		return nil, ErrFormat
	}
	pos := make([]int, count)
	prev := -1
	for i := range pos {
		g, err := r.rice(k)
		if err != nil {
			return nil, err
		}
		p := prev + 1 + g
		if g < 0 || p >= limit {
			return nil, ErrFormat
		}
		pos[i] = p
		prev = p
	}
	return pos, nil
}

func parse(src []byte) ([frames][pixels]byte, error) {
	var pix [frames][pixels]byte
	if len(src) == 0 || src[len(src)-1] != '\n' {
		return pix, ErrFormat
	}
	lines := bytes.Split(src[:len(src)-1], []byte("\n"))
	if len(lines) != 1+frames || string(lines[0]) != head {
		return pix, ErrFormat
	}
	for f, line := range lines[1:] {
		fields := bytes.Split(line, []byte(","))
		if len(fields) != 1+pixels || string(fields[0]) != timeOf(f) {
			return pix, ErrFormat
		}
		for p, field := range fields[1:] {
			v, err := parseLevel(field)
			if err != nil {
				return pix, err
			}
			pix[f][p] = byte(v)
		}
	}
	return pix, nil
}

func parseLevel(field []byte) (int, error) {
	if len(field) == 0 || (len(field) > 1 && field[0] == '0') {
		return 0, ErrFormat
	}
	v := 0
	for _, c := range field {
		if c < '0' || c > '9' {
			return 0, ErrFormat
		}
		v = v*10 + int(c-'0')
		if v > 255 {
			return 0, ErrFormat
		}
	}
	return v, nil
}

func format(pix [frames][pixels]byte) []byte {
	var b bytes.Buffer
	b.WriteString(head)
	b.WriteByte('\n')
	for f := range frames {
		b.WriteString(timeOf(f))
		for p := range pixels {
			b.WriteByte(',')
			b.WriteString(strconv.Itoa(int(pix[f][p])))
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func timeOf(i int) string {
	sec, frac := i/4, i%4
	switch frac {
	case 0:
		return strconv.Itoa(sec)
	case 1:
		return strconv.Itoa(sec) + ".25"
	case 2:
		return strconv.Itoa(sec) + ".5"
	default:
		return strconv.Itoa(sec) + ".75"
	}
}

// bits is a most-significant-bit-first stream. Rice codes write a unary
// quotient of one-bits ended by a zero, then the low k bits.
type bits struct {
	buf []byte
	acc uint64
	n   int
}

func (b *bits) bit(v int) { b.bits(v, 1) }

func (b *bits) bits(v, n int) {
	b.acc = (b.acc << uint(n)) | uint64(v)
	b.n += n
	for b.n >= 8 {
		b.n -= 8
		b.buf = append(b.buf, byte(b.acc>>uint(b.n)))
		b.acc &= (1 << uint(b.n)) - 1
	}
}

func (b *bits) rice(n, k int) {
	for range n >> k {
		b.bit(1)
	}
	b.bit(0)
	if k > 0 {
		b.bits(n&((1<<k)-1), k)
	}
}

func (b *bits) finish() []byte {
	if b.n > 0 {
		b.buf = append(b.buf, byte(b.acc<<uint(8-b.n)))
	}
	return b.buf
}

type reader struct {
	b    []byte
	i    int
	cur  byte
	left int
}

func (r *reader) bit() (int, error) {
	v, err := r.bits(1)
	return v, err
}

func (r *reader) bits(n int) (int, error) {
	v := 0
	for range n {
		if r.left == 0 {
			if r.i >= len(r.b) {
				return 0, ErrFormat
			}
			r.cur = r.b[r.i]
			r.i++
			r.left = 8
		}
		r.left--
		v = (v << 1) | int(r.cur>>uint(r.left))&1
		r.cur &= (1 << uint(r.left)) - 1
	}
	return v, nil
}

func (r *reader) rice(k int) (int, error) {
	q := 0
	for {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		if b == 0 {
			break
		}
		q++
		if q > baseN {
			return 0, ErrFormat
		}
	}
	rem := 0
	if k > 0 {
		var err error
		rem, err = r.bits(k)
		if err != nil {
			return 0, err
		}
	}
	return (q << k) | rem, nil
}

func (r *reader) done() error {
	if r.cur != 0 || r.i != len(r.b) {
		return ErrFormat
	}
	return nil
}
