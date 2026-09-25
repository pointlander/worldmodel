// Package iris is a symmetric compressor for the Fisher iris file.
//
// The file is three blocks of fifty rows (setosa, versicolor, virginica), so
// the class labels are a function of the row index and are not stored. Each
// measurement is a tenth of a centimeter and, within a block, lies in a small
// integer span. A row is one mixed-radix digit in the product of those spans;
// a file is that digit string evaluated as a single integer. Compress writes
// the integer and Decompress reads it back with the same bases, so the
// transform is an exact inverse and the packed form is 298 bytes.
package iris

import (
	"bytes"
	"errors"
)

// CompressedSize is the length of every blob Compress returns.
// 2382 bits is ceil(log2 of the number of representable files); 298 bytes
// is the smallest whole-byte width that holds them.
const CompressedSize = 298

// ErrFormat reports an input that is not a canonical iris file or a blob
// this model can decode.
var ErrFormat = errors.New("iris: invalid data")

// mins and spans are the tight per-class ranges of measurement tenths
// (sepal length, sepal width, petal length, petal width) on iris.data.
// Every in-range integer is representable, so the model needs no codebook.
var (
	mins = [3][4]int{
		{43, 23, 10, 1},
		{49, 20, 30, 10},
		{49, 22, 45, 14},
	}
	spans = [3][4]int{
		{16, 22, 10, 6},
		{22, 15, 22, 9},
		{31, 17, 25, 12},
	}
	names = [3]string{"Iris-setosa", "Iris-versicolor", "Iris-virginica"}
	bases [3]int
)

func init() {
	for c := range bases {
		bases[c] = 1
		for _, s := range spans[c] {
			bases[c] *= s
		}
	}
}

// Compress packs a canonical iris file into CompressedSize bytes.
func Compress(src []byte) ([]byte, error) {
	samples, err := parse(src)
	if err != nil {
		return nil, err
	}
	out := make([]byte, CompressedSize)
	for i, s := range samples {
		c := i / 50
		if err := mulAdd(out, bases[c], pack(c, s)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Decompress restores the exact file bytes written by Compress.
func Decompress(src []byte) ([]byte, error) {
	if len(src) != CompressedSize {
		return nil, ErrFormat
	}
	n := make([]byte, CompressedSize)
	copy(n, src)
	var samples [150][4]int
	for i := len(samples) - 1; i >= 0; i-- {
		c := i / 50
		samples[i] = unpack(c, divMod(n, bases[c]))
	}
	if !zero(n) {
		return nil, ErrFormat
	}
	return format(samples), nil
}

// pack is the mixed-radix evaluation of one row. unpack is its inverse.
func pack(class int, s [4]int) int {
	d := 0
	for i := range 4 {
		d = d*spans[class][i] + (s[i] - mins[class][i])
	}
	return d
}

func unpack(class, d int) [4]int {
	var s [4]int
	for i := 3; i >= 0; i-- {
		sp := spans[class][i]
		s[i] = mins[class][i] + d%sp
		d /= sp
	}
	return s
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

func parse(src []byte) ([150][4]int, error) {
	var samples [150][4]int
	lines := bytes.Split(src, []byte{'\n'})
	// 150 records and the trailing blank line of the canonical file.
	if len(lines) != 152 || len(lines[150]) != 0 || len(lines[151]) != 0 {
		return samples, ErrFormat
	}
	for i, line := range lines[:150] {
		s, err := parseLine(line, i/50)
		if err != nil {
			return samples, err
		}
		samples[i] = s
	}
	return samples, nil
}

func parseLine(line []byte, class int) ([4]int, error) {
	var s [4]int
	// "d.d,d.d,d.d,d.d," is 16 bytes; the class name occupies the rest.
	if len(line) != 16+len(names[class]) || line[15] != ',' || string(line[16:]) != names[class] {
		return s, ErrFormat
	}
	for i := range 4 {
		p := line[i*4 : i*4+3]
		if p[1] != '.' || p[0] < '0' || p[0] > '9' || p[2] < '0' || p[2] > '9' || (i < 3 && line[i*4+3] != ',') {
			return s, ErrFormat
		}
		v := int(p[0]-'0')*10 + int(p[2]-'0')
		if v < mins[class][i] || v >= mins[class][i]+spans[class][i] {
			return s, ErrFormat
		}
		s[i] = v
	}
	return s, nil
}

func format(samples [150][4]int) []byte {
	var b bytes.Buffer
	b.Grow(4551)
	for i, s := range samples {
		for j, v := range s {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteByte(byte('0' + v/10))
			b.WriteByte('.')
			b.WriteByte(byte('0' + v%10))
		}
		b.WriteByte(',')
		b.WriteString(names[i/50])
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}
