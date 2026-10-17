package main

import (
	"bytes"
	"compress/flate"
	"os"
	"slices"
	"testing"
)

func TestFactor2Compress(t *testing.T) {
	cases := [][]byte{
		nil,
		{0},
		{2, 0, 0, 0},
		{1, 2, 3},
		{2, 2},
	}
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = byte(i)
	}
	cases = append(cases, buf)
	for _, src := range cases {
		blob, err := compressFactor2(src)
		if err != nil {
			t.Fatalf("compress %v: %v", src, err)
		}
		got, err := decompressFactor2(blob)
		if err != nil {
			t.Fatalf("decompress %v: %v", src, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("got %v want %v", got, src)
		}
	}
	if _, err := decompressFactor2([]byte{1, 2, 3}); err != errFormat {
		t.Fatalf("short: %v", err)
	}

	src, err := os.ReadFile("iris/iris.data")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := compressFactor2(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decompressFactor2(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("iris round trip")
	}
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
	t.Logf("iris %d bytes, flate %d, factor2 %d", len(src), z.Len(), len(blob))
}

func TestRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		{0},
		{1},
		{255},
		{1, 2},
		{1, 2, 3},
		{0, 0, 0, 0, 0, 0, 0, 0},
		{1, 0, 0, 0},
	}
	buf := make([]byte, 1000)
	for i := range buf {
		buf[i] = byte(i * 17)
	}
	cases = append(cases, buf)
	wide := make([]byte, 4096)
	for i := range wide {
		wide[i] = byte(i)
	}
	cases = append(cases, wide)

	for _, src := range cases {
		spec, err := NTT(src)
		if err != nil {
			t.Fatalf("len %d: %v", len(src), err)
		}
		if len(src) == 0 {
			if spec != nil {
				t.Fatalf("empty spectrum %#v", spec)
			}
			got, err := INTT(nil)
			if err != nil || got != nil {
				t.Fatalf("empty inverse %v %#v", err, got)
			}
			continue
		}
		got, err := INTT(spec)
		if err != nil {
			t.Fatalf("inverse len %d: %v", len(src), err)
		}
		if len(got) < len(src) || !bytesMatch(got[:len(src)], src) {
			t.Fatalf("len %d: got prefix %v", len(src), got[:min(len(got), 8)])
		}
		for _, v := range got[len(src):] {
			if v != 0 {
				t.Fatalf("len %d: pad %d", len(src), v)
			}
		}
		var sum uint64
		for _, b := range src {
			sum += uint64(b)
		}
		if uint64(spec[0]) != sum%mod {
			t.Fatalf("len %d: X0 %d want %d", len(src), spec[0], sum%mod)
		}
	}
}

func TestKnown(t *testing.T) {
	spec, err := NTT([]byte{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	// X0 = 1+2, X1 = 1+2*(-1).
	want := []uint32{3, mod - 1}
	if !same(spec, want) {
		t.Fatalf("got %v", spec)
	}
	impulse, err := NTT([]byte{1, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range impulse {
		if v != 1 {
			t.Fatalf("impulse[%d] = %d", i, v)
		}
	}
}

func TestMatchesNaive(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16, 32} {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i*3 + 1)
		}
		got, err := NTT(src)
		if err != nil {
			t.Fatal(err)
		}
		if !same(got, naive(src)) {
			t.Fatalf("n=%d got %v want %v", n, got, naive(src))
		}
	}
}

func TestCyclicConvolution(t *testing.T) {
	a := []byte{1, 2, 3, 0}
	b := []byte{4, 5, 0, 0}
	want := []uint32{4, 13, 22, 15}
	if got := conv(a, b); !same(got, want) {
		t.Fatalf("small conv %v", got)
	}

	n := 256
	x := make([]byte, n)
	y := make([]byte, n)
	for i := range x {
		x[i] = byte(i * 7)
		y[i] = byte(255 - i)
	}
	got := conv(x, y)
	for i := 0; i < n; i++ {
		var s uint64
		for j := 0; j < n; j++ {
			s += uint64(x[j]) * uint64(y[(i-j+n)%n])
		}
		if uint64(got[i]) != s%mod {
			t.Fatalf("conv[%d] = %d want %d", i, got[i], s%mod)
		}
	}
}

func TestRootOfUnity(t *testing.T) {
	if modPow(root, (mod-1)/2) != mod-1 {
		t.Fatal("3 is not a primitive 2nd root generator")
	}
	w := modPow(root, (mod-1)/uint64(maxLen))
	if modPow(w, maxLen) != 1 || modPow(w, maxLen/2) != mod-1 {
		t.Fatal("principal 2^23 root")
	}
}

func TestRejects(t *testing.T) {
	if _, err := NTT(make([]byte, maxLen+1)); err != errFormat {
		t.Fatalf("long: %v", err)
	}
	if _, err := INTT([]uint32{1, 2, 3}); err != errFormat {
		t.Fatalf("odd: %v", err)
	}
	if _, err := INTT([]uint32{mod}); err != errFormat {
		t.Fatalf("residue: %v", err)
	}
	got, err := INTT([]uint32{mod - 1})
	if err != nil || len(got) != 1 || got[0] != mod-1 {
		t.Fatalf("mod-1: %v %v", err, got)
	}
}

func TestIrisFile(t *testing.T) {
	src, err := os.ReadFile("iris/iris.data")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NTT(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec) != 8192 {
		t.Fatalf("spectrum %d", len(spec))
	}
	got, err := INTT(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesMatch(got[:len(src)], src) {
		t.Fatal("iris round trip")
	}
}

func conv(a, b []byte) []uint32 {
	if len(a) != len(b) {
		panic("len")
	}
	af, err := NTT(a)
	if err != nil {
		panic(err)
	}
	bf, err := NTT(b)
	if err != nil {
		panic(err)
	}
	c := make([]uint32, len(af))
	for i := range c {
		c[i] = uint32(uint64(af[i]) * uint64(bf[i]) % mod)
	}
	out, err := INTT(c)
	if err != nil {
		panic(err)
	}
	return out
}

func naive(src []byte) []uint32 {
	n := len(src)
	out := make([]uint32, n)
	if n == 0 {
		return out
	}
	w := modPow(root, (mod-1)/uint64(n))
	for k := 0; k < n; k++ {
		var sum uint64
		wk := uint64(1)
		step := modPow(w, uint64(k))
		for _, b := range src {
			sum = (sum + uint64(b)*wk) % mod
			wk = wk * step % mod
		}
		out[k] = uint32(sum)
	}
	return out
}

func bytesMatch(got []uint32, src []byte) bool {
	if len(got) != len(src) {
		return false
	}
	for i, b := range src {
		if got[i] != uint32(b) {
			return false
		}
	}
	return true
}

func same(a, b []uint32) bool {
	return slices.Equal(a, b)
}
