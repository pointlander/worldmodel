// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// A cell is a mesh of linear triangles in plane stress. The rest shape is
// the last observed frame. Lumped mass and explicit Euler integrate the
// nodal forces. Viscous stress uses the same strain operator as the elastic
// stress, so a rigid motion produces no force.
//
// feaDrag is the rate at which a node is pulled toward the velocity of the
// Markov motion field. The element stress balances the part of that field
// that shears the cell, so the strain stays finite.

const (
	young   = 1.0
	poisson = 0.3
	thick   = 1.0
	visc    = 0.4
	feaDt   = 0.05
	feaRho  = 1.0
	feaDrag = 4.0
)

type feaNode struct {
	x, y   float64
	X, Y   float64
	vx, vy float64
	m      float64
	col    rgb
}

type feaElem struct {
	n    [3]int
	b, c [3]float64
	area float64
}

type feaMesh struct {
	g     geo
	nodes []feaNode
	elems []feaElem
}

func meshOf(g geo, frame []rgb, b *body) feaMesh {
	m := feaMesh{g: g, nodes: make([]feaNode, len(b.pix))}
	idx := make(map[int]int, len(b.pix))
	for i, p := range b.pix {
		x, y := float64(p%g.w), float64(p/g.w)
		m.nodes[i] = feaNode{x: x, y: y, X: x, Y: y, col: frame[p]}
		idx[p] = i
	}
	for _, p := range b.pix {
		x, y := p%g.w, p/g.w
		if x+1 >= g.w || y+1 >= g.h {
			continue
		}
		p10, p01, p11 := p+1, p+g.w, p+g.w+1
		i00, ok00 := idx[p]
		i10, ok10 := idx[p10]
		i01, ok01 := idx[p01]
		i11, ok11 := idx[p11]
		switch {
		case ok00 && ok10 && ok01 && ok11:
			m.addTri(i00, i10, i11)
			m.addTri(i00, i11, i01)
		case ok00 && ok10 && ok01:
			m.addTri(i00, i10, i01)
		case ok00 && ok10 && ok11:
			m.addTri(i00, i10, i11)
		case ok00 && ok01 && ok11:
			m.addTri(i00, i01, i11)
		case ok10 && ok01 && ok11:
			m.addTri(i10, i11, i01)
		}
	}
	for i := range m.nodes {
		if m.nodes[i].m == 0 {
			m.nodes[i].m = feaRho
		}
	}
	return m
}

func (m *feaMesh) addTri(i, j, k int) {
	x0, y0 := m.nodes[i].X, m.nodes[i].Y
	x1, y1 := m.nodes[j].X, m.nodes[j].Y
	x2, y2 := m.nodes[k].X, m.nodes[k].Y
	b0, b1, b2 := y1-y2, y2-y0, y0-y1
	twice := b0*x0 + b1*x1 + b2*x2
	if twice < 0 {
		j, k = k, j
		x1, y1, x2, y2 = x2, y2, x1, y1
		b0, b1, b2 = y1-y2, y2-y0, y0-y1
		twice = -twice
	}
	if twice < 1e-8 {
		return
	}
	c0, c1, c2 := x2-x1, x0-x2, x1-x0
	area := 0.5 * twice
	m.elems = append(m.elems, feaElem{
		n:    [3]int{i, j, k},
		b:    [3]float64{b0, b1, b2},
		c:    [3]float64{c0, c1, c2},
		area: area,
	})
	share := feaRho * area * thick / 3
	m.nodes[i].m += share
	m.nodes[j].m += share
	m.nodes[k].m += share
}

// forces returns the internal nodal forces. A positive value on a node is
// the force the stress exerts on the mesh residual; the integrator subtracts
// it, so a stretched node is pulled back.
func (m *feaMesh) forces() (fx, fy []float64) {
	d00 := young / (1 - poisson*poisson)
	d01 := d00 * poisson
	d22 := d00 * (1 - poisson) / 2
	fx = make([]float64, len(m.nodes))
	fy = make([]float64, len(m.nodes))
	for _, e := range m.elems {
		var ex, ey, g float64
		var exd, eyd, gd float64
		for a := range 3 {
			n := &m.nodes[e.n[a]]
			ex += e.b[a] * (n.x - n.X)
			ey += e.c[a] * (n.y - n.Y)
			g += e.c[a]*(n.x-n.X) + e.b[a]*(n.y-n.Y)
			exd += e.b[a] * n.vx
			eyd += e.c[a] * n.vy
			gd += e.c[a]*n.vx + e.b[a]*n.vy
		}
		den := 2 * e.area
		ex /= den
		ey /= den
		g /= den
		exd /= den
		eyd /= den
		gd /= den
		sx := d00*ex + d01*ey + visc*exd
		sy := d01*ex + d00*ey + visc*eyd
		txy := d22*g + visc*gd
		for a := range 3 {
			id := e.n[a]
			fx[id] += thick * (e.b[a]*sx + e.c[a]*txy) / 2
			fy[id] += thick * (e.c[a]*sy + e.b[a]*txy) / 2
		}
	}
	return fx, fy
}

func (m *feaMesh) advance(time float64) {
	m.advanceDriven(time, nil)
}

// advanceDriven integrates the mesh for time frames. When mv is set, drag
// pulls each node toward the motion vector of the block under that node.
func (m *feaMesh) advanceDriven(time float64, mv [][2]int) {
	steps := int(time/feaDt + 0.5)
	for range steps {
		fx, fy := m.forces()
		for i := range m.nodes {
			n := &m.nodes[i]
			ax := -fx[i] / n.m
			ay := -fy[i] / n.m
			if mv != nil {
				b := m.blockAt(n.x, n.y)
				ax += feaDrag * (float64(mv[b][0]) - n.vx)
				ay += feaDrag * (float64(mv[b][1]) - n.vy)
			}
			n.vx += ax * feaDt
			n.vy += ay * feaDt
			n.x += n.vx * feaDt
			n.y += n.vy * feaDt
		}
	}
}

func (m *feaMesh) blockAt(x, y float64) int {
	ix := int(x)
	iy := int(y)
	if ix < 0 {
		ix = 0
	}
	if iy < 0 {
		iy = 0
	}
	if ix >= m.g.w {
		ix = m.g.w - 1
	}
	if iy >= m.g.h {
		iy = m.g.h - 1
	}
	return (iy/m.g.block)*m.g.gridX + ix/m.g.block
}

func raster(g geo, meshes []feaMesh) []rgb {
	frame := make([]rgb, g.pixels)
	for i := range frame {
		frame[i] = bg
	}
	for mi := range meshes {
		m := &meshes[mi]
		for ei := range m.elems {
			paintTri(g, frame, m, &m.elems[ei])
		}
		for i := range m.nodes {
			n := &m.nodes[i]
			ix, iy := iround(n.x), iround(n.y)
			if uint(ix) >= uint(g.w) || uint(iy) >= uint(g.h) {
				continue
			}
			frame[iy*g.w+ix] = n.col
		}
	}
	return frame
}

func paintTri(g geo, frame []rgb, m *feaMesh, e *feaElem) {
	n0 := &m.nodes[e.n[0]]
	n1 := &m.nodes[e.n[1]]
	n2 := &m.nodes[e.n[2]]
	minx := ifloor(min3(n0.x, n1.x, n2.x))
	maxx := iceil(max3(n0.x, n1.x, n2.x))
	miny := ifloor(min3(n0.y, n1.y, n2.y))
	maxy := iceil(max3(n0.y, n1.y, n2.y))
	if minx < 0 {
		minx = 0
	}
	if miny < 0 {
		miny = 0
	}
	if maxx >= g.w {
		maxx = g.w - 1
	}
	if maxy >= g.h {
		maxy = g.h - 1
	}
	den := (n1.y-n2.y)*(n0.x-n2.x) + (n2.x-n1.x)*(n0.y-n2.y)
	if den > -1e-8 && den < 1e-8 {
		return
	}
	for y := miny; y <= maxy; y++ {
		for x := minx; x <= maxx; x++ {
			px, py := float64(x), float64(y)
			a := ((n1.y-n2.y)*(px-n2.x) + (n2.x-n1.x)*(py-n2.y)) / den
			b := ((n2.y-n0.y)*(px-n2.x) + (n0.x-n2.x)*(py-n2.y)) / den
			c := 1 - a - b
			if a < -1e-6 || b < -1e-6 || c < -1e-6 {
				continue
			}
			switch {
			case a >= b && a >= c:
				frame[y*g.w+x] = n0.col
			case b >= c:
				frame[y*g.w+x] = n1.col
			default:
				frame[y*g.w+x] = n2.col
			}
		}
	}
}

func iround(x float64) int {
	if x >= 0 {
		return int(x + 0.5)
	}
	return int(x - 0.5)
}

func ifloor(x float64) int {
	n := int(x)
	if float64(n) > x {
		n--
	}
	return n
}

func iceil(x float64) int {
	n := int(x)
	if float64(n) < x {
		n++
	}
	return n
}

func min3(a, b, c float64) float64 {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func max3(a, b, c float64) float64 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}
