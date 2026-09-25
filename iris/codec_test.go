package iris

import (
	"bytes"
	"math/big"
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, name := range []string{"iris.data", "bezdekIris.data"} {
		original, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		compressed, err := Compress(original)
		if err != nil {
			t.Fatalf("%s: compress: %v", name, err)
		}
		if len(compressed) != CompressedSize {
			t.Fatalf("%s: compressed %d bytes, want %d", name, len(compressed), CompressedSize)
		}
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("%s: decompress: %v", name, err)
		}
		if !bytes.Equal(got, original) {
			t.Fatalf("%s: round trip mismatch", name)
		}
		t.Logf("%s: %d -> %d bytes", name, len(original), len(compressed))
	}
}

func TestBoundsAreTight(t *testing.T) {
	original, err := os.ReadFile("iris.data")
	if err != nil {
		t.Fatal(err)
	}
	samples, err := parse(original)
	if err != nil {
		t.Fatal(err)
	}
	seenMin := [3][4]bool{}
	seenMax := [3][4]bool{}
	for i, s := range samples {
		c := i / 50
		for j, v := range s {
			if v == mins[c][j] {
				seenMin[c][j] = true
			}
			if v == mins[c][j]+spans[c][j]-1 {
				seenMax[c][j] = true
			}
		}
	}
	for c := range 3 {
		for j := range 4 {
			if !seenMin[c][j] || !seenMax[c][j] {
				t.Errorf("class %d feature %d range is loose", c, j)
			}
		}
	}
}

func TestCorners(t *testing.T) {
	var lo, hi [150][4]int
	for i := range lo {
		c := i / 50
		for j := range 4 {
			lo[i][j] = mins[c][j]
			hi[i][j] = mins[c][j] + spans[c][j] - 1
		}
	}
	for _, samples := range [][150][4]int{lo, hi} {
		file := format(samples)
		compressed, err := Compress(file)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, file) {
			t.Fatal("corner round trip mismatch")
		}
	}
}

func TestRejectsGarbage(t *testing.T) {
	original, err := os.ReadFile("iris.data")
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := Compress(original)
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(compressed)
	flipped[0] |= 0x80
	if _, err := Decompress(flipped); err != ErrFormat {
		t.Fatalf("high bit: got %v", err)
	}
	if _, err := Decompress(compressed[:10]); err != ErrFormat {
		t.Fatalf("short: got %v", err)
	}
	broken := bytes.Clone(original)
	broken[0] = '9'
	if _, err := Compress(broken); err != ErrFormat {
		t.Fatalf("out of range: got %v", err)
	}
}

// CompressedSize must be the smallest byte width of the mixed-radix space.
func TestCompressedSize(t *testing.T) {
	space := big.NewInt(1)
	fifty := big.NewInt(50)
	for _, base := range bases {
		block := new(big.Int).Exp(big.NewInt(int64(base)), fifty, nil)
		space.Mul(space, block)
	}
	space.Sub(space, big.NewInt(1))
	width := (space.BitLen() + 7) / 8
	if width != CompressedSize {
		t.Fatalf("space fits in %d bytes, CompressedSize is %d (bits %d)", width, CompressedSize, space.BitLen())
	}
	if (CompressedSize-1)*8 >= space.BitLen() {
		t.Fatalf("%d bytes also fit %d bits", CompressedSize-1, space.BitLen())
	}
}
