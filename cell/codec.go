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
const CompressedSize = 22995

// maxSamples bounds a decoded clip. The full-resolution canonical clip is
// about 30 million samples; a longer file that would not fit is rejected.
const maxSamples = 32 << 20

// ErrFormat reports an input that is not a canonical cell clip or a blob
// this model can decode.
var ErrFormat = errors.New("cell: invalid data")

// bg is the flat field frame 0 is coded against. Blue's mode on the
// canonical clip is 1.
var bg = rgb{0, 0, 1}

// A clip is two or more color frames. The time column is the frame index
// divided by the frame rate. The blob stores the width, the height, the rate,
// and the frame count. Block size and search radius are functions of the
// picture size; see geom. Counts are 16 bits when the pixel count fits in a
// uint16 and 32 bits otherwise.
//
// Frame 0 stores the samples that differ from the background. Each later
// frame is predicted by a finite-element law. A Markov chain, learned from
// the laws and the block velocities already coded, proposes the next law and
// the next velocity of each block. The blob stores only the miss: one bit
// when the proposal is right, otherwise the law id or the velocity. The laws
// are the shared table in fea.go. matCopy, legal in the second half of an
// even-length clip, repeats the paired earlier frame; that is how the
// 66-frame cycle stays cheap.
//
// matWarp cuts each block into two triangles and may split a triangle at the
// midpoints of its edges. The blob stores the diagonal and, when the search
// is exhaustive, a flag for that split. A mesh law stores one velocity per
// block and moves the grid by that velocity. The sample under (x, y) for a
// velocity (dx, dy) is the previous pixel at (x-dx, y-dy). Off-frame samples
// are black, which is not the background.
//
// The residual is the samples that differ from that prediction, a gap into
// the frame and a zigzag Rice value. The three channels are separate streams
// and share the motion field. Copy-frame residuals use the half-frame Rice
// parameters; the other laws use the motion parameters.

type rgb [channels]byte

type plane struct {
	pos []int
	val []int
}

// geo is the picture layout shared by the encoder and the decoder. It is a
// function of the width and the height only, so it is not stored past those
// two sizes. The block is the largest power of two no bigger than a quarter
// of the short side, and at least 1. matWarp cuts that block into triangles.
// The search radius is a quarter of that block and at most 8, which keeps a
// 32x32 frame on 8x8 blocks of radius 2. A block larger than 32 is scored on
// a lattice of block/8 and stays two triangles. Counts are 16
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

// Compress packs a cell CSV. The finite-element law and the block velocities
// of each frame are the symbols of two Markov chains learned from the frames
// already coded.
func Compress(src []byte) ([]byte, error) {
	g, fps, pix, err := parse(src)
	if err != nil {
		return nil, err
	}
	var f0 [channels]plane
	for c := range channels {
		for p := range g.pixels {
			if pix[0][p][c] != bg[c] {
				f0[c].pos = append(f0[c].pos, p)
				f0[c].val = append(f0[c].val, int(pix[0][p][c]))
			}
		}
	}
	bitsN := countWidth(g.pixels)
	var w bits
	w.bits(g.w, 16)
	w.bits(g.h, 16)
	w.bits(fps.num, 32)
	w.bits(fps.den, 32)
	w.bits(len(pix), 16)
	for c := range channels {
		w.bits(len(f0[c].pos), bitsN)
	}
	for c := range channels {
		writeAbs(&w, &f0[c], kGap0[c], kVal0[c])
	}
	laws := newMatChain()
	vel := newVecChain(g)
	for t := 1; t < len(pix); t++ {
		mot, law := fitCoded(g, pix, t, vel, laws)
		writeLaw(&w, laws, law)
		if law != matCopy {
			writeMotion(&w, vel, g, mot)
			vel.endFrame()
		}
		pred := predictMotion(g, pix, t, law, mot)
		mis := frameResidual(pred, pix[t])
		kGap, kDel := kGapM, kDelta
		if law == matCopy {
			kGap, kDel = kGapH, kDeltaH
		}
		for c := range channels {
			w.bits(len(mis[c].pos), bitsN)
		}
		for c := range channels {
			writeDelta(&w, &mis[c], kGap[c], kDel[c])
		}
	}
	return w.finish(), nil
}

func countWidth(n int) int {
	if n > 65535 {
		return 32
	}
	return 16
}

func copyLegal(n, t int) bool {
	return n >= 2 && n%2 == 0 && t >= n/2
}

func frameResidual(pred, cur []rgb) [channels]plane {
	var mis [channels]plane
	for p := range cur {
		for c := range channels {
			d := int(cur[p][c]) - int(pred[p][c])
			if d == 0 {
				continue
			}
			mis[c].pos = append(mis[c].pos, p)
			mis[c].val = append(mis[c].val, d)
		}
	}
	return mis
}

// motion is one coded frame. matWarp carries the triangle partition. A mesh
// law carries one velocity per block. The forecast reads blocks: for a warp
// frame that is the mean of the triangles in the block.
type motion struct {
	tris   triFrame
	blocks []blk
	warp   bool
}

// fitCoded chooses the motion that will be written. matWarp is the triangle
// partition. A mesh law is the flat block search, because the grid moves by
// one velocity per block.
func fitCoded(g geo, pix [][]rgb, t int, vel *vecChain, laws *matChain) (motion, int) {
	tris := fitTris(g, pix[t-1], pix[t], vel)
	flat := fitFlat(g, pix[t-1], pix[t])
	law := pickLaw(g, pix, t, tris, flat, laws)
	m := motion{blocks: flat}
	if law == matWarp {
		m.tris = tris
		m.blocks = meansBlocks(g, tris)
		m.warp = true
	}
	return m, law
}

func pickLaw(g geo, pix [][]rgb, t int, tris triFrame, flat []blk, laws *matChain) int {
	best := laws.proposal()
	if best < 0 || best >= matN || (best == matCopy && !copyLegal(len(pix), t)) {
		best = matWarp
	}
	bestCost := motionCost(g, pix, t, best, tris, flat)
	zero := flatZero(flat)
	for law := range matN {
		if law == best {
			continue
		}
		if law == matCopy && !copyLegal(len(pix), t) {
			continue
		}
		// A zero block field has the same prediction under every mesh law,
		// apart from a law the chain already proposed. Still compare the
		// triangle warp so a noisy elastic step cannot win a stationary frame.
		if zero && law != matCopy && law != matWarp {
			continue
		}
		if c := motionCost(g, pix, t, law, tris, flat); c < bestCost {
			best, bestCost = law, c
		}
	}
	return best
}

func flatZero(blocks []blk) bool {
	for _, b := range blocks {
		if b.dx != 0 || b.dy != 0 {
			return false
		}
	}
	return true
}

func motionCost(g geo, pix [][]rgb, t, law int, tris triFrame, flat []blk) int {
	return sad(pix[t], predictChoice(g, pix, t, law, tris, flat))
}

func predictChoice(g geo, pix [][]rgb, t, law int, tris triFrame, flat []blk) []rgb {
	if law == matCopy {
		return pix[t-len(pix)/2]
	}
	if law == matWarp {
		return tris.paint(g, pix[t-1])
	}
	return predictTrees(g, pix, t, law, flat)
}

func predictMotion(g geo, pix [][]rgb, t, law int, m motion) []rgb {
	if law == matCopy {
		return append([]rgb(nil), pix[t-len(pix)/2]...)
	}
	if m.warp {
		return m.tris.paint(g, pix[t-1])
	}
	return predictTrees(g, pix, t, law, m.blocks)
}

func writeMotion(w *bits, vel *vecChain, g geo, m motion) {
	if m.warp {
		writeField(w, vel, g, m.tris)
		return
	}
	for _, b := range m.blocks {
		writeVec(w, vel, g, b.y*g.w+b.x, b.dx, b.dy)
	}
}

func readMotion(r *reader, vel *vecChain, g geo, law int) (motion, error) {
	if law == matWarp {
		f, err := readField(r, vel, g)
		if err != nil {
			return motion{}, err
		}
		return motion{tris: f, blocks: meansBlocks(g, f), warp: true}, nil
	}
	parts := tiles(g)
	blocks := make([]blk, len(parts))
	for i, rc := range parts {
		dx, dy, err := readVec(r, vel, g, rc.y*g.w+rc.x)
		if err != nil {
			return motion{}, err
		}
		blocks[i] = blk{rect: rc, dx: dx, dy: dy}
	}
	return motion{blocks: blocks}, nil
}

func observeMotion(vel *vecChain, g geo, m motion) {
	if m.warp {
		noteField(vel, g, m.tris, false)
		return
	}
	for _, b := range m.blocks {
		vel.observe(b.y*g.w+b.x, vectorID(g, b.dx, b.dy))
	}
}

func followMotion(vel *vecChain, g geo, m motion) {
	if m.warp {
		noteField(vel, g, m.tris, true)
		return
	}
	eachLeaf(m.blocks, func(b blk) {
		vel.follow(b.y*g.w+b.x, vectorID(g, b.dx, b.dy))
	})
}

func sad(a, b []rgb) int {
	s := 0
	for i := range a {
		for c := range channels {
			d := int(a[i][c]) - int(b[i][c])
			if d < 0 {
				d = -d
			}
			s += d
		}
	}
	return s
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

// Predict extends a compressed clip by forecastLen frames.
//
// The laws and the velocities are the same Markov chains the compressor
// learns. A draw proposes the next law and one velocity per top-level block;
// the stored motion actually used is the one closest to that proposal.
// matCopy repeats an earlier frame of this file, so the forecast draws from
// the other laws. Each bright cell keeps the mesh built from the last frame,
// and that mesh advances under the drawn law.
func Predict(blob []byte) ([][]rgb, error) {
	raw, err := Decompress(blob)
	if err != nil {
		return nil, err
	}
	g, fps, pix, err := parse(raw)
	if err != nil {
		return nil, err
	}
	laws, vel, fields := learnCodec(g, pix)
	last := pix[len(pix)-1]
	bodies := components(g, last)
	meshes := make([]feaMesh, len(bodies))
	for i := range bodies {
		meshes[i] = meshOf(g, last, &bodies[i])
	}
	rng := lcg(1)
	tops := tiles(g)
	out := make([][]rgb, forecastLen(fps))
	for f := range out {
		law := drawLaw(&rng, laws)
		prop := make([][2]int, len(tops))
		for b, r := range tops {
			prop[b][0], prop[b][1] = idVector(g, drawVec(&rng, vel, r.y*g.w+r.x))
		}
		var mot motion
		if len(fields) > 0 {
			mot = nearestMotion(fields, prop)
		} else {
			mot.blocks = flatField(g, prop)
		}
		for i := range meshes {
			meshes[i].advanceForecast(law, mot.blocks)
		}
		out[f] = raster(g, meshes)
		laws.follow(law)
		followMotion(vel, g, mot)
	}
	return out, nil
}

// learnCodec replays the encoder's choices so the forecast chains match the
// chains that were coded. Copy frames have no velocity field.
func learnCodec(g geo, pix [][]rgb) (*matChain, *vecChain, []motion) {
	laws := newMatChain()
	vel := newVecChain(g)
	fields := make([]motion, 0, len(pix)-1)
	for t := 1; t < len(pix); t++ {
		mot, law := fitCoded(g, pix, t, vel, laws)
		laws.observe(law)
		if law == matCopy {
			continue
		}
		fields = append(fields, mot)
		observeMotion(vel, g, mot)
		vel.endFrame()
	}
	return laws, vel, fields
}

func nearestMotion(fields []motion, prop [][2]int) motion {
	blocks := make([][]blk, len(fields))
	for i, f := range fields {
		blocks[i] = f.blocks
	}
	return fields[nearestTrees(blocks, prop)]
}

func drawLaw(r *lcg, m *matChain) int {
	row := m.marg
	if m.seen {
		row = m.trans[m.prev]
	}
	return draw(r, without(row, matCopy), without(m.marg, matCopy), matWarp)
}

func drawVec(r *lcg, v *vecChain, b int) int {
	row := v.marg
	if v.seen {
		row = v.trans[v.prev[b]]
	}
	return draw(r, row, v.marg, v.zero)
}

func without(row []int, ban int) []int {
	out := make([]int, len(row))
	copy(out, row)
	if ban >= 0 && ban < len(out) {
		out[ban] = 0
	}
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

// matChain is the first-order model of the finite-element law. The proposal
// is the mode of the transitions out of the previous law, or the marginal
// when that row is empty. Ties keep the smaller id.
type matChain struct {
	trans [][]int
	marg  []int
	prev  int
	seen  bool
}

func newMatChain() *matChain {
	m := &matChain{trans: make([][]int, matN), marg: make([]int, matN)}
	for i := range m.trans {
		m.trans[i] = make([]int, matN)
	}
	return m
}

func (m *matChain) proposal() int {
	if m.seen {
		if id, ok := modeOf(m.trans[m.prev]); ok {
			return id
		}
	}
	if id, ok := modeOf(m.marg); ok {
		return id
	}
	return matWarp
}

func (m *matChain) observe(id int) {
	m.marg[id]++
	if m.seen {
		m.trans[m.prev][id]++
	}
	m.prev = id
	m.seen = true
}

// follow moves the chain's state without counting the forecast as training.
func (m *matChain) follow(id int) {
	m.prev = id
	m.seen = true
}

// vecChain is the same model on block velocities. A leaf keeps its state at
// the pixel where that leaf starts, so a block and the pixels inside it do
// not share a slot. The first motion frame fills the marginal only; seen
// flips after that frame, not after a fixed number of leaves.
type vecChain struct {
	trans [][]int
	marg  []int
	prev  []int
	zero  int
	seen  bool
}

func newVecChain(g geo) *vecChain {
	n := nVec(g)
	v := &vecChain{
		trans: make([][]int, n),
		marg:  make([]int, n),
		prev:  make([]int, g.pixels),
		zero:  vectorID(g, 0, 0),
	}
	for i := range v.trans {
		v.trans[i] = make([]int, n)
	}
	for i := range v.prev {
		v.prev[i] = v.zero
	}
	return v
}

func (v *vecChain) proposal(origin int) int {
	if v.seen {
		if id, ok := modeOf(v.trans[v.prev[origin]]); ok {
			return id
		}
	}
	if id, ok := modeOf(v.marg); ok {
		return id
	}
	return v.zero
}

func (v *vecChain) observe(origin, id int) {
	v.marg[id]++
	if v.seen {
		v.trans[v.prev[origin]][id]++
	}
	v.prev[origin] = id
}

func (v *vecChain) endFrame() {
	v.seen = true
}

func (v *vecChain) follow(origin, id int) {
	v.prev[origin] = id
}

func modeOf(row []int) (int, bool) {
	best, n := 0, 0
	for i, c := range row {
		n += c
		if c > row[best] {
			best = i
		}
	}
	if n == 0 {
		return 0, false
	}
	return best, true
}

func writeLaw(w *bits, m *matChain, id int) {
	prop := m.proposal()
	if id == prop {
		w.bit(0)
	} else {
		w.bit(1)
		w.rice(zig(id-prop), 0)
	}
	m.observe(id)
}

func readLaw(r *reader, m *matChain) (int, error) {
	flag, err := r.bit()
	if err != nil {
		return 0, err
	}
	id := m.proposal()
	if flag == 1 {
		z, err := r.rice(0)
		if err != nil {
			return 0, err
		}
		id += unzig(z)
	}
	if id < 0 || id >= matN {
		return 0, ErrFormat
	}
	m.observe(id)
	return id, nil
}

func writeVec(w *bits, v *vecChain, g geo, b, dx, dy int) {
	id := vectorID(g, dx, dy)
	if id == v.proposal(b) {
		w.bit(0)
	} else {
		w.bit(1)
		w.vector(dx, dy)
	}
	v.observe(b, id)
}

func readVec(r *reader, v *vecChain, g geo, b int) (int, int, error) {
	flag, err := r.bit()
	if err != nil {
		return 0, 0, err
	}
	var dx, dy int
	if flag == 0 {
		dx, dy = idVector(g, v.proposal(b))
	} else {
		dx, dy, err = r.vector()
		if err != nil {
			return 0, 0, err
		}
	}
	v.observe(b, vectorID(g, dx, dy))
	return dx, dy, nil
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
	n, err := r.bits(16)
	if err != nil {
		return nil, err
	}
	fps := rate{num: num, den: den}
	if fps.num <= 0 || fps.den <= 0 || n < 2 {
		return nil, ErrFormat
	}
	g, err := geom(w, h)
	if err != nil {
		return nil, err
	}
	if int64(n)*int64(g.pixels) > maxSamples {
		return nil, ErrFormat
	}
	r.limit = g.pixels
	r.mvRange = g.mvRange
	bitsN := countWidth(g.pixels)
	var n0 [channels]int
	for c := range n0 {
		v, err := r.bits(bitsN)
		if err != nil {
			return nil, err
		}
		n0[c] = v
	}
	pix := make([][]rgb, n)
	pix[0] = make([]rgb, g.pixels)
	for p := range pix[0] {
		pix[0][p] = bg
	}
	for c := range channels {
		if err := readAbs(&r, pix[0], c, n0[c], kGap0[c], kVal0[c], g.pixels); err != nil {
			return nil, err
		}
	}
	laws := newMatChain()
	vel := newVecChain(g)
	for t := 1; t < n; t++ {
		law, err := readLaw(&r, laws)
		if err != nil {
			return nil, err
		}
		if law == matCopy && !copyLegal(n, t) {
			return nil, ErrFormat
		}
		var mot motion
		if law != matCopy {
			var err error
			mot, err = readMotion(&r, vel, g, law)
			if err != nil {
				return nil, err
			}
			vel.endFrame()
		}
		pix[t] = predictMotion(g, pix, t, law, mot)
		var nMis [channels]int
		for c := range nMis {
			v, err := r.bits(bitsN)
			if err != nil {
				return nil, err
			}
			nMis[c] = v
		}
		kGap, kDel := kGapM, kDelta
		if law == matCopy {
			kGap, kDel = kGapH, kDeltaH
		}
		for c := range channels {
			if err := applyResidual(&r, pix[t], c, nMis[c], kGap[c], kDel[c]); err != nil {
				return nil, err
			}
		}
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return format(g, fps, pix), nil
}

func applyResidual(r *reader, frame []rgb, c, count, kPos, kVal int) error {
	hits, err := readHits(r, count, kPos, kVal, len(frame))
	if err != nil {
		return err
	}
	for _, h := range hits {
		v, err := addChan(frame[h.pos][c], h.d)
		if err != nil {
			return err
		}
		frame[h.pos][c] = v
	}
	return nil
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

func addChan(v byte, d int) (byte, error) {
	n := int(v) + d
	if uint(n) > 255 {
		return 0, ErrFormat
	}
	return byte(n), nil
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

func blockMiss(g geo, prev, cur []rgb, bx, by, bw, bh, dx, dy int) int {
	mis := 0
	step := g.stride
	if step > bw || step > bh {
		step = 1
	}
	for y := by; y < by+bh; y += step {
		row := y * g.w
		for x := bx; x < bx+bw; x += step {
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
	n := len(lines) - 1
	if n < 2 || n > 65535 {
		return geo{}, rate{}, nil, ErrFormat
	}
	g, err := headerGeo(lines[0])
	if err != nil {
		return geo{}, rate{}, nil, err
	}
	if int64(n)*int64(g.pixels) > maxSamples {
		return geo{}, rate{}, nil, ErrFormat
	}
	times := make([]string, n)
	rows := make([][][]byte, n)
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
	pix := make([][]rgb, n)
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
	for f := range pix {
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
