package main

import (
	"bytes"
	"os"
	"testing"
)

func TestMineMinimizes(t *testing.T) {
	data := []byte("iris-pow")
	const limit = 40
	sizes := make([]int, limit)
	minAt := 0
	for nonce := range limit {
		file, err := residualFile(data, uint32(nonce))
		if err != nil {
			t.Fatal(err)
		}
		sizes[nonce] = len(file)
		if sizes[nonce] < sizes[minAt] {
			minAt = nonce
		}
	}
	got, err := mine(data, sizes[minAt], limit)
	if err != nil {
		t.Fatal(err)
	}
	if int(got.Nonce) != minAt || len(got.File) != sizes[minAt] {
		t.Fatalf("nonce %d size %d, want nonce %d size %d", got.Nonce, len(got.File), minAt, sizes[minAt])
	}
	if err := verify(data, got, sizes[minAt]); err != nil {
		t.Fatal(err)
	}
	if err := verify(data, got, sizes[minAt]-1); err != errWork {
		t.Fatalf("tight target: %v", err)
	}
	flipped := append([]byte{}, data...)
	flipped[0] ^= 0xff
	if err := verify(flipped, got, sizes[minAt]); err != errFormat {
		t.Fatalf("other data: %v", err)
	}
	if _, err := mine(data, sizes[minAt]-1, limit); err != errWork {
		t.Fatalf("unreachable: %v", err)
	}
	if _, err := mine(data, 1, 0); err != errFormat {
		t.Fatalf("limit: %v", err)
	}
}

func TestMineIris(t *testing.T) {
	data, err := os.ReadFile("iris/iris.data")
	if err != nil {
		t.Fatal(err)
	}
	const limit = 16
	got, err := mine(data, 1<<20, limit)
	if err != nil {
		t.Fatal(err)
	}
	best := len(got.File)
	for nonce := range limit {
		file, err := residualFile(data, uint32(nonce))
		if err != nil {
			t.Fatal(err)
		}
		if len(file) < best {
			t.Fatalf("nonce %d size %d beats proof %d", nonce, len(file), best)
		}
	}
	if err := verify(data, got, best); err != nil {
		t.Fatal(err)
	}
	var other []byte
	for nonce := range limit {
		if uint32(nonce) == got.Nonce {
			continue
		}
		other, err = residualFile(data, uint32(nonce))
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if bytes.Equal(other, got.File) {
		t.Fatal("distinct nonces produced the same file")
	}
	t.Logf("iris residuals %d bytes at nonce %d over %d trials", best, got.Nonce, limit)
}
