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
	kMove   = 0
)

// Gap and value Rice parameters, one entry per channel. The order is red,
// green, blue. Encode and decode use these same constants; they are not
// written into the blob.
var (
	kGap0   = [channels]int{2, 2, 1}
	kVal0   = [channels]int{6, 4, 5}
	kGapM   = [channels]int{2, 2, 1}
	kDelta  = [channels]int{5, 4, 4}
	kGapH   = [channels]int{5, 5, 4}
	kDeltaH = [channels]int{1, 1, 1}
)

// CompressedSize is the length of the blob for the canonical cell.csv.
// 204669 bits is the shared-luma motion coding with one residual stream per channel.
const CompressedSize = 25584

// ErrFormat reports an input that is not the canonical cell clip or a blob
// this model can decode.
var ErrFormat = errors.New("cell: invalid data")

// bg is the flat field frame 0 is coded against. Blue's mode on this clip is 1.
var bg = rgb{0, 0, 1}

// The clip is 66 color frames at four per second, so the header and the time
// column are fixed. Motion is one field for every channel: each 8x8 block is
// a translation of the previous frame by at most two pixels, chosen by the
// integer luma of the block. A zero vector is one bit; any other vector is a
// zigzag Rice pair. The sample under (x, y) is the previous pixel at
// (x-dx, y-dy). Off-frame samples are black, which is not the background.
//
// Frame 0 stores the samples that differ from the background. Each later base
// frame stores the samples that differ from the motion prediction. The second
// half stores the samples that differ from the first. A difference is a gap
// into the scan and a zigzag Rice residual. The three channels are separate
// streams and share the motion field.

type rgb [channels]byte

type plane struct {
	pos []int
	val []int
}

var head string

func init() {
	var b strings.Builder
	b.WriteString("time")
	for y := range height {
		for x := range width {
			for c := range channels {
				b.WriteByte(',')
				b.WriteString(pixelName(y, x, c))
			}
		}
	}
	head = b.String()
}

func pixelName(y, x, c int) string {
	return fmt.Sprintf("r%dc%d%c", y, x, "rgb"[c])
}

// Compress packs a canonical cell.csv into its fixed-parameter motion coding.
func Compress(src []byte) ([]byte, error) {
	pix, err := parse(src)
	if err != nil {
		return nil, err
	}
	var f0, mis, corr [channels]plane
	for c := range channels {
		for p := range pixels {
			if pix[0][p][c] != bg[c] {
				f0[c].pos = append(f0[c].pos, p)
				f0[c].val = append(f0[c].val, int(pix[0][p][c]))
			}
		}
	}
	vecs := motionVecs(&pix)
	for f := 1; f < half; f++ {
		pred := warp(&pix[f-1], vecs[f-1])
		for p := range pixels {
			for c := range channels {
				d := int(pix[f][p][c]) - int(pred[p][c])
				if d == 0 {
					continue
				}
				mis[c].pos = append(mis[c].pos, (f-1)*pixels+p)
				mis[c].val = append(mis[c].val, d)
			}
		}
	}
	for f := range half {
		for p := range pixels {
			for c := range channels {
				d := int(pix[f+half][p][c]) - int(pix[f][p][c])
				if d == 0 {
					continue
				}
				corr[c].pos = append(corr[c].pos, f*pixels+p)
				corr[c].val = append(corr[c].val, d)
			}
		}
	}
	var w bits
	for c := range channels {
		w.bits(len(f0[c].pos), 16)
	}
	for c := range channels {
		w.bits(len(mis[c].pos), 16)
	}
	for c := range channels {
		w.bits(len(corr[c].pos), 16)
	}
	for c := range channels {
		writeAbs(&w, &f0[c], kGap0[c], kVal0[c])
	}
	for f := range vecs {
		for _, mv := range vecs[f] {
			w.vector(mv[0], mv[1])
		}
	}
	for c := range channels {
		writeDelta(&w, &mis[c], kGapM[c], kDelta[c])
	}
	for c := range channels {
		writeDelta(&w, &corr[c], kGapH[c], kDeltaH[c])
	}
	return w.finish(), nil
}

func writeAbs(w *bits, p *plane, kPos, kVal int) {
	writeGaps(w, p.pos, kPos)
	for _, v := range p.val {
		w.rice(v, kVal)
	}
}

func writeDelta(w *bits, p *plane, kPos, kVal int) {
	writeGaps(w, p.pos, kPos)
	for _, v := range p.val {
		w.rice(zig(v), kVal)
	}
}

// nVecSym is the number of motion vectors the codec can write.
const nVecSym = (2*mvRange + 1) * (2*mvRange + 1)

// Predict extends a compressed clip by 33 frames.
//
// Each block's motion vector is a Markov chain learned from the clip.
// A draw proposes one vector per block; the codeword actually written is
// the stored motion field closest to that proposal, so the blocks stay on
// a field the codec produced. The residual is empty, so each pixel is the
// color its vector points at. The three channels move together.
func Predict(blob []byte) ([half][pixels]rgb, error) {
	var out [half][pixels]rgb
	raw, err := Decompress(blob)
	if err != nil {
		return out, err
	}
	pix, err := parse(raw)
	if err != nil {
		return out, err
	}
	trans, marg, fields := learnMotion(&pix)
	state := fields[len(fields)-1]
	cur := pix[frames-1]
	rng := lcg(1)
	for f := range out {
		var prop [nBlock][2]int
		for b := range prop {
			id := vectorID(state[b][0], state[b][1])
			id = draw(&rng, &trans[id], &marg)
			prop[b][0], prop[b][1] = idVector(id)
		}
		mv := nearestField(fields, prop)
		next, err := stepCompressed(&cur, mv)
		if err != nil {
			return out, err
		}
		out[f] = next
		cur = next
		state = mv
	}
	return out, nil
}

func learnMotion(pix *[frames][pixels]rgb) ([nVecSym][nVecSym]int, [nVecSym]int, [][nBlock][2]int) {
	var trans [nVecSym][nVecSym]int
	var marg [nVecSym]int
	fields := make([][nBlock][2]int, 0, frames-1)
	var prev [nBlock]int
	seen := false
	for f := 1; f < frames; f++ {
		mv := searchMV(&pix[f-1], &pix[f])
		fields = append(fields, mv)
		for b, v := range mv {
			id := vectorID(v[0], v[1])
			marg[id]++
			if seen {
				trans[prev[b]][id]++
			}
			prev[b] = id
		}
		seen = true
	}
	return trans, marg, fields
}

func nearestField(fields [][nBlock][2]int, prop [nBlock][2]int) [nBlock][2]int {
	best := 0
	bestH, bestL := nBlock+1, 0
	for i, f := range fields {
		h, l := 0, 0
		for b := range f {
			if f[b] != prop[b] {
				h++
			}
			l += iabs(f[b][0]-prop[b][0]) + iabs(f[b][1]-prop[b][1])
		}
		if h < bestH || (h == bestH && l < bestL) {
			bestH, bestL, best = h, l, i
		}
	}
	return fields[best]
}

func stepCompressed(cur *[pixels]rgb, mv [nBlock][2]int) ([pixels]rgb, error) {
	var next [pixels]rgb
	var w bits
	for _, v := range mv {
		w.vector(v[0], v[1])
	}
	r := reader{b: w.finish()}
	var got [nBlock][2]int
	for b := range got {
		dx, dy, err := r.vector()
		if err != nil {
			return next, err
		}
		got[b] = [2]int{dx, dy}
	}
	if err := r.done(); err != nil {
		return next, err
	}
	return warp(cur, got), nil
}

func vectorID(dx, dy int) int {
	span := 2*mvRange + 1
	return (dy+mvRange)*span + (dx + mvRange)
}

func idVector(id int) (int, int) {
	span := 2*mvRange + 1
	return id%span - mvRange, id/span - mvRange
}

// lcg is the fixed generator for the forecast. The multiplier is the
// Numerical Recipes constant; the low bits are not used.
type lcg uint64

func (r *lcg) next() uint32 {
	*r = *r*1664525 + 1013904223
	return uint32(*r >> 32)
}

func draw(r *lcg, row *[nVecSym]int, marg *[nVecSym]int) int {
	counts := row[:]
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		counts = marg[:]
		for _, c := range counts {
			total += c
		}
	}
	if total == 0 {
		return vectorID(0, 0)
	}
	u := int(r.next() % uint32(total))
	for i, c := range counts {
		u -= c
		if u < 0 {
			return i
		}
	}
	return vectorID(0, 0)
}

// Decompress restores the exact file bytes written by Compress.
func Decompress(src []byte) ([]byte, error) {
	r := reader{b: src}
	var n0, nMis, nHalf [channels]int
	for c := range n0 {
		n, err := r.bits(16)
		if err != nil {
			return nil, err
		}
		n0[c] = n
	}
	for c := range nMis {
		n, err := r.bits(16)
		if err != nil {
			return nil, err
		}
		nMis[c] = n
	}
	for c := range nHalf {
		n, err := r.bits(16)
		if err != nil {
			return nil, err
		}
		nHalf[c] = n
	}
	var pix [frames][pixels]rgb
	for p := range pix[0] {
		pix[0][p] = bg
	}
	for c := range channels {
		if err := readAbs(&r, &pix[0], c, n0[c], kGap0[c], kVal0[c]); err != nil {
			return nil, err
		}
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
	var mis [channels][]hit
	for c := range channels {
		hits, err := readHits(&r, nMis[c], kGapM[c], kDelta[c], motionN)
		if err != nil {
			return nil, err
		}
		mis[c] = hits
	}
	// Each prediction is taken from the frame after its residuals are applied.
	var at [channels]int
	for dest := 1; dest < half; dest++ {
		pix[dest] = warp(&pix[dest-1], vecs[dest-1])
		for c := range channels {
			for at[c] < len(mis[c]) && mis[c][at[c]].pos/pixels == dest-1 {
				h := mis[c][at[c]]
				v, err := addChan(pix[dest][h.pos%pixels][c], h.d)
				if err != nil {
					return nil, err
				}
				pix[dest][h.pos%pixels][c] = v
				at[c]++
			}
		}
	}
	for c := range channels {
		if at[c] != len(mis[c]) {
			return nil, ErrFormat
		}
	}
	for f := range half {
		pix[f+half] = pix[f]
	}
	for c := range channels {
		if err := readDelta(&r, &pix, c, nHalf[c], kGapH[c], kDeltaH[c], half, baseN); err != nil {
			return nil, err
		}
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return format(pix), nil
}

func readAbs(r *reader, frame *[pixels]rgb, c, count, kPos, kVal int) error {
	pos, err := readGaps(r, count, kPos, pixels)
	if err != nil {
		return err
	}
	for _, p := range pos {
		v, err := r.rice(kVal)
		if err != nil {
			return err
		}
		if v > 255 {
			return ErrFormat
		}
		frame[p][c] = byte(v)
	}
	return nil
}

type hit struct {
	pos int
	d   int
}

func readHits(r *reader, count, kPos, kVal, limit int) ([]hit, error) {
	pos, err := readGaps(r, count, kPos, limit)
	if err != nil {
		return nil, err
	}
	hits := make([]hit, count)
	for i, p := range pos {
		d, err := r.signed(kVal)
		if err != nil {
			return nil, err
		}
		hits[i] = hit{pos: p, d: d}
	}
	return hits, nil
}

// readDelta adds zigzag residuals. origin is the frame index of scan position
// 0, so a second-half residual uses half.
func readDelta(r *reader, pix *[frames][pixels]rgb, c, count, kPos, kVal, origin, limit int) error {
	pos, err := readGaps(r, count, kPos, limit)
	if err != nil {
		return err
	}
	for _, i := range pos {
		d, err := r.signed(kVal)
		if err != nil {
			return err
		}
		f := i/pixels + origin
		p := i % pixels
		if f < 0 || f >= frames {
			return ErrFormat
		}
		v, err := addChan(pix[f][p][c], d)
		if err != nil {
			return err
		}
		pix[f][p][c] = v
	}
	return nil
}

func addChan(v byte, d int) (byte, error) {
	n := int(v) + d
	if uint(n) > 255 {
		return 0, ErrFormat
	}
	return byte(n), nil
}

func motionVecs(pix *[frames][pixels]rgb) [half - 1][nBlock][2]int {
	var vecs [half - 1][nBlock][2]int
	for f := 1; f < half; f++ {
		vecs[f-1] = searchMV(&pix[f-1], &pix[f])
	}
	return vecs
}

// (dx, dy) is how far the block moved. The sample under (x, y) is the
// previous pixel at (x-dx, y-dy); off-frame samples are black.
func predAt(src *[pixels]rgb, vecs [nBlock][2]int, p int) rgb {
	y, x := p/width, p%width
	b := (y/block)*grid + x/block
	dx, dy := vecs[b][0], vecs[b][1]
	return sample(src, x-dx, y-dy)
}

func warp(src *[pixels]rgb, mv [nBlock][2]int) [pixels]rgb {
	var dst [pixels]rgb
	for p := range dst {
		dst[p] = predAt(src, mv, p)
	}
	return dst
}

func sample(src *[pixels]rgb, x, y int) rgb {
	if uint(x) >= width || uint(y) >= height {
		return rgb{}
	}
	return src[y*width+x]
}

// luma is the integer Rec.601 value the motion search matches on.
func luma(p rgb) int {
	return (19595*int(p[0]) + 38470*int(p[1]) + 7471*int(p[2]) + 32768) >> 16
}

func searchMV(prev, cur *[pixels]rgb) [nBlock][2]int {
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

func blockMiss(prev, cur *[pixels]rgb, bx, by, dx, dy int) int {
	mis := 0
	for y := by; y < by+block; y++ {
		for x := bx; x < bx+block; x++ {
			p := sample(prev, x-dx, y-dy)
			c := cur[y*width+x]
			if (luma(p) > 1) != (luma(c) > 1) {
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

func (r *reader) signed(k int) (int, error) {
	n, err := r.rice(k)
	if err != nil {
		return 0, err
	}
	return unzig(n), nil
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

func parse(src []byte) ([frames][pixels]rgb, error) {
	var pix [frames][pixels]rgb
	if len(src) == 0 || src[len(src)-1] != '\n' {
		return pix, ErrFormat
	}
	lines := bytes.Split(src[:len(src)-1], []byte("\n"))
	if len(lines) != 1+frames || string(lines[0]) != head {
		return pix, ErrFormat
	}
	for f, line := range lines[1:] {
		fields := bytes.Split(line, []byte(","))
		if len(fields) != 1+pixels*channels || string(fields[0]) != timeOf(f) {
			return pix, ErrFormat
		}
		for p := range pixels {
			for c := range channels {
				v, err := parseLevel(fields[1+p*channels+c])
				if err != nil {
					return pix, err
				}
				pix[f][p][c] = byte(v)
			}
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

func format(pix [frames][pixels]rgb) []byte {
	var b bytes.Buffer
	b.WriteString(head)
	b.WriteByte('\n')
	for f := range frames {
		b.WriteString(timeOf(f))
		for p := range pixels {
			for c := range channels {
				b.WriteByte(',')
				b.WriteString(strconv.Itoa(int(pix[f][p][c])))
			}
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
