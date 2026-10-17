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
		t.Logf("%s: %d -> %d bytes, program %d, %d rounds", name, len(original), len(coded), len(m.Prog), len(m.Levels))
		for i, lv := range m.Levels {
			t.Logf("  level %d: %d machines, %d bytes", i, lv.Machines, lv.Bytes)
		}
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
	if len(m.Levels) != 3 || m.Levels[0].Machines != 4 || m.Levels[2].Machines != 1 {
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
	src := bytes.Repeat([]byte{0}, 100)
	m, err := Search(src, true)
	if err != nil {
		t.Fatal(err)
	}
	bits, err := codedBits(m.Prog)
	if err != nil {
		t.Fatal(err)
	}
	if bits%8 == 0 {
		t.Fatal("expected spare bits")
	}
	coded, err := pack(m.Prog)
	if err != nil {
		t.Fatal(err)
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
	if m.Levels[len(m.Levels)-1].Machines != 1 {
		t.Fatalf("levels %+v", m.Levels)
	}
	wantBlocks := (len(data) + blockSize - 1) / blockSize
	if m.Levels[0].Machines != wantBlocks {
		t.Fatalf("blocks %d, want %d", m.Levels[0].Machines, wantBlocks)
	}
	for i := 1; i < len(m.Levels); i++ {
		prev, cur := m.Levels[i-1], m.Levels[i]
		if cur.Machines != (prev.Machines+1)/2 {
			t.Fatalf("level %d machines %d, prev %d", i, cur.Machines, prev.Machines)
		}
		if cur.Bytes > prev.Bytes {
			t.Fatalf("level %d grew %d -> %d", i, prev.Bytes, cur.Bytes)
		}
	}
	return m, coded
}
