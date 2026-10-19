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
	clip := make([][pixels]byte, 0, frames+half)
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

func writeGIF(path string, frames [][pixels]byte) error {
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.Gray{Y: uint8(i)}
	}
	g := &gif.GIF{
		LoopCount: 0,
		Config: image.Config{
			ColorModel: pal,
			Width:      width * gifScale,
			Height:     height * gifScale,
		},
	}
	for f := range frames {
		img := image.NewPaletted(image.Rect(0, 0, width*gifScale, height*gifScale), pal)
		for y := range height {
			for x := range width {
				c := frames[f][y*width+x]
				for dy := range gifScale {
					for dx := range gifScale {
						img.SetColorIndex(x*gifScale+dx, y*gifScale+dy, c)
					}
				}
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
