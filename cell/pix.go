// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// pixAlpha is one channel. The pair table is 256³ counts per channel.
const pixAlpha = 256

// pixChain is a second-order model of each pixel's color, shared across the
// frame, with the state kept at the pixel. An empty pair falls back to the
// previous color alone. The motion sample is the other prediction: it stands
// when this row has not seen it, or when it is at least as common as the
// mode, so a blended edge is not replaced by a modal color.
type pixChain struct {
	pair   []int
	trans  []int
	marg   [channels][pixAlpha]int
	prev2  []rgb
	prev   []rgb
	frames int
}

func newPixChain(pixels int) *pixChain {
	return &pixChain{
		pair:  make([]int, channels*pixAlpha*pixAlpha*pixAlpha),
		trans: make([]int, channels*pixAlpha*pixAlpha),
		prev2: make([]rgb, pixels),
		prev:  make([]rgb, pixels),
	}
}

func (m *pixChain) pairAt(c, a, b int) []int {
	i := ((c*pixAlpha+a)*pixAlpha + b) * pixAlpha
	return m.pair[i : i+pixAlpha]
}

func (m *pixChain) transAt(c, a int) []int {
	i := (c*pixAlpha + a) * pixAlpha
	return m.trans[i : i+pixAlpha]
}

// observeFrame counts the true color of each pixel as a continuation of the
// motion source. A negative source learns the pixel's own history. The first
// frame fills the marginal only, and the second fills the first-order rows.
func (m *pixChain) observeFrame(src []int, frame []rgb) {
	for p, col := range frame {
		s := p
		if src != nil && src[p] >= 0 {
			s = src[p]
		}
		for c := range channels {
			id := int(col[c])
			m.marg[c][id]++
			if m.frames >= 2 {
				m.pairAt(c, int(m.prev2[s][c]), int(m.prev[s][c]))[id]++
			}
			if m.frames >= 1 {
				m.transAt(c, int(m.prev[s][c]))[id]++
			}
		}
	}
	if m.frames >= 1 {
		copy(m.prev2, m.prev)
	}
	copy(m.prev, frame)
	if m.frames < 2 {
		m.frames++
	}
}

// mixFrame returns the motion sample where the chain does not have a strictly
// more common color for that source.
func (m *pixChain) mixFrame(spatial []rgb, src []int) []rgb {
	out := append([]rgb(nil), spatial...)
	for p := range spatial {
		if src != nil && src[p] < 0 {
			continue
		}
		s := p
		if src != nil {
			s = src[p]
		}
		var col rgb
		for c := range channels {
			col[c] = byte(m.mixOne(s, c, int(spatial[p][c])))
		}
		out[p] = col
	}
	return out
}

func (m *pixChain) mixOne(origin, c, other int) int {
	prev := int(m.prev[origin][c])
	prev2 := int(m.prev2[origin][c])
	var row []int
	if m.frames >= 2 {
		row = m.pairAt(c, prev2, prev)
		if !usable(row, false) {
			row = nil
		}
	}
	if row == nil && m.frames >= 1 {
		row = m.transAt(c, prev)
		if !usable(row, false) {
			row = nil
		}
	}
	if row == nil {
		return other
	}
	mode, ok := modeOf(row)
	if !ok || other < 0 || other >= len(row) || row[other] == 0 || row[other] >= row[mode] {
		return other
	}
	return mode
}
