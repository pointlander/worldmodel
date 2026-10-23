// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func forecast(args []string) error {
	if len(args) > 2 {
		return fmt.Errorf("usage: cell predict [file] [webm]")
	}
	path := "cell.bin"
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("cell", "cell.bin")
	}
	if len(args) >= 1 {
		path = args[0]
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	raw, err := Decompress(blob)
	if err != nil {
		return err
	}
	g, fps, pix, err := parse(raw)
	if err != nil {
		return err
	}
	future, err := Predict(blob)
	if err != nil {
		return err
	}
	webmPath := filepath.Join(filepath.Dir(path), videoName(g.w, g.h))
	if len(args) >= 2 {
		webmPath = args[1]
	}
	clip := make([][]rgb, 0, len(pix)+len(future))
	clip = append(clip, pix...)
	clip = append(clip, future...)
	if err := writeWebM(webmPath, g, clip, fps); err != nil {
		return err
	}
	fmt.Printf("wrote %d source frames and %d predicted frames to %s\n", len(pix), len(future), webmPath)
	return nil
}

const forecastSeconds = 8

// forecastLen is the number of frames that span forecastSeconds at this
// rate, rounded to the nearest frame and at least one.
func forecastLen(fps rate) int {
	if fps.num <= 0 || fps.den <= 0 {
		return 1
	}
	n := (int64(forecastSeconds)*int64(fps.num) + int64(fps.den)/2) / int64(fps.den)
	if n < 1 {
		return 1
	}
	return int(n)
}

func videoName(w, h int) string {
	if w == 32 && h == 32 {
		return "future.webm"
	}
	return fmt.Sprintf("future%dx%d.webm", w, h)
}

// writeWebM pipes the rgb24 frames to ffmpeg. Lossless VP9 in gbrp stores
// those samples, so decoding the file restores them.
func writeWebM(path string, g geo, clip [][]rgb, fps rate) error {
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-nostats",
		"-y",
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", g.w, g.h),
		"-r", fmt.Sprintf("%d/%d", fps.num, fps.den),
		"-i", "pipe:0",
		"-an",
		"-c:v", "libvpx-vp9",
		"-lossless", "1",
		"-pix_fmt", "gbrp",
		"-f", "webm",
		path,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var log bytes.Buffer
	logErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(&log, stderr)
		logErr <- err
	}()
	buf := make([]byte, g.pixels*channels)
	var writeErr error
	for _, frame := range clip {
		if len(frame) != g.pixels {
			writeErr = fmt.Errorf("frame has %d pixels", len(frame))
			break
		}
		for i, p := range frame {
			buf[3*i] = p[0]
			buf[3*i+1] = p[1]
			buf[3*i+2] = p[2]
		}
		if _, err := stdin.Write(buf); err != nil {
			writeErr = err
			break
		}
	}
	if err := stdin.Close(); err != nil && writeErr == nil {
		writeErr = err
	}
	if err := <-logErr; err != nil && writeErr == nil {
		writeErr = err
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := log.String()
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return fmt.Errorf("ffmpeg: %w\n%s", waitErr, msg)
	}
	return writeErr
}
