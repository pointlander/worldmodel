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
//
// The compressor shares one table of laws. A Markov chain picks the law for
// each frame; the numeric values are not written into the blob.

const (
	young   = 1.0
	poisson = 0.3
	thick   = 1.0
	visc    = 0.4
	feaDt   = 0.05
	feaRho  = 1.0
	feaDrag = 4.0
)

// Prediction laws. matWarp is the integer block motion. matSmooth and matHalf
// move the grid nodes by the averaged block velocity. matSoft and matStiff
// integrate that velocity under plane stress. matCopy repeats the frame one
// half-clip earlier, which is the cycle in the 66-frame file.
const (
	matWarp = iota
	matSmooth
	matHalf
	matSoft
	matStiff
	matCopy
	matN
)

// feaLaw is one shared setting of the plane-stress integrator.
type feaLaw struct {
	young, poisson, visc, drag float64
}

func forecastLaw() feaLaw {
	return feaLaw{young, poisson, visc, feaDrag}
}

// lawSoft yields, so a sheared velocity field is pulled back toward a
// smoother motion. lawStiff keeps the coded block velocities.
var (
	lawSoft  = feaLaw{young: 0.25, poisson: 0.45, visc: 0.8, drag: 2}
	lawStiff = feaLaw{young: 4, poisson: 0.3, visc: 0.2, drag: 8}
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
	return m.forcesLaw(forecastLaw())
}

func (m *feaMesh) forcesLaw(law feaLaw) (fx, fy []float64) {
	d00 := law.young / (1 - law.poisson*law.poisson)
	d01 := d00 * law.poisson
	d22 := d00 * (1 - law.poisson) / 2
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
		sx := d00*ex + d01*ey + law.visc*exd
		sy := d01*ex + d00*ey + law.visc*eyd
		txy := d22*g + law.visc*gd
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

// coastLaw integrates nodes whose velocity is already the driving field.
// Drag pulls each node back toward that field while the element stress
// resists the part of the field that shears the mesh. A uniform field has
// no stress, so the node travels by exactly its velocity in one frame.
func (m *feaMesh) coastLaw(time float64, law feaLaw) {
	tx := make([]float64, len(m.nodes))
	ty := make([]float64, len(m.nodes))
	for i := range m.nodes {
		tx[i] = m.nodes[i].vx
		ty[i] = m.nodes[i].vy
	}
	steps := int(time/feaDt + 0.5)
	for range steps {
		fx, fy := m.forcesLaw(law)
		for i := range m.nodes {
			n := &m.nodes[i]
			ax := -fx[i]/n.m + law.drag*(tx[i]-n.vx)
			ay := -fy[i]/n.m + law.drag*(ty[i]-n.vy)
			n.vx += ax * feaDt
			n.vy += ay * feaDt
			n.x += n.vx * feaDt
			n.y += n.vy * feaDt
		}
	}
}

// advanceForecast applies one of the compressor's laws for one frame.
// The rest shape is reset to the current shape first, so strain does not
// accumulate across the forecast. matCopy is not a motion law.
func (m *feaMesh) advanceForecast(law int, mv [][2]int) {
	for i := range m.nodes {
		n := &m.nodes[i]
		n.X, n.Y = n.x, n.y
		n.vx, n.vy = 0, 0
	}
	switch law {
	case matHalf:
		m.translate(mv, 0.5)
	case matSoft:
		m.setVelocity(mv)
		m.coastLaw(1, lawSoft)
	case matStiff:
		m.setVelocity(mv)
		m.coastLaw(1, lawStiff)
	default:
		m.translate(mv, 1)
	}
}

func (m *feaMesh) translate(mv [][2]int, scale float64) {
	for i := range m.nodes {
		n := &m.nodes[i]
		b := m.blockAt(n.x, n.y)
		n.x += scale * float64(mv[b][0])
		n.y += scale * float64(mv[b][1])
	}
}

func (m *feaMesh) setVelocity(mv [][2]int) {
	for i := range m.nodes {
		n := &m.nodes[i]
		b := m.blockAt(n.x, n.y)
		n.vx = float64(mv[b][0])
		n.vy = float64(mv[b][1])
	}
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

// predictFrame is the inter-frame model shared by the encoder and the decoder.
// matCopy reads the frame one half-clip earlier. The other laws deform a grid
// of linear triangles and sample the previous frame through that mesh.
func predictFrame(g geo, pix [][]rgb, t, mat int, vecs [][2]int) []rgb {
	if mat == matCopy {
		return append([]rgb(nil), pix[t-len(pix)/2]...)
	}
	prev := pix[t-1]
	switch mat {
	case matSmooth:
		return warpMesh(g, prev, vecs, 1)
	case matHalf:
		return warpMesh(g, prev, vecs, 0.5)
	case matSoft:
		return warpElastic(g, prev, vecs, lawSoft)
	case matStiff:
		return warpElastic(g, prev, vecs, lawStiff)
	default:
		return warp(g, prev, vecs)
	}
}

func warpMesh(g geo, prev []rgb, vecs [][2]int, scale float64) []rgb {
	vx, vy := nodeVelocity(g, vecs)
	for i := range vx {
		vx[i] *= scale
		vy[i] *= scale
	}
	return sampleMesh(g, prev, vx, vy)
}

func warpElastic(g geo, prev []rgb, vecs [][2]int, law feaLaw) []rgb {
	m := gridMesh(g, vecs)
	m.coastLaw(1, law)
	dx := make([]float64, len(m.nodes))
	dy := make([]float64, len(m.nodes))
	for i, n := range m.nodes {
		dx[i] = n.x - n.X
		dy[i] = n.y - n.Y
	}
	return sampleMesh(g, prev, dx, dy)
}

// gridMesh is the block lattice as two triangles per block. Each node's
// velocity is the mean of the block vectors that meet there.
func gridMesh(g geo, vecs [][2]int) feaMesh {
	nx, ny := g.gridX+1, g.gridY+1
	m := feaMesh{g: g, nodes: make([]feaNode, nx*ny)}
	vx, vy := nodeVelocity(g, vecs)
	for j := 0; j < ny; j++ {
		for i := 0; i < nx; i++ {
			x, y := nodeRest(g, i, j)
			k := j*nx + i
			m.nodes[k] = feaNode{x: x, y: y, X: x, Y: y, vx: vx[k], vy: vy[k]}
		}
	}
	for j := 0; j < g.gridY; j++ {
		for i := 0; i < g.gridX; i++ {
			i00 := j*nx + i
			i10 := i00 + 1
			i01 := i00 + nx
			i11 := i01 + 1
			m.addTri(i00, i10, i11)
			m.addTri(i00, i11, i01)
		}
	}
	for i := range m.nodes {
		if m.nodes[i].m == 0 {
			m.nodes[i].m = feaRho
		}
	}
	return m
}

func nodeRest(g geo, i, j int) (float64, float64) {
	x, y := i*g.block, j*g.block
	if x > g.w {
		x = g.w
	}
	if y > g.h {
		y = g.h
	}
	return float64(x), float64(y)
}

func nodeVelocity(g geo, vecs [][2]int) (vx, vy []float64) {
	nx, ny := g.gridX+1, g.gridY+1
	vx = make([]float64, nx*ny)
	vy = make([]float64, nx*ny)
	cnt := make([]int, nx*ny)
	for j := 0; j < g.gridY; j++ {
		for i := 0; i < g.gridX; i++ {
			dx, dy := float64(vecs[j*g.gridX+i][0]), float64(vecs[j*g.gridX+i][1])
			for _, k := range []int{j*nx + i, j*nx + i + 1, (j+1)*nx + i, (j+1)*nx + i + 1} {
				vx[k] += dx
				vy[k] += dy
				cnt[k]++
			}
		}
	}
	for k := range vx {
		if cnt[k] > 0 {
			vx[k] /= float64(cnt[k])
			vy[k] /= float64(cnt[k])
		}
	}
	return vx, vy
}

// sampleMesh inverse-maps each pixel through the deformed grid. A node moves
// by at most the search radius, so the covering triangle is the pixel's block
// or a neighbor. A pixel that lands in no triangle keeps its previous color.
func sampleMesh(g geo, prev []rgb, dispX, dispY []float64) []rgb {
	nx := g.gridX + 1
	dst := make([]rgb, g.pixels)
	for y := 0; y < g.h; y++ {
		by := y / g.block
		if by >= g.gridY {
			by = g.gridY - 1
		}
		for x := 0; x < g.w; x++ {
			bx := x / g.block
			if bx >= g.gridX {
				bx = g.gridX - 1
			}
			sx, sy, ok := coverBlock(g, nx, dispX, dispY, bx, by, float64(x), float64(y))
			if !ok {
				dst[y*g.w+x] = prev[y*g.w+x]
				continue
			}
			dst[y*g.w+x] = sample(g, prev, iround(sx), iround(sy))
		}
	}
	return dst
}

func coverBlock(g geo, nx int, dispX, dispY []float64, bx, by int, px, py float64) (float64, float64, bool) {
	for dj := -1; dj <= 1; dj++ {
		for di := -1; di <= 1; di++ {
			i, j := bx+di, by+dj
			if i < 0 || j < 0 || i >= g.gridX || j >= g.gridY {
				continue
			}
			if sx, sy, ok := inBlock(g, nx, dispX, dispY, i, j, px, py); ok {
				return sx, sy, true
			}
		}
	}
	return 0, 0, false
}

func inBlock(g geo, nx int, dispX, dispY []float64, i, j int, px, py float64) (float64, float64, bool) {
	k00 := j*nx + i
	k10 := k00 + 1
	k01 := k00 + nx
	k11 := k01 + 1
	r00x, r00y := nodeRest(g, i, j)
	r10x, r10y := nodeRest(g, i+1, j)
	r11x, r11y := nodeRest(g, i+1, j+1)
	r01x, r01y := nodeRest(g, i, j+1)
	d00x, d00y := r00x+dispX[k00], r00y+dispY[k00]
	d10x, d10y := r10x+dispX[k10], r10y+dispY[k10]
	d11x, d11y := r11x+dispX[k11], r11y+dispY[k11]
	d01x, d01y := r01x+dispX[k01], r01y+dispY[k01]
	if sx, sy, ok := bary(d00x, d00y, d10x, d10y, d11x, d11y, r00x, r00y, r10x, r10y, r11x, r11y, px, py); ok {
		return sx, sy, true
	}
	return bary(d00x, d00y, d11x, d11y, d01x, d01y, r00x, r00y, r11x, r11y, r01x, r01y, px, py)
}

func bary(x0, y0, x1, y1, x2, y2, s0, t0, s1, t1, s2, t2, px, py float64) (float64, float64, bool) {
	den := (y1-y2)*(x0-x2) + (x2-x1)*(y0-y2)
	if den > -1e-8 && den < 1e-8 {
		return 0, 0, false
	}
	a := ((y1-y2)*(px-x2) + (x2-x1)*(py-y2)) / den
	b := ((y2-y0)*(px-x2) + (x0-x2)*(py-y2)) / den
	c := 1 - a - b
	if a < -1e-6 || b < -1e-6 || c < -1e-6 {
		return 0, 0, false
	}
	return a*s0 + b*s1 + c*s2, a*t0 + b*t1 + c*t2, true
}
