// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tm

import (
	"bytes"
	"math/rand/v2"
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 20000)
	for i := range random {
		random[i] = byte(r.Uint32())
	}
	far := make([]byte, 70000)
	pattern := []byte("abcdefghijklmnopqrstuvwxyz012345")
	copy(far, pattern)
	copy(far[len(far)-len(pattern):], pattern)

	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"one", []byte{0x7f}},
		{"pair", []byte{1, 1}},
		{"zeros", bytes.Repeat([]byte{0}, 100)},
		{"long-zeros", bytes.Repeat([]byte{0}, 300)},
		{"huge-zeros", bytes.Repeat([]byte{0}, 100000)},
		{"text", bytes.Repeat([]byte("hello, world!\n"), 40)},
		{"random", random},
		{"far", far},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundTrip(t, tc.data)
		})
	}
}

func TestFiles(t *testing.T) {
	for _, name := range []string{"../iris/iris.data", "../bcwd/wdbc.data"} {
		original, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		m, coded := assertRoundTrip(t, original)
		if len(coded) >= len(m.Prog) {
			t.Fatalf("table %d, program %d", len(coded), len(m.Prog))
		}
		t.Logf("%s: %d -> %d bytes, program %d", name, len(original), len(coded), len(m.Prog))
	}
}

func TestZerosAreOneRep(t *testing.T) {
	m, err := Search(bytes.Repeat([]byte{0}, 100), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Prog) != 3 || m.Prog[0] != opRep || m.Prog[2] != 100 {
		t.Fatalf("prog %x", m.Prog)
	}
	if len(m.Levels) != 1 || m.Levels[0].Machines != 1 || m.Levels[0].Bytes != len(m.Prog) {
		t.Fatalf("levels %+v", m.Levels)
	}
}

func TestFarCopy(t *testing.T) {
	data := make([]byte, 70000)
	pattern := []byte("abcdefghijklmnopqrstuvwxyz012345")
	copy(data, pattern)
	copy(data[len(data)-len(pattern):], pattern)
	prog, err := Search(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(prog.Prog, []byte{opFar}) {
		t.Fatalf("missing far copy, %d bytes", len(prog.Prog))
	}
}

func TestRejectsGarbage(t *testing.T) {
	prog, err := Compress([]byte("abcdefghijklmnopqrstuvwxyz012345HELLO"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decompress(prog[:len(prog)-1]); err != ErrFormat {
		t.Fatalf("short: %v", err)
	}
	if _, err := Decompress(append(append([]byte{}, prog...), 0)); err != ErrFormat {
		t.Fatalf("trailing: %v", err)
	}
	if _, err := Decompress([]byte{0xff}); err != ErrFormat {
		t.Fatalf("opcode: %v", err)
	}
}

func TestPaddingBits(t *testing.T) {
	var coded []byte
	for n := 2; n <= 40; n++ {
		m, err := Search(bytes.Repeat([]byte{0}, n), true)
		if err != nil {
			t.Fatal(err)
		}
		bits, err := codedBits(m.Prog)
		if err != nil {
			t.Fatal(err)
		}
		if bits%8 == 0 {
			continue
		}
		coded, err = pack(m.Prog)
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if coded == nil {
		t.Fatal("expected spare bits")
	}
	flipped := append([]byte(nil), coded...)
	flipped[len(flipped)-1] ^= 1
	if _, err := Decompress(flipped); err != ErrFormat {
		t.Fatalf("padding: %v", err)
	}
}

func TestSymbolCodes(t *testing.T) {
	for i, tab := range []*table{opTab, lenTab, payTab, distTab} {
		var w bitWriter
		var seq []byte
		for s := 0; s < len(tab.enc); s++ {
			if tab.enc[s].n == 0 {
				continue
			}
			if err := w.put(tab, byte(s)); err != nil {
				t.Fatal(err)
			}
			seq = append(seq, byte(s))
		}
		r := bitReader{b: w.finish()}
		for _, s := range seq {
			got, err := r.take(tab)
			if err != nil || got != s {
				t.Fatalf("table %d sym %d got %d err %v", i, s, got, err)
			}
		}
		if err := r.done(); err != nil {
			t.Fatalf("table %d: %v", i, err)
		}
	}
}

func assertRoundTrip(t *testing.T, data []byte) (Machine, []byte) {
	t.Helper()
	m, err := Search(data, true)
	if err != nil {
		t.Fatal(err)
	}
	coded, err := pack(m.Prog)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decompress(coded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip %d -> %d bytes", len(data), len(got))
	}
	again, err := Compress(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, coded) {
		t.Fatal("encoding is not stable")
	}
	if len(data) == 0 {
		return m, coded
	}
	if len(m.Levels) != 1 || m.Levels[0].Machines != 1 || m.Levels[0].Bytes != len(m.Prog) {
		t.Fatalf("levels %+v", m.Levels)
	}
	if m.Levels[0].Bits != mustBits(m.Prog) {
		t.Fatalf("bits %d, level %d", mustBits(m.Prog), m.Levels[0].Bits)
	}
	return m, coded
}
