package tm

import (
	"encoding/binary"
	"sort"
)

// Huffman lengths for opcode, length, payload, and distance. They were
// taken from the programs Search writes for iris.data and wdbc.data after
// the Burrows-Wheeler transform and move-to-front. Every encodable symbol
// is kept in the alphabet. Zero means the symbol is not encodable.
// Opcodes are 1..5, lengths are 2..255, and payload and distance bytes are
// 0..255.

var opLen = [6]uint8{
	0, 3, 4, 2, 1, 4,
}

var lenLen = [256]uint8{
	0, 0, 7, 2, 1, 3, 5, 6, 6, 7, 7, 8, 8, 8, 9, 9,
	9, 10, 10, 10, 10, 10, 10, 11, 12, 11, 11, 12, 11, 13, 12, 13,
	12, 13, 13, 12, 12, 14, 12, 14, 14, 13, 14, 12, 14, 14, 13, 11,
	13, 11, 12, 15, 13, 15, 14, 14, 14, 13, 14, 15, 14, 15, 14, 14,
	14, 15, 15, 15, 13, 13, 14, 15, 15, 15, 15, 15, 15, 15, 14, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 14, 15, 15, 15, 13, 15,
	15, 15, 15, 13, 14, 15, 14, 14, 15, 15, 15, 15, 15, 14, 15, 15,
	15, 15, 15, 14, 15, 15, 13, 15, 14, 15, 14, 15, 15, 14, 15, 15,
	14, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 12, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 14, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 14, 14, 14, 14,
	14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 10,
}

var payLen = [256]uint8{
	2, 4, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5, 6, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 11, 10, 11, 10, 9, 10, 10, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
}

var distLen = [256]uint8{
	5, 5, 5, 6, 6, 6, 6, 6, 6, 6, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 8, 7,
	8, 7, 8, 8, 7, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 9, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 9, 9,
	8, 9, 9, 9, 8, 9, 8, 9, 8, 8, 8, 8, 8, 8, 9, 8,
	8, 9, 8, 9, 9, 9, 8, 9, 9, 8, 9, 9, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 8, 9, 8, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 8, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
}

const maxBits = 15

type code struct {
	bits uint32
	n    uint8
}

type table struct {
	enc   [256]code
	max   int
	count [maxBits + 1]int
	first [maxBits + 1]int
	off   [maxBits + 1]int
	sym   []byte
}

var opTab, lenTab, payTab, distTab *table

func init() {
	opTab = newTable(opLen[:])
	lenTab = newTable(lenLen[:])
	payTab = newTable(payLen[:])
	distTab = newTable(distLen[:])
}

func newTable(lengths []uint8) *table {
	t := &table{}
	var sum uint32
	var syms []byte
	for s, n := range lengths {
		if n == 0 {
			continue
		}
		if int(n) > maxBits {
			panic("tm: code length")
		}
		t.count[n]++
		if int(n) > t.max {
			t.max = int(n)
		}
		sum += 1 << (maxBits - int(n))
		syms = append(syms, byte(s))
	}
	if sum != 1<<maxBits {
		panic("tm: incomplete code")
	}
	sort.Slice(syms, func(i, j int) bool {
		li, lj := lengths[syms[i]], lengths[syms[j]]
		if li != lj {
			return li < lj
		}
		return syms[i] < syms[j]
	})
	t.sym = syms
	next := 0
	si := 0
	for l := 1; l <= t.max; l++ {
		next <<= 1
		t.first[l] = next
		t.off[l] = si
		for si < len(syms) && int(lengths[syms[si]]) == l {
			if next >= 1<<l {
				panic("tm: code overflow")
			}
			t.enc[syms[si]] = code{bits: uint32(next), n: uint8(l)}
			next++
			si++
		}
	}
	if si != len(syms) || next != 1<<t.max {
		panic("tm: canonical lengths")
	}
	return t
}

// pack writes the program length as a little-endian uint32, then the fields
// in instruction order as canonical codes. The last byte is padded with
// zero bits.
func pack(prog []byte) ([]byte, error) {
	if len(prog) == 0 {
		return nil, nil
	}
	var w bitWriter
	if err := walkProg(prog, w.put); err != nil {
		return nil, err
	}
	body := w.finish()
	out := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(out[:4], uint32(len(prog)))
	copy(out[4:], body)
	return out, nil
}

func codedBits(prog []byte) (int, error) {
	n := 0
	err := walkProg(prog, func(t *table, s byte) error {
		c := t.enc[s]
		if c.n == 0 {
			return ErrFormat
		}
		n += int(c.n)
		return nil
	})
	return n, err
}

func walkProg(prog []byte, emit func(*table, byte) error) error {
	for i := 0; i < len(prog); {
		if err := emit(opTab, prog[i]); err != nil {
			return ErrFormat
		}
		switch prog[i] {
		case opEmit:
			if i+1 >= len(prog) {
				return ErrFormat
			}
			if err := emit(payTab, prog[i+1]); err != nil {
				return err
			}
			i += 2
		case opLit:
			if i+1 >= len(prog) {
				return ErrFormat
			}
			n := int(prog[i+1])
			if n < 2 || i+2+n > len(prog) {
				return ErrFormat
			}
			if err := emit(lenTab, prog[i+1]); err != nil {
				return err
			}
			for _, b := range prog[i+2 : i+2+n] {
				if err := emit(payTab, b); err != nil {
					return err
				}
			}
			i += 2 + n
		case opRep:
			if i+2 >= len(prog) || prog[i+2] < 2 {
				return ErrFormat
			}
			if err := emit(payTab, prog[i+1]); err != nil {
				return err
			}
			if err := emit(lenTab, prog[i+2]); err != nil {
				return err
			}
			i += 3
		case opCopy:
			if i+3 >= len(prog) || prog[i+3] < 3 {
				return ErrFormat
			}
			if prog[i+1] == 0 && prog[i+2] == 0 {
				return ErrFormat
			}
			if err := emit(distTab, prog[i+1]); err != nil {
				return err
			}
			if err := emit(distTab, prog[i+2]); err != nil {
				return err
			}
			if err := emit(lenTab, prog[i+3]); err != nil {
				return err
			}
			i += 4
		case opFar:
			if i+5 >= len(prog) || prog[i+5] < 3 {
				return ErrFormat
			}
			if prog[i+1]|prog[i+2]|prog[i+3]|prog[i+4] == 0 {
				return ErrFormat
			}
			for k := 1; k <= 4; k++ {
				if err := emit(distTab, prog[i+k]); err != nil {
					return err
				}
			}
			if err := emit(lenTab, prog[i+5]); err != nil {
				return err
			}
			i += 6
		default:
			return ErrFormat
		}
	}
	return nil
}

func unpack(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob) < 4 {
		return nil, ErrFormat
	}
	u := binary.LittleEndian.Uint32(blob[:4])
	if u == 0 || int64(u) > int64(len(blob)-4)<<3 {
		return nil, ErrFormat
	}
	n := int(u)
	r := bitReader{b: blob[4:]}
	prog := make([]byte, 0, n)
	for len(prog) < n {
		op, err := r.take(opTab)
		if err != nil {
			return nil, err
		}
		switch op {
		case opEmit:
			b, err := r.take(payTab)
			if err != nil {
				return nil, err
			}
			if len(prog)+2 > n {
				return nil, ErrFormat
			}
			prog = append(prog, opEmit, b)
		case opLit:
			ln, err := r.take(lenTab)
			if err != nil {
				return nil, err
			}
			if int(ln) < 2 || len(prog)+2+int(ln) > n {
				return nil, ErrFormat
			}
			prog = append(prog, opLit, ln)
			for range int(ln) {
				b, err := r.take(payTab)
				if err != nil {
					return nil, err
				}
				prog = append(prog, b)
			}
		case opRep:
			b, err := r.take(payTab)
			if err != nil {
				return nil, err
			}
			ln, err := r.take(lenTab)
			if err != nil {
				return nil, err
			}
			if ln < 2 || len(prog)+3 > n {
				return nil, ErrFormat
			}
			prog = append(prog, opRep, b, ln)
		case opCopy:
			lo, err := r.take(distTab)
			if err != nil {
				return nil, err
			}
			hi, err := r.take(distTab)
			if err != nil {
				return nil, err
			}
			ln, err := r.take(lenTab)
			if err != nil {
				return nil, err
			}
			if ln < 3 || (lo == 0 && hi == 0) || len(prog)+4 > n {
				return nil, ErrFormat
			}
			prog = append(prog, opCopy, lo, hi, ln)
		case opFar:
			var d [4]byte
			for i := range d {
				b, err := r.take(distTab)
				if err != nil {
					return nil, err
				}
				d[i] = b
			}
			ln, err := r.take(lenTab)
			if err != nil {
				return nil, err
			}
			if ln < 3 || d[0]|d[1]|d[2]|d[3] == 0 || len(prog)+6 > n {
				return nil, ErrFormat
			}
			prog = append(prog, opFar, d[0], d[1], d[2], d[3], ln)
		default:
			return nil, ErrFormat
		}
	}
	if len(prog) != n {
		return nil, ErrFormat
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return prog, nil
}

type bitWriter struct {
	buf []byte
	acc uint64
	n   int
}

func (w *bitWriter) put(t *table, s byte) error {
	c := t.enc[s]
	if c.n == 0 {
		return ErrFormat
	}
	w.acc = (w.acc << c.n) | uint64(c.bits)
	w.n += int(c.n)
	for w.n >= 8 {
		w.n -= 8
		w.buf = append(w.buf, byte(w.acc>>uint(w.n)))
		w.acc &= (uint64(1) << uint(w.n)) - 1
	}
	return nil
}

func (w *bitWriter) finish() []byte {
	if w.n > 0 {
		w.buf = append(w.buf, byte(w.acc<<uint(8-w.n)))
	}
	return w.buf
}

type bitReader struct {
	b    []byte
	i    int
	cur  byte
	left int
}

func (r *bitReader) bit() (uint32, error) {
	if r.left == 0 {
		if r.i >= len(r.b) {
			return 0, ErrFormat
		}
		r.cur = r.b[r.i]
		r.i++
		r.left = 8
	}
	r.left--
	bit := uint32(r.cur>>uint(r.left)) & 1
	r.cur &= byte((1 << uint(r.left)) - 1)
	return bit, nil
}

func (r *bitReader) take(t *table) (byte, error) {
	code := 0
	for l := 1; l <= t.max; l++ {
		bit, err := r.bit()
		if err != nil {
			return 0, err
		}
		code = (code << 1) | int(bit)
		if t.count[l] == 0 {
			continue
		}
		d := code - t.first[l]
		if d >= 0 && d < t.count[l] {
			return t.sym[t.off[l]+d], nil
		}
	}
	return 0, ErrFormat
}

func (r *bitReader) done() error {
	if r.cur != 0 || r.i != len(r.b) {
		return ErrFormat
	}
	return nil
}
