// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
)

const gifDelay = 25 // centiseconds; the clip is four frames per second

func forecast(args []string) error {
	if len(args) > 2 {
		return fmt.Errorf("usage: cell predict [csv] [gif]")
	}
	csvPath := "cell.csv"
	if _, err := os.Stat(csvPath); err != nil {
		csvPath = filepath.Join("cell", "cell.csv")
	}
	if len(args) >= 1 {
		csvPath = args[0]
	}
	src, err := os.ReadFile(csvPath)
	if err != nil {
		return err
	}
	blob, err := Compress(src)
	if err != nil {
		return err
	}
	g, pix, err := parse(src)
	if err != nil {
		return err
	}
	future, err := Predict(blob)
	if err != nil {
		return err
	}
	gifPath := filepath.Join(filepath.Dir(csvPath), gifName(g.w, g.h))
	if len(args) >= 2 {
		gifPath = args[1]
	}
	clip := make([][]rgb, 0, frames+half)
	clip = append(clip, pix...)
	clip = append(clip, future...)
	if err := writeGIF(gifPath, g, clip); err != nil {
		return err
	}
	fmt.Printf("wrote %d source frames and %d predicted frames to %s\n", frames, half, gifPath)
	return nil
}

func gifName(w, h int) string {
	if w == 32 && h == 32 {
		return "future.gif"
	}
	return fmt.Sprintf("future%dx%d.gif", w, h)
}

// displayScale keeps the long side near 256 pixels. A 32x32 frame stays at
// 8, which is the canonical GIF. Full resolution is drawn 1:1.
func displayScale(g geo) int {
	side := g.w
	if g.h > side {
		side = g.h
	}
	s := 256 / side
	if s < 1 {
		s = 1
	}
	return s
}

func writeGIF(path string, g geo, clip [][]rgb) error {
	scale := displayScale(g)
	img := &gif.GIF{LoopCount: 0}
	for f := range clip {
		frame, err := palettedFrame(g, scale, clip[f])
		if err != nil {
			return err
		}
		if f == 0 {
			img.Config = image.Config{
				ColorModel: frame.Palette,
				Width:      g.w * scale,
				Height:     g.h * scale,
			}
		}
		img.Image = append(img.Image, frame)
		img.Delay = append(img.Delay, gifDelay)
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gif.EncodeAll(out, img); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// palettedFrame builds a local palette. Frames with more than 256 colors,
// which a 32x32 frame of this clip does not have, are reduced to 3-3-2.
func palettedFrame(g geo, scale int, frame []rgb) (*image.Paletted, error) {
	src := frame
	if tooManyColors(frame) {
		q := make([]rgb, len(frame))
		for i, c := range frame {
			q[i] = rgb{c[0] & 0xe0, c[1] & 0xe0, c[2] & 0xc0}
		}
		src = q
	}
	index := make(map[rgb]uint8, 256)
	pal := make(color.Palette, 0, 256)
	for _, c := range src {
		if _, ok := index[c]; ok {
			continue
		}
		if len(pal) == 256 {
			return nil, fmt.Errorf("cell: frame has more than 256 colors")
		}
		index[c] = uint8(len(pal))
		pal = append(pal, color.RGBA{R: c[0], G: c[1], B: c[2], A: 255})
	}
	img := image.NewPaletted(image.Rect(0, 0, g.w*scale, g.h*scale), pal)
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			c := index[src[y*g.w+x]]
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetColorIndex(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}
	return img, nil
}

func tooManyColors(frame []rgb) bool {
	seen := make(map[rgb]struct{}, 257)
	for _, c := range frame {
		if _, ok := seen[c]; ok {
			continue
		}
		if len(seen) == 256 {
			return true
		}
		seen[c] = struct{}{}
	}
	return false
}
