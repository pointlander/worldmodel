// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// A block is one velocity, or it contains smaller blocks. The smallest
// block is a pixel. Both sides agree on where a split is even possible:
// the rectangle in the previous frame must contain a bright sample and a
// dark one, and it must be larger than a pixel. A root block also needs
// the smaller class to cover at least an eighth of its area, so a speck
// does not buy a flag. One flag there chooses the split. A tie keeps the
// single velocity.

type rect struct {
	x, y, w, h int
}

type blk struct {
	rect
	split  bool
	dx, dy int
	sub    []blk
}

func tiles(g geo) []rect {
	out := make([]rect, 0, g.nBlock)
	for by := 0; by < g.h; by += g.block {
		for bx := 0; bx < g.w; bx += g.block {
			bw, bh := g.block, g.block
			if bx+bw > g.w {
				bw = g.w - bx
			}
			if by+bh > g.h {
				bh = g.h - by
			}
			out = append(out, rect{bx, by, bw, bh})
		}
	}
	return out
}

// quadrants splits a rectangle into two or four pieces. A side of length
// one is not split. The order is top to bottom, then left to right.
func quadrants(r rect) []rect {
	if r.w < 2 && r.h < 2 {
		return nil
	}
	xs := []rect{{x: r.x, w: r.w}}
	if r.w >= 2 {
		lw := r.w / 2
		xs = []rect{{x: r.x, w: lw}, {x: r.x + lw, w: r.w - lw}}
	}
	ys := []rect{{y: r.y, h: r.h}}
	if r.h >= 2 {
		th := r.h / 2
		ys = []rect{{y: r.y, h: th}, {y: r.y + th, h: r.h - th}}
	}
	out := make([]rect, 0, len(xs)*len(ys))
	for _, yy := range ys {
		for _, xx := range xs {
			out = append(out, rect{xx.x, yy.y, xx.w, yy.h})
		}
	}
	return out
}

func candidate(g geo, prev []rgb, r rect, root bool) bool {
	if r.w < 2 && r.h < 2 {
		return false
	}
	bright, dark := 0, 0
	for y := r.y; y < r.y+r.h; y++ {
		row := y * g.w
		for x := r.x; x < r.x+r.w; x++ {
			if luma(prev[row+x]) > 1 {
				bright++
			} else {
				dark++
			}
		}
	}
	if bright == 0 || dark == 0 {
		return false
	}
	// A root flag is not worth writing for a speck of the other class.
	// Children of a real split may still refine down to a pixel.
	if root {
		minor := bright
		if dark < minor {
			minor = dark
		}
		if minor*8 < r.w*r.h {
			return false
		}
	}
	return true
}

func flatField(g geo, vecs [][2]int) []blk {
	parts := tiles(g)
	out := make([]blk, len(parts))
	for i, r := range parts {
		out[i].rect = r
		if i < len(vecs) {
			out[i].dx, out[i].dy = vecs[i][0], vecs[i][1]
		}
	}
	return out
}

func fitFrame(g geo, prev, cur []rgb, vel *vecChain) []blk {
	parts := tiles(g)
	out := make([]blk, len(parts))
	for i, r := range parts {
		out[i] = fitNode(g, prev, cur, vel, r, true)
	}
	return out
}

func fitNode(g geo, prev, cur []rgb, vel *vecChain, r rect, root bool) blk {
	var dx, dy int
	if root {
		dx, dy = searchRect(g, prev, cur, r)
	} else {
		dx, dy = searchBits(g, prev, cur, r)
	}
	leaf := blk{rect: r, dx: dx, dy: dy}
	if !candidate(g, prev, r, root) {
		return leaf
	}
	// A perfect cheap leaf cannot be beaten by two or more child velocities.
	if resBits(g, prev, cur, r, dx, dy) == 0 && estVec(vel, g, r, dx, dy) <= 1 {
		return leaf
	}
	parts := quadrants(r)
	kids := make([]blk, len(parts))
	split := blk{rect: r, split: true, sub: kids}
	for i, p := range parts {
		kids[i] = fitNode(g, prev, cur, vel, p, false)
	}
	if treeCost(g, prev, cur, vel, split, root) < treeCost(g, prev, cur, vel, leaf, root) {
		return split
	}
	return leaf
}

func treeCost(g geo, prev, cur []rgb, vel *vecChain, b blk, root bool) int {
	if b.split {
		c := 1
		for _, k := range b.sub {
			c += treeCost(g, prev, cur, vel, k, false)
		}
		return c
	}
	c := estVec(vel, g, b.rect, b.dx, b.dy) + resBits(g, prev, cur, b.rect, b.dx, b.dy)
	if candidate(g, prev, b.rect, root) {
		c++
	}
	return c
}

func estVec(v *vecChain, g geo, r rect, dx, dy int) int {
	if v.proposal(r.y*g.w+r.x) == vectorID(g, dx, dy) {
		return 1
	}
	return 1 + vecRawBits(dx, dy)
}

func vecRawBits(dx, dy int) int {
	if dx == 0 && dy == 0 {
		return 1
	}
	return 1 + riceLen(zig(dx), kMove) + riceLen(zig(dy), kMove)
}

func riceLen(n, k int) int {
	return (n >> k) + 1 + k
}

func resBits(g geo, prev, cur []rgb, r rect, dx, dy int) int {
	n := 0
	for y := r.y; y < r.y+r.h; y++ {
		row := y * g.w
		for x := r.x; x < r.x+r.w; x++ {
			p := sample(g, prev, x-dx, y-dy)
			c := cur[row+x]
			for ch := range channels {
				d := int(c[ch]) - int(p[ch])
				if d == 0 {
					continue
				}
				n += riceLen(0, kGapM[ch]) + riceLen(zig(d), kDelta[ch])
			}
		}
	}
	return n
}

// searchBits picks the child velocity by residual bits. The root block keeps
// the bright-mask search, so a block that does not split stays the vector
// the flat field would have coded.
func searchBits(g geo, prev, cur []rgb, r rect) (int, int) {
	best := resBits(g, prev, cur, r, 0, 0)
	var bestDX, bestDY int
	for oy := -g.mvRange; oy <= g.mvRange; oy++ {
		for ox := -g.mvRange; ox <= g.mvRange; ox++ {
			if ox == 0 && oy == 0 {
				continue
			}
			n := resBits(g, prev, cur, r, ox, oy)
			if n < best || (n == best && lessMV(0, ox, oy, 0, bestDX, bestDY)) {
				best = n
				bestDX, bestDY = ox, oy
			}
		}
	}
	return bestDX, bestDY
}

func searchRect(g geo, prev, cur []rgb, r rect) (int, int) {
	bestMis := r.w*r.h + 1
	var bestDX, bestDY int
	for oy := -g.mvRange; oy <= g.mvRange; oy++ {
		for ox := -g.mvRange; ox <= g.mvRange; ox++ {
			mis := blockMiss(g, prev, cur, r.x, r.y, r.w, r.h, ox, oy)
			if lessMV(mis, ox, oy, bestMis, bestDX, bestDY) {
				bestMis = mis
				bestDX, bestDY = ox, oy
			}
		}
	}
	return bestDX, bestDY
}

func writeTree(w *bits, vel *vecChain, g geo, prev []rgb, b blk, root bool) {
	if candidate(g, prev, b.rect, root) {
		if b.split {
			w.bit(1)
		} else {
			w.bit(0)
		}
	}
	if b.split {
		for _, k := range b.sub {
			writeTree(w, vel, g, prev, k, false)
		}
		return
	}
	writeVec(w, vel, g, b.y*g.w+b.x, b.dx, b.dy)
}

func readTree(r *reader, vel *vecChain, g geo, prev []rgb, rc rect, root bool) (blk, error) {
	b := blk{rect: rc}
	if candidate(g, prev, rc, root) {
		flag, err := r.bit()
		if err != nil {
			return blk{}, err
		}
		if flag == 1 {
			parts := quadrants(rc)
			b.split = true
			b.sub = make([]blk, len(parts))
			for i, p := range parts {
				k, err := readTree(r, vel, g, prev, p, false)
				if err != nil {
					return blk{}, err
				}
				b.sub[i] = k
			}
			return b, nil
		}
	}
	dx, dy, err := readVec(r, vel, g, rc.y*g.w+rc.x)
	if err != nil {
		return blk{}, err
	}
	b.dx, b.dy = dx, dy
	return b, nil
}

func noteTree(vel *vecChain, g geo, b blk) {
	if b.split {
		for _, k := range b.sub {
			noteTree(vel, g, k)
		}
		return
	}
	vel.observe(b.y*g.w+b.x, vectorID(g, b.dx, b.dy))
}

func treesZero(trees []blk) bool {
	for _, b := range trees {
		if !nodeZero(b) {
			return false
		}
	}
	return true
}

func nodeZero(b blk) bool {
	if !b.split {
		return b.dx == 0 && b.dy == 0
	}
	for _, k := range b.sub {
		if !nodeZero(k) {
			return false
		}
	}
	return true
}

func meansOf(trees []blk) [][2]float64 {
	out := make([][2]float64, len(trees))
	for i, b := range trees {
		sx, sy, a := coverSum(b)
		if a > 0 {
			out[i] = [2]float64{sx / float64(a), sy / float64(a)}
		}
	}
	return out
}

func coverSum(b blk) (sx, sy float64, a int) {
	if !b.split {
		a = b.w * b.h
		return float64(b.dx * a), float64(b.dy * a), a
	}
	for _, k := range b.sub {
		cx, cy, ca := coverSum(k)
		sx += cx
		sy += cy
		a += ca
	}
	return sx, sy, a
}

func topOf(b blk) [2]int {
	if !b.split {
		return [2]int{b.dx, b.dy}
	}
	sx, sy, a := coverSum(b)
	if a == 0 {
		return [2]int{}
	}
	return [2]int{iround(sx / float64(a)), iround(sy / float64(a))}
}

// blockVel is the root block's motion: the leaf velocity, or the mean of
// the leaves when that block split. The forecast moves a cell by this, so
// pixel-sized leaves do not shear it apart.
func blockVel(g geo, trees []blk, x, y int) (int, int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= g.w {
		x = g.w - 1
	}
	if y >= g.h {
		y = g.h - 1
	}
	bx := x / g.block
	if bx >= g.gridX {
		bx = g.gridX - 1
	}
	by := y / g.block
	if by >= g.gridY {
		by = g.gridY - 1
	}
	v := topOf(trees[by*g.gridX+bx])
	return v[0], v[1]
}

func velAt(g geo, trees []blk, x, y int) (int, int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= g.w {
		x = g.w - 1
	}
	if y >= g.h {
		y = g.h - 1
	}
	bx := x / g.block
	if bx >= g.gridX {
		bx = g.gridX - 1
	}
	by := y / g.block
	if by >= g.gridY {
		by = g.gridY - 1
	}
	return walkVel(trees[by*g.gridX+bx], x, y)
}

func walkVel(b blk, x, y int) (int, int) {
	if !b.split {
		return b.dx, b.dy
	}
	for _, k := range b.sub {
		if x >= k.x && x < k.x+k.w && y >= k.y && y < k.y+k.h {
			return walkVel(k, x, y)
		}
	}
	return b.dx, b.dy
}

func warpTrees(g geo, src []rgb, trees []blk) []rgb {
	dst := make([]rgb, g.pixels)
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			dx, dy := velAt(g, trees, x, y)
			dst[y*g.w+x] = sample(g, src, x-dx, y-dy)
		}
	}
	return dst
}

func eachLeaf(trees []blk, fn func(blk)) {
	var walk func(blk)
	walk = func(b blk) {
		if !b.split {
			fn(b)
			return
		}
		for _, k := range b.sub {
			walk(k)
		}
	}
	for _, b := range trees {
		walk(b)
	}
}

func hasPixel(b blk) bool {
	if !b.split {
		return b.w == 1 && b.h == 1
	}
	for _, k := range b.sub {
		if hasPixel(k) {
			return true
		}
	}
	return false
}

func nearestTrees(fields [][]blk, prop [][2]int) []blk {
	best := 0
	bestH, bestL := len(prop)+1, 0
	for i, f := range fields {
		h, l := 0, 0
		for b := range prop {
			tv := topOf(f[b])
			if tv != prop[b] {
				h++
			}
			l += iabs(tv[0]-prop[b][0]) + iabs(tv[1]-prop[b][1])
		}
		if h < bestH || (h == bestH && l < bestL) {
			bestH, bestL, best = h, l, i
		}
	}
	return fields[best]
}
