// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
)

// errWork means the smallest compressed residual file is still above the target.
var errWork = errors.New("ntt: compressed residuals above target")

// proof is a nonce whose forward transform compresses well.
// File is the flate encoding of that spectrum, little-endian residues.
type proof struct {
	Nonce uint32
	File  []byte
}

// mine appends each nonce in 0..limit-1 to data, takes the forward transform,
// and keeps the nonce with the smallest compressed residual file. A proof
// whose file is longer than target is rejected.
func mine(data []byte, target, limit int) (proof, error) {
	if target <= 0 || limit <= 0 || limit > 1<<16 {
		return proof{}, errFormat
	}
	var best proof
	for nonce := range limit {
		file, err := residualFile(data, uint32(nonce))
		if err != nil {
			return proof{}, err
		}
		if best.File == nil || len(file) < len(best.File) {
			best = proof{Nonce: uint32(nonce), File: file}
		}
	}
	if len(best.File) > target {
		return best, errWork
	}
	return best, nil
}

// verify checks that pr.File is the compressed forward transform of data
// followed by pr.Nonce, and that the file meets target.
func verify(data []byte, pr proof, target int) error {
	if target <= 0 {
		return errFormat
	}
	file, err := residualFile(data, pr.Nonce)
	if err != nil {
		return err
	}
	if !bytes.Equal(file, pr.File) {
		return errFormat
	}
	if len(file) > target {
		return errWork
	}
	return nil
}

// residualFile is the flate encoding of the forward transform of data with a
// four-byte little-endian nonce appended.
func residualFile(data []byte, nonce uint32) ([]byte, error) {
	buf := make([]byte, len(data)+4)
	copy(buf, data)
	binary.LittleEndian.PutUint32(buf[len(data):], nonce)
	spec, err := NTT(buf)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 4*len(spec))
	for i, v := range spec {
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
	return z.Bytes(), nil
}
