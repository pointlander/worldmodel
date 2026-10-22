// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// blk is one velocity over a rectangle. matWarp's coded partition is the
// triangles in tri.go. A mesh law stores one of these per root block, and
// the forecast moves a cell by that velocity so a triangle leaf does not
// shear the cell apart.

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

// fitFlat is the mesh-law field: one velocity per root block, chosen by the
// bright-mask search. The block does not split.
func fitFlat(g geo, prev, cur []rgb) []blk {
	parts := tiles(g)
	out := make([]blk, len(parts))
	for i, r := range parts {
		dx, dy := searchRect(g, prev, cur, r)
		out[i] = blk{rect: r, dx: dx, dy: dy}
	}
	return out
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

// blockVel is the velocity stored for the root block under (x, y).
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

func nearestTrees(fields [][]blk, prop [][2]int) int {
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
	return best
}
