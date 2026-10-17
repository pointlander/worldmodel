// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command worldmodel applies the Number Theoretic Transform to a byte string.
//
// The field is Z/998244353Z. 998244353-1 = 119*2^23 and 3 is a primitive root,
// so every power-of-two length up to 2^23 has a principal root of unity. Each
// input byte is that residue. The input is zero-padded to the next such
// length; the inverse of that spectrum is the bytes followed by the pad.
package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	mod    = 998244353
	root   = 3
	maxLen = 1 << 23
)

var errFormat = errors.New("ntt: invalid transform")

func main() {
	path := "iris/iris.data"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	original, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	spec, err := NTT(original)
	if err != nil {
		panic(err)
	}
	for _, value := range spec {
		fmt.Println(value)
	}
	got, err := INTT(spec)
	if err != nil {
		panic(err)
	}
	if len(got) < len(original) {
		panic("not the same")
	}
	for i, b := range original {
		if got[i] != uint32(b) {
			panic("not the same")
		}
	}
	for _, v := range got[len(original):] {
		if v != 0 {
			panic("not the same")
		}
	}
	fmt.Println(len(original), len(spec))
}

// compressFactor2 is a test compressor. It transforms src, divides every even
// residue by two, and flate-compresses the inverse transform of that spectrum.
// Parity bits put the factors of two back, so the pair of transforms is lossless.
func compressFactor2(src []byte) ([]byte, error) {
	spec, err := NTT(src)
	if err != nil {
		return nil, err
	}
	if len(spec) == 0 {
		return nil, nil
	}
	folded, parity := factor2(spec)
	time, err := INTT(folded)
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
	out := make([]byte, 8+len(parity)+z.Len())
	binary.LittleEndian.PutUint32(out[0:], uint32(len(src)))
	binary.LittleEndian.PutUint32(out[4:], uint32(len(spec)))
	copy(out[8:], parity)
	copy(out[8+len(parity):], z.Bytes())
	return out, nil
}

// decompressFactor2 is the inverse of compressFactor2.
func decompressFactor2(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob) < 8 {
		return nil, errFormat
	}
	nSrc := int(binary.LittleEndian.Uint32(blob[0:]))
	n := int(binary.LittleEndian.Uint32(blob[4:]))
	want, err := paddedLen(nSrc)
	if err != nil || want != n || n == 0 {
		return nil, errFormat
	}
	pLen := (n + 7) / 8
	if len(blob) < 8+pLen {
		return nil, errFormat
	}
	if n%8 != 0 && blob[8+pLen-1]&(byte(0xff)<<(n%8)) != 0 {
		return nil, errFormat
	}
	parity := blob[8 : 8+pLen]
	rest := bytes.NewReader(blob[8+pLen:])
	r := flate.NewReader(rest)
	raw, err := io.ReadAll(r)
	r.Close()
	if err != nil || rest.Len() != 0 || len(raw) != 4*n {
		return nil, errFormat
	}
	folded := make([]uint32, n)
	for i := range folded {
		v := binary.LittleEndian.Uint32(raw[4*i:])
		if v >= mod {
			return nil, errFormat
		}
		folded[i] = v
	}
	transform(folded, false)
	spec, err := unfold2(folded, parity)
	if err != nil {
		return nil, err
	}
	time, err := INTT(spec)
	if err != nil {
		return nil, err
	}
	if len(time) != n {
		return nil, errFormat
	}
	for _, v := range time[nSrc:] {
		if v != 0 {
			return nil, errFormat
		}
	}
	out := make([]byte, nSrc)
	for i := range out {
		if time[i] > 255 {
			return nil, errFormat
		}
		out[i] = byte(time[i])
	}
	return out, nil
}

// factor2 divides every even residue by two. Bit i of parity is set when
// spec[i] was even. An even residue is at most mod-1, so the quotient fits
// and doubling it restores the residue.
func factor2(spec []uint32) (folded []uint32, parity []byte) {
	folded = make([]uint32, len(spec))
	parity = make([]byte, (len(spec)+7)/8)
	for i, r := range spec {
		if r&1 == 0 {
			folded[i] = r / 2
			parity[i/8] |= 1 << (uint(i) % 8)
		} else {
			folded[i] = r
		}
	}
	return folded, parity
}

func unfold2(folded []uint32, parity []byte) ([]uint32, error) {
	if len(parity) != (len(folded)+7)/8 {
		return nil, errFormat
	}
	out := make([]uint32, len(folded))
	for i, r := range folded {
		if parity[i/8]&(1<<(uint(i)%8)) == 0 {
			out[i] = r
			continue
		}
		if r > (mod-1)/2 {
			return nil, errFormat
		}
		out[i] = r * 2
	}
	return out, nil
}

// NTT returns the Number Theoretic Transform of src.
func NTT(src []byte) ([]uint32, error) {
	n, err := paddedLen(len(src))
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	a := make([]uint32, n)
	for i, b := range src {
		a[i] = uint32(b)
	}
	transform(a, false)
	return a, nil
}

// INTT is the inverse of NTT. len(spec) must be a power of two no greater
// than 2^23, and every coefficient must be a residue modulo 998244353.
func INTT(spec []uint32) ([]uint32, error) {
	n := len(spec)
	if n == 0 {
		return nil, nil
	}
	if n > maxLen || n&(n-1) != 0 {
		return nil, errFormat
	}
	a := make([]uint32, n)
	for i, v := range spec {
		if v >= mod {
			return nil, errFormat
		}
		a[i] = v
	}
	transform(a, true)
	return a, nil
}

func paddedLen(n int) (int, error) {
	if n == 0 {
		return 0, nil
	}
	p := 1
	for p < n {
		if p > maxLen/2 {
			return 0, errFormat
		}
		p <<= 1
	}
	return p, nil
}

// Residues are multiplied in uint64. mod^2 fits in uint64, so a product does not wrap.
func transform(a []uint32, invert bool) {
	n := len(a)
	bitReverse(a)
	g := uint64(root)
	if invert {
		g = modPow(g, mod-2)
	}
	for length := 2; length <= n; length <<= 1 {
		wlen := modPow(g, (mod-1)/uint64(length))
		half := length >> 1
		for i := 0; i < n; i += length {
			w := uint64(1)
			for j := 0; j < half; j++ {
				u := uint64(a[i+j])
				v := uint64(a[i+j+half]) * w % mod
				a[i+j] = add(u, v)
				a[i+j+half] = sub(u, v)
				w = w * wlen % mod
			}
		}
	}
	if invert {
		inv := modPow(uint64(n), mod-2)
		for i, v := range a {
			a[i] = uint32(uint64(v) * inv % mod)
		}
	}
}

func bitReverse(a []uint32) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
}

func add(u, v uint64) uint32 {
	s := u + v
	if s >= mod {
		s -= mod
	}
	return uint32(s)
}

func sub(u, v uint64) uint32 {
	if u < v {
		return uint32(u + mod - v)
	}
	return uint32(u - v)
}

func modPow(a, e uint64) uint64 {
	r := uint64(1)
	a %= mod
	for e > 0 {
		if e&1 == 1 {
			r = r * a % mod
		}
		a = a * a % mod
		e >>= 1
	}
	return r
}
