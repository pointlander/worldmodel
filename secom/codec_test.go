// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package secom

import (
	"bytes"
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, name := range []string{"secom.data", "secom_labels.data"} {
		original, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		compressed, err := Compress(original)
		if err != nil {
			t.Fatalf("%s: compress: %v", name, err)
		}
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("%s: decompress: %v", name, err)
		}
		if !bytes.Equal(got, original) {
			t.Fatalf("%s: round trip mismatch", name)
		}
		again, err := Compress(got)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, compressed) {
			t.Fatalf("%s: encoding is not stable", name)
		}
		t.Logf("%s: %d -> %d bytes", name, len(original), len(compressed))
	}
}

func TestRejectsGarbage(t *testing.T) {
	original, err := os.ReadFile("secom.data")
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := Compress(original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decompress(append(compressed, 0)); err != ErrFormat {
		t.Fatalf("trailing byte: got %v", err)
	}
	if _, err := Decompress(compressed[:len(compressed)-1]); err != ErrFormat {
		t.Fatalf("short: got %v", err)
	}
	flipped := bytes.Clone(compressed)
	flipped[0] ^= 0x01
	if _, err := Decompress(flipped); err != ErrFormat {
		t.Fatalf("kind: got %v", err)
	}

	broken := bytes.Replace(original, []byte("NaN"), []byte("nan"), 1)
	if bytes.Equal(broken, original) {
		t.Fatal("no NaN to corrupt")
	}
	if _, err := Compress(broken); err != ErrFormat {
		t.Fatalf("nan: got %v", err)
	}

	labels, err := os.ReadFile("secom_labels.data")
	if err != nil {
		t.Fatal(err)
	}
	bad := bytes.Replace(labels, []byte("-1 \""), []byte("-2 \""), 1)
	if _, err := Compress(bad); err != ErrFormat {
		t.Fatalf("label: got %v", err)
	}
}

func TestZigzag(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 42, -100000, 148045000, -148045000} {
		if got := unzigzag(zigzag(v)); got != v {
			t.Fatalf("zigzag %d -> %d", v, got)
		}
	}
}
