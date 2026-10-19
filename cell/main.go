// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command cell writes one CSV row per video frame.
//
// ffmpeg decodes the video, scales every frame to 32x32, then converts
// that frame to grayscale. Each row is the frame time in seconds followed
// by the 1024 intensities in row-major order.
package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
)

const (
	width  = 32
	height = 32
	frame  = width * height
)

var ptsTime = regexp.MustCompile(`n:\s*\d+\s+pts:\s*\S+\s+pts_time:([0-9.]+)`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 2 {
		return errors.New("usage: cell [video] [csv]")
	}
	video, err := videoPath(args)
	if err != nil {
		return err
	}
	csvPath := filepath.Join(filepath.Dir(video), "cell.csv")
	if len(args) == 2 {
		csvPath = args[1]
	}
	times, frames, err := framesOf(video)
	if err != nil {
		return err
	}
	if err := writeCSV(csvPath, times, frames); err != nil {
		return err
	}
	fmt.Printf("wrote %d frames to %s\n", len(frames), csvPath)
	return nil
}

func videoPath(args []string) (string, error) {
	if len(args) >= 1 {
		return args[0], nil
	}
	for _, pattern := range []string{"*.mp4", filepath.Join("cell", "*.mp4")} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("more than one video matching %s", pattern)
		}
	}
	return "", errors.New("no mp4 file")
}

// framesOf asks ffmpeg for raw gray frames. The scale filter runs before
// format=gray, so the grayscale values are the 32x32 picture.
func framesOf(video string) (times []string, frames [][]byte, err error) {
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-nostats",
		"-i", video,
		"-vf", "scale=32:32:flags=lanczos,format=gray,showinfo",
		"-fps_mode", "passthrough",
		"-f", "rawvideo",
		"-pix_fmt", "gray",
		"pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	var log bytes.Buffer
	logErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(&log, stderr)
		logErr <- err
	}()
	raw, readErr := io.ReadAll(stdout)
	if err := <-logErr; err != nil && readErr == nil {
		readErr = err
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, nil, readErr
	}
	if waitErr != nil {
		msg := log.String()
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return nil, nil, fmt.Errorf("ffmpeg: %w\n%s", waitErr, msg)
	}
	if len(raw)%frame != 0 {
		return nil, nil, fmt.Errorf("ffmpeg wrote %d bytes, not a whole number of %d-byte frames", len(raw), frame)
	}
	for _, m := range ptsTime.FindAllStringSubmatch(log.String(), -1) {
		times = append(times, m[1])
	}
	n := len(raw) / frame
	if len(times) != n {
		return nil, nil, fmt.Errorf("ffmpeg reported %d timestamps for %d frames", len(times), n)
	}
	frames = make([][]byte, n)
	for i := range frames {
		frames[i] = raw[i*frame : (i+1)*frame]
	}
	return times, frames, nil
}

func writeCSV(path string, times []string, frames [][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	header := make([]string, 1+frame)
	header[0] = "time"
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			header[1+y*width+x] = fmt.Sprintf("r%dc%d", y, x)
		}
	}
	if err := w.Write(header); err != nil {
		return err
	}
	row := make([]string, len(header))
	for i, pixels := range frames {
		row[0] = times[i]
		for j, p := range pixels {
			row[j+1] = strconv.Itoa(int(p))
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return f.Close()
}
