// Package secom is a symmetric compressor for the SECOM semiconductor files.
//
// The matrix is 1567 rows by 590 space-separated features, with NaN for a
// missing cell. Each column has a fixed decimal scale: a value is an integer
// in units of that scale, written without trailing zeros. Offsets from the
// column minimum are Rice-coded, and identical missing-cell patterns are
// stored once. The labels file is a pass/fail bit and a Rice-coded run of
// timestamps. Compress and Decompress use those same widths and parameters,
// so each file round-trips to its original bytes.
package secom

import (
	"bytes"
	"errors"
	"strconv"
	"time"
)

const (
	nRows     = 1567
	nCols     = 590
	indexBits = 11 // nRows fits in 11 bits

	kindMatrix = 0
	kindLabels = 1
)

// ErrFormat reports an input that is not a canonical SECOM file or a blob
// this model can decode.
var ErrFormat = errors.New("secom: invalid data")

// Compress packs secom.data or secom_labels.data.
func Compress(src []byte) ([]byte, error) {
	if isLabels(src) {
		return compressLabels(src)
	}
	return compressMatrix(src)
}

// Decompress restores the exact file bytes written by Compress.
func Decompress(src []byte) ([]byte, error) {
	r := &bitio{b: src}
	kind, err := r.read(8)
	if err != nil {
		return nil, err
	}
	var out []byte
	switch kind {
	case kindMatrix:
		out, err = decompressMatrix(r)
	case kindLabels:
		out, err = decompressLabels(r)
	default:
		return nil, ErrFormat
	}
	if err != nil {
		return nil, err
	}
	if err := r.finish(); err != nil {
		return nil, err
	}
	return out, nil
}

func isLabels(src []byte) bool {
	return bytes.HasPrefix(src, []byte("-1 \"")) || bytes.HasPrefix(src, []byte("1 \""))
}

type bitio struct {
	b []byte
	i int
}

func (w *bitio) write(width int, v uint64) {
	for bit := width - 1; bit >= 0; bit-- {
		if w.i%8 == 0 {
			w.b = append(w.b, 0)
		}
		if (v>>uint(bit))&1 == 1 {
			w.b[w.i/8] |= 1 << uint(7-w.i%8)
		}
		w.i++
	}
}

func (r *bitio) read(width int) (uint64, error) {
	var v uint64
	for range width {
		if r.i/8 >= len(r.b) {
			return 0, ErrFormat
		}
		v <<= 1
		if r.b[r.i/8]&(1<<uint(7-r.i%8)) != 0 {
			v |= 1
		}
		r.i++
	}
	return v, nil
}

func (r *bitio) finish() error {
	if (r.i+7)/8 != len(r.b) {
		return ErrFormat
	}
	for ; r.i < len(r.b)*8; r.i++ {
		if r.b[r.i/8]&(1<<uint(7-r.i%8)) != 0 {
			return ErrFormat
		}
	}
	return nil
}

func zigzag(v int64) uint64 {
	return (uint64(v) << 1) ^ uint64(v>>63)
}

func unzigzag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

func bitlen(u uint64) int {
	n := 0
	for u > 0 {
		u >>= 1
		n++
	}
	return n
}

func writeSigned(w *bitio, v int64) {
	u := zigzag(v)
	n := bitlen(u)
	w.write(6, uint64(n))
	if n > 0 {
		w.write(n, u)
	}
}

func readSigned(r *bitio) (int64, error) {
	n, err := r.read(6)
	if err != nil {
		return 0, err
	}
	if n > 63 {
		return 0, ErrFormat
	}
	u, err := r.read(int(n))
	if err != nil {
		return 0, err
	}
	return unzigzag(u), nil
}

func writeRice(w *bitio, u uint64, k int) {
	q := u >> uint(k)
	for range int(q) {
		w.write(1, 1)
	}
	w.write(1, 0)
	if k > 0 {
		w.write(k, u&(uint64(1)<<uint(k)-1))
	}
}

func readRice(r *bitio, k int) (uint64, error) {
	q := uint64(0)
	for {
		bit, err := r.read(1)
		if err != nil {
			return 0, err
		}
		if bit == 0 {
			break
		}
		q++
		if q > 1<<20 {
			return 0, ErrFormat
		}
	}
	rem, err := r.read(k)
	if err != nil {
		return 0, err
	}
	u := q<<uint(k) | rem
	if u > 1<<40 {
		return 0, ErrFormat
	}
	return u, nil
}

func riceBits(vals []uint64, k int) int {
	n := 0
	for _, u := range vals {
		n += int(u>>uint(k)) + 1 + k
	}
	return n
}

func bestK(vals []uint64) int {
	bestK, bestN := 0, riceBits(vals, 0)
	for k := 1; k < 32; k++ {
		if n := riceBits(vals, k); n < bestN {
			bestK, bestN = k, n
		}
	}
	return bestK
}

func splitCRLF(src []byte, n int) ([][]byte, error) {
	if bytes.Count(src, []byte("\n")) != n || bytes.Count(src, []byte("\r\n")) != n {
		return nil, ErrFormat
	}
	parts := bytes.Split(src, []byte("\r\n"))
	if len(parts) != n+1 || len(parts[n]) != 0 {
		return nil, ErrFormat
	}
	return parts[:n], nil
}

func pow10(n int) int64 {
	p := int64(1)
	for range n {
		p *= 10
	}
	return p
}

func render(v int64, dec int) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	if dec == 0 {
		return sign + strconv.FormatInt(v, 10)
	}
	scale := pow10(dec)
	ip := v / scale
	fp := v % scale
	if fp == 0 {
		return sign + strconv.FormatInt(ip, 10)
	}
	frac := strconv.FormatInt(fp, 10)
	for len(frac) < dec {
		frac = "0" + frac
	}
	for len(frac) > 0 && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}
	return sign + strconv.FormatInt(ip, 10) + "." + frac
}

func parseNumber(s string, dec int) (int64, error) {
	if len(s) == 0 || len(s) > 32 || dec < 0 || dec > 7 {
		return 0, ErrFormat
	}
	i := 0
	sign := int64(1)
	if s[0] == '-' {
		sign = -1
		i++
		if i == len(s) {
			return 0, ErrFormat
		}
	}
	if s[i] < '0' || s[i] > '9' {
		return 0, ErrFormat
	}
	var ip int64
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		ip = ip*10 + int64(s[i]-'0')
		i++
	}
	fracDigits := 0
	var fp int64
	if i < len(s) {
		if s[i] != '.' {
			return 0, ErrFormat
		}
		i++
		if i == len(s) {
			return 0, ErrFormat
		}
		for i < len(s) {
			if s[i] < '0' || s[i] > '9' {
				return 0, ErrFormat
			}
			fp = fp*10 + int64(s[i]-'0')
			i++
			fracDigits++
		}
	}
	if i != len(s) || fracDigits > dec {
		return 0, ErrFormat
	}
	scale := pow10(dec)
	if scale != 0 && ip > (1<<62)/scale {
		return 0, ErrFormat
	}
	v := ip * scale
	if fracDigits > 0 {
		v += fp * pow10(dec-fracDigits)
	}
	v *= sign
	if render(v, dec) != s {
		return 0, ErrFormat
	}
	return v, nil
}

func fracLen(f []byte) (int, error) {
	if bytes.Equal(f, []byte("NaN")) {
		return 0, nil
	}
	s := string(f)
	if len(s) == 0 || len(s) > 32 {
		return 0, ErrFormat
	}
	i := 0
	if s[0] == '-' {
		i++
		if i == len(s) {
			return 0, ErrFormat
		}
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if digits == 0 {
		return 0, ErrFormat
	}
	if i == len(s) {
		return 0, nil
	}
	if s[i] != '.' {
		return 0, ErrFormat
	}
	i++
	frac := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		frac++
	}
	if i != len(s) || frac == 0 || frac > 7 {
		return 0, ErrFormat
	}
	return frac, nil
}

func maskKey(idx []int) string {
	b := make([]byte, 0, len(idx)*2)
	for _, i := range idx {
		b = append(b, byte(i), byte(i>>8))
	}
	return string(b)
}

// maskMode picks the shortest description of a missing-cell pattern.
// 0 is a bitset, 1 lists missing rows, 2 lists present rows.
func maskMode(nMissing int) int {
	list := indexBits + nMissing*indexBits
	comp := indexBits + (nRows-nMissing)*indexBits
	bit := nRows
	mode := 1
	best := list
	if comp < best {
		mode, best = 2, comp
	}
	if bit < best {
		mode = 0
	}
	return mode
}

func writeMask(w *bitio, idx []int) {
	mode := maskMode(len(idx))
	w.write(2, uint64(mode))
	switch mode {
	case 0:
		at := 0
		for i := range nRows {
			bit := uint64(0)
			if at < len(idx) && idx[at] == i {
				bit = 1
				at++
			}
			w.write(1, bit)
		}
	case 1:
		w.write(indexBits, uint64(len(idx)))
		for _, i := range idx {
			w.write(indexBits, uint64(i))
		}
	case 2:
		w.write(indexBits, uint64(nRows-len(idx)))
		at := 0
		for i := range nRows {
			if at < len(idx) && idx[at] == i {
				at++
				continue
			}
			w.write(indexBits, uint64(i))
		}
	}
}

func readMask(r *bitio) ([]bool, error) {
	mode, err := r.read(2)
	if err != nil {
		return nil, err
	}
	nan := make([]bool, nRows)
	switch mode {
	case 0:
		n := 0
		for i := range nan {
			bit, err := r.read(1)
			if err != nil {
				return nil, err
			}
			nan[i] = bit == 1
			if nan[i] {
				n++
			}
		}
		if maskMode(n) != 0 {
			return nil, ErrFormat
		}
	case 1, 2:
		count, err := r.read(indexBits)
		if err != nil {
			return nil, err
		}
		if count > nRows {
			return nil, ErrFormat
		}
		prev := -1
		listed := make([]bool, nRows)
		for range int(count) {
			idx, err := r.read(indexBits)
			if err != nil {
				return nil, err
			}
			if idx >= nRows || int(idx) <= prev {
				return nil, ErrFormat
			}
			listed[idx] = true
			prev = int(idx)
		}
		nMissing := int(count)
		if mode == 2 {
			nMissing = nRows - int(count)
			for i := range nan {
				nan[i] = !listed[i]
			}
		} else {
			copy(nan, listed)
		}
		if maskMode(nMissing) != int(mode) {
			return nil, ErrFormat
		}
	default:
		return nil, ErrFormat
	}
	return nan, nil
}

func compressMatrix(src []byte) ([]byte, error) {
	lines, err := splitCRLF(src, nRows)
	if err != nil {
		return nil, err
	}
	decs := make([]int, nCols)
	fields := make([][][]byte, nRows)
	for i, line := range lines {
		f := bytes.Split(line, []byte{' '})
		if len(f) != nCols {
			return nil, ErrFormat
		}
		for j, tok := range f {
			if len(tok) == 0 {
				return nil, ErrFormat
			}
			n, err := fracLen(tok)
			if err != nil {
				return nil, err
			}
			if n > decs[j] {
				decs[j] = n
			}
		}
		fields[i] = f
	}

	vals := make([][]int64, nCols)
	missing := make([][]int, nCols)
	for j := range nCols {
		vals[j] = make([]int64, 0, nRows)
	}
	for i := range nRows {
		for j := range nCols {
			tok := fields[i][j]
			if bytes.Equal(tok, []byte("NaN")) {
				missing[j] = append(missing[j], i)
				continue
			}
			v, err := parseNumber(string(tok), decs[j])
			if err != nil {
				return nil, err
			}
			vals[j] = append(vals[j], v)
		}
	}

	type dictEntry struct {
		id  int
		idx []int
	}
	dict := make(map[string]int)
	var masks [][]int
	ids := make([]int, nCols)
	for j := range nCols {
		key := maskKey(missing[j])
		id, ok := dict[key]
		if !ok {
			id = len(masks)
			dict[key] = id
			masks = append(masks, missing[j])
		}
		ids[j] = id
	}
	if len(masks) > 255 {
		return nil, ErrFormat
	}

	w := &bitio{}
	w.write(8, kindMatrix)
	w.write(8, uint64(len(masks)))
	for _, idx := range masks {
		writeMask(w, idx)
	}
	for j := range nCols {
		w.write(8, uint64(ids[j]))
		w.write(3, uint64(decs[j]))
		col := vals[j]
		constant := true
		var c int64
		if len(col) == 0 {
			c = 0
		} else {
			c = col[0]
			for _, v := range col[1:] {
				if v != c {
					constant = false
					break
				}
			}
		}
		if constant {
			w.write(1, 1)
			writeSigned(w, c)
			continue
		}
		w.write(1, 0)
		min := col[0]
		for _, v := range col[1:] {
			if v < min {
				min = v
			}
		}
		off := make([]uint64, len(col))
		for i, v := range col {
			off[i] = uint64(v - min)
		}
		k := bestK(off)
		writeSigned(w, min)
		w.write(5, uint64(k))
		for _, u := range off {
			writeRice(w, u, k)
		}
	}
	return w.b, nil
}

func decompressMatrix(r *bitio) ([]byte, error) {
	nmasks, err := r.read(8)
	if err != nil {
		return nil, err
	}
	if nmasks == 0 {
		return nil, ErrFormat
	}
	masks := make([][]bool, nmasks)
	for i := range masks {
		masks[i], err = readMask(r)
		if err != nil {
			return nil, err
		}
	}
	vals := make([][]int64, nCols)
	nans := make([][]bool, nCols)
	decs := make([]int, nCols)
	for j := range nCols {
		id, err := r.read(8)
		if err != nil {
			return nil, err
		}
		if id >= nmasks {
			return nil, ErrFormat
		}
		dec, err := r.read(3)
		if err != nil {
			return nil, err
		}
		flag, err := r.read(1)
		if err != nil {
			return nil, err
		}
		nan := masks[id]
		col := make([]int64, nRows)
		if flag == 1 {
			c, err := readSigned(r)
			if err != nil {
				return nil, err
			}
			for i := range col {
				col[i] = c
			}
		} else {
			min, err := readSigned(r)
			if err != nil {
				return nil, err
			}
			k, err := r.read(5)
			if err != nil {
				return nil, err
			}
			if k > 31 {
				return nil, ErrFormat
			}
			present := 0
			var first int64
			same := true
			for i := range nRows {
				if nan[i] {
					continue
				}
				u, err := readRice(r, int(k))
				if err != nil {
					return nil, err
				}
				v := min + int64(u)
				if present == 0 {
					first = v
				} else if v != first {
					same = false
				}
				col[i] = v
				present++
			}
			if present < 2 || same {
				return nil, ErrFormat
			}
		}
		vals[j] = col
		nans[j] = nan
		decs[j] = int(dec)
	}

	var b bytes.Buffer
	b.Grow(nRows * 64)
	for i := range nRows {
		for j := range nCols {
			if j > 0 {
				b.WriteByte(' ')
			}
			if nans[j][i] {
				b.WriteString("NaN")
				continue
			}
			b.WriteString(render(vals[j][i], decs[j]))
		}
		b.WriteString("\r\n")
	}
	return b.Bytes(), nil
}

func compressLabels(src []byte) ([]byte, error) {
	lines, err := splitCRLF(src, nRows)
	if err != nil {
		return nil, err
	}
	fail := make([]bool, nRows)
	times := make([]time.Time, nRows)
	for i, line := range lines {
		bad, t, err := parseLabel(string(line))
		if err != nil {
			return nil, err
		}
		fail[i] = bad
		times[i] = t
	}
	deltas := make([]uint64, nRows-1)
	for i := 1; i < nRows; i++ {
		d := times[i].Unix() - times[i-1].Unix()
		if d < 0 {
			return nil, ErrFormat
		}
		deltas[i-1] = uint64(d)
	}
	k := bestK(deltas)

	w := &bitio{}
	w.write(8, kindLabels)
	for _, bad := range fail {
		bit := uint64(0)
		if bad {
			bit = 1
		}
		w.write(1, bit)
	}
	if err := writeTime(w, times[0]); err != nil {
		return nil, err
	}
	w.write(5, uint64(k))
	for _, d := range deltas {
		writeRice(w, d, k)
	}
	return w.b, nil
}

func decompressLabels(r *bitio) ([]byte, error) {
	fail := make([]bool, nRows)
	for i := range fail {
		bit, err := r.read(1)
		if err != nil {
			return nil, err
		}
		fail[i] = bit == 1
	}
	cur, err := readTime(r)
	if err != nil {
		return nil, err
	}
	k, err := r.read(5)
	if err != nil {
		return nil, err
	}
	if k > 31 {
		return nil, ErrFormat
	}
	var b bytes.Buffer
	b.Grow(nRows * 24)
	writeLabel(&b, fail[0], cur)
	for i := 1; i < nRows; i++ {
		d, err := readRice(r, int(k))
		if err != nil {
			return nil, err
		}
		cur = cur.Add(time.Duration(d) * time.Second)
		writeLabel(&b, fail[i], cur)
	}
	return b.Bytes(), nil
}

func parseLabel(line string) (bool, time.Time, error) {
	var rest string
	fail := false
	switch {
	case len(line) >= 4 && line[:4] == "-1 \"":
		rest = line[4:]
	case len(line) >= 3 && line[:3] == "1 \"":
		fail = true
		rest = line[3:]
	default:
		return false, time.Time{}, ErrFormat
	}
	if len(rest) == 0 || rest[len(rest)-1] != '"' {
		return false, time.Time{}, ErrFormat
	}
	inner := rest[:len(rest)-1]
	t, err := time.Parse("02/01/2006 15:04:05", inner)
	if err != nil || t.Format("02/01/2006 15:04:05") != inner {
		return false, time.Time{}, ErrFormat
	}
	return fail, t, nil
}

func writeTime(w *bitio, t time.Time) error {
	y := t.Year()
	if y < 2000 || y > 2255 {
		return ErrFormat
	}
	w.write(8, uint64(y-2000))
	w.write(4, uint64(t.Month()))
	w.write(5, uint64(t.Day()))
	w.write(5, uint64(t.Hour()))
	w.write(6, uint64(t.Minute()))
	w.write(6, uint64(t.Second()))
	return nil
}

func readTime(r *bitio) (time.Time, error) {
	y, err := r.read(8)
	if err != nil {
		return time.Time{}, err
	}
	m, err := r.read(4)
	if err != nil {
		return time.Time{}, err
	}
	d, err := r.read(5)
	if err != nil {
		return time.Time{}, err
	}
	h, err := r.read(5)
	if err != nil {
		return time.Time{}, err
	}
	min, err := r.read(6)
	if err != nil {
		return time.Time{}, err
	}
	s, err := r.read(6)
	if err != nil {
		return time.Time{}, err
	}
	if m < 1 || m > 12 || d < 1 || d > 31 || h > 23 || min > 59 || s > 59 {
		return time.Time{}, ErrFormat
	}
	t := time.Date(2000+int(y), time.Month(m), int(d), int(h), int(min), int(s), 0, time.UTC)
	if t.Year() != 2000+int(y) || int(t.Month()) != int(m) || t.Day() != int(d) {
		return time.Time{}, ErrFormat
	}
	return t, nil
}

func writeLabel(b *bytes.Buffer, fail bool, t time.Time) {
	if fail {
		b.WriteString("1 \"")
	} else {
		b.WriteString("-1 \"")
	}
	b.WriteString(t.Format("02/01/2006 15:04:05"))
	b.WriteString("\"\r\n")
}
