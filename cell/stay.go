// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// A stay residual is the detail of one lifting step. The lowpass is the
// average of the stay residuals already decoded above and to the left
// (a missing neighbor is zero). The sixteen contexts are the absolute
// sizes of those two neighbors, split at 1, 6, and 16.

type stayCode struct {
	bits uint32
	n    uint8
}

type stayTab struct {
	max   int
	first [16]int
	count [16]int
	index [16]int
	sym   []uint16
}

var (
	stayEnc [16][staySpan]stayCode
	stayDec [16]stayTab
)

func init() {
	for ctx := range stayModel {
		buildStay(ctx, stayModel[ctx])
	}
}

func buildStay(ctx int, model [][2]uint16) {
	var count [16]int
	var lens [staySpan]uint8
	for _, pair := range model {
		sym, n := int(pair[0]), int(pair[1])
		if sym >= staySpan || n <= 0 || n >= 16 || lens[sym] != 0 {
			panic("cell: bad stay code")
		}
		lens[sym] = uint8(n)
		count[n]++
	}
	kraft := 0
	for n := 1; n < 16; n++ {
		kraft += count[n] << (15 - n)
	}
	if kraft != 1<<15 {
		panic("cell: stay code is not complete")
	}
	var first [16]int
	code := 0
	max := 0
	for n := 1; n < 16; n++ {
		code = (code + count[n-1]) << 1
		first[n] = code
		if count[n] > 0 {
			max = n
		}
	}
	next := first
	byLen := make([][]uint16, 16)
	var enc [staySpan]stayCode
	for sym := range staySpan {
		n := int(lens[sym])
		if n == 0 {
			continue
		}
		enc[sym] = stayCode{bits: uint32(next[n]), n: uint8(n)}
		next[n]++
		byLen[n] = append(byLen[n], uint16(sym))
	}
	var syms []uint16
	var index [16]int
	for n := 1; n <= max; n++ {
		index[n] = len(syms)
		syms = append(syms, byLen[n]...)
	}
	stayEnc[ctx] = enc
	stayDec[ctx] = stayTab{max: max, first: first, count: count, index: index, sym: syms}
}

func stayBucket(a int) int {
	switch {
	case a < 1:
		return 0
	case a < 6:
		return 1
	case a < 16:
		return 2
	default:
		return 3
	}
}

// stayPredict is shared by the encoder and the decoder. hat is the lowpass
// and ctx selects the code for the detail.
func stayPredict(isStay *[pixels]bool, resid *[pixels]int, p int) (hat, ctx int) {
	sum := 0
	al, au := 0, 0
	if p%width != 0 && isStay[p-1] {
		sum += resid[p-1]
		al = iabs(resid[p-1])
	}
	if p >= width && isStay[p-width] {
		sum += resid[p-width]
		au = iabs(resid[p-width])
	}
	return sum >> 1, stayBucket(al)*4 + stayBucket(au)
}

func (b *bits) writeStay(ctx, sym int) error {
	if uint(ctx) >= 16 || uint(sym) >= staySpan {
		return ErrFormat
	}
	c := stayEnc[ctx][sym]
	if c.n == 0 {
		return ErrFormat
	}
	b.bits(int(c.bits), int(c.n))
	return nil
}

func (r *reader) readStay(ctx int) (int, error) {
	if uint(ctx) >= 16 {
		return 0, ErrFormat
	}
	tab := &stayDec[ctx]
	code := 0
	for n := 1; n <= tab.max; n++ {
		bit, err := r.bit()
		if err != nil {
			return 0, err
		}
		code = (code << 1) | bit
		if tab.count[n] != 0 && code >= tab.first[n] && code < tab.first[n]+tab.count[n] {
			return int(tab.sym[tab.index[n]+code-tab.first[n]]), nil
		}
	}
	return 0, ErrFormat
}
