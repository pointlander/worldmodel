package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"os"
	"testing"
)

func TestEvolveRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		{0},
		{1, 1, 1, 1},
		{1, 2, 3},
		{5, 5, 5, 5},
	}
	buf := make([]byte, 32)
	for i := range buf {
		buf[i] = byte(i * 3)
	}
	cases = append(cases, buf)
	for _, src := range cases {
		blob, err := compressEvolve(src)
		if err != nil {
			t.Fatalf("compress %v: %v", src, err)
		}
		got, err := decompressEvolve(blob)
		if err != nil {
			t.Fatalf("decompress %v: %v", src, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("got %v want %v", got, src)
		}
	}
	if _, err := decompressEvolve([]byte{1, 2, 3}); err != errFormat {
		t.Fatalf("short: %v", err)
	}

	src := []byte{9, 8, 7, 6, 5, 4, 3, 2}
	spec, err := NTT(src)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := encodeEvolve(len(src), spec, []int{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decompressEvolve(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("mask got %v", got)
	}
}

func TestEvolveIris(t *testing.T) {
	src, err := os.ReadFile("iris/iris.data")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NTT(src)
	if err != nil {
		t.Fatal(err)
	}
	none, err := encodeEvolve(len(src), spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := encodeEvolve(len(src), spec, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	blob, err := compressEvolve(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decompressEvolve(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("iris round trip")
	}
	if len(blob) > len(none) || len(blob) > len(dc) {
		t.Fatalf("evolve %d none %d dc %d", len(blob), len(none), len(dc))
	}
	k := binary.LittleEndian.Uint32(blob[8:])
	var z bytes.Buffer
	w, err := flate.NewWriter(&z, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(src); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("iris %d bytes, flate %d, evolve %d clearing %d, none %d, dc %d",
		len(src), z.Len(), len(blob), k, len(none), len(dc))
}
