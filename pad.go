// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
)

// compressPad transforms src, centers that spectrum in the next power-of-two
// length so both ends are zero residuals, and flate-compresses the inverse
// transform. For a spectrum longer than one bin the new length is double, and
// each wing holds n/2 zeros. Stripping the wings and inverse-transforming
// returns the original bytes.
func compressPad(src []byte) ([]byte, error) {
	spec, err := NTT(src)
	if err != nil {
		return nil, err
	}
	if len(spec) == 0 {
		return nil, nil
	}
	ext, err := padEnds(spec)
	if err != nil {
		return nil, err
	}
	time, err := INTT(ext)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 4*len(time))
	for i, v := range time {
		binary.LittleEndian.PutUint32(raw[4*i:], v)
	}
	var z bytes.Buffer
	w, err := flate.NewWriter(&z, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	out := make([]byte, 12+z.Len())
	binary.LittleEndian.PutUint32(out[0:], uint32(len(src)))
	binary.LittleEndian.PutUint32(out[4:], uint32(len(spec)))
	binary.LittleEndian.PutUint32(out[8:], uint32(len(ext)))
	copy(out[12:], z.Bytes())
	return out, nil
}

// decompressPad is the inverse of compressPad.
func decompressPad(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob) < 12 {
		return nil, errFormat
	}
	nSrc := int(binary.LittleEndian.Uint32(blob[0:]))
	n := int(binary.LittleEndian.Uint32(blob[4:]))
	m := int(binary.LittleEndian.Uint32(blob[8:]))
	wantN, err := paddedLen(nSrc)
	if err != nil || wantN != n || n == 0 {
		return nil, errFormat
	}
	wantM, err := extendedLen(n)
	if err != nil || wantM != m {
		return nil, errFormat
	}
	rest := bytes.NewReader(blob[12:])
	r := flate.NewReader(rest)
	raw, err := io.ReadAll(r)
	r.Close()
	if err != nil || rest.Len() != 0 || len(raw) != 4*m {
		return nil, errFormat
	}
	ext := make([]uint32, m)
	for i := range ext {
		v := binary.LittleEndian.Uint32(raw[4*i:])
		if v >= mod {
			return nil, errFormat
		}
		ext[i] = v
	}
	transform(ext, false)
	left := (m - n) / 2
	for _, v := range ext[:left] {
		if v != 0 {
			return nil, errFormat
		}
	}
	for _, v := range ext[left+n:] {
		if v != 0 {
			return nil, errFormat
		}
	}
	got, err := INTT(ext[left : left+n])
	if err != nil {
		return nil, err
	}
	if len(got) != n {
		return nil, errFormat
	}
	for _, v := range got[nSrc:] {
		if v != 0 {
			return nil, errFormat
		}
	}
	out := make([]byte, nSrc)
	for i := range out {
		if got[i] > 255 {
			return nil, errFormat
		}
		out[i] = byte(got[i])
	}
	return out, nil
}

// padEnds copies spec into the middle of a longer zero residual vector.
func padEnds(spec []uint32) ([]uint32, error) {
	m, err := extendedLen(len(spec))
	if err != nil {
		return nil, err
	}
	ext := make([]uint32, m)
	copy(ext[(m-len(spec))/2:], spec)
	return ext, nil
}

// extendedLen is the next power of two at least two longer than n, so both
// ends of the residual vector gain a zero.
func extendedLen(n int) (int, error) {
	if n <= 0 || n > maxLen || n&(n-1) != 0 {
		return 0, errFormat
	}
	m := n
	for m < n+2 {
		if m > maxLen/2 {
			return 0, errFormat
		}
		m <<= 1
	}
	return m, nil
}
