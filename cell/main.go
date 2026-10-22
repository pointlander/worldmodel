// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command cell writes the compressed clip for a video.
//
// ffmpeg decodes the video. The default picture is 32x32. -size selects 32,
// 64, full, or WxH. full keeps the video's own frame size and does not scale.
// -video selects the file and defaults to DNA cell death and division-L.mp4.
// The frames are the same CSV the codec compresses: the frame time in
// seconds, then the red, green, and blue sample of every pixel. The time is
// the frame index divided by the frame rate reported by the video. The
// command writes that compressed blob.
//
// cell predict reads the blob and writes the original frames, then 33 future
// frames, as a GIF. A Markov chain draws the next motion field, and each
// cell's elastic mesh is integrated under that field.
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
	"strconv"
	"strings"
)

const (
	usageText    = "usage: cell [-size 32|64|full|WxH] [-video FILE] [file]\n       cell predict [file] [gif]"
	defaultVideo = "DNA cell death and division-L.mp4"
)

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
	sizeSpec, videoFlag, rest, err := splitArgs(args)
	if err != nil {
		return err
	}
	video, outArg, err := chooseVideo(videoFlag, rest)
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
	outPath := filepath.Join(filepath.Dir(video), binName(w, h))
	if outArg != "" {
		outPath = outArg
	}
	fps, err := probeRate(video)
	if err != nil {
		return err
	}
	frames, err := framesOf(video, w, h, native)
	if err != nil {
		return err
	}
	times := make([]string, len(frames))
	for i := range times {
		times[i] = frameTime(i, fps)
	}
	raw, err := encodeCSV(w, h, times, frames)
	if err != nil {
		return err
	}
	blob, err := Compress(raw)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, blob, 0644); err != nil {
		return err
	}
	fmt.Printf("wrote %d frames of %dx%d at %d/%d fps to %s (%d bytes)\n", len(frames), w, h, fps.num, fps.den, outPath, len(blob))
	return nil
}

func splitArgs(args []string) (size, video string, rest []string, err error) {
	size = "32"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-size":
			if i+1 >= len(args) {
				return "", "", nil, errors.New(usageText)
			}
			size = args[i+1]
			i++
		case "-video":
			if i+1 >= len(args) {
				return "", "", nil, errors.New(usageText)
			}
			video = args[i+1]
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	return size, video, rest, nil
}

// chooseVideo resolves the input file. -video names it. Otherwise a
// positional path is the file, and with no path the default clip is used.
// One positional beside -video is the output path.
func chooseVideo(videoFlag string, rest []string) (video, outArg string, err error) {
	if videoFlag != "" {
		if len(rest) > 1 {
			return "", "", errors.New(usageText)
		}
		if len(rest) == 1 {
			outArg = rest[0]
		}
		return videoFlag, outArg, nil
	}
	switch len(rest) {
	case 0:
		video, err = defaultVideoPath()
		return video, "", err
	case 1:
		return rest[0], "", nil
	case 2:
		return rest[0], rest[1], nil
	default:
		return "", "", errors.New(usageText)
	}
}

func defaultVideoPath() (string, error) {
	for _, p := range []string{defaultVideo, filepath.Join("cell", defaultVideo)} {
		info, err := os.Stat(p)
		if err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("video %s not found", defaultVideo)
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

func binName(w, h int) string {
	if w == 32 && h == 32 {
		return "cell.bin"
	}
	return fmt.Sprintf("cell%dx%d.bin", w, h)
}

func probeRate(video string) (rate, error) {
	for _, key := range []string{"avg_frame_rate", "r_frame_rate"} {
		out, err := exec.Command("ffprobe",
			"-v", "error",
			"-select_streams", "v:0",
			"-show_entries", "stream="+key,
			"-of", "csv=p=0",
			video,
		).Output()
		if err != nil {
			return rate{}, fmt.Errorf("ffprobe: %w", err)
		}
		s := strings.TrimSpace(string(out))
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		fps, err := parseRate(s)
		if err != nil || fps.num == 0 {
			continue
		}
		return fps, nil
	}
	return rate{}, fmt.Errorf("ffprobe: no frame rate for %s", video)
}

func parseRate(s string) (rate, error) {
	numText, denText, ok := strings.Cut(s, "/")
	if !ok {
		return rate{}, fmt.Errorf("ffprobe: unexpected frame rate %q", s)
	}
	num, err1 := strconv.Atoi(numText)
	den, err2 := strconv.Atoi(denText)
	if err1 != nil || err2 != nil || num <= 0 || den <= 0 || num > 0xffffffff || den > 0xffffffff {
		return rate{}, fmt.Errorf("ffprobe: unexpected frame rate %q", s)
	}
	g := gcd64(int64(num), int64(den))
	return rate{num: int(int64(num) / g), den: int(int64(den) / g)}, nil
}

// framesOf asks ffmpeg for raw rgb24 frames. native leaves the picture at
// the video's own size. Otherwise the scale filter runs before format=rgb24.
func framesOf(video string, w, h int, native bool) (frames [][]byte, err error) {
	vf := fmt.Sprintf("scale=%d:%d:flags=lanczos,format=rgb24", w, h)
	if native {
		vf = "format=rgb24"
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
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
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
		return nil, readErr
	}
	if waitErr != nil {
		msg := log.String()
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return nil, fmt.Errorf("ffmpeg: %w\n%s", waitErr, msg)
	}
	rawFrame := w * h * channels
	if rawFrame == 0 || len(raw)%rawFrame != 0 {
		return nil, fmt.Errorf("ffmpeg wrote %d bytes, not a whole number of %d-byte frames", len(raw), rawFrame)
	}
	n := len(raw) / rawFrame
	frames = make([][]byte, n)
	for i := range frames {
		frames[i] = raw[i*rawFrame : (i+1)*rawFrame]
	}
	return frames, nil
}

func writeCSV(path string, w, h int, times []string, frames [][]byte) error {
	b, err := encodeCSV(w, h, times, frames)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func encodeCSV(w, h int, times []string, frames [][]byte) ([]byte, error) {
	var buf bytes.Buffer
	out := csv.NewWriter(&buf)
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
		return nil, err
	}
	row := make([]string, len(header))
	for i, pixels := range frames {
		row[0] = times[i]
		for j, p := range pixels {
			row[j+1] = strconv.Itoa(int(p))
		}
		if err := out.Write(row); err != nil {
			return nil, err
		}
	}
	out.Flush()
	if err := out.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
