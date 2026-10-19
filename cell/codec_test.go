// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

func TestStayCodes(t *testing.T) {
	for ctx := range stayModel {
		for _, pair := range stayModel[ctx] {
			sym := int(pair[0])
			var w bits
			if err := w.writeStay(ctx, sym); err != nil {
				t.Fatal(err)
			}
			r := reader{b: w.finish()}
			got, err := r.readStay(ctx)
			if err != nil || got != sym {
				t.Fatalf("ctx %d sym %d got %d err %v", ctx, sym, got, err)
			}
			if err := r.done(); err != nil {
				t.Fatalf("ctx %d sym %d tail: %v", ctx, sym, err)
			}
		}
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
	if len(compressed) != CompressedSize {
		t.Fatalf("compressed %d bytes, want %d", len(compressed), CompressedSize)
	}
	got, err := Decompress(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("round trip mismatch")
	}
	t.Logf("cell.csv: %d -> %d bytes", len(original), len(compressed))
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
	bright := 0
	for _, v := range future[half-1] {
		if v > 1 {
			bright++
		}
	}
	if bright < 40 {
		t.Fatalf("cells faded: %d bright pixels", bright)
	}
	clip := make([][pixels]byte, 0, frames+half)
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
	check := func(frame, x, y int, want byte) {
		t.Helper()
		got := g.Image[frame].ColorIndexAt(x*gifScale, y*gifScale)
		if got != want {
			t.Fatalf("frame %d pixel %d,%d = %d, want %d", frame, x, y, got, want)
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
	lines[1] = setLevel(lines[1], "0")
	lines[1+half] = setLevel(lines[1+half], "2")
	broken := append(bytes.Join(lines, []byte("\n")), '\n')
	if _, err := Compress(broken); err != ErrFormat {
		t.Fatalf("level step: %v", err)
	}
}

func setLevel(line []byte, level string) []byte {
	fields := bytes.Split(line, []byte(","))
	fields[1] = []byte(level)
	return bytes.Join(fields, []byte(","))
}
