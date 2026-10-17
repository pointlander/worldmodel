// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
	"math/rand/v2"
	"slices"
)

const (
	evoPop  = 16
	evoGen  = 48
	evoTour = 3
)

// compressEvolve clears a set of spectrum bins and flate-compresses the
// inverse transform. A genetic algorithm chooses the set. Fitness is the
// length of the blob: each cleared bin is stored as an index and its
// original residue, then the inverse samples are compressed. The search is
// seeded with the empty set and the DC-only set, and with PCG(1, 2), so the
// same input always yields the same blob.
func compressEvolve(src []byte) ([]byte, error) {
	spec, err := NTT(src)
	if err != nil {
		return nil, err
	}
	if len(spec) == 0 {
		return nil, nil
	}
	idx, err := evolve(len(src), spec, rand.New(rand.NewPCG(1, 2)))
	if err != nil {
		return nil, err
	}
	return encodeEvolve(len(src), spec, idx)
}

// decompressEvolve is the inverse of compressEvolve.
func decompressEvolve(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob) < 12 {
		return nil, errFormat
	}
	nSrc := int(binary.LittleEndian.Uint32(blob[0:]))
	n := int(binary.LittleEndian.Uint32(blob[4:]))
	k := binary.LittleEndian.Uint32(blob[8:])
	want, err := paddedLen(nSrc)
	if err != nil || want != n || n == 0 {
		return nil, errFormat
	}
	if uint64(k) > uint64(n) || 12+8*uint64(k) > uint64(len(blob)) {
		return nil, errFormat
	}
	pairs := make([]struct {
		i int
		v uint32
	}, k)
	p := 12
	prev := -1
	for i := range pairs {
		ix := int(binary.LittleEndian.Uint32(blob[p:]))
		v := binary.LittleEndian.Uint32(blob[p+4:])
		p += 8
		if ix <= prev || ix >= n || v >= mod {
			return nil, errFormat
		}
		prev = ix
		pairs[i].i = ix
		pairs[i].v = v
	}
	rest := bytes.NewReader(blob[p:])
	r := flate.NewReader(rest)
	raw, err := io.ReadAll(r)
	r.Close()
	if err != nil || rest.Len() != 0 || len(raw) != 4*n {
		return nil, errFormat
	}
	buf := make([]uint32, n)
	for i := range buf {
		v := binary.LittleEndian.Uint32(raw[4*i:])
		if v >= mod {
			return nil, errFormat
		}
		buf[i] = v
	}
	transform(buf, false)
	for _, pair := range pairs {
		if buf[pair.i] != 0 {
			return nil, errFormat
		}
		buf[pair.i] = pair.v
	}
	got, err := INTT(buf)
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

// encodeEvolve writes the blob for one mask. idx lists bins to clear, in
// increasing order.
func encodeEvolve(nSrc int, spec []uint32, idx []int) ([]byte, error) {
	prev := -1
	for _, i := range idx {
		if i <= prev || i >= len(spec) {
			return nil, errFormat
		}
		prev = i
	}
	work := make([]uint32, len(spec))
	copy(work, spec)
	for _, i := range idx {
		work[i] = 0
	}
	transform(work, true)
	raw := make([]byte, 4*len(work))
	for i, v := range work {
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
	out := make([]byte, 12+8*len(idx)+z.Len())
	binary.LittleEndian.PutUint32(out[0:], uint32(nSrc))
	binary.LittleEndian.PutUint32(out[4:], uint32(len(spec)))
	binary.LittleEndian.PutUint32(out[8:], uint32(len(idx)))
	p := 12
	for _, i := range idx {
		binary.LittleEndian.PutUint32(out[p:], uint32(i))
		binary.LittleEndian.PutUint32(out[p+4:], spec[i])
		p += 8
	}
	copy(out[p:], z.Bytes())
	return out, nil
}

type evoInd struct {
	idx  []int
	size int
}

func evolve(nSrc int, spec []uint32, rng *rand.Rand) ([]int, error) {
	n := len(spec)
	cache := make(map[string]int)
	eval := func(idx []int) (int, error) {
		key := idxKey(idx)
		if s, ok := cache[key]; ok {
			return s, nil
		}
		blob, err := encodeEvolve(nSrc, spec, idx)
		if err != nil {
			return 0, err
		}
		cache[key] = len(blob)
		return len(blob), nil
	}
	pop := make([]evoInd, evoPop)
	pop[0].idx = nil
	if n > 0 {
		pop[1].idx = []int{0}
	}
	for i := 2; i < len(pop); i++ {
		pop[i].idx = randomIdx(n, 1+rng.IntN(3), rng)
	}
	for i := range pop {
		s, err := eval(pop[i].idx)
		if err != nil {
			return nil, err
		}
		pop[i].size = s
	}
	for range evoGen {
		next := make([]evoInd, len(pop))
		b1, b2 := twoBest(pop)
		next[0] = evoInd{idx: append([]int(nil), pop[b1].idx...), size: pop[b1].size}
		next[1] = evoInd{idx: append([]int(nil), pop[b2].idx...), size: pop[b2].size}
		for i := 2; i < len(pop); i++ {
			a := pop[tournament(pop, rng)]
			b := pop[tournament(pop, rng)]
			child := mutateIdx(crossIdx(a.idx, b.idx, rng), n, rng)
			s, err := eval(child)
			if err != nil {
				return nil, err
			}
			next[i] = evoInd{idx: child, size: s}
		}
		pop = next
	}
	b1, _ := twoBest(pop)
	return append([]int(nil), pop[b1].idx...), nil
}

func twoBest(pop []evoInd) (int, int) {
	best, second := 0, 1
	if pop[1].size < pop[0].size {
		best, second = 1, 0
	}
	for i := 2; i < len(pop); i++ {
		if pop[i].size < pop[best].size {
			second = best
			best = i
		} else if pop[i].size < pop[second].size {
			second = i
		}
	}
	return best, second
}

func tournament(pop []evoInd, rng *rand.Rand) int {
	best := rng.IntN(len(pop))
	for range evoTour - 1 {
		j := rng.IntN(len(pop))
		if pop[j].size < pop[best].size {
			best = j
		}
	}
	return best
}

func crossIdx(a, b []int, rng *rand.Rand) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && (j == len(b) || a[i] < b[j]):
			if rng.IntN(2) == 0 {
				out = append(out, a[i])
			}
			i++
		case j < len(b) && (i == len(a) || b[j] < a[i]):
			if rng.IntN(2) == 0 {
				out = append(out, b[j])
			}
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

// mutateIdx adds one bin that is not already cleared, or drops one.
func mutateIdx(idx []int, n int, rng *rand.Rand) []int {
	if n == 0 {
		return nil
	}
	if len(idx) > 0 && rng.IntN(2) == 0 {
		drop := rng.IntN(len(idx))
		out := append([]int(nil), idx...)
		return append(out[:drop], out[drop+1:]...)
	}
	for range 16 {
		v := rng.IntN(n)
		if containsIdx(idx, v) {
			continue
		}
		return insertIdx(idx, v)
	}
	return append([]int(nil), idx...)
}

func randomIdx(n, k int, rng *rand.Rand) []int {
	if n == 0 {
		return nil
	}
	if k > n {
		k = n
	}
	var out []int
	for len(out) < k {
		v := rng.IntN(n)
		if containsIdx(out, v) {
			continue
		}
		out = insertIdx(out, v)
	}
	return out
}

func insertIdx(idx []int, v int) []int {
	out := make([]int, len(idx)+1)
	i := 0
	for i < len(idx) && idx[i] < v {
		out[i] = idx[i]
		i++
	}
	out[i] = v
	copy(out[i+1:], idx[i:])
	return out
}

func containsIdx(idx []int, v int) bool {
	_, ok := slices.BinarySearch(idx, v)
	return ok
}

func idxKey(idx []int) string {
	b := make([]byte, 4*len(idx))
	for i, v := range idx {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(v))
	}
	return string(b)
}
