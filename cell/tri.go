// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// A matWarp frame cuts every root block along one diagonal. The bit chooses
// the diagonal. A triangle of at least four pixels may split into the four
// triangles formed by its edge midpoints; a tie keeps the unsplit triangle
// and the first diagonal. Empty children are not written: both sides assign
// pixels from the same vertices. A block larger than 32 stays the two root
// triangles and is scored on the lattice, which is the exhaustive search at
// every smaller block.

type tri struct {
	ax, ay, bx, by, cx, cy float64
	pix                    []int
	dx, dy                 int
	origin                 int
	cost                   int
	sub                    []*tri
}

type triFrame struct {
	flip []bool
	pair [][2]*tri
}

func fitTris(g geo, prev, cur []rgb, vel *vecChain) triFrame {
	parts := tiles(g)
	f := triFrame{flip: make([]bool, len(parts)), pair: make([][2]*tri, len(parts))}
	for i, r := range parts {
		bestCost := -1
		bestFlip := false
		var chosen [2]*tri
		for _, flip := range []bool{false, true} {
			a, b := blockTris(g, r, flip)
			a.fit(g, prev, cur, vel)
			b.fit(g, prev, cur, vel)
			n := a.cost + b.cost
			if bestCost < 0 || n < bestCost {
				bestCost = n
				bestFlip = flip
				chosen = [2]*tri{a, b}
			}
		}
		f.flip[i] = bestFlip
		f.pair[i] = chosen
	}
	return f
}

func blockTris(g geo, r rect, flip bool) (*tri, *tri) {
	x0, y0 := float64(r.x), float64(r.y)
	x1, y1 := float64(r.x+r.w), float64(r.y+r.h)
	var a, b tri
	if !flip {
		a = tri{ax: x0, ay: y0, bx: x1, by: y0, cx: x0, cy: y1}
		b = tri{ax: x1, ay: y1, bx: x1, by: y0, cx: x0, cy: y1}
	} else {
		a = tri{ax: x0, ay: y0, bx: x1, by: y0, cx: x1, cy: y1}
		b = tri{ax: x0, ay: y0, bx: x1, by: y1, cx: x0, cy: y1}
	}
	a.pix = ownedPix(g, r, a, b, true)
	b.pix = ownedPix(g, r, a, b, false)
	return &a, &b
}

func ownedPix(g geo, r rect, a, b tri, first bool) []int {
	var out []int
	for y := r.y; y < r.y+r.h; y++ {
		for x := r.x; x < r.x+r.w; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			as := baryMin(px, py, a.ax, a.ay, a.bx, a.by, a.cx, a.cy)
			bs := baryMin(px, py, b.ax, b.ay, b.bx, b.by, b.cx, b.cy)
			if first {
				if as >= bs {
					out = append(out, y*g.w+x)
				}
			} else if bs > as {
				out = append(out, y*g.w+x)
			}
		}
	}
	return out
}

func (t *tri) fit(g geo, prev, cur []rgb, vel *vecChain) {
	t.leaf(g, prev, cur, vel)
	if g.stride != 1 || len(t.pix) < 4 {
		return
	}
	kids := t.quarter(g)
	split := 1
	for _, k := range kids {
		if len(k.pix) == 0 {
			continue
		}
		k.fit(g, prev, cur, vel)
		split += k.cost
	}
	if split < t.cost {
		t.sub = kids
		t.cost = split
	}
}

func (t *tri) leaf(g geo, prev, cur []rgb, vel *vecChain) {
	t.cost = t.place(g, prev, cur, vel)
}

func (t *tri) place(g geo, prev, cur []rgb, vel *vecChain) int {
	if len(t.pix) == 0 {
		return 0
	}
	t.origin = t.pix[0]
	for _, p := range t.pix[1:] {
		if p < t.origin {
			t.origin = p
		}
	}
	at := rect{t.origin % g.w, t.origin / g.w, 1, 1}
	t.dx, t.dy = 0, 0
	best := estVec(vel, g, at, 0, 0) + triBits(g, prev, cur, t.pix, 0, 0)
	for oy := -g.mvRange; oy <= g.mvRange; oy++ {
		for ox := -g.mvRange; ox <= g.mvRange; ox++ {
			if ox == 0 && oy == 0 {
				continue
			}
			n := estVec(vel, g, at, ox, oy) + triBits(g, prev, cur, t.pix, ox, oy)
			if n < best || (n == best && lessMV(0, ox, oy, 0, t.dx, t.dy)) {
				best = n
				t.dx, t.dy = ox, oy
			}
		}
	}
	return best
}

func (t *tri) quarter(g geo) []*tri {
	abx, aby := (t.ax+t.bx)/2, (t.ay+t.by)/2
	bcx, bcy := (t.bx+t.cx)/2, (t.by+t.cy)/2
	cax, cay := (t.cx+t.ax)/2, (t.cy+t.ay)/2
	kids := []*tri{
		{ax: t.ax, ay: t.ay, bx: abx, by: aby, cx: cax, cy: cay},
		{ax: t.bx, ay: t.by, bx: bcx, by: bcy, cx: abx, cy: aby},
		{ax: t.cx, ay: t.cy, bx: cax, by: cay, cx: bcx, cy: bcy},
		{ax: abx, ay: aby, bx: bcx, by: bcy, cx: cax, cy: cay},
	}
	for _, p := range t.pix {
		x, y := float64(p%g.w)+0.5, float64(p/g.w)+0.5
		best, score := 0, -1e9
		for i, k := range kids {
			s := baryMin(x, y, k.ax, k.ay, k.bx, k.by, k.cx, k.cy)
			if s > score {
				best, score = i, s
			}
		}
		kids[best].pix = append(kids[best].pix, p)
	}
	return kids
}

func (t *tri) paint(g geo, prev, dst []rgb) {
	if len(t.sub) > 0 {
		for _, k := range t.sub {
			k.paint(g, prev, dst)
		}
		return
	}
	for _, p := range t.pix {
		dst[p] = sample(g, prev, p%g.w-t.dx, p/g.w-t.dy)
	}
}

func (f triFrame) paint(g geo, prev []rgb) []rgb {
	dst := make([]rgb, g.pixels)
	for _, ab := range f.pair {
		ab[0].paint(g, prev, dst)
		ab[1].paint(g, prev, dst)
	}
	return dst
}

func (t *tri) write(w *bits, vel *vecChain, g geo) {
	if g.stride == 1 && len(t.pix) >= 4 {
		if len(t.sub) == 4 {
			w.bit(1)
			for _, k := range t.sub {
				if len(k.pix) == 0 {
					continue
				}
				k.write(w, vel, g)
			}
			return
		}
		w.bit(0)
	}
	if len(t.pix) == 0 {
		return
	}
	writeVec(w, vel, g, t.origin, t.dx, t.dy)
}

func (t *tri) read(r *reader, vel *vecChain, g geo) error {
	if g.stride == 1 && len(t.pix) >= 4 {
		flag, err := r.bit()
		if err != nil {
			return err
		}
		if flag == 1 {
			t.sub = t.quarter(g)
			for _, k := range t.sub {
				if len(k.pix) == 0 {
					continue
				}
				if err := k.read(r, vel, g); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if len(t.pix) == 0 {
		return nil
	}
	t.origin = t.pix[0]
	for _, p := range t.pix[1:] {
		if p < t.origin {
			t.origin = p
		}
	}
	dx, dy, err := readVec(r, vel, g, t.origin)
	if err != nil {
		return err
	}
	t.dx, t.dy = dx, dy
	return nil
}

func (t *tri) note(vel *vecChain, g geo, follow bool) {
	if g.stride == 1 && len(t.pix) >= 4 && len(t.sub) == 4 {
		for _, k := range t.sub {
			if len(k.pix) == 0 {
				continue
			}
			k.note(vel, g, follow)
		}
		return
	}
	if len(t.pix) == 0 {
		return
	}
	id := vectorID(g, t.dx, t.dy)
	if follow {
		vel.follow(t.origin, id)
		return
	}
	vel.observe(t.origin, id)
}

func (t *tri) cover() (sx, sy float64, a int) {
	if len(t.sub) > 0 {
		for _, k := range t.sub {
			cx, cy, ca := k.cover()
			sx += cx
			sy += cy
			a += ca
		}
		return sx, sy, a
	}
	a = len(t.pix)
	return float64(t.dx * a), float64(t.dy * a), a
}

func writeField(w *bits, vel *vecChain, g geo, f triFrame) {
	for i, ab := range f.pair {
		bit := 0
		if f.flip[i] {
			bit = 1
		}
		w.bit(bit)
		ab[0].write(w, vel, g)
		ab[1].write(w, vel, g)
	}
}

func readField(r *reader, vel *vecChain, g geo) (triFrame, error) {
	parts := tiles(g)
	f := triFrame{flip: make([]bool, len(parts)), pair: make([][2]*tri, len(parts))}
	for i, rc := range parts {
		bit, err := r.bit()
		if err != nil {
			return triFrame{}, err
		}
		f.flip[i] = bit == 1
		a, b := blockTris(g, rc, f.flip[i])
		if err := a.read(r, vel, g); err != nil {
			return triFrame{}, err
		}
		if err := b.read(r, vel, g); err != nil {
			return triFrame{}, err
		}
		f.pair[i] = [2]*tri{a, b}
	}
	return f, nil
}

func noteField(vel *vecChain, g geo, f triFrame, follow bool) {
	for _, ab := range f.pair {
		ab[0].note(vel, g, follow)
		ab[1].note(vel, g, follow)
	}
}

func fieldTops(f triFrame) [][2]int {
	out := make([][2]int, len(f.pair))
	for i, ab := range f.pair {
		sx, sy, a := ab[0].cover()
		cx, cy, ca := ab[1].cover()
		sx += cx
		sy += cy
		a += ca
		if a > 0 {
			out[i] = [2]int{iround(sx / float64(a)), iround(sy / float64(a))}
		}
	}
	return out
}

func meansBlocks(g geo, f triFrame) []blk {
	parts := tiles(g)
	tops := fieldTops(f)
	out := make([]blk, len(parts))
	for i, r := range parts {
		out[i].rect = r
		if i < len(tops) {
			out[i].dx, out[i].dy = tops[i][0], tops[i][1]
		}
	}
	return out
}

func triBits(g geo, prev, cur []rgb, pixs []int, dx, dy int) int {
	step := 1
	if g.stride > 1 && len(pixs) > g.stride {
		step = g.stride
	}
	n := 0
	for i := 0; i < len(pixs); i += step {
		p := pixs[i]
		s := sample(g, prev, p%g.w-dx, p/g.w-dy)
		c := cur[p]
		for ch := range channels {
			d := int(c[ch]) - int(s[ch])
			if d == 0 {
				continue
			}
			n += riceLen(0, kGapM[ch]) + riceLen(zig(d), kDelta[ch])
		}
	}
	return n
}

func baryMin(x, y, ax, ay, bx, by, cx, cy float64) float64 {
	den := (by-cy)*(ax-cx) + (cx-bx)*(ay-cy)
	if den < 0 {
		den = -den
		bx, ax = ax, bx
		by, ay = ay, by
	}
	if den == 0 {
		return -1
	}
	a := ((by-cy)*(x-cx) + (cx-bx)*(y-cy)) / den
	b := ((cy-ay)*(x-cx) + (ax-cx)*(y-cy)) / den
	c := 1 - a - b
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
