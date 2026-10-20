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

const (
	gifScale = 8
	gifDelay = 25 // centiseconds; the clip is four frames per second
)

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
	gifPath := filepath.Join(filepath.Dir(csvPath), "future.gif")
	if len(args) >= 2 {
		gifPath = args[1]
	}
	src, err := os.ReadFile(csvPath)
	if err != nil {
		return err
	}
	blob, err := Compress(src)
	if err != nil {
		return err
	}
	pix, err := parse(src)
	if err != nil {
		return err
	}
	future, err := Predict(blob)
	if err != nil {
		return err
	}
	clip := make([][pixels]rgb, 0, frames+half)
	for f := range pix {
		clip = append(clip, pix[f])
	}
	clip = append(clip, future[:]...)
	if err := writeGIF(gifPath, clip); err != nil {
		return err
	}
	fmt.Printf("wrote %d source frames and %d predicted frames to %s\n", frames, half, gifPath)
	return nil
}

func writeGIF(path string, clip [][pixels]rgb) error {
	g := &gif.GIF{LoopCount: 0}
	for f := range clip {
		img, err := palettedFrame(&clip[f])
		if err != nil {
			return err
		}
		if f == 0 {
			g.Config = image.Config{
				ColorModel: img.Palette,
				Width:      width * gifScale,
				Height:     height * gifScale,
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, gifDelay)
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gif.EncodeAll(out, g); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// palettedFrame builds a local palette. A warped frame only copies colors
// from the previous one, and no source frame of this clip exceeds 256.
func palettedFrame(frame *[pixels]rgb) (*image.Paletted, error) {
	index := make(map[rgb]uint8, 256)
	pal := make(color.Palette, 0, 256)
	for p := range frame {
		c := frame[p]
		if _, ok := index[c]; ok {
			continue
		}
		if len(pal) == 256 {
			return nil, fmt.Errorf("cell: frame has more than 256 colors")
		}
		index[c] = uint8(len(pal))
		pal = append(pal, color.RGBA{R: c[0], G: c[1], B: c[2], A: 255})
	}
	img := image.NewPaletted(image.Rect(0, 0, width*gifScale, height*gifScale), pal)
	for y := range height {
		for x := range width {
			c := index[frame[y*width+x]]
			for dy := range gifScale {
				for dx := range gifScale {
					img.SetColorIndex(x*gifScale+dx, y*gifScale+dy, c)
				}
			}
		}
	}
	return img, nil
}
