// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestVideoFlag(t *testing.T) {
	size, video, rest, err := splitArgs([]string{"-video", "other.mp4", "-size", "full", "out.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if size != "full" || video != "other.mp4" || len(rest) != 1 || rest[0] != "out.bin" {
		t.Fatalf("size %q video %q rest %q", size, video, rest)
	}
	got, out, err := chooseVideo(video, rest)
	if err != nil || got != "other.mp4" || out != "out.bin" {
		t.Fatalf("choose %q %q %v", got, out, err)
	}
	got, out, err = chooseVideo("", nil)
	if err != nil || out != "" || filepath.Base(got) != defaultVideo {
		t.Fatalf("default %q %q %v", got, out, err)
	}
	if binName(32, 32) != "cell.bin" || binName(64, 36) != "cell64x36.bin" {
		t.Fatalf("names %s %s", binName(32, 32), binName(64, 36))
	}
	if _, _, err := chooseVideo("", []string{"clip.mp4", "a.bin", "extra"}); err == nil {
		t.Fatal("expected usage error")
	}
	if _, _, _, err := splitArgs([]string{"-video"}); err == nil {
		t.Fatal("expected a video path")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(wd)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	got, err = defaultVideoPath()
	if err != nil || filepath.Base(got) != defaultVideo {
		t.Fatalf("default from parent %q %v", got, err)
	}
}

func TestFrameRate(t *testing.T) {
	fps4 := rate{4, 1}
	if frameTime(0, fps4) != "0" || frameTime(1, fps4) != "0.25" || frameTime(2, fps4) != "0.5" ||
		frameTime(3, fps4) != "0.75" || frameTime(5, fps4) != "1.25" || frameTime(65, fps4) != "16.25" {
		t.Fatalf("4/1 times %q %q %q", frameTime(1, fps4), frameTime(2, fps4), frameTime(65, fps4))
	}
	video, err := defaultVideoPath()
	if err != nil {
		t.Fatal(err)
	}
	probed, err := probeRate(video)
	if err != nil || probed != fps4 {
		t.Fatalf("probed %d/%d %v", probed.num, probed.den, err)
	}
	for _, fps := range []rate{{10, 1}, {24, 1}, {30000, 1001}} {
		g, err := geom(16, 16)
		if err != nil {
			t.Fatal(err)
		}
		pix := make([][]rgb, frames)
		for f := range pix {
			pix[f] = make([]rgb, g.pixels)
			for i := range pix[f] {
				pix[f][i] = bg
			}
			pix[f][0] = rgb{byte(f + 1), 3, 4}
		}
		raw := format(g, fps, pix)
		blob, err := Compress(raw)
		if err != nil {
			t.Fatalf("%d/%d compress: %v", fps.num, fps.den, err)
		}
		back, err := Decompress(blob)
		if err != nil || !bytes.Equal(back, raw) {
			t.Fatalf("%d/%d round trip %v", fps.num, fps.den, err)
		}
		num := int(blob[4])<<24 | int(blob[5])<<16 | int(blob[6])<<8 | int(blob[7])
		den := int(blob[8])<<24 | int(blob[9])<<16 | int(blob[10])<<8 | int(blob[11])
		if num != fps.num || den != fps.den {
			t.Fatalf("stored %d/%d, want %d/%d", num, den, fps.num, fps.den)
		}
	}
	if frameTime(1, rate{24, 1}) != "1/24" || frameTime(1, rate{10, 1}) != "0.1" {
		t.Fatalf("times %s %s", frameTime(1, rate{24, 1}), frameTime(1, rate{10, 1}))
	}
}

func TestRoundTrip(t *testing.T) {
	original, err := os.ReadFile("cell.csv")
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := Compress(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decompress(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("round trip mismatch")
	}
	if len(compressed) != CompressedSize {
		t.Fatalf("compressed %d bytes, want %d", len(compressed), CompressedSize)
	}
	if compressed[0] != 0 || compressed[1] != 32 || compressed[2] != 0 || compressed[3] != 32 {
		t.Fatalf("geometry prefix %x", compressed[:4])
	}
	if !bytes.Equal(compressed[4:12], []byte{0, 0, 0, 4, 0, 0, 0, 1}) {
		t.Fatalf("frame rate %x", compressed[4:12])
	}
	if compressed[12] != 0 || compressed[13] != byte(frames) {
		t.Fatalf("frame count %x", compressed[12:14])
	}
	t.Logf("cell.csv: %d -> %d bytes", len(original), len(compressed))
}

func TestMeshShift(t *testing.T) {
	g, err := geom(32, 32)
	if err != nil {
		t.Fatal(err)
	}
	prev := make([]rgb, g.pixels)
	for i := range prev {
		prev[i] = bg
	}
	prev[g.w+2] = rgb{9, 8, 7}
	vecs := make([][2]int, g.nBlock)
	for i := range vecs {
		vecs[i] = [2]int{1, 0}
	}
	pix := [][]rgb{prev, nil}
	if got := predictFrame(g, pix, 1, matSmooth, vecs); got[g.w+3] != (rgb{9, 8, 7}) {
		t.Fatalf("smooth shift %v", got[g.w+3])
	}
	if got := predictFrame(g, pix, 1, matStiff, vecs); got[g.w+3] != (rgb{9, 8, 7}) {
		t.Fatalf("stiff shift %v", got[g.w+3])
	}
	if got := predictFrame(g, pix, 1, matSoft, vecs); got[g.w+3] != (rgb{9, 8, 7}) {
		t.Fatalf("soft shift %v", got[g.w+3])
	}
}

func TestShortClip(t *testing.T) {
	g, err := geom(8, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{4, 5} {
		pix := make([][]rgb, n)
		for f := range pix {
			row := make([]rgb, g.pixels)
			for i := range row {
				row[i] = bg
			}
			row[f%g.pixels] = rgb{byte(20 + f), 30, 40}
			pix[f] = row
		}
		// An even clip whose second half repeats the first should stay exact.
		if n%2 == 0 {
			for f := n / 2; f < n; f++ {
				pix[f] = append([]rgb(nil), pix[f-n/2]...)
			}
		}
		raw := format(g, rate{30, 1}, pix)
		blob, err := Compress(raw)
		if err != nil {
			t.Fatalf("n=%d compress: %v", n, err)
		}
		if int(blob[12])<<8|int(blob[13]) != n {
			t.Fatalf("n=%d stored %x", n, blob[12:14])
		}
		got, err := Decompress(blob)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("n=%d round trip %v", n, err)
		}
	}
}

func TestPredictFile(t *testing.T) {
	g, err := geom(8, 8)
	if err != nil {
		t.Fatal(err)
	}
	const n = 4
	fps := rate{4, 1}
	pix := make([][]rgb, n)
	times := make([]string, n)
	frames := make([][]byte, n)
	for f := range pix {
		row := make([]rgb, g.pixels)
		rawFrame := make([]byte, g.pixels*channels)
		for i := range row {
			row[i] = bg
		}
		row[0] = rgb{byte(20 + f), 30, 40}
		for i, p := range row {
			rawFrame[3*i] = p[0]
			rawFrame[3*i+1] = p[1]
			rawFrame[3*i+2] = p[2]
		}
		pix[f] = row
		times[f] = frameTime(f, fps)
		frames[f] = rawFrame
	}
	raw := format(g, fps, pix)
	encoded, err := encodeCSV(g.w, g.h, times, frames)
	if err != nil || !bytes.Equal(encoded, raw) {
		t.Fatalf("encodeCSV %v", err)
	}
	blob, err := Compress(raw)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cell.bin")
	if err := os.WriteFile(path, blob, 0644); err != nil {
		t.Fatal(err)
	}
	webmPath := filepath.Join(dir, "future.webm")
	if err := forecast([]string{path, webmPath}); err != nil {
		t.Fatal(err)
	}
	future, err := Predict(blob)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := framesOf(webmPath, g.w, g.h, true)
	if err != nil {
		t.Fatal(err)
	}
	want := make([][]rgb, 0, n+len(future))
	want = append(want, pix...)
	want = append(want, future...)
	if len(decoded) != len(want) {
		t.Fatalf("webm frames %d", len(decoded))
	}
	for i := range want {
		if !bytes.Equal(decoded[i], frameBytes(want[i])) {
			t.Fatalf("frame %d", i)
		}
	}
	got, err := probeRate(webmPath)
	if err != nil || got != fps {
		t.Fatalf("rate %d/%d %v", got.num, got.den, err)
	}
	if err := forecast([]string{"cell.csv", filepath.Join(dir, "bad.webm")}); err == nil {
		t.Fatal("expected a csv to be rejected")
	}
}

func TestVideo01(t *testing.T) {
	const w, h = 64, 36
	video := "01_video.webm"
	if _, err := os.Stat(video); err != nil {
		t.Fatal(err)
	}
	fps, err := probeRate(video)
	if err != nil {
		t.Fatal(err)
	}
	if fps != (rate{30, 1}) {
		t.Fatalf("rate %d/%d", fps.num, fps.den)
	}
	decoded, err := framesOf(video, w, h, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) < 2 {
		t.Fatalf("frames %d", len(decoded))
	}
	times := make([]string, len(decoded))
	for i := range times {
		times[i] = frameTime(i, fps)
	}
	path := filepath.Join(t.TempDir(), "01.csv")
	if err := writeCSV(path, w, h, times, decoded); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := Compress(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decompress(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("round trip %d vs %d", len(got), len(raw))
	}
	again, err := Compress(raw)
	if err != nil || !bytes.Equal(again, blob) {
		t.Fatal("compress is not stable")
	}
	t.Logf("01_video %dx%d %d frames: %d -> %d bytes", w, h, len(decoded), len(raw), len(blob))
}

func TestGeom(t *testing.T) {
	check := func(w, h, block, mv, gx, gy, stride, bits, cellMin int) {
		t.Helper()
		g, err := geom(w, h)
		if err != nil {
			t.Fatal(err)
		}
		if g.block != block || g.mvRange != mv || g.gridX != gx || g.gridY != gy ||
			g.stride != stride || g.countBits != bits || g.cellMin != cellMin ||
			g.nBlock != gx*gy {
			t.Fatalf("%dx%d geo %+v", w, h, g)
		}
	}
	check(32, 32, 8, 2, 4, 4, 1, 16, 8)
	check(64, 64, 16, 4, 4, 4, 1, 32, 32)
	check(16, 16, 4, 1, 4, 4, 1, 16, 8)
	check(762, 590, 128, 8, 6, 5, 16, 32, 3512)
	if _, err := geom(0, 32); err != ErrFormat {
		t.Fatalf("zero width: %v", err)
	}
	if _, err := geom(20000, 20000); err != ErrFormat {
		t.Fatalf("huge frame: %v", err)
	}
}

func TestTriangles(t *testing.T) {
	g, err := geom(40, 30)
	if err != nil {
		t.Fatal(err)
	}
	rects := append(tiles(g), rect{0, 0, 8, 8}, rect{0, 0, 5, 3}, rect{2, 2, 1, 4}, rect{0, 0, 1, 1})
	for _, r := range rects {
		for _, flip := range []bool{false, true} {
			a, b := blockTris(g, r, flip)
			if len(a.pix)+len(b.pix) != r.w*r.h {
				t.Fatalf("cover %+v flip %v %d+%d", r, flip, len(a.pix), len(b.pix))
			}
			seen := make([]bool, g.pixels)
			for _, p := range append(append([]int{}, a.pix...), b.pix...) {
				if seen[p] {
					t.Fatalf("dup pixel %d in %+v", p, r)
				}
				seen[p] = true
			}
		}
	}

	g32, err := geom(32, 32)
	if err != nil {
		t.Fatal(err)
	}
	still := make([]rgb, g32.pixels)
	for i := range still {
		still[i] = bg
	}
	quiet := fitTris(g32, still, still, newVecChain(g32))
	for _, ab := range quiet.pair {
		if len(ab[0].sub) > 0 || len(ab[1].sub) > 0 {
			t.Fatal("uniform frame split a triangle")
		}
	}

	small, err := geom(4, 4)
	if err != nil {
		t.Fatal(err)
	}
	pix := make([][]rgb, 4)
	for f := range pix {
		row := make([]rgb, small.pixels)
		for i := range row {
			row[i] = bg
		}
		row[f%small.pixels] = rgb{byte(20 + f), 30, 40}
		pix[f] = row
	}
	raw := format(small, rate{4, 1}, pix)
	blob, err := Compress(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decompress(blob)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("4x4 round trip %v", err)
	}

	original, err := os.ReadFile("cell.csv")
	if err != nil {
		t.Fatal(err)
	}
	cg, _, frames, err := parse(original)
	if err != nil {
		t.Fatal(err)
	}
	vel := newVecChain(cg)
	laws := newMatChain()
	splits, leaves := 0, 0
	var hist [matN]int
	var walk func(*tri)
	walk = func(n *tri) {
		if len(n.sub) > 0 {
			splits++
			for _, k := range n.sub {
				walk(k)
			}
			return
		}
		if len(n.pix) > 0 {
			leaves++
		}
	}
	for tix := 1; tix < len(frames); tix++ {
		mot, law := fitCoded(cg, frames, tix, vel, laws)
		hist[law]++
		laws.observe(law)
		if law == matCopy {
			continue
		}
		if mot.warp {
			for _, ab := range mot.tris.pair {
				walk(ab[0])
				walk(ab[1])
			}
		}
		observeMotion(vel, cg, mot)
		vel.endFrame()
	}
	if splits == 0 || leaves == 0 {
		t.Fatalf("canonical triangles splits %d leaves %d", splits, leaves)
	}
	t.Logf("triangle splits %d leaves %d laws warp %d smooth %d half %d soft %d stiff %d copy %d",
		splits, leaves, hist[matWarp], hist[matSmooth], hist[matHalf], hist[matSoft], hist[matStiff], hist[matCopy])
}

func TestOtherSizes(t *testing.T) {
	for _, wh := range [][2]int{{16, 16}, {40, 30}, {45, 45}, {64, 64}, {260, 256}} {
		w, h := wh[0], wh[1]
		raw := syntheticClip(w, h)
		blob, err := Compress(raw)
		if err != nil {
			t.Fatalf("%dx%d compress: %v", w, h, err)
		}
		if len(blob) < 4 || int(blob[0])<<8|int(blob[1]) != w || int(blob[2])<<8|int(blob[3]) != h {
			t.Fatalf("%dx%d prefix %x", w, h, blob[:min(4, len(blob))])
		}
		got, err := Decompress(blob)
		if err != nil {
			t.Fatalf("%dx%d decompress: %v", w, h, err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("%dx%d round trip mismatch", w, h)
		}
		g, fps, pix, err := parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if g.w != w || g.h != h || fps != (rate{4, 1}) || len(pix) != frames || len(pix[0]) != w*h {
			t.Fatalf("%dx%d parsed as %dx%d at %d/%d", w, h, g.w, g.h, fps.num, fps.den)
		}
	}
}

func syntheticClip(w, h int) []byte {
	g, err := geom(w, h)
	if err != nil {
		panic(err)
	}
	pix := make([][]rgb, frames)
	for f := range pix {
		row := make([]rgb, g.pixels)
		for i := range row {
			row[i] = bg
		}
		cx := g.block + (f % (g.w / 2))
		cy := g.block/2 + (f % (g.h / 2))
		stamp(g, row, cx, cy, rgb{220, 30, byte(20 + f%50)})
		stamp(g, row, g.w-1, g.h-1, rgb{byte(f + 1), 80, 40})
		pix[f] = row
	}
	return format(g, rate{4, 1}, pix)
}

func stamp(g geo, row []rgb, cx, cy int, col rgb) {
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			x, y := cx+dx, cy+dy
			if uint(x) >= uint(g.w) || uint(y) >= uint(g.h) {
				continue
			}
			row[y*g.w+x] = col
		}
	}
}

func TestTriangleRestores(t *testing.T) {
	m := feaMesh{nodes: []feaNode{
		{X: 0, Y: 0, x: 0, y: 0, m: 1},
		{X: 1, Y: 0, x: 1.1, y: 0, m: 1},
		{X: 0, Y: 1, x: 0, y: 1, m: 1},
	}}
	m.addTri(0, 1, 2)
	fx, fy := m.forces()
	sx, sy := 0.0, 0.0
	for i := range fx {
		sx += fx[i]
		sy += fy[i]
	}
	if sx > 1e-9 || sx < -1e-9 || sy > 1e-9 || sy < -1e-9 {
		t.Fatalf("forces do not balance: %v %v", sx, sy)
	}
	// Node 1 is stretched in +x, so the internal force on it points +x
	// and the integrator pulls it back.
	if fx[1] <= 0 {
		t.Fatalf("stretched node force %v", fx[1])
	}
	want := 5.0 / 91.0
	if fx[1]-want > 1e-9 || want-fx[1] > 1e-9 {
		t.Fatalf("node force %v, want %v", fx[1], want)
	}
	m.nodes[0].x, m.nodes[0].y = 4, -3
	m.nodes[1].x, m.nodes[1].y = 5, -3
	m.nodes[2].x, m.nodes[2].y = 4, -2
	for i := range m.nodes {
		m.nodes[i].vx = 0.2
		m.nodes[i].vy = -0.1
	}
	fx, fy = m.forces()
	for i := range fx {
		if fx[i] > 1e-9 || fx[i] < -1e-9 || fy[i] > 1e-9 || fy[i] < -1e-9 {
			t.Fatalf("rigid motion produced a force %v %v", fx[i], fy[i])
		}
	}
}

func TestFieldTranslates(t *testing.T) {
	g, err := geom(32, 32)
	if err != nil {
		t.Fatal(err)
	}
	m := feaMesh{g: g, nodes: []feaNode{
		{X: 1, Y: 1, x: 1, y: 1},
		{X: 2, Y: 1, x: 2, y: 1},
		{X: 1, Y: 2, x: 1, y: 2},
	}}
	m.addTri(0, 1, 2)
	mv := make([][2]int, g.nBlock)
	mv[m.blockAt(1, 1)] = [2]int{1, 0}
	m.advanceDriven(1, mv)
	for i := range m.nodes {
		got := m.nodes[i].x - m.nodes[i].X
		if got < 0.7 || got > 0.85 {
			t.Fatalf("node %d moved %v", i, got)
		}
	}
	span := m.nodes[1].x - m.nodes[0].x
	if span < 0.95 || span > 1.05 {
		t.Fatalf("edge stretched to %v", span)
	}
}

func TestPredict(t *testing.T) {
	original, err := os.ReadFile("cell.csv")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := Compress(original)
	if err != nil {
		t.Fatal(err)
	}
	future, err := Predict(blob)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Predict(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !sameFrames(future, again) {
		t.Fatal("forecast changed between draws")
	}
	g, fps, pix, err := parse(original)
	if err != nil {
		t.Fatal(err)
	}
	moved := false
	for f := range future {
		if !samePic(future[f], pix[frames-1]) {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("forecast did not move")
	}
	bright, srcBright := 0, 0
	for _, v := range future[half-1] {
		if luma(v) > 1 {
			bright++
		}
	}
	for _, v := range pix[frames-1] {
		if luma(v) > 1 {
			srcBright++
		}
	}
	if bright*5 < srcBright*4 {
		t.Fatalf("cells faded: %d bright pixels, source had %d", bright, srcBright)
	}
	kept := 0
	for _, c := range components(g, future[half-1]) {
		if len(c.pix) >= g.cellMin {
			kept++
		}
	}
	if kept < 3 {
		t.Fatalf("cells lost: %d remain", kept)
	}
	clip := make([][]rgb, 0, frames+half)
	clip = append(clip, pix...)
	clip = append(clip, future...)
	path := filepath.Join(t.TempDir(), "future.webm")
	if err := writeWebM(path, g, clip, fps); err != nil {
		t.Fatal(err)
	}
	decoded, err := framesOf(path, g.w, g.h, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(clip) {
		t.Fatalf("webm frames %d", len(decoded))
	}
	for i := range clip {
		if !bytes.Equal(decoded[i], frameBytes(clip[i])) {
			t.Fatalf("frame %d", i)
		}
	}
	got, err := probeRate(path)
	if err != nil || got != fps {
		t.Fatalf("rate %d/%d %v", got.num, got.den, err)
	}
}

func sameFrames(a, b [][]rgb) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !samePic(a[i], b[i]) {
			return false
		}
	}
	return true
}

func samePic(a, b []rgb) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func frameBytes(frame []rgb) []byte {
	raw := make([]byte, len(frame)*channels)
	for i, p := range frame {
		raw[3*i] = p[0]
		raw[3*i+1] = p[1]
		raw[3*i+2] = p[2]
	}
	return raw
}

func TestRejects(t *testing.T) {
	original, err := os.ReadFile("cell.csv")
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := Compress(original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decompress(nil); err != ErrFormat {
		t.Fatalf("empty: %v", err)
	}
	if _, err := Decompress(compressed[:len(compressed)-1]); err != ErrFormat {
		t.Fatalf("short: %v", err)
	}
	if _, err := Decompress(append(append([]byte{}, compressed...), 0)); err != ErrFormat {
		t.Fatalf("trailing: %v", err)
	}
	flipped := append([]byte{}, compressed...)
	flipped[len(flipped)-1] ^= 1
	if _, err := Decompress(flipped); err != ErrFormat {
		t.Fatalf("padding: %v", err)
	}
	zeroW := append([]byte{}, compressed...)
	zeroW[0], zeroW[1] = 0, 0
	if _, err := Decompress(zeroW); err != ErrFormat {
		t.Fatalf("zero width: %v", err)
	}
	zeroN := append([]byte{}, compressed...)
	zeroN[12], zeroN[13] = 0, 0
	if _, err := Decompress(zeroN); err != ErrFormat {
		t.Fatalf("zero frames: %v", err)
	}
	if _, err := Compress(original[:len(original)-1]); err != ErrFormat {
		t.Fatalf("truncated file: %v", err)
	}
	lines := bytes.Split(bytes.TrimSuffix(original, []byte("\n")), []byte("\n"))
	lines[1] = setLevel(lines[1], "00")
	broken := append(bytes.Join(lines, []byte("\n")), '\n')
	if _, err := Compress(broken); err != ErrFormat {
		t.Fatalf("canonical: %v", err)
	}
}

func setLevel(line []byte, level string) []byte {
	fields := bytes.Split(line, []byte(","))
	fields[1] = []byte(level)
	return bytes.Join(fields, []byte(","))
}
