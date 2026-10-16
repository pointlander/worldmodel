// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bcwd is a symmetric compressor for the Wisconsin diagnostic
// breast cancer file.
//
// Each of the thirty features is a decimal with at most four significant
// digits, so a column is one digit string in the mixed radix of the four-digit
// mantissas that fit between that column's minimum and maximum. Identifiers
// are raw integers with a two-bit width tag, and the diagnosis is one bit.
// Compress writes those integers and Decompress reads them back with the same
// bases, which restores the original file bytes.
package bcwd

import (
	"bytes"
	"errors"
	"strconv"
)

const (
	rowCount  = 569
	featCount = 30
	diagBytes = (rowCount + 7) / 8
)

// ErrFormat reports an input that is not a canonical WDBC file or a blob
// this model can decode.
var ErrFormat = errors.New("bcwd: invalid data")

// bounds holds, for each feature, the normalized exponent range and the
// inclusive value range in units of 10^eMin. Exponents count powers of ten
// on a four-digit mantissa (17.99 is 1799 * 10^-2).
//
// Columns are mean, standard error, then worst, and within each group:
// radius, texture, perimeter, area, smoothness, compactness, concavity,
// concave points, symmetry, fractal dimension.
var bounds = [featCount][4]int{
	{-3, -2, 6981, 28110},  // mean radius
	{-3, -2, 9710, 39280},  // mean texture
	{-2, -1, 4379, 18850},  // mean perimeter
	{-1, 0, 1435, 25010},   // mean area
	{-5, -4, 5263, 16340},  // mean smoothness
	{-5, -4, 1938, 34540},  // mean compactness
	{-7, -4, 0, 4268000},   // mean concavity
	{-6, -4, 0, 201200},    // mean concave points
	{-4, -4, 1060, 3040},   // mean symmetry
	{-5, -5, 4996, 9744},   // mean fractal
	{-4, -3, 1115, 28730},  // se radius
	{-4, -3, 3602, 48850},  // se texture
	{-4, -2, 7570, 219800}, // se perimeter
	{-3, -1, 6802, 542200}, // se area
	{-6, -5, 1713, 31130},  // se smoothness
	{-6, -4, 2252, 135400}, // se compactness
	{-7, -4, 0, 3960000},   // se concavity
	{-6, -5, 0, 52790},     // se concave points
	{-6, -5, 7882, 78950},  // se symmetry
	{-7, -5, 8948, 298400}, // se fractal
	{-3, -2, 7930, 36040},  // worst radius
	{-2, -2, 1202, 4954},   // worst texture
	{-2, -1, 5041, 25120},  // worst perimeter
	{-1, 0, 1852, 42540},   // worst area
	{-5, -4, 7117, 22260},  // worst smoothness
	{-5, -3, 2729, 105800}, // worst compactness
	{-6, -3, 0, 1252000},   // worst concavity
	{-6, -4, 0, 291000},    // worst concave points
	{-4, -4, 1565, 6638},   // worst symmetry
	{-5, -4, 5504, 20750},  // worst fractal
}

// idPayload is the payload width of each identifier tag. The tag is the
// smallest bucket the integer fits in.
var idPayload = [4]int{20, 24, 27, 30}

var (
	bases        [featCount]int
	widths       [featCount]int
	featureBytes int
)

func init() {
	for j := range bounds {
		bases[j] = alphabet(j)
		widths[j] = (bitsNeeded(bases[j], rowCount) + 7) / 8
		featureBytes += widths[j]
	}
}

// Compress packs a canonical WDBC file.
func Compress(src []byte) ([]byte, error) {
	ids, diag, ranks, err := parse(src)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, featureBytes+diagBytes+len(src)/8)
	for j := range featCount {
		col := make([]byte, widths[j])
		for i := range rowCount {
			if err := mulAdd(col, bases[j], ranks[i][j]); err != nil {
				return nil, err
			}
		}
		out = append(out, col...)
	}
	out, err = appendDiag(out, diag)
	if err != nil {
		return nil, err
	}
	return appendIDs(out, ids)
}

// Decompress restores the exact file bytes written by Compress.
func Decompress(src []byte) ([]byte, error) {
	if len(src) < featureBytes+diagBytes {
		return nil, ErrFormat
	}
	var ranks [rowCount][featCount]int
	off := 0
	for j := range featCount {
		col := src[off : off+widths[j]]
		off += widths[j]
		n := make([]byte, widths[j])
		copy(n, col)
		for i := rowCount - 1; i >= 0; i-- {
			ranks[i][j] = divMod(n, bases[j])
		}
		if !zero(n) {
			return nil, ErrFormat
		}
	}
	diag, err := readDiag(src[off : off+diagBytes])
	if err != nil {
		return nil, err
	}
	off += diagBytes
	ids, err := readIDs(src[off:])
	if err != nil {
		return nil, err
	}
	return format(ids, diag, ranks)
}

func alphabet(j int) int {
	n := 0
	if bounds[j][2] == 0 {
		n = 1
	}
	for e := bounds[j][0]; e <= bounds[j][1]; e++ {
		if start, end, ok := block(j, e); ok {
			n += end - start + 1
		}
	}
	return n
}

// block is the inclusive mantissa range at exponent e that lands inside the
// column bounds. rank and unrank walk the same blocks.
func block(j, e int) (start, end int, ok bool) {
	pow := pow10(e - bounds[j][0])
	start = 1000
	if bounds[j][2] > 0 {
		start = ceilDiv(bounds[j][2], pow)
		if start < 1000 {
			start = 1000
		}
	}
	end = bounds[j][3] / pow
	if end > 9999 {
		end = 9999
	}
	return start, end, start <= end
}

func rank(j, scaled int) (int, error) {
	lo, hi := bounds[j][2], bounds[j][3]
	if scaled < lo || scaled > hi {
		return 0, ErrFormat
	}
	idx := 0
	if lo == 0 {
		if scaled == 0 {
			return 0, nil
		}
		idx = 1
	}
	for e := bounds[j][0]; e <= bounds[j][1]; e++ {
		pow := pow10(e - bounds[j][0])
		start, end, ok := block(j, e)
		if !ok {
			continue
		}
		if scaled%pow == 0 {
			sig := scaled / pow
			if sig >= start && sig <= end {
				return idx + sig - start, nil
			}
		}
		idx += end - start + 1
	}
	return 0, ErrFormat
}

func unrank(j, idx int) (sig, exp int, isZero bool, err error) {
	if idx < 0 {
		return 0, 0, false, ErrFormat
	}
	if bounds[j][2] == 0 {
		if idx == 0 {
			return 0, 0, true, nil
		}
		idx--
	}
	for e := bounds[j][0]; e <= bounds[j][1]; e++ {
		start, end, ok := block(j, e)
		if !ok {
			continue
		}
		n := end - start + 1
		if idx < n {
			return start + idx, e, false, nil
		}
		idx -= n
	}
	return 0, 0, false, ErrFormat
}

func pow10(k int) int {
	p := 1
	for range k {
		p *= 10
	}
	return p
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func parse(src []byte) ([rowCount]int64, [rowCount]bool, [rowCount][featCount]int, error) {
	var ids [rowCount]int64
	var diag [rowCount]bool
	var ranks [rowCount][featCount]int
	lines := bytes.Split(src, []byte{'\n'})
	if len(lines) != rowCount+1 || len(lines[rowCount]) != 0 {
		return ids, diag, ranks, ErrFormat
	}
	for i, line := range lines[:rowCount] {
		id, malignant, rankRow, err := parseLine(line)
		if err != nil {
			return ids, diag, ranks, err
		}
		ids[i] = id
		diag[i] = malignant
		ranks[i] = rankRow
	}
	return ids, diag, ranks, nil
}

func parseLine(line []byte) (int64, bool, [featCount]int, error) {
	var ranks [featCount]int
	fields := bytes.Split(line, []byte{','})
	if len(fields) != featCount+2 {
		return 0, false, ranks, ErrFormat
	}
	id, err := parseID(string(fields[0]))
	if err != nil {
		return 0, false, ranks, err
	}
	var malignant bool
	switch string(fields[1]) {
	case "M":
		malignant = true
	case "B":
		malignant = false
	default:
		return 0, false, ranks, ErrFormat
	}
	for j, field := range fields[2:] {
		ranks[j], err = parseFeat(string(field), j)
		if err != nil {
			return 0, false, ranks, err
		}
	}
	return id, malignant, ranks, nil
}

func parseID(s string) (int64, error) {
	if len(s) == 0 || len(s) > 9 || s[0] == '0' {
		return 0, ErrFormat
	}
	var v int64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrFormat
		}
		v = v*10 + int64(s[i]-'0')
	}
	if v >= 1<<30 || strconv.FormatInt(v, 10) != s {
		return 0, ErrFormat
	}
	return v, nil
}

func parseFeat(s string, j int) (int, error) {
	if len(s) == 0 || len(s) > 12 {
		return 0, ErrFormat
	}
	scaled, sig, exp, isZero, err := parseScaled(s, j)
	if err != nil {
		return 0, err
	}
	if render(sig, exp, isZero) != s {
		return 0, ErrFormat
	}
	return rank(j, scaled)
}

func parseScaled(s string, j int) (scaled, sig, exp int, isZero bool, err error) {
	if s == "0" {
		if bounds[j][2] != 0 {
			return 0, 0, 0, false, ErrFormat
		}
		return 0, 0, 0, true, nil
	}
	sig, exp, err = normalize(s)
	if err != nil {
		return 0, 0, 0, false, err
	}
	if exp < bounds[j][0] || exp > bounds[j][1] {
		return 0, 0, 0, false, ErrFormat
	}
	return sig * pow10(exp-bounds[j][0]), sig, exp, false, nil
}

func normalize(s string) (sig, exp int, err error) {
	dot := -1
	mag := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '.':
			if dot >= 0 {
				return 0, 0, ErrFormat
			}
			dot = i
		case s[i] >= '0' && s[i] <= '9':
			mag = mag*10 + int(s[i]-'0')
		default:
			return 0, 0, ErrFormat
		}
	}
	if mag == 0 {
		return 0, 0, ErrFormat
	}
	if dot >= 0 {
		exp = -(len(s) - dot - 1)
	}
	for mag%10 == 0 {
		mag /= 10
		exp++
	}
	for mag < 1000 {
		mag *= 10
		exp--
	}
	if mag > 9999 {
		return 0, 0, ErrFormat
	}
	return mag, exp, nil
}

func format(ids [rowCount]int64, diag [rowCount]bool, ranks [rowCount][featCount]int) ([]byte, error) {
	var b bytes.Buffer
	b.Grow(124103)
	for i := range rowCount {
		b.WriteString(strconv.FormatInt(ids[i], 10))
		b.WriteByte(',')
		if diag[i] {
			b.WriteByte('M')
		} else {
			b.WriteByte('B')
		}
		for j := range featCount {
			sig, exp, isZero, err := unrank(j, ranks[i][j])
			if err != nil {
				return nil, err
			}
			b.WriteByte(',')
			b.WriteString(render(sig, exp, isZero))
		}
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

func render(sig, exp int, isZero bool) string {
	if isZero {
		return "0"
	}
	var dig [4]byte
	for i := 3; i >= 0; i-- {
		dig[i] = byte('0' + sig%10)
		sig /= 10
	}
	var s string
	switch {
	case exp >= 0:
		buf := make([]byte, 4+exp)
		copy(buf, dig[:])
		for i := 4; i < len(buf); i++ {
			buf[i] = '0'
		}
		return string(buf)
	case -exp < 4:
		k := 4 + exp
		s = string(dig[:k]) + "." + string(dig[k:])
	default:
		zeros := -exp - 4
		buf := make([]byte, 2+zeros+4)
		buf[0], buf[1] = '0', '.'
		for i := range zeros {
			buf[2+i] = '0'
		}
		copy(buf[2+zeros:], dig[:])
		s = string(buf)
	}
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

func appendDiag(dst []byte, diag [rowCount]bool) ([]byte, error) {
	var bits []byte
	n := 0
	for _, malignant := range diag {
		bit := uint32(0)
		if malignant {
			bit = 1
		}
		bits, n = writeBits(bits, n, 1, bit)
	}
	if len(bits) != diagBytes {
		return nil, ErrFormat
	}
	return append(dst, bits...), nil
}

func readDiag(src []byte) ([rowCount]bool, error) {
	var diag [rowCount]bool
	if len(src) != diagBytes {
		return diag, ErrFormat
	}
	n := 0
	for i := range diag {
		v, bit, err := readBits(src, n, 1)
		if err != nil {
			return diag, err
		}
		diag[i] = v == 1
		n = bit
	}
	if err := padding(src, n); err != nil {
		return diag, err
	}
	return diag, nil
}

func appendIDs(dst []byte, ids [rowCount]int64) ([]byte, error) {
	var bits []byte
	n := 0
	for _, id := range ids {
		tag := idTag(id)
		if tag < 0 {
			return nil, ErrFormat
		}
		bits, n = writeBits(bits, n, 2, uint32(tag))
		bits, n = writeBits(bits, n, idPayload[tag], uint32(id))
	}
	return append(dst, bits...), nil
}

func readIDs(src []byte) ([rowCount]int64, error) {
	var ids [rowCount]int64
	n := 0
	for i := range ids {
		tag, bit, err := readBits(src, n, 2)
		if err != nil {
			return ids, err
		}
		if tag > 3 {
			return ids, ErrFormat
		}
		v, bit, err := readBits(src, bit, idPayload[tag])
		if err != nil {
			return ids, err
		}
		if idTag(int64(v)) != int(tag) {
			return ids, ErrFormat
		}
		ids[i] = int64(v)
		n = bit
	}
	if err := padding(src, n); err != nil {
		return ids, err
	}
	return ids, nil
}

func idTag(v int64) int {
	switch {
	case v < 0 || v >= 1<<30:
		return -1
	case v < 1<<20:
		return 0
	case v < 1<<24:
		return 1
	case v < 1<<27:
		return 2
	default:
		return 3
	}
}

func writeBits(dst []byte, n, width int, v uint32) ([]byte, int) {
	for i := width - 1; i >= 0; i-- {
		if n%8 == 0 {
			dst = append(dst, 0)
		}
		if (v>>uint(i))&1 != 0 {
			dst[n/8] |= 1 << uint(7-n%8)
		}
		n++
	}
	return dst, n
}

func readBits(src []byte, n, width int) (uint32, int, error) {
	var v uint32
	for range width {
		if n/8 >= len(src) {
			return 0, n, ErrFormat
		}
		v <<= 1
		if src[n/8]&(1<<uint(7-n%8)) != 0 {
			v |= 1
		}
		n++
	}
	return v, n, nil
}

func padding(src []byte, n int) error {
	if (n+7)/8 != len(src) {
		return ErrFormat
	}
	for i := n; i < len(src)*8; i++ {
		if src[i/8]&(1<<uint(7-i%8)) != 0 {
			return ErrFormat
		}
	}
	return nil
}

// mulAdd sets n = n*base + add, with n stored big-endian.
// divMod sets n = floor(n/base) and returns n mod base.
func mulAdd(n []byte, base, add int) error {
	carry := add
	for i := len(n) - 1; i >= 0; i-- {
		v := int(n[i])*base + carry
		n[i] = byte(v)
		carry = v >> 8
	}
	if carry != 0 {
		return ErrFormat
	}
	return nil
}

func divMod(n []byte, base int) int {
	rem := 0
	for i := range n {
		v := rem<<8 | int(n[i])
		n[i] = byte(v / base)
		rem = v % base
	}
	return rem
}

func zero(n []byte) bool {
	for _, b := range n {
		if b != 0 {
			return false
		}
	}
	return true
}

func bitsNeeded(base, exp int) int {
	n := []byte{1}
	for range exp {
		n = mulGrow(n, base)
	}
	bits := bitLen(n)
	if isPow2(n) {
		bits--
	}
	return bits
}

func mulGrow(n []byte, m int) []byte {
	carry := 0
	for i := len(n) - 1; i >= 0; i-- {
		v := int(n[i])*m + carry
		n[i] = byte(v)
		carry = v >> 8
	}
	for carry > 0 {
		n = append([]byte{byte(carry)}, n...)
		carry >>= 8
	}
	return n
}

func bitLen(n []byte) int {
	i := 0
	for i < len(n) && n[i] == 0 {
		i++
	}
	if i == len(n) {
		return 0
	}
	bits := (len(n) - i - 1) * 8
	for v := n[i]; v > 0; v >>= 1 {
		bits++
	}
	return bits
}

func isPow2(n []byte) bool {
	seen := false
	for _, b := range n {
		if b == 0 {
			continue
		}
		if seen || b&(b-1) != 0 {
			return false
		}
		seen = true
	}
	return seen
}
