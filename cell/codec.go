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
	channels = 3
	frames   = 66
	half     = frames / 2
	kMove    = 0
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

// CompressedSize is the length of the blob for the canonical 32x32 cell.csv.
// 204765 bits is the shared-luma motion coding, the picture size, and the 4/1 frame rate.
const CompressedSize = 25596

// ErrFormat reports an input that is not a canonical cell clip or a blob
// this model can decode.
var ErrFormat = errors.New("cell: invalid data")

// bg is the flat field frame 0 is coded against. Blue's mode on the
// canonical clip is 1.
var bg = rgb{0, 0, 1}

// The clip is 66 color frames. The time column is the frame index divided
// by the frame rate, and that rate is stored after the width and the height.
// On the canonical clip the rate is 4/1, so the times are 0, 0.25, 0.5, 0.75.
// Block size, search radius, and the width of the counts are functions of
// the picture size; see geom. Motion is one field for every channel. A zero vector
// is one bit; any other vector is a zigzag Rice pair. The sample under (x, y)
// is the previous pixel at (x-dx, y-dy). Off-frame samples are black, which
// is not the background.
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

// geo is the picture layout shared by the encoder and the decoder. It is a
// function of the width and the height only, so it is not stored past those
// two sizes. The block is the largest power of two no bigger than a quarter
// of the short side, and at least 1. The search radius is a quarter of that
// block and at most 8, which keeps a 32x32 frame on 8x8 blocks of radius 2.
// A block larger than 32 is scored on a lattice of block/8 so the search
// stays exhaustive, including its tie breaks, at 32 and at 64. Counts are 16
// bits when both residual scans fit in a uint16, and 32 bits otherwise.
type geo struct {
	w, h      int
	pixels    int
	block     int
	gridX     int
	gridY     int
	nBlock    int
	mvRange   int
	stride    int
	cellMin   int
	baseN     int
	motionN   int
	countBits int
}

func geom(w, h int) (geo, error) {
	if w < 1 || h < 1 || w > 65535 || h > 65535 {
		return geo{}, ErrFormat
	}
	pixels64 := int64(w) * int64(h)
	if pixels64 > int64(^uint32(0))/half {
		return geo{}, ErrFormat
	}
	g := geo{w: w, h: h, pixels: int(pixels64)}
	side := w
	if h < side {
		side = h
	}
	lim := side / 4
	g.block = 1
	for g.block*2 <= lim {
		g.block *= 2
	}
	g.gridX = (w + g.block - 1) / g.block
	g.gridY = (h + g.block - 1) / g.block
	g.nBlock = g.gridX * g.gridY
	g.mvRange = g.block / 4
	if g.mvRange < 1 {
		g.mvRange = 1
	}
	if g.mvRange > 8 {
		g.mvRange = 8
	}
	g.stride = 1
	if g.block > 32 {
		g.stride = g.block / 8
	}
	g.cellMin = 8 * g.pixels / (32 * 32)
	if g.cellMin < 8 {
		g.cellMin = 8
	}
	g.baseN = half * g.pixels
	g.motionN = (half - 1) * g.pixels
	g.countBits = 16
	if g.baseN > 65535 || g.motionN > 65535 {
		g.countBits = 32
	}
	return g, nil
}

func nVec(g geo) int {
	span := 2*g.mvRange + 1
	return span * span
}

func pixelName(y, x, c int) string {
	return fmt.Sprintf("r%dc%d%c", y, x, "rgb"[c])
}

func headerOf(g geo) string {
	var b strings.Builder
	b.WriteString("time")
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			for c := 0; c < channels; c++ {
				b.WriteByte(',')
				b.WriteString(pixelName(y, x, c))
			}
		}
	}
	return b.String()
}

// Compress packs a canonical cell CSV into its fixed-parameter motion coding.
func Compress(src []byte) ([]byte, error) {
	g, fps, pix, err := parse(src)
	if err != nil {
		return nil, err
	}
	var f0, mis, corr [channels]plane
	for c := range channels {
		for p := range g.pixels {
			if pix[0][p][c] != bg[c] {
				f0[c].pos = append(f0[c].pos, p)
				f0[c].val = append(f0[c].val, int(pix[0][p][c]))
			}
		}
	}
	vecs := motionVecs(g, pix)
	for f := 1; f < half; f++ {
		pred := warp(g, pix[f-1], vecs[f-1])
		for p := range g.pixels {
			for c := range channels {
				d := int(pix[f][p][c]) - int(pred[p][c])
				if d == 0 {
					continue
				}
				mis[c].pos = append(mis[c].pos, (f-1)*g.pixels+p)
				mis[c].val = append(mis[c].val, d)
			}
		}
	}
	for f := range half {
		for p := range g.pixels {
			for c := range channels {
				d := int(pix[f+half][p][c]) - int(pix[f][p][c])
				if d == 0 {
					continue
				}
				corr[c].pos = append(corr[c].pos, f*g.pixels+p)
				corr[c].val = append(corr[c].val, d)
			}
		}
	}
	var w bits
	w.bits(g.w, 16)
	w.bits(g.h, 16)
	w.bits(fps.num, 32)
	w.bits(fps.den, 32)
	for c := range channels {
		w.bits(len(f0[c].pos), g.countBits)
	}
	for c := range channels {
		w.bits(len(mis[c].pos), g.countBits)
	}
	for c := range channels {
		w.bits(len(corr[c].pos), g.countBits)
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

// Predict extends a compressed clip by 33 frames.
//
// Each block's motion vector is a Markov chain learned from the clip.
// A draw proposes one vector per block; the field actually used is the
// stored motion field closest to that proposal. Each cell is a plane-stress
// mesh. Drag pulls its nodes toward the velocity of the block they sit in,
// and the element stress resists the part of that field that would shear
// the cell apart.
func Predict(blob []byte) ([][]rgb, error) {
	raw, err := Decompress(blob)
	if err != nil {
		return nil, err
	}
	g, _, pix, err := parse(raw)
	if err != nil {
		return nil, err
	}
	last := pix[frames-1]
	bodies := components(g, last)
	meshes := make([]feaMesh, len(bodies))
	for i := range bodies {
		meshes[i] = meshOf(g, last, &bodies[i])
	}
	trans, marg, fields := learnMotion(g, pix)
	state := fields[len(fields)-1]
	rng := lcg(1)
	out := make([][]rgb, half)
	zero := vectorID(g, 0, 0)
	for f := range out {
		prop := make([][2]int, g.nBlock)
		for b := range prop {
			id := vectorID(g, state[b][0], state[b][1])
			id = draw(&rng, trans[id], marg, zero)
			prop[b][0], prop[b][1] = idVector(g, id)
		}
		mv := nearestField(fields, prop)
		for i := range meshes {
			meshes[i].advanceDriven(1, mv)
		}
		out[f] = raster(g, meshes)
		state = mv
	}
	return out, nil
}

func learnMotion(g geo, pix [][]rgb) ([][]int, []int, [][][2]int) {
	n := nVec(g)
	trans := make([][]int, n)
	for i := range trans {
		trans[i] = make([]int, n)
	}
	marg := make([]int, n)
	fields := make([][][2]int, 0, frames-1)
	prev := make([]int, g.nBlock)
	seen := false
	for f := 1; f < frames; f++ {
		mv := searchMV(g, pix[f-1], pix[f])
		fields = append(fields, mv)
		for b, v := range mv {
			id := vectorID(g, v[0], v[1])
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

func nearestField(fields [][][2]int, prop [][2]int) [][2]int {
	best := 0
	bestH, bestL := len(prop)+1, 0
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
	out := make([][2]int, len(fields[best]))
	copy(out, fields[best])
	return out
}

func vectorID(g geo, dx, dy int) int {
	span := 2*g.mvRange + 1
	return (dy+g.mvRange)*span + (dx + g.mvRange)
}

func idVector(g geo, id int) (int, int) {
	span := 2*g.mvRange + 1
	return id%span - g.mvRange, id/span - g.mvRange
}

// lcg is the fixed generator for the forecast. The multiplier is the
// Numerical Recipes constant; the low bits are not used.
type lcg uint64

func (r *lcg) next() uint32 {
	*r = *r*1664525 + 1013904223
	return uint32(*r >> 32)
}

func draw(r *lcg, row, marg []int, zero int) int {
	counts := row
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		counts = marg
		for _, c := range counts {
			total += c
		}
	}
	if total == 0 {
		return zero
	}
	u := int(r.next() % uint32(total))
	for i, c := range counts {
		u -= c
		if u < 0 {
			return i
		}
	}
	return zero
}

// A body is one bright connected cell.
type body struct {
	pix    []int
	cx, cy float64
}

func components(g geo, f []rgb) []body {
	seen := make([]bool, g.pixels)
	var out []body
	for p := range g.pixels {
		if seen[p] || luma(f[p]) <= 1 {
			continue
		}
		pixs := []int{}
		q := []int{p}
		seen[p] = true
		for len(q) > 0 {
			i := q[len(q)-1]
			q = q[:len(q)-1]
			pixs = append(pixs, i)
			x, y := i%g.w, i/g.w
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				if uint(nx) >= uint(g.w) || uint(ny) >= uint(g.h) {
					continue
				}
				j := ny*g.w + nx
				if seen[j] || luma(f[j]) <= 1 {
					continue
				}
				seen[j] = true
				q = append(q, j)
			}
		}
		sx, sy := 0, 0
		for _, i := range pixs {
			sx += i % g.w
			sy += i / g.w
		}
		n := float64(len(pixs))
		out = append(out, body{pix: pixs, cx: float64(sx) / n, cy: float64(sy) / n})
	}
	return out
}

// Decompress restores the exact file bytes written by Compress.
func Decompress(src []byte) ([]byte, error) {
	r := reader{b: src}
	w, err := r.bits(16)
	if err != nil {
		return nil, err
	}
	h, err := r.bits(16)
	if err != nil {
		return nil, err
	}
	num, err := r.bits(32)
	if err != nil {
		return nil, err
	}
	den, err := r.bits(32)
	if err != nil {
		return nil, err
	}
	fps := rate{num: num, den: den}
	if fps.num <= 0 || fps.den <= 0 {
		return nil, ErrFormat
	}
	g, err := geom(w, h)
	if err != nil {
		return nil, err
	}
	r.limit = g.baseN
	r.mvRange = g.mvRange
	var n0, nMis, nHalf [channels]int
	for c := range n0 {
		n, err := r.bits(g.countBits)
		if err != nil {
			return nil, err
		}
		n0[c] = n
	}
	for c := range nMis {
		n, err := r.bits(g.countBits)
		if err != nil {
			return nil, err
		}
		nMis[c] = n
	}
	for c := range nHalf {
		n, err := r.bits(g.countBits)
		if err != nil {
			return nil, err
		}
		nHalf[c] = n
	}
	pix := make([][]rgb, frames)
	pix[0] = make([]rgb, g.pixels)
	for p := range pix[0] {
		pix[0][p] = bg
	}
	for c := range channels {
		if err := readAbs(&r, pix[0], c, n0[c], kGap0[c], kVal0[c], g.pixels); err != nil {
			return nil, err
		}
	}
	vecs := make([][][2]int, half-1)
	for f := range vecs {
		vecs[f] = make([][2]int, g.nBlock)
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
		hits, err := readHits(&r, nMis[c], kGapM[c], kDelta[c], g.motionN)
		if err != nil {
			return nil, err
		}
		mis[c] = hits
	}
	// Each prediction is taken from the frame after its residuals are applied.
	var at [channels]int
	for dest := 1; dest < half; dest++ {
		pix[dest] = warp(g, pix[dest-1], vecs[dest-1])
		for c := range channels {
			for at[c] < len(mis[c]) && mis[c][at[c]].pos/g.pixels == dest-1 {
				h := mis[c][at[c]]
				v, err := addChan(pix[dest][h.pos%g.pixels][c], h.d)
				if err != nil {
					return nil, err
				}
				pix[dest][h.pos%g.pixels][c] = v
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
		// The second half is a corrected copy. Sharing the slice would
		// let those corrections overwrite the first half.
		pix[f+half] = append([]rgb(nil), pix[f]...)
	}
	for c := range channels {
		if err := readDelta(&r, pix, g, c, nHalf[c], kGapH[c], kDeltaH[c], half, g.baseN); err != nil {
			return nil, err
		}
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return format(g, fps, pix), nil
}

func readAbs(r *reader, frame []rgb, c, count, kPos, kVal, limit int) error {
	pos, err := readGaps(r, count, kPos, limit)
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
func readDelta(r *reader, pix [][]rgb, g geo, c, count, kPos, kVal, origin, limit int) error {
	pos, err := readGaps(r, count, kPos, limit)
	if err != nil {
		return err
	}
	for _, i := range pos {
		d, err := r.signed(kVal)
		if err != nil {
			return err
		}
		f := i/g.pixels + origin
		p := i % g.pixels
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

func motionVecs(g geo, pix [][]rgb) [][][2]int {
	vecs := make([][][2]int, half-1)
	for f := 1; f < half; f++ {
		vecs[f-1] = searchMV(g, pix[f-1], pix[f])
	}
	return vecs
}

// (dx, dy) is how far the block moved. The sample under (x, y) is the
// previous pixel at (x-dx, y-dy); off-frame samples are black.
func predAt(g geo, src []rgb, vecs [][2]int, p int) rgb {
	y, x := p/g.w, p%g.w
	b := (y/g.block)*g.gridX + x/g.block
	dx, dy := vecs[b][0], vecs[b][1]
	return sample(g, src, x-dx, y-dy)
}

func warp(g geo, src []rgb, mv [][2]int) []rgb {
	dst := make([]rgb, g.pixels)
	for p := range dst {
		dst[p] = predAt(g, src, mv, p)
	}
	return dst
}

func sample(g geo, src []rgb, x, y int) rgb {
	if uint(x) >= uint(g.w) || uint(y) >= uint(g.h) {
		return rgb{}
	}
	return src[y*g.w+x]
}

// luma is the integer Rec.601 value the motion search matches on.
func luma(p rgb) int {
	return (19595*int(p[0]) + 38470*int(p[1]) + 7471*int(p[2]) + 32768) >> 16
}

func searchMV(g geo, prev, cur []rgb) [][2]int {
	out := make([][2]int, g.nBlock)
	b := 0
	for by := 0; by < g.h; by += g.block {
		for bx := 0; bx < g.w; bx += g.block {
			bw, bh := g.block, g.block
			if bx+bw > g.w {
				bw = g.w - bx
			}
			if by+bh > g.h {
				bh = g.h - by
			}
			bestMis := bw*bh + 1
			var bestDX, bestDY int
			for oy := -g.mvRange; oy <= g.mvRange; oy++ {
				for ox := -g.mvRange; ox <= g.mvRange; ox++ {
					mis := blockMiss(g, prev, cur, bx, by, bw, bh, ox, oy)
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

func blockMiss(g geo, prev, cur []rgb, bx, by, bw, bh, dx, dy int) int {
	mis := 0
	for y := by; y < by+bh; y += g.stride {
		row := y * g.w
		for x := bx; x < bx+bw; x += g.stride {
			p := sample(g, prev, x-dx, y-dy)
			c := cur[row+x]
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
	if v < -r.mvRange || v > r.mvRange {
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

func parse(src []byte) (geo, rate, [][]rgb, error) {
	if len(src) == 0 || src[len(src)-1] != '\n' {
		return geo{}, rate{}, nil, ErrFormat
	}
	lines := bytes.Split(src[:len(src)-1], []byte("\n"))
	if len(lines) != 1+frames {
		return geo{}, rate{}, nil, ErrFormat
	}
	g, err := headerGeo(lines[0])
	if err != nil {
		return geo{}, rate{}, nil, err
	}
	times := make([]string, frames)
	rows := make([][][]byte, frames)
	for f, line := range lines[1:] {
		fields := bytes.Split(line, []byte(","))
		if len(fields) != 1+g.pixels*channels {
			return geo{}, rate{}, nil, ErrFormat
		}
		times[f] = string(fields[0])
		rows[f] = fields
	}
	fps, err := rateFromTimes(times)
	if err != nil {
		return geo{}, rate{}, nil, err
	}
	pix := make([][]rgb, frames)
	for f, fields := range rows {
		pix[f] = make([]rgb, g.pixels)
		for p := range g.pixels {
			for c := range channels {
				v, err := parseLevel(fields[1+p*channels+c])
				if err != nil {
					return geo{}, rate{}, nil, err
				}
				pix[f][p][c] = byte(v)
			}
		}
	}
	return g, fps, pix, nil
}

func headerGeo(line []byte) (geo, error) {
	fields := bytes.Split(line, []byte(","))
	if len(fields) < 1+channels || string(fields[0]) != "time" {
		return geo{}, ErrFormat
	}
	w := 0
	for {
		base := 1 + w*channels
		if base+channels-1 >= len(fields) {
			break
		}
		match := true
		for c := 0; c < channels; c++ {
			if string(fields[base+c]) != pixelName(0, w, c) {
				match = false
				break
			}
		}
		if !match {
			break
		}
		w++
	}
	if w == 0 || (len(fields)-1)%(w*channels) != 0 {
		return geo{}, ErrFormat
	}
	h := (len(fields) - 1) / (w * channels)
	g, err := geom(w, h)
	if err != nil {
		return geo{}, err
	}
	if string(line) != headerOf(g) {
		return geo{}, ErrFormat
	}
	return g, nil
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

func format(g geo, fps rate, pix [][]rgb) []byte {
	var b bytes.Buffer
	b.WriteString(headerOf(g))
	b.WriteByte('\n')
	for f := range frames {
		b.WriteString(frameTime(f, fps))
		for p := range g.pixels {
			for c := range channels {
				b.WriteByte(',')
				b.WriteString(strconv.Itoa(int(pix[f][p][c])))
			}
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// rate is frames per second as the reduced fraction num/den.
type rate struct {
	num, den int
}

// frameTime is the canonical text of frame i. A terminating decimal is
// written the short way, so 4/1 yields 0, 0.25, 0.5, 0.75. Any other
// exact value is the reduced fraction.
func frameTime(i int, fps rate) string {
	if fps.num <= 0 || fps.den <= 0 {
		return ""
	}
	num := int64(i) * int64(fps.den)
	den := int64(fps.num)
	g := gcd64(num, den)
	num /= g
	den /= g
	if den == 1 {
		return strconv.FormatInt(num, 10)
	}
	if text, ok := decimal(num, den); ok {
		return text
	}
	return strconv.FormatInt(num, 10) + "/" + strconv.FormatInt(den, 10)
}

func decimal(num, den int64) (string, bool) {
	d := den
	for d%2 == 0 {
		d /= 2
	}
	for d%5 == 0 {
		d /= 5
	}
	if d != 1 {
		return "", false
	}
	sec := num / den
	rem := num % den
	digits := make([]byte, 0, 8)
	for rem != 0 {
		rem *= 10
		digits = append(digits, byte('0'+rem/den))
		rem %= den
		if len(digits) > 12 {
			return "", false
		}
	}
	return strconv.FormatInt(sec, 10) + "." + string(digits), true
}

func rateFromTimes(times []string) (rate, error) {
	if len(times) < 2 || times[0] != "0" {
		return rate{}, ErrFormat
	}
	n, d, err := parseSeconds(times[1])
	if err != nil || n <= 0 || d <= 0 {
		return rate{}, ErrFormat
	}
	g := gcd64(n, d)
	n /= g
	d /= g
	if d > int64(^uint32(0)) || n > int64(^uint32(0)) {
		return rate{}, ErrFormat
	}
	// Frame 1 is one frame period, so the rate is the reciprocal.
	fps := rate{num: int(d), den: int(n)}
	for i, s := range times {
		if s != frameTime(i, fps) {
			return rate{}, ErrFormat
		}
	}
	return fps, nil
}

func parseSeconds(s string) (int64, int64, error) {
	if slash := strings.IndexByte(s, '/'); slash >= 0 {
		num, err := parseCanonInt(s[:slash])
		if err != nil {
			return 0, 0, err
		}
		den, err := parseCanonInt(s[slash+1:])
		if err != nil || den <= 0 {
			return 0, 0, err
		}
		return reduce(num, den)
	}
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		n, err := parseCanonInt(s)
		if err != nil {
			return 0, 0, err
		}
		return n, 1, nil
	}
	whole, frac := s[:dot], s[dot+1:]
	if len(frac) == 0 || len(frac) > 12 || frac[len(frac)-1] == '0' {
		return 0, 0, ErrFormat
	}
	n, err := parseCanonInt(whole)
	if err != nil {
		return 0, 0, err
	}
	var f int64
	scale := int64(1)
	for _, c := range frac {
		if c < '0' || c > '9' {
			return 0, 0, ErrFormat
		}
		f = f*10 + int64(c-'0')
		scale *= 10
	}
	if n > (1<<62)/scale {
		return 0, 0, ErrFormat
	}
	return reduce(n*scale+f, scale)
}

func parseCanonInt(s string) (int64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, ErrFormat
	}
	n := int64(0)
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrFormat
		}
		n = n*10 + int64(c-'0')
		if n < 0 {
			return 0, ErrFormat
		}
	}
	return n, nil
}

func reduce(n, d int64) (int64, int64, error) {
	if d <= 0 || n < 0 {
		return 0, 0, ErrFormat
	}
	g := gcd64(n, d)
	return n / g, d / g, nil
}

func gcd64(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// centiseconds is the GIF delay for one frame at this rate, rounded to
// the nearest centisecond and at least 1. 4/1 is 25.
func centiseconds(fps rate) int {
	if fps.num <= 0 || fps.den <= 0 {
		return 1
	}
	cs := (200*int64(fps.den) + int64(fps.num)) / (2 * int64(fps.num))
	if cs < 1 {
		return 1
	}
	return int(cs)
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
	b       []byte
	i       int
	cur     byte
	left    int
	limit   int
	mvRange int
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
		if q > r.limit {
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
