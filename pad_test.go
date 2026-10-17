package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"os"
	"testing"
)

func TestPadRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		{0},
		{5},
		{1, 2, 3},
		{1, 1, 1, 1},
		{9, 8, 7, 6, 5, 4, 3, 2},
	}
	buf := make([]byte, 32)
	for i := range buf {
		buf[i] = byte(i * 3)
	}
	cases = append(cases, buf)
	for _, src := range cases {
		blob, err := compressPad(src)
		if err != nil {
			t.Fatalf("compress %v: %v", src, err)
		}
		got, err := decompressPad(blob)
		if err != nil {
			t.Fatalf("decompress %v: %v", src, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("got %v want %v", got, src)
		}
	}
	if _, err := decompressPad([]byte{1, 2, 3}); err != errFormat {
		t.Fatalf("short: %v", err)
	}

	spec, err := NTT([]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	ext, err := padEnds(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 8 || ext[0] != 0 || ext[1] != 0 || ext[6] != 0 || ext[7] != 0 {
		t.Fatalf("wings %v", ext)
	}
}

func TestPadIris(t *testing.T) {
	src, err := os.ReadFile("iris/iris.data")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := compressPad(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decompressPad(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("iris round trip")
	}
	n := int(binary.LittleEndian.Uint32(blob[4:]))
	m := int(binary.LittleEndian.Uint32(blob[8:]))
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
	t.Logf("iris %d bytes, flate %d, pad %d, zeros %d + %d",
		len(src), z.Len(), len(blob), (m-n)/2, m-n-(m-n)/2)
}
