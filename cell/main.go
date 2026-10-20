// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command cell writes one CSV row per video frame.
//
// ffmpeg decodes the video. The default picture is 32x32. -size selects 32,
// 64, full, or WxH. full keeps the video's own frame size and does not scale.
// Each row is the frame time in seconds followed by the red, green, and blue
// sample of every pixel in row-major order.
//
// cell predict writes the original frames, then 33 future frames, as a GIF.
// A Markov chain draws the next motion field, and each cell's elastic mesh
// is integrated under that field.
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
	"strings"
)

const usageText = "usage: cell [-size 32|64|full|WxH] [video] [csv]\n       cell predict [csv] [gif]"

var ptsTime = regexp.MustCompile(`n:\s*\d+\s+pts:\s*\S+\s+pts_time:([0-9.]+)`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "predict" {
		return forecast(args[1:])
	}
	sizeSpec, rest, err := splitArgs(args)
	if err != nil {
		return err
	}
	if len(rest) > 2 {
		return errors.New(usageText)
	}
	video, err := videoPath(rest)
	if err != nil {
		return err
	}
	w, h, native, err := resolveSize(sizeSpec, video)
	if err != nil {
		return err
	}
	if _, err := geom(w, h); err != nil {
		return fmt.Errorf("size %dx%d is too large to code", w, h)
	}
	csvPath := filepath.Join(filepath.Dir(video), csvName(w, h))
	if len(rest) == 2 {
		csvPath = rest[1]
	}
	times, frames, err := framesOf(video, w, h, native)
	if err != nil {
		return err
	}
	if err := writeCSV(csvPath, w, h, times, frames); err != nil {
		return err
	}
	fmt.Printf("wrote %d frames of %dx%d to %s\n", len(frames), w, h, csvPath)
	return nil
}

func splitArgs(args []string) (string, []string, error) {
	size := "32"
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-size" {
			if i+1 >= len(args) {
				return "", nil, errors.New(usageText)
			}
			size = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return size, rest, nil
}

func resolveSize(spec, video string) (w, h int, native bool, err error) {
	if spec == "full" {
		w, h, err = probeVideo(video)
		return w, h, true, err
	}
	w, h, ok := parseWH(spec)
	if !ok {
		return 0, 0, false, errors.New(usageText)
	}
	return w, h, false, nil
}

func parseWH(spec string) (int, int, bool) {
	if i := strings.IndexAny(spec, "xX"); i > 0 {
		w, err1 := strconv.Atoi(spec[:i])
		h, err2 := strconv.Atoi(spec[i+1:])
		if err1 != nil || err2 != nil || w < 1 || h < 1 {
			return 0, 0, false
		}
		return w, h, true
	}
	n, err := strconv.Atoi(spec)
	if err != nil || n < 1 {
		return 0, 0, false
	}
	return n, n, true
}

func probeVideo(video string) (int, int, error) {
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0:s=x",
		video,
	).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe: %w", err)
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	w, h, ok := parseWH(line)
	if !ok {
		return 0, 0, fmt.Errorf("ffprobe: unexpected size %q", strings.TrimSpace(string(out)))
	}
	return w, h, nil
}

func csvName(w, h int) string {
	if w == 32 && h == 32 {
		return "cell.csv"
	}
	return fmt.Sprintf("cell%dx%d.csv", w, h)
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

// framesOf asks ffmpeg for raw rgb24 frames. native leaves the picture at
// the video's own size. Otherwise the scale filter runs before format=rgb24.
func framesOf(video string, w, h int, native bool) (times []string, frames [][]byte, err error) {
	vf := fmt.Sprintf("scale=%d:%d:flags=lanczos,format=rgb24,showinfo", w, h)
	if native {
		vf = "format=rgb24,showinfo"
	}
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-nostats",
		"-i", video,
		"-vf", vf,
		"-fps_mode", "passthrough",
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
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
	rawFrame := w * h * channels
	if len(raw)%rawFrame != 0 {
		return nil, nil, fmt.Errorf("ffmpeg wrote %d bytes, not a whole number of %d-byte frames", len(raw), rawFrame)
	}
	for _, m := range ptsTime.FindAllStringSubmatch(log.String(), -1) {
		times = append(times, m[1])
	}
	n := len(raw) / rawFrame
	if len(times) != n {
		return nil, nil, fmt.Errorf("ffmpeg reported %d timestamps for %d frames", len(times), n)
	}
	frames = make([][]byte, n)
	for i := range frames {
		frames[i] = raw[i*rawFrame : (i+1)*rawFrame]
	}
	return times, frames, nil
}

func writeCSV(path string, w, h int, times []string, frames [][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	out := csv.NewWriter(f)
	header := make([]string, 1+w*h*channels)
	header[0] = "time"
	i := 1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < channels; c++ {
				header[i] = pixelName(y, x, c)
				i++
			}
		}
	}
	if err := out.Write(header); err != nil {
		return err
	}
	row := make([]string, len(header))
	for i, pixels := range frames {
		row[0] = times[i]
		for j, p := range pixels {
			row[j+1] = strconv.Itoa(int(p))
		}
		if err := out.Write(row); err != nil {
			return err
		}
	}
	out.Flush()
	if err := out.Error(); err != nil {
		return err
	}
	return f.Close()
}
