// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"image"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

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
	t.Logf("cell.csv: %d -> %d bytes", len(original), len(compressed))
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
	m := feaMesh{nodes: []feaNode{
		{X: 1, Y: 1, x: 1, y: 1},
		{X: 2, Y: 1, x: 2, y: 1},
		{X: 1, Y: 2, x: 1, y: 2},
	}}
	m.addTri(0, 1, 2)
	var mv [nBlock][2]int
	mv[blockAt(1, 1)] = [2]int{1, 0}
	m.advanceDriven(1, &mv)
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
	if future != again {
		t.Fatal("forecast changed between draws")
	}
	pix, err := parse(original)
	if err != nil {
		t.Fatal(err)
	}
	moved := false
	for f := range future {
		if future[f] != pix[frames-1] {
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
	for _, c := range components(&future[half-1]) {
		if len(c.pix) >= cellMin {
			kept++
		}
	}
	if kept < 3 {
		t.Fatalf("cells lost: %d remain", kept)
	}
	clip := make([][pixels]rgb, 0, frames+half)
	for f := range pix {
		clip = append(clip, pix[f])
	}
	clip = append(clip, future[:]...)
	path := filepath.Join(t.TempDir(), "future.gif")
	if err := writeGIF(path, clip); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != frames+half || len(g.Delay) != frames+half {
		t.Fatalf("gif frames %d delays %d", len(g.Image), len(g.Delay))
	}
	if g.Image[0].Bounds().Dx() != width*gifScale || g.Image[0].Bounds().Dy() != height*gifScale {
		t.Fatalf("gif size %v", g.Image[0].Bounds())
	}
	check := func(frame, x, y int, want rgb) {
		t.Helper()
		if got := gifRGB(g.Image[frame], x*gifScale, y*gifScale); got != want {
			t.Fatalf("frame %d pixel %d,%d = %v, want %v", frame, x, y, got, want)
		}
		if got := gifRGB(g.Image[frame], x*gifScale+gifScale-1, y*gifScale+gifScale-1); got != want {
			t.Fatalf("frame %d pixel %d,%d scale edge = %v, want %v", frame, x, y, got, want)
		}
	}
	check(0, 0, 0, pix[0][0])
	check(frames-1, 4, 4, pix[frames-1][4*width+4])
	check(frames, 4, 4, future[0][4*width+4])
	for _, d := range g.Delay {
		if d != gifDelay {
			t.Fatalf("delay %d", d)
		}
	}
}

func gifRGB(img image.Image, x, y int) rgb {
	r, g, b, _ := img.At(x, y).RGBA()
	return rgb{byte(r >> 8), byte(g >> 8), byte(b >> 8)}
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
