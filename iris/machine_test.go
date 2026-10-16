// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package iris

import (
	"bytes"
	"os"
	"testing"
)

func TestSearch(t *testing.T) {
	for _, name := range []string{"iris.data", "bezdekIris.data"} {
		original, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Search(original)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := m.Run()
		if err != nil {
			t.Fatalf("%s: run: %v", name, err)
		}
		if !bytes.Equal(got, original) {
			t.Fatalf("%s: machine output mismatch", name)
		}
		if len(m.Levels) == 0 || m.Levels[len(m.Levels)-1].Machines != 1 {
			t.Fatalf("%s: levels %+v", name, m.Levels)
		}
		if m.Levels[0].Machines != 151 {
			t.Fatalf("%s: %d blocks", name, m.Levels[0].Machines)
		}
		for i := 1; i < len(m.Levels); i++ {
			prev, cur := m.Levels[i-1], m.Levels[i]
			if cur.Machines != (prev.Machines+1)/2 {
				t.Fatalf("%s: level %d machines %d, prev %d", name, i, cur.Machines, prev.Machines)
			}
			if cur.Bytes > prev.Bytes {
				t.Fatalf("%s: level %d grew %d -> %d", name, i, prev.Bytes, cur.Bytes)
			}
		}
		again, err := Search(original)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again.Prog, m.Prog) {
			t.Fatalf("%s: search is not stable", name)
		}
		text, err := progText(m.Prog)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes -> %d byte machine\n%s", name, len(original), len(m.Prog), text)
		for i, lv := range m.Levels {
			t.Logf("%s level %d: %d machines, %d bytes", name, i, lv.Machines, lv.Bytes)
		}
	}
}

func TestTapePrograms(t *testing.T) {
	rep := smallest([]byte("aaaa"))
	got, err := run(rep)
	if err != nil || !bytes.Equal(got, []byte("aaaa")) {
		t.Fatalf("rep %q %v", got, err)
	}
	if !bytes.Contains(rep, []byte{opRep}) {
		t.Fatalf("rep prog %x", rep)
	}

	s := []byte("abcdefgabcdefg")
	copyProg := smallest(s)
	got, err = run(copyProg)
	if err != nil || !bytes.Equal(got, s) {
		t.Fatalf("copy %q %v", got, err)
	}
	if !bytes.Contains(copyProg, []byte{opCopy}) {
		t.Fatalf("copy prog %x", copyProg)
	}
}

func TestMachineRejectsGarbage(t *testing.T) {
	original, err := os.ReadFile("iris.data")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Search(original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(m.Prog[:len(m.Prog)-1]); err != ErrFormat {
		t.Fatalf("short: %v", err)
	}
	if _, err := run([]byte{0xff}); err != ErrFormat {
		t.Fatalf("opcode: %v", err)
	}
}
